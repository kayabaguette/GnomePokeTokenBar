package companion

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

// Ported from ShopTests, MintTests (shop half), ShinyCharmTests, FreshEggTests, DifficultyTests and
// DifficultySaveTests (shop half), PreciousCompanionGuardTests and the release tests of
// CompanionTests, EggPrayerTests and UnownTests (docs/spec/bag-shop.md §5.4). Each test keeps its
// Swift file:line; "derived" marks a value upstream does not assert.

// walletJSON mirrors the Swift suites' JSON seed (ShopTests.swift:19-27): a baseline, a wallet,
// an optional active Pokémon (JSON or "null") and extra top-level keys.
func walletJSON(used, spent int, active, extra string) string {
	return fmt.Sprintf(`{"installBaselineSet":true,"usedSinceInstall":%d,"spentTokens":%d,"lastDate":"d","active":%s,"dex":[],"collectedFinals":[]%s}`,
		used, spent, active, extra)
}

// mon10 is the active Pokémon of the Shop, FreshEgg and PremiumEgg seeds: base 10, common, k = 3,
// 200M at stage 0.
func mon10(shiny bool) string {
	return fmt.Sprintf(`{"baseID":10,"pathIDs":[10],"stageIndex":0,"usedAtStage":200000000,"rarity":"common","totalForms":3,"isShiny":%t}`, shiny)
}

// noData is the Swift ShopNoProvider: the line throws, the index is empty, no species is a base.
func noData() *fakeData { return &fakeData{} }

func shopStore(t *testing.T, used, spent int, extra string) *Store {
	return openAt(t, writeSave(t, walletJSON(used, spent, "null", extra)), noData(), 1)
}

func bought(t *testing.T, s *Store, item string, count int) bool {
	t.Helper()
	before := s.st
	p, err := s.Buy(item, count)
	if err != nil {
		if s.st.SpentTokens != before.SpentTokens {
			t.Fatalf("failed buy %s×%d changed spentTokens", item, count)
		}
		return false
	}
	eq(t, p.Item, item, "receipt item")
	eq(t, p.Count, count, "receipt count")
	eq(t, p.Wallet, s.wallet(), "receipt wallet")
	eq(t, p.Owned, s.itemCount(item), "receipt owned")
	return true
}

func errCode(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return fmt.Sprint(err)
}

// entryKeys names the Shop rows: "item:<kind>" or "egg:<tier>".
func entryKeys(v ShopView) []string {
	var out []string
	for _, e := range v.Entries {
		if e.Kind == "item" {
			out = append(out, "item:"+*e.Item)
		} else {
			out = append(out, "egg:"+deref(e.Tier))
		}
	}
	return out
}

// purchasable is purchasableItems: the Shop's item rows in display order (CS:1280-1290).
func purchasable(s *Store) []string {
	var out []string
	for _, e := range s.shopView().Entries {
		if e.Kind == "item" {
			out = append(out, *e.Item)
		}
	}
	return out
}

func ascending(t *testing.T, v ShopView) {
	t.Helper()
	for i := 1; i < len(v.Entries); i++ {
		if v.Entries[i].Price < v.Entries[i-1].Price {
			t.Fatalf("prices not ascending: %v", entryKeys(v))
		}
	}
}

var defaultShopOrder = []string{"item:mint", "item:rareCandy", "egg:<nil>", "egg:uncommon", "item:shinyCharm", "egg:rare"}

func TestWallet(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:31
	eq(t, shopStore(t, 1_000_000_000, 0, "").wallet(), 1_000_000_000, "nothing spent")
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:35
	eq(t, shopStore(t, 1_000_000_000, 300_000_000, "").wallet(), 700_000_000, "spent subtracted")
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:40
	eq(t, shopStore(t, 100_000_000, 500_000_000, "").wallet(), 0, "never negative")
}

func TestSpentTokensDecodeAndRoundTrip(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:45
	st, ok := decodeState([]byte(`{"installBaselineSet":true,"usedSinceInstall":900,"lastDate":"d","dex":[]}`))
	if !ok || st.SpentTokens != 0 || st.UsedSinceInstall != 900 {
		t.Fatalf("decode without spentTokens: %+v", st)
	}
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:52
	st = freshState()
	st.UsedSinceInstall, st.SpentTokens = 1000, 400
	data, err := encodeState(&st)
	if err != nil {
		t.Fatal(err)
	}
	round, _ := decodeState(data)
	eq(t, round.SpentTokens, 400, "round trip")
}

func TestCanBuyBoundary(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:62
	eq(t, shopStore(t, rareCandyPrice, 0, "").canBuy(itemRareCandy), true, "exact price")
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:66
	eq(t, shopStore(t, rareCandyPrice-1, 0, "").canBuy(itemRareCandy), false, "one below")
}

func TestBuyDebitsWalletAndCreditsInventory(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:72
	s := shopStore(t, 1_000_000_000, 0, "")
	eq(t, bought(t, s, itemRareCandy, 1), true, "buy")
	eq(t, s.itemCount(itemRareCandy), 1, "candy")
	eq(t, s.st.SpentTokens, rareCandyPrice, "spent")
	eq(t, s.wallet(), 1_000_000_000-rareCandyPrice, "wallet")
	eq(t, s.st.UsedSinceInstall, 1_000_000_000, "growth meter unchanged")
}

func TestBuyInsufficientIsNoOp(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:82
	s := shopStore(t, 400_000_000, 0, "")
	_, err := s.Buy(itemRareCandy, 1)
	eq(t, errCode(err), "insufficientFunds", "code")
	eq(t, s.itemCount(itemRareCandy), 0, "candy")
	eq(t, s.st.SpentTokens, 0, "spent")
}

