package companion

import (
	"math"
	"testing"
)

// diffStore mirrors DifficultyTests.store: a saved baseline at a growth difficulty.
func diffStore(t *testing.T, growth float64) *Store {
	st := freshState()
	st.InstallBaselineSet, st.LastDate, st.GrowthDifficulty = true, "d", clampDifficulty(growth)
	return openAt(t, writeState(t, st), stub(linear3()), 7)
}

func hatched(t *testing.T, growth float64) *Store {
	s := diffStore(t, growth)
	s.hatch(bg, 1)
	return s
}

func tokensToNext(s *Store) int { return max(0, s.stageThreshold(s.st.Active)-s.st.Active.UsedAtStage) }

func TestRepeatBoostComposesWithDifficultyAndLiveChanges(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:39
	s := hatched(t, 0.5)
	s.applyUsage(graduationTotal(rarityCommon) / 2)
	if s.st.Active != nil {
		t.Fatal("not graduated at half difficulty")
	}
	s.hatch(bg, 1)
	eq(t, s.st.Active.HasGrowthBoost, true, "boost")
	base := phase(rarityCommon, 3, 0)
	eq(t, s.stageThreshold(s.st.Active), base/4, "boost and 0.5 compose")
	s.applyUsage(base/4 - 1)
	eq(t, s.st.Active.StageIndex, 0, "one short")
	eq(t, tokensToNext(s), 1, "tokens to next")
	s.applyUsage(1)
	eq(t, s.st.Active.StageIndex, 1, "evolved")
	eq(t, s.st.Active.UsedAtStage, 0, "no carry")
	stageOne := s.stageThreshold(s.st.Active)
	s.applyUsage(stageOne / 2)
	s.SetGrowthDifficulty(0.25)
	eq(t, s.st.Active.StageIndex, 1, "rescale never evolves")
	if p := float64(s.st.Active.UsedAtStage) / float64(s.stageThreshold(s.st.Active)); math.Abs(p-0.5) > 1e-6 {
		t.Fatalf("progress %v", p)
	}
	s.applyUsage(tokensToNext(s))
	eq(t, s.st.Active.StageIndex, 2, "evolved again")
	s.applyUsage(tokensToNext(s))
	if s.st.Active != nil || s.st.Dex[len(s.st.Dex)-1].FinalID != 3 {
		t.Fatal("not graduated as #3")
	}
}

func TestSameTokensEvolveOnlyAtLowerDifficulty(t *testing.T) {
	half := phase(rarityCommon, 3, 0) / 2
	full := phase(rarityCommon, 3, 0)
	cases := []struct {
		growth float64
		usage  int
		stage  int
	}{
		// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:67
		{1.0, half, 0},
		{0.5, half, 1},
		// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:82
		{1.0, full, 1},
		{2.0, full, 0},
	}
	for _, c := range cases {
		s := hatched(t, c.growth)
		s.applyUsage(c.usage)
		eq(t, s.st.Active.StageIndex, c.stage, "stage")
	}
}

func TestDisplayedThresholdMatchesEvolution(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:96
	s := hatched(t, 0.5)
	shown := tokensToNext(s)
	eq(t, shown, phase(rarityCommon, 3, 0)/2, "scaled")
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Active.Threshold, shown, "view threshold")
	s.applyUsage(shown - 1)
	eq(t, s.st.Active.StageIndex, 0, "one short")
	eq(t, tokensToNext(s), 1, "remaining")
	s.applyUsage(1)
	eq(t, s.st.Active.StageIndex, 1, "evolved")
}

func TestEggHatchPointMovesWithDifficulty(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:111
	half := eggHatchThreshold / 2
	for _, c := range []struct {
		growth float64
		hatch  bool
	}{{1.0, false}, {0.5, true}} {
		s := diffStore(t, c.growth)
		update(s, map[string]int{"t": 0}, "d1", true)
		update(s, map[string]int{"t": half}, "d1", true)
		s.hatchIfNeeded(bg)
		eq(t, s.st.Active != nil, c.hatch, "hatched")
	}
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:132
	eq(t, diffStore(t, 0.1).BuildState(MoodInput{Now: fixedNow}).Egg.Threshold, 500_000, "scaled hatch point")
	eq(t, diffStore(t, 1.0).eggThreshold(), eggHatchThreshold, "default")
}

func TestLoweringDifficultyPreservesIncompleteStage(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:188
	s := hatched(t, 1.0)
	s.applyUsage(phase(rarityCommon, 3, 0) - 1)
	s.SetGrowthDifficulty(0.5)
	eq(t, s.st.Active.StageIndex, 0, "not evolved by the setting")
	eq(t, tokensToNext(s), 1, "one token left")
	s.applyUsage(0)
	eq(t, s.st.Active.StageIndex, 0, "still incomplete")
	s.applyUsage(1)
	eq(t, s.st.Active.StageIndex, 1, "evolves on real usage")
}

func TestDifficultyPersistsAndClamps(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:210
	dir := t.TempDir()
	s := openAt(t, dir, stub(linear3()), 7)
	s.SetGrowthDifficulty(0.3)
	eq(t, openAt(t, dir, stub(linear3()), 7).GrowthDifficulty(), 0.3, "persisted")
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:234
	s.SetGrowthDifficulty(0)
	eq(t, s.GrowthDifficulty(), minDifficulty, "zero clamps")
	s.SetGrowthDifficulty(math.NaN())
	eq(t, s.GrowthDifficulty(), defaultDifficulty, "NaN resets")
	z := hatched(t, 0)
	if tokensToNext(z) <= 0 || z.eggThreshold() <= 0 {
		t.Fatal("zero threshold")
	}
}

func TestEggRescaleKeepsFraction(t *testing.T) {
	// companion.md §2.4: only the egg's credits are rescaled; lifetime and ledger are untouched.
	s := diffStore(t, 1.0)
	s.st.EggUsage, s.st.UsedSinceInstall = 2_500_000, 9
	s.SetGrowthDifficulty(2)
	eq(t, s.st.EggUsage, 5_000_000, "half of 10M")
	eq(t, s.st.UsedSinceInstall, 9, "lifetime untouched")
	s.st.EggUsage = 9_999_999
	s.SetGrowthDifficulty(0.1)
	eq(t, s.st.EggUsage, 499_999, "an incomplete egg stays incomplete")
}
