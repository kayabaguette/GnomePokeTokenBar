# PokeTokenBar companion game: porting spec for the Go helper (M1 focus)

I could not create `/tmp/claude-1000/-home-user-Documents-01-Projects-PokeTokenBar/896ff0d3-0052-4f31-aa96-72c3ce053637/scratchpad/spec-companion.md` because this agent is read-only. The full spec is below; please save it yourself.

Conventions:
- Paths are relative to `/home/user/Documents/01_Projects/PokeTokenBar/`.
- `CM` = `Sources/PokeTokenBar/Core/CompanionModel.swift`
- `CS` = `Sources/PokeTokenBar/Core/CompanionStore.swift`
- `PF` = `Core/PokemonForm.swift`, `PP` = `Core/PokemonProfile.swift`, `API` = `Core/PokeAPIClient.swift`
- `ST` = `Core/SaveTransfer.swift`, `SS` = `Core/SaveSnapshots.swift`, `US` = `Core/UsageStore.swift`, `APP` = `Sources/PokeTokenBar/PokeTokenBarApp.swift`
- `SL` = `UI/SpriteLoader.swift`, `SA` = `UI/SpriteAnimation.swift`
- Repo HEAD is 38e3e55 (v2.5.7).

---

## 1. State model

### 1.1 Files and locations (macOS)
- **State directory:** `~/Library/Application Support/PokeTokenBar/`. The `PTB_STATE_DIR` environment variable overrides it (`Core/AppStatePaths.swift:5-18`).
- **Main save:** `companion-state.json` (`CS:110-115`).
  - Written atomically on every mutation through `save()` (`CS:2306-2311`).
  - `save()` also calls `refreshRepresentativeSubject()` and `checkDexCompletions()` before encoding.
- **Corrupt backup:** `companion-state.json.corrupt`, which replaces any previous one (`CS:2282-2289`).
- **One-off pre-profile migration backup:** `companion-state.pre-profiles-v1.json`, never overwritten (`CS:2237-2243`).
- **Import backups:** `companion-state.pre-import-yyyy-MM-dd-HHmmss.json`. The newest 5 are kept (`ST:83-88`, `CS:2055-2077`).
- **Snapshots:** `.snapshots/companion-state.json/companion-snapshot-yyyy-MM-dd-HHmmss[-N].json`. At most 10 are kept (`SS:16-18`, `SS:29-35`, `SS:134-174`).
  - Stamps use a DateFormatter with no explicit time zone, so local time (`SS:42-47`, `ST:90-97`).
- **Caches (not part of the save):**
  - `base-index.json`. The path is hard-coded to Application Support and ignores `PTB_STATE_DIR` (`API:126-131`).
  - `pokemon-details-v1/<id>.json` (`API:34-38`).
  - `pokemon-names-v1/<kind>-<name>.json` (`Core/PokemonNameLocalization.swift:70`).
  - `sprites/` (hard-coded path, `SL:19-21`, `SL:119-122`).
- **Settings outside the save (UserDefaults):**
  - `growthDifficulty`, `shopDifficulty` (`CS:89-102`).
  - `companionNotifications`, default true (`CS:1548`).
  - `alwaysFacesBack` (`US:183-184`), `animationQuality` (`US:154-155`, default `.powerSaver` at `US:975`).
  - `claudeAccountCandyArmed` (`US:851`), `refreshInterval`, default 120 s (`US:951`).

### 1.2 JSON shape of `companion-state.json`
The encoder is Swift's default `JSONEncoder()` (`CS:2309`):
- Optionals that are nil are omitted.
- `Set` encodes as an array.
- `[Int: X]` encodes as an object with string keys.
- **`Date` encodes as a Double: seconds since 2001-01-01T00:00:00Z** (Apple reference date). Add 978307200 to get Unix seconds.
- Envelopes (export and snapshots) use ISO-8601 dates instead, with pretty-printed, sorted keys (`ST:106-110`).

**`CompanionState`** (`CM:652-704`):

| key | type | meaning |
|---|---|---|
| `installBaselineSet` | bool | first real usage data has seeded the ledger |
| `usedSinceInstall` | int | lifetime credited tokens (growth meter and shop wallet) |
| `spentTokens` | int | shop spend ledger; wallet = used − spent (`CS:1277`) |
| `eggUsage` | int | tokens credited to the current egg |
| `eggTier`? | "uncommon"\|"rare" | premium-egg guarantee (Later) |
| `pendingHatchID`? | int | pre-rolled species of the current egg |
| `pendingForm`? | string | pre-rolled form (legacy key `pendingUnownForm`) |
| `eggPrayers`? | {count:int} | cosmetic prayer counter |
| `claimedTodayTokensByProvider`? | {providerID:int} | per-provider "already credited today" ledger. nil means a legacy save that has not been seeded yet |
| `lastDate` | "yyyy-MM-dd" | date of that ledger |
| `active`? | MonState | the Pokémon being raised; absent means an egg |
| `representativeSpeciesID`? | int | pinned representative; nil follows the active Pokémon |
| `representativeForm`? | string | legacy key `representativeUnownForm` |
| `dex` | [DexEntry] | graduated and released individuals (append-only) |
| `collectedFinals` | [string "base:final"] | graduations only |
| `language` | ko,en,ja,es,fr,pt,de,ru | `CM:10-57` |
| `inventory` | {itemRaw:int} | `rareCandy`, `mint`, `shinyCharm` |
| `candyGrantTier` | {windowKey:int} | candy grant edge state |
| `candyWindowEpoch` | {windowKey:string} | last seen reset epoch per window |
| `candyFeatureSeeded` | bool | first-run candy seeding done |
| `notifiedDexCompletions` | [string] | badge keys already announced |
| `dexCompletionsSeeded` | bool | |

**`MonState`** (`CM:463-519`):
- `baseID`, `pathIDs` (realized path), `plannedPathIDs` (full route chosen at hatch), `stageIndex`, `usedAtStage`.
- `rarity`, `totalForms` (= length of the planned path), `isShiny`, `nature`?, `form`? (legacy key `unownForm`), `profile`?.
- `hasGrowthBoost`, `dittoDisguise`? (int), `dittoRevealed`, `prayers`?.
- Derived fields:
  - `currentID = pathIDs[min(stageIndex, count−1)]`, falling back to `baseID` (`CM:486`).
  - `displaysShiny = isShiny && (dittoDisguise == nil || dittoRevealed)` (`CM:484`).