func TestMultipleBuysUntilBroke(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:90
	s := shopStore(t, 1_200_000_000, 0, "")
	eq(t, bought(t, s, itemRareCandy, 1), true, "first")
	eq(t, bought(t, s, itemRareCandy, 1), true, "second")
	eq(t, bought(t, s, itemRareCandy, 1), false, "third")
	eq(t, s.itemCount(itemRareCandy), 2, "candy")
	eq(t, s.st.SpentTokens, 2*rareCandyPrice, "spent")
	eq(t, s.wallet(), 200_000_000, "wallet")
}

func TestBuyAddsToExistingStock(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:101
	s := shopStore(t, 1_000_000_000, 0, `,"inventory":{"rareCandy":3}`)
	eq(t, bought(t, s, itemRareCandy, 1), true, "buy")
	eq(t, s.itemCount(itemRareCandy), 4, "candy")
	inv := s.BuildState(MoodInput{Now: fixedNow}).Inventory
	eq(t, inv, Inventory{RareCandy: 4}, "inventory view (ownedItems first = rareCandy ×4)")
}

func TestBuyPersistsAcrossRestart(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:110
	dir := writeSave(t, walletJSON(1_000_000_000, 0, "null", ""))
	eq(t, bought(t, openAt(t, dir, noData(), 1), itemRareCandy, 1), true, "buy")
	s2 := openAt(t, dir, noData(), 1)
	eq(t, s2.itemCount(itemRareCandy), 1, "stock persisted")
	eq(t, s2.st.SpentTokens, rareCandyPrice, "spent persisted")
	eq(t, s2.wallet(), 1_000_000_000-rareCandyPrice, "wallet")
}

func TestMaxBuyCount(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:127
	eq(t, shopStore(t, 1_200_000_000, 0, "").maxBuyCount(itemRareCandy), 2, "candy")
	eq(t, shopStore(t, 1_200_000_000, 0, "").maxBuyCount(itemMint), 12, "mint")
	eq(t, shopStore(t, rareCandyPrice-1, 0, "").maxBuyCount(itemRareCandy), 0, "below price")
	eq(t, shopStore(t, 0, 0, "").maxBuyCount("pokeBall"), 0, "not sold") // derived: an unsold item
}

func TestMaxBuyCountCapsPassiveAtOne(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:134
	s := shopStore(t, 10*shinyCharmPrice, 0, "")
	eq(t, s.maxBuyCount(itemShinyCharm), 1, "passive max")
	_, err := s.Buy(itemShinyCharm, 2)
	eq(t, errCode(err), "badArgs", "passive ×2 (protocol §7.4)")
	eq(t, s.st.SpentTokens, 0, "spent")
	eq(t, bought(t, s, itemShinyCharm, 1), true, "buy one")
	eq(t, s.maxBuyCount(itemShinyCharm), 0, "owned")
}

func TestBuyCountDebitsTotalAndCreditsInventory(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:144
	s := shopStore(t, 2_000_000_000, 0, `,"inventory":{"rareCandy":1}`)
	p, err := s.Buy(itemRareCandy, 3)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, s.itemCount(itemRareCandy), 4, "candy")
	eq(t, s.st.SpentTokens, 3*rareCandyPrice, "spent")
	eq(t, p.Spent, 3*rareCandyPrice, "receipt spent")
	eq(t, s.wallet(), 2_000_000_000-3*rareCandyPrice, "wallet")
	eq(t, s.st.UsedSinceInstall, 2_000_000_000, "growth meter unchanged")
}

func TestBuyExactlyMaxCountEmptiesWallet(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:154
	s := shopStore(t, 3*rareCandyPrice, 0, "")
	eq(t, bought(t, s, itemRareCandy, s.maxBuyCount(itemRareCandy)), true, "buy max")
	eq(t, s.itemCount(itemRareCandy), 3, "candy")
	eq(t, s.wallet(), 0, "wallet")
}

func TestBuyCountAboveAffordableIsNoOp(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:162
	s := shopStore(t, 1_200_000_000, 0, "")
	_, err := s.Buy(itemRareCandy, 3)
	eq(t, errCode(err), "insufficientFunds", "no partial buy")
	eq(t, s.itemCount(itemRareCandy), 0, "candy")
	eq(t, s.st.SpentTokens, 0, "spent")
}

func TestBuyNonPositiveCountIsNoOp(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:170
	s := shopStore(t, 1_000_000_000, 0, "")
	for _, c := range []int{0, -1} {
		_, err := s.Buy(itemRareCandy, c)
		eq(t, errCode(err), "badArgs", fmt.Sprint("count ", c))
	}
	eq(t, s.itemCount(itemRareCandy), 0, "candy")
	eq(t, s.st.SpentTokens, 0, "spent")
}

func TestBuyCountPersistsAcrossRestart(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:179
	dir := writeSave(t, walletJSON(1_000_000_000, 0, "null", ""))
	eq(t, bought(t, openAt(t, dir, noData(), 1), itemMint, 4), true, "buy")
	s2 := openAt(t, dir, noData(), 1)
	eq(t, s2.itemCount(itemMint), 4, "stock persisted")
	eq(t, s2.st.SpentTokens, 4*mintPrice, "spent persisted")
}

// Protocol §7.4: the buyItem validation order. Derived: upstream's buy() only returns false.
func TestBuyErrorOrder(t *testing.T) {
	s := shopStore(t, 10*shinyCharmPrice, 0, `,"inventory":{"shinyCharm":1}`)
	for _, c := range []struct {
		item  string
		count int
		code  string
	}{
		{"pokeBall", 1, "badArgs"}, {itemMint, 0, "badArgs"}, {itemShinyCharm, 2, "badArgs"},
		{itemShinyCharm, 1, "alreadyOwned"}, {itemRareCandy, 61, "insufficientFunds"},
	} {
		_, err := s.Buy(c.item, c.count)
		eq(t, errCode(err), c.code, fmt.Sprintf("%s×%d", c.item, c.count))
		if err.Error() == "" {
			t.Error("empty message")
		}
	}
	eq(t, s.st.SpentTokens, 0, "nothing applied")
}

