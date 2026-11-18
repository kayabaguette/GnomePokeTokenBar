package companion

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// slowpoke mirrors PokemonProfileTests.swift:4-30.
func slowpoke() *Details {
	lu := func(l int) []LearnMethod { return []LearnMethod{{"level-up", l}} }
	return &Details{
		SpeciesID: 79, Height: 12, Weight: 360, GenderRate: 8, Types: []string{"water", "psychic"},
		BaseStats: map[string]int{"hp": 90, "attack": 65, "defense": 65, "special-attack": 40, "special-defense": 40, "speed": 15},
		Abilities: []Ability{{"oblivious", 1, false}},
		Moves: []Move{{"tackle", lu(1)}, {"curse", lu(1)}, {"yawn", lu(1)}, {"growl", lu(5)}, {"water-gun", lu(9)},
			{"surf", []LearnMethod{{"machine", 0}}}},
	}
}

func testDetails(genderRate int, abilities []Ability) *Details {
	return &Details{SpeciesID: 999, GenderRate: genderRate, Types: []string{"normal"}, BaseStats: map[string]int{"hp": 50}, Abilities: abilities}
}

func TestProfileGenerationDeterministic(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:40
	a, b := generateProfile(42, 0, "same"), generateProfile(42, 0, "same")
	if !reflect.DeepEqual(a, b) {
		t.Fatal("not deterministic")
	}
	r := profileRNG(42)
	want := ivSet{}
	for _, f := range want.fields() {
		*f = int(r.Next() % 32)
	}
	eq(t, a.IVs, want, "IVs are six draws % 32 in stat order")
	eq(t, a.Level, 5, "hatch level")
}

func TestEnrichRollsIdentityAndFourLegalMoves(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:50
	p := generateProfile(7, 0, "p")
	p.Level = 9
	p.enrich(slowpoke())
	eq(t, deref(p.Gender), "female", "gender rate 8")
	eq(t, deref(p.AbilityName), "oblivious", "ability")
	eq(t, *p.AbilitySlot, 1, "slot")
	eq(t, len(p.Moves), 4, "four moves")
	names := ""
	for _, m := range p.Moves {
		names += m.Name + ","
	}
	if !strings.Contains(names, "water-gun") || strings.Contains(names, "surf") {
		t.Fatalf("moves %s", names)
	}
	first := p.clone()
	p.enrich(slowpoke())
	if !reflect.DeepEqual(first, p) {
		t.Fatal("enrich re-rolled")
	}
}

func TestNatureChangesNonHPStat(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:67
	p := generateProfile(1, 0, "neutral")
	p.Level = 50
	st := computeStats(slowpoke(), p, sp("adamant"))
	na := ((2*65+p.IVs.Attack)*50)/100 + 5
	ns := ((2*40+p.IVs.SpecialAttack)*50)/100 + 5
	eq(t, st.Attack, int(math.Floor(float64(na)*1.1)), "attack +10%")
	eq(t, st.SpecialAttack, int(math.Floor(float64(ns)*0.9)), "special attack −10%")
}

func TestShedinjaAlwaysHasOneHP(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:81
	d := &Details{SpeciesID: 292, GenderRate: -1, BaseStats: map[string]int{"hp": 1, "attack": 90, "defense": 45,
		"special-attack": 30, "special-defense": 30, "speed": 40}}
	for _, level := range []int{1, 50, 100} {
		p := generateProfile(3, 0, "shedinja")
		p.Level = level
		st := computeStats(d, p, sp("hardy"))
		eq(t, st.HP, 1, "HP")
		eq(t, st.Attack, ((2*90+p.IVs.Attack)*level)/100+5, "attack")
	}
	p := generateProfile(3, 0, "base-1")
	p.Level = 50
	other := &Details{SpeciesID: 999, BaseStats: map[string]int{"hp": 1}}
	eq(t, computeStats(other, p, nil).HP, ((2+p.IVs.HP)*50)/100+60, "only Shedinja is special")
}

func TestEveryNatureChangesExpectedStats(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:110
	neutral := map[string]bool{"hardy": true, "docile": true, "serious": true, "bashful": true, "quirky": true}
	eq(t, len(natures), 25, "natures")
	for _, n := range natures {
		up, down := 0, 0
		for _, s := range statOrder[1:] {
			switch natureModifier(sp(n), s) {
			case 1.1:
				up++
			case 0.9:
				down++
			}
		}
		if neutral[n] && (up|down) != 0 || !neutral[n] && (up != 1 || down != 1) {
			t.Errorf("%s up=%d down=%d", n, up, down)
		}
	}
}

