package companion

import (
	"math"
	"sort"
	"strconv"
)

// Shop: the wallet, the catalog, purchases and egg release (upstream CompanionStore.swift:1273-1402,
// CompanionModel.swift:182-290; docs/spec/bag-shop.md §1).

// Item kinds; the raw value is the inventory key (CM:182-216).
const (
	itemRareCandy  = "rareCandy"
	itemMint       = "mint"
	itemShinyCharm = "shinyCharm"
)

// itemKinds is ItemKind.allCases: the Bag order and the base order of the Shop sort.
var itemKinds = []string{itemRareCandy, itemMint, itemShinyCharm}

// Prices and odds (CM:221-262).
const (
	rareCandyXP           = 100_000_000
	rareCandyPrice        = 500_000_000
	mintPrice             = 100_000_000
	shinyCharmPrice       = 3_000_000_000
	shinyCharmDenominator = 48
	freshEggPrice         = 1_000_000_000
)

// eggShopTiers are the eggs on sale; "" is the plain egg (FreshEgg.shopTiers, CM:262).
var eggShopTiers = []string{"", rarityUncommon, rarityRare}

// Error is a Shop or Bag rule violation. Code is the protocol §11.2 error code.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

func gameErr(code, msg string) *Error { return &Error{Code: code, Message: msg} }

func itemBasePrice(item string) (int, bool) {
	switch item {
	case itemRareCandy:
		return rareCandyPrice, true
	case itemMint:
		return mintPrice, true
	case itemShinyCharm:
		return shinyCharmPrice, true
	}
	return 0, false
}

// itemPassive: bought once and never consumed (CM:212-217).
func itemPassive(item string) bool { return item == itemShinyCharm }

func isEggShopTier(tier string) bool {
	for _, t := range eggShopTiers {
		if t == tier {
			return true
		}
	}
	return false
}

// eggBasePrice is the tier price before the shop difficulty: 1e9 × T(tier) / T(common), rounded
// half away from zero (FreshEgg.price(guaranteeing:), CM:271-275).
func eggBasePrice(tier string) int {
	if tier == "" {
		return freshEggPrice
	}
	ratio := float64(graduationTotal(tier)) / float64(graduationTotal(rarityCommon))
	return int(math.Round(freshEggPrice * ratio))
}

// captureRateCeiling: a capture rate at or below it means the species is of this rarity or better.
// A legendary has none: the hatch index carries no legendary flag (CM:83-89).
func captureRateCeiling(r string) (int, bool) {
	switch r {
	case rarityRare:
		return 45, true
	case rarityUncommon:
		return 120, true
	case rarityCommon:
		return 255, true
	}
	return 0, false
}

// tierIncludes reports whether a capture rate means this tier or better (Rarity.includes, CM:91-94).
func tierIncludes(tier string, captureRate int) bool {
	c, ok := captureRateCeiling(tier)
	return ok && captureRate <= c
}

// shinyOdds is the hatch shiny denominator: 48 with the Shiny Charm, else 64 (CS:1726-1731).
func shinyOdds(charm bool) int {
	if charm {
		return shinyCharmDenominator
	}
	return shinyDenominator
}

// rollsShiny is the pure shiny test on one draw (CS:1734-1736).
func rollsShiny(roll uint64, charm bool) bool { return roll%uint64(shinyOdds(charm)) == 0 }

// SetShopDifficulty clamps and applies the shop price multiplier. Prices are derived, so nothing is
// rescaled or saved; the value lives in the daemon's settings-applied.json (CS:155-160).
func (s *Store) SetShopDifficulty(v float64) { s.shopDifficulty = clampDifficulty(v) }

// ShopDifficulty returns the applied shop price multiplier (0.1–2.0).
func (s *Store) ShopDifficulty() float64 { return s.shopDifficulty }

// wallet is the spendable balance: used since install minus spent, never negative (CS:1277).
func (s *Store) wallet() int { return max(0, s.st.UsedSinceInstall-s.st.SpentTokens) }

func (s *Store) itemCount(item string) int { return s.st.Inventory[item] }

func (s *Store) ownsShinyCharm() bool { return s.itemCount(itemShinyCharm) > 0 }