func TestItemsSortedByPriceAscending(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:195
	s := shopStore(t, 0, 0, "")
	if got := purchasable(s); !reflect.DeepEqual(got, []string{itemMint, itemRareCandy, itemShinyCharm}) {
		t.Fatalf("purchasable = %v", got)
	}
	ascending(t, s.shopView())
}

func TestOwnedPassiveSinksToBottom(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:204
	s := shopStore(t, 0, 0, `,"inventory":{"shinyCharm":1}`)
	if got := purchasable(s); got[len(got)-1] != itemShinyCharm {
		t.Fatalf("purchasable = %v", got)
	}
	// Derived: with the Charm owned the full list ends with it, after the rare egg.
	want := []string{"item:mint", "item:rareCandy", "egg:<nil>", "egg:uncommon", "egg:rare", "item:shinyCharm"}
	if got := entryKeys(s.shopView()); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v", got)
	}
	last := s.shopView().Entries[5]
	if !last.Purchased || !last.Passive || last.Owned != 1 || last.CanBuy || last.MaxCount != 0 {
		t.Fatalf("owned charm row = %+v", last)
	}
}

func TestShopEntriesInterleavesFreshEggByPrice(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:220
	s := openAt(t, writeSave(t, walletJSON(5_000_000_000, 0, mon10(false), "")), noData(), 1)
	if s.st.Active == nil {
		t.Fatal("no active")
	}
	v := s.shopView()
	if got := entryKeys(v); !reflect.DeepEqual(got, defaultShopOrder) {
		t.Fatalf("entries = %v", got)
	}
	ascending(t, v)
}

func TestShopEntriesKeepsEggsVisibleButUnbuyableWhenNoActive(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShopTests.swift:243
	s := shopStore(t, 5_000_000_000, 0, "")
	v := s.shopView()
	if got := entryKeys(v); !reflect.DeepEqual(got, defaultShopOrder) {
		t.Fatalf("entries = %v", got)
	}
	for _, tier := range eggShopTiers {
		eq(t, s.canBuyEgg(tier), false, "canBuyEgg "+tier)
		_, err := s.BuyEgg(tier)
		eq(t, errCode(err), "noActive", "buyEgg "+tier)
	}
	for _, e := range v.Entries {
		if e.Kind == "egg" && (e.CanBuy || e.MaxCount != 0) {
			t.Fatalf("egg row buyable while incubating: %+v", e)
		}
	}
	eq(t, s.wallet(), 5_000_000_000, "wallet untouched")
}

// ---------------------------------------------------------------- Mint in the Shop

func TestMintShopPriceAndPurchasable(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:130
	p, _ := itemBasePrice(itemMint)
	eq(t, p, 100_000_000, "mint price")
	got := purchasable(mintStore(t, sp("adamant"), 0, 1_000_000_000, 50_000_000, false, 7))
	if !strings.Contains(strings.Join(got, ","), itemRareCandy) || !strings.Contains(strings.Join(got, ","), itemMint) {
		t.Fatalf("purchasable = %v", got)
	}
}

func TestBuyMintDebitsWalletAndCredits(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:138
	s := mintStore(t, sp("adamant"), 0, 300_000_000, 50_000_000, false, 7)
	eq(t, s.canBuy(itemMint), true, "canBuy")
	eq(t, bought(t, s, itemMint, 1), true, "buy")
	eq(t, s.itemCount(itemMint), 1, "mint")
	eq(t, s.st.SpentTokens, mintPrice, "spent")
	eq(t, s.wallet(), 300_000_000-mintPrice, "wallet")
}

func TestCannotBuyMintBelowPrice(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/MintTests.swift:147
	s := mintStore(t, sp("adamant"), 0, mintPrice-1, 50_000_000, false, 7)
	eq(t, s.canBuy(itemMint), false, "canBuy")
	eq(t, bought(t, s, itemMint, 1), false, "buy")
	eq(t, s.itemCount(itemMint), 0, "mint")
}

// ---------------------------------------------------------------- Shiny Charm

// charmStore mirrors ShinyCharmTests.store: an egg with a wallet and maybe the Charm, and the
// CharmStubProvider (index [(1, 255)], one-node common lines).
func charmStore(t *testing.T, used int, charm bool, seed uint64) *Store {
	extra := ""
	if charm {
		extra = `,"inventory":{"shinyCharm":1}`
	}
	data := &fakeData{
		lineFn: func(b int) (*Line, error) { return makeLine(b, node(b), rarityCommon), nil },
		index:  []BaseEntry{{1, 255}},
	}
	return openAt(t, writeSave(t, walletJSON(used, 0, "null", extra)), data, seed)
}

func TestRollsShinyFlipsWithCharm(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShinyCharmTests.swift:32
	for _, c := range []struct {
		roll  uint64
		charm bool
		want  bool
	}{
		{48, true, true}, {48, false, false}, {64, false, true}, {64, true, false},
		{96, true, true}, {96, false, false}, {0, true, true}, {0, false, true}, {1, true, false}, {1, false, false},
	} {
		eq(t, rollsShiny(c.roll, c.charm), c.want, fmt.Sprintf("roll %d charm %t", c.roll, c.charm))
	}
}

