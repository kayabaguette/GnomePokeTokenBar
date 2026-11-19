package companion

import (
	"reflect"
	"testing"
)

func ownedRange(lo, hi int, isShiny bool) []dexSpecies {
	var out []dexSpecies
	for id := lo; id <= hi; id++ {
		out = append(out, dexSpecies{id: id, isShiny: isShiny})
	}
	return out
}

func TestRegions(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:9
	for id, want := range map[int]string{1: "kanto", 151: "kanto", 152: "johto", 251: "johto", 252: "hoenn", 386: "hoenn",
		387: "sinnoh", 493: "sinnoh", 494: "unova", 649: "unova"} {
		r, ok := regionOf(id)
		eq(t, ok && r.key == want, true, want)
	}
	// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:23
	for _, id := range []int{0, -5, 650, 10_000} {
		if _, ok := regionOf(id); ok {
			t.Errorf("%d has a region", id)
		}
	}
	// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:30 and :44-56
	sum := 0
	for i, want := range []int{151, 100, 135, 107, 156} {
		eq(t, regions[i].total(), want, regions[i].key)
		sum += want
		if i > 0 {
			eq(t, regions[i-1].hi+1, regions[i].lo, "contiguous")
		}
	}
	eq(t, sum, maxSpeciesID, "national total")
}

func TestRegionCompletions(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:60
	p := regionCompletions([]dexSpecies{{id: 1}, {id: 4}, {id: 7}, {id: 152}, {id: 999}})
	eq(t, p[0].owned, 3, "kanto")
	eq(t, p[1].owned, 1, "johto")
	// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:76
	eq(t, regionCompletions([]dexSpecies{{id: 25}, {id: 25, isShiny: true}, {id: 26}})[0].owned, 2, "duplicates once")
	// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:82 and :90
	j := regionCompletions(ownedRange(152, 251, false))[1]
	eq(t, j.owned, 100, "johto complete")
	sh := ownedRange(152, 251, true)
	sh[0].isShiny = false
	eq(t, regionCompletions(sh)[1].shinyOwned, 99, "one normal")
}

func badgeStore(t *testing.T, lo, hi int, isShiny bool, unownForms int) *Store {
	st := freshState()
	if hi >= lo {
		e := entry(lo, hi, nil, rarityCommon)
		for id := lo; id <= hi; id++ {
			e.ChainOrder = append(e.ChainOrder, id)
		}
		e.IsShiny = isShiny
		st.Dex = append(st.Dex, e)
	}
	for _, l := range unownLetters[:unownForms] {
		st.Dex = append(st.Dex, unownEntry(l, false))
	}
	return openAt(t, writeState(t, st), &fakeData{}, 7)
}

func keys(bs []badge) []string {
	out := []string{}
	for _, b := range bs {
		out = append(out, b.key)
	}
	return out
}

func TestEarnedBadges(t *testing.T) {
	cases := []struct {
		name     string
		lo, hi   int
		isShiny  bool
		unown    int
		expected []string
	}{
		// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:100
		{"nothing complete", 1, 2, false, 0, []string{}},
		// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:105 and :149
		{"region only", 1, 151, false, 0, []string{"region.kanto"}},
		// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:113
		{"shiny region", 1, 151, true, 0, []string{"region.kanto", "regionShiny.kanto"}},
		// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:120 (27 letters miss, 28 earn; Unown is Johto)
		{"27 letters", 0, -1, false, 27, []string{}},
		{"28 letters", 0, -1, false, 28, []string{"unown"}},
		// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:126
		{"national", 1, 649, false, 0, []string{"region.kanto", "region.johto", "region.hoenn", "region.sinnoh", "region.unova", "national"}},
		// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:134
		{"national shiny", 1, 649, true, 0, []string{"region.kanto", "regionShiny.kanto", "region.johto", "regionShiny.johto",
			"region.hoenn", "regionShiny.hoenn", "region.sinnoh", "regionShiny.sinnoh", "region.unova", "regionShiny.unova", "national", "nationalShiny"}},
	}
	for _, c := range cases {
		s := badgeStore(t, c.lo, c.hi, c.isShiny, c.unown)
		if got := keys(s.earnedBadges()); !reflect.DeepEqual(got, c.expected) {
			t.Errorf("%s: %v", c.name, got)
		}
		// Swift: Tests/PokeTokenBarTests/DexCompletionStoreTests.swift:57 — existing badges are seeded silently.
		if len(s.drain()) != 0 {
			t.Errorf("%s: seeding announced badges", c.name)
		}
		for _, k := range c.expected {
			if !s.st.NotifiedDexCompletions[k] {
				t.Errorf("%s: %s not seeded", c.name, k)
			}
		}
	}
}