// itemPrice is the scaled unit price; false for an item that is not sold (CS:174-176).
func (s *Store) itemPrice(item string) (int, bool) {
	base, ok := itemBasePrice(item)
	if !ok {
		return 0, false
	}
	return scaled(base, s.shopDifficulty), true
}

// eggPrice rounds twice: the tier price, then the difficulty scaling (CS:179-181).
func (s *Store) eggPrice(tier string) int { return scaled(eggBasePrice(tier), s.shopDifficulty) }

// canBuy: sold, not a passive item already owned, and affordable. The egg and the active Pokémon do
// not matter, so stock can be bought ahead (CS:1321-1326).
func (s *Store) canBuy(item string) bool {
	price, ok := s.itemPrice(item)
	if !ok || (itemPassive(item) && s.itemCount(item) > 0) {
		return false
	}
	return s.wallet() >= price
}

// maxBuyCount: 0 unless canBuy, 1 for a passive item, else floor(wallet / price) (CS:1330-1333).
func (s *Store) maxBuyCount(item string) int {
	price, _ := s.itemPrice(item)
	if !s.canBuy(item) || price <= 0 {
		return 0
	}
	if itemPassive(item) {
		return 1
	}
	return s.wallet() / price
}

// Purchase is the buyItem reply (protocol §7.4).
type Purchase struct {
	Item   string `json:"item"`
	Count  int    `json:"count"`
	Spent  int    `json:"spent"`
	Wallet int    `json:"wallet"`
	Owned  int    `json:"owned"`
}

// Buy buys count items, all or nothing: the wallet pays price × count and the inventory gains
// count. It never clamps. Errors in the protocol §7.4 order: badArgs, alreadyOwned,
// insufficientFunds (CS:1338-1344).
func (s *Store) Buy(item string, count int) (Purchase, error) {
	price, sold := s.itemPrice(item)
	switch {
	case !sold:
		return Purchase{}, gameErr("badArgs", "unknown item "+strconv.Quote(item))
	case count < 1:
		return Purchase{}, gameErr("badArgs", "count must be at least 1")
	case itemPassive(item) && count != 1:
		return Purchase{}, gameErr("badArgs", "a passive item is bought one at a time")
	case itemPassive(item) && s.itemCount(item) > 0:
		return Purchase{}, gameErr("alreadyOwned", item+" is already owned")
	case count > s.maxBuyCount(item):
		return Purchase{}, gameErr("insufficientFunds", "the wallet does not cover "+strconv.Itoa(count)+" × "+item)
	}
	s.st.SpentTokens += price * count
	s.st.Inventory[item] += count
	s.save()
	return Purchase{Item: item, Count: count, Spent: price * count, Wallet: s.wallet(), Owned: s.itemCount(item)}, nil
}

// canBuyEgg: a tier on sale, an active Pokémon to release and the wallet for the scaled price.
// The tier is checked first, so an unsatisfiable tier is never charged (CS:1360-1367).
func (s *Store) canBuyEgg(tier string) bool {
	return isEggShopTier(tier) && s.st.Active != nil && s.wallet() >= s.eggPrice(tier)
}

// EggPurchase is the buyEgg reply (protocol §7.4); Tier is nil for the plain egg.
type EggPurchase struct {
	Tier            *string `json:"tier"`
	Spent           int     `json:"spent"`
	Wallet          int     `json:"wallet"`
	ReleasedEntryID string  `json:"releasedEntryId"`
}

