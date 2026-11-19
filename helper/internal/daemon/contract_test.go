package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/limits"
	"poketokenbar/helper/internal/usage"
)

const fixtureDir = "../../../extension/test/fixtures"

// typedResult decodes a result fixture's data strictly; the re-encode goes through resultMsg, the
// envelope the daemon really writes.
type typedResult[T any] struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
	OK   bool   `json:"ok"`
	Data T      `json:"data"`
}

func strictDecode(t *testing.T, raw []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode into %T: %v", v, err)
	}
}

// keyDiff compares the key sets of two JSON values recursively; values may differ.
func keyDiff(path string, want, got any) []string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: fixture has an object, Go emits %T", path, got)}
		}
		var out []string
		for k := range w {
			if _, ok := g[k]; !ok {
				out = append(out, fmt.Sprintf("%s.%s: in the fixture, not emitted", path, k))
			}
		}
		for k := range g {
			if _, ok := w[k]; !ok {
				out = append(out, fmt.Sprintf("%s.%s: emitted, not in the fixture", path, k))
				continue
			}
			out = append(out, keyDiff(path+"."+k, w[k], g[k])...)
		}
		return out
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return []string{fmt.Sprintf("%s: array shape differs", path)}
		}
		var out []string
		for i := range w {
			out = append(out, keyDiff(fmt.Sprintf("%s[%d]", path, i), w[i], g[i])...)
		}
		return out
	case nil:
		if got != nil {
			return []string{fmt.Sprintf("%s: fixture null, Go emits %T", path, got)}
		}
	}
	return nil
}

