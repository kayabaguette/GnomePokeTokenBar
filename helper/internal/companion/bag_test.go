package companion

import (
	"fmt"
	"math"
	"strconv"
	"testing"
)

// Ported from MintTests (use half), RareCandyTests (use half) and ProfileGrowthIntegrationTests
// (docs/spec/bag-shop.md §5.4). Each test keeps its Swift file:line; "derived" marks a value
// upstream does not assert.

// mintStore mirrors MintTests.store: an active base-1 Pokémon (common, k = 3) with a nature (nil:
// none), Mint stock and a wallet. The line provider throws (MintNoProvider).
func mintStore(t *testing.T, nature *string, mint, used, usedAtStage int, shinyMon bool, seed uint64) *Store {
	n := ""
	if nature != nil {
		n = `,"nature":"` + *nature + `"`
	}
	m := fmt.Sprintf(`{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":%d,"rarity":"common","totalForms":3,"isShiny":%t%s}`, usedAtStage, shinyMon, n)
	extra := ""
	if mint > 0 {
		extra = fmt.Sprintf(`,"inventory":{"mint":%d}`, mint)
	}
	return openAt(t, writeSave(t, walletJSON(used, 0, m, extra)), noData(), seed)
}

func mintUse(t *testing.T, s *Store) string {
	t.Helper()
	n, err := s.UseMint()
	if err != nil {
		t.Fatalf("UseMint: %v", err)
	}
	return n
}

func TestUseMintChangesNatureToDifferent(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:35
	s := mintStore(t, sp("adamant"), 1, 1_000_000_000, 50_000_000, false, 7)
	n := mintUse(t, s)
	if n == "adamant" || *s.st.Active.Nature != n || s.itemCount(itemMint) != 0 {
		t.Fatalf("nature %s → %s, mint %d", "adamant", n, s.itemCount(itemMint))
	}
	eq(t, n, "mild", "derived pin, seed 7")
}

func TestMintNeverRepeatsCurrentAcrossUses(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:46
	s := mintStore(t, sp("adamant"), 6, 1_000_000_000, 50_000_000, false, 7)
	var seq []string
	for i := 0; i < 6; i++ {
		before := *s.st.Active.Nature
		n := mintUse(t, s)
		if n == before {
			t.Fatalf("use %d repeated %s", i, n)
		}
		seq = append(seq, n)
	}
	eq(t, fmt.Sprint(seq), "[mild serious rash adamant hasty lax]", "derived pin, seed 7")
}

func TestUseMintFromNilNatureSetsValid(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:57
	s := mintStore(t, nil, 1, 1_000_000_000, 50_000_000, false, 7)
	if s.st.Active.Nature != nil {
		t.Fatal("nature must start nil")
	}
	n := mintUse(t, s)
	if !validNature(n) || *s.st.Active.Nature != n {
		t.Fatalf("nature %q", n)
	}
	eq(t, n, "serious", "derived pin: draw % 25")
}

func TestUseMintDoesNotAffectGrowthOrIdentity(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:66
	s := mintStore(t, sp("adamant"), 1, 1_000_000_000, 50_000_000, true, 7)
	stage, species := s.st.Active.StageIndex, s.st.Active.currentID()
	mintUse(t, s)
	a := s.st.Active
	if a.UsedAtStage != 50_000_000 || a.StageIndex != stage || a.currentID() != species || !a.displaysShiny() || s.st.UsedSinceInstall != 1_000_000_000 {
		t.Fatalf("mint changed growth or identity: %+v used %d", a, s.st.UsedSinceInstall)
	}
}

func TestCannotUseMintOnEgg(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:81
	s := openAt(t, writeSave(t, `{"installBaselineSet":true,"usedSinceInstall":1,"lastDate":"d","dex":[],"collectedFinals":[],"inventory":{"mint":2}}`), noData(), 1)
	eq(t, s.canUseMint(), false, "canUse")
	_, err := s.UseMint()
	eq(t, errCode(err), "noActive", "use")
	eq(t, s.itemCount(itemMint), 2, "stock kept")
}