**`DexEntry`** (`CM:553-607`):
- `id` (= `profile.instanceID` or a UUID), `baseID`, `finalID`, `chainOrder` (reached path), `rarity`.
- `caughtAt`? (Date), `isShiny`, `nature`?, `form`?, `profile`?, `prayers`?.
- `names`? (`{"<speciesID>":{"<lang>":"name"}}`), `namesVersion`? (current = 1), `releasedAt`? (non-nil means released, not graduated).

**`PokemonProfile`** (`PP:92-103`):
- `instanceID`, `seed` (UInt64; may exceed 2^53, so do not pass it through a JS number), `gender`? (male/female/genderless).
- `ivs` {hp, attack, defense, specialAttack, specialDefense, speed}.
- `abilitySlot`?, `abilityName`?, `abilityIsHidden`, `level`, `growthTokens`, `moves` [{name, learnedAtLevel}].

**Envelope** (`ST:10-21`):
```
{format:"poketokenbar.save", schema:2, appVersion, exportedAt, sourceDevice, state}
```

### 1.3 Decode robustness, sanitization and recovery
- **Lenient decoding.** Each top-level field falls back to its default on a type mismatch (`CM:640-649`, `CM:711-748`).
  - Dex entries decode one by one (`Lossy`, `CM:635-638`, `CM:739`).
  - An `active` with empty `pathIDs` throws, and the state falls back to an egg while keeping the dex and inventory (`CM:526-529`, `CM:734`).
  - A corrupt `profile` or `prayers` decodes as nil (`CM:543`, `CM:547`, `CM:621-622`).
  - `stageIndex` is clamped into the bounds of `pathIDs` (`CM:535-536`).
  - Forms are normalised against the line, so an unknown form becomes the default (`PF:115-127`, `PF:194-202`).
- **`sanitized()`** runs on load, import and snapshot restore (`ST:143-193`):
  - Token values are clamped to [0, 1e15] (`ST:74`).
  - An `eggTier`, `pendingHatchID` or `eggPrayers` that coexists with an `active` is dropped.
  - An unsatisfiable tier (legendary) is dropped.
  - `totalForms` is clamped to 1…12. `stageIndex` is clamped.
  - Duplicate species IDs are removed from `pathIDs`, `plannedPathIDs` and `chainOrder`, keeping the current species.
  - Profiles are sanitised: level 5…100, IVs 0…31, at most 4 moves (`PP:190-203`).
  - The representative is reconciled.
- **Load** (`CS:2278-2305`):
  - No file means a fresh install.
  - A decode failure moves the file to `.corrupt`, then tries the newest valid snapshot (sanitised and saved). If none exists, it starts fresh without saving.
- **Automatic snapshots** (`CS:2104-2117`):
  - Taken at the end of every `update()` tick when the newest snapshot is at least 12 h old.
  - Only when the state is non-trivial: `usedSinceInstall>0 || dex non-empty || active`.
- **Restore** (`CS:2119-2136`):
  - Load and validate the chosen snapshot first, then write a safety snapshot, then `applySave`.
  - `applySave` writes a pre-import backup and aborts if that write fails (`CS:2020-2049`).

### 1.4 Migrations (do not port; listed so you know they existed)
1. App Support folder rename TokenMac → PokeTokenBar (`APP:556-563`).
2. Legacy aggregate key `claimedTodayTokens` is ignored. The nil per-provider ledger is seeded on the first valid observation, without crediting past usage (`CM:723-731`, `CS:899-904`).
3. Unown-only form keys `unownForm`, `pendingUnownForm` and `representativeUnownForm` are read as generic forms (`PF:192-202`, `CM:548`, `CM:720-721`, `CM:736-737`).
4. Pre-profile saves get deterministic profiles (FNV-1a seeds) and a backup file (`CS:2201-2246`, `PP:289-296`).
5. Dex names: a legacy single map decodes as nil, and `namesVersion` 1 triggers a backfill (`CM:554-577`, `CM:623-626`, `CS:802-861`).
6. `releasedAt == nil` means graduated, so no migration is needed (`CM:578-585`).
7. The old difficulty range 0.0001–20 is rescaled to the new range, keeping banked progress (`CS:89-100`).
8. Invalid or legacy `plannedPathIDs` are repaired on line load (`CS:1102-1121`, `CM:530-534`).
9. First-run seeding of dex badges and candy windows: existing state is recorded without notifications (`CS:531-541`, `CS:1495-1506`).
10. Envelope schema 1 → 2 added profiles (`ST:12-13`).
11. Saved Codex `candyWindowEpoch` values are no longer used (`docs/reference/defect-log.md:1231-1233`).

---

## 2. Growth economics

### 2.1 Token input
- **Input:** a map `providerID → today's total tokens`, built only from snapshots whose `today.date == todayKey()` (`US:337-343`).
  - `todayKey()` is the local-time-zone date `yyyy-MM-dd` (`Core/LocalUsageReader.swift:2182-2190`).
  - **Total = input + output + cacheWrite + cacheRead** (`LocalUsageReader.swift:49`, `:56`; `Models.swift` `DailyUsage` fallback sum).
  - The balance is calibrated for about 253M tokens/day with cache tokens included (`CM:104`). A Go reader that leaves out cache tokens slows growth dramatically.
- Only enabled providers produce snapshots (`US:258`).
- `hasUsageData = !snapshots.isEmpty` (`US:346`).
- `update()` runs on every UsageStore change and after every refresh (`APP:272-289`). The default poll is 120 s.

### 2.2 Per-provider ledger: dedupe across restarts and days (`CS:867-964`)
- `hasCurrentProviderData = hasUsageData && !map.isEmpty` (`CS:874`). When it is false, the ledger and date are never touched (`CS:897`).
- **Install baseline.** The first valid observation sets `installBaselineSet`, ledger = map and `lastDate`, and credits nothing (`CS:877-892`). Before that, an imported `active` still shows and its line keeps loading (`CS:880-887`).
- **Legacy ledger (nil):** seed only (`CS:899-904`).
- **Date changed** (`CS:905-931`):
  - New ledger = every known provider set to 0, overlaid with the current map.
  - Credit = sum of the current map, i.e. the whole of the new day.
  - Consequence: usage between the last refresh before midnight and midnight is never credited.
- **Same day**, per provider (`CS:932-962`):
  - Unknown provider: seed it, no credit.
  - `current < previous`: rebase that provider only, no credit.
  - Otherwise credit `current − previous`.
- **Applying credit (`delta > 0`):**
  - `usedSinceInstall += delta`.
  - Egg: `eggUsage += delta`. Active Pokémon: `applyUsage(delta)`.

### 2.3 Thresholds (`CM:107-131`)
- **Egg hatch:** `eggHatchThreshold = 5_000_000` (`CM:111`). Effective value = `round(5M × difficulty)` (`CS:163-165`).
- **Graduation total T per rarity** (`CM:114-121`):

  | rarity | T |
  |---|---|
  | common | 750,000,000 |
  | uncommon | 1,875,000,000 |
  | rare | 3,000,000,000 |
  | legendary | 6,000,000,000 |

