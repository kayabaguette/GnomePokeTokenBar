package companion

import (
	"testing"
	"time"
)

func TestEvolvesThroughLineAndGraduatesWithFullChain(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1119
	s := newStore(t, stub(linear3()), 7)
	s.SetLanguage("ko")
	s.hatch(bg, 1)
	eq(t, s.st.Active.currentID(), 1, "hatched")
	eq(t, s.st.Active.TotalForms, 3, "forms")
	s.applyUsage(125_000_000)
	eq(t, s.st.Active.currentID(), 2, "evolved at 125M")
	s.applyUsage(250_000_000)
	eq(t, s.st.Active.currentID(), 3, "evolved at 250M")
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Active.IsFinalStage, true, "final stage")
	s.applyUsage(375_000_000)
	if s.st.Active != nil {
		t.Fatal("not graduated at 375M")
	}
	if !ints(s.st.Dex[0].ChainOrder, []int{1, 2, 3}) {
		t.Fatalf("chain %v", s.st.Dex[0].ChainOrder)
	}
	v := s.BuildState(MoodInput{Now: fixedNow})
	eq(t, deref(v.JustGraduated), "포3", "graduated name")
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:359 — chain names stored at graduation.
	eq(t, s.st.Dex[0].Names[1]["ko"], "포1", "first stage name")
	eq(t, s.st.Dex[0].Names[3]["ja"], "ポ3", "final stage name")
	eq(t, *s.st.Dex[0].NamesVersion, 1, "names version")
}

func TestNoEvolutionGraduatesAtSingleThreshold(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1137
	s := newStore(t, stub(noEvo()), 7)
	s.hatch(bg, 20)
	s.applyUsage(750_000_000)
	if len(s.st.Dex) != 1 || !ints(s.st.Dex[0].ChainOrder, []int{20}) {
		t.Fatalf("dex %v", s.st.Dex)
	}
}

func TestActiveAppearsInLogBeforeGraduationWithoutDuplicate(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1012
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	log := s.CatchLog(CatchLogQuery{})
	eq(t, len(s.st.Dex), 0, "persistent dex empty")
	eq(t, log.Total, 1, "active shown")
	eq(t, log.Entries[0].SpeciesID, 1, "final id")
	s.applyUsage(125_000_000)
	log = s.CatchLog(CatchLogQuery{})
	eq(t, len(log.Entries[0].Chain), 2, "evolved chain")
	s.applyUsage(250_000_000)
	s.applyUsage(375_000_000)
	log = s.CatchLog(CatchLogQuery{})
	eq(t, len(s.st.Dex), 1, "one persistent entry")
	eq(t, log.Total, 1, "no duplicate")
	eq(t, len(log.Entries[0].Chain), 3, "full chain")
}

func TestActivePinnedBeforeGraduatedEntries(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1036 and :1051
	dir := writeSave(t, `{"active":{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":5,"rarity":"common","totalForms":3},"dex":[{"id":"legacy-graduated","baseID":150,"finalID":150,"chainOrder":[150],"rarity":"legendary"}]}`)
	s := openAt(t, dir, stub(linear3()), 7)
	log := s.CatchLog(CatchLogQuery{})
	eq(t, log.Entries[0].ID, "active-1-1", "active first")
	eq(t, log.Entries[0].IsRaising, true, "raising")
	eq(t, log.Entries[1].ID, "legacy-graduated", "legacy entry")
	eq(t, log.Entries[1].IsRaising, false, "nil caughtAt is not the active entry")
	eq(t, log.RarityCounts.Common, 1, "common count")
	eq(t, log.RarityCounts.Legendary, 1, "legendary count")
}

