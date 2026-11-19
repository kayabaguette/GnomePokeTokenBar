package companion

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// Ported from PremiumEggTests.swift (docs/spec/bag-shop.md §5.4). Each test keeps its Swift
// file:line; "derived" marks a value upstream does not assert (SplitMix64 and the documented draw
// order). PremiumEggTests.swift:377 (save import) waits for save transfer.

// tieredData is TieredProvider: four bases whose line rarity follows the same capture-rate rule,
// #4 legendary (PremiumEggTests.swift:15-37).
func tieredData() *fakeData {
	idx := []BaseEntry{{1, 255}, {2, 100}, {3, 30}, {4, 3}}
	return &fakeData{
		index: idx,
		lineFn: func(b int) (*Line, error) {
			for _, e := range idx {
				if e.ID == b {
					return &Line{BaseID: b, Tree: node(b), CaptureRate: e.CaptureRate, IsLegendary: b == 4,
						Names: map[int]map[string]string{b: {"en": "M" + strconv.Itoa(b)}}}, nil
				}
			}
			return nil, errOffline
		},
	}
}

func restCaptureRate(id int) int {
	switch {
	case id%3 == 0:
		return 30
	case id%2 == 0:
		return 100
	}
	return 255
}

// restOnlyData is RestOnlyTieredProvider: the index fails and every id is a base whose capture
// rate follows its id (PremiumEggTests.swift:42-54).
func restOnlyData() *fakeData {
	return &fakeData{
		indexErr: errOffline,
		speciesFn: func(id int) (*Species, error) {
			return &Species{ID: id, CaptureRate: restCaptureRate(id)}, nil
		},
		lineFn: func(b int) (*Line, error) {
			return &Line{BaseID: b, Tree: node(b), CaptureRate: restCaptureRate(b), Names: map[int]map[string]string{b: {"en": "M"}}}, nil
		},
	}
}

// lyingData is LyingIndexProvider: the index says rare (30), the line is common (:57-64).
func lyingData() *fakeData {
	return &fakeData{
		index:  []BaseEntry{{7, 30}},
		lineFn: func(int) (*Line, error) { return makeLine(7, node(7), rarityCommon), nil },
	}
}

// premiumActive mirrors PremiumEggTests.activeStore: base 10 active and a wallet.
func premiumActive(t *testing.T, used int, dir string) *Store {
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(walletJSON(used, 0, mon10(false), "")), 0o600); err != nil {
		t.Fatal(err)
	}
	return openAt(t, dir, tieredData(), 7)
}

// premiumEgg mirrors PremiumEggTests.eggStore: a ready egg (5M) carrying a tier ("" = none).
func premiumEgg(t *testing.T, tier string, seed uint64, data DataSource) *Store {
	tj := "null"
	if tier != "" {
		tj = strconv.Quote(tier)
	}
	return openAt(t, writeSave(t, walletJSON(10_000_000, 0, "null", fmt.Sprintf(`,"eggUsage":%d,"eggTier":%s`, eggHatchThreshold, tj))), data, seed)
}

func hatchedRarity(s *Store) string {
	s.hatchIfNeeded(bg)
	if s.st.Active == nil {
		return ""
	}
	return s.st.Active.Rarity
}

func TestCaptureRateCeilingIsTheSameThresholdAsClassification(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:100
	for cr := 0; cr <= 255; cr++ {
		classified := rarityFrom(cr, false, false)
		for _, tier := range []string{rarityCommon, rarityUncommon, rarityRare} {
			if tierIncludes(tier, cr) != (rarityRank(classified) >= rarityRank(tier)) {
				t.Fatalf("cr %d tier %s: includes %t, classified %s", cr, tier, tierIncludes(tier, cr), classified)
			}
		}
	}
}

func TestLegendaryHasNoCaptureRateCeilingButPassesLowerTierFilters(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:113
	if _, ok := captureRateCeiling(rarityLegendary); ok {
		t.Fatal("legendary has a ceiling")
	}
	eq(t, tierIncludes(rarityLegendary, 3), false, "legendary includes")
	for _, cr := range []int{3, 30, 45} {
		eq(t, tierIncludes(rarityRare, cr), true, fmt.Sprint("rare ", cr))
		eq(t, tierIncludes(rarityUncommon, cr), true, fmt.Sprint("uncommon ", cr))
	}
	eq(t, isEggShopTier(rarityLegendary), false, "no legendary egg on sale")
}