// BuyEgg releases the active Pokémon into the Pokédex and starts a new egg carrying the tier as its
// guarantee ("" is the plain egg). No species is rolled here, because rolling needs the network;
// the prefetch and hatch paths read the tier. collectedFinals is untouched. Errors in the protocol
// §7.4 order: badArgs, noActive, insufficientFunds (CS:1378-1402).
func (s *Store) BuyEgg(tier string) (EggPurchase, error) {
	st := &s.st
	switch {
	case !isEggShopTier(tier):
		return EggPurchase{}, gameErr("badArgs", "tier "+strconv.Quote(tier)+" is not on sale")
	case st.Active == nil:
		return EggPurchase{}, gameErr("noActive", "an egg is incubating")
	case s.wallet() < s.eggPrice(tier):
		return EggPurchase{}, gameErr("insufficientFunds", "the wallet does not cover the egg")
	}
	price := s.eggPrice(tier)
	st.SpentTokens += price
	released := s.releasedEntry(st.Active)
	st.Dex = append(st.Dex, released)
	st.Active = nil
	st.reconcileRepresentative() // a released species is still owned, so the selection stays
	s.line = nil
	st.EggUsage = 0
	st.EggPrayers = nil
	s.hatchDelayed = false
	st.EggTier = nil
	if tier != "" {
		st.EggTier = strPtr(tier)
	}
	st.PendingHatchID, st.PendingForm = nil, nil
	s.prefetchedLineID = 0
	s.window = levelUpWindow{} // ends any celebration window
	s.warmPending = false
	s.save()
	return EggPurchase{Tier: st.EggTier, Spent: price, Wallet: s.wallet(), ReleasedEntryID: released.ID}, nil
}

// releasedEntry records a released Pokémon: only the reached forms, the displayed shininess (a
// disguised Ditto stays hidden), and names only from a loaded line (CS:355-375).
func (s *Store) releasedEntry(a *monState) dexEntry {
	// pathIDs is never empty: decode drops an active Pokémon without one (CM:522-549).
	chain := append([]int{}, a.PathIDs[:min(len(a.PathIDs), max(1, a.StageIndex+1))]...)
	now := s.now()
	e := dexEntry{
		ID: a.Profile.InstanceID, BaseID: a.BaseID, FinalID: chain[len(chain)-1], ChainOrder: chain,
		Rarity: a.Rarity, CaughtAt: &now, ReleasedAt: timePtr(now), IsShiny: a.displaysShiny(), Nature: a.Nature,
		Form: formForLine(a.BaseID, a.Form), Profile: a.Profile, Prayers: a.Prayers,
	}
	if s.line != nil {
		e.Names = map[int]map[string]string{}
		allNamed := true
		for _, id := range chain {
			if n, ok := s.line.names[id]; ok {
				e.Names[id] = n
			}
			allNamed = allNamed && len(s.line.names[id]) > 0
		}
		if allNamed {
			e.NamesVersion = intPtr(currentNamesVersion)
		}
	}
	return e
}

// ShopEntry is one Shop row (protocol §5.4).
type ShopEntry struct {
	Kind      string  `json:"kind"`
	Item      *string `json:"item"`
	Tier      *string `json:"tier"`
	Price     int     `json:"price"`
	Passive   bool    `json:"passive"`
	Owned     int     `json:"owned"`
	Purchased bool    `json:"purchased"`
	CanBuy    bool    `json:"canBuy"`
	MaxCount  int     `json:"maxCount"`
}

// ShopView is state.companion.shop.
type ShopView struct {
	Entries   []ShopEntry `json:"entries"`
	ShinyOdds int         `json:"shinyOdds"`
}

// shopView lists the sold items and the three eggs by ascending scaled price, with a purchased
// passive item last (shopEntries, CS:1304-1319). Eggs stay listed while incubating.
func (s *Store) shopView() ShopView {
	var entries []ShopEntry
	for _, item := range itemKinds { // every ItemKind is sold
		price, _ := s.itemPrice(item)
		owned := s.itemCount(item)
		entries = append(entries, ShopEntry{
			Kind: "item", Item: strPtr(item), Price: price, Passive: itemPassive(item), Owned: owned,
			Purchased: itemPassive(item) && owned > 0, CanBuy: s.canBuy(item), MaxCount: s.maxBuyCount(item),
		})
	}
	for _, tier := range eggShopTiers {
		e := ShopEntry{Kind: "egg", Price: s.eggPrice(tier), CanBuy: s.canBuyEgg(tier)}
		if tier != "" {
			e.Tier = strPtr(tier)
		}
		if e.CanBuy {
			e.MaxCount = 1
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Purchased != entries[j].Purchased {
			return !entries[i].Purchased
		}
		return entries[i].Price < entries[j].Price
	})
	return ShopView{Entries: entries, ShinyOdds: shinyOdds(s.ownsShinyCharm())}
}