func TestShinyOddsFollowTheCharm(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShinyCharmTests.swift:50 (the l10n half belongs to the extension)
	eq(t, shinyOdds(false), shinyDenominator, "without")
	eq(t, shinyOdds(true), shinyCharmDenominator, "with")
	eq(t, charmStore(t, 5_000_000_000, false, 7).BuildState(MoodInput{Now: fixedNow}).Shop.ShinyOdds, 64, "store without")
	eq(t, charmStore(t, 5_000_000_000, true, 7).BuildState(MoodInput{Now: fixedNow}).Shop.ShinyOdds, 48, "store with")
}

func TestCharmConstantsAndPassiveFlag(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShinyCharmTests.swift:71 (spriteName is the extension's item key)
	p, _ := itemBasePrice(itemShinyCharm)
	eq(t, p, 3_000_000_000, "price")
	eq(t, shinyCharmDenominator, 48, "denominator")
	eq(t, itemPassive(itemShinyCharm), true, "charm passive")
	eq(t, itemPassive(itemRareCandy), false, "candy")
	eq(t, itemPassive(itemMint), false, "mint")
}

func TestBuyCharmDeductsAndOwns(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShinyCharmTests.swift:82
	s := charmStore(t, 5_000_000_000, false, 7)
	eq(t, s.ownsShinyCharm(), false, "not owned")
	eq(t, strings.Contains(strings.Join(purchasable(s), ","), itemShinyCharm), true, "listed")
	eq(t, s.canBuy(itemShinyCharm), true, "canBuy")
	eq(t, bought(t, s, itemShinyCharm, 1), true, "buy")
	eq(t, s.ownsShinyCharm(), true, "owned")
	eq(t, s.itemCount(itemShinyCharm), 1, "count")
	eq(t, s.st.SpentTokens, shinyCharmPrice, "spent")
	eq(t, s.wallet(), 5_000_000_000-shinyCharmPrice, "wallet")
}

func TestCharmBuyOnceNoRepurchase(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShinyCharmTests.swift:95
	s := charmStore(t, 10_000_000_000, true, 7)
	eq(t, s.canBuy(itemShinyCharm), false, "canBuy")
	_, err := s.Buy(itemShinyCharm, 1)
	eq(t, errCode(err), "alreadyOwned", "rebuy")
	eq(t, s.st.SpentTokens, 0, "spent")
	eq(t, s.itemCount(itemShinyCharm), 1, "count stays 1")
}

func TestCharmNeedsEnoughTokens(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShinyCharmTests.swift:105
	s := charmStore(t, 2_000_000_000, false, 7)
	eq(t, s.canBuy(itemShinyCharm), false, "canBuy")
	eq(t, bought(t, s, itemShinyCharm, 1), false, "buy")
	eq(t, s.ownsShinyCharm(), false, "owned")
}

func TestOwnsCharmReflectsInventory(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShinyCharmTests.swift:112
	eq(t, charmStore(t, 0, false, 7).ownsShinyCharm(), false, "without")
	eq(t, charmStore(t, 0, true, 7).ownsShinyCharm(), true, "with")
}

func TestHatchWorksWithCharmOwned(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/ShinyCharmTests.swift:119
	s := charmStore(t, 5_000_000_000, true, 7)
	s.hatch(bg, 1)
	if s.st.Active == nil || s.st.Active.currentID() != 1 {
		t.Fatalf("active = %+v", s.st.Active)
	}
}

// Go-only (bag-shop.md §5.4): the Charm changes the modulus of the same single shiny draw. A seed
// whose shiny draw is ≡ 0 mod 48 but not mod 64 hatches shiny only with the Charm, and the hatch
// event reports the odds it used. Every other draw stays in place: the nature matches.
func TestCharmChangesOnlyTheShinyModulus(t *testing.T) {
	seed := uint64(0)
	for s := uint64(1); s < 1_000_000; s++ {
		d := draws(s, 2)
		if d[1]%48 == 0 && d[1]%64 != 0 {
			seed = s
			break
		}
	}
	if seed == 0 {
		t.Fatal("no seed found")
	}
	hatch := func(charm bool) (*Store, HatchEvent) {
		s := charmStore(t, 5_000_000_000, charm, seed)
		s.st.EggUsage = eggHatchThreshold
		s.hatchIfNeeded(bg)
		evs := s.drain()
		if len(evs) != 1 {
			t.Fatalf("events = %v", evs)
		}
		return s, evs[0].(HatchEvent)
	}
	with, ev := hatch(true)
	if !with.st.Active.IsShiny || !ev.IsShiny || ev.ShinyOdds != 48 {
		t.Fatalf("seed %d with the Charm: shiny=%t event=%+v", seed, with.st.Active.IsShiny, ev)
	}
	without, ev2 := hatch(false)
	if without.st.Active.IsShiny || ev2.IsShiny || ev2.ShinyOdds != 64 {
		t.Fatalf("seed %d without the Charm: shiny=%t event=%+v", seed, without.st.Active.IsShiny, ev2)
	}
	eq(t, *with.st.Active.Nature, *without.st.Active.Nature, "same draw positions")
}

// ---------------------------------------------------------------- shop difficulty

// diffShopStore mirrors DifficultyTests.hatched(growth:shop:used:): a wallet, both multipliers and
// a hatched linear3 Pokémon.
func diffShopStore(t *testing.T, growth, shop float64, used int) *Store {
	st := freshState()
	st.InstallBaselineSet, st.LastDate, st.GrowthDifficulty, st.UsedSinceInstall = true, "d", clampDifficulty(growth), used
	s := openAt(t, writeState(t, st), stub(linear3()), 7)
	s.SetShopDifficulty(shop)
	s.hatch(bg, 1)
	return s
}

func TestShopGateAndChargedAmountFollowDifficulty(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:145
	half := rareCandyPrice / 2
	eq(t, diffShopStore(t, 1, 1, half).canBuy(itemRareCandy), false, "1.0×")
	cheap := diffShopStore(t, 1, 0.5, half)
	eq(t, cheap.canBuy(itemRareCandy), true, "0.5×")
	eq(t, bought(t, cheap, itemRareCandy, 1), true, "buy")
	eq(t, cheap.st.SpentTokens, 250_000_000, "charged")
	eq(t, cheap.itemCount(itemRareCandy), 1, "delivered")
}

func TestEggPriceScalesAndIsActuallyCharged(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:158
	s := diffShopStore(t, 1, 0.5, freshEggPrice)
	eq(t, s.eggPrice(""), 500_000_000, "price")
	p, err := s.BuyEgg("")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, s.st.SpentTokens, 500_000_000, "charged")
	eq(t, p.Spent, 500_000_000, "receipt")
}