func TestCannotUseMintWithoutStock(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:93
	s := mintStore(t, sp("adamant"), 0, 1_000_000_000, 50_000_000, false, 7)
	eq(t, s.canUseMint(), false, "canUse")
	_, err := s.UseMint()
	eq(t, errCode(err), "noStock", "use")
	eq(t, *s.st.Active.Nature, "adamant", "unchanged")
}

func TestMintFeedbackComesFromTheResult(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:101 (Go: the useMint reply carries the nature;
	// the one-shot sparkle is the extension's)
	s := mintStore(t, sp("adamant"), 1, 1_000_000_000, 50_000_000, false, 7)
	n := mintUse(t, s)
	eq(t, deref(s.BuildState(MoodInput{Now: fixedNow}).Active.Nature), n, "the state shows the new nature")
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Bag.CanUseMint, false, "stock spent")
}

func TestUseMintPersistsAcrossRestart(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:113
	dir := writeSave(t, `{"installBaselineSet":true,"usedSinceInstall":1,"lastDate":"d","active":{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":3,"nature":"adamant"},"inventory":{"mint":2},"dex":[],"collectedFinals":[]}`)
	n := mintUse(t, openAt(t, dir, noData(), 3))
	s2 := openAt(t, dir, noData(), 3)
	eq(t, *s2.st.Active.Nature, n, "nature persisted")
	eq(t, s2.itemCount(itemMint), 1, "stock persisted")
	eq(t, n, "sassy", "derived pin, seed 3")
}

// Derived (decision §14.15): the Mint works while the line is loading and draws exactly once.
func TestMintWorksWithoutLineInOneDraw(t *testing.T) {
	r := &countingRng{SplitMix64: SplitMix64{State: 7}}
	s := mintStore(t, sp("adamant"), 1, 1_000_000_000, 0, false, 7)
	s.rng = r
	if s.line != nil {
		t.Fatal("line must not be loaded")
	}
	mintUse(t, s)
	eq(t, r.calls, 1, "draws")
}

// ---------------------------------------------------------------- Rare Candy

// giveCandies seeds the grant feature, then grants n session candies (RareCandyTests.swift:280-285).
func giveCandies(s *Store, n int) {
	s.GrantCandies(nil, true)
	for i := 0; i < n; i++ {
		s.GrantCandies([]CandyWindow{{Key: "test.session." + strconv.Itoa(i), Kind: CandySession, Utilization: 100}}, true)
	}
}

func candy(t *testing.T, s *Store, count int) string {
	t.Helper()
	u, _, err := s.UseCandy(count)
	if err != nil {
		return errCode(err)
	}
	return u.Result
}

func mustPlan(t *testing.T, s *Store, count int) CandyPlan {
	t.Helper()
	p := s.PlanCandies(count)
	if p == nil {
		t.Fatalf("PlanCandies(%d) = nil", count)
	}
	return *p
}

func TestBatchCarriesXPThroughMultipleEvolutions(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:369
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	s.applyUsage(100_000_000)
	giveCandies(s, 10)
	before := s.st.UsedSinceInstall
	p := mustPlan(t, s, 3)
	eq(t, s.MaxCandyUse(), 7, "maxUse")
	eq(t, p, CandyPlan{Count: 3, XP: 300_000_000, Evolves: true, CarryoverXP: 25_000_000}, "plan")
	u, evs, err := s.UseCandy(3)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, u, CandyUse{Consumed: 3, XP: 300_000_000, Result: CandyEvolved}, "use (feedback 300M)")
	eq(t, s.st.Active.StageIndex, 2, "stage")
	eq(t, s.st.Active.UsedAtStage, p.CarryoverXP, "carry")
	eq(t, s.itemCount(itemRareCandy), 7, "stock")
	eq(t, s.st.UsedSinceInstall, before, "usedSinceInstall unchanged")
	eq(t, len(evs), 2, "derived: two evolve events returned")
}