func checkKeys(t *testing.T, name string, fixture []byte, v any) {
	t.Helper()
	enc, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(fixture, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(enc, &got); err != nil {
		t.Fatal(err)
	}
	if diff := keyDiff(name, want, got); len(diff) > 0 {
		sort.Strings(diff)
		t.Errorf("contract drift:\n%s", strings.Join(diff, "\n"))
	}
}

func checkResult[T any](t *testing.T, file string) {
	raw, err := os.ReadFile(filepath.Join(fixtureDir, file))
	if err != nil {
		t.Fatal(err)
	}
	var r typedResult[T]
	strictDecode(t, raw, &r)
	checkKeys(t, file, raw, resultMsg{Type: r.Type, ID: r.ID, OK: r.OK, Data: r.Data})
}

// m2Pending lists the state-fixture paths (dotted, object keys only) that the Milestone 2 contract
// (docs/protocol.md) added but the Go structs do not carry yet. They are removed from the fixture
// before the strict decode and the key comparison, so this test stays green until each Go phase
// lands. The phase that implements a field deletes its line here: once it does, the emitted key is
// no longer in the stripped fixture and the test checks it for real. Groups are separated by blank
// lines so parallel phases do not touch the same lines.
var m2Pending = []string{
	// Bag and Shop (phase 10)
	"companion.wallet",
	"companion.shop",
	"companion.bag",
	"companion.inventory.mint",
	"companion.inventory.shinyCharm",
	"companion.egg.guarantee",
	"settingsApplied.shopDifficulty",

	// Recap, heatmap and pet fields (phase 11)
	"usage.combined.heatmap",
	"companion.sulkIdleSeconds",
}

// stripPending removes the m2Pending paths from a state fixture and reports which of them it found.
func stripPending(t *testing.T, raw []byte) ([]byte, []string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, path := range m2Pending {
		keys := strings.Split(path, ".")
		node := doc
		for _, k := range keys[:len(keys)-1] {
			next, ok := node[k].(map[string]any)
			if !ok {
				node = nil
				break
			}
			node = next
		}
		if node == nil {
			continue
		}
		if _, ok := node[keys[len(keys)-1]]; ok {
			delete(node, keys[len(keys)-1])
			found = append(found, path)
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out, found
}

// Every fixture decodes into the structs the daemon emits, with no unknown field, and re-encodes
// to exactly the fixture's key set.
func TestContractDrift(t *testing.T) {
	seenPending := map[string]bool{}
	defer func() {
		for _, path := range m2Pending {
			if !seenPending[path] {
				t.Errorf("m2Pending path %q is in no state fixture: remove it", path)
			}
		}
	}()
	for _, file := range []string{"state-egg.json", "state-active.json"} {
		t.Run(file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(fixtureDir, file))
			if err != nil {
				t.Fatal(err)
			}
			raw, found := stripPending(t, raw)
			for _, path := range found {
				seenPending[path] = true
			}
			var st stateMsg
			strictDecode(t, raw, &st)
			checkKeys(t, file, raw, st)
		})
	}
	// M2 result fixtures (candy-plan.json, recap-week.json, recap-week-prev.json, recap-year.json) get a
	// checkResult line here once their Go types exist (phases 10 and 11).
	t.Run("dex-page.json", func(t *testing.T) { checkResult[companion.DexPage](t, "dex-page.json") })
	t.Run("species.json", func(t *testing.T) { checkResult[companion.SpeciesView](t, "species.json") })
	t.Run("catch-log.json", func(t *testing.T) { checkResult[companion.CatchLog](t, "catch-log.json") })
	t.Run("events.ndjson", func(t *testing.T) {
		f, err := os.Open(filepath.Join(fixtureDir, "events.ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		types := map[string]func() any{
			"hatch":        func() any { return &companion.HatchEvent{} },
			"evolve":       func() any { return &companion.EvolveEvent{} },
			"graduate":     func() any { return &companion.GraduateEvent{} },
			"candyGranted": func() any { return &companion.CandyGrantedEvent{} },
			"badge":        func() any { return &companion.BadgeEvent{} },
			"limitAlert":   func() any { return &limitAlertEvent{} },
			"authExpired":  func() any { return &authExpiredEvent{} },
			"error":        func() any { return &errorEvent{} },
		}
		seen := map[string]bool{}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			var head struct{ Kind string }
			_ = json.Unmarshal(line, &head)
			mk, ok := types[head.Kind]
			if !ok {
				t.Errorf("event kind %q has no Go type", head.Kind)
				continue
			}
			seen[head.Kind] = true
			v := mk()
			strictDecode(t, line, v)
			checkKeys(t, "event "+head.Kind, line, v)
		}
		if len(seen) != len(types) {
			t.Errorf("events.ndjson covers %d kinds, the daemon emits %d", len(seen), len(types))
		}
	})
}

// budgetDaemon builds a realistic world for the state budget: a 31-day month fully used, three
// limit windows, an active three-stage line with a pinned representative and a sprite path.
func budgetDaemon(t *testing.T) (*daemon, time.Time) {
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata")
	}
	now := time.Date(2026, 12, 31, 14, 20, 0, 0, loc)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "companion-state.json"), `{"installBaselineSet":true,"lastDate":"2026-12-31",
		"claimedTodayTokensByProvider":{"claude_code":187291640},
		"active":{"baseID":16,"pathIDs":[16,17],"plannedPathIDs":[16,17,18],"stageIndex":1,"usedAtStage":162400000,
		"rarity":"common","totalForms":3,"nature":"adamant"}}`)
	u := newFakeUsage(187_291_640)
	for ago := 1; ago <= 40; ago++ {
		if ago%7 != 3 {
			u.history[ago] = 214_386_120 + int64(ago)*1_234_567
		}
	}
	cfg := Config{Version: "0.1.0", StateDir: dir, CacheDir: filepath.Join(dir, "cache"), Providers: []UsageSource{u},
		Limits: []LimitsSpec{{ProviderID: "claude_code"}}, Data: newFakePoke(16), Now: func() time.Time { return now },
		Loc: loc, Logf: t.Logf}.withDefaults()
	d, err := open(cfg, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	enr, _ := u.Scan(now, time.Monday)
	d.applyUsage(now, []scanResult{{enr: enr}})
	d.companionSync(context.Background())
	st, err := limits.ParseStatus([]byte(fmt.Sprintf(`{"five_hour":{"utilization":47.0,"resets_at":%q},
		"seven_day":{"utilization":83.0,"resets_at":%q},"limits":[{"kind":"weekly_scoped","percent":31.0,
		"resets_at":%q,"scope":{"model":{"display_name":"Opus"}}}]}`,
		now.Add(70*time.Minute).UTC().Format(time.RFC3339Nano), now.Add(53*time.Hour).UTC().Format(time.RFC3339Nano),
		now.Add(53*time.Hour).UTC().Format(time.RFC3339Nano))))
	if err != nil {
		t.Fatal(err)
	}
	st.SubscriptionType, st.RateLimitTier = "max", "default_claude_max_5x"
	d.limits["claude_code"].applyResult(st, nil, now.Add(-2*time.Second))
	d.lastUpdated = now.Add(-3 * time.Second)
	d.fromCache = false
	// The subject is the active Pidgeotto; its sprite is on disk, so the state carries its path.
	manifest := filepath.Join(cfg.CacheDir, "sprites", "17-a", "manifest.json")
	writeFile(t, manifest, "{}")
	d.menu = menuSprite{want: spriteWant{key: "17-a"}, gen: 1, key: "17-a", status: "ready", path: manifest}
	return d, now
}

