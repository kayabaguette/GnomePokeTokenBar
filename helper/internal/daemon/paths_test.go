package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"poketokenbar/helper/internal/claude"
	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/limits"
	"poketokenbar/helper/internal/usage"
)

// usage.md §6.2: a failed scan keeps the previous data, stops lastUpdated and shows usageScan.
func TestUsageScanFailureKeepsData(t *testing.T) {
	h := newHarness(t, "")
	h.start()
	h.sendSettings(1, false)
	fresh := h.expect("fresh", wait, isFreshState)
	updated := fresh.str("meta", "lastUpdated")
	time.Sleep(1100 * time.Millisecond) // lastUpdated has whole seconds
	h.usage.setErr(errors.New("disk on fire"))
	h.usage.set(5_000_000)
	h.send(`{"id":2,"cmd":"refresh"}`)
	h.expect("reply", wait, isResult(2))
	st := h.lastState()
	errs, _ := st.path("meta", "errors").([]any)
	if len(errs) != 1 || errs[0].(map[string]any)["code"] != "usageScan" || errs[0].(map[string]any)["providerId"] != "claude_code" {
		t.Fatalf("meta.errors = %v", errs)
	}
	if st.num("usage", "combined", "today", "tokens") != 1_000_000 || st.str("meta", "lastUpdated") != updated {
		t.Fatalf("a failed scan must keep the data and the clock: today=%v lastUpdated=%v", st.path("usage", "combined", "today", "tokens"), st.path("meta", "lastUpdated"))
	}
	h.usage.setErr(nil)
	h.send(`{"id":3,"cmd":"refresh"}`)
	h.expect("reply", wait, isResult(3))
	st = h.lastState()
	if errs, _ := st.path("meta", "errors").([]any); len(errs) != 0 || st.num("usage", "combined", "today", "tokens") != 5_000_000 ||
		st.str("meta", "lastUpdated") == updated {
		t.Fatalf("recovery: errors=%v today=%v", errs, st.path("usage", "combined", "today", "tokens"))
	}
	h.stop()
}

// Protocol §2.1 step 6: without setSettings the first scan starts after SettingsWait with the
// persisted (here default) settings.
func TestSettingsWaitTimeout(t *testing.T) {
	h := newHarness(t, "")
	h.cfg.SettingsWait = 100 * time.Millisecond
	h.start()
	st := h.expect("fresh without setSettings", wait, isFreshState)
	if st.num("settingsApplied", "refreshInterval") != 120 || st.num("settingsApplied", "weekStart") != 1 {
		t.Fatalf("settingsApplied = %v", st["settingsApplied"])
	}
	h.stop()
}

// The active Pokémon's names, line and types are loaded before the first state.
func TestPreWarmNamesInFirstState(t *testing.T) {
	h := newHarness(t, "")
	writeFile(t, filepath.Join(h.cfg.StateDir, "companion-state.json"), `{"installBaselineSet":true,"lastDate":"2026-01-01",
		"claimedTodayTokensByProvider":{},"active":{"baseID":16,"pathIDs":[16,17],"plannedPathIDs":[16,17,18],
		"stageIndex":1,"usedAtStage":1000,"rarity":"common","totalForms":3,"nature":"adamant"}}`)
	h.start()
	h.expect("hello", wait, func(m msg) bool { return m.typ() == "hello" })
	st, _ := h.next(wait)
	line, _ := st.path("companion", "active", "evoLine").([]any)
	types, _ := st.path("companion", "active", "types").([]any)
	if !st.flag("meta", "fromCache") || st.str("companion", "active", "displayName") != "Mon17" || len(line) != 3 || len(types) != 2 {
		t.Fatalf("first state companion = %v", st.path("companion", "active"))
	}
	h.stop()
}

type panicUsage struct{ *fakeUsage }

func (panicUsage) Scan(time.Time, time.Weekday) (usage.Enrichment, error) { panic("boom") }

// Protocol §3: a recovered panic is `fatal internal`, exit 70.
func TestPanicIsFatalInternal(t *testing.T) {
	h := newHarness(t, "")
	h.cfg.Providers = []UsageSource{panicUsage{h.usage}}
	h.start()
	h.sendSettings(1, false)
	m := h.expect("fatal", wait, func(m msg) bool { return m.typ() == "fatal" })
	if m.str("code") != "internal" {
		t.Fatalf("fatal = %v", m)
	}
	if code := h.exitCode(); code != ExitInternal {
		t.Fatalf("exit %d", code)
	}
}

