package companion

import (
	"math"
	"testing"
)

func TestPhaseSumsEqualGraduationTotal(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:8
	for _, r := range rarities {
		T := graduationTotal(r)
		for k := 1; k <= 3; k++ {
			sum := 0
			for i := 0; i < k; i++ {
				sum += phase(r, k, i)
			}
			if d := sum - T; d < -2 || d > 2 {
				t.Errorf("%s k=%d sum=%d T=%d", r, k, sum, T)
			}
		}
	}
}

func TestRepeatGrowthHalvesEveryPhase(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:18
	for _, r := range rarities {
		for k := 1; k <= 3; k++ {
			for i := 0; i < k; i++ {
				want := int(math.Round(float64(phase(r, k, i)) / 2))
				eq(t, phaseThreshold(r, k, i, repeatGrowthMultiplier), want, r)
			}
		}
	}
	eq(t, phaseThreshold(rarityCommon, 3, 0, repeatGrowthMultiplier), 62_500_000, "common k=3 boosted")
}

func TestHigherStageAndRarerCostMore(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:37
	for k := 2; k <= 3; k++ {
		for i := 0; i < k-1; i++ {
			if phase(rarityCommon, k, i) >= phase(rarityCommon, k, i+1) {
				t.Errorf("k=%d stage %d not cheaper than next", k, i)
			}
		}
	}
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:46
	order := []string{rarityCommon, rarityUncommon, rarityRare, rarityLegendary}
	for i := 0; i < 3; i++ {
		if graduationTotal(order[i]) >= graduationTotal(order[i+1]) {
			t.Errorf("%s not cheaper than %s", order[i], order[i+1])
		}
	}
}

func TestDefaultDifficultyTable(t *testing.T) {
	// companion.md §2.3 default-difficulty table (CM:114-131)
	cases := []struct {
		r    string
		k    int
		want []int
	}{
		{rarityCommon, 1, []int{750_000_000}},
		{rarityCommon, 2, []int{250_000_000, 500_000_000}},
		{rarityCommon, 3, []int{125_000_000, 250_000_000, 375_000_000}},
		{rarityUncommon, 3, []int{312_500_000, 625_000_000, 937_500_000}},
		{rarityRare, 2, []int{1_000_000_000, 2_000_000_000}},
		{rarityLegendary, 3, []int{1_000_000_000, 2_000_000_000, 3_000_000_000}},
	}
	for _, c := range cases {
		for i, w := range c.want {
			eq(t, phase(c.r, c.k, i), w, c.r)
		}
	}
}

func TestRarityDerivation(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:51
	eq(t, rarityFrom(255, false, false), rarityCommon, "255")
	eq(t, rarityFrom(90, false, false), rarityUncommon, "90")
	eq(t, rarityFrom(45, false, false), rarityRare, "45")
	eq(t, rarityFrom(3, true, false), rarityLegendary, "legendary")
	eq(t, rarityFrom(255, false, true), rarityLegendary, "mythical")
	eq(t, rarityFrom(120, false, false), rarityUncommon, "120 ceiling")
	eq(t, rarityFrom(121, false, false), rarityCommon, "121")
}

func TestClampDifficulty(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:221
	cases := []struct{ in, want float64 }{
		{0, 0.1}, {-5, 0.1}, {20, 2.0}, {math.NaN(), 1}, {math.Inf(1), 1}, {math.Inf(-1), 1}, {0.5, 0.5},
	}
	for _, c := range cases {
		if got := clampDifficulty(c.in); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("clamp(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNothingCollapsesToZeroAtMinimumDifficulty(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:242
	if scaled(eggHatchThreshold, minDifficulty) <= 0 {
		t.Fatal("egg threshold collapsed")
	}
	eq(t, scaled(eggHatchThreshold, minDifficulty), 500_000, "egg at 0.1")
	for _, r := range rarities {
		for k := 1; k <= 3; k++ {
			for i := 0; i < k; i++ {
				if scaled(phase(r, k, i), minDifficulty) <= 0 {
					t.Errorf("%s k=%d i=%d collapsed", r, k, i)
				}
			}
		}
	}
}

func TestSplitMix64KnownValues(t *testing.T) {
	// Reference SplitMix64 outputs (seed 0 first value is the published 0xE220A8397B1DCDAF).
	eq(t, draws(0, 1)[0], uint64(0xE220A8397B1DCDAF), "seed 0")
	eq(t, profileRNG(0).Next(), (&SplitMix64{State: 0x9E3779B97F4A7C15}).Next(), "ProfileRNG seed 0 uses the gamma")
}

func TestCollectionWeight(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:135
	for w := 0; w <= 255; w++ {
		eq(t, collectionWeight(w, false), max(1, w), "uncollected")
		eq(t, collectionWeight(w, true), max(1, w/2), "collected")
	}
}

func TestDittoDisguiseHit(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DittoTests.swift:72-94
	cases := []struct {
		r     string
		forms int
		roll  uint64
		want  bool
	}{
		{rarityCommon, 2, 0, true}, {rarityCommon, 3, 128, true}, {rarityCommon, 3, 256, true},
		{rarityCommon, 3, 1, false}, {rarityCommon, 3, 127, false}, {rarityCommon, 3, 129, false},
		{rarityCommon, 1, 0, false},
		{rarityUncommon, 3, 0, false}, {rarityRare, 3, 0, false}, {rarityLegendary, 3, 0, false},
	}
	for _, c := range cases {
		eq(t, dittoDisguiseHit(c.r, c.forms, c.roll), c.want, c.r)
	}
	eq(t, dittoDisguiseDenominator, 128, "denominator")
}

func TestThresholdRoundingChain(t *testing.T) {
	// companion.md §2.3 (CM:123-131, CS:169-171): std, then boost, then difficulty, each rounded half
	// away from zero. A 7-form line makes every step fractional: 750M·3/28 = 80,357,142.86.
	st := freshState()
	st.GrowthDifficulty = 0.3
	s := openAt(t, writeState(t, st), &fakeData{}, 7)
	m := &monState{Rarity: rarityCommon, TotalForms: 7, StageIndex: 2}
	eq(t, phaseThreshold(rarityCommon, 7, 2, 1), 80_357_143, "std")
	eq(t, phaseThreshold(rarityCommon, 7, 2, 2), 40_178_572, "boost rounds 40,178,571.5 up")
	eq(t, s.stageThreshold(m), 24_107_143, "difficulty on std")
	m.HasGrowthBoost = true
	eq(t, s.stageThreshold(m), 12_053_572, "difficulty on the rounded boost, not 12,053,571")
	eq(t, s.eggThreshold(), 1_500_000, "egg at 0.3")
}