func TestGrowthMapsHatchToFiveAndGraduationToHundred(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:127
	p := generateProfile(1, 0, "growth")
	eq(t, p.Level, 5, "hatch")
	p.advanceGrowth(p.GrowthTokens+graduationTotal(rarityCommon), rarityCommon)
	eq(t, p.Level, 100, "graduation")
}

func TestGenderAndAbilityBranches(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:134
	g := generateProfile(1, 0, "genderless")
	g.enrich(testDetails(-1, nil))
	eq(t, deref(g.Gender), "genderless", "genderless")
	m := generateProfile(1, 0, "male")
	m.enrich(testDetails(0, nil))
	eq(t, deref(m.Gender), "male", "male")
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:144
	h := generateProfile(115, 0, "hidden-roll")
	h.enrich(testDetails(-1, []Ability{{"hidden-one", 3, true}, {"hidden-two", 4, true}}))
	eq(t, deref(h.AbilityName), "hidden-two", "seed 115 rolls the hidden slot")
	eq(t, *h.AbilitySlot, 4, "slot")
	eq(t, h.AbilityIsHidden, true, "hidden")
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:156
	f := generateProfile(1, 0, "hidden-fallback")
	f.enrich(testDetails(-1, []Ability{{"first-hidden", 2, true}, {"second-hidden", 3, true}}))
	eq(t, deref(f.AbilityName), "first-hidden", "lowest slot fallback")
	eq(t, f.AbilityIsHidden, true, "hidden")
}

func TestAbilityNameFallbackChain(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:168
	normal, hidden := Ability{"normal", 1, false}, Ability{"hidden", 3, true}
	a := generateProfile(2, 0, "slot-only")
	a.AbilitySlot, a.AbilityIsHidden = intPtr(1), true
	a.enrich(testDetails(-1, []Ability{normal}))
	eq(t, deref(a.AbilityName), "normal", "same slot repairs a stale hidden flag")
	eq(t, a.AbilityIsHidden, false, "flag repaired")
	b := generateProfile(3, 0, "normal-fallback")
	b.AbilitySlot, b.AbilityIsHidden = intPtr(99), true
	b.enrich(testDetails(-1, []Ability{hidden, normal}))
	eq(t, deref(b.AbilityName), "normal", "normal fallback")
	eq(t, b.AbilityIsHidden, false, "not hidden")
	c := generateProfile(4, 0, "hidden-fallback-chain")
	c.AbilitySlot = intPtr(99)
	c.enrich(testDetails(-1, []Ability{hidden}))
	eq(t, deref(c.AbilityName), "hidden", "hidden fallback")
	eq(t, c.AbilityIsHidden, true, "hidden")
}

func TestProfileSanitize(t *testing.T) {
	hostile := func() *profile {
		p := generateProfile(5, 0, "")
		p.Level, p.GrowthTokens = 9999, math.MaxInt64
		p.IVs = ivSet{999, -5, 31, 64, -1, 500}
		for i := 0; i < 400; i++ {
			p.Moves = append(p.Moves, knownMove{"move-" + strconv.Itoa(i) + "-" + strings.Repeat("x", 200), 900 + i})
		}
		return p
	}
	check := func(label string, p *profile) {
		if p.Level != 100 || p.GrowthTokens != maxTokenValue || p.IVs != (ivSet{31, 0, 31, 31, 0, 31}) || len(p.Moves) != 4 || p.InstanceID == "" {
			t.Errorf("%s: %+v", label, p)
		}
		for _, m := range p.Moves {
			if len([]rune(m.Name)) > 80 || m.LearnedAtLevel < 0 || m.LearnedAtLevel > 100 {
				t.Errorf("%s move %+v", label, m)
			}
		}
	}
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:271
	st := freshState()
	st.Active = mon(79, []int{79}, 0, rarityCommon, 1)
	st.Active.Profile = hostile()
	st.sanitize()
	check("active", st.Active.Profile)
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:279
	st = freshState()
	st.Dex = []dexEntry{entry(79, 79, []int{79}, rarityCommon)}
	st.Dex[0].Profile = hostile()
	st.sanitize()
	check("dex", st.Dex[0].Profile)
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:287
	p := generateProfile(5, 0, "low")
	p.Level = -99
	p.sanitize()
	eq(t, p.Level, 5, "hatch minimum")
}