func TestBatchCapsAtGraduationAndPreservesUnusedCandies(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:390
	for _, remaining := range []int{250_000_000, 300_000_000} {
		s := newStore(t, stub(noEvo()), 7)
		s.hatch(bg, 20)
		s.applyUsage(750_000_000 - remaining)
		giveCandies(s, 5)
		eq(t, s.MaxCandyUse(), 3, "maxUse")
		p := mustPlan(t, s, 5)
		if p.Count != 3 || !p.Graduates || p.DiscardedXP != 300_000_000-remaining {
			t.Fatalf("remaining %d: plan %+v", remaining, p)
		}
		u, evs, err := s.UseCandy(5)
		if err != nil || u.Result != CandyGraduated || u.Consumed != 3 {
			t.Fatalf("use = %+v, %v", u, err)
		}
		if len(evs) != 1 || evs[0].EventKind() != "graduate" {
			t.Fatalf("events = %v", evs)
		}
		eq(t, s.itemCount(itemRareCandy), 2, "stock")
		eq(t, s.st.EggUsage, 0, "egg usage")
		eq(t, len(s.st.Dex), 1, "dex")
		eq(t, s.MaxCandyUse(), 0, "maxUse after")
		eq(t, candy(t, s, 5), "noActive", "unavailable after")
	}
}

func TestBatchCapsAtInventoryAndRejectsNonpositiveAmounts(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:410
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	giveCandies(s, 1)
	for _, c := range []int{0, -1, math.MinInt} {
		if s.PlanCandies(c) != nil {
			t.Fatalf("plan(%d) not nil", c)
		}
		eq(t, candy(t, s, c), "badArgs", fmt.Sprint("use ", c))
	}
	eq(t, s.itemCount(itemRareCandy), 1, "stock")
	eq(t, s.MaxCandyUse(), 1, "maxUse")
	p := mustPlan(t, s, math.MaxInt)
	if p.Count != 1 || p.Evolves {
		t.Fatalf("plan(MaxInt) = %+v", p)
	}
	// derived: no carryover is reported without an evolution (protocol §7.4)
	eq(t, p, CandyPlan{Count: 1, XP: rareCandyXP}, "plan(MaxInt)")
	eq(t, candy(t, s, math.MaxInt), CandyProgressed, "use(MaxInt)")
	eq(t, s.st.Active.UsedAtStage, rareCandyXP, "usedAtStage")
	eq(t, s.itemCount(itemRareCandy), 0, "stock")
}