// usage.md §7.1: a 429 starts the backoff; automatic polls skip the endpoint, a manual one does not.
func TestRateLimitBackoff(t *testing.T) {
	fl := newFakeLimits(t, 10, 10)
	fl.setStatus(429)
	h := newHarness(t, "")
	h.cfg.Limits = []LimitsSpec{fl.spec()}
	h.start()
	h.sendSettings(1, false)
	st := h.expect("fresh", wait, isFreshState)
	p := func(k string) any { return st.path("limits", "providers", "claude_code", k) }
	if p("errorCode") != "rateLimited" || p("httpStatus") != float64(429) || p("authState") != "error" || p("backoffUntil") == nil ||
		p("available") != false {
		t.Fatalf("limits = %v", st.path("limits", "providers", "claude_code"))
	}
	if until, _ := time.Parse(time.RFC3339, p("backoffUntil").(string)); until.Sub(time.Now()) > 125*time.Second || until.Sub(time.Now()) < 100*time.Second {
		t.Fatalf("Retry-After 120 s not honoured: %v", until)
	}
	h.send(`{"id":2,"cmd":"refresh"}`)
	h.expect("automatic", wait, isResult(2))
	if n := fl.callCount(); n != 1 {
		t.Fatalf("automatic refresh during backoff called the endpoint (%d calls)", n)
	}
	fl.setStatus(200)
	h.send(`{"id":3,"cmd":"refresh","args":{"manual":true}}`)
	h.expect("manual", wait, isResult(3))
	st = h.lastState()
	if fl.callCount() != 2 || st.path("limits", "providers", "claude_code", "backoffUntil") != nil ||
		!st.flag("limits", "providers", "claude_code", "available") {
		t.Fatalf("manual refresh must bypass and clear the backoff: %v", st.path("limits", "providers", "claude_code"))
	}
	h.stop()
}

// Protocol §6 `error`: PokéAPI being unreachable is reported once until it answers again.
func TestPokeAPIUnreachableEvent(t *testing.T) {
	h := newHarness(t, "")
	h.poke.offline.Store(true)
	h.start()
	h.sendSettings(1, false)
	ev := h.expect("error event", wait, isEvent("error"))
	if ev.str("code") != "pokeapiUnreachable" || ev.str("message") == "" {
		t.Fatalf("event = %v", ev)
	}
	h.send(`{"id":2,"cmd":"refresh"}`)
	h.expect("reply", wait, isResult(2))
	h.quiet(300*time.Millisecond, "a second error event", isEvent("error"))
	h.stop()
}

// Protocol §2.3/§8.2: a menu sprite that vanished from disk shows loading and is loaded again.
func TestMenuSpriteReloadedWhenDeleted(t *testing.T) {
	fs := newFakeSprites(t)
	h := newHarness(t, "")
	h.cfg.Sprites = fs.store(h.cfg.CacheDir)
	h.start()
	h.sendSettings(1, false)
	ready := func(m msg) bool {
		return m.typ() == "state" && m.str("companion", "menuSubject", "sprite", "status") == "ready"
	}
	st := h.expect("egg ready", wait, ready)
	path := st.str("companion", "menuSubject", "sprite", "manifestPath")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	h.send(`{"id":2,"cmd":"setRepresentative","args":{"speciesId":null}}`)
	h.expect("loading after removal", wait, func(m msg) bool {
		return m.typ() == "state" && m.str("companion", "menuSubject", "sprite", "status") == "loading" &&
			m.path("companion", "menuSubject", "sprite", "manifestPath") == nil
	})
	h.expect("ready again", wait, ready)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("not reloaded: %v", err)
	}
	h.stop()
}