func TestCorruptProfileRegeneratesWithoutDroppingPokemon(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:358
	dir := writeSave(t, `{"active":{"baseID":79,"pathIDs":[79],"plannedPathIDs":[79],"stageIndex":0,"usedAtStage":10,"rarity":"common","totalForms":1,"profile":"broken"},"dex":[{"id":"kept","baseID":1,"finalID":1,"chainOrder":[1],"rarity":"common","profile":"broken"}]}`)
	s := openAt(t, dir, stub(noEvo()), 7)
	if s.st.Active.currentID() != 79 || s.st.Active.Profile == nil || s.st.Dex[0].ID != "kept" || s.st.Dex[0].Profile == nil {
		t.Fatal("profiles not regenerated")
	}
	eq(t, s.st.Dex[0].Profile.Level, 100, "graduated entry at level 100")
	eq(t, s.st.Dex[0].Profile.Seed, fnv1a64("dex:kept:1"), "FNV-1a seed")
	id := s.st.Active.Profile.InstanceID
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:374 — the regenerated individual is persisted.
	eq(t, openAt(t, dir, stub(noEvo()), 7).st.Active.Profile.InstanceID, id, "not regenerated on restart")
}

func TestReleasedLegacyEntryUsesReachedChainAsGrowthUpperBound(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:406
	dir := writeSave(t, `{"dex":[{"id":"released","baseID":1,"finalID":2,"chainOrder":[1,2],"rarity":"common","releasedAt":"2026-01-01T00:00:00Z"}]}`)
	s := openAt(t, dir, stub(noEvo()), 7)
	p := s.st.Dex[0].Profile
	want := reconstructedGrowthTokens(rarityCommon, 2, 1, 0)
	eq(t, p.GrowthTokens, want, "growth")
	if want <= phase(rarityCommon, 3, 0) || p.Level <= 5 || p.Level >= 100 {
		t.Fatalf("growth %d level %d", want, p.Level)
	}
}

func TestDetailLoadEnrichesMatchingIndividuals(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:461
	st := freshState()
	for i, id := range []string{"one", "two"} {
		e := entry(79, 79, []int{79}, rarityCommon)
		e.ID, e.Profile = id, generateProfile(uint64(i+1), 0, id)
		st.Dex = append(st.Dex, e)
	}
	f := stub(noEvo())
	f.detailsFn = func(int) (*Details, error) { return slowpoke(), nil }
	s := openAt(t, writeState(t, st), f, 7)
	v := s.Species(bg, 79)
	eq(t, v.DetailsStatus, "ready", "details")
	for _, e := range s.st.Dex {
		if deref(e.Profile.Gender) != "female" || deref(e.Profile.AbilityName) != "oblivious" || len(e.Profile.Moves) == 0 {
			t.Fatalf("not enriched %+v", e.Profile)
		}
	}
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:485 — a failure leaves nothing cached.
	s2 := newStore(t, stub(noEvo()), 7)
	eq(t, s2.Species(bg, 79).DetailsStatus, "unavailable", "failure")
	if s2.details[79] != nil {
		t.Fatal("failure cached")
	}
}

func TestIndividualsActiveFirstThenNewest(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:431
	st := freshState()
	st.Active = mon(79, []int{79}, 0, rarityCommon, 1)
	st.Active.Profile = generateProfile(1, 0, "active-instance")
	newer, older := entry(79, 79, []int{79}, rarityCommon), entry(79, 79, []int{79}, rarityCommon)
	newer.ID, newer.CaughtAt, newer.Profile = "stored-new", timePtr(fixedNow.Add(20)), generateProfile(2, 0, "stored-new")
	older.ID, older.CaughtAt, older.Profile = "stored-old", timePtr(fixedNow.Add(10)), generateProfile(3, 0, "stored-old")
	st.Dex = []dexEntry{older, newer}
	s := openAt(t, writeState(t, st), stub(noEvo()), 7)
	v := s.Species(bg, 79)
	var ids []string
	for _, i := range v.Individuals {
		ids = append(ids, i.ID)
	}
	if !reflect.DeepEqual(ids, []string{"active-79-79", "stored-new", "stored-old"}) {
		t.Fatalf("order %v", ids)
	}
	if v.Individuals[0].CaughtAt != nil || len(s.Species(bg, 80).Individuals) != 0 {
		t.Fatal("active caughtAt must be null; other species has none")
	}
}

func TestStartupWarmupLoadsOnlyTheActivePokemon(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PokemonProfileTests.swift:499 and :520 (path normalization)
	st := freshState()
	st.Active = &monState{BaseID: 79, PathIDs: []int{79, 999}, PlannedPathIDs: []int{79, 999}, StageIndex: 1, Rarity: rarityCommon, TotalForms: 2}
	st.Dex = []dexEntry{entry(1, 3, []int{1, 2, 3}, rarityCommon)}
	f := &fakeData{
		lineFn:    func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil },
		detailsFn: func(id int) (*Details, error) { return &Details{SpeciesID: id, GenderRate: -1}, nil },
	}
	s := openAt(t, writeState(t, st), f, 7)
	use(s, 0)
	eq(t, s.st.Active.currentID(), 79, "normalized")
	if !ints(f.detailIDs, []int{79}) {
		t.Fatalf("details requested %v", f.detailIDs)
	}
	eq(t, deref(s.st.Active.Profile.Gender), "genderless", "enriched")
}

