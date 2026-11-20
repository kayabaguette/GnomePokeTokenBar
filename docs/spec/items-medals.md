# M3 spec: item sprites and Pokédex completion medals

Upstream refs: `CM` = Core/CompanionModel.swift, `CS` = Core/CompanionStore.swift, `PR` = Core/PokedexRegion.swift,
`LOC` = Core/Localization.swift, `SL` = UI/SpriteLoader.swift, `CV` = UI/CompanionView.swift, `SV` = UI/ShopView.swift,
`BV` = UI/BagView.swift. Port refs are repo-relative to this repository. All paths verified by reading, nothing run.

**Status.** The open questions of the raw spec are resolved in §3 with the Milestone 3 decisions of `tasks/todo.md`
("Milestone 3 plan"). The contract that came out of it is in `docs/protocol.md` (§8.1, §14 decisions 37 and 38).

## 0. Headline: what is already done in the port

- **Item sprites: helper side is 90% done.** `helper/internal/sprites/store.go:183 Store.Item(ctx,name)` exists, with
  key `item-<name>` (`spritekey.go:15,88`), a name check (`validName`) and a test (`sprites_test.go:539-544`).
  The daemon never exposes it: `cmdSprite` (`internal/daemon/spritework.go:149-175`) accepts only `{egg:true}` or `speciesId`.
  The extension shows emoji for every item (`extension/lib/shop/shopModel.js:19-44`, `shopWidgets.js:22 iconTile`).
  The egg icon already uses a sprite (`shopView.js:90-101`, `EGG_ICON_SIZE = 26`).
- **Medals: done in the port (M1)**. The docs marked them LATER; phase 16 reconciled `protocol.md` §6 and `ui.md` §5.1, §9.
  - Helper: `companion/dex.go:114-210` (`earnedBadges`, `checkDexCompletions`, `seedDexCompletions`), `views.go:297-310`
    (`dexSummary.badges`), `events.go:55 BadgeEvent`, with tests in `dex_test.go:56-185`.
  - Extension: `dex/dexModel.js:431-465` (`regionMedal`, `headerMedals`), `dex/dexView.js:265-285` (header medals),
    `viewModel.js:385-400` (`badgeText`, notification copy), `stylesheet.css:621-629` (`.ptb-dex-medal`).
  - So sub-feature 2 is a **verification and doc-reconciliation task, not a build**. The gaps are listed in §2.9.

## 1. Item sprites

### 1.1 How upstream obtains item images
| Fact | Detail | Cite |
|---|---|---|
| Source | Not bundled. Fetched at runtime from `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/items/<name>.png` | `SL:7`, `SL:74-85` |
| Names | `rareCandy` -> `rare-candy`; `shinyCharm` -> `shiny-charm`; `mint` -> `nil` (no sprite, Gen 8 item) | `CM:188-194` |
| Fallback emoji | 🍬 rareCandy, 🌿 mint, ✨ shinyCharm; shown while loading, when the sprite is unavailable, or after a failure | `CM:196-202`, `CV:17-49` |
| Cache | Same dir as Pokémon sprites. File `item-<name>.png`, memory key `item-<name>`. Disk hit first, then network. Needs HTTP 200 and a non-empty body. Atomic write. A failure returns nil and **is not remembered** (retried on the next view appearance). | `SL:74-85`, `SL:292-312` |
| Decode caches | A shared `imageCache` of NSImage keyed by file path. Concurrent loads share one image object. | `SL:292-312`, SpriteImageCacheTests:120-160 |
| Native size | Not enforced. Comments say items are "usually square 30x30" (`CV:34-36`). Rendered 30 pt, aspect-fit, nearest-neighbour. | `CV:18-49` |
| Fit rule | `SpriteFit.size(for:box:)`: the long side touches the box, the ratio is kept. An empty image gives a square of the shorter side. | `SL:360-372` |
| Egg (for the same surfaces) | Not an item. `pokemon/egg.png` is a 96x96 canvas, cropped once to a square around its non-transparent content (alpha>0.01), then drawn through `SpriteView(speciesID: nil, size: 26)` inside a 30x30 frame. Fallback 🥚. | `SL:88-100`, `SL:317-350`, `SV:176-177`, `CV:149,261-263` |

