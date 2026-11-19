# M3 spec: Prayers and Ditto disguise reveal

Read-only extraction from upstream Swift (`/home/user/Documents/01_Projects/PokeTokenBar`) against the
GNOME port (`/home/user/Documents/01_Projects/PokeTokenBar-GNOME`, after M2).

**Citation legend.** Paths are under `Sources/PokeTokenBar/` unless they start with `Tests/`.

| Tag | File |
|---|---|
| CS | `Core/CompanionStore.swift` |
| CM | `Core/CompanionModel.swift` |
| PP | `Core/PokemonProfile.swift` |
| ST | `Core/SaveTransfer.swift` |
| L10N | `Core/Localization.swift` |
| CV | `UI/CompanionView.swift` |
| FPP | `UI/FloatingPetPanel.swift` |
| T/… | `Tests/PokeTokenBarTests/…` |
| DL | `docs/reference/defect-log.md` |

## 0. Corrections to the brief

1. **The popover card has no prayer interaction.** `pray()` has a single caller, the floating pet's
   click router (`FPP:292-293`). The popover touches prayers only in the Pokédex individual card,
   which shows "Born after N prayers" (`CV:1947-1953`). Praying is a floating-pet feature only.
2. **`revealPending` is a `bag.candyBlocked` value, not an event.** The port has no reveal event, so the
   reveal needs a new event kind (§2.6).
3. **The disguise species is not chosen separately.** It is the normally rolled line (pending or fresh
   roll), flagged by one extra draw after the nature (`CS:1787-1791`). That draw is consumed on
   **every** hatch while the flag is on, for any rarity, because the argument is evaluated before the
   predicate.
4. **The protocol's `revealPending` sentence is true only for today's port.** The port never sets
   `DittoDisguiseEnabled` (`helper/internal/daemon/daemon.go:177-178`), so today only a copied macOS
   save can hold a disguise. Upstream it is a real transient state for every genuine Ditto: the #132
   line fetch is async and can fail and retry (`CS:1179-1182`, `CS:1835-1836`). Once M3 enables the roll,
   it is live whenever line 132 is missing from the helper's memory data view. Protocol §5.4, §7.4 and
   §11.2 must change (§2.7).