// growthData mirrors GrowthProfileProvider (ProfileGrowthIntegrationTests.swift:4-22).
func growthData(forms int) *fakeData {
	tree := node(forms)
	for id := forms - 1; id >= 1; id-- {
		tree = node(id, tree)
	}
	return &fakeData{
		lineFn: func(int) (*Line, error) { return makeLine(1, tree, rarityCommon), nil },
		detailsFn: func(id int) (*Details, error) {
			return &Details{SpeciesID: id, GenderRate: -1, Moves: []Move{{"final-" + strconv.Itoa(id), []LearnMethod{{"level-up", 100}}}}}, nil
		},
	}
}

func TestEveryDifficultyAndRepeatHatchGraduatesAtLevel100(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ProfileGrowthIntegrationTests.swift:48
	for _, d := range []float64{0.1, 1, 2} {
		for forms := 1; forms <= 3; forms++ {
			st := freshState()
			st.GrowthDifficulty = d
			s := openAt(t, writeState(t, st), growthData(forms), 7)
			for _, repeated := range []bool{false, true} {
				s.hatch(bg, 1)
				eq(t, s.st.Active.HasGrowthBoost, repeated, "boost")
				eq(t, s.st.Active.Profile.Level, 5, "hatch level")
				if _, err := s.loadDetails(bg, forms); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < forms; i++ {
					s.applyUsage(s.stageThreshold(s.st.Active) - s.st.Active.UsedAtStage)
				}
				if s.st.Active != nil {
					t.Fatalf("d=%v forms=%d not graduated", d, forms)
				}
				p := s.st.Dex[len(s.st.Dex)-1].Profile
				if p.Level != 100 || p.GrowthTokens != 750_000_000 || len(p.Moves) != 1 || p.Moves[0].Name != "final-"+strconv.Itoa(forms) {
					t.Fatalf("d=%v forms=%d profile %+v", d, forms, p)
				}
			}
		}
	}
}

func TestDifficultyChangesAndRestartNeverUndoLevels(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ProfileGrowthIntegrationTests.swift:67
	st := freshState()
	st.GrowthDifficulty = 0.5
	st.CollectedFinals = toSet([]string{"1:1"})
	dir := writeState(t, st)
	s := openAt(t, dir, growthData(1), 7)
	s.hatch(bg, 1)
	s.applyUsage(93_750_000)
	prof := *s.st.Active.Profile
	eq(t, prof.Level, 52, "93.75M at 0.5 with boost")
	s.SetGrowthDifficulty(2)
	eq(t, s.st.Active.Profile.Level, 52, "harder difficulty keeps the level")
	re := openAt(t, dir, growthData(1), 7)
	if re.st.Active.Profile.InstanceID != prof.InstanceID || re.st.Active.Profile.IVs != prof.IVs || re.st.Active.Profile.Level != 52 {
		t.Fatal("restart changed the individual")
	}
	s.SetGrowthDifficulty(0.1)
	thr := s.stageThreshold(s.st.Active)
	if p := float64(s.st.Active.UsedAtStage) / float64(thr); math.Abs(p-0.5) > 1e-6 {
		t.Fatalf("progress %v", p)
	}
	eq(t, s.st.Active.Profile.Level, 52, "easier difficulty keeps the level")
	s.applyUsage(thr - s.st.Active.UsedAtStage)
	eq(t, s.st.Dex[len(s.st.Dex)-1].Profile.Level, 100, "graduated")
}

func TestSmallUsageChunksMatchSingleDelta(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ProfileGrowthIntegrationTests.swift:89
	mk := func() *Store {
		st := freshState()
		st.GrowthDifficulty = 2
		s := openAt(t, writeState(t, st), growthData(3), 7)
		s.hatch(bg, 1)
		return s
	}
	small, large := mk(), mk()
	for i := 0; i < 20; i++ {
		small.applyUsage(1)
	}
	large.applyUsage(20)
	eq(t, small.st.Active.Profile.GrowthTokens, 10, "growth")
	eq(t, small.st.Active.Profile.GrowthTokens, large.st.Active.Profile.GrowthTokens, "same growth")
	eq(t, small.st.Active.Profile.Level, large.st.Active.Profile.Level, "same level")
}

