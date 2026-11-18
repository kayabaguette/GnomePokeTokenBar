package companion

import (
	"testing"
)

func TestEggHatchThreshold(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:990 (the inline "1M" comments are stale: 5M)
	s := newStore(t, stub(linear3()), 7)
	use(s, 0)
	use(s, 500_000)
	eq(t, s.st.EggUsage, 500_000, "below threshold")
	s.hatchIfNeeded(bg)
	if s.st.Active != nil {
		t.Fatal("hatched below the threshold")
	}
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1000
	s = newStore(t, stub(linear3()), 7)
	use(s, 0)
	use(s, eggHatchThreshold)
	s.hatchIfNeeded(bg)
	if s.st.Active == nil {
		t.Fatal("no hatch at exactly 5,000,000")
	}
	eq(t, s.st.EggUsage, 0, "egg usage reset")
}

func TestEggOverflowCarriesToHatchedMon(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1077
	s := newStore(t, stub(linear3()), 7)
	use(s, 0)
	use(s, eggHatchThreshold+500_000)
	s.hatchIfNeeded(bg)
	eq(t, s.st.Active.UsedAtStage, 500_000, "overflow")
}

func TestEggHatchesViaRESTFallbackWhenIndexDown(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1086
	f := &fakeData{
		lineFn:    func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil },
		indexErr:  errOffline,
		speciesFn: func(id int) (*Species, error) { return &Species{ID: id, CaptureRate: 100}, nil },
	}
	s := newStore(t, f, 7)
	use(s, 0)
	use(s, eggHatchThreshold)
	s.hatchIfNeeded(bg)
	if s.st.Active == nil {
		t.Fatal("REST fallback did not hatch")
	}
	eq(t, s.st.EggUsage, 0, "egg usage")
	want := int(draws(7, 1)[0]%maxSpeciesID) + 1
	eq(t, s.st.Active.BaseID, want, "first REST draw is the species")
}

func TestNewEggAfterGraduationReincubates(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1097
	s := newStore(t, stub(noEvo()), 7)
	use(s, 0)
	use(s, eggHatchThreshold)
	if s.st.Active == nil {
		t.Fatal("no hatch")
	}
	s.applyUsage(graduationTotal(rarityCommon))
	if s.st.Active != nil {
		t.Fatal("not graduated")
	}
	eq(t, s.st.EggUsage, 0, "new egg")
	s.hatchIfNeeded(bg)
	if s.st.Active != nil {
		t.Fatal("a fresh egg hatched at 0")
	}
}

func TestHatchAssignsDeterministicShinyAndNature(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1637 — hatch(baseID:) draws shiny, then nature.
	for _, seed := range []uint64{1, 7, 42, 12345} {
		s := newStore(t, stub(linear3()), seed)
		d := draws(seed, 2)
		s.hatch(bg, 1)
		eq(t, s.st.Active.IsShiny, d[0]%64 == 0, "shiny")
		eq(t, *s.st.Active.Nature, natures[d[1]%25], "nature")
	}
}

func TestShinyPathReachable(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1648
	var seed uint64
	for seed = 0; seed < 5000 && draws(seed, 1)[0]%64 != 0; seed++ {
	}
	s := newStore(t, stub(linear3()), seed)
	s.hatch(bg, 1)
	eq(t, s.st.Active.IsShiny, true, "shiny")
	ev := s.drain()[0].(HatchEvent)
	eq(t, ev.IsShiny, true, "hatch event shiny")
	eq(t, ev.ShinyOdds, 64, "odds")
}

func TestGraduateCarriesIdentityToDex(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1659
	s := newStore(t, stub(noEvo()), 3)
	s.hatch(bg, 20)
	sh, nat := s.st.Active.IsShiny, *s.st.Active.Nature
	s.applyUsage(graduationTotal(rarityCommon))
	if s.st.Active != nil || len(s.st.Dex) != 1 {
		t.Fatal("not graduated")
	}
	eq(t, s.st.Dex[0].IsShiny, sh, "shiny")
	eq(t, *s.st.Dex[0].Nature, nat, "nature")
}

