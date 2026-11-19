# PokeTokenBar Bag and Shop: porting spec for the Go helper and the GNOME extension (M2)

Conventions (same as `docs/spec/companion.md`):
- Upstream paths are relative to the upstream repo root (the Swift PokeTokenBar checkout), at HEAD 38e3e55 (v2.5.7).
- `CS` = `Sources/PokeTokenBar/Core/CompanionStore.swift`, `CM` = `Sources/PokeTokenBar/Core/CompanionModel.swift`
- `SV` = `Sources/PokeTokenBar/UI/ShopView.swift`, `BV` = `Sources/PokeTokenBar/UI/BagView.swift`, `CV` = `Sources/PokeTokenBar/UI/CompanionView.swift`
- `PV` = `UI/PopoverView.swift`, `DSS` = `UI/DifficultySettingsSection.swift`, `LOC` = `Core/Localization.swift`, `PP` = `Core/PokemonProfile.swift`, `ST` = `Core/SaveTransfer.swift`, `TF` = `Core/TokenFormatter.swift`
- Tests are under `Tests/PokeTokenBarTests/`: `ShopT` = `ShopTests.swift`, `MintT` = `MintTests.swift`, `CharmT` = `ShinyCharmTests.swift`, `RCT` = `RareCandyTests.swift`, `PremT` = `PremiumEggTests.swift`, `FreshT` = `FreshEggTests.swift`, `DiffT` = `DifficultyTests.swift`, `DSaveT` = `DifficultySaveTests.swift`, `GuardT` = `PreciousCompanionGuardTests.swift`, `DittoT` = `DittoTests.swift`, `CompT` = `CompanionTests.swift`.
- Port paths are relative to this repo root. `go:` = `helper/internal/companion/`.
- **Asserted** means a value an upstream XCTest assertion checks. **Derived** means I computed it with SplitMix64 and the documented draw order; upstream does not assert it. Use derived values as extra Go assertions only once the asserted ones pass.

What the port already has (do not redo):
- Save fields `spentTokens`, `eggTier` (lenient, valid rarities only), `inventory` (`map[string]int`), `releasedAt` on dex entries (`go:state.go:84-104`, `:235-238`, `:269`).
- Sanitizing: `spentTokens` clamped to [0, 1e15]; `eggTier`, `pendingHatchID` and `eggPrayers` dropped when an active Pokémon exists; a legendary tier dropped (`go:state.go:463-483`). Ported tests: `go:save_test.go` `TestExtremeValuesAreClampedOnLoad` (SaveTransferTests:489, :528).
- Candy **grants** (`go:candy.go:66-138`) and their tests (RCT:34-365 ported in `go:candy_test.go`, `go:save_test.go`).
- `inventory.rareCandy` in the state (`go:views.go:119-123`, `:176`).
- What is missing is everything that **spends**: wallet, prices, buy, egg purchase and release, the `eggTier` filter and gate in the hatch, Shiny Charm odds, candy use, Mint use, plus their protocol and UI.

---

## 1. Shop

### 1.1 Wallet
- **Wallet = `max(0, usedSinceInstall − spentTokens)`** (`CS:1277`).
  - `usedSinceInstall` is credited only by the usage ledger (companion.md §2.2).
  - Candy XP never touches it (`CS:1226-1227`; asserted by RCT:469-481 and RCT:369-387).
  - Buying only raises `spentTokens`. It never changes `usedSinceInstall`, `eggUsage`, `usedAtStage` or the daily, weekly and monthly totals (`CS:1335-1343`; ShopT:72-79).
- **Clamping:** `max(0, …)` hides a save where spent > used (ShopT:40-42). Both fields are clamped to [0, 1e15] at the trust boundary (`ST:146-147`); Go already does this.
- **Display:** `TokenFormatter.compact(wallet)` (`SV:38`), which gives K and M with one decimal and B with two, trailing zeros trimmed (`TF:5-19`, `TF:54-59`).

### 1.2 Catalog
Items are `ItemKind.allCases` in declaration order: `rareCandy`, `mint`, `shinyCharm` (`CM:182-216`). The inventory key is the raw value.

| Entry | Base price | Source | Kind | Item sprite | Fallback emoji |
|---|---|---|---|---|---|
| `rareCandy` | 500,000,000 | `CM:232` | consumable | `rare-candy` | 🍬 |
| `mint` | 100,000,000 | `CM:240` | consumable | none (Gen 8 item) | 🌿 |
| `shinyCharm` | 3,000,000,000 | `CM:246` | **passive**: bought once, never consumed (`CM:212-217`) | `shiny-charm` | ✨ |
| egg, no tier (`nil`) | 1,000,000,000 | `CM:257` | instant action | the egg sprite | — |
| egg, `uncommon` | 2,500,000,000 | `CM:271-275` | instant action | the egg sprite | — |
| egg, `rare` | 4,000,000,000 | `CM:271-275` | instant action | the egg sprite | — |

- Sprites and emoji: `CM:188-202`. The sprite URL is `.../sprites/items/<name>.png` (companion.md §8.1); the helper key is `item-<name>` (protocol §8.2).
- **Tiered egg price** = `Int((1e9 × T(tier) / T(common)).rounded())`, with T = the graduation totals 750M, 1.875B and 3B (`CM:271-275`). That gives 1 : 2.5 : 4. Asserted: 1e9, 2.5e9 and 4e9 (PremT:126-131).
- **Eggs on sale:** `FreshEgg.shopTiers = [nil, .uncommon, .rare]` (`CM:262`). There is no legendary egg and no `.common` egg; the plain egg is `nil` only (PremT:422-430).
- Eggs are not `ItemKind`s and never enter the inventory (`CM:278-280`).

### 1.3 Shop difficulty and the rounding chain
- **Price formula:** `price = scaled(base, shopDifficulty) = Int((Double(base) × clamp(d)).rounded())`, rounding half away from zero (`CM:150-152`, `CS:174-181`). Go's `scaled()` (`go:balance.go:100-102`) is the same function.
- **Eggs are rounded twice:** first the tier price, `round(1e9 × ratio)` (`CM:271-275`), then the difficulty scaling (`CS:179-181`, `CM:285-289`). Items are rounded once.
- **Clamp:** 0.1…2.0, and a non-finite value becomes 1.0 (`CM:136-145`). Asserted: setting 10 gives 2, setting 0.01 gives 0.1 (DSaveT:127-138). A stored value of 20 loads as 2 (DSaveT:29, :44).
- **Independence:** shop difficulty scales only prices, and growth difficulty only thresholds. Candy XP is scaled by neither (`CS:63-73`; DiffT:167-177).
- **Storage:** UserDefaults key `shopDifficulty`, not the save (`CS:63-66`, `CS:94-95`, `CS:102`).
  - `setShopDifficulty` clamps, returns early when the value is unchanged, then stores it. Nothing is re-evaluated because prices are derived (`CS:155-160`).
  - Prices change live (DiffT:203-208).
- **Table** (prices are whole tokens; no ties at any difficulty, so the order never changes):

| d | Mint | Candy | Egg | Uncommon egg | Charm | Rare egg |
|---|---|---|---|---|---|---|
| 0.1 | 10,000,000 | 50,000,000 | 100,000,000 | 250,000,000 | 300,000,000 | 400,000,000 |
| 0.4 | 40,000,000 | 200,000,000 | 400,000,000 | 1,000,000,000 | 1,200,000,000 | 1,600,000,000 |
| 0.5 | 50,000,000 | 250,000,000 | 500,000,000 | 1,250,000,000 | 1,500,000,000 | 2,000,000,000 |
| 1.0 | 100,000,000 | 500,000,000 | 1,000,000,000 | 2,500,000,000 | 3,000,000,000 | 4,000,000,000 |
| 2.0 | 200,000,000 | 1,000,000,000 | 2,000,000,000 | 5,000,000,000 | 6,000,000,000 | 8,000,000,000 |

  Rows 0.4 and 0.5 are asserted in part (DiffT:158-163, :179-184). The others are derived.

### 1.4 Order of the Shop list (`shopEntries`, `CS:1304-1313`)
1. Start from the sold items (every `ItemKind` with a `shopPrice`, `CS:1280-1281`) plus the three eggs.
2. Sort with two keys:
   - a **purchased passive** item (passive with count > 0) goes last (`CS:1316-1319`);
   - otherwise ascending **scaled** price.
- Asserted default order: `mint, rareCandy, egg(nil), egg(uncommon), shinyCharm, egg(rare)`, both with and without an active Pokémon (ShopT:220-259).
- With the Charm owned, the derived order is `mint, rareCandy, egg, uncommon egg, rare egg, shinyCharm`.
- `purchasableItems` is the items-only list with the same sort: `[mint, rareCandy, shinyCharm]`, and an owned charm goes last (`CS:1280-1290`; ShopT:195-212).
- **Eggs stay listed while incubating.** Only the purchase is blocked (`CS:1295-1303`; ShopT:243-259).

### 1.5 Buying items
- **`canBuy(kind)`** (`CS:1322-1326`) is true when all three hold:
  - the item is sold;
  - it is not a passive item already owned;
  - wallet ≥ unit price.
  It does not depend on the egg or the active Pokémon, so stock can be bought ahead (`CS:1321`).
- **`maxBuyCount(kind)`** (`CS:1330-1333`): 0 unless `canBuy` and price > 0. Then 1 for a passive item, else `floor(wallet / price)`.
- **`buy(kind, count)`** (`CS:1338-1344`):
  - It is all-or-nothing. It is rejected with no change when `count < 1` or `count > maxBuyCount` (ShopT:162-176).
  - **There is no clamping**, unlike candy use (§2.2).
  - On success: `spentTokens += price × count`, `inventory[kind] += count`, save.
  - `price × count ≤ wallet ≤ 1e15`, so the debit cannot overflow.
- There is no stock limit. A buy emits no notification and no event.

### 1.6 Buying an egg (release and re-incubate)
**`canBuyEgg(tier)`** (`CS:1360-1367`):
1. `tier` must be in `shopTiers`. This is checked first, so `.legendary` and `.common` are always false (PremT:422-430).
2. Then `hasActive && wallet ≥ scaled egg price`. While incubating it is always false, whatever the wallet holds (PremT:192-200, ShopT:243-259).