// The local-midnight timer refreshes and rolls the calendar over without any command.
func TestMidnightTimerRollsOver(t *testing.T) {
	realNow := time.Now()
	y, m, d := realNow.Date()
	midnight := time.Date(y, m, d+1, 0, 0, 0, 0, time.Local)
	// 2 s before midnight: enough margin for the first scan under `go test ./...` load (a 700 ms
	// margin was flaky — the first fresh state could already land after midnight).
	offset := midnight.Add(-2 * time.Second).Sub(realNow)
	h := newHarness(t, "")
	h.cfg.Now = func() time.Time { return time.Now().Add(offset) }
	h.start()
	h.sendSettings(1, false)
	before := h.expect("fresh", wait, isFreshState).str("usage", "calendar", "today")
	after := h.expect("next day", wait, func(m msg) bool {
		return isFreshState(m) && m.str("usage", "calendar", "today") != before
	})
	if got := after.str("usage", "calendar", "today"); got != usage.DayKey(midnight) {
		t.Fatalf("calendar %s, want %s", got, usage.DayKey(midnight))
	}
	// The rollover state can be a rebuild that lands just before the timer's rescan is counted:
	// wait for the rescan instead of sampling the counter once.
	deadline := time.Now().Add(wait)
	for h.usage.scanCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("midnight timer did not rescan: %d scans", h.usage.scanCount())
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.stop()
}

// The refresh interval timer requests a refresh and re-arms; manual mode disarms it.
func TestIntervalTimer(t *testing.T) {
	u := newFakeUsage(10)
	cfg := Config{StateDir: t.TempDir(), Providers: []UsageSource{u}, Data: newFakePoke(), Logf: t.Logf}.withDefaults()
	cfg.CacheDir = filepath.Join(cfg.StateDir, "cache")
	d, err := open(cfg, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		d.cancel()
		d.closeQuit()
		for name := range d.timers {
			d.disarm(name)
		}
		d.wg.Wait()
	}()
	d.started = true
	d.settings.RefreshInterval = 60
	d.armInterval()
	if d.timers["interval"] == nil {
		t.Fatal("interval timer not armed")
	}
	d.onTimer("interval")
	waitFor(t, func() bool { return u.scanCount() == 1 })
	if d.timers["interval"] == nil || d.cycle == nil {
		t.Fatal("the timer must start a cycle and re-arm")
	}
	d.settings.RefreshInterval = 0
	d.armInterval()
	if d.timers["interval"] != nil {
		t.Fatal("manual mode must disarm the interval timer")
	}
}

// The Claude adapter derives today and the series from one scan of the transcripts.
func TestClaudeSource(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	line := fmt.Sprintf(`{"type":"assistant","requestId":"r1","timestamp":%q,"message":{"id":"m1","model":"claude-sonnet-5","usage":{"input_tokens":10,"output_tokens":7,"cache_creation_input_tokens":5,"cache_read_input_tokens":100}}}`,
		now.UTC().Format(time.RFC3339))
	writeFile(t, filepath.Join(root, "proj", "session-1.jsonl"), line+"\n")
	src := ClaudeSource{P: claude.New(claude.Options{Roots: []string{root}, CacheDir: t.TempDir()})}
	if src.ID() != "claude_code" || src.DisplayName() != "Claude Code" || src.ShortName() != "Claude" || !src.ReportsCost() {
		t.Fatal("identity")
	}
	e, err := src.Scan(now, time.Monday)
	if err != nil || e.RecentDaily[len(e.RecentDaily)-1].Tokens != 122 || e.ActiveBlock == nil || e.ActiveBlock.Tokens != 122 {
		t.Fatalf("scan = %+v, %v", e.RecentDaily[len(e.RecentDaily)-1], err)
	}
	if err := src.Flush(); err != nil {
		t.Fatal(err)
	}
}