func TestBadgeAnnouncedOnceWhenEarned(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DexCompletionStoreTests.swift:116 (non-import half) and PokedexRegionTests.swift:155-180
	st := freshState()
	e := entry(152, 250, nil, rarityCommon)
	for id := 152; id <= 250; id++ {
		e.ChainOrder = append(e.ChainOrder, id)
	}
	st.Dex = []dexEntry{e}
	st.EggUsage, st.PendingHatchID = eggHatchThreshold, intPtr(251)
	f := &fakeData{lineFn: func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil }}
	s := openAt(t, writeState(t, st), f, 7)
	eq(t, s.st.DexCompletionsSeeded, true, "fresh load seeds")
	eq(t, len(s.st.NotifiedDexCompletions), 0, "nothing earned yet")
	s.hatchIfNeeded(bg)
	evs := s.drain()
	var badges []BadgeEvent
	for _, e := range evs {
		if b, ok := e.(BadgeEvent); ok {
			badges = append(badges, b)
		}
	}
	if len(badges) != 1 || badges[0].Key != "region.johto" || badges[0].Badge != "region" || deref(badges[0].Region) != "johto" {
		t.Fatalf("badges %+v", badges)
	}
	s.save()
	if len(s.drain()) != 0 {
		t.Fatal("announced twice")
	}
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).DexSummary.Badges[0], "region.johto", "summary badges")
}

func TestUnownLettersDoNotInflateRegionalSpecies(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DexCompletionStoreTests.swift:73
	st := freshState()
	for _, l := range unownLetters[:27] {
		e := unownEntry(l, false)
		e.Rarity = rarityRare
		st.Dex = append(st.Dex, e)
	}
	for _, f := range formsOf(351) {
		st.Dex = append(st.Dex, entry(351, 351, []int{351}, rarityCommon, withForm(f)))
	}
	partial := openAt(t, writeState(t, st), &fakeData{}, 7)
	for _, b := range partial.earnedBadges() {
		if b.key == "unown" {
			t.Fatal("27 letters earned the badge")
		}
	}
	st.Active = unownMon("question", false)
	full := openAt(t, writeState(t, st), &fakeData{}, 7)
	if got := keys(full.earnedBadges()); !reflect.DeepEqual(got, []string{"unown"}) {
		t.Fatalf("badges %v", got)
	}
	page := full.DexPage(DexQuery{})
	eq(t, page.Regions[1].Owned, 1, "johto")
	eq(t, page.Regions[2].Owned, 1, "hoenn")
	eq(t, full.BuildState(MoodInput{Now: fixedNow}).DexSummary.OwnedSpecies, 2, "national")
	eq(t, full.st.NotifiedDexCompletions["unown"], true, "seeded")
}

func TestRegionFilter(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DexCompletionStoreTests.swift:62
	s := badgeStore(t, 1, 200, false, 0)
	eq(t, s.DexPage(DexQuery{Region: sp("johto")}).Total, 49, "johto")
	eq(t, s.DexPage(DexQuery{Region: sp("kanto")}).Total, 151, "kanto")
	eq(t, s.DexPage(DexQuery{}).Total, 200, "all")
	p := s.DexPage(DexQuery{Region: sp("johto"), PageSize: 16})
	eq(t, p.PageCount, 4, "pages")
	eq(t, p.Cells[0].SpeciesID, 152, "first johto")
	a := s.DexPage(DexQuery{AnchorSpeciesID: intPtr(40)})
	eq(t, a.Page, 3, "anchor page")
	eq(t, s.DexPage(DexQuery{Page: 99}).Page, 13, "page clamp")
}