- **Phase cost** for a line with k forms, phase i (1-based), boost multiplier m (`CM:123-131`):
  ```
  std  = round(T·i / (k(k+1)/2))
  base = max(1, round(std / m))
  ```
  - m = 2 when `hasGrowthBoost` (`CM:112`, `CM:487-493`).
  - The effective threshold is `round(base × clamp(difficulty))` (`CS:169-171`, `CM:150-152`).
  - Rounding is half away from zero (Swift `.rounded()`), which matches Go `math.Round`.
- **Phase count.** k = `totalForms` = length of the chosen plan, not tree depth (`CS:1800-1802`). There are k phases: k−1 evolutions plus one final phase that graduates. Graduation happens when the current tree node has no children (`CS:1020-1021`).
- **Default-difficulty table:**
  - common: k=1: 750M. k=2: 250M, 500M. k=3: 125M, 250M, 375M.
  - uncommon: k=1: 1.875B. k=2: 625M, 1.25B. k=3: 312.5M, 625M, 937.5M.
  - rare: k=1: 3B. k=2: 1B, 2B. k=3: 500M, 1B, 1.5B.
  - legendary: k=1: 6B. k=2: 2B, 4B. k=3: 1B, 2B, 3B.
  - The boost halves each value; for example common k=3 phase 1 becomes 62,500,000.
- **`applyUsage` loop** (`CS:1002-1059`):
  - Add to `usedAtStage`, then loop at most 50 times while `usedAtStage ≥ threshold`. Each evolution carries the excess over (`CS:1039`).
  - **Graduation discards the excess** (`graduate()` sets `eggUsage = 0`, `CS:1147`).
  - With no line loaded, usage is banked but nothing evolves until the line loads; `loadCurrentLine` then calls `applyUsage(0)` (`CS:1006`, `CS:1894`).
- **Hatch overflow carries:** `eggUsage − eggThreshold` goes straight into `applyUsage` and can evolve immediately (`CS:1775`, `CS:1818`).
- **Repeat line ("2× faster")** (`CS:1795`, `CM:763-765`):
  - `hasGrowthBoost = collectedFinals` contains any `"<baseID>:*"` at hatch time. It is decided by the base, not the planned final.
  - It is persisted per individual.
  - On a Ditto reveal it is recomputed for base 132 (`CS:1857`).

### 2.4 Difficulty
- **Range and clamp:** 0.1…2.0, default 1.0. Non-finite values become 1.0 (`CM:136-145`).
- **Two independent multipliers.**
  - Growth difficulty scales the egg and stage thresholds.
  - Shop difficulty scales prices (`CS:71-73`, `CS:174-181`).
  - Candy XP is not scaled (`CS:70`).
- **Changing growth difficulty keeps progress** (`CS:126-152`):
  - Only the current egg's `eggUsage`, or the current stage's `usedAtStage`, is rescaled.
  - `new = floor(credits / oldThr × newThr)`, clamped to [0, 1e15].
  - If the stage was incomplete, `min(newThr − 1, new)`, so a settings change never evolves, hatches or graduates.
  - `usedSinceInstall`, the ledger and the wallet are untouched.
- **Slider (UI):** position p maps to `0.1·20^p`, snapped to 2 significant digits, and snaps to 1.0 within ±0.01 of its position (`CM:159-178`).

---

## 3. Hatch roll

### 3.1 Candidate pool (fetched, not bundled)
- **Query** (`API:210-227`): PokéAPI GraphQL, `evolves_from_species_id IS NULL AND id ≤ 649 AND id ≠ 132` (Ditto), ordered by id, selecting `{id, capture_rate}`.
- **Size:** the comment says 329 bases (`CS:1906`). A measured pool, Ditto excluded, gives 328 (`Tests/PokeTokenBarTests/PremiumEggTests.swift:146-149`). Babies such as Pichu and Munchlax are bases.
- **Cache:** memory, then disk (30-day TTL), then a fetch, then a stale disk copy. With nothing available it throws and the egg is kept (`API:142-172`).
- **REST self-heal (once per session):** scan ids 1…649 in batches of 6 and persist only if at least 150 bases were found (`API:177-208`).

### 3.2 Weighted pick (`CS:1912-1937`)
- weight = `capture_rate`, or `max(1, cr/2)` (integer division) if the base was already collected (`Core/CollectionWeight.swift:2-5`).
- A guaranteed egg tier filters the pool first by `tier.includes(captureRate)`. An empty pool means no hatch and a retry on the next tick.
- Exactly one roll: `r = rng.next() % total`, then walk the cumulative weights.
- The legendary share is about 0.77% of the measured pool weight (339 / 43,767, `PremiumEggTests.swift:149`, `CS:1908`).
- **REST fallback** when the index is unavailable (`CS:1942-1963`):
  - Up to 16 tries of a uniform id in 1…649. Accept it if it is a base (`evolves_from_species == nil`, not 132) and passes the tier filter.
  - Unweighted. A network error returns nil.

### 3.3 Rarity tiers (`CM:83-101`, `API:107-109`)
- legendary if `is_legendary || is_mythical`; else rare if cr ≤ 45; else uncommon if cr ≤ 120; else common.
- Computed from the **base** species of the line. Every species in the line, and its Pokédex cell, carries the line's rarity.

### 3.4 Per-individual rolls and RNG order
- **Production RNG:** `SystemRandomNumberGenerator` (`CS:79`). Every draw is `rng.next() % n`, with modulo bias.
- **Test RNG:** SplitMix64 (`Tests/PokeTokenBarTests/CompanionTests.swift:63-73`). Use it in Go for test parity.
- **Draw order:**
  1. **Species** (`CS:1927`), unless a prefetched `pendingHatchID` exists. Prefetch also rolls the **form** right after the species (`CS:1644-1645`).
  2. **Shiny:** `next % 64 == 0`, or `% 48` with the Shiny Charm (`CS:1726-1736`, `CS:1783`, `CM:449`, `CM:249`).
  3. **Nature:** `allCases[next % 25]` in the declaration order hardy, lonely, brave, adamant, naughty, bold, docile, relaxed, impish, lax, timid, hasty, serious, jolly, naive, modest, mild, quiet, bashful, rash, calm, gentle, sassy, careful, quirky (`CM:399-404`, `CS:1784`).
  4. **Ditto disguise roll** (`CS:1788-1791`, `CS:1721-1723`, `CM:451`):
     - Drawn only when `dittoDisguiseRollingEnabled`, which is true only in the bundled app (`CS:80`).
     - Hit when common, tree depth ≥ 2 and `next % 128 == 0`.
  5. **Evolution plan:** one draw per non-leaf node of the chosen route, **even when the node has a single child** (`CS:1061-1078`, `CS:1792`).
  6. **Profile seed:** `PokemonProfile.generate(seed: next)` (`CS:1793`).
  7. **Form:** only if none was prepared and the line has forms (`CS:1796`, `CS:1681-1685`).