5. **A known, deliberately deferred limitation that M3 removes.** A disguised Ditto stops at its first
   threshold: `applyUsage` breaks there (`helper/internal/companion/store.go:347-349`), the bag shows
   `revealPending`, and usage keeps banking. `store.go:23-25`, protocol §5.4 and `hatch_test.go:279-291`
   all document this on purpose.
   - Today it is reachable only through a save that came from the macOS app. Save import is not ported
     (`SPEC.md` "Not yet ported" #8), so whether a macOS save can actually reach the port is
     **unverified**.

---

## 1. Prayers

### 1.1 Mechanics (pure logic)

- **A placebo.** `pray()` only counts. It never reads or draws the RNG, and it changes no roll: species,
  rarity, shiny, nature, form or profile (`CS:1404-1412`, `CM:456-460`). README.md:60 says the same to
  users.
- **Guard.** It is a no-op while a Pokémon is active, with no error (`CS:1407`).
- **Effect.** `eggPrayers = (eggPrayers ?? {count:0})`, then `count += 1`, then `save()`. The save runs on
  every click (`CS:1408-1411`).
- **No limits and no cooldown on counting.** Only the usage refresh the UI triggers is throttled (§1.4).
- **Lifecycle** (egg-scoped, like `eggUsage`):

| Event | Effect | Source |
|---|---|---|
| Hatch | `active.prayers = eggPrayers`, then `eggPrayers = nil` (before the shiny draw) | `CS:1778-1779`, `CS:1803` |
| Graduation | `DexEntry.prayers = active.prayers`; `eggPrayers` reset | `CS:1137`, `CS:1148` |
| Release (`buyEgg`) | the released entry keeps `prayers`; the new egg starts at nil | `CS:374`, `CS:1391` |
| Synthetic active dex entry | carries `active.prayers` | `CS:343` |
| Premium-egg rarity miss | the egg is kept and **prayers are kept** (no reset on that path) | `CS:1759-1767` |
| Sanitize (load, import, snapshot) | dropped if an `active` coexists; count clamped to `maxTokenValue` (1e15) | `ST:156-160` |
| Lenient decode | a corrupt record decodes as nil; unknown keys such as `wasFutile` are ignored | `CM:547`, `CM:622`, `CM:722` |

- **Interactions.** There is none with the Shiny Charm (it changes only the shiny modulus, `CS:1726-1736`),
  with the premium-egg guarantee or with the egg threshold. The prayer count is never read by a roll.

### 1.2 Save fields: already in the port

- `CompanionState.eggPrayers {count}`, `MonState.prayers`, `DexEntry.prayers`: Go
  `helper/internal/companion/state.go:22`, `:41`, `:81`, `:97`.
- They are decoded leniently (`state.go:246`, `:330`, `:372`) and sanitized (`state.go:471-474`).
- They are copied at hatch, graduation and release (`store.go:673-674`, `:410`, `:427`; `shop.go:230`,
  `:253`; `dex.go:240`).
- **No save-format work is needed.** What is missing is the `pray` command, the wire fields, the copy
  and the pet UI.

### 1.3 Copy (English, `L10N`)

| Key | English | Line |
|---|---|---|
| hover count, n = 1 | `1 prayer` | `L10N:1396` |
| hover count, n > 1 | `%d prayers` | `L10N:1397` |
| hover line | `<count> · <quip>`; `""` when n ≤ 0 | `L10N:1393-1399` |
| quip 0 (1–29) | just trying it out | `L10N:1403` |
| quip 1 (30–59) | heaven hasn't checked yet | `:1404` |
| quip 2 | the egg feels a little pressured | `:1405` |
| quip 3 | your praying form is getting pro | `:1406` |
| quip 4 | heaven's servers are lagging | `:1407` |
| quip 5 | you're #3,812 in heaven's queue | `:1408` |
| quip 6 | your palms are wearing thin | `:1409` |
| quip 7 | at this point the egg is praying with you | `:1410` |
| quip 8 | heaven: "received, we'll get back to you" | `:1411` |
| quip 9 | the egg just whispered "please stop" | `:1412` |
| quip 10 (≥ 300) | if a legendary hatches, that's pure skill | `:1413` |
| dex individual card, line 1 | `Born after %d prayers` (no singular form upstream) | `L10N:1379` |
| dex individual card, line 2 | `Your prayers reached the heavens. Probably.` (caption, secondary) | `L10N:1380` |
| hatch body: shiny and legendary | `%d prayers… heaven went all in. Shiny %s!` | `L10N:1419-1420` |
| hatch body: legendary | `%d prayers… it worked?! %s hatched!` | `:1422-1423` |
| hatch body: shiny | `%d prayers AND it's shiny?! %s hatched!` | `:1425-1426` |
| hatch body: common | `%d prayers later: %s. Heaven must be swamped today.` | `:1428-1429` |
| hatch body: uncommon or rare | `%d prayers later: %s. Let's call it half a miracle.` | `:1431` |

- **Quip index:** `prayers ≤ 0` gives nil, else `min(count / 30, 10)` (`L10N:1383-1390`).
- **Branch order** of the hatch body: shiny∧legendary, legendary, shiny, common, else (`L10N:1418-1432`).
- **Hatch notification** (`CS:1806-1813`):
  - title: `✨ Shiny Pokémon!` if `showShiny`, else `🥚 Hatched!` (`L10N:1373`, `:1375`);
  - body: the prayer body when `prayers.count > 0`, with `shiny = showShiny` (so a disguised Ditto
    shows false) and `rarity = line.rarity`;
  - the prayer body **replaces** the "(1 in N)" odds text.
- **The pet callout** shows the same body (`notifyCompanionEvent` records `latestEvent`, `CS:1542-1546`).
- **`name`** is `formDisplayName`, so a non-default form carries its label (`CS:1805-1806`).

### 1.4 Floating-pet UI (GJS)

**Click route** (`FPP:503-506`, `FPP:259-262`, `FPP:846-893`):
- A plain click first dismisses agent bubbles.
- Then: subject is an egg (`representativeSubject.speciesID == nil`, the port's `menuSubject.egg`) → pray;
  otherwise open the popover.
- The context-menu **Open** always opens, even for an egg, and does not pray (`FPP:509-513`,
  `T/FloatingPetAgentTests.swift:378-402`).
- A pinned representative over an incubating egg is not an egg subject, so the click opens the popover.

**Per prayer, in order** (`FPP:874-887`):
1. `pray()`;
2. `spawnEmoji()` (one particle and one wobble);
3. `didPray()` (rebuild the hover text and tooltip at once, `FPP:707-713`);
4. the throttled refresh check.

**Usage refresh throttle** (`FPP:264-290`, `FPP:878-887`), extension-side memory only:
- `milestone = max(0, count) / 30`.
- If the milestone dropped (a new egg), reset `lastRefreshedMilestone`.
- Refresh when `milestone > lastRefreshedMilestone` and (`lastRefreshAt` is nil or `now − lastRefreshAt ≥ 30 s`).
- On refresh, set `lastRefreshedMilestone = milestone` and `lastRefreshAt = now`.
- A milestone held back by the 30 s floor is not lost; the first click after the floor refreshes it.
- Upstream calls `store.refresh()`, the automatic path (`FPP:297-300`, `Core/UsageStore.swift:1095`).
  The port sends `refresh` with `manual: false`.

**Wobble** (`FPP:1044-1054`):
- One-shot `rotationEffect` keyframes, linear: −9° over 60 ms, +9° over 100 ms, −5° over 80 ms, 0° over
  60 ms. Total 300 ms. Pivot at the centre. One per click (trigger =
  `wobbleCount`). That a rapid click restarts it is *inferred* from SwiftUI keyframe-trigger semantics, not
  tested.

**🙏 particles** (`FPP:1104-1186`):
- **Spawn:** one per click, stacking without limit. `xFraction = random(0…1)` clamped to [0, 1]; a UI
  random, not the game RNG (`FPP:1121-1124`).
- **Lifetime:** removed after 1100 ms (`FPP:1115`).
- **Emoji box:** `e = clamp(petSize × 0.25, 14, 32)`; glyph font `e × 0.8` (`FPP:1140`, `FPP:1167-1168`).
- **Position** inside the pet's own frame, y-down, so the panel never grows:
  - `x_centre = e/2 + xFraction × (petSize − e)`;
  - `y` goes from `petSize × 0.7` to `e/2`;
  - `opacity = 1 − progress`;
  - ease-out over 1.0 s (`FPP:1164-1171`, `FPP:1184`).
- **Attachment:** both effects ride the agent bob modifier (`FPP:1074-1075`).
- **Colors:** none. It is the system emoji glyph.
- **Reduced motion:** upstream's `PrayerEmojiLayer` receives `animated` but ignores it, so particles and
  wobble always play (`FPP:1057`, `FPP:1134-1150`).

**Hover text** (`FPP:1086-1101`, `FPP:696-703`):
- `base [+ "\n" + eventLine] [+ "\n" + prayerHoverLine(n)]`.
- `n` = `eggPrayers.count` only when the subject is the egg, else 0.
- The test `T/PetEventLineTests.swift:59-67` fixes the order.

### 1.5 Pure logic vs UI split

**Go helper (protocol additions, additive under §2.7):**

| Item | Shape |
|---|---|
| command `pray` | args none. Reply `{count: int}`, the new `eggPrayers.count`. Mutating, so it joins the arrival-order list in §4. Saves before replying, and emits `state` first. |
| error `notEgg` (new) | `pray` while a Pokémon is active. Upstream is a silent no-op, but the reply needs a code. Do **not** reuse `noActive`, which means the inverse. The extension ignores it (a click racing a hatch). |
| `companion.egg.prayers` | int ≥ 0, always present (0 = none). Feeds the hover line. |
| `hatch.prayers` | int ≥ 0, always present. The extension picks the prayer body when > 0. |
| `Individual.prayers` (getSpecies §7.2) | int ≥ 0, the `DexEntry`/active `prayers.count`. Feeds the dex individual card. |

- **"Optional" in the brief** maps to §2.7's rule: these fields are additive and always present once
  emitted. Only command args are optional.
- **Timing differs from upstream.** `FPP:878` reads the count synchronously after `pray()`; in the port it
  arrives in the reply. The wobble and particle fire on click. The hover rebuild and the milestone check
  run on the reply's `count`.
- No state field for quips: the extension derives them from the count.
- The catch log does not show prayers (only `CV:1947` reads them), so `Entry` gains no field.

**GJS:**
- `petModel.clickAction`: remove the `PRAYERS_ENABLED` gate (`extension/lib/pet/petModel.js:37`, `:206-208`).
- `petController.petClicked`: add the pray route (`extension/lib/pet/petController.js:223-228`).
- Port the click router's milestone and floor logic as pure functions in `petModel.js`.
- `floatingPet.js`: wobble and particle actors.
- `petModel.hoverText`: add `prayers` (`petModel.js:256`).
- `viewModel.notificationText('hatch')`: the five prayer bodies (`extension/lib/viewModel.js:341-348`).
- `speciesDetail._individualCard`: two prayer lines after Level / Gender / Nature
  (`extension/lib/dex/speciesDetail.js:429-434`).
- gettext strings from §1.3, with `ngettext` for the count.

---

## 2. Ditto disguise reveal

### 2.1 Roll at hatch

- **Constants:**
  - `dittoDisguiseDenominator = 128`, `dittoSpeciesID = 132` (`CM:451-453`);
  - Ditto is excluded from the hatch pool and the REST fallback (`Core/PokeAPIClient.swift:219`,
    `Core/PokeAPIClient.swift:239`).
- **Draw order** (`CS:1782-1796`): [species] → shiny → nature → **disguise** → one draw per non-leaf plan
  node → profile seed → [form].
- **The disguise draw happens only when `dittoDisguiseRollingEnabled`** (`CS:80`, `CS:1788`). Upstream that
  is `AppEnv.isBundledApp`: on in the shipped app, off in tests unless passed.
- **Hit:** `rarity == common && line.totalForms ≥ 2 && roll % 128 == 0` (`CS:1721-1723`). `totalForms` is
  the tree depth (`CM:384`). So premium eggs (uncommon or rare floor) can never yield a disguise.
- **On a hit, `dittoDisguise = line.baseID`** (`CS:1790`). The individual is otherwise a normal hatch of
  that line: its plan, form, profile and growth boost all come from the disguise line (`CS:1792-1803`).
- **Shiny:** the raw `isShiny` is stored. `showShiny = isShiny && dittoDisguise == nil` (`CS:1798`), and
  `displaysShiny` (`CM:484`) is the single display predicate (DL:1015-1024).
- **The Shiny Charm** applies to the hidden draw as usual (48 instead of 64). The reveal copy names no
  odds, because charm ownership at hatch time is not saved (`L10N:1452`, `T/ShinyCharmTests.swift:63-69`,
  DL:236).
- **Go is already ported:** `balance.go:155-158`, the draw at `store.go:680-683`, the flag at
  `store.go:23-25`. Only the daemon flag is off.

### 2.1b Save fields

- **`MonState`** has `dittoDisguise: Int?` (nil = a normal Pokémon) and `dittoRevealed: Bool`
  (`CM:479-480`).
- **Absent keys** decode to nil and false, and then behave as a normal Pokémon with the shiny shown
  (`CM:545-546`, `T/DittoTests.swift:419-431`).
- **After the reveal**, `dittoDisguise` keeps the original disguise id (`CM:478` comment;
  `T/DittoTests.swift:147`).
- **`DexEntry` has no Ditto field.**
- **Go already carries both fields** (`state.go:39-40`, `state.go:324-328`), so the format does not change.

### 2.2 What every surface shows while disguised

Everything derives from `MonState` with `baseID = disguise`, so nothing is special-cased except shiny.

| Surface | While disguised | Source |
|---|---|---|
| Hatch notification and pet callout | disguise name, non-shiny title and body (the prayer body with `shiny:false`, `rarity: common`) | `CS:1798`, `CS:1806-1813`; `T/PetEventLineTests.swift:125-140` |
| Top-bar sprite and floating pet | disguise species and form, non-shiny (`representativeSubject` uses `currentIsShiny`) | `CS:187`, `CS:221-224` |
| Home card | disguise sprite, name, `COMMON` badge, disguise types (details loaded by `currentSpeciesID`), disguise evo line, no ✨ | `CV:704-760`, `CV:830-835` |
| Growth bar and thresholds | the disguise line's stage 0, boost included | `CS:1016`, `CS:1229-1238` |
| Profile | enriched with the disguise's gender, ability and moves | `CS:1794` |
| Dex and catch log | the active entry is the disguise species, **counted as owned**, shiny hidden (`hasNormal` true) | `CS:326-345`, `CM:794-805`, `CS:570-575`; `T/CompanionTests.swift:619-639` |
| Dex badges | the disguise species counts toward regional completion. `checkDexCompletions` runs on every save and persists the badge key, so a disguise **can permanently award a badge** for a species that disappears at reveal. This is upstream behaviour. | `CS:494-525`, `CS:2308` |
| Release (`buyEgg`) | a `DexEntry` of the disguise species with `isShiny = displaysShiny` (false). Releasing is not a reveal route. | `CS:355-375`; `T/CompanionTests.swift:765-779`; DL:1008 |
| High-value release warning | not shown (`currentIsShiny` false, rarity common) | `CS:193-195`; `T/PreciousCompanionGuardTests.swift:75-86` |
| Snapshots (Settings) | `displaysShiny` | DL:1015-1024; `T/SaveSnapshotTests.swift:128-156` |
| Candy picker | it plans the **apparent** path, so it matches a real Pokémon of that line. Spend is capped at the reveal (§2.4). | `CS:1176-1186`, `CS:1229-1238` |

The port already mirrors the shiny rules: `dex.go:95`, `dex.go:239`, `shop.go:252`, `views.go:236`,
`state.go:536-548`.

### 2.3 Trigger

- **Only one trigger: growth reaching the disguise's first stage threshold.** It is not time, a click,
  a level, or a menu.
- **Primary** (`CS:1012-1019`): in `applyUsage`, with the line loaded, when
  `usedAtStage ≥ stageThreshold` and the node exists. It is checked **before** the leaf or graduate
  branch, so a disguise line pruned to a leaf reveals instead of graduating
  (`T/DittoTests.swift:249-276`). It kicks `revealDitto()` and breaks.
  - Sources of usage: real tokens, the hatch overflow (`CS:1817`) and candies.
- **Backup** (`CS:985-990`): on every `update()` tick, when disguised, unrevealed, the line loaded, not
  hatching or revealing, and at the threshold. It covers restarts and failed fetches.
- **Rescaling the difficulty never completes a stage** (companion.md §2.4), so it cannot trigger.

### 2.4 Reveal transaction (`CS:1826-1878`)

1. **Guards:** disguised and unrevealed, not already revealing, `usedAtStage ≥ threshold`. Capture
   `activeGeneration`.
2. **Fetch line 132.** On failure, log and retry next tick (`CS:1835-1836`). Re-check the generation and
   the guards after the await (`CS:1838-1841`).
3. **Read `disguiseName`** before the swap: `currentLine.localizedName(m.baseID)`. It is the **plain
   species name with no form label**; fallback `#<id>` (`CS:1842`).
4. **Compute the carry:** `carry = max(0, usedAtStage − threshold)`, with the threshold recomputed after
   the await (`CS:1840-1843`).
5. **Mutate** (`CS:1845-1858`):
   - `baseID = 132`;
   - `plannedPathIDs = makeEvolutionPlan(dittoTree)`. The tree is a leaf, so this costs **0 RNG draws**;
   - `pathIDs = [132]`, `stageIndex = 0`, `rarity` = the loaded Ditto line's rarity, which is rare
     (`CS:1827`, `CS:1873`, `T/DittoTests.swift:141`);
     `totalForms` = plan length (1);
   - `usedAtStage = carry`, `dittoRevealed = true`, `form = nil`;
   - **kept:** `isShiny`, `nature`, `dittoDisguise` (the original disguise id), `prayers` and the profile
     identity;
   - `hasGrowthBoost` is recomputed for base 132 (`collectedFinals` has a `132:*` entry; DL:289-306);
   - `profile.rebaseForSpeciesIdentity(previousRarity → rare)` (`PP:128-140`):
     - `fraction = min(1, growth / graduationTotal(old))`;
     - `growth = floor(fraction × graduationTotal(new))`;
     - clear gender, abilitySlot, abilityName, abilityIsHidden and moves;
     - `advanceGrowth` (monotonic, level never drops, `PP:183-188`);
   - enrich from Ditto details if they are cached, else load them after the reveal (`CS:1859-1877`).
6. **Representative:** `reconcileRepresentativeSelection()`. A pin on the disguise species is cleared
   when that species is no longer owned (`CS:1862`, `CM:811-822`). Then `currentLine = dittoLine`.
7. **Effects:**
   - `fireCelebration(.dittoReveal(shiny))`;
   - `displayState = .levelUp`, a **5 s** window (`CS:1867-1869`);
   - notify (§2.5);
   - `save()`;
   - `applyUsage(0)` re-evaluates the carry. The rare threshold (3B × difficulty, 1.5B boosted) can
     graduate Ditto at once (`CS:1872-1873`).
8. **Graduation later** writes a normal `DexEntry` for base 132 with the raw `isShiny`. `DexEntry` has no
   Ditto field, so the disguise history is lost after graduation (`CS:1123-1154`, `CM:553-607`).
9. **Candy** (`CS:1232-1238`):
   - with the reveal ahead, `consumed = min(consumed, ceil((threshold − used) / 100M))`; only the last
     consumed candy's excess carries;
   - while the threshold is met but the reveal has not landed, the stage costs are `[]`, so
     `maxRareCandyUseCount = 0` and `useRareCandy` returns `.unavailable` (`CS:1179-1182`);
   - the candy call that reaches the threshold returns `.progressed`, because the reveal is async.
   - The Go port already has this (`bag.go:21-49`, `bag.go:138-140`).

### 2.5 Reveal copy and animation

| Key | English | Line |
|---|---|---|
| title | `🎭 Huh? It's Ditto!` | `L10N:1449` |
| body | `You thought it was %s — it was Ditto all along!` | `L10N:1450` |
| shiny title | `🎭✨ Huh? A shiny Ditto!` | `L10N:1451` |
| shiny body | `You thought it was %s — it was a shiny Ditto!` | `L10N:1453` |

- `%s` is the plain disguise name.
- "Ditto" is literal in English; other languages hardcode the localized species name (Métamorph, 메타몽,
  メタモン). It is not the PokéAPI name.
- The pet callout shows the body (`CS:1870-1871`; `T/PetEventLineTests.swift:142-153`).

**Card animation** (`CV:866-875`), on top of the common flash:
- **Flash:** a white 0.85 overlay fading over 0.8 s ease-out; scale 0.6 → 1 with a spring
  (response 0.5, damping 0.55) (`CV:854-859`).
- **🎭 burst:**
  - a 26 pt emoji, top-centre, offset y −12;
  - scale and fade in with a spring (0.4, 0.5) after a 0.25 s delay (`CV:718-723`).
- **Shiny ✨:** 22 pt, top-trailing, offset (6, −6), spring after a 0.45 s delay (`CV:711-716`).
- **Both** fade out over 0.5 s ease-out after 2.6 s.
- The flash covers the sprite swap from the disguise to Ditto (`CV:865`).
- **No particles on the pet;** only the callout.

### 2.6 Pure logic vs UI split

**Go helper:**

- **`revealIfNeeded(ctx)`** in `store.go`:
  - **Where it is called:** in `Update` after `loadCurrentLine`, and again after a hatch with overflow.
    `applyUsage` keeps its `break` (`store.go:347-349`) and stays ctx-free.
  - **Why this is enough:** the daemon re-runs `Update` right after `useCandy`, `buyEgg`, `networkUp` and
    every warm (`helper/internal/daemon/shop.go:84-85`, `daemon.go:881-888`).
- **Line 132 fetch:** use `fetchLine(ctx, 132)` through the memory view (`daemon/data.go:120-123`). A miss
  records a `wantLine 132` and the warm chain re-runs `Update`, the same path as `loadCurrentLine`.
  - This miss path is *inferred* from the `dataCache` doc comment and the `wantLine` kind; the view's
    `Line` method itself was not read.
  - **Optional:** pre-warm line 132 whenever a disguise is active, so the reveal lands in the same pass.
    It is one extra cached line.
- **No generation guard and no `isRevealing` flag.** The Store is single-owner and synchronous
  (`store.go:31-35`). The upstream race test cannot be reached here (see §3.4).
- **No RNG:** the Ditto plan costs 0 draws, so `replayRng` is unaffected (`daemon/data.go:366-390`).
- **`profile.rebaseForSpeciesIdentity(old, new)`** in `profile.go`. It is missing in Go today: the only
  growth helpers are `advanceGrowth` and `reconstructedGrowthTokens` (`profile.go:186`, `:239`).
- **Enable the roll:** `DittoDisguiseEnabled: true` in `daemon.go:177`. Keep the `Options` default false
  so existing parity tests keep their draw counts (upstream tests do the same).
- **New event `dittoReveal`:**

  | Field | Value |
  |---|---|
  | `disguiseSpeciesId` | the original disguise id |
  | `disguiseDisplayName` | plain species name, no form label, `#id` fallback |
  | `speciesId` | 132 |
  | `displayName` | localized Ditto name (for logs; the copy does not use it) |
  | `rarity` | the loaded Ditto line's rarity (rare) |
  | `isShiny` | the raw `isShiny`, now displayed |

  - It carries no `shinyOdds`.
  - It is emitted after the `state`, like the other events. An immediate graduation from the carry adds
    a `graduate` after it.
- **`levelUpUntil`:** the window is 5 s. Document "4, 4, 6 or 5 s (reveal)".
- **`active` gains no disguise field.** Leaking the disguise is the bug class in DL:1015.
- **`useCandy`:** the call that reaches the reveal threshold returns `result: progressed` (upstream
  parity). The reveal state and event arrive in the follow-up `companionUpdate`.

**GJS:**
- `viewModel.notificationText('dittoReveal')`: the §2.5 strings.
- `notificationGate`: `dittoReveal` → `companion-notifications` (`viewModel.js:407-420`).
- Add `dittoReveal` to `COMPANION_EVENTS` (`extension/lib/pet/floatingPet.js:46`), so the callout pops.
- Add `dittoReveal` to `CELEBRATED` (`extension/extension.js:37`) for the existing CSS flash
  (M2 decision 32).
- **Optional:** a 🎭 label burst on the card's `_fxLayer`, reusing `shop/feedbackFx.js`.
- The bag keeps showing `revealPending` like `lineLoading`; no new text.

### 2.7 Protocol text to change

- **§5.4 `candyBlocked`:** `revealPending` = "a disguised Ditto reached its reveal threshold and the
  reveal waits for line 132 (offline, or the same pass)". Drop the "imported or hand-edited only"
  wording.
- **§7.4 `useCandy.consumed`:** drop "which M2 does not emit".
- **§11.2 `notReady`:** keep it, and add `notEgg`.
- **§6:** add `dittoReveal`, plus `hatch.prayers`.
- **§5.4:** `egg.prayers`, and the `levelUpUntil` 5 s value.
- **§7.2:** `Individual.prayers`.
- **§4:** the `pray` row, which is mutating.
- **§14 decision 24:** replace with "an egg click prays (`pray`); the context menu Open opens".
- **New decisions:**
  - the Ditto roll is on in the helper;
  - `notEgg` is a new error code;
  - the reveal is synchronous in `Update`, so `useCandy` returns `progressed`.

---

## 3. Swift tests to port (exact values)

**RNG.** Test RNG = SplitMix64 (`T/CompanionTests.swift:63-73`, Go `rng.go:13-24`); every roll is
`next % n`. I recomputed the seeds independently (Python SplitMix64):

| Seed | Draws (entry point `hatch(baseID)`, no species draw) | Used by |
|---|---|---|
| **239** | first seed in 0…<100,000 whose 3rd draw is `% 128 == 0` (disguise hit, not shiny) | `T/DittoTests.swift:284-289`; Go `hatch_test.go:280-282` finds the same |
| **2466** | shiny (draw 1 `% 64 == 0`), nature index 6 = `docile`, disguise hit. The first seed with shiny and disguise. | `T/PetEventLineTests.swift:114` |

Through `hatchIfNeeded` with no pending species, the disguise is the **4th** draw, so these seeds do not
apply.

### 3.1 Prayers (Go store and daemon)

| Swift | Expectation |
|---|---|
| `T/EggPrayerTests.swift:61-65` | 3× pray gives `eggPrayers == {count:3}` |
| `:67-71` | pray with an active Pokémon gives nil (Go: `notEgg`, state unchanged) |
| `:75-86` | `pending` ∈ {true, false}, seed 7, 50 prayers: the hatch signature (MonState minus `prayers` and `instanceID`, sorted keys) equals the unprayed one |
| `:90-96` | 2 prayers, then hatch: `active.prayers == {2}`, `eggPrayers` nil |
| `:98-108` | 1 prayer, hatch, `applyUsage(phaseThreshold(common, 3, s))` for s = 0, 1, 2: active nil, `dex[0].prayers.count == 1`, `eggPrayers` nil |
| `:110-116` | `buyFreshEgg`: `eggPrayers` nil; pray gives 1 |
| `:118-129` | active `prayers` 4: the synthetic active entry shows 4; after `buyFreshEgg`, `dex.last.prayers == 4` |
| `:131-142` | sanitize: active plus `eggPrayers` gives nil; `Int.max` clamps to 1e15 (Go already has this; verify it is covered) |
| `:146-187` | round-trip; legacy extra keys (`wasFutile`, …) decode to `{7}`; `"oops"` decodes to nil |
| `T/PetEventLineTests.swift:125-140` | seed 2466, ditto on, en: plain hatch body `Bulb hatched from the egg!`; with 2 prayers, the body is `2 prayers later: Bulb. Heaven must be swamped today.`, with no "Ditto" and no "shiny" (Go: `hatch.prayers == 2`, `isShiny false`, `rarity common`) |

### 3.2 Prayers (JS `node:test`)

| Swift | Expectation |
|---|---|
| `T/EggPrayerTests.swift:190-214` | the five bodies, en: `37 prayers… heaven went all in. Shiny Mew!`, `37 prayers… it worked?! Mew hatched!`, `37 prayers AND it's shiny?! Pikachu hatched!` (common), `37 prayers later: Pikachu. Let's call it half a miracle.` (uncommon and rare), `3 prayers later: Rattata. Heaven must be swamped today.` |
| `:230-238` | quip index: 0 and −1 give null; (1,0) (29,0) (30,1) (59,1) (60,2) (299,9) (300,10) (10000,10); count 11 |
| `:240-246` | 11 distinct non-empty quips |
| `:249-261` | en hover: `1 prayer · just trying it out`, `31 prayers · heaven hasn't checked yet`, `120 prayers · heaven's servers are lagging`, `180 prayers · your palms are wearing thin`, `300 prayers · if a legendary hatches, that's pure skill`; 0 gives `""` |
| `:263-272` | `Born after 37 prayers`; `Your prayers reached the heavens. Probably.` |
| `:275-278` | `clickAction(true) = pray`, `clickAction(false) = openPopover` |
| `:280-293` | burst: `xFraction` −3 clamps to 0 and 7 to 1; 3 particles and wobble 3; empty after 1100 + 300 ms; wobble stays 3 |
| `:295-305` | for petSize 48…384 step 8, the emoji fits inside the frame at `xFraction` 0 and 1 |
| `:307-313` | hover with prayers 3 = `plain + "\n" + "3 prayers · just trying it out"` (ko in Swift; use en) |
| `:328-335` | milestones `{0:0, 1:0, 29:0, 30:1, 59:1, 60:2, 89:2, 90:3, 120:4}`; a huge count does not throw |
| `:337-359` | refreshes at 29/30/59/60/89/90, with the clock advanced by 30 s between groups: 0, 1, 1, 2, 2, 3; emoji and didPray counts equal pray clicks |
| `:361-375` | 30 prayers (1 refresh); +5 s, 30 more (still 1); +30 s, 1 more (2); 1 more (still 2) |
| `:377-389` | 60 prayers (1); +60 s and the count reset to 0; 29 (1); 1 more (2) |
| `:391-402` | 29 prayers, then 50 hatched clicks: 50 opens, 29 prays, 0 refreshes; then 1 prayer gives 1 refresh |
| `:404-415` | `shouldRefresh(29,0,nil)` F; `(30,0,nil)` T; `(30,1,nil)` F; `(30,0,now,now)` F; `(30,0,now,now+30)` T; `(60,1,now,now+30)` T |
| `T/PetEventLineTests.swift:59-67` | `plain + "\nEVENT\n" + prayerLine(3)` (restore the full form of the M2 half-test) |
| `T/FloatingPetAgentTests.swift:378-415` | egg click: dismisses waiting and prays (count 1, opened 0); with working, click gives count 2 and working survives; context Open gives opened 1 and count stays 2; hatched click opens and `eggPrayers` stays nil |

### 3.3 Ditto (Go store)

The Swift `seedDisguise` fixture (`T/DittoTests.swift:104-117`):
- active `{baseID 1, pathIDs [1], common, totalForms 3, dittoDisguise 1}`;
- line 1 → 2 → 3, common; Ditto line `{132}`, rare;
- reveal threshold 125M at difficulty 1.0.

| Swift | Expectation |
|---|---|
| `T/DittoTests.swift:71-96` | the `dittoDisguiseHit` table (already in `balance_test.go:130-147`) |
| `:123-128` | shiny hidden while disguised (already in `hatch_test.go:294`) |
| `:130-149` | `applyUsage(300M)` with the line not loaded: banked 300M, still species 1. After `Update`: revealed, base 132, rare, totalForms 1, stage 0, `pathIDs` and `planned` `[132]`, used **175M**, `dittoDisguise` kept, reveal event `isShiny false` |
| `:151-171` | details: before, not genderless, `overgrow`, `["tackle"]`; after, `genderless`, `limber`, `["transform"]`, growth **500M**, level **20** |
| `:173-200` | difficulty 0.1, boosted: `applyUsage(7.25M)` (threshold 6.25M) gives used **1M**, boost false; instanceID, IVs and level kept; genderless, transform; then `applyUsage(tokensToNext)` gives dex level 100 |
| `:202-213` | boosted, `applyUsage(62.5M)`: revealed, boost false, used 0, `phaseThreshold` **3,000,000,000** |
| `:215-227` | `collectedFinals ["132:132"]`, 125M: boost **true**, `phaseThreshold` **1,500,000,000** |
| `:229-247` | shiny, pinned representative 1: subject `(1, shiny false)`; after the reveal the pin is cleared and the subject is `(132, shiny true)` |
| `:249-276` | pruned line `{206}` (982 pruned), `usedAtStage = 750M` (`phaseThreshold(common, 1, 0)`), shiny, timid, `planned [206, 982]`: reveals to 132, used 0, `dittoDisguise` 206, shiny, timid; no dex entry with `finalID` 206; no `"206:206"` |
| `:320-329` | shiny: hidden before, shown after; event `isShiny true` |
| `:331-337` | `applyUsage(100M)` (below 125M): not revealed, species 1 |
| `:339-374` | candy, used 100M, 5 candies: max equals the apparent normal Pokémon; plans (count, xp, evolves, graduates, carryover, discarded) equal for 1…5; `UseCandy(5)` consumes 1, leaves 4, `xp` 100M; a second `UseCandy` gives `notReady`. After `Update`: 132, used 75M, max 4; `UseCandy(2)` gives `progressed`, used 275M, stock 2 |
| `:376-403` | difficulty ∈ {0.1, 1.0, 2.0} × boosted ∈ {F, T}, 20 candies: preview `graduates` true; `consumed = ceil(thr / 100M)`; used `= consumed × 100M − thr`; xp `= consumed × 100M`; stock `20 − consumed`; dex empty |
| `:405-417` | used 25M, `UseCandy(5)`: 132, used 0, stock 4 |
| `:419-431` | a save without Ditto fields gives nil and false; shiny shown |
| `T/PokemonProfileTests.swift:193-210` | `generate(seed 7, "ditto")`, enrich, `applyGrowth(125M, common)`, rebase common → rare: id and IVs kept; gender, ability and moves cleared; `abilityIsHidden` false; growth 500M; level 20 |
| `T/PokemonFormTests.swift:274-296` | Burmy 412 `sandy` disguise, used 900M, difficulty 1: after `Update`, base 132 and `form` nil |
| `T/PetEventLineTests.swift:142-153` | seed 2466, `hatch(1)`, `applyUsage(130M)`: `dittoReveal.isShiny` true, `disguiseDisplayName` `Bulb` (en: `You thought it was Bulb — it was a shiny Ditto!`) |
| `T/ShinyCharmTests.swift:63-69` | JS: no reveal body contains `64` or `48` |
| `T/SaveSnapshotTests.swift:128-156` | snapshot summary uses `displaysShiny`. Port only if the Go snapshot summary is ever surfaced; today it is not (backup UI is LATER). |

### 3.4 Go tests that flip or are dropped

- `hatch_test.go:279-291` asserts "a disguise never evolves while the reveal is deferred". Rewrite it to
  expect a reveal once line 132 is served.
- `bag_test.go:493-496`: its comment says "the reveal itself is deferred". Keep the guards and update the
  comment.
- **Drop** `T/DittoTests.swift:278-318` (`DelayedDittoProvider` race). The Go Store is single-owner and
  synchronous, so a `buyEgg` cannot interleave with a reveal. That makes the trigger unreachable
  (CLAUDE.md protocol step 3).
- `T/DittoTests.swift:433-436` (REST excludes Ditto) is already in `edge_test.go:58-76`.
- Already ported to Go:

  | Swift | Go |
  |---|---|
  | `T/CompanionTests.swift:619` | `hatch_test.go:294` |
  | `T/CompanionTests.swift:765` | `shop_test.go:790` |
  | `T/CompanionTests.swift:1375` | `evolution_test.go:299` |
  | `T/PreciousCompanionGuardTests.swift:75` | `shop_test.go:890` |

---

## 4. Open questions

1. **Enable the disguise roll in production?** Upstream ships it on (bundled app). The recommendation is
   on, for parity; it also resolves any disguise a macOS-origin save may carry (unverified path, §0.5).
   Alternative: a hidden GSettings key.
2. **`pray` while active:** a new error `notEgg`, or a silent `{count: 0}` reply like upstream?
   Recommendation: `notEgg`, which the extension ignores.
3. **Save per prayer:** upstream saves on every click. Rapid clicking means an atomic write plus fsync
   per click in Go. Keep parity unless profiling shows cost; the fallback is a 1 s debounce flushed on
   shutdown.
4. **`Born after 1 prayers`:** upstream has no singular. Using `ngettext` is a harmless deviation.
5. **Reduced motion:** upstream ignores it for prayers. Does the port's pet honour
   `St.Settings.enable_animations` elsewhere? If it does, skip the wobble and particles but still count.
   Unverified.
6. **The 🎭 card burst:** CSS flash only, as decision 32 does for hatch and evolve, or also an emoji burst?
   Recommendation: flash plus a 🎭 label via `feedbackFx`.
7. **Badge from a disguised species** (§2.2): keep parity, or exclude the disguised active entry from
   completion checks? Recommendation: parity. It is rare (1/128 of common multi-form hatches) and the
   species is usually already owned.
8. **Pre-warm line 132** while a disguise is active (one extra cached line), so the reveal never waits a
   pass?

## 5. Recommended scope

| Sub-feature | Keep or drop | Reason |
|---|---|---|
| `pray` command, count, save, hatch copy | **Keep** | The save fields exist; small; visible in the README feature list |
| Egg-click route, wobble, 🙏 particles | **Keep** | The `clickAction` seam was left for this |
| Prayer hover line and 11 quips | **Keep** | Pure strings and one int |
| 30-prayer usage refresh throttle | **Keep** | Pure functions; it uses the existing `refresh` |
| Dex individual "Born after N prayers" | **Keep** | One int on `Individual`, two labels |
| Ditto roll enabled | **Keep** | The roll is ported; only the flag is off |
| Ditto reveal transaction and event | **Keep** | Needed once the roll is on; it also lifts the deferred stop (§0.5) |
| Profile rebase | **Keep** | Required for reveal level parity |
| Reveal notification and callout | **Keep** | Strings plus one event kind |
| 🎭 / ✨ card burst | **Keep, simplified** | The flash already exists; the emoji burst is optional polish |
| Upstream async race guard (`activeGeneration`, `isRevealingDitto`) | **Drop** | Unreachable in the synchronous Store |
| Snapshot-summary shiny rule | **Drop for now** | No snapshot UI in the port yet |
| Badge exclusion for a disguise | **Drop** | Keep upstream behaviour |

## 6. Work breakdown

| # | Side | Work | Files | Days |
|---|---|---|---|---|
| 1 | Go | `Store.Pray`, `notEgg`, `egg.prayers`, `hatch.prayers`, `Individual.prayers`; tests §3.1 | `companion/store.go`, `views.go`, `events.go`, `*_test.go` | 0.5 |
| 2 | Go | daemon `pray` command (mutating, state then reply) and contract test | `daemon/commands.go`, `daemon/shop.go` or a new `pray.go`, `contract_test.go` | 0.25 |
| 3 | Go | `rebaseForSpeciesIdentity`; `revealIfNeeded` in `Update`; `DittoRevealEvent`; 5 s window; flag on in the daemon; optional pre-warm of line 132; tests §3.3–3.4 | `companion/profile.go`, `store.go`, `events.go`, `daemon/daemon.go`, `daemon/data.go`, tests | 1.5 |
| 4 | Docs | protocol §4, §5.4, §6, §7.2, §7.4, §11.2, §14; `SPEC.md` "Not yet ported" items 2 and 3; CHANGELOG | `docs/protocol.md`, `SPEC.md`, `CHANGELOG.md` | 0.25 |
| 5 | JS | `petModel` (clickAction, milestone/floor, quips, hover), `petController` pray route and refresh, `floatingPet` wobble and particles; tests §3.2 | `lib/pet/petModel.js`, `petController.js`, `floatingPet.js`, `test/pet*.test.js` | 1.25 |
| 6 | JS | notifications: prayer bodies, `dittoReveal` title/body/gate, `COMPANION_EVENTS`, `CELEBRATED`; card 🎭 burst; dex prayer lines; `.pot` strings | `lib/viewModel.js`, `extension.js`, `lib/popover/companionCard.js`, `lib/dex/speciesDetail.js`, `stylesheet.css`, tests | 0.5 |
| 7 | Both | screenshots (pet prayer particles, reveal card), README | `docs/screenshots`, `README.md` | 0.25 |

**Total about 4.5 days.** Go items 1–3 and JS item 5 are independent once the wire shapes in §1.5 and
§2.6 are fixed. Item 6 depends on 1 and 3 for fixtures.