func TestDexFoldsDuplicateLines(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:393
	st := freshState()
	st.Language = "ko"
	st.Dex = []dexEntry{
		entry(1, 3, []int{1, 2, 3}, rarityCommon, withNames(map[int]map[string]string{1: {"ko": "포1"}, 2: {"ko": "포2"}, 3: {"ko": "포3"}})),
		entry(1, 3, []int{1, 2, 3}, rarityCommon),
	}
	s := openAt(t, writeState(t, st), stub(linear3()), 7)
	page := s.DexPage(DexQuery{})
	var names []string
	for _, c := range page.Cells {
		names = append(names, c.DisplayName)
	}
	if !reflect.DeepEqual(names, []string{"포1", "포2", "포3"}) || page.RarityCounts.Common != 3 {
		t.Fatalf("cells %v", names)
	}
	eq(t, s.CatchLog(CatchLogQuery{}).Total, 2, "log keeps both")
}

func TestDexCountsOnlyReachedStagesOfActive(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:418
	st := freshState()
	st.Active = &monState{BaseID: 1, PathIDs: []int{1, 2}, PlannedPathIDs: []int{1, 2, 3}, Rarity: rarityCommon, TotalForms: 3, Nature: sp("brave")}
	s := openAt(t, writeState(t, st), stub(linear3()), 7)
	if c := s.DexPage(DexQuery{}).Cells; len(c) != 1 || c[0].SpeciesID != 1 {
		t.Fatalf("cells %+v", c)
	}
}

func TestDexShinyAcrossChainAndBothColours(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:435 and :454
	normal := func() dexEntry { return entry(605, 606, []int{605, 606}, rarityRare) }
	shinyE := func() dexEntry { return entry(605, 606, []int{605, 606}, rarityRare, shiny) }
	for _, list := range [][]dexEntry{{normal(), shinyE()}, {shinyE(), normal()}, {normal()}, {shinyE()}} {
		st := freshState()
		st.Dex = list
		s := openAt(t, writeState(t, st), stub(linear3()), 7)
		hasNormal, hasShiny := false, false
		for _, e := range list {
			hasNormal, hasShiny = hasNormal || !e.IsShiny, hasShiny || e.IsShiny
		}
		for _, c := range s.DexPage(DexQuery{}).Cells {
			if c.HasNormal != hasNormal || c.IsShiny != hasShiny || c.Sprite.Shiny != hasShiny {
				t.Fatalf("cell %+v", c)
			}
		}
		eq(t, len(s.Species(bg, 605).Individuals), 0, "earlier stages are not individuals")
	}
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:476
	for _, activeShiny := range []bool{false, true} {
		st := freshState()
		e := entry(1, 1, []int{1}, rarityCommon)
		e.IsShiny = !activeShiny
		st.Dex = []dexEntry{e}
		st.Active = &monState{BaseID: 1, PathIDs: []int{1, 2}, PlannedPathIDs: []int{1, 2, 3}, StageIndex: 1, Rarity: rarityCommon, TotalForms: 3, IsShiny: activeShiny}
		s := openAt(t, writeState(t, st), stub(linear3()), 7)
		c := s.DexPage(DexQuery{}).Cells
		if len(c) != 2 || !c[0].HasNormal || !c[0].IsShiny || c[1].HasNormal == activeShiny || c[1].IsShiny != activeShiny {
			t.Fatalf("active shiny %v: %+v", activeShiny, c)
		}
	}
}