**`buyEgg(tier)`** (`CS:1378-1402`) runs these steps in order:
1. `spentTokens += price`.
2. Append `releasedDexEntry(active)` to `dex` (`CS:1381-1383`).
3. `active = nil`. Reconcile the representative; a released species is still owned, so the selection stays (CompT:590-604).
4. Invalidate pending async work (`activeGeneration += 1`) and set `currentLine = nil`.
5. `eggUsage = 0` (the new egg needs a full 5M × growth difficulty), clear `eggPrayers`, `isHatchRetryDelayed = false`.
6. `eggTier = tier`, `pendingHatchID = nil`, `pendingForm = nil`, `prefetchedLineID = nil`.
7. Clear `justGraduated`, `justEvolvedTo` and `eventUntil`, which ends any celebration window.
8. Kick off `ensureEggPrefetch()` asynchronously, then save.

Rules around the purchase:
- **No species is rolled at purchase.** Rolling needs the network, so an offline purchase would otherwise lose the tokens. The prefetch and hatch paths read the tier (`CS:1374-1376`).
- `collectedFinals` is **not** changed, so hatch weights are unaffected (`CS:1385`; FreshT:57, :77-83).
- There is no notification and no event.

**`releasedDexEntry(a)`** (`CS:355-375`):
- `chain = pathIDs.prefix(max(1, stageIndex + 1))`, or `[baseID]` if that is empty. Only reached forms, never `plannedPathIDs` (CompT:744-761).
- `id = profile.instanceID` or a new UUID. `finalID = chain.last`.
- `isShiny = currentIsShiny`, i.e. `displaysShiny`, so a disguised Ditto stays hidden (CompT:765-777).
- `nature`, `profile`, `form` and `prayers` are copied (EggPrayerTests:118-128; UnownTests:429-455).
- `names` holds only chain species from the **loaded** line. It is `nil` when no line is loaded, in which case the dex name backfill handles it.
- `caughtAt = releasedAt = now`.

**High-value guard (UI only).** `isHighValueCompanion = currentIsShiny || rarity == .legendary` (`CS:193-195`).
- `rare` is excluded on purpose, to avoid confirmation fatigue (GuardT:62-67).
- A disguised Ditto reads as common and not shiny (GuardT:75-87).

### 1.7 Shiny Charm
- **Owned** = `inventory.shinyCharm > 0` (`CS:1161`). It is never consumed and cannot be "used" in the Bag (`BV:109`, `BV:124`).
- **Effect:** the hatch shiny test becomes `roll % 48 == 0` instead of `roll % 64 == 0` (`CS:1726-1736`, `CM:249`, `CM:449`).
  - **Only the modulus changes.** It is the same single draw at the same position, step 2 of companion.md §3.4, right after the species and before the nature (`CS:1783-1784`). The draw count and order are unchanged.
  - Asserted: roll 48 is shiny only with the charm, 64 only without, 96 only with, 0 always, 1 never (CharmT:32-47).
- **When it is read:** inside `hatchCore` at hatch time (`CS:1781-1783`). A charm bought during incubation therefore applies to the current egg. It is never retroactive (`CM:248`).
- **Notification odds:** the shiny-hatch body shows the denominator used by that roll, 48 or 64 (`CS:1781-1782`, `LOC:1377` `notifShinyHatchBody`; CharmT:50-61). The Ditto-reveal shiny text never quotes odds (CharmT:64-69).
  - Go today sends the constant 64: `go:store.go:660` `odds := shinyDenominator`.

### 1.8 Premium eggs: how `eggTier` changes the hatch roll
- **Persisted** as `eggTier` (`"uncommon"` or `"rare"`), meaningful only while an egg is incubating. Sanitize and decode rules are already in Go.
- **UI guarantee:** `eggGuarantee = active == nil ? eggTier : nil` (`CS:1354`). The Home egg card shows the badge `eggGuaranteeHint(tier)` in the rarity color (`CV:790-794`).
- **Filter.** `tier.includes(captureRate)` is `captureRate ≤ ceiling`, where the ceiling is uncommon 120 and rare 45 (`CM:83-95`).
  - Legendaries (all cr ≤ 45) pass both filters, which is intended (PremT:113-122, :281-289).
  - Asserted invariant: for every cr in 0…255 and every tier, the filter result equals `Rarity.from(cr) ≥ tier` (PremT:100-109).
- **Weighted pick** (`chooseBase`, `CS:1912-1937`):
  1. `pool = tier ? index.filter(includes) : index` (`CS:1918`).
  2. **An empty pool returns nil before any draw**, and the egg is kept.
  3. Weights are `collectionWeight(cr, collected)` over the **filtered** pool. Exactly **one** draw: `r = next % Σweights(filtered)` (`CS:1927`).
- **REST fallback** (`CS:1942-1963`), at most 16 attempts. Each attempt draws `id = next % 649 + 1` (one draw), then:
  - not a base species (or Ditto) → next attempt;
  - tier miss → `continue` (`CS:1950`), so the attempt **and** its draw are consumed;
  - a network error → nil, and the egg is kept.
- **Prefetch** uses the same `chooseBase`, so the pre-rolled `pendingHatchID` already obeys the tier. The form draw follows the species draw (`CS:1636-1645`).
- **Final gate** in `hatchCore`, after the line fetch and **before any individual draw** (`CS:1759-1767`):
  - If `line.rarity.sortRank < tier.sortRank`, clear `pendingHatchID`, `pendingForm` and `prefetchedLineID`, mark the hatch delayed (if ready), save and return.
  - No draw is consumed. `eggUsage` and `eggTier` are kept, and the next tick re-rolls the species (and form) (PremT:334-348).
  - This catches a stale index, because the index has no legendary flag.
- **Consumption:** a successful hatch sets `eggTier = nil` before the shiny draw (`CS:1777`; PremT:352-358). Graduation never touches the tier (`CS:1150-1151`), so the next free egg has no guarantee (PremT:362-372).
- **Draw order with a tier** is identical to companion.md §3.4 except for the filtered species draw and the draw-free gate:
  ```
  species (filtered pool; prefetch also draws the form right after)
    → gate (no draw)
    → shiny (% 48 with the Charm, else % 64)
    → nature (% 25)
    → [Ditto roll, only when enabled]
    → one draw per non-leaf plan node
    → profile seed
    → [form, only if not prepared]
  ```
- **Go today ignores the tier entirely:** `go:store.go:657` sets `st.EggTier = nil`, `chooseBase` does not filter (`:707-727`), and `chooseBaseViaREST` has no tier test (`:729-746`).

### 1.9 Persistence keys
| Key | Where | Notes |
|---|---|---|
| `spentTokens` | save | int, clamped [0, 1e15] |
| `inventory` | save | `{"rareCandy":n,"mint":n,"shinyCharm":n}`; missing keys mean 0 (`CS:1159`) |
| `eggTier` | save | `"uncommon"`, `"rare"`, or absent |
| `dex[].releasedAt` | save | set by `buyEgg` |
| `shopDifficulty` | upstream UserDefaults; port: GSettings `shop-difficulty`, forwarded and persisted in `settings-applied.json` (§4.4) | not in the save, as upstream (`CS:63-66`) |

---

## 2. Bag

### 2.1 Inventory and listing
- **`ownedItems`** = `ItemKind.allCases` (`rareCandy`, `mint`, `shinyCharm`) filtered to count > 0, in that fixed order rather than price order (`CS:1164-1169`; RCT:603-609).
- An empty list shows the empty state (§3.3).
- Counts are raw ints. Neither upstream nor Go sanitizes `inventory` values (see §6, item 6).

### 2.2 Rare Candy use
**Constants:**
- `RareCandy.xp = 100,000,000` per candy, **not** scaled by difficulty (`CM:225`, `CS:68-70`).
- The XP goes into `usedAtStage` through `applyUsage`, so it never touches `usedSinceInstall` or the wallet.

**Effective stage cost:** `cost(stage) = scaled(phaseThreshold(rarity, k = totalForms, stage, boost ? 2 : 1), growthDifficulty)` (`CS:167-171`, `CM:123-131`). This is the exact rounding chain of companion.md §2.3.

**`stageCosts`** (`CS:1177-1187`):
- Empty unless an active Pokémon exists **and the line is loaded**.
- Empty while a disguised, unrevealed Ditto has `usedAtStage ≥ threshold` (reveal pending).
- Otherwise `[cost(s) for s in stageIndex ..< totalForms]`, the **apparent** planned path.

**`maxUse`** (`CS:1189-1193`):
```
remaining = max(0, Σ stageCosts − usedAtStage)
needed    = ceil(remaining / 1e8)        // integer: r/x + (r%x != 0)
maxUse    = min(stock, needed)
canUseRareCandy = maxUse > 0             // CS:1173
```

**How many candies are needed:**
- To graduate: `needed` above, before the stock cap. That is what the UI stepper's upper bound shows once capped.
- To evolve next: `ceil(max(0, stageCosts[0] − usedAtStage) / 1e8)`. Upstream never displays this; it is derivable.

**`plan(count)`** (`CS:1195-1221`):
```
c = min(max(0, requested), maxUse)           // clamps; ≤0 → nil
if c == 0 || active == nil → nil
rem = usedAtStage + c·1e8; evolves = false
for (i, cost) in stageCosts:
    if rem < cost: break
    rem -= cost
    if i == last: return {count:c, evolves, graduates:true, carryover:0, discarded:rem}
    evolves = true
return {count:c, evolves, graduates:false, carryover: evolves ? rem : 0, discarded:0}
xp = c·1e8
```
- In the graduation branch, `evolves` only reports whether **earlier** stages were passed, so k = 1 gives false.
- The carryover is reported only when it evolves, otherwise 0.

**`use(count)`** (`CS:1229-1249`):
1. `preview = plan(count)`. If it is nil, return `.unavailable` and change nothing (RCT:410-426, :557-590).
2. `consumed = preview.count`.
3. For a disguised, unrevealed Ditto, cap it: `consumed = min(consumed, ceil(max(0, threshold − usedAtStage) / 1e8))`, so whole candies beyond the reveal stay in stock (`CS:1231-1238`).
4. `inventory.rareCandy −= consumed`.
5. Set `candyFeedbackAmount = consumed·1e8` and `candyFeedbackSeq += 1` **before** `applyUsage` (`CS:1241-1244`).
6. `applyUsage(consumed·1e8)`. It runs the normal evolve and graduate loop with carry-over, the notifications, the celebration, the level-up window and the save; graduation discards the excess (`CS:1147`).
7. Return `.graduated` if `active == nil`, `.evolved` if `stageIndex` rose, otherwise `.progressed` (`CS:1246-1248`).