func TestLineNodes(t *testing.T) {
	type item struct {
		id      int // 0 = mystery
		reached bool
		current bool
	}
	cases := []struct {
		name string
		line *Line
		base int
		want []item
	}{
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1146
		{"linear preview", linear3(), 1, []item{{1, true, true}, {2, false, false}, {3, false, false}}},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1170
		{"wurmple branch is one mystery", wurmpleLine(), 265, []item{{265, true, true}, {0, false, false}}},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1180
		{"known prefix then mystery", delayedBranchLine(), 43, []item{{43, true, true}, {44, false, false}, {0, false, false}}},
	}
	for _, c := range cases {
		s := newStore(t, stub(c.line), 7)
		s.hatch(bg, c.base)
		got := s.BuildState(MoodInput{Now: fixedNow}).Active.EvoLine
		if len(got) != len(c.want) {
			t.Fatalf("%s: %d nodes", c.name, len(got))
		}
		for i, w := range c.want {
			id := 0
			if got[i].SpeciesID != nil {
				id = *got[i].SpeciesID
			}
			if id != w.id || got[i].Reached != w.reached || got[i].Current != w.current {
				t.Errorf("%s node %d: %+v", c.name, i, got[i])
			}
			if (got[i].DisplayName == nil) != (w.id == 0) {
				t.Errorf("%s node %d: display name presence", c.name, i)
			}
		}
	}
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1191 — the chosen branch shows after evolving.
	s := newStore(t, stub(wurmpleLine()), 7)
	s.hatch(bg, 265)
	plan := s.st.Active.PlannedPathIDs
	s.applyUsage(phase(rarityCommon, len(plan), 0))
	got := s.BuildState(MoodInput{Now: fixedNow}).Active.EvoLine
	if len(got) != 3 || *got[1].SpeciesID != plan[1] || !got[1].Current || *got[2].SpeciesID != plan[2] || got[2].Reached {
		t.Fatalf("after evolving: %+v", got)
	}
}

func TestRepairedPlan(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1164
	if got := repairedPlan([]int{265}, 0, []int{265, 266, 267}); !ints(got, []int{265, 266, 267}) {
		t.Fatalf("got %v", got)
	}
}

func TestBranchingPrefersUncollectedFinals(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1208
	s := newStore(t, stub(branch3()), 7)
	finals := map[int]bool{}
	for i := 0; i < 3; i++ {
		s.hatch(bg, 10)
		s.applyUsage(phase(rarityCommon, 2, 0))
		s.applyUsage(phase(rarityCommon, 2, 1))
		finals[s.st.Dex[len(s.st.Dex)-1].FinalID] = true
	}
	if !finals[11] || !finals[12] || !finals[13] {
		t.Fatalf("finals %v", finals)
	}
}

func TestRepeatGrowthDecidedByCollectedBase(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1223
	s := newStore(t, stub(branch3()), 7)
	evo, grad := phase(rarityCommon, 2, 0), phase(rarityCommon, 2, 1)
	s.hatch(bg, 10)
	eq(t, s.st.Active.HasGrowthBoost, false, "first hatch")
	first := s.st.Active.PlannedPathIDs[1]
	s.applyUsage(evo)
	s.applyUsage(grad)
	s.hatch(bg, 10)
	a := s.st.Active
	if a.PlannedPathIDs[1] == first {
		t.Fatal("repeat hatch planned the same final")
	}
	eq(t, a.HasGrowthBoost, true, "boost by base")
	v := s.BuildState(MoodInput{Now: fixedNow}).Active
	eq(t, v.GrowthBoost, true, "growthBoost")
	eq(t, v.Threshold, evo/2, "halved threshold")
}

func TestWurmplePlanPreselectedEvolutionConsumesNoRNG(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1244
	rng := &countingRng{SplitMix64: SplitMix64{State: 7}}
	s := newStore(t, stub(wurmpleLine()), 0, withRng(rng))
	s.hatch(bg, 265)
	plan := s.st.Active.PlannedPathIDs
	if !ints(plan, []int{265, 266, 267}) && !ints(plan, []int{265, 268, 269}) {
		t.Fatalf("plan %v", plan)
	}
	if !ints(s.st.Active.PathIDs, []int{265}) || s.st.Active.TotalForms != 3 {
		t.Fatal("bad hatch state")
	}
	calls := rng.calls
	s.applyUsage(phase(rarityCommon, 3, 0))
	eq(t, rng.calls, calls, "evolution draws nothing")
	eq(t, s.st.Active.currentID(), plan[1], "follows the plan")
}

func TestPersistenceRoundTrip(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1267
	dir := t.TempDir()
	s1 := openAt(t, dir, stub(linear3()), 1)
	s1.hatch(bg, 1)
	s1.applyUsage(125_000_000)
	s1.SetLanguage("ja")
	s2 := openAt(t, dir, stub(linear3()), 1)
	eq(t, s2.st.Active.currentID(), 2, "current")
	eq(t, s2.st.Active.StageIndex, 1, "stage")
	eq(t, s2.st.Language, "ja", "language")
}

