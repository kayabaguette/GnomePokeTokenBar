package companion

import (
	"encoding/json"
	"testing"
)

func w(key, kind string, util float64, epoch string) CandyWindow {
	return CandyWindow{Key: key, Kind: kind, Utilization: util, Epoch: epoch, ProviderID: "claude_code"}
}

func counts(g []CandyGrant) []int {
	out := []int{}
	for _, x := range g {
		out = append(out, x.Count)
	}
	return out
}

func TestEvaluateCandyGrants(t *testing.T) {
	type step struct {
		windows []CandyWindow
		want    []int
	}
	cases := []struct {
		name  string
		tier  map[string]int
		steps []step
		check func(t *testing.T, tier map[string]int, epoch map[string]string)
	}{
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:34
		{"session grants one", nil, []step{{[]CandyWindow{w("s", CandySession, 100, "")}, []int{1}}},
			func(t *testing.T, tier map[string]int, _ map[string]string) { eq(t, tier["s"], 1, "tier") }},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:43
		{"weekly grants five", nil, []step{{[]CandyWindow{w("wk", CandyWeekly, 100, "")}, []int{5}}}, nil},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:51
		{"99.9 grants nothing", nil, []step{{[]CandyWindow{w("s", CandySession, 99.9, "")}, []int{}}},
			func(t *testing.T, tier map[string]int, _ map[string]string) {
				if _, ok := tier["s"]; ok {
					t.Error("tier set")
				}
			}},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:61
		{"no double grant", nil, []step{
			{[]CandyWindow{w("s", CandySession, 100, "E1")}, []int{1}},
			{[]CandyWindow{w("s", CandySession, 100, "E1")}, []int{}}}, nil},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:72
		{"re-arm below 100", nil, []step{
			{[]CandyWindow{w("s", CandySession, 100, "")}, []int{1}},
			{[]CandyWindow{w("s", CandySession, 40, "")}, []int{}},
			{[]CandyWindow{w("s", CandySession, 100, "")}, []int{1}}}, nil},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:87
		{"epoch advance re-arms at 100", nil, []step{
			{[]CandyWindow{w("claude.fiveHour", CandySession, 100, "2026-09-17T10:00:00Z")}, []int{1}},
			{[]CandyWindow{w("claude.fiveHour", CandySession, 100, "2026-09-17T15:00:00Z")}, []int{1}}},
			func(t *testing.T, _ map[string]int, epoch map[string]string) {
				eq(t, epoch["claude.fiveHour"], "2026-09-17T15:00:00Z", "epoch")
			}},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:103
		{"microsecond jitter", nil, []step{
			{[]CandyWindow{w("claude.152146ba.fiveHour", CandySession, 100, "2026-09-28T17:10:00.928794+00:00")}, []int{1}},
			{[]CandyWindow{w("claude.152146ba.fiveHour", CandySession, 100, "2026-09-28T17:10:00.935515+00:00")}, []int{}}}, nil},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:119
		{"±1 s unix jitter", nil, []step{
			{[]CandyWindow{w("codex.codex.primary", CandySession, 100, "1790613843")}, []int{1}},
			{[]CandyWindow{w("codex.codex.primary", CandySession, 100, "1790613844")}, []int{}},
			{[]CandyWindow{w("codex.codex.primary", CandySession, 100, "1790613843")}, []int{}}}, nil},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:140
		{"1–5 min drift", nil, []step{
			{[]CandyWindow{w("antigravity.3p.5h", CandySession, 100, "2026-09-28T19:10:12Z")}, []int{1}},
			{[]CandyWindow{w("antigravity.3p.5h", CandySession, 100, "2026-09-28T19:11:12Z")}, []int{}},
			{[]CandyWindow{w("antigravity.3p.5h", CandySession, 100, "2026-09-28T19:15:12Z")}, []int{}}}, nil},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:160
		{"5 h unix advance", nil, []step{
			{[]CandyWindow{w("codex.codex.primary", CandySession, 100, "1790613843")}, []int{1}},
			{[]CandyWindow{w("codex.codex.primary", CandySession, 100, "1790631843")}, []int{1}}},
			func(t *testing.T, _ map[string]int, epoch map[string]string) {
				eq(t, epoch["codex.codex.primary"], "1790631843", "epoch")
			}},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:176
		{"7 d advance", nil, []step{
			{[]CandyWindow{w("claude.sevenDay", CandyWeekly, 100, "2026-09-20T00:00:00Z")}, []int{5}},
			{[]CandyWindow{w("claude.sevenDay", CandyWeekly, 100, "2026-09-27T00:00:00Z")}, []int{5}}}, nil},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:192
		{"first epoch sighting is not a re-arm", map[string]int{"claude.fiveHour": 1}, []step{
			{[]CandyWindow{w("claude.fiveHour", CandySession, 100, "E1")}, []int{}}},
			func(t *testing.T, tier map[string]int, epoch map[string]string) {
				eq(t, epoch["claude.fiveHour"], "E1", "epoch recorded")
				eq(t, tier["claude.fiveHour"], 1, "tier kept")
			}},
		// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:204
		{"mixed windows", nil, []step{{[]CandyWindow{
			w("claude.fiveHour", CandySession, 100, ""), w("claude.sevenDay", CandyWeekly, 100, ""),
			w("codex.codex.primary", CandySession, 50, "")}, []int{1, 5}}}, nil},
		// Unparseable epochs reset on any change (CS:1438-1440).
		{"unparseable epoch change", nil, []step{
			{[]CandyWindow{w("k", CandySession, 100, "E1")}, []int{1}},
			{[]CandyWindow{w("k", CandySession, 100, "E2")}, []int{1}}}, nil},
		// A weekly window needs 24 h of advance; 5 h is jitter there.
		{"weekly ignores a 5 h advance", nil, []step{
			{[]CandyWindow{w("claude.sevenDay", CandyWeekly, 100, "2026-09-20T00:00:00Z")}, []int{5}},
			{[]CandyWindow{w("claude.sevenDay", CandyWeekly, 100, "2026-09-20T05:00:00Z")}, []int{}}}, nil},
	}
	for _, c := range cases {
		tier, epoch := map[string]int{}, map[string]string{}
		for k, v := range c.tier {
			tier[k] = v
		}
		for i, st := range c.steps {
			got := counts(EvaluateCandyGrants(st.windows, tier, epoch))
			if jsonOf(got) != jsonOf(st.want) {
				t.Errorf("%s step %d: grants %v, want %v", c.name, i, got, st.want)
			}
		}
		if c.check != nil {
			c.check(t, tier, epoch)
		}
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestParseEpoch(t *testing.T) {
	// CS:1420-1429: seconds above 1e8, milliseconds above 1e12, else ISO-8601.
	s, ok := parseEpoch("1790613843")
	eq(t, ok && s.Unix() == 1790613843, true, "seconds")
	ms, ok := parseEpoch("1790613843000")
	eq(t, ok && ms.Unix() == 1790613843, true, "milliseconds")
	_, ok = parseEpoch("12345")
	eq(t, ok, false, "small numbers are not epochs")
	_, ok = parseEpoch("2026-09-28T17:10:00.928794+00:00")
	eq(t, ok, true, "ISO with microseconds and offset")
}

func TestGrantCandiesSeedingAndPersistence(t *testing.T) {
	five := func(util float64, epoch string) []CandyWindow {
		return []CandyWindow{w("claude.fiveHour", CandySession, util, epoch)}
	}
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:290 — first run seeds without granting.
	s := newStore(t, stub(linear3()), 7)
	eq(t, len(s.GrantCandies(five(100, ""), true)), 0, "no event on seed")
	eq(t, s.st.Inventory["rareCandy"], 0, "no candy")
	eq(t, s.st.CandyFeatureSeeded, true, "seeded")
	eq(t, s.st.CandyGrantTier["claude.fiveHour"], 1, "tier seeded")
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:299
	s.GrantCandies(five(100, ""), true)
	eq(t, s.st.Inventory["rareCandy"], 0, "seeded window does not grant")

	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:307
	s = newStore(t, stub(linear3()), 7)
	s.GrantCandies(five(100, ""), false)
	eq(t, s.st.CandyFeatureSeeded, false, "not ready: no seed")

	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:268 — epoch advance after seeding grants.
	s = newStore(t, stub(linear3()), 7)
	s.GrantCandies(five(100, "E1"), true)
	s.GrantCandies(five(100, "E1"), true)
	eq(t, s.st.Inventory["rareCandy"], 0, "same epoch")
	evs := s.GrantCandies(five(100, "E2"), true)
	eq(t, s.st.Inventory["rareCandy"], 1, "new epoch")
	eq(t, s.st.CandyWindowEpoch["claude.fiveHour"], "E2", "epoch")
	ev := evs[0].(CandyGrantedEvent)
	if ev.Item != "rareCandy" || ev.Count != 1 || ev.ProviderID != "claude_code" || ev.WindowKey != "claude.fiveHour" || ev.WindowKind != "session" {
		t.Fatalf("event %+v", ev)
	}

	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:315 and :323
	s = newStore(t, stub(linear3()), 7)
	s.GrantCandies(nil, true)
	s.GrantCandies(five(100, ""), true)
	s.GrantCandies([]CandyWindow{w("claude.sevenDay", CandyWeekly, 100, "")}, true)
	eq(t, s.st.Inventory["rareCandy"], 6, "1 session + 5 weekly")
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Inventory.RareCandy, 6, "inventory view")

	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:332 — the tier persists: no double grant after restart.
	dir := t.TempDir()
	s1 := openAt(t, dir, stub(linear3()), 1)
	s1.GrantCandies(nil, true)
	s1.GrantCandies(five(100, ""), true)
	s2 := openAt(t, dir, stub(linear3()), 1)
	eq(t, s2.st.Inventory["rareCandy"], 1, "inventory persisted")
	s2.GrantCandies(five(100, ""), true)
	eq(t, s2.st.Inventory["rareCandy"], 1, "no re-grant after restart")

	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:350 — a re-arm alone is persisted.
	s2.GrantCandies(five(40, ""), true)
	s3 := openAt(t, dir, stub(linear3()), 1)
	if _, ok := s3.st.CandyGrantTier["claude.fiveHour"]; ok {
		t.Fatal("re-arm not persisted")
	}
	s3.GrantCandies(five(100, ""), true)
	eq(t, s3.st.Inventory["rareCandy"], 2, "re-grant after re-arm")
}