### 1.2 Every surface that shows an item image
| # | Surface | Size | Cite |
|---|---|---|---|
| 1 | Shop item card (rareCandy, mint, shinyCharm) | 30 pt `ItemIconView` | `SV:71` |
| 2 | Shop egg cards (three tiers, same egg sprite) | 26 pt in a 30 frame | `SV:176-177` |
| 3 | Bag item card (owned items only; fixed order rareCandy, mint, shinyCharm) | 30 pt `ItemIconView` | `BV:59` |
| 4 | No other surface. Notifications for candy use the 🍬 emoji in text (`viewModel.js:362`), not an image. `SettingsView.swift:557` shows the egg sprite at 28, which is not an item. | | grep `ItemIconView` |

The port mirrors surfaces 1-3 with `iconTile(emoji, tint)` (34 px tile, 18 px emoji, `stylesheet.css:1398-1408`).

### 1.3 Proposed protocol addition (additive, optional)
`getSprite` gets a third request shape, next to `{speciesId...}` and `{egg:true}`:
- Args: `{"item": "<name>"}`. `name` is the PokéAPI file stem (`rare-candy`, `shiny-charm`). It must match
  `validName`. A bad name gives `badArgs`. It is mutually exclusive with `egg` and `speciesId`; if two are present the order is `egg`, then `item`, then `speciesId`.
- Reply is unchanged: `{key: "item-<name>", status: "ready", manifestPath: ".../sprites/item-<name>.png"}`.
  Always `ready`, never `fallback`, because items have no fallback chain. A 404 or offline gives error `unavailable`.
- No new events, no change to `state`. Old extensions are unaffected (the field is new on requests only).
- Who knows the names: the **extension** (`ITEM_SPRITE = {rareCandy:'rare-candy', shinyCharm:'shiny-charm'}`, mint absent).
  Do not add a field to `companion.inventory` or `shop.entries`; the item ids are already stable and the name table is 2 rows. Alternative (rejected):
  the helper sends `spriteName` per entry. It couples the wire to upstream filenames for no gain.
- `unavailable` for a permanent 404 should be cached for the session on the helper (`store.missing`, already in `load`), so `mint`
  is never fetched. The extension skips mint anyway (no name).
- Doc edits: `protocol.md` §8.1 args list (add the `item` line), §8.2 note that item keys are already specified, §14 decision entry (item/egg shapes are mutually exclusive).
- Extension: `lib/sprites.js:9 spriteRequestKey` must return `item-<name>` for `{item}`, or the cache collides on `JSON.stringify([undefined,...])`.

### 1.4 Extension behavior
- `iconTile(emoji, tint)` takes an optional `{item}`. It renders the emoji at once, then calls `sprites.resolve({item})`; on success it swaps
  the child for `St.Icon` (`Gio.FileIcon`, `icon_size` 30, class `ptb-item-sprite`), the same pattern as `shopView.js:_loadEggIcon`. On failure the emoji stays, and a retry happens the next time the popover opens (the resolver forgets failed entries, `sprites.js:44-47`).
- Pixel art must be nearest-neighbour. The egg path uses `St.Icon`; check that the egg PNG is not blurred at 26 px
  (`Clutter` texture filter) and use the same approach for 30 px. This is **uncertain**: St.Icon scaling filters in GNOME 51 were not verified. Fall back to `St.Widget` with a `Clutter.Image` or a `background-image` if linear filtering shows.
- Native item PNGs are about 30x30 (unverified; upstream only asserts "mostly square"). At 30 px this is 1:1, so no scaling artefacts if the tile shows it at 30. Never stretch a non-square image: use `spriteFit.js` (already in the extension) for aspect-fit.
- Mint keeps 🌿 permanently. That is upstream behavior, so no work.

### 1.5 Pure logic vs UI
- Go (pure): the `item` arg parse in `cmdSprite` plus `spriteWant` for items (new `item string` field). The loading code exists.
- GJS (UI): the name table, `iconTile` upgrade, `spriteRequestKey`, bagView/shopView wiring.

### 1.6 Swift tests to port
- `SpriteImageCacheTests.testItemLoadsShareTheImageCacheInBothDirections` (SpriteImageCacheTests:120-160): Swift-specific (NSImage object identity). **Do not port**; the Go equivalent is "second `Store.Item` call is a cache hit with no HTTP request" (`sprites_test.go:539` covers a first call only; add a hit counter assertion).
- `ShinyCharmTests.testConstantsAndPassiveFlag` (ShinyCharmTests:70-78) asserts `spriteName == "shiny-charm"`. Port as a JS table test: `ITEM_SPRITE.shinyCharm === 'shiny-charm'`, `.rareCandy === 'rare-candy'`, `.mint === undefined`.
- New port-only tests: `daemon_test.go` for `getSprite {item:"rare-candy"}` (ready, `.png` path, key `item-rare-candy`), `{item:"../x"}` -> `badArgs`, `{item:"mint"}` with a 404 server -> `unavailable`; JS `spriteRequestKey({item:'rare-candy'}) === 'item-rare-candy'` and differs from the egg key.