func TestRepeatGrowthPersistsAcrossRestart(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1279
	dir := t.TempDir()
	s1 := openAt(t, dir, stub(linear3()), 1)
	s1.hatch(bg, 1)
	for stage := 0; stage < 3; stage++ {
		s1.applyUsage(phase(rarityCommon, 3, stage))
	}
	s1.hatch(bg, 1)
	s2 := openAt(t, dir, stub(linear3()), 1)
	eq(t, s2.st.Active.HasGrowthBoost, true, "boost persisted")
	eq(t, s2.stageThreshold(s2.st.Active), 62_500_000, "tokens to next")
	m, err := decodeMon([]byte(`{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":3}`))
	if err != nil || m.HasGrowthBoost || m.standardThreshold() != 125_000_000 {
		t.Fatalf("legacy active %+v %v", m, err)
	}
}

func TestReloadNormalizesPlans(t *testing.T) {
	cases := []struct {
		name     string
		saved    string
		line     *Line
		seed     uint64
		path     []int
		plans    [][]int // any of
		stage    int
		used     int
		rngCalls int // -1: unchecked
	}{
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1328
		{"legacy incomplete plan migrates", `{"active":{"baseID":265,"pathIDs":[265],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":1}}`,
			wurmpleLine(), 7, []int{265}, [][]int{{265, 266, 267}, {265, 268, 269}}, 0, 0, 2},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1357
		{"invalid suffix repaired", `{"active":{"baseID":265,"pathIDs":[265,266],"plannedPathIDs":[265,266,269],"stageIndex":1,"usedAtStage":42,"rarity":"common","totalForms":3}}`,
			wurmpleLine(), 9, []int{265, 266}, [][]int{{265, 266, 267}}, 1, 42, 1},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1395
		{"leaf plan reused", `{"active":{"baseID":265,"pathIDs":[265,266,267],"plannedPathIDs":[265,266,267],"stageIndex":2,"usedAtStage":42,"rarity":"common","totalForms":3}}`,
			wurmpleLine(), 9, []int{265, 266, 267}, [][]int{{265, 266, 267}}, 2, 42, 0},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1764 — an unsupported (>649) stage is cut.
		{"unsupported evolution pruned", `{"installBaselineSet":true,"lastDate":"d1","active":{"baseID":56,"pathIDs":[56,57,979],"stageIndex":2,"usedAtStage":123,"rarity":"common","totalForms":3},"dex":[]}`,
			makeLine(56, node(56, node(57, node(979))), rarityCommon), 5, []int{56, 57}, [][]int{{56, 57}}, 1, 123, 0},
	}
	for _, c := range cases {
		rng := &countingRng{SplitMix64: SplitMix64{State: c.seed}}
		s := openAt(t, writeSave(t, c.saved), stub(c.line), 0, withRng(rng))
		use(s, 0)
		if s.line == nil {
			t.Fatalf("%s: line not loaded", c.name)
		}
		a := s.st.Active
		ok := false
		for _, p := range c.plans {
			ok = ok || ints(a.PlannedPathIDs, p)
		}
		if !ok || !ints(a.PathIDs, c.path) || a.StageIndex != c.stage || a.UsedAtStage != c.used || a.TotalForms != len(a.PlannedPathIDs) {
			t.Errorf("%s: %+v", c.name, a)
		}
		if c.rngCalls >= 0 {
			eq(t, rng.calls, c.rngCalls, c.name+" rng calls")
		}
	}
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1345-1354 — the migration's choice persists.
	dir := writeSave(t, cases[0].saved)
	s := openAt(t, dir, stub(wurmpleLine()), 7)
	use(s, 0)
	plan := s.st.Active.PlannedPathIDs
	rng := &countingRng{SplitMix64: SplitMix64{State: 1}}
	re := openAt(t, dir, stub(wurmpleLine()), 0, withRng(rng))
	use(re, 0)
	if !ints(re.st.Active.PlannedPathIDs, plan) || rng.calls != 0 {
		t.Fatalf("reload re-rolled: %v calls=%d", re.st.Active.PlannedPathIDs, rng.calls)
	}
}