func TestBatchPreviewMatchesGrowthWithDifficultyAndRepeatBonus(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:428
	// The comparison logic is upstream's; the exact values are derived (bag-shop.md §5.4 matrix).
	type row struct {
		costs      [3]int
		first      CandyPlan
		afterStage int // -1: graduated by the first candy
		final      CandyPlan
		stock      int
	}
	matrix := map[[2]any]row{
		{0.1, false}: {[3]int{12_500_000, 25_000_000, 37_500_000}, CandyPlan{1, rareCandyXP, true, true, 0, 37_499_999}, -1, CandyPlan{}, 19},
		{0.1, true}:  {[3]int{6_250_000, 12_500_000, 18_750_000}, CandyPlan{1, rareCandyXP, true, true, 0, 68_749_999}, -1, CandyPlan{}, 19},
		{0.5, false}: {[3]int{62_500_000, 125_000_000, 187_500_000}, CandyPlan{1, rareCandyXP, true, false, 99_999_999, 0}, 1, CandyPlan{3, 300_000_000, true, true, 0, 87_499_999}, 16},
		{0.5, true}:  {[3]int{31_250_000, 62_500_000, 93_750_000}, CandyPlan{1, rareCandyXP, true, false, 37_499_999, 0}, 2, CandyPlan{1, rareCandyXP, false, true, 0, 43_749_999}, 18},
		{1.0, false}: {[3]int{125_000_000, 250_000_000, 375_000_000}, CandyPlan{1, rareCandyXP, true, false, 99_999_999, 0}, 1, CandyPlan{6, 600_000_000, true, true, 0, 74_999_999}, 13},
		{1.0, true}:  {[3]int{62_500_000, 125_000_000, 187_500_000}, CandyPlan{1, rareCandyXP, true, false, 99_999_999, 0}, 1, CandyPlan{3, 300_000_000, true, true, 0, 87_499_999}, 16},
		{2.0, false}: {[3]int{250_000_000, 500_000_000, 750_000_000}, CandyPlan{1, rareCandyXP, true, false, 99_999_999, 0}, 1, CandyPlan{12, 1_200_000_000, true, true, 0, 49_999_999}, 7},
		{2.0, true}:  {[3]int{125_000_000, 250_000_000, 375_000_000}, CandyPlan{1, rareCandyXP, true, false, 99_999_999, 0}, 1, CandyPlan{6, 600_000_000, true, true, 0, 74_999_999}, 13},
	}
	for _, d := range []float64{0.1, 0.5, 1.0, 2.0} {
		for _, boosted := range []bool{false, true} {
			want := matrix[[2]any{d, boosted}]
			label := fmt.Sprintf("d=%v boost=%t", d, boosted)
			st := freshState()
			st.GrowthDifficulty = d
			dir := writeState(t, st)
			s := openAt(t, dir, stub(linear3()), 7)
			s.hatch(bg, 1)
			if boosted {
				s.applyUsage(10_000_000_000)
				s.hatch(bg, 1)
			}
			costs := s.candyStageCosts()
			if len(costs) != 3 || [3]int(costs) != want.costs {
				t.Fatalf("%s: stage costs %v", label, costs)
			}
			s.applyUsage(s.stageThreshold(s.st.Active) - 1)
			giveCandies(s, 20)
			p := mustPlan(t, s, 1)
			eq(t, p, want.first, label+" plan(1)")
			result := candy(t, s, 1)
			if p.Graduates {
				eq(t, result, CandyGraduated, label)
				if s.st.Active != nil {
					t.Fatalf("%s: still active", label)
				}
			} else {
				eq(t, result, CandyEvolved, label)
				eq(t, s.st.Active.UsedAtStage, p.CarryoverXP, label+" carry")
				eq(t, s.st.Active.StageIndex, want.afterStage, label+" stage")
				final := mustPlan(t, s, 20)
				if !final.Graduates {
					t.Fatalf("%s: plan(20) does not graduate", label)
				}
				eq(t, final, want.final, label+" plan(20)")
				stock := s.itemCount(itemRareCandy)
				eq(t, candy(t, s, 20), CandyGraduated, label+" use(20)")
				eq(t, s.itemCount(itemRareCandy), stock-final.Count, label+" stock")
			}
			eq(t, s.itemCount(itemRareCandy), want.stock, label+" stock after")
			re := openAt(t, dir, stub(linear3()), 7)
			eq(t, re.itemCount(itemRareCandy), s.itemCount(itemRareCandy), label+" reloaded stock")
			if re.st.Active != nil {
				t.Fatalf("%s: reloaded active", label)
			}
			eq(t, len(re.st.Dex), len(s.st.Dex), label+" reloaded dex")
		}
	}
}

func TestCandyUseProgressesWithoutEvolution(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:469
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	giveCandies(s, 1)
	before := s.st.UsedSinceInstall
	eq(t, candy(t, s, 1), CandyProgressed, "result")
	eq(t, s.st.Active.UsedAtStage, rareCandyXP, "usedAtStage")
	eq(t, s.st.Active.StageIndex, 0, "stage")
	eq(t, s.itemCount(itemRareCandy), 0, "stock")
	eq(t, s.st.UsedSinceInstall, before, "usedSinceInstall")
}

func TestCandyUseEvolvesWhenCrossingThreshold(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:484
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	s.applyUsage(50_000_000)
	giveCandies(s, 1)
	eq(t, candy(t, s, 1), CandyEvolved, "result")
	eq(t, s.st.Active.currentID(), 2, "species")
	eq(t, s.st.Active.StageIndex, 1, "stage")
}

func TestSingleCandyAdvancesAtMostOneStage(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:496
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	s.applyUsage(124_000_000)
	giveCandies(s, 1)
	candy(t, s, 1)
	eq(t, s.st.Active.StageIndex, 1, "at most one stage")
}