// usage.md §4: the forecast needs the 5 h window with a future reset; at 100 % it is now.
func TestForecastPreconditions(t *testing.T) {
	now := time.Now()
	parse := func(s string) *limits.Status {
		st, err := limits.ParseStatus([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	block := &blockState{Start: now.Add(-time.Hour), End: now.Add(4 * time.Hour), Tokens: 60_000_000, TokensPerMinute: 1_000_000}
	if forecast(parse(`{"five_hour":null,"seven_day":{"utilization":50}}`), block, now) != nil {
		t.Error("no 5 h window")
	}
	past := now.Add(-time.Minute).UTC().Format(time.RFC3339)
	if forecast(parse(`{"five_hour":{"utilization":50,"resets_at":"`+past+`"}}`), block, now) != nil {
		t.Error("reset in the past")
	}
	future := now.Add(time.Hour).UTC().Format(time.RFC3339)
	if f := forecast(parse(`{"five_hour":{"utilization":100,"resets_at":"`+future+`"}}`), nil, now); f == nil || !f.BeforeReset || f.DepletionAt != ts(now) {
		t.Errorf("at 100 %%: %+v", f)
	}
	// 60M tokens for 40 % → 1.5M per percent; 60 % left at 1M/min is 90 min: before a 3 h reset,
	// after a 1 h one.
	later := now.Add(3 * time.Hour).UTC().Format(time.RFC3339)
	if f := forecast(parse(`{"five_hour":{"utilization":40,"resets_at":"`+later+`"}}`), block, now); f == nil || !f.BeforeReset ||
		f.DepletionAt != ts(now.Add(90*time.Minute)) {
		t.Errorf("fast burn, late reset: %+v", f)
	}
	if f := forecast(parse(`{"five_hour":{"utilization":40,"resets_at":"`+future+`"}}`), block, now); f == nil || f.BeforeReset {
		t.Errorf("fast burn, early reset: %+v", f)
	}
	if f := forecast(parse(`{"five_hour":{"utilization":40,"resets_at":"`+future+`"}}`), nil, now); f != nil {
		t.Errorf("no block: %+v", f)
	}
}

// A getSprite for a species with no sprite anywhere is `unavailable`.
func TestGetSpriteUnavailable(t *testing.T) {
	fs := newFakeSprites(t)
	fs.srv.Close() // offline and uncached
	h := newHarness(t, "")
	h.cfg.Sprites = fs.store(h.cfg.CacheDir)
	h.start()
	h.send(`{"id":1,"cmd":"getSprite","args":{"speciesId":25,"shiny":true}}`)
	r := h.expect("reply", wait, isResult(1))
	if e, _ := r["error"].(map[string]any); e == nil || e["code"] != "unavailable" {
		t.Fatalf("getSprite offline = %v", r)
	}
	h.send(`{"id":2,"cmd":"getSprite","args":{"egg":true}}`)
	if r := h.expect("egg reply", wait, isResult(2)); r.flag("ok") {
		t.Fatalf("egg offline = %v", r)
	}
	h.stop()
}

// Protocol §2.1 step 6 and §4: growth is rescaled only when a received growthDifficulty differs
// from the persisted value; a restart never rescales from defaults.
func TestGrowthDifficultyRescaleRules(t *testing.T) {
	h := newHarness(t, "")
	h.start()
	h.send(`{"id":1,"cmd":"setSettings","args":{"refreshInterval":0,"warnThreshold":80,"critThreshold":95,"growthDifficulty":2.5,"weekStart":1,"language":"en","alwaysFacesBack":false}}`)
	if r := h.expect("reply", wait, isResult(1)); r.num("data", "growthDifficulty") != 2 {
		t.Fatalf("clamped difficulty = %v", r["data"])
	}
	st := h.expect("fresh", wait, isFreshState)
	if st.num("companion", "egg", "threshold") != 10_000_000 {
		t.Fatalf("egg threshold = %v", st.path("companion", "egg"))
	}
	h.stop()
	if err := os.Remove(filepath.Join(h.cfg.StateDir, settingsFile)); err != nil {
		t.Fatal(err)
	}
	h2 := newHarness(t, h.cfg.StateDir)
	h2.cfg.SettingsWait = 50 * time.Millisecond
	h2.start()
	st = h2.expect("fresh without setSettings", wait, isFreshState)
	if st.num("settingsApplied", "growthDifficulty") != 2 || st.num("companion", "egg", "threshold") != 10_000_000 {
		t.Fatalf("restart must keep the save's difficulty: %v / %v", st["settingsApplied"], st.path("companion", "egg"))
	}
	h2.stop()
}

// getSpecies answers before its deadline when PokéAPI is down: details unavailable, page still served.
func TestGetSpeciesOffline(t *testing.T) {
	h := newHarness(t, "")
	h.poke.offline.Store(true)
	h.start()
	start := time.Now()
	h.send(`{"id":1,"cmd":"getSpecies","args":{"speciesId":25}}`)
	r := h.expect("reply", wait, isResult(1))
	if !r.flag("ok") || r.str("data", "detailsStatus") != "unavailable" || r.path("data", "details") != nil ||
		r.flag("data", "owned") || r.str("data", "displayName") != "#25" || time.Since(start) > 2*time.Second {
		t.Fatalf("offline getSpecies after %s: %v", time.Since(start), r["data"])
	}
	h.stop()
}

// Update sees no usage before this process has scanned: a last-snapshot.json older than the
// save's ledger must not rebase it down (which would credit the gap twice after the scan).
func TestNoCreditFromRestoredUsageBeforeScan(t *testing.T) {
	h := newHarness(t, "")
	today := usage.DayKey(time.Now())
	writeFile(t, filepath.Join(h.cfg.StateDir, "companion-state.json"), `{"installBaselineSet":true,"eggUsage":0,
		"lastDate":"`+today+`","claimedTodayTokensByProvider":{"claude_code":120}}`)
	writeFile(t, filepath.Join(h.cfg.StateDir, snapshotFile), `{"version":1,"usage":{"claude_code":{"days":{"`+today+`":{"cr":100}},"scanned":true}},"limits":{}}`)
	h.usage.set(125)
	h.start()
	h.send(`{"id":1,"cmd":"networkUp"}`)
	h.expect("networkUp reply", wait, isResult(1))
	h.sendSettings(2, false)
	st := h.expect("fresh", wait, isFreshState)
	if st.num("companion", "egg", "used") != 5 {
		t.Fatalf("egg credited %v, want 5 (125 − 120)", st.path("companion", "egg", "used"))
	}
	h.stop()
}

// A fetch cut short by its deadline is not a failure: the next pass may request it again.
func TestDeadlineCutFetchIsNotCooledDown(t *testing.T) {
	src := &slowPoke{fakePoke: newFakePoke(16)}
	c := newDataCache(src, time.Now)
	v := c.view()
	_, _ = v.Details(context.Background(), 16)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if got, failed := c.warm(ctx, c.takeWants()); got != 0 || failed != 1 {
		t.Fatalf("warm got=%d failed=%d", got, failed)
	}
	_, _ = v.Details(context.Background(), 16)
	if w := c.takeWants(); len(w) != 1 {
		t.Fatalf("a deadline-cut key must be wanted again: %v", w)
	}
}

// slowPoke's Details waits for its context.
type slowPoke struct{ *fakePoke }

func (s *slowPoke) Details(ctx context.Context, id int) (*companion.Details, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// gatedPoke's Line waits for a release, so a warm can be held in flight.
type gatedPoke struct {
	*fakePoke
	gate  chan struct{}
	calls atomic.Int32
}

func (g *gatedPoke) Line(ctx context.Context, base int) (*companion.Line, error) {
	g.calls.Add(1)
	<-g.gate
	return g.fakePoke.Line(ctx, base)
}

// A warm requested while one runs does not start a second one; it reruns Update afterwards.
func TestWarmWhileBusyReruns(t *testing.T) {
	g := &gatedPoke{fakePoke: newFakePoke(16), gate: make(chan struct{})}
	cfg := Config{StateDir: t.TempDir(), Data: g, Logf: t.Logf}.withDefaults()
	cfg.CacheDir = filepath.Join(cfg.StateDir, "cache")
	d, err := open(cfg, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		d.cancel()
		d.closeQuit()
		d.wg.Wait()
	}()
	d.startWarm([]want{{kind: wantLine, id: 16}})
	waitFor(t, func() bool { return g.calls.Load() == 1 })
	d.startWarm([]want{{kind: wantLine, id: 19}})
	if !d.warmBusy || !d.warmAgain || g.calls.Load() != 1 {
		t.Fatalf("busy=%t again=%t calls=%d", d.warmBusy, d.warmAgain, g.calls.Load())
	}
	close(g.gate)
	m := (<-d.inbox).(warmMsg)
	d.onWarm(m)
	if d.warmAgain || d.warmPasses != 1 {
		t.Fatalf("after the warm: again=%t passes=%d", d.warmAgain, d.warmPasses)
	}
}
