package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/ipc"
)

const wait = 5 * time.Second

// Protocol §2: hello → cached state (fromCache, sent even on first run) → setSettings result →
// fresh state; EOF flushes and exits 0 within 2 s; the next start serves the persisted inputs.
func TestLifecycleHelloCachedThenFresh(t *testing.T) {
	h := newHarness(t, "")
	h.start()
	hello, _ := h.next(wait)
	if hello.typ() != "hello" || hello.num("protocol") != 1 || hello.str("helperVersion") != "1.2.3" ||
		int(hello.num("pid")) != os.Getpid() || len(hello) != 4 {
		t.Fatalf("hello = %v", hello)
	}
	first, _ := h.next(wait)
	if first.typ() != "state" || !first.flag("meta", "fromCache") || first.flag("meta", "refreshing") ||
		first.path("meta", "lastUpdated") != nil || !first.flag("meta", "isStale") {
		t.Fatalf("first state meta = %v", first["meta"])
	}
	if first.num("usage", "combined", "today", "tokens") != 0 || first.str("usage", "combined", "today", "cost", "coverage") != "empty" ||
		len(first.path("usage", "combined", "recentDaily", "tokens").([]any)) != 21 || first.str("companion", "phase") != "egg" {
		t.Fatalf("first-run state not zero-filled: %v", first["usage"])
	}

	h.sendSettings(1, false)
	res := h.expect("setSettings result", wait, isResult(1))
	if !res.flag("ok") || res.num("data", "refreshInterval") != 0 || res.str("data", "language") != "en" {
		t.Fatalf("setSettings result = %v", res)
	}
	fresh := h.expect("fresh state", wait, isFreshState)
	if fresh.num("usage", "combined", "today", "tokens") != 1_000_000 || fresh.path("meta", "lastUpdated") == nil ||
		fresh.num("usage", "providers", "claude_code", "today", "tokens") != 1_000_000 {
		t.Fatalf("fresh usage = %v", fresh["usage"])
	}
	lastUpdated := fresh.str("meta", "lastUpdated")

	code, took := h.stop()
	if code != 0 || took > 2*time.Second {
		t.Fatalf("exit %d after %s", code, took)
	}
	if h.usage.flushCount() == 0 {
		t.Fatal("usage cache not flushed")
	}
	if _, err := os.Stat(filepath.Join(h.cfg.StateDir, snapshotFile)); err != nil {
		t.Fatalf("last-snapshot.json not written: %v", err)
	}

	h2 := newHarness(t, h.cfg.StateDir)
	h2.usage.set(0)
	h2.start()
	h2.expect("hello", wait, func(m msg) bool { return m.typ() == "hello" })
	cached, _ := h2.next(wait)
	if !cached.flag("meta", "fromCache") || cached.str("meta", "lastUpdated") != lastUpdated ||
		cached.num("usage", "combined", "today", "tokens") != 1_000_000 {
		t.Fatalf("cached state after restart: meta=%v today=%v", cached["meta"], cached.path("usage", "combined", "today"))
	}
	if code, _ := h2.stop(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

// Protocol §2.1 step 2: a second instance waits LockWait, then reports alreadyRunning (exit 3);
// once the holder is gone, a waiting instance takes over.
func TestLockContention(t *testing.T) {
	dir := t.TempDir()
	holder, err := acquireLock(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, dir)
	h.cfg.LockWait = 250 * time.Millisecond
	h.start()
	m, _ := h.next(wait)
	if m.typ() != "fatal" || m.str("code") != "alreadyRunning" || !strings.Contains(m.str("message"), fmt.Sprint(os.Getpid())) {
		t.Fatalf("want fatal alreadyRunning first, got %v", m)
	}
	if code := h.exitCode(); code != ExitRunning {
		t.Fatalf("exit %d, want %d", code, ExitRunning)
	}

	h2 := newHarness(t, dir)
	h2.cfg.LockWait = 3 * time.Second
	h2.start()
	time.Sleep(300 * time.Millisecond)
	holder.Close()
	if m := h2.expect("hello after the holder exits", wait, func(m msg) bool { return m.typ() != "" }); m.typ() != "hello" {
		t.Fatalf("got %v", m)
	}
	h2.stop()
}

// Protocol §4/§11: routing, error codes, ids and malformed input.
func TestCommandRoutingAndErrors(t *testing.T) {
	h := newHarness(t, "")
	h.start()
	h.sendSettings(1, false)
	h.expect("fresh state", wait, isFreshState)

	h.send(`not json`)
	h.send(`[1,2]`)
	h.send(`{"cmd":"ping"}`)
	h.send(`{"id":"7","cmd":"ping"}`)
	h.send(`{"id":7.0,"cmd":"ping"}`)
	h.send(`{"id":0,"cmd":"ping"}`)
	h.send(`{"id":-3,"cmd":"ping"}`)
	h.send(`{"id":9007199254740992,"cmd":"ping"}`)
	h.send(`{"id":2,"cmd":"ping","x":` + strings.Repeat(`"y",`, ipc.MaxLine/4) + `1}`) // over 1 MiB: skipped
	h.send(`{"id":3,"cmd":"ping"}`)
	if r := h.expect("ping", wait, func(m msg) bool { return m.typ() == "result" }); r.id() != 3 || !r.flag("ok") ||
		len(r["data"].(map[string]any)) != 0 {
		t.Fatalf("the first reply must be ping 3 with {}: %v", r)
	}

	cases := []struct {
		line, code string
	}{
		{`{"id":10,"cmd":"bogus"}`, "unknownCommand"},
		{`{"id":11}`, "unknownCommand"},
		{`{"id":12,"cmd":5}`, "unknownCommand"},
		{`{"id":13,"cmd":"setSettings","args":{"refreshInterval":60}}`, "badArgs"},
		{`{"id":14,"cmd":"setSettings","args":{"refreshInterval":60,"warnThreshold":80,"critThreshold":95,"growthDifficulty":1,"weekStart":8,"language":"en","alwaysFacesBack":false}}`, "badArgs"},
		{`{"id":15,"cmd":"setSettings","args":{"refreshInterval":"60","warnThreshold":80,"critThreshold":95,"growthDifficulty":1,"weekStart":1,"language":"en","alwaysFacesBack":false}}`, "badArgs"},
		{`{"id":16,"cmd":"getDexPage","args":{"pageSize":65}}`, "badArgs"},
		{`{"id":17,"cmd":"getDexPage","args":{"rarity":"mythic"}}`, "badArgs"},
		{`{"id":18,"cmd":"getCatchLog","args":{"limit":0}}`, "badArgs"},
		{`{"id":19,"cmd":"getSpecies","args":{"speciesId":650}}`, "badArgs"},
		{`{"id":20,"cmd":"refresh","args":[1]}`, "badArgs"},
		{`{"id":21,"cmd":"setRepresentative","args":{"speciesId":25}}`, "notOwned"},
		{`{"id":22,"cmd":"setRepresentative","args":{}}`, "badArgs"},
		{`{"id":23,"cmd":"getSprite","args":{"speciesId":0}}`, "badArgs"},
		{`{"id":24,"cmd":"getSprite","args":{"speciesId":25}}`, "unavailable"}, // no sprite store configured
	}
	for _, c := range cases {
		h.send(c.line)
	}
	for _, c := range cases {
		var id int64
		fmt.Sscanf(c.line, `{"id":%d`, &id)
		r := h.expect(c.line, wait, isResult(id))
		e, _ := r["error"].(map[string]any)
		if r.flag("ok") || e == nil || e["code"] != c.code || e["message"] == "" {
			t.Errorf("%s → %v, want %s", c.line, r, c.code)
		}
	}

	h.send(`{"id":30,"cmd":"wake"}`)
	h.send(`{"id":31,"cmd":"networkUp"}`)
	h.send(`{"id":32,"cmd":"getDexPage"}`)
	h.send(`{"id":33,"cmd":"getCatchLog","args":{"sort":"oldestFirst","unknownArg":true}}`)
	h.send(`{"id":34,"cmd":"setRepresentative","args":{"speciesId":null}}`)
	for id := int64(30); id <= 34; id++ {
		if r := h.expect("reply", wait, isResult(id)); !r.flag("ok") {
			t.Errorf("id %d: %v", id, r)
		}
	}
	if code, _ := h.stop(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

// usage.md §6.1: refreshes requested during a running cycle coalesce into exactly one rerun, and
// their replies come after the rerun's fresh state.
func TestRefreshCoalescing(t *testing.T) {
	h := newHarness(t, "")
	gate := make(chan struct{})
	h.usage.gate = gate
	h.start()
	h.sendSettings(1, false)
	h.expect("setSettings result", wait, isResult(1))
	waitFor(t, func() bool { return h.usage.scanCount() == 1 })
	for id := 10; id <= 12; id++ {
		h.send(fmt.Sprintf(`{"id":%d,"cmd":"refresh","args":{"manual":%t}}`, id, id == 11))
	}
	h.send(`{"id":13,"cmd":"ping"}`)
	h.expect("ping during the scan", wait, isResult(13))
	h.usage.set(2_000_000)
	gate <- struct{}{} // first cycle
	waitFor(t, func() bool { return h.usage.scanCount() == 2 })
	h.usage.set(3_000_000)
	gate <- struct{}{} // the single rerun
	var replies []int64
	sawRerunState := false
	for len(replies) < 3 {
		m, ok := h.next(wait)
		if !ok {
			t.Fatalf("missing refresh replies; got %v", replies)
		}
		if isFreshState(m) && m.num("usage", "combined", "today", "tokens") == 3_000_000 {
			sawRerunState = true
		}
		if m.typ() == "result" {
			if !sawRerunState {
				t.Fatalf("reply %d before the rerun's fresh state", m.id())
			}
			replies = append(replies, m.id())
		}
	}
	close(gate)
	time.Sleep(150 * time.Millisecond)
	if n := h.usage.scanCount(); n != 2 {
		t.Fatalf("%d scans, want 2 (one cycle plus one coalesced rerun)", n)
	}
	h.stop()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// usage.md §6.1: no usage and no error → one retry after EmptyRetry, and the retry schedules none.
func TestEmptyRetryOnce(t *testing.T) {
	h := newHarness(t, "")
	h.usage.set(0)
	h.cfg.EmptyRetry = 80 * time.Millisecond
	h.start()
	h.sendSettings(1, false)
	waitFor(t, func() bool { return h.usage.scanCount() == 2 })
	time.Sleep(300 * time.Millisecond)
	if n := h.usage.scanCount(); n != 2 {
		t.Fatalf("%d scans, want 2", n)
	}
	h.stop()
}

// Protocol §2.4: the periodic rebuild emits a state only when something besides generatedAt changed.
func TestTickEmitsOnlyOnChange(t *testing.T) {
	h := newHarness(t, "")
	h.cfg.Tick = 20 * time.Millisecond
	h.start()
	h.sendSettings(1, false)
	h.expect("fresh state", wait, isFreshState)
	h.quiet(300*time.Millisecond, "state without change", func(m msg) bool { return m.typ() == "state" })
	h.stop()
}

// Protocol §6 and §14.1: limitAlert is edge-triggered per window and the tiers survive a restart.
func TestLimitAlertTiersPersistAcrossRestart(t *testing.T) {
	fl := newFakeLimits(t, 10, 83)
	h := newHarness(t, "")
	h.cfg.Limits = []LimitsSpec{fl.spec()}
	h.start()
	h.sendSettings(1, false)
	ev := h.expect("limitAlert", wait, isEvent("limitAlert"))
	if ev.str("windowKey") != "claude.sevenDay" || ev.str("tier") != "warn" || ev.str("providerId") != "claude_code" ||
		ev.str("windowKind") != "sevenDay" || ev.path("label") != nil || ev.num("utilization") != 83 {
		t.Fatalf("event = %v", ev)
	}
	before := h.seen[len(h.seen)-2]
	if before.typ() != "state" || !before.flag("limits", "providers", "claude_code", "available") {
		t.Fatalf("the event must follow the state that reflects it: %v", before.typ())
	}
	h.stop()

	h2 := newHarness(t, h.cfg.StateDir)
	h2.cfg.Limits = []LimitsSpec{fl.spec()}
	h2.start()
	h2.sendSettings(1, false)
	h2.expect("fresh state", wait, func(m msg) bool {
		return isFreshState(m) && m.flag("limits", "providers", "claude_code", "available")
	})
	refresh := func(id int, five, seven float64) {
		fl.set(five, seven)
		h2.send(fmt.Sprintf(`{"id":%d,"cmd":"refresh","args":{"manual":true}}`, id))
	}
	refresh(2, 10, 84)
	for {
		m, _ := h2.next(wait)
		if m == nil || isResult(2)(m) {
			break
		}
		if m.kind() == "limitAlert" {
			t.Fatalf("alert repeated after restart: %v", m)
		}
	}
	refresh(3, 10, 96)
	if ev := h2.expect("crit alert", wait, isEvent("limitAlert")); ev.str("tier") != "crit" {
		t.Fatalf("event = %v", ev)
	}
	refresh(4, 10, 50)
	h2.expect("refresh 4", wait, isResult(4))
	refresh(5, 10, 85)
	if ev := h2.expect("re-armed warn", wait, isEvent("limitAlert")); ev.str("tier") != "warn" {
		t.Fatalf("event = %v", ev)
	}
	h2.stop()
}

// usage.md §6.8: the first limits fetch only seeds; a 5 h window reaching 100 % grants one candy,
// with the provider id filled, after the state that already counts it.
func TestCandyGrantEndToEnd(t *testing.T) {
	fl := newFakeLimits(t, 50, 10)
	h := newHarness(t, "")
	h.cfg.Limits = []LimitsSpec{fl.spec()}
	h.start()
	h.sendSettings(1, false)
	h.expect("fresh state", wait, func(m msg) bool {
		return isFreshState(m) && m.flag("limits", "providers", "claude_code", "available")
	})
	fl.set(100, 10)
	h.send(`{"id":2,"cmd":"refresh","args":{"manual":true}}`)
	ev := h.expect("candyGranted", wait, isEvent("candyGranted"))
	if ev.str("item") != "rareCandy" || ev.num("count") != 1 || ev.str("providerId") != "claude_code" ||
		ev.str("windowKey") != "claude.fiveHour" || ev.str("windowKind") != "session" {
		t.Fatalf("event = %v", ev)
	}
	// The 100 % window also raises a crit limitAlert; both events follow the state that counts the candy.
	if before := h.lastState(); before.num("companion", "inventory", "rareCandy") != 1 {
		t.Fatalf("state before the event: %v\n%s", before.path("companion", "inventory"), h.tail(4))
	}
	h.expect("refresh reply", wait, isResult(2))
	h.send(`{"id":3,"cmd":"refresh","args":{"manual":true}}`)
	h.quiet(400*time.Millisecond, "second grant", isEvent("candyGranted"))
	h.stop()
}

// companion.md §8.3 and the Phase 4 carry-over: a back sprite that fails transiently falls back to
// the front (status fallback) and is recovered later. Also covers the hatch through the warm loop.
func TestHatchAndBackSpriteFallback(t *testing.T) {
	fs := newFakeSprites(t)
	fs.backFail.Store(true)
	h := newHarness(t, "")
	h.cfg.Sprites = fs.store(h.cfg.CacheDir)
	h.usage.set(1000)
	h.start()
	h.sendSettings(1, true)
	h.expect("fresh state with the egg sprite ready", wait, func(m msg) bool {
		return isFreshState(m) && m.str("companion", "menuSubject", "sprite", "key") == "egg" &&
			m.str("companion", "menuSubject", "sprite", "status") == "ready" &&
			strings.HasSuffix(m.str("companion", "menuSubject", "sprite", "manifestPath"), "egg.png")
	})
	h.usage.set(6_000_000)
	h.send(`{"id":2,"cmd":"refresh"}`)
	hatch := h.expect("hatch", wait, isEvent("hatch"))
	if hatch.num("speciesId") != 16 || hatch.str("displayName") != "Mon16" || hatch.num("shinyOdds") != 64 {
		t.Fatalf("hatch = %v", hatch)
	}
	if before := h.seen[len(h.seen)-2]; before.str("companion", "phase") != "active" {
		t.Fatalf("hatch event before its state: %v", before.path("companion", "phase"))
	}
	st := h.expect("front fallback", wait, func(m msg) bool {
		return m.typ() == "state" && m.str("companion", "menuSubject", "sprite", "status") == "fallback"
	})
	if !st.flag("companion", "menuSubject", "back") || st.str("companion", "menuSubject", "sprite", "key") != "16-a" ||
		!strings.HasSuffix(st.str("companion", "menuSubject", "sprite", "manifestPath"), filepath.Join("16-a", "manifest.json")) {
		t.Fatalf("menu subject = %v", st.path("companion", "menuSubject"))
	}

	h.send(`{"id":3,"cmd":"getSprite","args":{"speciesId":16,"back":true}}`)
	r := h.expect("getSprite", wait, isResult(3))
	if r.str("data", "status") != "fallback" || r.str("data", "key") != "16-a" {
		t.Fatalf("getSprite = %v", r)
	}
	h.send(`{"id":4,"cmd":"getSprite","args":{"speciesId":16,"animated":false}}`)
	if r := h.expect("static", wait, isResult(4)); r.str("data", "status") != "ready" ||
		!strings.HasSuffix(r.str("data", "manifestPath"), "16-s.png") {
		t.Fatalf("static getSprite = %v", r)
	}

	fs.backFail.Store(false)
	h.expect("recovered back sprite", 6*time.Second, func(m msg) bool {
		return m.typ() == "state" && m.str("companion", "menuSubject", "sprite", "status") == "ready" &&
			m.str("companion", "menuSubject", "sprite", "key") == "16-back-a"
	})

	// The 4 s hatch window ends by itself (protocol §2.4: levelUpUntil expiry emits a state).
	h.expect("hatch window over", wait, func(m msg) bool {
		types, _ := m.path("companion", "active", "types").([]any)
		return m.typ() == "state" && m.str("companion", "phase") == "active" && len(types) == 2 &&
			m.path("companion", "levelUpUntil") == nil && m.str("companion", "mood") != "levelUp"
	})

	h.send(`{"id":5,"cmd":"getSpecies","args":{"speciesId":16}}`)
	sp := h.expect("getSpecies", wait, isResult(5))
	if sp.str("data", "detailsStatus") != "ready" || !sp.flag("data", "owned") || sp.str("data", "displayName") != "Mon16" {
		t.Fatalf("getSpecies = %v", sp["data"])
	}
	moves, _ := sp.path("data", "details", "moves").([]any)
	if len(moves) != 3 {
		t.Fatalf("every learn method must be kept: %v", moves)
	}
	h.send(`{"id":6,"cmd":"getDexPage"}`)
	dp := h.expect("getDexPage", wait, isResult(6))
	if cells, _ := dp.path("data", "cells").([]any); len(cells) != 1 || cells[0].(map[string]any)["spriteKey"] != "16-s" {
		t.Fatalf("dex cells = %v", dp.path("data", "cells"))
	}
	h.stop()
}

// The replayed draws keep a ready egg on the species it rolled while its line loads; without them
// every warm pass would roll, and fetch, another line.
func TestReadyEggRollsOneSpeciesAcrossWarms(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "companion-state.json"), `{"installBaselineSet":true,"eggUsage":6000000,"lastDate":"2026-01-01","claimedTodayTokensByProvider":{}}`)
	poke := newFakePoke(1, 4, 7, 10, 13, 16, 19, 22, 25, 28, 31, 34)
	cfg := Config{StateDir: dir, CacheDir: filepath.Join(dir, "cache"), Data: poke, Rng: &companion.SplitMix64{State: 7},
		Logf: t.Logf}.withDefaults()
	d, err := open(cfg, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	d.companionSync(context.Background())
	lines := poke.lines()
	v := d.store.BuildState(companion.MoodInput{Now: time.Now()})
	if v.Active == nil || len(lines) != 1 || lines[v.Active.SpeciesID] != 1 {
		t.Fatalf("line fetches %v, active %+v", lines, v.Active)
	}
	if len(d.rng.queued) != 0 {
		t.Fatalf("draws left queued after the hatch: %v", d.rng.queued)
	}
}

// The prefetch path saves its draws as pendingHatchID; they are never replayed.
func TestPrefetchDrawsAreNotReplayed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "companion-state.json"), `{"installBaselineSet":true,"eggUsage":10,"lastDate":"2026-01-01","claimedTodayTokensByProvider":{}}`)
	poke := newFakePoke(1, 4, 7)
	cfg := Config{StateDir: dir, CacheDir: filepath.Join(dir, "cache"), Data: poke, Rng: &companion.SplitMix64{State: 3},
		Logf: t.Logf}.withDefaults()
	d, err := open(cfg, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	d.companionSync(context.Background())
	if len(d.rng.queued) != 0 {
		t.Fatalf("prefetch draws were requeued: %v", d.rng.queued)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "companion-state.json"))
	if !bytes.Contains(b, []byte(`"pendingHatchID"`)) {
		t.Fatalf("no prefetched species saved: %s", b)
	}
	if lines := poke.lines(); len(lines) != 1 {
		t.Fatalf("line fetches %v", lines)
	}
}

// snapshot --json: one fresh state line, no events, alert tiers untouched.
func TestSnapshotOneShot(t *testing.T) {
	dir := t.TempDir()
	fl := newFakeLimits(t, 40, 90)
	u := newFakeUsage(2_500_000)
	fs := newFakeSprites(t)
	cfg := Config{Version: "1.2.3", StateDir: dir, CacheDir: filepath.Join(dir, "cache"), Providers: []UsageSource{u},
		Limits: []LimitsSpec{fl.spec()}, Data: newFakePoke(16), Logf: t.Logf}
	cfg.Sprites = fs.store(cfg.CacheDir)
	var out bytes.Buffer
	if code := Snapshot(cfg, &out, 5*time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("%d lines", len(lines))
	}
	var m msg
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatal(err)
	}
	if m.typ() != "state" || m.flag("meta", "fromCache") || m.num("usage", "combined", "today", "tokens") != 2_500_000 ||
		!m.flag("limits", "providers", "claude_code", "available") || m.str("limits", "providers", "claude_code", "plan") != "Max" {
		t.Fatalf("snapshot state: %v", m["meta"])
	}
	if _, err := os.Stat(filepath.Join(dir, alertTiersFile)); err == nil {
		t.Fatal("snapshot must not persist alert tiers")
	}
	if fs.hits.Load() != 0 || m.str("companion", "menuSubject", "sprite", "status") != "loading" {
		t.Fatalf("snapshot must read sprites from disk only: %d downloads, %v", fs.hits.Load(), m.path("companion", "menuSubject", "sprite"))
	}
	// A sprite already on disk is reported ready.
	if _, err := cfg.Sprites.Egg(context.Background()); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Snapshot(cfg, &out, 5*time.Second); code != 0 || !strings.Contains(out.String(), `"status":"ready"`) {
		t.Fatalf("second snapshot: exit %d, %s", code, out.String())
	}
	if n := fl.callCount(); n != 2 {
		t.Fatalf("%d limits calls, want 1 per snapshot", n)
	}
}

// Protocol §2.5: SIGTERM takes the EOF path.
func TestSignalShutdown(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir)
	sig := make(chan os.Signal, 1)
	out := &syncBuffer{}
	done := make(chan int, 1)
	go func() { done <- Serve(h.cfg, blockingReader{}, out, sig) }()
	waitFor(t, func() bool { return strings.Contains(out.String(), `"type":"state"`) })
	sig <- syscall.SIGTERM
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no exit on SIGTERM")
	}
}

type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