func TestReloadPreservesCompleteShortPlannedRoute(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1303
	line := makeLine(1, node(1, node(2), node(3, node(4))), rarityCommon)
	dir := t.TempDir()
	s1 := openAt(t, dir, stub(line), 7)
	s1.hatch(bg, 1)
	if !ints(s1.st.Active.PlannedPathIDs, []int{1, 2}) {
		t.Fatalf("seed 7 plan %v", s1.st.Active.PlannedPathIDs)
	}
	eq(t, draws(1, 1)[0]%2, uint64(1), "a reroll with seed 1 would pick branch 3")
	rng := &countingRng{SplitMix64: SplitMix64{State: 1}}
	s2 := openAt(t, dir, stub(line), 0, withRng(rng))
	use(s2, 0)
	if !ints(s2.st.Active.PlannedPathIDs, []int{1, 2}) || s2.st.Active.TotalForms != 2 {
		t.Fatalf("reloaded %+v", s2.st.Active)
	}
	calls := rng.calls
	s2.applyUsage(phase(rarityCommon, 2, 0))
	eq(t, s2.st.Active.currentID(), 2, "persisted route wins")
	eq(t, rng.calls, calls, "no reroll")
}

func TestReloadWrongRootKeepsIdentityAndDisguise(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1375
	dir := writeSave(t, `{"active":{"baseID":265,"pathIDs":[999],"plannedPathIDs":[999],"stageIndex":0,"usedAtStage":42,"rarity":"common","totalForms":1,"isShiny":true,"nature":"timid","dittoDisguise":265}}`)
	s := openAt(t, dir, stub(wurmpleLine()), 9)
	use(s, 0)
	a := s.st.Active
	if !ints(a.PathIDs, []int{265}) || a.UsedAtStage != 42 || !a.IsShiny || *a.Nature != "timid" ||
		a.DittoDisguise == nil || *a.DittoDisguise != 265 || a.DittoRevealed {
		t.Fatalf("%+v", a)
	}
}

func TestUsageAccruesWhileLineUnloadedThenEvolvesOnLoad(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1736
	dir := t.TempDir()
	s1 := openAt(t, dir, stub(linear3()), 5)
	use(s1, 0)
	s1.hatch(bg, 1)
	s2 := openAt(t, dir, stub(linear3()), 5)
	if s2.line != nil {
		t.Fatal("line must start unloaded")
	}
	s2.applyUsage(300_000_000)
	eq(t, s2.st.Active.UsedAtStage, 300_000_000, "banked")
	eq(t, s2.st.Active.StageIndex, 0, "no evolution without a line")
	use(s2, 0)
	eq(t, s2.st.Active.StageIndex, 1, "evolved on load")
	eq(t, s2.st.Active.UsedAtStage, 175_000_000, "excess carried")
}

func TestAsymmetricBranchGraduatesSafely(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1454
	line := makeLine(1, node(1, node(2), node(3, node(4))), rarityCommon)
	s := newStore(t, stub(line), 7)
	s.hatch(bg, 1)
	eq(t, s.st.Active.TotalForms, len(s.st.Active.PlannedPathIDs), "totalForms is the plan length")
	for guard := 0; s.st.Active != nil && guard < 12; guard++ {
		a := s.st.Active
		s.applyUsage(phase(rarityCommon, a.TotalForms, a.StageIndex))
	}
	if s.st.Active != nil || len(s.st.Dex) != 1 {
		t.Fatal("did not graduate")
	}
	c := s.st.Dex[0].ChainOrder
	if !ints(c, []int{1, 2}) && !ints(c, []int{1, 3, 4}) {
		t.Fatalf("chain %v", c)
	}
}

func TestLocalizedDisplayName(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1444
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	for tag, want := range map[string]string{"ko": "포1", "en": "P1", "ja": "ポ1", "ja-jp": "ポ1", "fr-fr": "P1"} {
		s.SetLanguage(tag)
		eq(t, s.BuildState(MoodInput{Now: fixedNow}).Active.DisplayName, want, tag)
	}
}

func TestCatchLogSortedByRecencyRegardlessOfRarity(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1572
	tick := 0
	var current *Line
	f := &fakeData{lineFn: func(int) (*Line, error) { return current, nil }}
	s := newStore(t, f, 3, withNow(func() time.Time { return fixedNow.Add(time.Duration(tick) * time.Second) }))
	for i, c := range []struct {
		base   int
		rarity string
	}{{200, rarityLegendary}, {100, rarityCommon}, {101, rarityCommon}} {
		current = makeLine(c.base, node(c.base), c.rarity)
		tick = i + 1
		s.hatch(bg, c.base)
		s.applyUsage(graduationTotal(c.rarity))
	}
	log := s.CatchLog(CatchLogQuery{})
	got := []int{log.Entries[0].SpeciesID, log.Entries[1].SpeciesID, log.Entries[2].SpeciesID}
	if !ints(got, []int{101, 100, 200}) {
		t.Fatalf("order %v", got)
	}
	eq(t, log.RarityCounts.Common, 2, "common")
	eq(t, log.RarityCounts.Legendary, 1, "legendary")
	eq(t, log.RarityCounts.Rare, 0, "rare")
	old := s.CatchLog(CatchLogQuery{Sort: "oldestFirst"})
	eq(t, old.Entries[0].SpeciesID, 200, "oldest first")
}