**Count semantics:** use **clamps** to `maxUse` (`useRareCandy(count: Int.max)` uses 1 when the stock is 1, RCT:418-425), but `count ≤ 0` is unavailable. A buy rejects a count above its maximum (§1.5).

**One candy can pass several stages** when stage costs are below 100M (low difficulty or the 2× boost). At default difficulty without the boost it passes at most one (RCT:496-503, :505-524; `CM:222-224`).

### 2.3 Mint use (`CS:1251-1271`)
- **Condition:** `canUseMint = hasActive && stock > 0` (`CS:1255`). **No loaded line is required**, so it works offline and right after a restart.
- **Roll:** `pool = the 25 natures in declaration order, minus the current one` (all 25 when nature is nil), then `new = pool[next % pool.count]`. That is **one** draw from the game RNG (`CS:1262-1264`).
  - The result always differs from the previous nature (MintT:35-54).
- **Effects:**
  - `nature = new`, `inventory.mint −= 1`, `mintFeedbackNature = new`, `mintFeedbackSeq += 1`, save.
  - It returns the new nature; nil means unusable, and nothing is consumed (MintT:81-97).
  - Unchanged: `usedAtStage`, `stageIndex`, species, shiny, `usedSinceInstall` (MintT:66-77).
- **Stats.** The Swift comment calls Mint "purely cosmetic" (`CS:1257-1258`), **but** the non-HP actual stats in the species detail are `floor(neutral × nature modifier)` with ±10% (`PP:225-240`, `PP:245-273`). A Mint therefore changes the displayed stats.
  - Go computes the stats at view time from the stored nature (`go:views.go:646`, `go:profile.go:215`), so there is **nothing to recompute in the save**. The next `getSpecies` reflects the change.
  - The Home stage line (`"Stage 2/3 · Adamant"`) changes too.
- **Not a Mint effect:** a nature is never re-rolled at evolution. Released and graduated entries copy the nature current at that moment.

### 2.4 Evolve and graduate controls in the Bag
**There are none.** `BV:1-170` has no evolve or graduate button, and graduation is only reachable through the private `graduate()` (`CS:1123`). `docs/spec/ui.md` §9 ("Bag tab: candies, mints, evolve, graduate controls") is wrong.

From the Bag, evolution and graduation happen only as a side effect of candy use (`applyUsage`), gated by `canUseRareCandy` (§2.2). Their preview lines are §3.3.

### 2.5 Feedback effects (UI, Home tab)
- **Tab switch.** After any Bag use the popover **switches to Home** (`BV:165-169`), even when the use failed. A successful egg purchase in the Shop also switches to Home (`SV:262-265`); item purchases do not.
- **"+XP" capsule** (`CV:725-733`, `CV:880-891`):
  - Text `+<compact(xp)> XP`, e.g. `+100M XP` or `+300M XP`.
  - Caption, bold, orange, on a `regularMaterial` capsule (horizontal padding 6, vertical 2), top-center of the 76 pt sprite tile, offset y −16.
  - It springs in (response 0.4, damping 0.6), stays 1.3 s, then eases out over 0.4 s.
  - It plays once: Swift guards it with a sequence number and calls `consumeCandyFeedback`.
  - `xp` is the **consumed** amount, which for a Ditto can be less than the selected count (DittoT:339-373).
- **Mint sparkle** (`CV:735-744`, `CV:893-902`):
  - Three ✨ glyphs, 22, 15 and 12 pt, at offsets (−11, −9), (13, 5) and (1, 13) over the tile.
  - It springs in (0.35, 0.5), stays 0.9 s, then fades over 0.4 s. It has no text and plays once.
- **Evolve and graduate** celebrations, events and notifications come from `applyUsage` exactly as in M1.
- **Import or load** clears pending feedback (`CS:2040-2041`).
- **Port:** the extension triggers both effects from the command **reply** (§4.3) after switching to Home. A transient St actor with `ease()` covers both. Celebrations still come from the `evolve`/`graduate` events.

---

## 3. UI layout (same detail level as `docs/spec/ui.md`)

### 3.1 Tab bar and frame
- **Tabs:** the segmented picker reads, in order, `home` "Home", `shop` "Shop", `bag` "Bag" and `collection` "Collection" (`PV:4`, `PV:153-160`; `LOC:24`, `LOC:1878`, `LOC:1798`).
  - M1 hid Shop and Bag (ui.md §2.0); M2 shows all four.
- **Height:** both tabs use a fixed scroll height of 520, like Home and Collection (`SV:31`, `BV:22`).
  - Each sits in an `St.ScrollView` with the right scroller lane (`reservesScrollerLane`).
  - Content spacing is 10 in the Shop and 8 in the Bag.
- **Cards:** every card is a rounded rectangle of radius 10, padding 10, with a `secondary` background at 6% opacity (`SV:99-101`, `BV:96-98`).
- **No modal dialogs.** Every confirmation is an **inline button morph** inside the card. Upstream forbids sheets and alerts in the transient popover (`SV:3-5`, `BV:42-43`).
  - In St, swap the card's control row in place. Never open a `ModalDialog` from the panel menu.

### 3.2 Shop tab (`SV:7-48`)
From top to bottom:

**1. Wallet header** (`SV:34-47`), a full-width card:
- caption, secondary: `spendableTokens` "Spendable tokens";
- the wallet in **24 pt bold monospaced digits**, `compact(wallet)`;
- caption2, tertiary: `shopHint` "Spend the tokens you've used on items."

**2. Entries**, in `shop.entries` order (§1.4). Each is an item card or an egg card.

**Item card** (`SV:54-152`):
- **Header row:**
  - a 30 pt item icon: the sprite, scaled aspect-fit with nearest filtering, or the fallback emoji while loading, unavailable or failed (`CV:17-49`);
  - the name (`itemName`, callout semibold);
  - for a **consumable** with count > 0: `ownedCount(n)` "Owned ×n" (caption2 bold, secondary, monospaced digits) (`SV:75-79`);
  - a spacer;
  - a **quantity stepper** only when the item is consumable **and** `maxBuyCount > 1` (`SV:65`, `SV:81-88`). It shows `×N`, covers 1…`maxBuyCount`, and has the item name as its accessible label.
- **Description** below the name: `itemDescription(kind)` (caption, secondary, wrapping).
- **Control row** (`SV:113-146`), one of three:
  1. **Passive item owned:** a green `checkmark.seal.fill` plus `ownedAlready` "Owned" (caption2 semibold, green). There is no button.
  2. **Confirming:**
     - text `buyConfirm(name)` "Buy <name>?", where name is `"<itemName> ×N"` when N > 1 (`SV:108-110`); caption2, secondary, one line;
     - spacer;
     - a prominent **Buy** button, labelled `"Buy ×N"` when N > 1;
     - a borderless **Cancel** (`cancel`).
  3. **Idle:**
     - `shopPriceLabel` "Price" plus `compact(unitPrice × N)` (caption2, tertiary, monospaced). The price shows the **selected total**;
     - spacer;
     - if `canBuy`, a bordered small **Buy**, which switches to confirming;
     - otherwise the caption `notEnoughTokens` "Not enough tokens".
- **Behaviour:**
  - On a successful buy, reset N to 1 (`SV:148-151`). On failure N is kept, and the card leaves the confirming state either way.
  - When `maxBuyCount` changes (the wallet moved), clamp N to the new maximum (`SV:63`, `SV:102-104`).

**Egg card** (`SV:161-266`):
- **Header:**
  - the egg sprite at 26 pt inside a 30 pt frame (`SV:176-177`);
  - the name `eggName(tier)`;
  - for a tier, a **rarity badge** with `rarityLabel(tier)` uppercased, in the rarity color (`SV:181-184`).
- **Below the name:**
  - `eggDescription(tier)` (caption, secondary);
  - only while an active Pokémon exists, `eggReleaseNote` (caption2, tertiary) (`SV:193-197`).
- **Controls, idle** (`SV:211-234`):
  - "Price <compact(price)>", then a spacer, then one of:
    - **no active Pokémon:** a **disabled** Buy button, plus the line `eggShopLockedHint` below the row;
    - `canBuyEgg`: a bordered **Buy**, which goes to *confirm*;
    - otherwise "Not enough tokens".
- **Controls, confirm** (`SV:235-247`):
  - `eggConfirm(displayName, eggName)` (caption2, secondary, 2 lines);
  - a prominent **Buy**: if `isHighValueCompanion`, go to *preciousConfirm*, else commit;
  - **Cancel**, which returns to idle.
- **Controls, preciousConfirm** (`SV:248-257`):
  - The warning text is caption2 semibold in **orange**: `freshEggLegendaryWarning` if the rarity is legendary, else `freshEggShinyWarning`.
  - The button is prominent with an **orange tint**: `freshEggDiscardShiny` "Send shiny off" if shiny and not legendary, else `freshEggDiscardValuable` "Send off". It commits.
  - **Cancel.**
- **Commit:** `buyEgg(tier)`. On success switch to the **Home** tab (`SV:262-265`).
- **`displayName`** is `"Token Egg"` (hard-coded, untranslated) while the line is not loaded (`CS:258-261`). The port should use the localized display name of `companion.active.displayName`.