## 2. Pokédex completion medals

### 2.1 Medal catalog and exact earning rules (`PR:51-112`)
| Medal (key) | Count | Rule |
|---|---|---|
| `region.<r>` | 5: kanto 1-151, johto 152-251, hoenn 252-386, sinnoh 387-493, unova 494-649 (`PR:14-22`) | owned species ids in range `>= total` (151, 100, 135, 107, 156) |
| `regionShiny.<r>` | 5 | species in range that are shiny-owned `>= total` |
| `unown` | 1 | `collectedForms(201).count >= PokemonForm.forms(201).count` (28 letters: A-Z, !, ?; `PokemonForm.swift:14,48`) |
| `national` | 1 | owned species in 1...649 `>= 649` |
| `nationalShiny` | 1 | shiny-owned species in 1...649 `>= 649` |
Order: for each region, `region` then `regionShiny`; then `unown`, `national`, `nationalShiny` (`PR:101-112`). 13 possible medals.
Keys are persisted and must never change (`PR:49-66`). There is **no** shiny Unown medal and no per-form medal.

Counting rules (`PR:77-98`, `CS:473`, `CS:494-512`):
- Species-level, forms collapsed: `dexSpecies = collectedDexSpecies(groupForms: false)`. One id counts once however many forms or individuals exist.
- "Owned" = every id on a `dex` entry's `chainOrder` (the entire evolution line of a graduated individual) plus the **reached** part of the active path (`pathIDs.prefix(stageIndex+1)`); never `plannedPathIDs` (`CS:471-472`). Released entries still count only while in `dex`.
- Shiny-owned = any individual of that species is shiny (`collectedDexSpecies`: `if entry.isShiny { a.isShiny = true } else { a.hasNormal = true }`). A species with both a normal and a shiny individual is owned and shiny-owned.
- Unown: forms come from `dex` entries whose `chainOrder` contains 201 (resolved form) plus the active Pokémon's form if its reached path contains 201 (`CompanionModel.swift:769-779`). Color/shiny is irrelevant. Unown (id 201) counts once as a Johto species (`testCurrentFormsCountUnownLettersWithoutInflatingRegionalSpecies`, DexCompletionStoreTests:80-108, expects johto.owned 1 from Unown and hoenn.owned 1 from species 351).
- Out-of-range ids (0, 650+, forms with ids over 10000) are ignored by regions and by national (`PR:93`; region test 62).

### 2.2 Persistence
- Medals are **derived, never stored**: "derived, never persisted (ownership only grows)" (`CS:508-513`).
- Two saved fields only: `notifiedDexCompletions: Set<String>` (announced keys) and `dexCompletionsSeeded: Bool` (`CM:700-704`, decode `CM:746-747`, both lenient with defaults `[]` and `false`).
- Seeding (`CS:531-541`, called at init `CS:104` and on save import `CS:2044`): first time only, set `seeded = true`; add all currently earned keys to `notified` **without notifying**; write the file only if the key set is non-empty (so a fresh or corrupt-recovered save is not recreated). Consequence: if nothing is earned yet, `seeded` is saved only on the next natural save.
- Announcing (`CS:518-527`): runs inside `save()` just before encoding (`CS:2306-2311`). Returns early unless `seeded`. For every earned-but-not-notified badge, in badge order: fire the notification, insert the key. So the keys are written in the same pass.
- A save import (`applySave`) goes through `seedDexCompletionsIfNeeded` again after state replacement, and an export written by an older build that lacks both fields is treated as "not seeded", so its existing medals are silent and the **next** completion still fires in the same session (DexCompletionStoreTests:110-160). SaveTransfer treats both fields as progress, kept across rebase (SaveTransferTests:554-560).
- Port: already identical (`dex.go:164-210`; `persist.go:93`). The Go `save` announces before write per `CS:2306`.