func TestHatchIfNeededDrawOrderAndOverflowEvolve(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1785 — species, shiny, nature; 135M evolves once.
	var seed uint64
	for seed = 0; seed < 20000 && draws(seed, 2)[1]%64 != 0; seed++ {
	}
	s := newStore(t, stub(linear3()), seed)
	use(s, 0)
	evs := use(s, 135_000_000)
	eq(t, s.st.Active.IsShiny, true, "shiny on the second draw")
	eq(t, s.st.Active.StageIndex, 1, "overflow evolved once")
	if len(evs) != 2 || evs[0].EventKind() != "hatch" || evs[1].EventKind() != "evolve" {
		t.Fatalf("events %v", evs)
	}
}

func TestHatchInstantGraduation(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1807
	s := newStore(t, stub(noEvo()), 11)
	use(s, 0)
	evs := use(s, 800_000_000)
	if s.st.Active != nil || len(s.st.Dex) != 1 {
		t.Fatal("expected an instant graduation")
	}
	if len(evs) < 2 || evs[0].EventKind() != "hatch" || evs[1].EventKind() != "graduate" {
		t.Fatalf("events %v", evs)
	}
}

func TestSamplerWeightedPick(t *testing.T) {
	ready := func() state {
		st := freshState()
		st.InstallBaselineSet, st.LastDate, st.EggUsage = true, "d1", eggHatchThreshold+1
		return st
	}
	cases := []struct {
		name      string
		index     []BaseEntry
		collected []string
		pick      func(v uint64) int
	}{
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1838
		{"equal weights", []BaseEntry{{10, 100}, {20, 100}, {30, 100}}, nil,
			func(v uint64) int { return []int{10, 20, 30}[v%300/100] }},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1854
		{"capture rate is weight", []BaseEntry{{100, 254}, {200, 2}}, nil,
			func(v uint64) int {
				if v%256 < 254 {
					return 100
				}
				return 200
			}},
		// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1874
		{"collected weight halves", []BaseEntry{{1, 200}, {2, 200}}, []string{"1:1"},
			func(v uint64) int {
				if v%300 < 100 {
					return 1
				}
				return 2
			}},
	}
	for _, c := range cases {
		hits := map[int]bool{}
		for seed := uint64(0); seed < 400; seed++ {
			st := ready()
			st.CollectedFinals = toSet(c.collected)
			f := &fakeData{index: c.index, lineFn: func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil }}
			s := openAt(t, writeState(t, st), f, seed)
			s.hatchIfNeeded(bg)
			want := c.pick(draws(seed, 1)[0])
			if s.st.Active == nil || s.st.Active.BaseID != want {
				t.Fatalf("%s seed %d: got %v, want %d", c.name, seed, s.st.Active, want)
			}
			hits[want] = true
		}
		if len(hits) != len(c.index) {
			t.Errorf("%s: only %v reached", c.name, hits)
		}
	}
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1874 — the boundary seed moves with collection.
	var seed uint64
	for seed = 0; seed < 5000; seed++ {
		v := draws(seed, 1)[0]
		if v%400 < 200 && v%300 >= 100 {
			break
		}
	}
	f := func() *fakeData {
		return &fakeData{index: []BaseEntry{{1, 200}, {2, 200}},
			lineFn: func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil }}
	}
	st := ready()
	s := openAt(t, writeState(t, st), f(), seed)
	s.hatchIfNeeded(bg)
	eq(t, s.st.Active.BaseID, 1, "uncollected")
	st.CollectedFinals = toSet([]string{"1:1"})
	s = openAt(t, writeState(t, st), f(), seed)
	s.hatchIfNeeded(bg)
	eq(t, s.st.Active.BaseID, 2, "collected base halves")
}