func TestGrowthSliderDoesNotMoveShopPricesAndViceVersa(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:167
	growthOnly := diffShopStore(t, 0.1, 1, 0)
	p, _ := growthOnly.itemPrice(itemRareCandy)
	eq(t, p, rareCandyPrice, "candy price")
	eq(t, growthOnly.eggPrice(rarityRare), 4_000_000_000, "rare egg price")
	shopOnly := diffShopStore(t, 1, 0.1, 0)
	eq(t, tokensToNext(shopOnly), phase(rarityCommon, 3, 0), "growth threshold")
}

func TestGradedEggRatioSurvivesScaling(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:179
	s := diffShopStore(t, 1, 0.4, 0)
	plain := float64(s.eggPrice(""))
	if math.Abs(float64(s.eggPrice(rarityUncommon))/plain-2.5) > 0.001 || math.Abs(float64(s.eggPrice(rarityRare))/plain-4) > 0.001 {
		t.Fatalf("ratios %v %v", float64(s.eggPrice(rarityUncommon))/plain, float64(s.eggPrice(rarityRare))/plain)
	}
	// derived: bag-shop.md §1.3 row 0.4
	eq(t, s.eggPrice(""), 400_000_000, "plain")
	eq(t, s.eggPrice(rarityUncommon), 1_000_000_000, "uncommon")
	eq(t, s.eggPrice(rarityRare), 1_600_000_000, "rare")
}

func TestShopPriceChangesLiveWithoutRestart(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:203
	s := diffShopStore(t, 1, 1, 0)
	p, _ := s.itemPrice(itemRareCandy)
	eq(t, p, rareCandyPrice, "1.0×")
	s.SetShopDifficulty(0.5)
	p, _ = s.itemPrice(itemRareCandy)
	eq(t, p, rareCandyPrice/2, "0.5×")
}

func TestNoShopPriceCollapsesToZeroAtMinimumDifficulty(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultyTests.swift:242 (the shop half; balance_test.go has the thresholds)
	s := diffShopStore(t, 1, minDifficulty, 0)
	v := s.shopView()
	for _, e := range v.Entries {
		if e.Price <= 0 {
			t.Fatalf("%+v", e)
		}
	}
	eq(t, v.Entries[0].Price, 10_000_000, "smallest") // derived: bag-shop.md §1.3 row 0.1
}

func TestShopDifficultyClamp(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/DifficultySaveTests.swift:127
	s := shopStore(t, 0, 0, "")
	s.SetShopDifficulty(10)
	eq(t, s.ShopDifficulty(), 2.0, "10 → 2")
	s.SetShopDifficulty(0.01)
	eq(t, s.ShopDifficulty(), 0.1, "0.01 → 0.1")
	s.SetShopDifficulty(math.NaN())
	eq(t, s.ShopDifficulty(), 1.0, "non-finite → 1") // derived: CM:136-145
	eq(t, shopStore(t, 0, 0, "").ShopDifficulty(), 1.0, "default")
}

// derived: the price table of bag-shop.md §1.3; upstream asserts rows 0.4 and 0.5 only in part.
func TestShopPriceTable(t *testing.T) {
	want := map[float64][6]int{
		0.1: {10_000_000, 50_000_000, 100_000_000, 250_000_000, 300_000_000, 400_000_000},
		0.4: {40_000_000, 200_000_000, 400_000_000, 1_000_000_000, 1_200_000_000, 1_600_000_000},
		0.5: {50_000_000, 250_000_000, 500_000_000, 1_250_000_000, 1_500_000_000, 2_000_000_000},
		1.0: {100_000_000, 500_000_000, 1_000_000_000, 2_500_000_000, 3_000_000_000, 4_000_000_000},
		2.0: {200_000_000, 1_000_000_000, 2_000_000_000, 5_000_000_000, 6_000_000_000, 8_000_000_000},
	}
	s := shopStore(t, 0, 0, "")
	for d, prices := range want {
		s.SetShopDifficulty(d)
		v := s.shopView()
		if got := entryKeys(v); !reflect.DeepEqual(got, defaultShopOrder) {
			t.Fatalf("d=%v order %v", d, got)
		}
		for i, e := range v.Entries {
			eq(t, e.Price, prices[i], fmt.Sprintf("d=%v %s", d, entryKeys(v)[i]))
		}
	}
}

// ---------------------------------------------------------------- Fresh Egg (release)

// freshStore mirrors FreshEggTests.store: base 10 active, a 1→3 dex entry, collectedFinals 1:3.
func freshStore(t *testing.T, active, shinyMon bool, used int) *Store {
	a := "null"
	if active {
		a = mon10(shinyMon)
	}
	json := fmt.Sprintf(`{"installBaselineSet":true,"usedSinceInstall":%d,"spentTokens":0,"lastDate":"d","active":%s,"dex":[{"baseID":1,"finalID":3,"chainOrder":[1,2,3],"rarity":"common"}],"collectedFinals":["1:3"]}`, used, a)
	return openAt(t, writeSave(t, json), noData(), 7)
}