// Protocol §3: a typical state stays within 8 KB compact, here with a 31-day month.
func TestStateBudget(t *testing.T) {
	d, now := budgetDaemon(t)
	st := d.buildState(now)
	if st.Companion.Active == nil || len(st.Companion.Active.EvoLine) != 3 || len(st.Limits.Providers["claude_code"].Windows) != 3 ||
		len(st.Usage.Combined.MonthDaily.Tokens) != 31 {
		t.Fatalf("budget world incomplete: active=%v windows=%d month=%d", st.Companion.Active,
			len(st.Limits.Providers["claude_code"].Windows), len(st.Usage.Combined.MonthDaily.Tokens))
	}
	if p := st.Companion.MenuSubject.Sprite.ManifestPath; p == nil || st.Companion.MenuSubject.Sprite.Status != "ready" {
		t.Fatalf("the sprite path must be part of the measured state: %+v", st.Companion.MenuSubject.Sprite)
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("state: %d bytes", len(b))
	if len(b) > 8*1024 {
		t.Fatalf("state is %d bytes, budget 8192", len(b))
	}
}

// Protocol §5.2 invariants, from one scan, across a week that starts in the previous month and
// after midnight passes without a rescan (the cached-state today guard of §2.3).
func TestUsageInvariants(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata")
	}
	now := time.Date(2026, 12, 2, 10, 0, 0, 0, loc) // a Wednesday
	u := newFakeUsage(5_000)
	for ago := 1; ago <= 25; ago++ {
		u.history[ago] = int64(1000 * ago)
	}
	cfg := Config{StateDir: t.TempDir(), Providers: []UsageSource{u}, Data: newFakePoke(), Now: func() time.Time { return now },
		Loc: loc, Logf: t.Logf}.withDefaults()
	cfg.CacheDir = filepath.Join(cfg.StateDir, "cache")
	d, err := open(cfg, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	d.settings.WeekStart = 7 // Sunday: the week began on 2026-11-29
	enr, _ := u.Scan(now, d.settings.weekday())
	d.applyUsage(now, []scanResult{{enr: enr}})

	check := func(at time.Time, wantToday int64) {
		t.Helper()
		v, _, _ := d.buildUsage(at.In(loc), nil)
		c := v.Combined
		r, m := c.RecentDaily.Tokens, c.MonthDaily.Tokens
		if len(r) != 21 || r[20] != c.Today.Tokens || c.Today.Tokens != wantToday {
			t.Fatalf("%s: recentDaily %d entries, last %d, today %d (want %d)", v.Calendar.Today, len(r), r[len(r)-1], c.Today.Tokens, wantToday)
		}
		var sum int64
		for _, x := range m {
			sum += x
		}
		if sum != c.Month.Tokens || c.MonthDaily.Start != v.Calendar.MonthStart {
			t.Fatalf("%s: Σ monthDaily %d ≠ month %d", v.Calendar.Today, sum, c.Month.Tokens)
		}
		ws, _ := time.ParseInLocation(usage.DayLayout, v.Calendar.WeekStart, loc)
		td, _ := time.ParseInLocation(usage.DayLayout, v.Calendar.Today, loc)
		n := int(td.Sub(ws).Hours()/24+0.5) + 1
		var week int64
		for _, x := range r[21-n:] {
			week += x
		}
		if week != c.Week.Tokens {
			t.Fatalf("%s: last %d days sum %d ≠ week %d", v.Calendar.Today, n, week, c.Week.Tokens)
		}
	}
	check(now, 5_000)
	if v, _, _ := d.buildUsage(now, nil); v.Calendar.WeekStart != "2026-11-29" || v.Calendar.MonthStart != "2026-12-01" {
		t.Fatalf("calendar = %+v", v.Calendar)
	}
	check(now.Add(24*time.Hour), 0) // no rescan: today is zero, yesterday shifts into the series
}