func TestEventsAndLevelUpWindow(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1720 — hatch then evolve celebrations.
	now := fixedNow
	s := newStore(t, stub(linear3()), 9, withNow(func() time.Time { return now }))
	s.hatch(bg, 1)
	evs := s.drain()
	if len(evs) != 1 || evs[0].EventKind() != "hatch" {
		t.Fatalf("events %v", evs)
	}
	v := s.BuildState(MoodInput{Now: now})
	eq(t, v.Mood, "levelUp", "hatch window")
	eq(t, deref(v.LevelUpUntil), "2023-11-14T22:13:24Z", "4 s window")
	s.applyUsage(125_000_000)
	ev := s.drain()[0].(EvolveEvent)
	eq(t, ev.FromSpeciesID, 1, "from")
	eq(t, ev.SpeciesID, 2, "to")
	eq(t, ev.DisplayName, "P2", "name")
	v = s.BuildState(MoodInput{Now: now})
	eq(t, deref(v.JustEvolvedTo), "P2", "justEvolvedTo")
	v = s.BuildState(MoodInput{Now: now.Add(5 * time.Second), HasUsageData: true, TodayTokens: 1})
	eq(t, v.Mood, "idle", "window closed")
	if v.JustEvolvedTo != nil || v.LevelUpUntil != nil {
		t.Fatal("window fields must clear")
	}
	// Swift: CompanionStore.swift:968-970 — an expired window's names never leak into the next one.
	now = now.Add(5 * time.Second)
	s.applyUsage(250_000_000 + 375_000_000) // evolve to #3 and graduate in one call
	v = s.BuildState(MoodInput{Now: now})
	if deref(v.JustEvolvedTo) != "P3" || deref(v.JustGraduated) != "P3" {
		t.Fatalf("same-instant names carry over: %v %v", deref(v.JustEvolvedTo), deref(v.JustGraduated))
	}
	now = now.Add(7 * time.Second)
	s.hatch(bg, 1)
	v = s.BuildState(MoodInput{Now: now})
	if v.JustGraduated != nil || v.JustEvolvedTo != nil {
		t.Fatalf("stale names after a later hatch: %v %v", deref(v.JustGraduated), deref(v.JustEvolvedTo))
	}
	now = now.Add(5 * time.Second)
	s.applyUsage(125_000_000) // evolve
	now = now.Add(5 * time.Second)
	s.applyUsage(250_000_000 + 375_000_000) // evolve and graduate
	s.hatch(bg, 1)
	now = now.Add(7 * time.Second)
	s.applyUsage(125_000_000)
	v = s.BuildState(MoodInput{Now: now})
	if v.JustGraduated != nil || deref(v.JustEvolvedTo) != "P2" {
		t.Fatalf("evolve after an expired graduation: %v %v", deref(v.JustGraduated), deref(v.JustEvolvedTo))
	}
	now = now.Add(5 * time.Second)
	s.st.Active.UsedAtStage = 0
	s.applyUsage(250_000_000)
	now = now.Add(5 * time.Second)
	s.applyUsage(375_000_000) // graduate long after the last evolution
	v = s.BuildState(MoodInput{Now: now})
	if v.JustEvolvedTo != nil || deref(v.JustGraduated) != "P3" {
		t.Fatalf("graduation after an expired evolution: %v %v", deref(v.JustEvolvedTo), deref(v.JustGraduated))
	}
}

func TestGraduationNamesVersionNeedsEveryChainName(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DexNameMigrationTests.swift:136 — a species without names leaves
	// the version unset (the backfill that would retry it is not ported).
	l := linear3()
	delete(l.Names, 2)
	s := newStore(t, stub(l), 7)
	s.hatch(bg, 1)
	s.applyUsage(125_000_000)
	s.applyUsage(250_000_000)
	s.applyUsage(375_000_000)
	e := s.st.Dex[0]
	if e.NamesVersion != nil || e.Names[1]["en"] != "P1" || e.Names[2] != nil {
		t.Fatalf("entry %+v", e)
	}
	eq(t, s.CatchLog(CatchLogQuery{}).Entries[0].Chain[1].DisplayName, "#2", "unnamed stage")
}