### 2.3 Display surfaces
1. **Dex header, row 1** (`CV:1562-1569`, `earnedMedals` `CV:1623-1636`): `dexTitle` + species total + spacer + medals. Only `unown`, `national`, `nationalShiny` (region medals are filtered out, `CV:1625-1628`). The strip takes no space when empty.
2. **Region chips, row 2** (`regionChips` `CV:1597-1620`, `RegionTally` `CV:955-989`): one capsule per region, tap filters the grid. They carry the region medal inline: ✨ when shiny-complete, else 🏅 when complete, else nothing (`CV:968-973`).
3. **Notification** (one per newly earned badge, §2.5). No toast, no celebration animation, no Bag/Shop involvement.
4. Not shown anywhere else: not on the species detail, not on the home tab, not in the catch log header.
5. `dexNationalLabel` ("National Dex") and `dexUnownLabel` ("Unown collection") exist in `LOC:1473-1479` but are **unused** by any view (grep shows no caller); only the tooltips from `dexBadgeLabel` appear.

### 2.4 Visuals
| Element | Spec | Cite |
|---|---|---|
| Medal capsule | emoji at 12 pt; padding 5 h / 2 v; fill `yellow` at 0.14; stroke `yellow` at 0.5, 0.5 pt; Capsule; tooltip `dexBadgeLabel` | `CV:991-1004` |
| Emoji map | region 🏅, regionShiny ✨, unown 🔤, national 🌍, nationalShiny ✨ | `CV:1638-1646` |
| Region chip | HStack spacing 3; emoji 10 pt; label 11 pt (semibold if selected, else medium); count `owned/total` 11 pt bold; selected adds a 9 pt bold checkmark; padding 6 h / 2 v | `CV:962-989` |
| Chip color | accent = green if complete, else secondary. Fill `accent` at 0.22 (selected) / 0.14 (complete) / 0.08. Stroke `accent` at 1.0, 1.5 pt (selected) or 0.35, 0.5 pt. Count text green if complete else secondary. Whole chip at 0.5 opacity when owned == 0 (and disabled). | `CV:964-988` |
| Tooltip on chip | `dexRegionProgressLabel`: "Kanto 87/151" | `LOC:1469-1471`, `CV:1614` |
| Order and wrap | chips scroll horizontally, 4 pt spacing, no wrapping | `CV:1597-1620` |

The Unown and national tiers show **no progress** before they complete; only the medals appear, so there is no "27/28" display anywhere.

### 2.5 Notification copy (English, exact; `LOC:1481-1550`; title and body, `%s` = `regionName`)
`regionName` English: Kanto, Johto, Hoenn, Sinnoh, Unova (`LOC:1458-1466`).
| Badge | Title | Body |
|---|---|---|
| region | `🏅 %s Dex complete!` | `You've caught every Pokémon in %s!` |
| regionShiny | `✨ Shiny %s Dex complete!` | `You've caught a shiny of every Pokémon in %s!` |
| unown | `🔤 Unown collection complete!` | `You've collected all 28 Unown forms!` |
| national | `🌍 National Dex complete!` | `You've caught every Pokémon from Gen 1–5!` |
| nationalShiny | `✨ Shiny National Dex complete!` | `You've caught a shiny of every Pokémon!` |
Upstream uses a straight apostrophe in "You've"; the port uses a curly one (`viewModel.js:389-396`). Gettext msgids must match whichever is chosen; check against the .pot.
Tooltips (`dexBadgeLabel`, `LOC:1481-1503`), English: `%s Dex complete`, `Shiny %s Dex complete`, `Unown collection complete`, `National Dex complete`, `Shiny National Dex complete`.
Delivery: `notifyCompanionEvent(title, body)` (`CS:524`); it has no sound and no action.

### 2.6 How the port's `badge` event and helper relate
- `BadgeEvent {key, badge, region|null}` (`events.go:55`, `protocol.md:603`) is the exact analogue of `CS:518-527`; the extension builds the copy (`viewModel.js:385`). Channel: `companion-notifications` (`viewModel.js:416`).
- `state.companion.dexSummary.badges` (`protocol.md:526-532`) = `earnedDexBadges`, in display order. The header medals and the region medals derive from it and from `getDexPage.regions` (`protocol.md:638`, `{key, owned, shinyOwned, total, complete, shinyComplete}`).
- What the helper already computes: region owned and shinyOwned (`dex.go:120-143`), national and nationalShiny (inside `earnedBadges`, `dex.go:147-175`), Unown form count (`collectedForms(201)`, vs 28 letters), and `dexSummary.{ownedSpecies, shinySpecies, nationalTotal=649}` (`views.go:297-312`). It does **not** expose national owned count or Unown collected count as numbers; they exist upstream (`nationalDexProgress`, `CS:504`) but no upstream view uses them (see §2.3.5). Do not add them.