func TestLegacyStageMigrationUsesBoostAndDifficulty(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ProfileGrowthIntegrationTests.swift:101
	st := freshState()
	st.GrowthDifficulty = 0.1
	st.Active = &monState{BaseID: 1, PathIDs: []int{1, 2}, PlannedPathIDs: []int{1, 2, 3}, StageIndex: 1, UsedAtStage: 6_250_000,
		Rarity: rarityCommon, TotalForms: 3, HasGrowthBoost: true}
	s := openAt(t, writeState(t, st), growthData(3), 7)
	eq(t, s.st.Active.Profile.GrowthTokens, 250_000_000, "growth")
	eq(t, s.st.Active.Profile.Level, 36, "level")
}

func TestOfflineOverflowCatchesUpWhenLineLoads(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ProfileGrowthIntegrationTests.swift:111
	st := freshState()
	st.GrowthDifficulty = 0.1
	st.Active = &monState{BaseID: 1, PathIDs: []int{1}, PlannedPathIDs: []int{1, 2, 3}, Rarity: rarityCommon, TotalForms: 3}
	s := openAt(t, writeState(t, st), growthData(3), 7)
	s.applyUsage(75_000_000)
	eq(t, s.st.Active.UsedAtStage, 75_000_000, "banked")
	update(s, map[string]int{}, "day", false)
	if s.st.Active != nil {
		t.Fatal("banked usage did not graduate once the line loaded")
	}
	eq(t, s.st.Dex[0].Profile.Level, 100, "level")
}

// Not a Swift port. A details or type-name fetch that fails when the line loads is retried by the
// next Update, so the card does not stay without types until the next evolution (the daemon's
// memory-only data view misses on every first attempt).
func TestActiveCardDataRetriedAfterFailure(t *testing.T) {
	st := freshState()
	st.Active = &monState{BaseID: 16, PathIDs: []int{16}, PlannedPathIDs: []int{16, 17}, StageIndex: 0, Rarity: rarityCommon, TotalForms: 2}
	online := false
	f := &fakeData{
		lineFn: func(b int) (*Line, error) { return makeLine(b, node(b, node(b+1)), rarityCommon), nil },
		detailsFn: func(id int) (*Details, error) {
			if !online {
				return nil, errOffline
			}
			return &Details{SpeciesID: id, GenderRate: -1, Types: []string{"normal"}}, nil
		},
		namesFn: func(kind, name string) (map[string]string, error) {
			if !online {
				return nil, errOffline
			}
			return map[string]string{"en": "Normal"}, nil
		},
	}
	s := openAt(t, writeState(t, st), f, 7)
	use(s, 0)
	if v := s.BuildState(MoodInput{Now: fixedNow}); len(v.Active.Types) != 0 || !s.warmPending {
		t.Fatalf("offline: types %v, retry pending %t", v.Active.Types, s.warmPending)
	}
	online = true
	use(s, 0)
	v := s.BuildState(MoodInput{Now: fixedNow})
	if len(v.Active.Types) != 1 || v.Active.Types[0].Name != "Normal" || s.warmPending {
		t.Fatalf("after recovery: types %v, retry pending %t", v.Active.Types, s.warmPending)
	}
}

// Not a Swift port. A form name that fails during the egg prefetch is fetched again by the next
// Update, so the hatch event names the form ("[East Sea]", not the identifier "[East]").
func TestPrefetchRetriesMissingFormNames(t *testing.T) {
	pending, form := 422, "east"
	st := freshState()
	st.InstallBaselineSet, st.EggUsage, st.PendingHatchID, st.PendingForm = true, 10, &pending, &form
	online := false
	f := &fakeData{
		lineFn: func(b int) (*Line, error) { return makeLine(b, node(b, node(b+1)), rarityCommon), nil },
		namesFn: func(kind, name string) (map[string]string, error) {
			if !online {
				return nil, errOffline
			}
			return map[string]string{"en": "East Sea"}, nil
		},
	}
	s := openAt(t, writeState(t, st), f, 7)
	use(s, 0)
	if s.prefetchedLineID != 0 {
		t.Fatal("a prefetch with a missing form name must not count as done")
	}
	online = true
	use(s, 0)
	if s.prefetchedLineID != pending {
		t.Fatalf("prefetch not completed: %d", s.prefetchedLineID)
	}
	var hatch *HatchEvent
	for _, e := range use(s, 6_000_000) {
		if h, ok := e.(HatchEvent); ok {
			hatch = &h
		}
	}
	if hatch == nil || hatch.DisplayName != "P422 [East Sea]" {
		t.Fatalf("hatch event = %+v", hatch)
	}
}