func TestRaisingMarksOnlyCurrentStage(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:708
	s := newStore(t, stub(linear3()), 7)
	s.SetLanguage("ko")
	s.hatch(bg, 1)
	eq(t, s.DexPage(DexQuery{}).Cells[0].DisplayName, "포1", "active name from the loaded line")
	s.applyUsage(125_000_000)
	c := s.DexPage(DexQuery{}).Cells
	if len(c) != 2 || c[0].IsRaising || !c[1].IsRaising {
		t.Fatalf("cells %+v", c)
	}
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:722
	st := freshState()
	st.Dex = []dexEntry{entry(1, 3, []int{1, 2, 3}, rarityCommon)}
	st.Active = &monState{BaseID: 1, PathIDs: []int{1, 2, 3}, PlannedPathIDs: []int{1, 2, 3}, StageIndex: 1, Rarity: rarityCommon, TotalForms: 3}
	g := openAt(t, writeState(t, st), stub(linear3()), 7)
	var raising []bool
	for _, c := range g.DexPage(DexQuery{}).Cells {
		raising = append(raising, c.IsRaising)
	}
	if !reflect.DeepEqual(raising, []bool{false, true, false}) {
		t.Fatalf("raising %v", raising)
	}
}

func TestRepresentativeSelection(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:500 — nil follows the companion.
	st := freshState()
	st.Active = &monState{BaseID: 1, PathIDs: []int{1}, PlannedPathIDs: []int{1}, Rarity: rarityCommon, TotalForms: 3, IsShiny: true}
	s := openAt(t, writeState(t, st), stub(linear3()), 7)
	v := s.BuildState(MoodInput{Now: fixedNow})
	if v.Representative != nil || *v.MenuSubject.SpeciesID != 1 || !v.MenuSubject.Shiny {
		t.Fatalf("subject %+v", v.MenuSubject)
	}
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:516 — a pinned species uses owned shiny and persists.
	st = freshState()
	st.Dex = []dexEntry{entry(1, 3, []int{1, 2, 3}, rarityCommon, shiny)}
	st.Active = &monState{BaseID: 20, PathIDs: []int{20}, PlannedPathIDs: []int{20}, Rarity: rarityCommon, TotalForms: 1}
	dir := writeState(t, st)
	s = openAt(t, dir, stub(linear3()), 7)
	if !s.SetRepresentative(intPtr(2), nil) {
		t.Fatal("owned species rejected")
	}
	v = s.BuildState(MoodInput{Now: fixedNow})
	if v.Active.SpeciesID != 20 || *v.MenuSubject.SpeciesID != 2 || !v.MenuSubject.Shiny || !v.Representative.Shiny {
		t.Fatalf("view %+v", v.MenuSubject)
	}
	re := openAt(t, dir, stub(linear3()), 7)
	eq(t, *re.BuildState(MoodInput{Now: fixedNow}).MenuSubject.SpeciesID, 2, "persisted")
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:561 — clearing returns to the companion.
	re.SetRepresentative(nil, nil)
	eq(t, *re.BuildState(MoodInput{Now: fixedNow}).MenuSubject.SpeciesID, 20, "follows the companion")
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:542 — the pin survives graduation.
	g := newStore(t, stub(linear3()), 7)
	g.hatch(bg, 1)
	g.SetRepresentative(intPtr(1), nil)
	for stage := 0; stage < 3; stage++ {
		g.applyUsage(phase(rarityCommon, 3, stage))
		eq(t, *g.BuildState(MoodInput{Now: fixedNow}).MenuSubject.SpeciesID, 1, "pinned")
	}
	if g.st.Active != nil || g.st.RepresentativeSpeciesID == nil {
		t.Fatal("pin lost at graduation")
	}
}

func TestSearchMatcher(t *testing.T) {
	// CS:615-643: number, #number and partial numbers, names in any language ignoring case and diacritics.
	m := newMatcher("25")
	eq(t, m.matches([]int{25}, nil), true, "exact")
	eq(t, m.matches([]int{125}, nil), true, "partial")
	eq(t, newMatcher("#25").matches([]int{250}, nil), true, "#25 finds #250")
	eq(t, newMatcher("#25").matches([]int{125}, nil), false, "#25 does not find #125")
	eq(t, newMatcher("flabebe").matches(nil, []string{"Flabébé"}), true, "diacritics")
	eq(t, newMatcher("PIKA").matches(nil, []string{"Pikachu"}), true, "case")
	eq(t, newMatcher("ピカ").matches(nil, []string{"ピカチュウ"}), true, "kana")
	if newMatcher("   ") != nil {
		t.Fatal("blank query filters nothing")
	}
	st := freshState()
	st.Dex = []dexEntry{entry(1, 3, []int{1, 2, 3}, rarityCommon, withNames(map[int]map[string]string{3: {"fr": "Florizarre", "en": "Venusaur"}}))}
	s := openAt(t, writeState(t, st), stub(linear3()), 7)
	eq(t, s.CatchLog(CatchLogQuery{Query: "florí"}).Total, 1, "log matches any stored language")
	eq(t, s.DexPage(DexQuery{Query: "venus"}).Total, 1, "dex")
	eq(t, s.DexPage(DexQuery{Query: "2"}).Total, 1, "number")
}