func TestEggPricesFollowGraduationTotalRatio(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:126
	eq(t, eggBasePrice(""), 1_000_000_000, "plain")
	eq(t, eggBasePrice(rarityUncommon), 2_500_000_000, "uncommon")
	eq(t, eggBasePrice(rarityRare), 4_000_000_000, "rare")
	if !reflect.DeepEqual(eggShopTiers, []string{"", rarityUncommon, rarityRare}) {
		t.Fatalf("tiers = %v", eggShopTiers)
	}
}

func TestRareEggIsNotDominatedAtMeasuredPoolComposition(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:148
	c, u, r, l := 37_240.0, 3_135.0, 3_053.0, 339.0
	_ = c
	share := (r + l) / (u + r + l)
	uncPrice, rarePrice := float64(eggBasePrice(rarityUncommon)), float64(eggBasePrice(rarityRare))
	if !(rarePrice < uncPrice/share) || !(share < uncPrice/rarePrice) {
		t.Fatalf("share %v, prices %v %v", share, uncPrice, rarePrice)
	}
	boosted := (r + l) / (u*0.70 + r + l)
	if !(boosted < uncPrice/rarePrice) {
		t.Fatalf("boosted share %v", boosted)
	}
}

func TestBuyPremiumEggRecordsGuaranteeAndDebitsTierPrice(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:176
	s := premiumActive(t, 20_000_000_000, "")
	eq(t, s.canBuyEgg(rarityRare), true, "canBuy")
	p, err := s.BuyEgg(rarityRare)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, deref(s.st.EggTier), rarityRare, "tier recorded")
	eq(t, deref(s.BuildState(MoodInput{Now: fixedNow}).Egg.Guarantee), rarityRare, "guarantee")
	eq(t, deref(p.Tier), rarityRare, "receipt tier")
	eq(t, s.st.SpentTokens, 4_000_000_000, "spent")
	if s.st.Active != nil || s.st.EggUsage != 0 || len(s.st.Dex) != 1 || s.st.Dex[0].ReleasedAt == nil || len(s.st.CollectedFinals) != 0 {
		t.Fatalf("after purchase: %+v", s.st)
	}
}

func TestCannotBuyAnyEggWhileIncubating(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:192
	s := premiumEgg(t, "", 1, tieredData())
	for _, tier := range eggShopTiers {
		eq(t, s.canBuyEgg(tier), false, "canBuy "+tier)
		_, err := s.BuyEgg(tier)
		eq(t, errCode(err), "noActive", "buy "+tier)
	}
	eq(t, s.st.SpentTokens, 0, "spent")
}

func TestFundsAreCheckedAgainstTierPrice(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:203
	s := premiumActive(t, 3_000_000_000, "")
	eq(t, s.canBuyEgg(""), true, "plain")
	eq(t, s.canBuyEgg(rarityUncommon), true, "uncommon")
	eq(t, s.canBuyEgg(rarityRare), false, "rare")
	_, err := s.BuyEgg(rarityRare)
	eq(t, errCode(err), "insufficientFunds", "buy rare")
	if s.st.Active == nil || s.st.SpentTokens != 0 {
		t.Fatal("state changed")
	}
}

func TestPurchaseStartsFromCleanRollState(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:219
	s := premiumActive(t, 20_000_000_000, "")
	if s.st.PendingHatchID != nil {
		t.Fatal("pre-roll with an active Pokémon")
	}
	if _, err := s.BuyEgg(rarityRare); err != nil {
		t.Fatal(err)
	}
	if deref(s.st.EggTier) != rarityRare || s.st.PendingHatchID != nil || s.st.EggUsage != 0 {
		t.Fatalf("%+v", s.st)
	}
}

func TestGuaranteeSurvivesRestart(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:229
	dir := t.TempDir()
	if _, err := premiumActive(t, 20_000_000_000, dir).BuyEgg(rarityUncommon); err != nil {
		t.Fatal(err)
	}
	s2 := openAt(t, dir, tieredData(), 7)
	eq(t, deref(s2.st.EggTier), rarityUncommon, "tier")
	eq(t, deref(s2.BuildState(MoodInput{Now: fixedNow}).Egg.Guarantee), rarityUncommon, "guarantee")
}