### 3.3 Bag tab (`BV:1-170`)
**Empty state** (`BV:26-38`), shown when no inventory count is > 0:
- an animated **Snorlax (#143)** sprite at 96 pt (`BV:29`), loaded with `getSprite {speciesId:143, animated:true}`;
- `bagEmptyTitle` "Your bag is empty!" (callout semibold);
- a bordered small **Shop** button that switches to the Shop tab;
- 28 pt vertical padding, centered.

**Otherwise**, one **item card** per owned item, in the fixed order `rareCandy`, `mint`, `shinyCharm` (`BV:16-19`).

**Item card** (`BV:45-170`):
- **Header row:**
  - the 30 pt icon and the name;
  - for a consumable, `"×<count>"` (caption bold, secondary, monospaced);
  - a spacer;
  - for **Rare Candy when `canUseRareCandy`**, a stepper covering 1…`maxUse` that shows `×N` (`BV:68-75`). It shows even when `maxUse == 1`, unlike the Shop.
- **Description:** `itemDescription(kind)`.
- **Candy preview** (`BV:82-93`), only for Rare Candy and only when `plan(N)` is non-nil. In order:
  - if `graduates`: `candyGraduates` "Expected to graduate." (caption2, secondary);
  - then, if `discardedXP > 0`: `candyDiscardedXP(compact(discarded))` in **orange** (caption2, wrapping);
  - else, if `evolves && !graduates`: `candyCarryoverXP(compact(carryover))` (caption2, secondary). A carryover of 0 prints "0 XP".
- **Control row** (`BV:128-162`):
  1. **Passive** (the Shiny Charm): a green seal plus `shinyCharmEffectHint` "Shiny rate ↑ · active" (caption2 semibold, green). There is no button.
  2. **Usable** (candy: `canUseRareCandy`; mint: `canUseMint`):
     - *Idle:* the effect hint (caption2, tertiary, monospaced), then a spacer, then a bordered small `useItem` **Use** button.
       - For candy the hint is `"+<compact(N × 1e8)> XP"`; for mint it is `mintEffectHint` "Random nature" (`BV:113-119`).
     - *Confirming:*
       - `useOnCurrent(displayName)` "Use on <name>?" (caption2, secondary, 1 line);
       - a prominent `use` button, labelled `"Use ×N"` for candy;
       - **Cancel**.
  3. **Not usable:** caption2, tertiary: `useAfterHatch` "Usable after hatching" while incubating, else `useNeedsPokemon` "No Pokémon to use it on" (`BV:159-161`).
     - This second string also shows when a Pokémon **exists** but the line is loading, or a Ditto reveal is pending. That is a quirk; see §6, item 3.
- **After use:** call `useCandy {count: N}` or `useMint`, then **always switch to Home** and play the feedback (§2.5).
- **Clamping:** clamp N to `maxUse` whenever it changes (`BV:53`, `BV:99-101`).

### 3.4 Settings: shop prices (`DSS:25-83`)
**Upstream:**
- One "Difficulty" section with two rows.
  - Each row is a 76 pt label, a log slider over positions 0…1, and a 52 pt value `difficultyValue(v)` (e.g. "100%").
  - Under each slider sit the low and high labels.
- Growth: "Easy" to "Hard". Shop prices: `difficultyShopLabel` "Shop prices", from `difficultyShopLowLabel` "Cheap" to `difficultyShopHighLabel` "Expensive" (`LOC:413-431`).
- Both rows edit **one draft**, and a single **Save** (`save`, `LOC:575`) appears when either value differs (`DSS:4-22`, `DSS:48-55`).
- The accessibility hint is `difficultyScaleHint(low:high:)`: "Lower: Cheap, higher: Expensive".

**Port** (`extension/prefs.js:223-284`): the growth row is already an `Adw.ActionRow` with a value label and its own **Save**, plus a `Gtk.Scale` row using `prefsModel.difficultyAtPosition`.
- Add a "Shop prices" block with the same widgets and the "Cheap"/"Expensive" labels, writing `shop-difficulty`.
- The mapping, the snap to 1.0 within ±0.01 of the track, and the 2-significant-digit snap are the same as growth (companion.md §2.4).
- **Decision needed:** one shared Save as upstream, or one Save per row as the current prefs code does (§6, item 14). I recommend per row: the two values are independent and the shop value needs no rescale.
  **Resolved (2026-11-19):** no draft and no Save for the shop row; it applies immediately (§6, item 14).

### 3.5 Strings (English column of `LOC`; key = the Swift accessor)
| Key | English | Line |
|---|---|---|
| `shop` | Shop | `LOC:1878` |
| `bag` | Bag | `LOC:1798` |
| `spendableTokens` | Spendable tokens | `LOC:1879` |
| `shopHint` | Spend the tokens you've used on items. | `LOC:1880` |
| `buy` | Buy | `LOC:1881` |
| `buyConfirm(name)` | Buy {name}? | `LOC:1882` |
| `notEnoughTokens` | Not enough tokens | `LOC:1883` |
| `ownedCount(n)` | Owned ×{n} | `LOC:1884` |
| `shopPriceLabel` | Price | `LOC:1885` |
| `ownedAlready` | Owned | `LOC:1886` |
| `shinyCharmEffectHint` | Shiny rate ↑ · active | `LOC:1887` |
| `itemName(rareCandy/mint/shinyCharm)` | Rare Candy / Mint / Shiny Charm | `LOC:1835-1841` |
| `itemDescription(rareCandy)` | Raises your Pokémon's EXP by {compact(1e8)}. (renders "100M") | `LOC:1844-1853` |
| `itemDescription(mint)` | Randomly changes your Pokémon's nature. | `LOC:1854-1862` |
| `itemDescription(shinyCharm)` | While owned, raises the chance of hatching a shiny. | `LOC:1863-1871` |
| `mintEffectHint` | Random nature | `LOC:1875` |
| `bagEmptyTitle` | Your bag is empty! | `LOC:1799` |
| `useItem` | Use | `LOC:1800` |
| `use` | Use | `LOC:1801` |
| `cancel` | Cancel | `LOC:1802` |
| `useOnCurrent(name)` | Use on {name}? | `LOC:1803-1805` |
| `useAfterHatch` | Usable after hatching | `LOC:1806` |
| `useNeedsPokemon` | No Pokémon to use it on | `LOC:1807` |
| `candyGraduates` | Expected to graduate. | `LOC:1810-1814` |
| `candyCarryoverXP(xp)` | Expected after evolution: {xp} XP carried over. | `LOC:1815-1824` |
| `candyDiscardedXP(xp)` | On graduation, {xp} leftover XP will be discarded. | `LOC:1825-1832` |
| `eggName(nil)` | Pokémon Egg | `LOC:1893` |
| `eggName(uncommon)` | Uncommon Egg | `LOC:1894` |
| `eggName(rare)` | Rare Egg | `LOC:1895` |
| `eggDescription(nil)` | Send off your current Pokémon and start fresh with a new egg. | `LOC:1899-1909` |
| `eggDescription(tier)` | Send off your current Pokémon for an egg guaranteed to hatch {rarityLabel} or better. | `LOC:1910-1918` |
| `eggShopLockedHint` | Available once your current egg hatches. | `LOC:1921-1930` |
| `eggReleaseNote` | A released Pokémon stays in your Pokédex and can hatch again at the same odds — only the growth progress is lost. | `LOC:1940-1949` |
| `eggGuaranteeHint(tier)` (Home egg badge) | {rarityLabel} or better | `LOC:1951-1954` |
| `eggConfirm(mon, egg)` | Send off {mon} for the {egg}? | `LOC:1955-1964` |
| `freshEggShinyWarning` | ⚠️ This one is shiny! Really send it off? | `LOC:1965` |
| `freshEggLegendaryWarning` | ⚠️ This is a Legendary Pokémon! Really send it off? | `LOC:1966` |
| `freshEggDiscardShiny` | Send shiny off | `LOC:1967` |
| `freshEggDiscardValuable` | Send off | `LOC:1968` |
| `rarityUncommon` / `rarityRare` / `rarityLegendary` | Uncommon / Rare / Legendary | `LOC:1257-1259` |
| `difficultyShopLabel` | Shop prices | `LOC:415` |
| `difficultyShopLowLabel` / `difficultyShopHighLabel` | Cheap / Expensive | `LOC:418-419` |
| `difficultyScaleHint(low, high)` | Lower: {low}, higher: {high} | `LOC:420-424` |
| `notifCandyTitle(item, count)` (existing M1 event) | 🍬 You got {count}× {item}! | `LOC:1971-1980` |
| `notifCandyBody(window)` | You maxed out your {window} token limit. A treat for the effort — use it to evolve your Pokémon! | `LOC:1981-1990` |
| (literal, no key) | "×{n}", "Buy ×{n}", "Use ×{n}", "+{xp} XP" | `SV:83`, `SV:126`, `BV:70`, `BV:143`, `BV:115` |

- **Korean particles:** `eggConfirm` and `useOnCurrent` use them (`LOC:1956`, `LOC:1804`).
- **Mint has no sprite**, so the port always shows the 🌿 emoji in an `St.Label`, or a bundled symbolic icon.

---

## 4. Proposed protocol additions (`docs/protocol.md`, still protocol 1 per §2.7)

### 4.1 State: `companion` gains or extends
| Field | Type | Meaning | Consumer |
|---|---|---|---|
| `wallet` | int | `max(0, usedSinceInstall − spentTokens)` | Shop header |
| `inventory` | `{rareCandy:int, mint:int, shinyCharm:int}` | Every key is always present (protocol §1.1). An item is owned iff its count is > 0. **Extends** the M1 `{rareCandy}`. | Bag list, Shop "Owned ×n" |
| `egg.guarantee` | Rarity \| null | `eggTier` while incubating (`uncommon` or `rare`) | Home egg badge `eggGuaranteeHint` |
| `shop` | Shop | below | Shop tab |
| `bag` | Bag | below | Bag tab |

**Shop:**
- `entries`: ShopEntry[6], already in display order (§1.4).
- `shinyOdds`: int, 48 or 64. Optional display; the `hatch` event carries the odds actually used.

**ShopEntry:**

| Field | Type | Meaning |
|---|---|---|
| `kind` | `item` \| `egg` | |
| `item` | Item \| null | for `item` |
| `tier` | Rarity \| null | for `egg`; `null` is the plain egg |
| `price` | int | **scaled unit price** (§1.3) |
| `passive` | bool | `shinyCharm` only |
| `owned` | int | inventory count; 0 for eggs |
| `purchased` | bool | passive and owned, which shows "Owned" |
| `canBuy` | bool | items: `canBuy`. Eggs: `canBuyEgg`, false while incubating. |
| `maxCount` | int | items: `maxBuyCount`. Eggs: 1 if `canBuy`, else 0. |

- The extension shows the egg "locked" state when `companion.phase == "egg"`. It needs no extra field.

**Bag:**
- `candyMaxUse`: int, `min(stock, needed)` (§2.2). 0 disables candy.
- `canUseMint`: bool.
- `candyBlocked`: `null` \| `egg` \| `noStock` \| `lineLoading` \| `revealPending`, so the port can show a truthful disabled reason (§6, item 3).

**Budget:** about 700 bytes more per `state`, which stays under 8 KB. Do **not** put a per-count plan table in `state`. Up to 120 counts exist (legendary k = 1 at 2.0× is 12B / 1e8), so previews go through `planCandies`.

**`settingsApplied`** gains `shopDifficulty` (number).

**Enumerations:**
- `Item` = `rareCandy`, `mint`, `shinyCharm`. Fallback: skip the entry.
- `CandyResult` = `evolved`, `graduated`, `progressed`.
- New error codes are listed in §4.3.

### 4.2 Commands
Mutating commands apply in **arrival order**, so add them to protocol §4's ordered set with `setSettings` and `setRepresentative`. The reply order is **state → events (evolve/graduate) → result**.

| `cmd` | `args` | Reply `data` | Reply sent | Ext. timeout |
|---|---|---|---|---|
| `buyItem` | `item` (Item), `count` (int ≥ 1, default 1) | `{item, count, spent, wallet, owned}` | after saving | 10 s |
| `buyEgg` | `tier` (`null` \| `uncommon` \| `rare`) | `{tier, spent, wallet, releasedEntryId}` | after saving; the prefetch starts afterwards in the background | 10 s |
| `planCandies` | `count` (int) | `{plan: CandyPlan \| null}` | immediately, on the owner goroutine (read-only) | 5 s |
| `useCandy` | `count` (int ≥ 1) | `{consumed, xp, result: CandyResult}` | after applying and saving | 10 s |
| `useMint` | none | `{nature}` | after saving | 10 s |

- **CandyPlan** = `{count, xp, evolves, graduates, carryoverXP, discardedXP}`, exactly §2.2. The `count` is the **clamped** count. `null` means the clamped count is 0, so the extension shows no preview.
  - The extension calls `planCandies` on each stepper change and when the Bag opens. Its stdio round trip is milliseconds.
- **`buyItem` validation order:**
  1. unknown `item`, or `count` < 1 or not an int → `badArgs`;
  2. passive item and `count` ≠ 1 → `badArgs`;
  3. passive item already owned → `alreadyOwned`;
  4. `count > maxBuyCount` → `insufficientFunds`.
  Nothing is applied on any error, as upstream's all-or-nothing rule requires.
- **`buyEgg` validation order:**
  1. `tier` not in `[null, uncommon, rare]` (including `common` and `legendary`) → `badArgs`;
  2. no active Pokémon → `noActive`;
  3. wallet < price → `insufficientFunds`.
- **`useCandy`:**
  - errors are checked in the order `badArgs` (count < 1), `noActive`, `noStock`, `notReady`;
  - `notReady` means the line is not loaded or a Ditto reveal is pending (this activates the reserved protocol §11.2 code);
  - a count above `candyMaxUse` is **clamped**, not rejected (upstream semantics);
  - `consumed` may be less than the clamped count only for a disguised Ditto.
- **`useMint`:** `noActive`, then `noStock`. It works while the line is loading.

### 4.3 Errors (protocol §11.2 additions)
| `code` | Meaning | Extension |
|---|---|---|
| `insufficientFunds` | wallet below the total price | refresh from the next `state`; the button should already have been hidden |
| `alreadyOwned` | passive item already owned | refresh |
| `noActive` | needs a hatched Pokémon | refresh |
| `noStock` | inventory count is 0 | refresh |
| `notReady` | (existing, reserved) the candy needs the evolution line, or a Ditto reveal is pending | keep the button disabled until the next `state` |

### 4.4 Events
- **No new kinds are needed.** Candy-driven evolution and graduation reuse `evolve` and `graduate`, emitted before the `useCandy` result.
- **Feedback** ("+XP", sparkle) is driven by the **reply**, because events are not replayed and the popover is open when the user acts.
- **`hatch.shinyOdds`** becomes 48 when the Charm was owned at hatch time; it was always 64 in M1.

### 4.5 GSettings and `setSettings`
| Key | Type | Default | Range | Scope | Spec |
|---|---|---|---|---|---|
| `shop-difficulty` | `d` | 1.0 | 0.1–2.0 | forwarded as `shopDifficulty` | §1.3; prefs §3.4 |

- **`setSettings.shopDifficulty`** is an **optional** number. Protocol §9.2 says every field is required, but §2.7 allows only new **optional** args without a bump. Amend §9.2 to read "every M1 field is required".
  - A missing value keeps the last applied one.
  - Normalization: clamp to 0.1–2.0, non-finite → 1.0.
  - It never rescales anything.
- **Persistence:** in `settings-applied.json`. At startup the daemon calls `store.SetShopDifficulty(applied.ShopDifficulty)` **before** the first cached `state`, so prices are right from the first frame.
- Remove "shop difficulty" from the "LATER keys" line of protocol §9.1.

### 4.6 Fixture to add
`state-shop.json`:
- wallet 3.2B with the Charm owned;
- one mint and three candies;
- an active Pokémon at stage 1/3, with `bag.candyMaxUse` 3;
- `shop.entries` in the owned-Charm order.

---

## 5. Go work list (`helper/internal/companion/` + daemon)

### 5.1 Functions to add (signature sketch)
```go
// balance.go — constants (CM:221-275, CM:449)
const (
    rareCandyXP           = 100_000_000
    rareCandyPrice        = 500_000_000
    mintPrice             = 100_000_000
    shinyCharmPrice       = 3_000_000_000
    shinyCharmDenominator = 48
    freshEggPrice         = 1_000_000_000
)
const (itemRareCandy = "rareCandy"; itemMint = "mint"; itemShinyCharm = "shinyCharm")
var itemKinds = []string{itemRareCandy, itemMint, itemShinyCharm} // ItemKind.allCases order (Bag order)
func itemBasePrice(item string) (int, bool)          // false = not sold / unknown
func itemPassive(item string) bool                   // shinyCharm
var eggShopTiers = []string{"", rarityUncommon, rarityRare} // "" = plain egg (nil)
func eggBasePrice(tier string) int                   // round(1e9 * T(tier)/T(common)); "" → 1e9
func captureRateCeiling(r string) (int, bool)        // rare 45, uncommon 120, common 255, legendary none
func tierIncludes(tier string, captureRate int) bool  // ceiling known && cr <= ceiling
func shinyOdds(charm bool) int                        // 48 : 64
func rollsShiny(roll uint64, charm bool) bool         // roll % odds == 0

// store.go — Store gains `shopDifficulty float64` (default 1.0; not in the save)
func (s *Store) SetShopDifficulty(v float64)          // clampDifficulty; no rescale, no save
func (s *Store) ShopDifficulty() float64

// shop.go (new)
func (s *Store) wallet() int                          // max(0, used − spent)
func (s *Store) itemCount(item string) int
func (s *Store) itemPrice(item string) int            // scaled(itemBasePrice, shopDifficulty)
func (s *Store) eggPrice(tier string) int             // scaled(eggBasePrice(tier), shopDifficulty) — two roundings
func (s *Store) canBuy(item string) bool
func (s *Store) maxBuyCount(item string) int
func (s *Store) Buy(item string, count int) error     // ErrBadArgs | ErrAlreadyOwned | ErrInsufficientFunds
func (s *Store) canBuyEgg(tier string) bool           // tier ∈ eggShopTiers && active != nil && wallet ≥ price
func (s *Store) BuyEgg(tier string) (releasedID string, err error) // §1.6 steps 1-7; the daemon kicks the prefetch
func (s *Store) releasedEntry(a *monState) dexEntry   // §1.6 releasedDexEntry
func (s *Store) shopEntries() []ShopEntry             // §1.4 sort (stable sort; no ties)
func (s *Store) isHighValue() bool                    // displaysShiny || rarity == legendary (for the UI field, optional)

// bag.go (new)
type CandyPlan struct { Count, XP int; Evolves, Graduates bool; CarryoverXP, DiscardedXP int }
func (s *Store) candyStageCosts() []int               // §2.2; nil when no active or s.line == nil or reveal pending
func (s *Store) MaxCandyUse() int
func (s *Store) PlanCandies(count int) *CandyPlan     // clamps; nil when the clamped count is 0
func (s *Store) UseCandy(count int) (consumed int, result string, err error) // ErrBadArgs|ErrNoActive|ErrNoStock|ErrNotReady
func (s *Store) canUseMint() bool
func (s *Store) UseMint() (string, error)             // one s.rng.Next() draw; ErrNoActive|ErrNoStock

// errors.go (new): ErrBadArgs, ErrInsufficientFunds, ErrAlreadyOwned, ErrNoActive, ErrNoStock, ErrNotReady
```

### 5.2 Changes to existing Go code
1. **`chooseBase`** (`go:store.go:707-727`):
   - Filter `idx` by `tierIncludes(*st.EggTier, cr)` when a tier is set.
   - If the filtered pool is empty, return `(0, false)` **without drawing**.
   - Compute the weights and the one draw over the filtered pool.
2. **`chooseBaseViaREST`** (`go:store.go:729-746`): after the `EvolvesFrom == 0` check, `continue` when a tier is set and `!tierIncludes(tier, sp.CaptureRate)`. The attempt and its draw stay consumed.
   - The 132 skip happens before the fetch, as upstream's `baseSpecies` does (`API:239`).
3. **`hatchCore`** (`go:store.go:641-694`):
   - After `fetchLine` and **before** clearing the pending state or drawing, add the gate: if `rarityRank(l.rarity) < rarityRank(*st.EggTier)`, clear `PendingHatchID`, `PendingForm` and `prefetchedLineID`, set `hatchDelayed` if the egg is ready, `save()` and return.
   - Keep `st.EggTier = nil` at its current place (upstream `CS:1777`).
   - Replace `odds := shinyDenominator` with `odds := shinyOdds(st.Inventory[itemShinyCharm] > 0)`. The shiny test becomes `rollsShiny(s.rng.Next(), charm)`, still exactly one draw.
4. **`views.go`:**
   - `Inventory` gains `Mint` and `ShinyCharm`.
   - `EggView` gains `Guarantee *string` (`json:"guarantee"`).
   - `CompanionView` gains `Wallet int`, `Shop ShopView` and `Bag BagView` (§4.1).
5. **Daemon** (`helper/internal/daemon/commands.go:23-72`, `settings.go`):
   - Add the five commands and the optional `shopDifficulty`.
   - Mutating commands run on the owner goroutine in arrival order.
   - After `UseCandy`, `drain()` the store events, emit `state`, then the events, then the result.
   - Arm the `levelUp` timer when a window opened (`daemon.go:520`) and refresh the menu-subject sprite. A graduation turns the menu sprite into the egg, which is the Go form of RCT:541-555 (the menu must update without waiting for a usage poll).
   - After `BuyEgg`: emit `state` and reply, then run the prefetch through the normal `companionUpdate` path. The store must not block the reply on the network.
6. **`BuyEgg` reset list.** Go has no `activeGeneration`. Reset the fields its graduate path resets (`go:store.go:421-427`): `line = nil`, `hatchDelayed = false`, `EggUsage = 0`, `EggPrayers = nil`. Also reset `prefetchedLineID = 0`, `window = levelUpWindow{}` and `warmPending = false`, and set `EggTier`.
7. **Optional hardening** (§6, item 6): clamp `inventory` values to [0, 1e15] in `sanitize`. This is a deviation, so decide first.

### 5.3 Size estimate
| Area | LOC |
|---|---|
| Go `balance.go` additions | ~40 |
| Go `shop.go` | ~170 |
| Go `bag.go` | ~120 |
| Go hatch changes | ~30 |
| Go views | ~70 |
| Go daemon commands and settings | ~180 |
| **Go production total** | **~600** |
| Go tests | ~900–1,100 |
| JS `lib/popover/shopTab.js` | ~330 |
| JS `lib/popover/bagTab.js` | ~280 |
| JS feedback overlays in `companionCard.js` | ~80 |
| JS tab bar change | ~30 |
| JS `helperClient` commands | ~50 |
| JS view-model and format helpers | ~60 |
| JS prefs shop slider | ~60 |
| **JS total** | **~900** |
| gettext strings | ~45 |

### 5.4 Swift tests to port (primary oracle; Go test names suggested)

**Shared Go helpers.**
- `shopStore(t, used, spent int, inv map[string]int, active string)` writes the same JSON as the Swift helper: ShopT:19-27, MintT:19-31, CharmT:21-28, PremT:74-94, FreshT:18-28. Reload with `openAt` on the same dir.
- `giveCandies(s, n)` = `GrantCandies(nil, true)` (seeding), then n session windows `test.session.i` at utilization 100 (RCT:280-285).
- Lines: `rcLinear3` = `linear3()` (common 1→2→3; 125M/250M/375M at default, RCT:14), `rcNoEvo` = `noEvo()` (common, single 750M, RCT:15).
- Seeds: tests use SplitMix64 seed 7 unless noted.

#### ShopTests.swift (23)
| Test | Line | Setup | Expected (asserted) |
|---|---|---|---|
| testAvailableEqualsUsedWhenNothingSpent | ShopT:31 | used 1e9 | wallet 1,000,000,000 |
| testAvailableSubtractsSpent | :35 | used 1e9, spent 3e8 | 700,000,000 |
| testAvailableNeverNegative | :40 | used 1e8, spent 5e8 | 0 |
| testDecodesWithoutSpentTokens | :45 | JSON without `spentTokens`, used 900 | spent 0, used 900 |
| testSpentTokensRoundTrip | :52 | used 1000, spent 400; encode then decode | spent 400 |
| testCanBuyAtExactPrice | :62 | used 500,000,000 | canBuy(rareCandy) true |
| testCannotBuyOneBelowPrice | :66 | used 499,999,999 | false |
| testBuyDebitsWalletAndCreditsInventory | :72 | used 1e9; buy candy | candy 1, spent 5e8, wallet 5e8, used 1e9 unchanged |
| testBuyInsufficientIsNoOp | :82 | used 4e8 | buy false, candy 0, spent 0 |
| testMultipleBuysUntilBroke | :90 | used 1.2e9; buy ×3 calls | true, true, false; candy 2; spent 1e9; wallet 2e8 |
| testBuyAddsToExistingStock | :101 | used 1e9, candy 3 | candy 4; ownedItems first = (rareCandy, 4) |
| testBuyPersistsAcrossRestart | :110 | used 1e9; buy; reopen | candy 1, spent 5e8, wallet 5e8 |
| testMaxBuyCountIsFloorOfWalletOverPrice | :127 | used 1.2e9 | max candy 2, max mint 12; used 499,999,999 → max candy 0 |
| testMaxBuyCountCapsPassiveAtOne | :134 | used 30e9 | max charm 1; buy(charm, 2) false, spent 0; buy(charm) true; then max 0 |
| testBuyCountDebitsTotalAndCreditsInventory | :144 | used 2e9, candy 1; buy(candy, 3) | candy 4, spent 1.5e9, wallet 5e8, used 2e9 |
| testBuyExactlyMaxCountEmptiesWallet | :154 | used 1.5e9; buy(candy, max) | candy 3, wallet 0 |
| testBuyCountAboveAffordableIsNoOp | :162 | used 1.2e9; buy(candy, 3) | false, candy 0, spent 0 |
| testBuyNonPositiveCountIsNoOp | :170 | used 1e9; counts 0 and −1 | false, false; candy 0, spent 0 |
| testBuyCountPersistsAcrossRestart | :179 | used 1e9; buy(mint, 4); reopen | mint 4, spent 4e8 |
| testItemsSortedByPriceAscending | :195 | used 0 | purchasable = [mint, rareCandy, shinyCharm]; prices ascending |
| testOwnedPassiveSinksToBottom | :204 | charm 1 | purchasable.last = shinyCharm |
| testShopEntriesInterleavesFreshEggByPrice | :220 | active base 10, common, k=3, used 5e9 | entries = [item mint, item rareCandy, egg nil, egg uncommon, item shinyCharm, egg rare]; prices ascending |
| testShopEntriesKeepsEggsVisibleButUnbuyableWhenNoActive | :243 | no active, used 5e9 | same order; for each tier canBuyEgg false and buyEgg false; wallet still 5e9 |

#### MintTests.swift (11), seed 7 unless noted
| Test | Line | Expected (asserted) | Derived pin |
|---|---|---|---|
| testUseMintChangesNatureToDifferent | MintT:35 | from adamant: new ≠ adamant; nature = new; mint 0 | new = `mild` |
| testMintNeverRepeatsCurrentAcrossUses | :46 | 6 uses from adamant, each ≠ previous | sequence `mild, serious, rash, adamant, hasty, lax` |
| testUseMintFromNilNatureSetsValid | :57 | nil → a valid nature, stored | `serious` (draw % 25) |
| testUseMintDoesNotAffectGrowthOrIdentity | :66 | shiny active, usedAtStage 5e7: usedAtStage 5e7, stage, species, shiny and used 1e9 all unchanged | — |
| testCannotUseMintOnEgg | :81 | egg with mint 2: canUse false, use nil, mint 2 | — |
| testCannotUseMintWithoutStock | :93 | mint 0: canUse false, use nil | — |
| testMintFeedbackSetAndConsumed | :101 | feedback nature = new, seq +1, consume → nil | Go: the reply carries `nature` (no seq) |
| testUseMintPersistsAcrossRestart | :113 | seed 3, mint 2; reopen: nature = new, mint 1 | new = `sassy` |
| testMintShopPriceAndPurchasable | :130 | mint price 100,000,000; purchasable contains rareCandy and mint | — |
| testBuyMintDebitsWalletAndCredits | :138 | used 3e8: canBuy, buy; mint 1, spent 1e8, wallet 2e8 | — |
| testCannotBuyMintBelowPrice | :147 | used 99,999,999: canBuy false, buy false, mint 0 | — |

Derived pins assume the store draws nothing from the game RNG before the Mint. Go `ensureProfiles` uses FNV seeds, not `s.rng` (`go:store.go:909-913`).

#### ShinyCharmTests.swift (9 logic tests)
| Test | Line | Expected (asserted) |
|---|---|---|
| testRollsShinyFlipsWithCharm | CharmT:32 | (48, charm) true; (48, no) false; (64, no) true; (64, charm) false; (96, charm) true; (96, no) false; 0 both true; 1 both false |
| testShinyNotificationShowsTheOddsTheHatchUsed | :50 | odds(false) 64, odds(true) 48; the store's odds follow ownership. The l10n half (body contains "48", not "64") moves to a JS/gettext test |
| testShinyDittoRevealDoesNotClaimFixedOdds | :64 | l10n only: the shiny Ditto reveal body has no "64" or "48" (JS) |
| testConstantsAndPassiveFlag | :71 | price 3e9; denominator 48; passive true only for the charm; sprite `shiny-charm` |
| testBuyDeductsAndOwns | :82 | used 5e9: listed, canBuy, buy; owned, count 1, spent 3e9, wallet 2e9 |
| testBuyOnceNoRepurchase | :95 | owned, used 10e9: canBuy false, buy false, spent unchanged, count 1 |
| testCanBuyNeedsEnoughTokens | :105 | used 2e9: canBuy false, buy false, not owned |
| testOwnsReflectsInventory | :112 | inventory decides ownership |
| testHatchWorksWithCharmOwned | :119 | charm, `hatch(1)`: active, species 1 |

Add one Go test that upstream lacks: a hatch with the charm at a seed whose first post-species draw is ≡ 0 mod 48 but not mod 64 is shiny, and the `hatch` event has `shinyOdds` 48. It proves the modulus is wired. Find the seed by search in the test.

#### RareCandyTests.swift, use part (17; grants and decode already ported)
| Test | Line | Setup | Expected (asserted) |
|---|---|---|---|
| testBatchCarriesXPThroughMultipleEvolutions | RCT:369 | linear3, hatch, +100M, 10 candies | maxUse 7; plan(3) = {count 3, evolves, not graduates, carry 25,000,000, discarded 0}; use(3) evolved; stage 2; usedAtStage 25M; candies 7; feedback 300M; `usedSinceInstall` unchanged |
| testBatchCapsAtGraduationAndPreservesUnusedCandies | :390 | noEvo; remaining ∈ {250M, 300M}: applyUsage(750M − rem); 5 candies | maxUse 3; plan(5).count 3, graduates, discarded = 300M − rem (50M / 0); use(5) graduated; candies 2; eggUsage 0; dex 1; then maxUse 0, use unavailable |
| testBatchCapsAtInventoryAndRejectsNonpositiveAmounts | :410 | linear3, 1 candy | counts 0, −1, MinInt: plan nil, use unavailable; candies 1; maxUse 1; plan(MaxInt).count 1, not evolves; use(MaxInt) progressed; usedAtStage 1e8; candies 0 |
| testBatchPreviewMatchesGrowthWithDifficultyAndRepeatBonus | :428 | d ∈ {0.1, 0.5, 1, 2} × boost; applyUsage(thr − 1); 20 candies | plan(1).graduates ⇒ result graduated and no active; else evolved, usedAtStage = plan.carry, plan(20) graduates, stock −= its count. Reload: candies, no active, dex count equal |
| testUseProgressesWithoutEvolution | :469 | linear3, 1 candy | progressed; usedAtStage 1e8; stage 0; candies 0; used unchanged |
| testUseEvolvesWhenCrossingThreshold | :484 | +50M, 1 candy | evolved; species 2; stage 1 |
| testSingleCandyAdvancesAtMostOneStage | :496 | +124M, 1 candy | stage 1 |
| testSingleCandyAdvancesAtMostOneBoostedStage | :505 | graduate 1→3 (125M, 250M, 375M), re-hatch (boost), +thr − 1 (62,499,999) | evolved; stage 1; usedAtStage 99,999,999 (< threshold 125M) |
| testUseGraduatesFinalStage | :526 | noEvo +700M, 1 candy | graduated; no active; dex 1 |
| testCandyGraduationFiresSpriteIdentityObservation | :541 | as above | Go/daemon: after `useCandy` the emitted `state` has `menuSubject.egg == true` with no `refresh` |
| testCannotUseOnEgg | :557 | egg, 2 candies | canUse false; unavailable (Go: `ErrNoActive`); candies 2 |
| testCannotUseWithoutStock | :567 | hatched, 0 candies | canUse false; unavailable (`ErrNoStock`) |
| testCannotUseWhileLineUnloaded | :577 | active from JSON, line fetch fails, 1 candy | line nil; canUse false; unavailable (`ErrNotReady`); candies 1 |
| testUseBumpsCandyFeedback | :592 | 1 candy | feedback amount 1e8 (Go: reply `xp` 1e8) |
| testOwnedItemsReflectsStock | :603 | 0 then 3 candies | empty, then [(rareCandy, 3)] |
| testSequentialCandyUseMatchesDemo | :612 | +100M, 3 candies | evolved (stage 1, carry 75M), progressed (stage 1), evolved (stage 2); candies 0 |
| testConsumeCandyFeedbackResets | :627 | UI one-shot contract; in Go it becomes a JS test that a re-mount does not replay "+XP" | — |

**Derived matrix for RCT:428** (the Go test should assert these exact values; the comparison logic above is the upstream assertion):

| d | boost | stage costs | plan(1) | after | plan(20) | stock after |
|---|---|---|---|---|---|---|
| 0.1 | no | 12.5M / 25M / 37.5M | graduates, discarded 37,499,999 | no active | — | 19 |
| 0.1 | yes | 6.25M / 12.5M / 18.75M | graduates, discarded 68,749,999 | no active | — | 19 |
| 0.5 | no | 62.5M / 125M / 187.5M | evolves, carry 99,999,999 | stage 1 | count 3, discarded 87,499,999 | 16 |
| 0.5 | yes | 31.25M / 62.5M / 93.75M | evolves (2 stages), carry 37,499,999 | stage 2 | count 1, discarded 43,749,999 | 18 |
| 1.0 | no | 125M / 250M / 375M | evolves, carry 99,999,999 | stage 1 | count 6, discarded 74,999,999 | 13 |
| 1.0 | yes | 62.5M / 125M / 187.5M | evolves, carry 99,999,999 | stage 1 | count 3, discarded 87,499,999 | 16 |
| 2.0 | no | 250M / 500M / 750M | evolves, carry 99,999,999 | stage 1 | count 12, discarded 49,999,999 | 7 |
| 2.0 | yes | 125M / 250M / 375M | evolves, carry 99,999,999 | stage 1 | count 6, discarded 74,999,999 | 13 |

`CandyNotificationCopyTests` (RCT:1020-1049) concern notification copy, so they belong to the JS l10n tests.

#### PremiumEggTests.swift (24)
Providers:
- **Tiered** = index `[(1,255),(2,100),(3,30),(4,3 legendary)]`; one-node lines whose rarity comes from `Rarity.from`.
- **RestOnly** = the index throws; every id is a base with cr 30 if `id % 3 == 0`, else 100 if even, else 255.
- **Lying** = index `[(7,30)]` but the line is common.

The egg store holds `eggUsage = 5,000,000` and a tier (PremT:86-94).

| Test | Line | Expected (asserted) | Derived pin |
|---|---|---|---|
| testCaptureRateCeilingIsTheSameThresholdAsClassification | PremT:100 | for every cr 0…255 and tier in {common, uncommon, rare}: includes == (from(cr) ≥ tier) | — |
| testLegendaryHasNoCaptureRateCeilingButPassesLowerTierFilters | :113 | legendary has no ceiling, includes(3) false; cr 3/30/45 pass rare and uncommon; no legendary tier on sale | — |
| testPricesFollowGraduationTotalRatio | :126 | 1e9 / 2.5e9 / 4e9; tiers [nil, uncommon, rare] | — |
| testRareEggIsNotDominatedAtMeasuredPoolComposition | :148 | weights c 37,240 / u 3,135 / r 3,053 / L 339: rarePrice < uncPrice / share(0.5906); share < 0.625; boosted ×0.70 share < 0.625 | pure arithmetic; port as a constants test |
| testBuyPremiumEggRecordsGuaranteeAndDebitsTierPrice | :176 | active, used 20e9: canBuyEgg(rare), buy; tier rare; guarantee rare; spent 4e9; no active; eggUsage 0; dex 1 released; collectedFinals empty | — |
| testCannotBuyAnyEggWhileIncubating | :192 | egg store: all tiers false, spent 0 | — |
| testFundsAreCheckedAgainstTierPrice | :203 | used 3e9: nil ✓, uncommon ✓, rare ✗; buy(rare) false; active kept; spent 0 | — |
| testPurchaseStartsFromCleanRollState | :219 | pending nil before; after buy(rare) tier rare, pending nil, eggUsage 0 | — |
| testGuaranteeSurvivesRestart | :229 | buy(uncommon); reopen: tier uncommon; guarantee uncommon | — |
| testWeightedRollNeverGoesBelowGuarantee | :242 | seeds 1…20, rare egg: 20 hatches, all ≥ rare; control (no tier) contains common | rare egg: all `rare` (3) for seeds 1–20 |
| testUncommonEggHatchesUncommonAndExcludesOnlyCommon | :267 | seeds 1…20: every hatch ≠ common, at least one uncommon | u,r,u,r,u,u,r,u,u,u,r,r,r,u,u,u,u,u,u,u |
| testLegendaryStillReachableFromPremiumEgg | :281 | rare egg, seeds 1…60: some legendary | first at seed 42 |
| testRestFallbackRespectsGuarantee | :293 | RestOnly, rare, seeds 1…10: every hatch rare, at least one | picks 111, 555, 225, 99, 600, 552, 399, 54, 81, 606 |
| testRestFallbackHonoursUncommonTierWithoutOverFiltering | :307 | RestOnly, uncommon, seeds 1…20: none common, some uncommon | — |
| testRestFallbackWithoutGuaranteeCanHatchCommon | :320 | RestOnly, no tier, seeds 1…20: some common | seed 3 → id 225 is rare; seed 1 → 340 uncommon; seed 11 → 329 common |
| testHatchDiscardsSpeciesBelowGuaranteeWhenIndexIsStale | :334 | Lying, rare: no active; pending nil; tier rare; eggUsage 5,000,000 | — |
| testSameSpeciesHatchesNormallyWithoutGuarantee | :344 | Lying, no tier: active rarity common | — |
| testHatchConsumesGuarantee | :352 | rare, seed 1: active; tier nil; guarantee nil | — |
| testGuaranteeDoesNotSurviveIntoTheNextEgg | :362 | after the hatch, applyUsage(2T): graduated; dex 1; tier nil | — |
| testGuaranteeTravelsWithSave | :377 | rebase on import keeps the tier (import is LATER; port when it lands) | — |
| testSanitizedDropsGuaranteeAndItsPreRollWhenActiveExists | :390 | **already covered** by `go:save_test.go` `TestExtremeValuesAreClampedOnLoad`. The egg half (tier and pending kept without an active) still needs one assertion | — |
| testUnsatisfiableGuaranteeIsNormalizedAwayAndEggStillHatches | :413 | `eggTier: "legendary"` loads as nil, then hatches | — |
| testCannotBuyTierThatIsNotSold | :422 | legendary not on sale; canBuyEgg(legendary) false; buy false; spent 0; active kept; canBuyEgg(common) false | — |
| testUnknownGuaranteeDecodesAsNoGuarantee | :433 | `"eggTier":"mythic"` → nil | — |

The derived pins use one draw `% Σ` over the filtered weights (rare 33, uncommon 133, none 388) and `id = draw % 649 + 1` for REST. Swift's RestOnly provider accepts id 132, while Go skips 132 before fetching; no seed in 1…20 draws 132, so the pins agree.

#### FreshEggTests.swift (7)
The store has active base 10 (common, k = 3, usedAtStage 200M), dex `[{1→3}]`, collectedFinals `["1:3"]`, used 5e9, seed 7, and a line provider that throws.

| Test | Line | Expected (asserted) |
|---|---|---|
| testPriceIsOneBillion | FreshT:30 | 1,000,000,000 |
| testBuyFreshEggReleasesIntoDexWithoutProbabilityImpact | :35 | dexEntries before = dex + 1; buy true; no active; egg; eggUsage 0; pending nil; dex +1; last baseID 10, released, chain [10]; dexEntries count unchanged; collectedFinals unchanged; spent 1e9; wallet 4e9 |
| testReleasedSpeciesStaysInTheDex | :67 | species 10 in the dex before and after; not raising after |
| testDiscardedSpeciesNotCollected | :78 | no `10:*` in collectedFinals |
| testCannotRerollWhenEgg | :86 | no active: canBuy false, buy false, spent 0 |
| testCannotRerollWithoutFunds | :95 | used 5e8: false; active kept; spent 0 |
| testShinyCanBeRerolled | :104 | shiny active: buy true; no active; currentIsShiny false |

#### Release tests elsewhere (6)
| Test | Line | Expected (asserted) |
|---|---|---|
| testFreshEggKeepsRepresentativeSelectionOfReleasedSpecies | CompT:590 | representative 1 before and after buyEgg; menu subject species 1 |
| testReleasingMidChainCreditsOnlyReachedForms | CompT:744 | path [1,2] of plan [1,2,3], stage 1: dex species [1,2] before and after; released chain [1,2], finalID 2; species 3 absent |
| testReleasingDisguisedDittoKeepsShinyHidden | CompT:765 | disguised shiny → released isShiny false |
| testBuyEggStartsWithoutPrayers | EggPrayerTests:110 | after buy, eggPrayers nil; pray → 1 (pray is LATER; assert the nil) |
| testReleasedEntryKeepsThePrayersOfTheIndividual | EggPrayerTests:118 | active prayers 4 → released entry prayers 4 |
| testReleasingActiveLetterPreservesFormProfileAndRepresentative | UnownTests:429 | Unown B active, dex A shiny, representative 201/B, used 1e9: after buy, representative 201/B kept, not shiny subject; dex 2; forms [A, B]; released form B, same profile; collectedFinals empty |

#### Shop half of DifficultyTests and DifficultySaveTests (8)
| Test | Line | Expected (asserted) |
|---|---|---|
| testShopGateAndChargedAmountFollowDifficulty | DiffT:145 | used 250M: at 1.0 canBuy(candy) false; at 0.5 true, buy, spent 250,000,000, candy 1 |
| testEggPriceScalesAndIsActuallyCharged | DiffT:158 | shop 0.5, used 1e9: egg price 500,000,000; buyEgg(nil); spent 500,000,000 |
| testGrowthSliderDoesNotMoveShopPricesAndViceVersa | DiffT:167 | growth 0.1: candy 500M, rare egg 4e9; shop 0.1: tokensToNext = 125,000,000 |
| testGradedEggRatioSurvivesScaling | DiffT:179 | shop 0.4: uncommon/plain = 2.5, rare/plain = 4.0 (±0.001); derived 400M / 1e9 / 1.6e9 |
| testShopPriceChangesLiveWithoutRestart | DiffT:203 | 500M, then setShop(0.5) gives 250M |
| testDifficultyPersistsToDefaults | DiffT:210 | set shop 1.7 → 1.7 (Go: `settings-applied.json` round trip) |
| testNothingCollapsesToZeroAtMinimumDifficulty | DiffT:242 | at 0.1 every shop entry price > 0 (smallest is 10,000,000) |
| testNewRangeClampsBothControls | DSaveT:127 | setShop(10) → 2; setShop(0.01) → 0.1 (plus the load-time clamp of 20 → 2, DSaveT:29/:44) |

`testDraftDoesNotApplyUntilSaveAndSavesBothControls` (DSaveT:34) is the prefs draft behaviour, so it becomes a JS `prefsModel` test.

#### PreciousCompanionGuardTests.swift (8) → the `isHighValue` helper, or a JS view-model test
| Test | Line | Expected |
|---|---|---|
| testHighValueCompanionIdentifiesLegendaryNonShiny | GuardT:31 | true |
| testHighValueCompanionIdentifiesShinyNonLegendary | :38 | true |
| testHighValueCompanionIdentifiesShinyLegendary | :45 | true |
| testHighValueCompanionExcludesCommonNonShiny | :52 | false |
| testHighValueCompanionExcludesUncommonNonShiny | :57 | false |
| testHighValueCompanionExcludesRareNonShinyToAvoidRerollFatigue | :62 | false |
| testHighValueCompanionIsFalseWhenNoActive | :69 | false |
| testDisguisedDittoDoesNotLeakHighValueStatusBeforeReveal | :75 | disguised shiny: shiny false, common, false; revealed rare shiny: true |

The extension can compute it from `active.isShiny || active.rarity == "legendary"`; `isShiny` is already `displaysShiny`.

#### Other
- `ProfileGrowthIntegrationTests:150` testRareCandyFinalizesGraduationProfile: growth 0.1, 1 candy, details for species 3 → no active; the last dex profile is level 100 with moves `["final-3"]`.

#### Deferred with the Ditto reveal (not ported in Go; `DittoDisguiseEnabled` is off)
- `DittoT:339` testCandyPickerMatchesApparentPokemonButOnlySpendsEnoughToReveal:
  - At usedAtStage 100M with 5 candies, the disguised plans equal a normal Pokémon's for counts 1–5.
  - use(5) consumes 1 (4 left, feedback 1e8), and the next use is unavailable.
  - After the reveal: species 132, usedAtStage 75M, maxUse 4. use(2) progressed, usedAtStage 275M, candies 2.
- `DittoT:376` testApparentGraduationBatchPreservesUnusedCandiesAtDittoReveal, for d ∈ {0.1, 1, 2} × boost with 20 candies:
  - the preview graduates;
  - consumed = ceil(revealThr / 1e8);
  - after the reveal usedAtStage = consumed·1e8 − revealThr, feedback = consumed·1e8, candies 20 − consumed, dex empty.
- `DittoT:405` testCandyBatchStopsExactlyAtRevealWithoutOverflow: at usedAtStage 25M, use(5) → after the reveal usedAtStage 0 and candies 4.
- Port the **guards** now anyway: `candyStageCosts` empty while a reveal is pending, and the consumed cap for disguised Pokémon. They are cheap, and they keep an imported disguised save from wasting candies.

**Count:** 23 + 11 + 9 + 17 + 24 + 7 + 6 + 8 + 8 + 1 = **114 upstream tests in scope**.
- About 104 are Go tests. The rest are JS or l10n: CharmT:64 and the l10n halves, RCT:627, DSaveT:34, and the 3 CandyNotificationCopy tests (not counted above).
- 3 Ditto tests are deferred.
- 1 import test waits for save transfer.

---

## 6. Open questions, ambiguities and upstream bugs
1. **`ui.md` §9 is wrong about the Bag.** There are no evolve or graduate controls (§2.4). Fix the line when this spec lands.
2. **Mint "cosmetic" contradiction.** Upstream calls it pure cosmetic (`CS:1257-1258`), yet the displayed actual stats use the nature modifier (`PP:236`). Keep upstream behaviour (the stats change), and make the item description honest if desired. This is a product decision.
   **Resolved (2026-11-19):** Mint changes the displayed stats, as upstream does.
3. **Misleading disabled reason in the Bag.** With a Pokémon present but the line not loaded, or a Ditto reveal pending, the candy card says "No Pokémon to use it on" (`BV:159-161`).
   - Proposed: a new string such as "Available once data loads", driven by `bag.candyBlocked`. That deviates from upstream, so decide.
   **Resolved (2026-11-19):** a `loading` disabled reason for candy use: `bag.candyBlocked == "lineLoading"` (protocol §5.4). The Bag shows a new string such as "Available once data loads".
4. **The Bag stepper shows at `maxUse == 1`** (range 1…1), while the Shop hides it unless `maxBuyCount > 1` (`BV:68`, `SV:65`). This is a cosmetic inconsistency; mirroring the Shop is suggested.
5. **The Bag always switches to Home after "Use", even on failure** (`BV:165-169`). Keep it, or switch only on `ok` (the reply makes that easy).
6. **`inventory` is never sanitized,** upstream or in Go.
   - A negative count disables use but is still "added to" by a buy. A huge count overflows on `+= count`: Swift traps, Go wraps.
   - Proposed: clamp to [0, 1e15] in `sanitize`. That is a deviation, so decide.
   **Resolved (2026-11-19):** no inventory clamp.
7. **Released entries have no names if the line is not loaded** (`CS:370-373`). They rely on the dex name backfill. Go must set `NamesVersion` only when every chain species is named, as `go:store.go:398-403` does for graduation.
8. **`displayName` is "Token Egg" when the line is not loaded** (`CS:258-261`). That literal is not localized, and it appears in `eggConfirm` and `useOnCurrent`. The port should use `active.displayName`, which is `#<id>` without names.
9. **Possible Swift trap:** `rareCandyStageCosts` builds `stageIndex..<totalForms` (`CS:1183`), which traps when `stageIndex > totalForms`. Sanitize does not enforce `totalForms ≥ stageIndex + 1` (`ST:174-178`). Line normalization probably repairs this first, which I did not verify. A Go loop simply yields no costs.
10. **The preview can differ from the outcome.** The plan uses the stored `totalForms` and assumes the last cost is the leaf. If `applyUsage` repairs an invalid plan mid-batch (`CS:1029-1035`), the outcome can differ. This is rare.
11. **The Charm applies to an egg already incubating** (it is read at hatch time). This is undocumented upstream but follows from the code; keep it.
12. **REST fallback and id 132:** Go skips 132 before fetching, but Swift test providers can return it. There is no divergence in the ported seeds.
13. **Sort stability:** Swift `sorted` is not documented as stable. There are no ties at any difficulty, so this is moot. Use `sort.SliceStable` in Go anyway.
14. **Settings Save button:** one shared Save upstream (`DSS:48-55`) versus per-row Save in the current prefs. Decision needed (§3.4).
   **Resolved (2026-11-19):** prefs apply immediately, with no draft and no Save. The new shop-prices row writes `shop-difficulty` on change; the extension debounces the forward (300 ms, protocol §2.2).
15. **`setSettings` "every field required" versus a new optional `shopDifficulty`** (§4.5). Amend protocol §9.2, or bump the protocol.
   **Resolved (2026-11-19):** protocol additions are optional and additive: §9.2 is amended, no bump (still protocol 1).
16. **Where `shopDifficulty` lives in Go:** in `settings-applied.json` only (proposed, as upstream keeps it out of the save) or also in the save. The proposal is to keep it out of the save, unlike `growthDifficulty`, which needs its last-applied value for the rescale.
    **Resolved (2026-11-19):** `shopDifficulty` is persisted in `settings-applied.json` only, not in the save.
17. **The Ditto reveal is not ported,** so a disguised Pokémon imported from macOS stops at its first threshold. The candy guards keep candies safe, but the companion stays stuck. This is the existing M1 decision (companion.md §4.5).
18. **Egg purchase while offline:** upstream allows it (the tier is persisted and the species is rolled later). Go must make sure `BuyEgg` never waits on the network (§5.2, item 5).
19. **`canBuyEgg` ignores `isHatchRetryDelayed`.** That flag only matters while incubating, and purchases are impossible then, so this is fine.