func TestSingleCandyAdvancesAtMostOneBoostedStage(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:505
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	for stage := 0; stage < 3; stage++ {
		s.applyUsage(phase(rarityCommon, 3, stage))
	}
	s.hatch(bg, 1)
	eq(t, s.st.Active.HasGrowthBoost, true, "boost")
	s.applyUsage(s.stageThreshold(s.st.Active) - 1)
	giveCandies(s, 1)
	eq(t, candy(t, s, 1), CandyEvolved, "result")
	eq(t, s.st.Active.StageIndex, 1, "stage")
	eq(t, s.st.Active.UsedAtStage, rareCandyXP-1, "usedAtStage")
	if s.st.Active.UsedAtStage >= s.stageThreshold(s.st.Active) {
		t.Fatal("must stay below the next threshold")
	}
}

func TestCandyUseGraduatesFinalStage(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:526
	s := newStore(t, stub(noEvo()), 7)
	s.hatch(bg, 20)
	s.applyUsage(700_000_000)
	giveCandies(s, 1)
	eq(t, candy(t, s, 1), CandyGraduated, "result")
	if s.st.Active != nil || len(s.st.Dex) != 1 {
		t.Fatal("not graduated into the dex")
	}
}

func TestCandyGraduationTurnsTheMenuSubjectIntoTheEgg(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:541 (Go: the next state shows the egg; the
	// daemon half is TestUseCandyGraduationEmitsEggState)
	s := newStore(t, stub(noEvo()), 7)
	s.hatch(bg, 20)
	s.applyUsage(700_000_000)
	giveCandies(s, 1)
	eq(t, candy(t, s, 1), CandyGraduated, "result")
	if ms := s.BuildState(MoodInput{Now: fixedNow}).MenuSubject; !ms.Egg || ms.SpeciesID != nil {
		t.Fatalf("menu subject = %+v", ms)
	}
}

func TestCannotUseCandyOnEgg(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:557
	s := newStore(t, stub(linear3()), 7)
	giveCandies(s, 2)
	eq(t, s.MaxCandyUse(), 0, "canUse")
	eq(t, candy(t, s, 1), "noActive", "use")
	eq(t, s.itemCount(itemRareCandy), 2, "stock kept")
}

func TestCannotUseCandyWithoutStock(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:567
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	eq(t, s.MaxCandyUse(), 0, "canUse")
	eq(t, candy(t, s, 1), "noStock", "use")
}

func TestCannotUseCandyWhileLineUnloaded(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:577
	s := openAt(t, writeSave(t, `{"installBaselineSet":true,"lastDate":"d1","active":{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":3},"inventory":{"rareCandy":1},"candyFeatureSeeded":true,"dex":[],"collectedFinals":[]}`), noData(), 1)
	use(s, 0) // the line fetch fails
	if s.st.Active == nil || s.line != nil {
		t.Fatal("want an active Pokémon without its line")
	}
	eq(t, s.MaxCandyUse(), 0, "canUse")
	eq(t, candy(t, s, 1), "notReady", "use")
	eq(t, s.itemCount(itemRareCandy), 1, "stock kept")
	eq(t, deref(s.BuildState(MoodInput{Now: fixedNow}).Bag.CandyBlocked), candyBlockedLineLoading, "blocked reason")
}

func TestUseCandyReportsTheConsumedXP(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:592 (Go: the reply's xp drives "+XP")
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	giveCandies(s, 1)
	u, _, err := s.UseCandy(1)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, u.XP, rareCandyXP, "xp")
}

func TestOwnedItemsReflectsStock(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:603
	s := newStore(t, stub(linear3()), 7)
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Inventory, Inventory{}, "empty")
	giveCandies(s, 3)
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Inventory, Inventory{RareCandy: 3}, "three candies")
}