func TestWeightedRollNeverGoesBelowGuarantee(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:242
	var guaranteed, control []string
	for seed := uint64(1); seed <= 20; seed++ {
		if r := hatchedRarity(premiumEgg(t, rarityRare, seed, tieredData())); r != "" {
			guaranteed = append(guaranteed, r)
		}
		if r := hatchedRarity(premiumEgg(t, "", seed, tieredData())); r != "" {
			control = append(control, r)
		}
	}
	eq(t, len(guaranteed), 20, "every rare egg hatched")
	for _, r := range guaranteed {
		if rarityRank(r) < rarityRank(rarityRare) {
			t.Fatalf("rare egg hatched %s", r)
		}
		eq(t, r, rarityRare, "derived pin: all rare for seeds 1–20")
	}
	found := false
	for _, r := range control {
		found = found || r == rarityCommon
	}
	eq(t, found, true, "the control hatches common")
}

func TestUncommonEggHatchesUncommonAndExcludesOnlyCommon(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:267
	var got []string
	for seed := uint64(1); seed <= 20; seed++ {
		r := hatchedRarity(premiumEgg(t, rarityUncommon, seed, tieredData()))
		if r == "" || r == rarityCommon {
			t.Fatalf("seed %d hatched %q", seed, r)
		}
		got = append(got, r[:1])
	}
	pin := fmt.Sprint(got)
	if want := "[u r u r u u r u u u r r r u u u u u u u]"; pin != want { // derived pin
		t.Fatalf("rarities %s, want %s", pin, want)
	}
}

func TestLegendaryStillReachableFromPremiumEgg(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:281
	first := 0
	for seed := uint64(1); seed <= 60; seed++ {
		if hatchedRarity(premiumEgg(t, rarityRare, seed, tieredData())) == rarityLegendary {
			first = int(seed)
			break
		}
	}
	if first == 0 {
		t.Fatal("no legendary from a rare egg")
	}
	eq(t, first, 42, "derived pin: first legendary seed")
}

func TestRestFallbackRespectsGuarantee(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:293
	var picks []int
	for seed := uint64(1); seed <= 10; seed++ {
		s := premiumEgg(t, rarityRare, seed, restOnlyData())
		if r := hatchedRarity(s); r != "" {
			eq(t, r, rarityRare, fmt.Sprint("seed ", seed))
			picks = append(picks, s.st.Active.BaseID)
		}
	}
	if len(picks) == 0 {
		t.Fatal("the fallback never hatched")
	}
	eq(t, fmt.Sprint(picks), "[111 555 225 99 600 552 399 54 81 606]", "derived pin")
}

func TestRestFallbackHonoursUncommonTierWithoutOverFiltering(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:307
	sawUncommon := false
	for seed := uint64(1); seed <= 20; seed++ {
		r := hatchedRarity(premiumEgg(t, rarityUncommon, seed, restOnlyData()))
		if r == rarityCommon {
			t.Fatalf("seed %d hatched common", seed)
		}
		sawUncommon = sawUncommon || r == rarityUncommon
	}
	eq(t, sawUncommon, true, "uncommon hatched")
}

func TestRestFallbackWithoutGuaranteeCanHatchCommon(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:320
	sawCommon := false
	for seed := uint64(1); seed <= 20; seed++ {
		if hatchedRarity(premiumEgg(t, "", seed, restOnlyData())) == rarityCommon {
			sawCommon = true
			break
		}
	}
	eq(t, sawCommon, true, "common hatched")
	// derived pins
	for seed, want := range map[uint64]int{3: 225, 1: 340, 11: 329} {
		s := premiumEgg(t, "", seed, restOnlyData())
		hatchedRarity(s)
		eq(t, s.st.Active.BaseID, want, fmt.Sprint("seed ", seed))
	}
}

func TestHatchDiscardsSpeciesBelowGuaranteeWhenIndexIsStale(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:334
	r := &countingRng{SplitMix64: SplitMix64{State: 1}}
	s := premiumEgg(t, rarityRare, 1, lyingData())
	s.rng = r
	s.hatchIfNeeded(bg)
	if s.st.Active != nil || s.st.PendingHatchID != nil || deref(s.st.EggTier) != rarityRare || s.st.EggUsage != eggHatchThreshold {
		t.Fatalf("%+v", s.st)
	}
	eq(t, r.calls, 1, "derived: only the species draw; the gate draws nothing")
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).Egg.HatchDelayed, true, "derived: delayed while ready")
	eq(t, len(s.drain()), 0, "no event")
	// Derived: a pre-rolled species below the tier is dropped the same way.
	s.st.PendingHatchID = intPtr(7)
	s.hatchIfNeeded(bg)
	if s.st.Active != nil || s.st.PendingHatchID != nil {
		t.Fatal("pre-rolled species below the tier hatched")
	}
}