### 2.7 Pure logic (Go) vs UI (GJS)
- Go, done: earning rules, seeding, announce-once, persisted keys, `dexSummary.badges`.
- GJS, done: header strip, chip medal, notification text. Remaining UI work is cosmetic only (§2.9).

### 2.8 Swift tests to port (exact expected values)
`PokedexRegionTests.swift` (all pure; Go cases already mirror most, see `dex_test.go`):
- Boundaries: 1 and 151 -> kanto; 152, 251 -> johto; 252, 386 -> hoenn; 387, 493 -> sinnoh; 494, 649 -> unova (lines 9-20).
- nil for 0, -5, 650, 10000 (22-27).
- Totals 151, 100, 135, 107, 156; sum 649 (29-45); ranges contiguous, first lower 1, last upper 649 (47-54).
- Partial: owned (1,4,7 | 152 | 999 ignored) -> kanto.owned 3, total 151, not complete; johto.owned 1 (58-71).
- Duplicate ids (25 plain, 25 shiny, 26) -> kanto.owned 2 (73-77).
- Johto fully owned -> owned 100, complete true, shinyComplete false (79-85).
- Johto fully shiny except first -> complete true, shinyComplete false, shinyOwned 99 (87-94).
- Badges: none for (1,2) (98-101); complete Kanto -> `region.kanto` yes, `regionShiny.kanto` no, `national` no (103-109); shiny Kanto -> both (111-116);
  Unown 27 -> no, 28 -> yes (118-122); full 1...649 normal -> national yes, nationalShiny no, all 5 region badges (124-130);
  full shiny -> national and nationalShiny (132-137); Kanto only never gives national (140-145).
- newlyEarned: `[region.kanto, national]` with `[]` -> both; with `{region.kanto}` -> `[national]`; with both -> `[]` (149-165).
- Keys unique and exactly `region.kanto`, `regionShiny.kanto`, `unown`, `national`, `nationalShiny` (167-180).
`DexCompletionStoreTests.swift` (store-level, port via `Store` in `dex_test.go`):
- Empty store: no badges, `seeded` true, `notified` empty (34-42).
- Owned range 1...151 -> kanto 151, `region.kanto` earned (44-50); seeded silently, re-check changes nothing (52-62).
- Region filter: owned 1...200 -> johto 49, kanto 151, nil 200 (64-71); belongs to dex search/filter specs, not medals.
- National: owned 1...649 -> owned 649, `national` + 5 region badges (73-78).
- Unown: 27 letter entries + all forms of 351 -> no `unown`; adding the active 28th letter -> `unown`, johto.owned 1, hoenn.owned 1 (351 is Hoenn), national owned 2, `formSpecies(201)` owned count 28, `notified` contains `unown` (80-108).
- Legacy import: with or without Kanto pre-owned, `notified` is `{region.kanto}` or `{}`; seeded true and persisted even when empty; after hatching the last Johto species (251) `notified` gains `region.johto` in the same session; a re-check adds nothing (110-160).
Already in Go: `dex_test.go:80-185` (`TestEarnedBadges`, `TestBadgeAnnouncedOnceWhenEarned`, the 27 vs 28 Unown test). Diff the cases above against it and add any missing ones, notably the **duplicate ids**, **out-of-range** and **legacy import** cases (the last needs `applySave`; M2 has no import, so skip it and note why).
`GenerationDexScreenshotTests` and `SaveTransferTests:554-560`: visual fixture only and the import field list; not logic.
JS: `dexModel.test.js:360-368` and `viewModel.test.js:154` already cover medals and one notification; add the other four notification texts and the exact strings from §2.5.

### 2.9 Gaps to verify in the port (all small)
1. Chip fill, stroke and 0.5 opacity at owned 0 (§2.4) match upstream; the port uses "tiles with a progress line" (`stylesheet.css:631`), a deliberate deviation. Check `ui.md` for the recorded decision before changing it.
2. Header medals: yellow 0.14/0.5 style is in `stylesheet.css:621-628`; verify the tooltip strings match §2.5.
3. Docs: `protocol.md` §6 and `ui.md` §5.1 and §9 said LATER. **Done in phase 16**; the `badge` event row now says it is consumed. (`companion.md` has no LATER mention.)
4. Notification punctuation (curly vs straight apostrophe) and the `🏅`/`✨` emoji in titles are present; confirm the Shell notification does not drop emoji in the title.
5. Confirm `badge` notifications respect the same "companion-notifications" setting as `graduate` (`viewModel.js:416`).