func TestSequentialCandyUseMatchesDemo(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/RareCandyTests.swift:612
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	s.applyUsage(100_000_000)
	giveCandies(s, 3)
	eq(t, candy(t, s, 1), CandyEvolved, "first")
	eq(t, s.st.Active.StageIndex, 1, "stage 1")
	eq(t, s.st.Active.UsedAtStage, 75_000_000, "carry 75M") // derived: 200M − 125M
	eq(t, candy(t, s, 1), CandyProgressed, "second")
	eq(t, s.st.Active.StageIndex, 1, "still stage 1")
	eq(t, candy(t, s, 1), CandyEvolved, "third")
	eq(t, s.st.Active.StageIndex, 2, "stage 2")
	eq(t, s.itemCount(itemRareCandy), 0, "stock")
}

func TestRareCandyFinalizesGraduationProfile(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ProfileGrowthIntegrationTests.swift:150
	st := freshState()
	st.GrowthDifficulty = 0.1
	st.Inventory[itemRareCandy] = 1
	s := openAt(t, writeState(t, st), growthData(3), 7)
	s.hatch(bg, 1)
	if _, err := s.loadDetails(bg, 3); err != nil {
		t.Fatal(err)
	}
	candy(t, s, 1)
	if s.st.Active != nil {
		t.Fatal("not graduated")
	}
	p := s.st.Dex[len(s.st.Dex)-1].Profile
	if p.Level != 100 || len(p.Moves) != 1 || p.Moves[0].Name != "final-3" {
		t.Fatalf("profile %+v", p)
	}
}

// Derived (protocol §5.4 Bag): candyBlocked is evaluated in order and candyMaxUse is 0 exactly when
// it is set.
func TestCandyBlockedOrder(t *testing.T) {
	check := func(s *Store, want string) {
		t.Helper()
		b := s.BuildState(MoodInput{Now: fixedNow}).Bag
		eq(t, deref(b.CandyBlocked), want, "candyBlocked")
		eq(t, b.CandyMaxUse == 0, b.CandyBlocked != nil, "candyMaxUse is 0 iff blocked")
	}
	egg := newStore(t, stub(linear3()), 7)
	giveCandies(egg, 1)
	check(egg, candyBlockedEgg)
	s := newStore(t, stub(linear3()), 7)
	s.hatch(bg, 1)
	check(s, candyBlockedNoStock)
	giveCandies(s, 1)
	check(s, "<nil>")
	s.line = nil
	check(s, candyBlockedLineLoading)
}

// Go-only guards (bag-shop.md §5.4, "port the guards now"): a disguised, unrevealed Ditto at its
// threshold accepts no candy (revealPending, reserved in M2), and one below it spends only the
// candies its reveal needs. The Ditto reveal itself is deferred, so the DittoTests are not ported.
func TestDisguisedDittoCandyGuards(t *testing.T) {
	m := `{"baseID":1,"pathIDs":[1],"plannedPathIDs":[1,2,3],"stageIndex":0,"usedAtStage":%d,"rarity":"common","totalForms":3,"dittoDisguise":1,"dittoRevealed":false}`
	at := func(used int) *Store {
		s := openAt(t, writeSave(t, fmt.Sprintf(`{"installBaselineSet":true,"lastDate":"d1","active":`+m+`,"inventory":{"rareCandy":5},"candyFeatureSeeded":true}`, used)), stub(linear3()), 7)
		use(s, 0) // loads the line
		if s.line == nil {
			t.Fatal("line not loaded")
		}
		return s
	}
	pending := at(125_000_000)
	b := pending.BuildState(MoodInput{Now: fixedNow}).Bag
	if deref(b.CandyBlocked) != candyBlockedRevealPending || b.CandyMaxUse != 0 || pending.candyStageCosts() != nil {
		t.Fatalf("reveal pending: %+v", b)
	}
	eq(t, candy(t, pending, 1), "notReady", "use while the reveal is pending")
	eq(t, pending.itemCount(itemRareCandy), 5, "stock kept")

	below := at(25_000_000)
	eq(t, below.MaxCandyUse(), 5, "the picker matches the apparent Pokémon")
	u, _, err := below.UseCandy(5)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, u, CandyUse{Consumed: 1, XP: rareCandyXP, Result: CandyProgressed}, "only the reveal's candies")
	eq(t, below.itemCount(itemRareCandy), 4, "stock")
	eq(t, below.st.Active.UsedAtStage, 125_000_000, "stops at the reveal")
}