- **Profile generation** (`PP:105-126`, `PP:277-287`):
  - `ProfileRNG` is SplitMix64; seed 0 becomes `0x9E3779B97F4A7C15`.
  - IVs are 6 draws `% 32` in the order hp, atk, def, spa, spd, spe.
  - level = 5, growthTokens = 0, gender, ability and moves unset.
- **Enrichment** when PokéAPI details load (`PP:142-176`). The RNG is `ProfileRNG(seed ^ 0xA11B_1E5D_9EED)`.
  - **Gender**, only if nil: rate < 0 gives genderless; otherwise `next % 8 < gender_rate` gives female, else male.
  - **Ability**, only if `abilitySlot` is nil:
    - If hidden abilities exist and `next % 128 == 0`: hidden[`next % count`].
    - Otherwise, if normal abilities exist: normal[`next % count`].
    - Otherwise the lowest slot.
    - The name is then resolved through the fallback chain at `PP:167-172`.
  - **Moves:** the last 4 of `levelUpMoves(through: level)`, sorted by (level, name), from version group `black-2-white-2` (`PP:21-37`, `PP:175`, `API:86-96`).
  - Enrichment never re-rolls fields already set.
- **Level** (`PP:183-188`): `level = max(old, 5 + floor(min(1, growthTokens/T) × 95))`, capped at 100. Growth is monotonic.
- **Growth tokens** (`CS:2250-2275`) are in standard-balance units:
  - `growthTokens = Σ std phases completed + floor(std current phase × min(1, usedAtStage/effectiveThreshold))`, capped at T.
  - Recomputed on every `applyUsage`. Graduation forces T, i.e. level 100 (`CS:1125`).