func TestDexSorts(t *testing.T) {
	st := freshState()
	st.Language = "en"
	named := func(id int, name, rarity string) dexEntry {
		return entry(id, id, []int{id}, rarity, withNames(map[int]map[string]string{id: {"en": name}}))
	}
	st.Dex = []dexEntry{named(3, "Charlie", rarityCommon), named(1, "Bravo", rarityRare), named(2, "Alpha", rarityLegendary)}
	s := openAt(t, writeState(t, st), stub(linear3()), 7)
	order := func(sort string) []int {
		var out []int
		for _, c := range s.DexPage(DexQuery{Sort: sort}).Cells {
			out = append(out, c.SpeciesID)
		}
		return out
	}
	for sort, want := range map[string][]int{"numberAsc": {1, 2, 3}, "numberDesc": {3, 2, 1}, "nameAsc": {2, 1, 3},
		"nameDesc": {3, 1, 2}, "rarityDesc": {2, 1, 3}} {
		if got := order(sort); !ints(got, want) {
			t.Errorf("%s: %v", sort, got)
		}
	}
}

func TestRegionCompletionsShinyAndOutOfRange(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokedexRegionTests.swift:62-71 (out-of-range ids ignored, exact counts)
	p := regionCompletions([]dexSpecies{{id: 0}, {id: -5}, {id: 650}, {id: 10_000}, {id: 1}})
	for i, want := range []int{1, 0, 0, 0, 0} {
		eq(t, p[i].owned, want, regions[i].key)
	}
	// Swift: PokedexRegionTests.swift:79-94 — Johto fully owned: 100 owned, 0 shiny; all shiny but one: 99 shiny.
	j := regionCompletions(ownedRange(152, 251, false))[1]
	eq(t, j.owned >= j.region.total(), true, "johto complete")
	eq(t, j.shinyOwned >= j.region.total(), false, "johto shiny incomplete")
	eq(t, j.shinyOwned, 0, "no shiny")
	sh := ownedRange(152, 251, true)
	sh[0].isShiny = false
	j = regionCompletions(sh)[1]
	eq(t, j.owned >= j.region.total(), true, "complete")
	eq(t, j.shinyOwned >= j.region.total(), false, "shiny incomplete")
	eq(t, j.shinyOwned, 99, "shinyOwned")
	// A species owned both plain and shiny counts once, as shiny-owned.
	k := regionCompletions([]dexSpecies{{id: 25}, {id: 25, isShiny: true}})[0]
	eq(t, k.owned, 1, "owned")
	eq(t, k.shinyOwned, 1, "shinyOwned")
}

func TestNationalIgnoresOutOfRangeIds(t *testing.T) {
	// Swift: PokedexRegionTests.swift:93 — ids 0, 650 and 10000 never count toward the national badge.
	st := freshState()
	e := entry(1, 648, nil, rarityCommon)
	for id := 1; id <= 648; id++ {
		e.ChainOrder = append(e.ChainOrder, id)
	}
	e.ChainOrder = append(e.ChainOrder, 0, 650, 10_000)
	st.Dex = []dexEntry{e}
	s := openAt(t, writeState(t, st), &fakeData{}, 7)
	got := keys(s.earnedBadges())
	want := []string{"region.kanto", "region.johto", "region.hoenn", "region.sinnoh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("badges %v, want %v (unova is one short, no national)", got, want)
	}
}