func TestFreshEggPriceIsOneBillion(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/FreshEggTests.swift:30
	eq(t, eggBasePrice(""), 1_000_000_000, "price")
}

func TestBuyFreshEggReleasesIntoDexWithoutProbabilityImpact(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/FreshEggTests.swift:35
	s := freshStore(t, true, false, 5_000_000_000)
	dexBefore := len(s.st.Dex)
	collected := fmt.Sprint(sortedKeys(s.st.CollectedFinals))
	eq(t, s.dexSummary().Entries, dexBefore+1, "the active Pokémon shows in the dex")
	p, err := s.BuyEgg("")
	if err != nil {
		t.Fatal(err)
	}
	if s.st.Active != nil || s.st.EggUsage != 0 || s.st.PendingHatchID != nil {
		t.Fatalf("not a fresh egg: %+v", s.st)
	}
	eq(t, len(s.st.Dex), dexBefore+1, "released entry recorded")
	rel := s.st.Dex[len(s.st.Dex)-1]
	if rel.BaseID != 10 || rel.ReleasedAt == nil || !ints(rel.ChainOrder, []int{10}) {
		t.Fatalf("released = %+v", rel)
	}
	eq(t, p.ReleasedEntryID, rel.ID, "receipt entry id")
	eq(t, s.dexSummary().Entries, dexBefore+1, "the record replaces the synthesized entry")
	eq(t, fmt.Sprint(sortedKeys(s.st.CollectedFinals)), collected, "collectedFinals unchanged")
	eq(t, s.st.SpentTokens, freshEggPrice, "spent")
	eq(t, s.wallet(), 5_000_000_000-freshEggPrice, "wallet")
	eq(t, p.Wallet, s.wallet(), "receipt wallet")
	if p.Tier != nil {
		t.Fatal("plain egg tier must be nil")
	}
}

func dexHas(s *Store, id int) (found, raising bool) {
	for _, d := range s.collectedDexSpecies(false) {
		if d.id == id {
			return true, d.isRaising
		}
	}
	return false, false
}

func TestReleasedSpeciesStaysInTheDex(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/FreshEggTests.swift:67
	s := freshStore(t, true, false, 5_000_000_000)
	if found, raising := dexHas(s, 10); !found || !raising {
		t.Fatal("raising species not in the dex")
	}
	if _, err := s.BuyEgg(""); err != nil {
		t.Fatal(err)
	}
	if found, raising := dexHas(s, 10); !found || raising {
		t.Fatalf("after release: found=%t raising=%t", found, raising)
	}
}

func TestDiscardedSpeciesNotCollected(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/FreshEggTests.swift:78
	s := freshStore(t, true, false, 5_000_000_000)
	if _, err := s.BuyEgg(""); err != nil {
		t.Fatal(err)
	}
	for k := range s.st.CollectedFinals {
		if strings.HasPrefix(k, "10:") {
			t.Fatalf("released species collected: %s", k)
		}
	}
}

func TestCannotRerollWhenEgg(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/FreshEggTests.swift:86
	s := freshStore(t, false, false, 5_000_000_000)
	eq(t, s.canBuyEgg(""), false, "canBuy")
	_, err := s.BuyEgg("")
	eq(t, errCode(err), "noActive", "buy")
	eq(t, s.st.SpentTokens, 0, "spent")
}

func TestCannotRerollWithoutFunds(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/FreshEggTests.swift:95
	s := freshStore(t, true, false, 500_000_000)
	eq(t, s.canBuyEgg(""), false, "canBuy")
	_, err := s.BuyEgg("")
	eq(t, errCode(err), "insufficientFunds", "buy")
	if s.st.Active == nil || s.st.SpentTokens != 0 {
		t.Fatal("state changed")
	}
}

func TestShinyCanBeRerolled(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/FreshEggTests.swift:104
	s := freshStore(t, true, true, 5_000_000_000)
	eq(t, s.st.Active.displaysShiny(), true, "shiny")
	if _, err := s.BuyEgg(""); err != nil {
		t.Fatal(err)
	}
	if s.st.Active != nil || s.BuildState(MoodInput{Now: fixedNow}).Active != nil {
		t.Fatal("still active")
	}
}

// Derived (bag-shop.md §1.6 step 7, §5.2 item 6): a purchase ends the celebration window and
// clears the hatch-delay and warm flags; an egg needs no line.
func TestBuyEggResetsTransientState(t *testing.T) {
	s := diffShopStore(t, 1, 1, 5_000_000_000)
	if s.window.until.IsZero() || s.line == nil {
		t.Fatal("hatch window and line expected")
	}
	s.warmPending, s.hatchDelayed, s.prefetchedLineID = true, true, 4
	if _, err := s.BuyEgg(rarityUncommon); err != nil {
		t.Fatal(err)
	}
	if !s.window.until.IsZero() || s.line != nil || s.warmPending || s.hatchDelayed || s.prefetchedLineID != 0 {
		t.Fatalf("transient state kept: window=%v line=%v warm=%t delayed=%t prefetched=%d",
			s.window, s.line, s.warmPending, s.hatchDelayed, s.prefetchedLineID)
	}
	v := s.BuildState(MoodInput{Now: fixedNow})
	if v.LevelUpUntil != nil || v.Egg == nil || deref(v.Egg.Guarantee) != rarityUncommon {
		t.Fatalf("view after purchase: %+v %+v", v, v.Egg)
	}
	// The released entry has every chain name from the loaded line, so it is marked current.
	rel := s.st.Dex[len(s.st.Dex)-1]
	if rel.NamesVersion == nil || rel.Names[1]["en"] != "P1" {
		t.Fatalf("released names: %+v %v", rel.Names, rel.NamesVersion)
	}
}

// ---------------------------------------------------------------- release tests elsewhere