func TestEggPrefetchStoresPendingAndHatchUsesIt(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1900
	st := freshState()
	st.InstallBaselineSet, st.LastDate = true, "d1"
	f := &fakeData{index: []BaseEntry{{77, 255}}, lineFn: func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil }}
	rng := &countingRng{SplitMix64: SplitMix64{State: 5}}
	s := openAt(t, writeState(t, st), f, 0, withRng(rng))
	use(s, 1_000)
	if s.st.PendingHatchID == nil || *s.st.PendingHatchID != 77 {
		t.Fatalf("pending %v", s.st.PendingHatchID)
	}
	eq(t, rng.calls, 1, "prefetch draws the species only")
	use(s, 6_000_000)
	eq(t, s.st.Active.BaseID, 77, "hatch uses pending")
	if s.st.PendingHatchID != nil {
		t.Fatal("pending not cleared")
	}
	// shiny, nature, profile seed; no species re-roll, single-node plan, no forms.
	eq(t, rng.calls, 4, "draws after prefetch")
}

func TestFailedHatchRetriesAfterProviderRecovers(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1920
	st := freshState()
	st.InstallBaselineSet, st.LastDate, st.EggUsage = true, "d1", eggHatchThreshold+1
	f := &fakeData{index: []BaseEntry{{88, 255}}, indexErr: errOffline}
	s := openAt(t, writeState(t, st), f, 9)
	s.hatchIfNeeded(bg)
	if s.st.Active != nil || !s.hatchDelayed {
		t.Fatal("expected a delayed hatch")
	}
	if !s.BuildState(MoodInput{Now: fixedNow}).Egg.HatchDelayed {
		t.Fatal("hatchDelayed not exposed")
	}
	f.indexErr = nil
	f.lineFn = func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil }
	s.hatchIfNeeded(bg)
	eq(t, s.st.Active.BaseID, 88, "retry hatched")
	eq(t, s.hatchDelayed, false, "delay cleared")
}

func TestSamplerOfflineKeepsEgg(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:1938
	st := freshState()
	st.InstallBaselineSet, st.LastDate, st.EggUsage = true, "d1", eggHatchThreshold+1
	s := openAt(t, writeState(t, st), &fakeData{indexErr: errOffline}, 1)
	s.hatchIfNeeded(bg)
	if s.st.Active != nil || s.st.EggUsage < eggHatchThreshold {
		t.Fatal("egg progress lost")
	}
}

func TestDittoFlagGatesTheDisguiseDraw(t *testing.T) {
	// Swift: CompanionStore.swift:1788-1791 — the draw happens only when the flag is on.
	for _, enabled := range []bool{false, true} {
		rng := &countingRng{SplitMix64: SplitMix64{State: 7}}
		opts := []storeOpt{withRng(rng)}
		if enabled {
			opts = append(opts, withDitto())
		}
		s := newStore(t, stub(linear3()), 0, opts...)
		s.hatch(bg, 1)
		want := 5 // shiny, nature, plan nodes 1 and 2, profile seed
		if enabled {
			want = 6
		}
		eq(t, rng.calls, want, "draws")
	}
	// With the flag on and a hit, the active Pokémon is disguised and stops at its first threshold.
	var seed uint64
	for seed = 0; seed < 100000 && draws(seed, 3)[2]%128 != 0; seed++ {
	}
	s := newStore(t, stub(linear3()), seed, withDitto())
	s.hatch(bg, 1)
	if s.st.Active.DittoDisguise == nil {
		t.Fatal("expected a disguise")
	}
	s.applyUsage(300_000_000)
	eq(t, s.st.Active.StageIndex, 0, "a disguise never evolves while the reveal is deferred")
	eq(t, len(s.st.Dex), 0, "nor graduates")
}

func TestDisguisedShinyStaysHidden(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:619 and DittoTests.swift:123
	for _, revealed := range []bool{false, true} {
		st := freshState()
		m := mon(1, []int{1}, 0, rarityCommon, 3)
		m.IsShiny, m.DittoDisguise, m.DittoRevealed = true, intPtr(1), revealed
		st.Active = m
		s := openAt(t, writeState(t, st), stub(linear3()), 7)
		sp := s.collectedDexSpecies(false)[0]
		eq(t, sp.isShiny, revealed, "dex shiny")
		eq(t, sp.hasNormal, !revealed, "dex normal")
		eq(t, s.BuildState(MoodInput{Now: fixedNow}).Active.IsShiny, revealed, "card shiny")
	}
}