func TestSameSpeciesHatchesNormallyWithoutGuarantee(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:344
	eq(t, hatchedRarity(premiumEgg(t, "", 1, lyingData())), rarityCommon, "control")
}

func TestHatchConsumesGuarantee(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:352
	s := premiumEgg(t, rarityRare, 1, tieredData())
	s.hatchIfNeeded(bg)
	if s.st.Active == nil || s.st.EggTier != nil {
		t.Fatalf("%+v", s.st)
	}
	if v := s.BuildState(MoodInput{Now: fixedNow}); v.Egg != nil {
		t.Fatal("egg view while active")
	}
}

func TestGuaranteeDoesNotSurviveIntoTheNextEgg(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:362
	s := premiumEgg(t, rarityRare, 1, tieredData())
	s.hatchIfNeeded(bg)
	a := s.st.Active
	if a == nil || s.st.EggTier != nil {
		t.Fatal("hatch failed")
	}
	s.applyUsage(graduationTotal(a.Rarity) * 2)
	if s.st.Active != nil || len(s.st.Dex) != 1 || s.st.EggTier != nil {
		t.Fatalf("%+v", s.st)
	}
	if g := s.BuildState(MoodInput{Now: fixedNow}).Egg.Guarantee; g != nil {
		t.Fatalf("guarantee %v", *g)
	}
}

func TestSanitizeKeepsGuaranteeAndPreRollOfAnEgg(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:390 (the active half is covered by
	// TestExtremeValuesAreClampedOnLoad)
	st := freshState()
	st.EggTier, st.PendingHatchID = sp(rarityRare), intPtr(3)
	st.sanitize()
	if deref(st.EggTier) != rarityRare || st.PendingHatchID == nil || *st.PendingHatchID != 3 {
		t.Fatalf("%+v", st)
	}
}

func TestUnsatisfiableGuaranteeIsNormalizedAwayAndEggStillHatches(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:413
	s := premiumEgg(t, rarityLegendary, 1, tieredData())
	if s.st.EggTier != nil {
		t.Fatal("legendary tier kept")
	}
	s.hatchIfNeeded(bg)
	if s.st.Active == nil {
		t.Fatal("egg did not hatch")
	}
}

func TestCannotBuyTierThatIsNotSold(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:422
	s := premiumActive(t, 20_000_000_000, "")
	eq(t, s.canBuyEgg(rarityLegendary), false, "legendary")
	_, err := s.BuyEgg(rarityLegendary)
	eq(t, errCode(err), "badArgs", "buy legendary")
	eq(t, s.canBuyEgg(rarityCommon), false, "common is sold as the plain egg only")
	if s.st.SpentTokens != 0 || s.st.Active == nil {
		t.Fatal("state changed")
	}
}

func TestUnknownGuaranteeDecodesAsNoGuarantee(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/PremiumEggTests.swift:433
	st, _ := decodeState([]byte(`{"installBaselineSet":true,"usedSinceInstall":0,"spentTokens":0,"lastDate":"d","dex":[],"collectedFinals":[],"eggTier":"mythic"}`))
	if st.EggTier != nil {
		t.Fatalf("tier %v", *st.EggTier)
	}
}

// Derived (bag-shop.md §1.8): a tier the index cannot satisfy keeps the egg without any draw, and
// the prefetch rolls inside the tier too.
func TestEmptyTierPoolKeepsTheEggWithoutDrawing(t *testing.T) {
	r := &countingRng{SplitMix64: SplitMix64{State: 1}}
	data := &fakeData{index: []BaseEntry{{1, 255}}, lineFn: func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil }}
	s := premiumEgg(t, rarityRare, 1, data)
	s.rng = r
	s.hatchIfNeeded(bg)
	if s.st.Active != nil || r.calls != 0 || !s.hatchDelayed {
		t.Fatalf("active %v draws %d delayed %t", s.st.Active, r.calls, s.hatchDelayed)
	}
	pre := premiumEgg(t, rarityRare, 1, tieredData())
	pre.st.EggUsage = 0
	pre.prefetchEgg(bg)
	if p := pre.st.PendingHatchID; p == nil || (*p != 3 && *p != 4) {
		t.Fatalf("prefetched %v", pre.st.PendingHatchID)
	}
}