func activeJSON(m string, extra string) string {
	return fmt.Sprintf(`{"active":%s,"usedSinceInstall":5000000000%s}`, m, extra)
}

func TestFreshEggKeepsRepresentativeSelectionOfReleasedSpecies(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:590
	m := `{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":3}`
	s := openAt(t, writeSave(t, fmt.Sprintf(`{"active":%s,"representativeSpeciesID":1,"usedSinceInstall":1000000000}`, m)), stub(linear3()), 7)
	eq(t, *s.st.RepresentativeSpeciesID, 1, "before")
	if _, err := s.BuyEgg(""); err != nil {
		t.Fatal(err)
	}
	if s.st.RepresentativeSpeciesID == nil || *s.st.RepresentativeSpeciesID != 1 {
		t.Fatal("selection dropped")
	}
	if ms := s.BuildState(MoodInput{Now: fixedNow}).MenuSubject; ms.SpeciesID == nil || *ms.SpeciesID != 1 {
		t.Fatalf("menu subject = %+v", ms)
	}
}

func TestReleasingMidChainCreditsOnlyReachedForms(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:744
	m := `{"baseID":1,"pathIDs":[1,2],"plannedPathIDs":[1,2,3],"stageIndex":1,"usedAtStage":0,"rarity":"common","totalForms":3}`
	s := openAt(t, writeSave(t, activeJSON(m, "")), stub(linear3()), 7)
	ids := func() []int {
		var out []int
		for _, d := range s.collectedDexSpecies(false) {
			out = append(out, d.id)
		}
		return out
	}
	if !ints(ids(), []int{1, 2}) {
		t.Fatalf("before: %v", ids())
	}
	if _, err := s.BuyEgg(""); err != nil {
		t.Fatal(err)
	}
	if !ints(ids(), []int{1, 2}) {
		t.Fatalf("after: %v", ids())
	}
	rel := s.st.Dex[len(s.st.Dex)-1]
	if !ints(rel.ChainOrder, []int{1, 2}) || rel.FinalID != 2 {
		t.Fatalf("released = %+v", rel)
	}
}

func TestReleasingDisguisedDittoKeepsShinyHidden(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/CompanionTests.swift:765
	m := `{"baseID":1,"pathIDs":[1],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":3,"isShiny":true,"dittoDisguise":1,"dittoRevealed":false}`
	s := openAt(t, writeSave(t, activeJSON(m, "")), stub(linear3()), 7)
	if _, err := s.BuyEgg(""); err != nil {
		t.Fatal(err)
	}
	eq(t, s.st.Dex[len(s.st.Dex)-1].IsShiny, false, "hidden until the reveal")
}

func TestBuyEggStartsWithoutPrayers(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:110 (pray is LATER; the nil is asserted)
	s := openAt(t, writeSave(t, walletJSON(5_000_000_000, 0, mon10(false), "")), noData(), 7)
	if _, err := s.BuyEgg(""); err != nil {
		t.Fatal(err)
	}
	if s.st.EggPrayers != nil {
		t.Fatal("egg prayers kept")
	}
}

func TestReleasedEntryKeepsThePrayersOfTheIndividual(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/EggPrayerTests.swift:118
	m := `{"baseID":10,"pathIDs":[10],"stageIndex":0,"usedAtStage":0,"rarity":"common","totalForms":3,"prayers":{"count":4}}`
	s := openAt(t, writeSave(t, walletJSON(5_000_000_000, 0, m, "")), noData(), 7)
	if _, err := s.BuyEgg(""); err != nil {
		t.Fatal(err)
	}
	if p := s.st.Dex[len(s.st.Dex)-1].Prayers; p == nil || p.Count != 4 {
		t.Fatalf("prayers = %+v", p)
	}
}

func TestReleasingActiveLetterPreservesFormProfileAndRepresentative(t *testing.T) {
	// Swift: Tests/PokeTokenBarTests/UnownTests.swift:429
	st := freshState()
	st.Dex = []dexEntry{entry(201, 201, []int{201}, rarityCommon, withForm("a"), shiny)}
	st.Active = &monState{BaseID: 201, PathIDs: []int{201}, PlannedPathIDs: []int{201}, Rarity: rarityCommon, TotalForms: 1, Form: sp("b")}
	st.RepresentativeSpeciesID, st.RepresentativeForm = intPtr(201), sp("b")
	st.UsedSinceInstall = freshEggPrice
	dir := writeState(t, st)
	s := openAt(t, dir, noData(), 7)
	profile := *s.st.Active.Profile
	if _, err := s.BuyEgg(""); err != nil {
		t.Fatal(err)
	}
	eq(t, *s.st.RepresentativeSpeciesID, 201, "representative")
	eq(t, deref(s.st.RepresentativeForm), "b", "representative form")
	eq(t, s.BuildState(MoodInput{Now: fixedNow}).MenuSubject.Shiny, false, "subject not shiny")
	eq(t, len(s.st.Dex), 2, "dex")
	if !reflect.DeepEqual(s.st.collectedForms(201), toSet([]string{"a", "b"})) {
		t.Fatalf("forms = %v", s.st.collectedForms(201))
	}
	for _, d := range s.collectedDexSpecies(true) {
		if d.isRaising {
			t.Fatalf("raising after release: %+v", d)
		}
	}
	eq(t, s.st.ownsShinySpecies(201, sp("a")), true, "shiny A")
	eq(t, s.st.ownsSpecies(201, sp("b")), true, "B")
	rel := s.st.Dex[1]
	if rel.ReleasedAt == nil || deref(rel.Form) != "b" || !reflect.DeepEqual(*rel.Profile, profile) {
		t.Fatalf("released = %+v", rel)
	}
	eq(t, deref(openAt(t, dir, noData(), 7).st.RepresentativeForm), "b", "reloaded representative form")
	eq(t, len(s.st.CollectedFinals), 0, "release is not a graduation")
}