- **Stats (detail view):** HP = `((2b+iv)·L)/100 + L + 10`, with Shedinja (#292) fixed at 1. Others = `floor((((2b+iv)·L)/100 + 5) × nature)`, with nature ±10% (`PP:214-275`).
- **Forms:** rolled among `lineForms(baseID)` with weight 2 if uncollected and 1 if collected (`PF:138-147`).
  - "Collected" means every species of the line that shows the form is owned in it (`CM:783-790`).
  - Ownership includes the active Pokémon and released entries; shiny and normal count once.
- **Not rolled at hatch:** gender, ability and moves wait for details. Level starts at 5.
- **Prayers:** `pray()` increments `eggPrayers`, which is copied to the individual at hatch. It is cosmetic and never read by a roll (`CS:1406-1412`, `CS:1778-1779`).

### 3.5 Hatch flow
- **Trigger:** each `update()` tick. If the egg is ready (`eggUsage ≥ threshold`), call `hatchIfNeeded`; otherwise call `ensureEggPrefetch` (`CS:971-980`).
- **Prefetch** (`CS:1628-1678`):
  - Roll and persist `pendingHatchID` and `pendingForm`.
  - Fetch the line.
  - Warm sprites: static, animated, shiny animated, and static shiny for form lines.
  - Load form names.
- **Hatch:**
  - Use the pending species or roll now (`CS:1560-1590`). `hatchCore` fetches the line, and a failure keeps the egg and sets `isHatchRetryDelayed` (`CS:1739-1747`).
  - A premium-egg guarantee is checked against the real rarity; a miss discards the pre-roll (`CS:1759-1767`).
  - Effects: reset `eggUsage` and `eggTier`, build the plan, create `MonState` at stage 0, notify, open a 4 s `levelUp` window, apply the overflow, fire the celebration (`CS:1768-1824`).
- **Concurrency guard:** `activeGeneration` is captured before the first await and checked again before mutating (`CS:1569-1583`, `CS:1751-1755`; defect-log 798-810).

---

## 4. Evolution

### 4.1 Fetching the line (`API:98-119`)
- `GET /pokemon-species/{base}`: `capture_rate`, `is_legendary`, `is_mythical`, `names`, `evolution_chain.url`.
- Then GET the chain URL. **SSRF guard:** the URL must be https with host exactly `pokeapi.co` (`API:266-269`).
- Build the tree from `chain.species.url` (id = last path segment, `API:258-262`) and `evolves_to`.
- Then fetch `/pokemon-species/{id}` for every node to collect names in all languages (`API:111-115`).
- **Caches:** the line and species caches are **memory-only** (`API:30-31`). After a restart nothing evolves until a fetch succeeds.
- **Pruning:** the tree keeps only ids 1…649, the range of Gen-V animated sprites; a pruned node drops its whole subtree (`CM:324-330`, `CM:349-353`, `CM:388`).
  - Example: Eevee loses Sylveon.

### 4.2 Choosing a branch
- `makeEvolutionPlan` runs at hatch and walks root to leaf (`CS:1069-1078`).
- At each node the pool is the children whose reachable finals include one not yet in `collectedFinals` as `"base:final"`; if that is empty, all children (`CS:1061-1067`).
- The choice is uniform (`next % count`).
- The plan is persisted in `plannedPathIDs`, and evolution consumes it without RNG (`CS:1025-1028`).
- An invalid plan is repaired at evolve time (`CS:1029-1035`, `CS:1080-1086`) or on line load (`CS:1102-1121`). A complete valid plan is reused, so no RNG is spent.
- **Display:** the realized path, then the guaranteed linear prefix, then a single "mystery" item if a branch follows (`CS:297-316`).

### 4.3 Evolve step (`CS:1037-1054`)
- `pathIDs = prefix(stageIndex+1) + [next]`, `stageIndex += 1`, carry the excess.
- Enrich the profile from the new species' details.
- Set `justEvolvedTo`, fire the evolve celebration, open a 4 s `levelUp` window, notify.

### 4.4 Forms through evolution (`PF:91-127`, `docs/reference/pokemon-forms.md`)
- The form belongs to the individual and is resolved per species.
- `lineBaseIDs`: 413→412, 421→420, 423→422, 555→554, 586→585.
- So Shellos (422) east becomes Gastrodon (423) east. Burmy sandy becomes Wormadam sandy, while Mothim (414) shows its default.
- Cherubi, Darumaka and Deerling eggs already carry their Cherrim, Darmanitan and Sawsbuck form.
- **Catalog:** 23 species (`PF:45-89`).
- Forms change sprites, names and collection slots only. Battle data stays default-form.

### 4.5 Ditto disguise (M1 decision needed)
- A disguised hatch looks like the rolled common line (`dittoDisguise = baseID`, shiny hidden).
- At the **first** evolution threshold it reveals instead of evolving (`CS:1016-1019`, `CS:986-990`, `CS:1828-1878`):
  - It becomes #132's line (rare, one form), the excess carries over, the form is cleared.
  - The boost is recomputed for base 132, the profile growth is rebased by fraction (`PP:130-140`), and the representative is reconciled.
  - A 5 s `levelUp` window opens.
- Ditto never appears in the normal pool.
- **Candy during a disguise:** only enough candies to reach the reveal are consumed (`CS:1232-1238`, `CS:1179-1182`).
- It is off in non-bundled builds. Decide whether the Go helper enables it.

---

## 5. Graduation and Pokédex

### 5.1 Graduation (`CS:1123-1154`)
- Set profile growth to T (level 100) and enrich.
- `collectedFinals.insert("base:final")`.
- Append a `DexEntry`:
  - id = `instanceID`, `chainOrder` = `pathIDs`, `caughtAt` = now.
  - `isShiny` = raw `isShiny`.
  - `nature`, `form`, `profile`, `prayers`, and `names` taken from the line.
- Notify, open a 6 s `levelUp` window, `active = nil`, reconcile the representative.
- Reset `eggUsage` to 0 (excess lost) and clear prayers. Start the next prefetch immediately.

### 5.2 Release (Later, but it shapes the dex)
- `buyEgg` appends a `DexEntry` with `releasedAt` set (`CS:355-375`, `CS:1381-1383`).
- Only the reached forms are recorded (`prefix(stageIndex+1)`), with shiny = `displaysShiny`.
- `collectedFinals` is not changed.

### 5.3 Species cells (`CS:558-596`)
- **Owned species:** every id in `dex[].chainOrder` plus the active Pokémon's reached `pathIDs[0...stageIndex]`. `plannedPathIDs` is never used.
- **Per species:**
  - rarity of the first entry seen (the line's rarity)
  - names
  - `isShiny` if any owned individual is shiny (the active one uses `displaysShiny`)
  - `hasNormal`
  - `isRaising` only when it is the active Pokémon's current species
- The main grid has one cell per species (forms collapsed), sorted by id. The detail view lists forms in catalog order (`CS:486-489`).
- **Catch log:** `dexEntries = dex` plus a synthetic active entry with id `active-<base>-<current>`, pinned first. The rest sort by newest `caughtAt` (`CS:326-345`, `CS:394-400`, `CS:717-794`).
- **Individuals of a species:** `finalID == species && profile != nil && matching form` (`CS:2142-2148`).
- **Search:** number or `#number` (partial match), or a case- and diacritic-insensitive name in any stored language (`CS:615-643`).
- **Name backfill** when `namesVersion != 1` or a chain species has no names. Partial responses do not set the version, and offline does not store `#id` (`CS:802-861`).

### 5.4 Regions and badges (`Core/PokedexRegion.swift`)
- **Regions:** Kanto 1–151, Johto 152–251, Hoenn 252–386, Sinnoh 387–493, Unova 494–649. National = 1–649 (`:14-34`).
- **Completion** uses owned species ids (forms collapsed). Shiny-owned counts if any individual of the species is shiny (`:77-98`; `CS:494-512`).
- **Badges:** in order region, regionShiny, unown (all 28 Unown forms, any color), national, nationalShiny (`:101-112`).
  - Keys: `region.<r>`, `regionShiny.<r>`, `unown`, `national`, `nationalShiny`.
  - These keys are persisted and must never change (`:58-66`).
- **Notifications:** once per badge from `save()` (`CS:518-527`). On a first run, or after importing a legacy save, existing badges are recorded without notifying (`CS:531-541`, `CS:2044`).

### 5.5 Representative Pokémon
- **State:** `representativeSpeciesID` plus `representativeForm`. nil follows the active Pokémon, or the egg (`CM:683-687`).
- **Setter** rejects species that are not owned, i.e. not in `dex.chainOrder` with the same resolved form and not in the active Pokémon's reached path (`CS:230-236`, `CM:752-761`).
- **Reconciled** on decode, sanitize, graduation, release, reveal and line normalisation (`CM:811-821`).
- **Subject drawn by the menu bar and floating pet** (`CS:214-225`):
  - pinned: (species, `ownsShinySpecies`, resolved form)
  - otherwise: (current, `currentIsShiny`, current form)
- **Growth text** always follows the active Pokémon or egg: integer percent `used*100/total` (`CS:284-291`).

---

## 6. Rare Candy

### 6.1 Awarding (`CS:1436-1531`, `US:776-875`)
- **Windows** (`US:776-844`, `CM:294-313`):
  - Claude per account: `<prefix>.fiveHour` (session) and `<prefix>.sevenDay` (weekly). The prefix is `claude` or `claude.<accountKey>` (`Core/ClaudeAccountRoots.swift:290-307`). Non-default accounts only after a reading below 100% has been seen (`US:846-862`).
  - Codex: `codex.<limitId|limitName|"codex">.primary|secondary`. A window of ≤1440 min (or unknown) is a session; longer is weekly (`US:865-868`). **No epoch.**
  - Antigravity: `antigravity.<gemini|3p>.5h|weekly`, with an epoch.
  - Excluded: Opus/Sonnet weekly, scoped limits, Codex spend limits, providers without official limits.
- **Pure evaluation** `evaluateCandyGrants` (`CS:1454-1488`), per window:
  - If an epoch is present: the first sighting is only recorded. A **new epoch** (`isNewEpoch`) clears the tier and stores the epoch.
  - `isNewEpoch` (`CS:1420-1444`):
    - Parse the value as Unix seconds (>1e8), milliseconds (>1e12) or ISO-8601.
    - The window counts as reset if it advanced ≥1800 s (session) or ≥86400 s (weekly).
    - Unparseable strings reset on any change.
  - Utilization < 100: clear the tier (re-arm), store the epoch.
  - Otherwise, if tier < 1: set tier 1 and grant 1 (session) or **5** (weekly) (`CM:227`).
- **Wrapper** `grantCandies` (`CS:1493-1531`):
  - Requires `limitsReady` (`US:871-876`).
  - On the first run, mark every window already at ≥100% as granted, without granting.
  - Persist even when nothing was granted if the tier or epoch maps changed.
  - Notification with `showsOnPet: false`.
  - Called after every refresh (`APP:286-289`).

### 6.2 Use (`CS:1171-1249`)
- **XP per candy:** 100,000,000, not scaled by difficulty (`CM:225`).
- Added to `usedAtStage` through `applyUsage`; `usedSinceInstall` is never touched.
- **Requires** an active Pokémon, **a loaded line**, and stock (`CS:1173`, `CS:1177-1187`).
- **Bulk:**
  - `maxUse = min(stock, ceil((Σ effective thresholds from stageIndex to totalForms−1 − usedAtStage) / 100M))` (`CS:1189-1193`).
  - `plan(count)` gives evolves/graduates, the carry-over, and the XP discarded at graduation (`CS:1204-1221`).
  - Only the needed candies are consumed (`CS:1229-1249`).
- **Result:** `.evolved`, `.graduated`, `.progressed` or `.unavailable`. The UI shows "+XP" feedback.

---

## 7. External data

| Call | URL | Fields used | Cache |
|---|---|---|---|
| Base index | POST `https://graphql.pokeapi.co/v1beta2`, body `{"query":"{ pokemonspecies(where: {evolves_from_species_id: {_is_null: true}, id: {_lte: 649, _neq: 132}}, order_by: {id: asc}) { id capture_rate } }"}`, timeout 15 s, status 200 required | id, capture_rate | `base-index.json` `{fetchedAt, entries:[{id,captureRate}]}`, 30 days plus stale fallback (`API:142-227`) |
| Species | GET `https://pokeapi.co/api/v2/pokemon-species/{id}` | capture_rate, is_legendary, is_mythical, names[], evolution_chain.url, evolves_from_species, gender_rate | memory only (`API:229-234`) |
| Chain | GET the `evolution_chain.url` (validated) | chain.species.url, evolves_to | memory only, as part of the line |
| Details | GET `/api/v2/pokemon/{id}` | name, height, weight, base_experience, types, abilities, stats, moves filtered to b2w2 | `pokemon-details-v1/{id}.json`, 30 days plus stale fallback (`API:45-82`) |
| Names | GET `/api/v2/{type\|ability\|move\|pokemon-form}/{name}/` | `names`, or `form_names` for pokemon-form | `pokemon-names-v1/<kind>-<name>.json`, 30 days plus stale fallback. Name must match `[a-z0-9-]`; in-flight requests are deduped (`PokemonNameLocalization.swift:56-121`) |

- `get()` does not check the HTTP status (`API:245-250`).
- **Language resolution:** preferred codes (ja → `ja-hrkt`/`ja`, pt → `pt-br`/`pt`), then `en`, then `#id` (`CM:13-26`, `PokemonNameLocalization.swift:20-31`).
- **Form name ids** come from `PF:30-89`. Castform, Rotom, Kyurem and Genesect show a localized "Normal Form" for their default (`PF:208-237`).
- **Offline behaviour:**
  - The egg is kept and the hatch is retried every tick.
  - Usage is banked while no line is loaded, and evolution catches up once it loads.
  - Candy cannot be used without a line.
- **Worth bundling in Go** (removes the memory-only line problem). The Swift app deliberately bundles no Pokémon data (`API:24`, `SL:3`), so bundling is a design choice:
  - the base index
  - pruned evolution trees
  - capture_rate, is_legendary, is_mythical, gender_rate
  - species names in all languages
  - the form catalog and form names
  - optionally default-form details (types, stats, abilities, b2w2 level-up moves)
  - Fetch sprites at runtime.

---

## 8. Sprites

### 8.1 URLs (`SL:6-7`, `SL:36-45`)
Base: `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon`
- **Animated:** `/versions/generation-v/black-white/animated/[back/][shiny/]<asset>.gif`, only for ids 1…649 (`SL:49`).
- **Static:** `/[back/][shiny/]<staticAsset>.png`.
- `asset = "<id>"` for the default form, otherwise `"<id>-<form>"` (`PF:153-158`).
- `staticAsset` = the PokéAPI variety id when one exists (for example `10006` for Sky Shaymin), else `asset` (`PF:161-167`).
- **Items:** `.../sprites/items/<name>.png`, e.g. `rare-candy`, `shiny-charm`; Mint has none (`CM:188-194`).
- **Egg:** `.../sprites/pokemon/egg.png`, a 96×96 canvas cropped to a square around its content (`SL:88-99`, `SL:314-351`).

### 8.2 Cache
- **Disk:** `sprites/<key>.<gif|png>` with `key = <asset>-[back-][sh](a|s)`. Examples: `25-a`, `25-sha`, `492-sky-s`.
- Static files use `asset`, not `staticAsset`, in the key (`SL:27-31`). Item and egg keys are `item-<name>` and `egg`.
- Writes are atomic. A 200 status and a non-empty body are required (`SL:57-63`). There is no disk eviction.
- **Memory:** an LRU of 64 raw blobs (`SL:12`, `SL:102-114`), 64 NSImages, and 16 decoded animations.

### 8.3 Fallbacks and retry
- Shiny missing → plain. Animated missing → static (`SL:135-155`, `SL:267-289`).
- Back missing → front, but **only after a 404**. The 404 is remembered for the session; other errors retry the back sprite (`SL:15`, `SL:59`, `SL:186-214`).
- **Recovery loop** (`SL:218-248`):
  - Show the static fallback once.
  - Retry the GIF after 2, 4, 8, 16, then every 30 s, with no attempt limit.
  - Success requires **>1 frame**. Species above 649 stay static.

### 8.4 GIF decoding and timing
- The Swift code does **no** disposal or compositing: ImageIO returns fully composited frames (`SA:9-19`).
- **The Go helper must composite itself** (`image/gif`):
  - Draw each frame's sub-rectangle onto the logical canvas (`Config.Width` × `Height`).
  - Honour disposal None, Background and Previous, and transparency.
  - Emit full RGBA frames, for example as PNGs for St.
- **Delays** (`SA:56-63`): prefer the unclamped delay, then the clamped one, else 0.1 s; anything under 0.02 s becomes 0.1 s. In Go, `Delay[i]` is in centiseconds.
- **A single-frame GIF counts as "not animated"** (`SA:11`).
- **Frame-rate cap by decimation**, `capFrameRate(floor)` (`SA:31-53`):
  - Accumulate delays. When `acc + 1e-9 ≥ floor`, emit the first frame of the group with the accumulated delay.
  - A short tail is added to the last emitted frame, so the loop keeps its length (a typical Gen-V GIF is 55 × 0.05 s).
  - Floors: powerSaver 0.4 (default), balanced 0.2, smooth 0.1. Low-power mode uses at least 0.4 (`US:133-152`).
  - The transient popover uses native timing (floor 0).
  - Timer tolerance is 0.1 (`APP:311`).
- **Bob fallback and egg:** 2 frames at 0.5 s each, offset 1 px (`APP:518-525`).
- **Geometry:** GIF canvases are tight and non-square. Use aspect-fit, never stretch (`SL:360-376`). The menu bar is 22 pt high with the bottom aligned (`APP:527-552`).

---

## 9. Mood and display state (`CS:1987-2001`)
States are checked in this order:
1. **egg** if there is no active Pokémon.
2. **levelUp** while an event window is open: hatch 4 s, evolve 4 s, graduate 6 s, reveal 5 s (`CS:1817`, `CS:1053`, `CS:1142`, `CS:1869`).
3. **tired** if `isLimitWarning`: any official limit at or above the critical threshold, or the 5-hour forecast runs out before reset (`US:727-747`).
4. **sulk** if `hasUsageData` and idle for at least 3 h (`CM:109`).
   - Idle = `min(reported, time since this store last saw today's total rise)` (`CS:1974-1985`).
   - nil idle never sulks. Sulk shows the back sprite, as does `alwaysFacesBack`, but never for an egg (`CS:18`).
5. **sleep** if there is no data or `today == 0`.
6. Otherwise by burn tier, using the sum of active-block tokens per minute (`US:682-684`, `US:878-884`):
   - **idle** ≤ 1,000
   - **working** < 100,000
   - **focus** otherwise ("fast" < 400,000 and "blazing" both map here)

Other display bits:
- The egg is "imminent" at ≥ 90% (`UI/CompanionView.swift:694`).
- Status strings live at `UI/CompanionView.swift:912-923`.
- `latestEvent` drives the pet's speech bubble (`CS:1535-1556`).
- **Agent activity** (floating pet only):
  - URL: `poketokenbar://agent?state=working|waiting|done|idle&message=&label=`.
  - Limits: message 80 characters, label 24.
  - Working times out after 10 min, done shows for 6 s, at most 8 agents (`Core/AgentActivity.swift:5-100`).

---

## 10. Later milestones (brief)
- **Shop** (`CS:1277-1349`):
  - Wallet = `usedSinceInstall − spentTokens`.
  - Prices × shop difficulty: Candy 500M (`CM:232`), Mint 100M (`CM:240`), Shiny Charm 3B, passive and buy-once (`CM:246`).
  - Sorted by price, with owned passive items last. Buying `count` items is all-or-nothing.
- **Mint:** sets a random nature different from the current one. Cosmetic, works without a line (`CS:1255-1271`).
- **Shiny Charm:** 1/48 instead of 1/64. Notification copy must use the runtime denominator (`CM:249`; defect-log 228-238).
- **Eggs and premium eggs** (`CM:253-276`, `CS:1360-1402`):
  - Prices: plain 1B, uncommon-or-better 2.5B, rare-or-better 4B (ratio of the T values). No legendary-only egg.
  - Buying requires an active Pokémon, which is released.
  - `eggTier` is persisted. It filters the pool by capture_rate, is re-checked against the real rarity at hatch, and is consumed by the hatch.
- **Save export and import** (`ST`):
  - The envelope must have the right format and a schema ≤ 2; files over 8 MB are rejected.
  - Rebase on import (`ST:211-239`):
    - Keep this device's language.
    - Merge the candy ledgers: max tier, latest epoch (`ST:242-268`).
    - Re-seed the device ledger if this device has current data; otherwise defer the baseline.
  - Default file name `PokeTokenBar-Save-yyyy-MM-dd.json`.
- **Usage recap:** `Core/UsageRecap.swift` (not read).

---

## 11. Relevant defect-log entries (`docs/reference/defect-log.md`)
- 19-36: a transient GIF failure must not become a permanent static state. Use one cancellable recovery loop with 2–30 s backoff.
- 38-43: seed animated views from cached GIF frames. Static PNG padding differs from the GIF canvas.
- 58-72: a legacy import must seed dex-completion history before new catches.
- 106-118: snapshot summaries must carry the species-resolved form.
- 134-146: alternate forms use default-form data, and the UI says so.
- 172-190: species shiny ownership is not an individual's appearance. Normal and shiny are tracked separately.
- 200-218: never replace API identifiers with localized names. Keep all languages; use `namesVersion` for backfill.
- 228-238: balance numbers in copy (odds, hatch point) come from runtime values.
- 259-264: profile level is computed from difficulty- and boost-aware stage progress and never drops.
- 281-288: lenient decoding must be paired with range sanitization at the trust boundary (overflow trap loop).
- 289-306: the repeat-growth boost is decided by the collected base at hatch, persisted, and re-decided at a Ditto reveal.
- 798-810: async completions must not land on a replaced subject. Capture `activeGeneration` before the earliest await.
- 984-990: lazily backfilled names need a trigger on every screen. Never persist `#id`.
- 993-1013: released individuals stay in the dex (reached forms only). `collectedFinals` is untouched.
- 1015-1024: Ditto's hidden shiny must go through `displaysShiny` everywhere, including snapshots.
- 1185-1198: the fps cap must decimate, not hold. `max(floor, delay)` turns animations into slow motion.
- 1199-1204: timer tolerance stretches playback. Keep it ≤ 0.1.
- 1224-1244: Codex candy re-arms only after a reading below 100% is observed (no epoch).
- 1246-1261: candy epoch re-arm must ignore jitter. Session needs ≥ 30 min of advance, weekly ≥ 24 h.
- 1272-1281: on transfer, separate progress from device ledgers. Re-seed the ledger.
- 1282-1289: read a rotated snapshot into memory before any write that can prune it.
- 1290-1302: sanitize duplicate species ids in chains, which otherwise crash.

---

## 12. Test cases worth porting
Tests use SplitMix64 (`CompanionTests.swift:63-73`).

**Bodies read:**
- `CompanionTests.swift:8-56` (PokemonBalanceTests):
  - Phase sums equal T within ±2 for k = 1…3.
  - The boost halves every phase; common k=3 stage 0 boosted = 62,500,000.
  - cr 255 → common, 90 → uncommon, 45 → rare, 3 + legendary → legendary.
- `CompanionTests.swift:815-976`, ledger. Exact before/after values are in the test bodies:
  - Baseline: 48M then 148M credits 100M.
  - Drop-then-rise: 200, then 40, then 75 gives 235.
  - Empty snapshot: no rebase.
  - Partial provider loss, then recovery: no re-credit (1500 → 1500 → 1700).
  - Rollover d1 200 → d2 100 gives 300.
  - Late provider after rollover: 1600, then 2300, then 2500.
  - A stale empty map does not consume the date.
  - Legacy aggregate: seed only.
- `CompanionTests.swift:990-1008`, `:1077-1083`: hatch at exactly 5,000,000. Below that, no hatch. Overflow of 500,000 gives `usedAtStage` 500,000.
  - Note: the inline comments wrongly say "1M".
- `CompanionTests.swift:1086-1108`: REST fallback hatch; a new egg after graduation re-incubates.
- `CompanionTests.swift:1119-1144`: linear 1→2→3 evolves at 125M and 250M, graduates at 375M with `chainOrder` [1,2,3]. With no evolutions, graduation at 750M.
- `CompanionTests.swift:1208-1265`:
  - Branch preference: three re-hatches of 10→{11,12,13} yield all three finals.
  - Repeat boost halves the threshold.
  - Wurmple plan is preselected, and evolution consumes no RNG.
- `CompanionTests.swift:1454-1476`: an asymmetric branch graduates with chain [1,2] or [1,3,4].
- `CompanionTests.swift:1637-1656`: shiny = draw 1 % 64 == 0 and nature = draw 2 % 25, for seeds 1, 7, 42, 12345 via `hatch(baseID:)`.
- `CompanionTests.swift:1785-1806`: on the `hatchIfNeeded` path the draws are species, shiny, nature. 135M from the egg hatches and evolves once.
- `CompanionTests.swift:1838-1897`: weighted sampler.
  - [100,100,100] maps roll%300 to bands.
  - [254,2] gives the legendary band.
  - Collecting halves the weight ([200,200] becomes [100,200]).
- `CompanionTests.swift:1949-1954`, `PokemonFormTests.swift:115-137`: sprite URLs and cache keys (`25-a`, `25-sha`, `492-sky-s`, `10006.png`, `412-sandy.png`, Mothim 414 default GIF).
- `CompanionTests.swift:2173-2182`: chain URL validation (rejects evil hosts, look-alike hosts, http, empty).
- `PokemonFormTests.swift:93-111`, `:161-212`: line forms, per-species resolution, legacy Unown keys, normalisation, collected line forms.
- `UnownTests.swift:113-140`: form roll weights. All uncollected: `roll(i)` for i < 56 gives each letter twice. `CollectionWeight` is `max(1,w)` or `max(1,w/2)`.
- `UnownTests.swift:203-227`: draw order species, shiny, nature, profile seed. Pikachu 255 plus collected Unown 112 gives `% 367`.
- `RareCandyTests.swift:34-231`: `evaluateCandyGrants`.
  - Session gives 1, weekly 5, 99.9 gives nothing, no double grant, re-arm below 100.
  - An epoch advance of 5 h or 7 d re-grants; microsecond, ±1 s, and 1–5 min drift do not.
  - The first epoch sighting is not a re-arm. Mixed windows give 6.
  - Codex classification: 300 and 1440 min are session, 10080 weekly, nil session.
- `RareCandyTests.swift:369-535`: candy use.
  - Batch from 100M with 10 candies: max 7; plan(3) evolves twice with carry 25M.
  - Graduation cap: 3 used, discarded = 300M − remaining.
  - Rejects counts ≤ 0.
  - 124M + 1 candy gives exactly one stage. Boosted: carry = 100M − 1.
- `DifficultyTests.swift:39-129`, `:188-201`, `:221-290`:
  - Thresholds × difficulty. At 0.1 the egg needs 500,000.
  - Rescale keeps an incomplete stage incomplete (`tokensToNext` = 1).
  - Clamp: 0 and −5 give 0.1; NaN and ±inf give 1.0.
  - Slider snap: 1.234 → 1.2, 15.7 → 16, 0.0123 → 0.012.
- `PokemonProfileTests.swift:40-210`:
  - IV determinism.
  - Enrich: Slowpoke gender_rate 8 gives female, 4 moves at L9 without the TM move, idempotent.
  - Stats including Shedinja.
  - Level 5 → 100.
  - Rebase common 125M → rare 500M, level 20.
  - Hidden-ability seed 115 gives "hidden-two".
- `ProfileGrowthIntegrationTests.swift:48-109`: every difficulty and form count graduates at level 100 with growth 750M. 93.75M at 0.5 with boost gives level 52. Legacy migration gives 250M, level 36.
- `SaveTransferTests.swift:118-137`, `:489-571`: chain dedupe, clamps (1e15, 0, `totalForms` 12, `stageIndex` 0), field classification.
- `SaveSnapshotTests.swift:158-226`: prune to 10, 12 h auto interval, corrupt-file recovery with a `.corrupt` copy.
- `PokedexRegionTests.swift:9-36`: boundaries and totals 151, 100, 135, 107, 156.
- `DittoTests.swift:72-96`, `:130-149`: disguise hit only for common with ≥ 2 forms and `roll % 128 == 0`. Reveal at 125M with 300M banked gives #132, rare, carry 175M.
- `PremiumEggTests.swift:100-131`: tier ceilings match the classification; prices.

**Candidates (name and line only, body not read):** `PokedexRegionTests.swift:58-170` (badges); `DexCompletionStoreTests.swift:34-114`; `SpriteAnimationRecoveryTests.swift`; `SpriteAnimationPreviewTests.swift`; `DexNameMigrationTests.swift`; `CompanionTests.swift:1279-1412` (reload and plan normalisation); `FreshEggTests.swift:35-104`; `SaveTransferTests.swift:212-305` (rebase).

---

## Summary
- **Size of the port:** the M1 logic is about 2,500 lines of Swift (CompanionStore 2.3k with UI derivations, CompanionModel 0.8k, PokemonForm, PokemonProfile, PokeAPIClient, PokedexRegion and SaveTransfer about 1.2k). In Go that is roughly 2–3k lines plus the sprite/GIF pipeline. About 25 test groups have exact expected values.
- **Top 5 subtleties:**
  1. RNG draw order. One draw is taken per non-leaf plan node even with a single child; the Ditto roll is drawn only when enabled; the form roll comes after the profile seed unless prefetched.
  2. The per-provider ledger semantics: date rollover, regressions, missing providers, baseline. Plus the token definition (cache tokens included).
  3. The threshold rounding chain (std, then boost, then difficulty, each rounded) and the difficulty rescale that never completes a stage.
  4. Lines are memory-cached only. Usage banks while the line is unloaded, and evolution and candy wait for it. Bundling the data fixes this.
  5. GIF compositing and disposal: ImageIO does it on macOS, so Go must implement it. Also the decimating fps cap and the 1-frame-means-not-animated rule.
- **Could not determine:** the exact base-pool size (329 in a comment versus 328 measured; it is fetched at runtime); whether the Ditto disguise and prayers belong in M1 (product decision); the internals of `UsageRecap` and the floating-pet petting logic (not read).