## 3. Open questions (resolved; one needs a real-shell check)

Each answer is a Milestone 3 decision (`tasks/todo.md`, "Milestone 3 plan") unless it says otherwise.

1. **Tile and size.** Decided: keep the existing 34 px tile with a 30 px sprite inside. The tile is a port addition and
   stays (spec recommendation; nothing in the plan changes it).
2. **Is `St.Icon` nearest-neighbour at 30 px on GNOME 51?** **Not resolved: needs a screenshot on a real shell.** The
   plan: phase 19 renders it in the nested shell. If linear filtering shows, switch the tile to `St.Widget` with a
   `Clutter.Image`. The wire contract does not depend on the answer.
3. **Offline start.** Decided: the emoji stays and the resolver retries the next time the popover opens (upstream
   behavior; `sprites.js:44-47` forgets failed entries).
4. **Prefetch.** Decided: no. The helper does not prefetch `rare-candy` or `shiny-charm` (decision "no prefetch").
5. **Medal docs say LATER while the code exists.** Decided: the medals were built in M1 and are accepted as delivered.
   M3 adds the missing tests (§2.8 diff) and reconciles the docs. The strip layout stays as it is. Verified in phase 16
   against `helper/internal/companion/dex.go` (`earnedBadges` :147, `checkDexCompletions` :174,
   `seedDexCompletions` :193) and `extension/lib/dex/dexModel.js` (`regionMedal` :438, `headerMedals` :455),
   `extension/lib/viewModel.js` (`badgeText` :385), `extension/stylesheet.css` (`.ptb-dex-medal` :621).
6. **Progress readout for Unown and national.** Decided: none (upstream has none; the two labels are dead strings).
7. **Apostrophe in the notification msgids.** Decided: keep the curly apostrophe the port already ships
   (`viewModel.js:385-400`); the msgids follow the code. There is no `.pot` yet, so nothing else changes.

## 4. Recommended scope
| Sub-feature | Verdict | Reason |
|---|---|---|
| `getSprite {item}` + Go wiring | **Keep** | The store method and tests exist; about 20 lines in `cmdSprite`; additive and optional. |
| Item icons in Shop and Bag (rareCandy, shinyCharm) | **Keep** | Visible parity gain, same pattern as the egg icon. |
| Mint sprite | **Drop** | None exists upstream; emoji stays (`CM:190-191`). |
| Egg icon | **Done** | Already a sprite (`shopView.js:90`). Only verify the crop is applied. |
| Helper prefetch of item sprites | **Drop** | Not upstream; adds a startup network path. |
| Medal rules, seeding, `badge` event, header strip | **Done (M1)** | Verify; the docs are fixed (§2.9). |
| New progress UI for Unown or national | **Drop** | Not upstream; dead strings upstream. |
| Chip restyle to match upstream capsule | **Drop unless asked** | Port uses tiles by design; check the recorded decision. |

## 5. Work breakdown
| Task | Side | Files | Estimate |
|---|---|---|---|
| `item` arg in `cmdSprite`, `spriteWant.item`, `loadSprite` branch; daemon tests (ready, badArgs, unavailable, cache hit) | Go | `helper/internal/daemon/spritework.go`, `daemon_test.go`, maybe `sprites_test.go` | 0.5 d |
| Protocol doc: §8.1 item shape, §14 decision, remove the "no item request shape" comments | docs | `docs/protocol.md`, `docs/spec/bag-shop.md:43` | 0.25 d |
| `ITEM_SPRITE` table, `spriteRequestKey` item case, `iconTile` async upgrade, shop and bag wiring, filtering and fit check on the real shell | JS | `extension/lib/sprites.js`, `lib/shop/shopModel.js`, `shopWidgets.js`, `shopView.js`, `bagView.js`, `stylesheet.css`, tests `shopModel.test.js`, new `sprites.test.js` case | 1 d |
| Screenshot fixtures for Shop and Bag with sprites (stub helper serves item PNGs) | JS/test | `extension/test/stub-helper.py`, `make-sprite-fixture.js` | 0.5 d |
| Medal verification: diff `dex_test.go` against §2.8, add the missing cases; check the five notification strings and tooltips; doc reconciliation | Go/JS/docs | `dex_test.go`, `viewModel.test.js`, `dexModel.test.js`, `protocol.md:603`, `ui.md:499,634`, `companion.md` | 0.5 d |
Total about 2.75 days; the critical path is the St.Icon filtering check on a real shell (§3 Q2).