// ---------------------------------------------------------------- high-value guard

// highValue is the extension's precious-companion rule over the wire fields (bag-shop.md §5.4:
// active.isShiny || active.rarity == "legendary"; isShiny is already displaysShiny).
func highValue(v CompanionView) bool {
	return v.Active != nil && (v.Active.IsShiny || v.Active.Rarity == rarityLegendary)
}

func guardStore(t *testing.T, active bool, rarity string, shinyMon bool, disguise string) CompanionView {
	m := "null"
	if active {
		m = fmt.Sprintf(`{"baseID":10,"pathIDs":[10],"stageIndex":0,"usedAtStage":200000000,"rarity":%q,"totalForms":3,"isShiny":%t%s}`, rarity, shinyMon, disguise)
	}
	return openAt(t, writeSave(t, walletJSON(5_000_000_000, 0, m, "")), noData(), 1).BuildState(MoodInput{Now: fixedNow})
}

func TestHighValueCompanion(t *testing.T) {
	cases := []struct {
		line     string
		active   bool
		rarity   string
		shiny    bool
		disguise string
		want     bool
	}{
		{"Swift: Tests/PokeTokenBarTests/PreciousCompanionGuardTests.swift:31", true, rarityLegendary, false, "", true},
		{"Swift: Tests/PokeTokenBarTests/PreciousCompanionGuardTests.swift:38", true, rarityCommon, true, "", true},
		{"Swift: Tests/PokeTokenBarTests/PreciousCompanionGuardTests.swift:45", true, rarityLegendary, true, "", true},
		{"Swift: Tests/PokeTokenBarTests/PreciousCompanionGuardTests.swift:52", true, rarityCommon, false, "", false},
		{"Swift: Tests/PokeTokenBarTests/PreciousCompanionGuardTests.swift:57", true, rarityUncommon, false, "", false},
		{"Swift: Tests/PokeTokenBarTests/PreciousCompanionGuardTests.swift:62", true, rarityRare, false, "", false},
		{"Swift: Tests/PokeTokenBarTests/PreciousCompanionGuardTests.swift:69", false, "", false, "", false},
		{"Swift: Tests/PokeTokenBarTests/PreciousCompanionGuardTests.swift:75 disguised", true, rarityCommon, true, `,"dittoDisguise":10,"dittoRevealed":false`, false},
		{"Swift: Tests/PokeTokenBarTests/PreciousCompanionGuardTests.swift:75 revealed", true, rarityRare, true, `,"dittoDisguise":10,"dittoRevealed":true`, true},
	}
	for _, c := range cases {
		v := guardStore(t, c.active, c.rarity, c.shiny, c.disguise)
		eq(t, highValue(v), c.want, c.line)
	}
}

// ---------------------------------------------------------------- fixtures as the value oracle

func fixtureJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// The M2 companion values of state-active.json, state-egg.json and candy-plan.json are what the
// helper computes for the same world (the extension phases build against these files).
func TestShopAndBagMatchFixtureValues(t *testing.T) {
	s := pidgeottoWorld(t)
	s.st.UsedSinceInstall, s.st.SpentTokens = 3_214_500_000, 0
	s.st.Inventory = map[string]int{itemRareCandy: 3, itemMint: 1}
	v := s.BuildState(MoodInput{Now: fixedNow})
	for _, k := range []struct {
		key string
		got any
	}{{"wallet", v.Wallet}, {"inventory", v.Inventory}, {"shop", v.Shop}, {"bag", v.Bag}} {
		if want := fixture(t, "state-active.json", "companion", k.key); !reflect.DeepEqual(fixtureJSON(t, k.got), want) {
			t.Errorf("state-active %s:\n got %v\nwant %v", k.key, fixtureJSON(t, k.got), want)
		}
	}
	if want := fixture(t, "candy-plan.json", "data", "plan"); !reflect.DeepEqual(fixtureJSON(t, s.PlanCandies(3)), want) {
		t.Errorf("candy-plan: got %v, want %v", fixtureJSON(t, s.PlanCandies(3)), want)
	}

	egg := newStore(t, stub(linear3()), 7)
	egg.st.UsedSinceInstall, egg.st.EggUsage = 3_100_000, 3_100_000
	ev := egg.BuildState(MoodInput{Now: fixedNow})
	for _, k := range []struct {
		key string
		got any
	}{{"wallet", ev.Wallet}, {"inventory", ev.Inventory}, {"shop", ev.Shop}, {"bag", ev.Bag}, {"egg", ev.Egg}} {
		want := fixture(t, "state-egg.json", "companion", k.key)
		if m, ok := want.(map[string]any); ok && k.key == "egg" {
			delete(m, "prayers") // M3 pending (phase 17 removes this): the Go Egg view has no prayers yet
		}
		if !reflect.DeepEqual(fixtureJSON(t, k.got), want) {
			t.Errorf("state-egg %s:\n got %v\nwant %v", k.key, fixtureJSON(t, k.got), want)
		}
	}
}

// Protocol §1.1 and decision §14.11: inventory counts are stored as they are, and only the wire
// clamp of 2^53−1 applies to what the state sends.
func TestInventoryCountsAreClampedOnTheWire(t *testing.T) {
	s := shopStore(t, 0, 0, `,"inventory":{"mint":100000000000000000,"rareCandy":-2}`)
	v := s.BuildState(MoodInput{Now: fixedNow})
	eq(t, v.Inventory, Inventory{RareCandy: -2, Mint: maxWireInt}, "inventory")
	eq(t, v.Shop.Entries[0].Owned, maxWireInt, "mint row")
	eq(t, s.itemCount(itemMint), 100_000_000_000_000_000, "stored as is")
}
