# M3 spec: save export, import and snapshots

Source: upstream PokeTokenBar at 38e3e55 (v2.5.7). Target: the GNOME port (helper protocol 1, additive).
The citations below were read in this session.

**Status.** The open questions of the raw spec are resolved in §7 with the Milestone 3 decisions of `tasks/todo.md`
("Milestone 3 plan"). The contract that came out of it is in `docs/protocol.md` (§4, §7.7, §11.2, §14 decisions 44 to 51,
§15.1 for the D-Bus `Save1` interface). Where a sentence below still says "Accept?" or "open", §7 wins.

**Abbreviations**
- Upstream (Swift): `ST` = `Sources/PokeTokenBar/Core/SaveTransfer.swift`, `SS` = `Core/SaveSnapshots.swift`, `CS` = `Core/CompanionStore.swift`, `CM` = `Core/CompanionModel.swift`, `SV` = `UI/SettingsView.swift`, `LOC` = `Core/Localization.swift`, `LE` = `Core/LocalizationErrors.swift`, `STT` = `Tests/PokeTokenBarTests/SaveTransferTests.swift`, `SST` = `Tests/PokeTokenBarTests/SaveSnapshotTests.swift`, `DL` = `docs/reference/defect-log.md`.
- Port (Go and JS): `persist.go` = `helper/internal/companion/persist.go`, `state.go`, `store.go`, `bag.go` (same package), `commands.go`, `build.go`, `data.go`, `daemon.go` = `helper/internal/daemon/*`.

---

## 1. Export

### 1.1 Format
An **envelope** wraps the state. The state file is never exported bare (`ST:3-21`).

```json
{ "appVersion": "2.5.7", "exportedAt": "2026-08-03T12:00:00Z", "format": "poketokenbar.save",
  "schema": 2, "sourceDevice": "Jane's MacBook Pro", "state": { …CompanionState… } }
```

| Field | Rule | Cite |
|---|---|---|
| `format` | always `"poketokenbar.save"` | `ST:11` |
| `schema` | `2`. Schema 1 had no profiles; v2 "adds persistent generated Pokémon profiles". | `ST:12-13` |
| `appVersion` | `CFBundleShortVersionString`, or `"—"` | `SV:60-62` |
| `exportedAt` | `clock()` at export, ISO-8601 | `CS:2013-2015`, `ST:109` |
| `sourceDevice` | `Host.current().localizedName ?? hostName` | `SV:55-57` |
| `state` | the whole `CompanionState` | `ST:20` |

- **Why the envelope exists.** `CompanionState` decodes leniently, so any JSON object decodes as an empty state. The envelope keys are not lenient, so a foreign file is rejected instead of wiping the dex (`ST:5-9`).
- **Encoding.**
  - `JSONEncoder` with `.prettyPrinted, .sortedKeys` and `.iso8601` dates (`ST:106-110`).
  - Every `Date` inside `state` (`caughtAt`, `releasedAt`) becomes an ISO string. The live `companion-state.json` instead stores Apple reference-date doubles, through a default `JSONEncoder` (`CS:2309`).
- **No checksum, signature, compression or encryption.** Integrity rests on the format/schema header and on the sanitizer at import (§2.2).
- **Included:** every `CompanionState` field. The fields are enumerated in §4.1; the full list is pinned by `STT:551-571`.
- **Excluded:**
  - UserDefaults: `growthDifficulty` and `shopDifficulty`, deliberately kept out of the save (`CS:63-67`); notifications, animation and refresh settings.
  - Caches: PokéAPI, names, sprites.
  - The usage ledger and usage cache.
  - Snapshots and backups.
  - Limits and session keys.
- **Size:** about 4 KB typical (comment at `ST:107`); hundreds of KB with a full dex (`ST:68`).

### 1.2 File name and location
- **Suggested name:** `PokeTokenBar-Save-yyyy-MM-dd.json`, in local time, `en_US_POSIX` (`ST:76-79`, `ST:90-96`). The name comes from the same `clock()` as `exportedAt`, so the two cannot disagree across midnight (`CS:2008-2010`).
- **Picker:**
  - `NSSavePanel` with title "Export save", the suggested name, `.json` only and `canCreateDirectories` (`SV:1065-1071`).
  - The app activates first: it is an LSUIElement, so `activate(ignoringOtherApps:)` is required (`SV:1060-1063`).
- **Write:** `data.write(to:url, options:.atomic)`, then the Finder reveals the file (`SV:1074-1076`).
- **Failure:** an alert with `userFacingError` (`SV:1077-1080`, `LE:5-22`).

---

## 2. Import

### 2.1 Flow (`SV:1083-1138`)
1. `NSOpenPanel`: `.json` only, single file, no directories (`SV:1084-1091`).
2. `SaveTransfer.decode(Data(contentsOf:))`. Any error shows an alert built by `importErrorMessage` (`SV:1092-1099`).
3. **Confirmation.**
   - An `NSAlert` with `.warning` style, title `importConfirmTitle` and body `importConfirmBody(...)`, with the incoming and current summaries (`SV:1100-1113`).
   - A summary is `{dexCount = dex.count, lifetimeTokens = usedSinceInstall}` (`ST:33-41`).
   - The buttons are [Replace, Cancel]. **Return activates Cancel**; the destructive button is never the default (`ST:54-65`, `SV:1114-1122`).
4. `companion.applySave(envelope, todayTokensByProvider:, todayDate:, hasUsageData:)` (`SV:1124-1133`).
5. A success alert with `importSaveDone` (`SV:1134-1137`).

### 2.2 Validation: `SaveTransfer.decode` (`ST:113-132`)
Rules apply in this order:

| # | Check | Error |
|---|---|---|
| 1 | `data.count > 8 MiB` (8,388,608), checked before parsing (39 MB once froze the main thread for 1.8 s) | `fileTooLarge(bytes, limit)` (`ST:68-70`, `ST:114-116`) |
| 2 | The header `{format: String, schema: Int}` fails to decode, or `format != "poketokenbar.save"` | `notASaveFile` (`ST:120-123`) |
| 3 | `schema > 2`, read from the header only, so a future body that does not parse still reports this | `newerSchema(found, supported)` (`ST:124-126`, `ST:23-29`) |
| 4 | The full envelope does not decode. Six fields are required (`appVersion`, `exportedAt`, `sourceDevice` and `state` included); a bad ISO date also fails here. | `notASaveFile` ("same schema but unreadable = corrupt", `ST:127-129`) |
| 5 | `state = sanitized(state)` | none (`ST:130`) |

- **Migration of older versions.** `schema` 1 is accepted (≤ 2). It has no schema-specific branch. The missing profiles are generated by `migratePokemonProfilesIfNeeded()` inside `applySave` (`CS:2043`, `CS:2201`).
- **Legacy keys inside `state`:**
  - `unownForm`, `pendingUnownForm` and `representativeUnownForm` are read as forms (`CM:548`, `CM:629`, `CM:721`, `CM:737`).
  - The aggregate `claimedTodayTokens` is ignored, which leaves the ledger nil (`CM:723-731`).
  - Missing `dexCompletionsSeeded` defaults to false. It is re-seeded at import (`DL:58-72`, `CS:2044`).
- **`sanitized()`** (`ST:143-193`) is the trust boundary. It runs on load, import and snapshot restore.
  - Tokens are clamped to [0, 10^15] (`ST:74`, `ST:144-151`).
  - An `active` plus an egg guarantee drops `eggTier`, `pendingHatchID` and `eggPrayers` (`ST:156`).
  - The prayer count is clamped. `pendingForm` is resolved against `pendingHatchID`. An unsatisfiable `eggTier` is dropped (`ST:157-167`).
  - `totalForms` is clamped to 1…12; `stageIndex` is clamped; chains are deduped while keeping the current species (`ST:170-183`).
  - Profiles are sanitized (`ST:184`, `ST:189`). The representative is reconciled (`ST:191`).

### 2.3 Error messages (exact English, `LOC`)

| Case | Text | Cite |
|---|---|---|
| `notASaveFile` | `That isn't a PokeTokenBar save file.` | `LOC:1010-1019` |
| `newerSchema` | `This save was made by a newer version — update the app and try again.` | `LOC:1020-1029` |
| `fileTooLarge` | `That file is too large to be a save — it looks like the wrong file.` | `LOC:1042-1051` |
| `backupFailed` | `Import stopped because the current state couldn't be backed up — your progress is untouched. Check free disk space.` | `LOC:1052-1062` |
| no read/write permission | `File access was denied. Check permissions or choose another location.` | `LE:12-13` |
| out of space | `There is not enough disk space. Free up space and try again.` | `LE:14-15` |
| no such file | `The file could not be found. Check its location and select it again.` | `LE:16-17` |
| anything else | `The operation could not be completed. Please try again.` | `LE:21` |

The mapping is `importErrorMessage` (`LOC:1033-1041`). It lives outside the view so that it can be tested (`STT:740-755`).

### 2.4 Confirmation and result texts (exact English)
- **Title:** `Replace this Mac's progress?` (`LOC:777-779`).
- **Body** (`LOC:793-799`):
  ```
  Incoming save: {incomingDex} in Pokédex · {incomingTokens} lifetime
  Exported: {exportedAt} · {sourceDevice}
  This Mac now: {currentDex} in Pokédex · {currentTokens} lifetime

  This Mac's current progress is replaced. The previous state is kept as a backup in the state folder (last 5).
  ```
  - Tokens use `TokenFormatter.compact`.
  - `exportedAt` uses a medium date and short time in the app-language locale (`SV:1192-1198`).
  - The date and device are shown so that a stale save with the same dex count is noticed (`LOC:780-782`).
- **Buttons:** `Replace` (`LOC:843`) and `Cancel`.
- **Done:** `Imported — {dex} in Pokédex · {tokens} lifetime` (`LOC:844-853`).
- **Labels:**
  - Section: `Backup & Transfer` (`LOC:752`).
  - Export row: `Export save` with the hint `Saves your Pokédex, lifetime tokens, Bag, and current Pokémon as one file`, and the button `Export…` (`LOC:753-764`).
  - Import row: `Import save` with the hint `Pick a file exported from another Mac and continue here`, and the button `Import…` (`LOC:765-776`).

### 2.5 What happens to the current save: `applySave` (`CS:2020-2049`)
1. **Pre-import backup first, and mandatory** (`CS:2055-2067`).
   - The current state is written to `<stateDir>/companion-state.pre-import-yyyy-MM-dd-HHmmss.json` (local time).
   - It is bare `CompanionState` with the default encoder, so dates are doubles. It is *not* an envelope, so upstream's own Import rejects it (`STT:162-166`). Undoing an import is a manual file copy.
   - A new file is written per import, and the newest 5 survive, pruned by lexicographic sort (`ST:81-88`, `CS:2070-2077`).
   - If the write fails, the import **throws `backupFailed` and changes nothing** (`CS:2017-2019`, `STT:624-647`).
2. `state = rebasedForThisDevice(...)` (`ST:195-239`). Every field falls into one of four classes (`ST:197-210`, `STT:556-563`):
   - **Progress (copied):**
     - `usedSinceInstall`, `spentTokens`, `eggUsage`, `eggTier`, `pendingHatchID`, `pendingForm`, `eggPrayers`;
     - `active`, `representativeSpeciesID`, `representativeForm`, `dex`, `collectedFinals`, `inventory`;
     - `notifiedDexCompletions`, `dexCompletionsSeeded`.
   - **Device ledger (re-seeded):** `installBaselineSet`, `claimedTodayTokensByProvider`, `lastDate`.
     - If `hasUsageData && !todayTokensByProvider.isEmpty`: baseline = true, ledger = this device's today map, `lastDate` = today. Usage before the import is never credited.
     - Otherwise: baseline = false, ledger = nil, `lastDate` = `""`. The baseline is deferred to the fresh-install path, so a stale or empty map is never stored as a seeded ledger (`ST:223-237`).
   - **Account ledger (merged):**
     - `candyGrantTier`: per-key max (`ST:241-244`).
     - `candyWindowEpoch`: if both sides granted, the later epoch; if one side granted, that side's epoch; if neither, the later epoch (`ST:246-268`).
     - `candyFeatureSeeded`: OR of both sides (`ST:222`).
   - **Device preference (current value kept):** `language` (`ST:217`).
3. **In-memory reset.**
   - `activeGeneration += 1`, which cancels async work started on the old subject.
   - The line is cleared, along with `prefetchedLineID`, the evolve/graduate/celebration fields, candy/mint feedback and the hatch-retry flag.
   - `displayState = active != nil ? .idle : .egg` (`CS:2027-2042`).
4. `migratePokemonProfilesIfNeeded()`, then `seedDexCompletionsIfNeeded()` (existing badges become history and do not notify, `DL:58-72`), then `save()` (`CS:2043-2045`).
5. The line reload is kicked off, along with profile preparation (`CS:2046-2047`).
- **No auto-snapshot is taken on import.** Only the pre-import backup protects the old state. `restoreSnapshot` additionally takes a safety snapshot (§3.3).
- **Post-import effects:**
  - No celebration or event is emitted.
  - The difficulty is *not* changed and banked progress is *not* rescaled. A save carries raw `usedAtStage` and is read under the destination's difficulty, while the profile level is preserved (`ProfileGrowthIntegrationTests.swift:128-147`).
  - A disguised Ditto keeps its disguise (it is a progress field).

---

## 3. Snapshots

### 3.1 Storage
- **Directory:** `<stateDir>/.snapshots/companion-state.json/`, created on demand (`SS:28-35`).
- **File name:** `companion-snapshot-yyyy-MM-dd-HHmmss.json` in local time. On a clash, `-1`, `-2` and so on (`SS:37-47`, `SS:141-151`).
- **Content:** the same envelope as an export (`SS:152`, `ST:99-111`). A snapshot file is therefore importable through Import.
- **Retention:** the newest **10**, by `exportedAt` and then by id (`SS:17`, `SS:167-174`). Only files that list successfully are counted and pruned, so a corrupt file is never deleted (`SS:69-97`).

### 3.2 When snapshots are taken
- **Automatic:** at the end of every `update()` tick (`CS:996`), when:
  - there is no snapshot yet, or the newest is at least **12 h** old (`SS:18`, `CS:2104-2111`); and
  - the state is non-trivial: `usedSinceInstall > 0 || dex non-empty || active != nil` (`CS:2114`).
  - A failure is swallowed (`try?`, `CS:2116`).
- **Manual:** the "Create snapshot" button (`SV:518-523`, `SV:1140-1148`).
  - It always writes, without the interval check, and refreshes the list.
  - Success shows `Snapshot created` (`LOC:891-900`). Failure shows an alert with `userFacingError`.
- **Safety:** taken inside `restoreSnapshot`, failure ignored (`CS:2129`).
- **Recovery source:**
  - When the state file does not decode, it is moved to `.corrupt`, replacing any older one.
  - Then the newest valid snapshot is sanitized and saved (`CS:2278-2299`, `SS:176-185`).

### 3.3 Restore flow
1. **UI** (`SV:1150-1190`). An `NSAlert` with `.warning` style:
   - title `Restore this snapshot?` (`LOC:911-920`);
   - body (`LOC:930-936`):
     ```
     Target snapshot: {snapshotDex} in Pokédex · {snapshotTokens} lifetime
     Created: {snapshotDate}
     Current state: {currentDex} in Pokédex · {currentTokens} lifetime

     Your current state will be backed up as a new snapshot before restoring.
     ```
   - buttons [`Restore` (`LOC:901-910`), `Cancel`], with Cancel as the default (`SV:1162-1166`).
   - Done: `Restored — {dex} in Pokédex · {tokens} lifetime` (`LOC:980-989`). An error uses `importErrorMessage`.
2. **Store** (`CS:2119-2136`).
   1. **Load and validate the chosen file first** (`SS:106-125`). Then take the safety snapshot.
      - This order matters: at 10 files, the safety snapshot prunes the oldest, which may be the chosen one (`DL:1282-1289`).
      - An unreadable selection throws before anything is written or pruned.
   2. `applySave(...)` with this device's usage: the pre-import backup, the rebase and the reset of §2.5.
   3. Refresh the list.

### 3.4 Listing and UI
- **`listSnapshots`** (`SS:61-103`):
  - It decodes each file as an envelope through `SaveTransfer.decode`.
  - Otherwise it accepts a **bare legacy `CompanionState`**, dated by its file-name stamp, then the creation date, then now.
  - Files that do not decode are skipped.
  - The order is newest first.
- **Row model** `SaveSnapshot` (`SS:4-13`): `id` (the file name), `fileURL`, `date`, `dexCount`, `lifetimeTokens`, `currentSpeciesID?`, `currentIsShiny`, `currentForm?`.
  - The species comes from `active.currentID`.
  - Shiny uses **`displaysShiny`**, so a disguised Ditto is not spoiled (`SST:128-156`, `CM:484`).
  - The form is species-resolved (`SS:78-82`, `DL:106-117`).
- **Settings section** "Backup & Transfer" (`SV:493-516`):
  - an export row and an import row;
  - a "Create snapshot" row with the hint `Saves an instant local restore point of your progress (keeps up to 10)` (`LOC:881-890`);
  - a disclosure button `Automatic Backups (Snapshots)` (`LOC:856-865`). It is collapsed on every appear (`SV:89-91`), with the accessibility value `Expanded`/`Collapsed` (`LOC:876-880`, `SV:1213-1250`).
  - Expanded, it shows rows with a 28 px sprite (an egg when there is no species), a date (`exportedAtText`), the line `Pokédex {dex} · {tokens} lifetime` (`LOC:1000-1009`) and a small `Restore` button (`SV:549-578`).
  - Empty: `No snapshots saved yet` (`LOC:990-999`).

---

## 4. macOS ↔ GNOME compatibility

### 4.1 Field by field (upstream `CompanionState` `CM:652-704` vs port `state` `state.go:87-115`)

| Upstream key | Port key / handling | Import class (port) | Notes |
|---|---|---|---|
| `installBaselineSet` | same | device ledger | re-seeded |
| `usedSinceInstall`, `spentTokens`, `eggUsage` | same | progress | clamped by `sanitize` (`state.go:463-466`) |
| `eggTier`, `pendingHatchID`, `pendingForm`, `eggPrayers` | same | progress | **legacy `pendingUnownForm` is not read** (`state.go:245`) |
| `claimedTodayTokensByProvider` | `Claimed`, written only when non-nil (`state.go:145-155`, `:247-249`) | device ledger | re-seeded |
| `lastDate` | same | device ledger | re-seeded |
| `active` (MonState) | same keys (`state.go:26-42`) | progress | **legacy `unownForm` is not read** (`state.go:331`); see Ditto below |
| `representativeSpeciesID`, `representativeForm` | same | progress | **legacy `representativeUnownForm` is not read** (`state.go:261`) |
| `dex` (DexEntry) | same keys (`state.go:70-85`), decoded entry by entry | progress | `caughtAt` and `releasedAt` are `time.Time`; `names` uses int keys as strings |
| `collectedFinals` | a set, `"base:final"` on both sides (`CS:1128`, `store.go:397`) | progress | |
| `language` | same string | device preference | the port sets it from GSettings (`store.go:164-171`) |
| `inventory` | same; item keys `rareCandy`, `mint`, `shinyCharm` (`shop.go:14-16`) | progress | |
| `candyGrantTier`, `candyWindowEpoch` | same; window keys `claude.fiveHour` and `claude.sevenDay` on both sides (`UsageStore.swift:834-840`, `limits/status.go:100`) | account ledger (merge) | macOS multi-account prefixes come along inertly |
| `candyFeatureSeeded` | same | account ledger (OR) | |
| `notifiedDexCompletions`, `dexCompletionsSeeded` | same | progress | seed after import |
| — | `schemaVersion` (port only) | **port format: keep current** | ignored by Swift |
| — | `growthDifficulty` (port only; upstream keeps it in UserDefaults) | **device preference: keep current** | see hazard 2 |
| profile `seed` (UInt64) | `uint64` (`state.go:419`) | progress | never passes through JS (§5.2) |
| profile shape (`PokemonProfile.swift:57-103`) | `profile.go:23-51`: same keys; the optional `gender`, `abilitySlot` and `abilityName` are `omitempty`; gender `male`/`female`/`genderless` | progress | verified by reading both. Swift decodes `profile` with `try?` (`CM:543`), so a mismatch would silently regenerate it. |
| `nature`, `rarity` raw values (`CM:399-404`, `CM:60-61`) | identical lists (`balance.go:107-118`) | progress | an unknown nature makes Swift drop the whole `active` |

The envelope struct already exists in the port (`persist.go:108-115`). Snapshots use the same path, prefix, retention, 12 h interval and envelope (`persist.go:17-25`, `:183-234`).

### 4.2 macOS export → GNOME import
**Yes, with a mapping layer.** The envelope and key names match ("Key names match upstream so a later macOS-export import stays cheap", `state.go:12-15`). Envelope dates are ISO strings, which Go `time.Time` parses (`state.go:362`, `:377`).

The mapping needs these pieces:
1. **Rebase** (§2.5) and **keep the port-only fields**:
   - A macOS state has no `growthDifficulty`. Lenient decoding gives it 1.0 (`state.go:232`).
   - **Hazard 2.** If an import replaced `GrowthDifficulty`, the next `setSettings` would see GSettings ≠ store and **rescale the imported progress** (`commands.go:89-91`, `store.go:175-190`).
   - So `GrowthDifficulty`, `SchemaVersion` and `Language` are always copied from the current state.
2. **Legacy form-key aliases** in the import decode path: `unownForm`, `pendingUnownForm` and `representativeUnownForm`, for schema-1 or old schema-2 exports. Today the Unown letter is silently reset.
3. **Header-first validation, size cap and error kinds** (§2.2). The port's `readEnvelope` has no 8 MiB cap, no `newerSchema` versus `notASaveFile` distinction, and does not require the six fields (`persist.go:157-170`).
4. **Disguised Ditto (was a blocking caveat; resolved by `prayers-ditto.md` §2, shipped in the same milestone).**
   - Before the reveal is ported: An imported unrevealed Ditto at its threshold never evolves or graduates: the evolve loop breaks at `store.go:347-349`, and candy is blocked with `revealPending` (`bag.go:21-24`, protocol §5.4).
   - Usage keeps banking, so nothing is lost, but the companion is stuck until the reveal is ported.
   - Upstream rolls the disguise in shipping builds, since `dittoDisguiseRollingEnabled` defaults to `AppEnv.isBundledApp` (`CS:80`), at 1/128 for common lines with at least 2 forms (port mirror: `balance.go:155-157`), so real exports can contain one.
5. **Providers.** Rebase replaces the ledger with the port's providers, so macOS-only provider ids such as Codex disappear harmlessly.

### 4.3 GNOME export → macOS import
**Yes, if the port normalizes dates.**
- **Hazard 1 (likely; not verified, since Swift cannot run here):**
  - The port writes `caughtAt` and `releasedAt` from `time.Now()` with nanoseconds, a test pins this (`save_test.go:111-112`: `"2026-11-18T13:20:00.123456789Z"`), and possibly a local offset (`store.go:406-409`, `shop.go:252`).
  - Swift's `.iso8601` decoding strategy (`ST:118`) is the `.withInternetDateTime` formatter, which **rejects fractional seconds**.
  - `DexEntry.init` uses a throwing `decodeIfPresent(Date)` (`CM:617`, `CM:628`), and `Lossy` drops each failing entry (`CM:635-637`), so **every dex entry would vanish silently**.
  - **Mitigation (required):** the export encoder writes every date as UTC whole seconds with `Z` (`2006-01-02T15:04:05Z`), matching protocol §1.1. Add a Go test on the exported bytes, and verify once on macOS.
- **Port-only keys** `schemaVersion` and `growthDifficulty` are ignored by Swift's keyed decoding (`CM:711-748`).
- **The envelope needs all six fields.** `createSnapshot` already writes them (`persist.go:203-206`). Use `appVersion` = helper version and `sourceDevice` = hostname.
- **GNOME snapshots are valid macOS imports only once they are written with normalized dates.** `createSnapshot` embeds `encodeState` output (`persist.go:198-206`), so today's snapshots carry nanosecond dates.

---

## 5. Port design

**Supersedes an M1 decision.** "No import of macOS saves. Save field names stay identical to upstream so a later import stays cheap" (`SPEC.md:57`, `tasks/todo.md:24-25`). The rationale anticipated exactly this import of a macOS *export*. M3 lifts the exclusion for envelope exports only: a bare `companion-state.json` copied from macOS stays unsupported (Apple reference-date doubles, no envelope). Record this as a new protocol §14 decision and update the SPEC deviation line.

### 5.1 Helper (Go)
**New code in `companion`** (a new `transfer.go`, plus `persist.go` changes):
- `DecodeEnvelope(data) (Envelope, error)`, mirroring `ST:113-132`:
  - size cap 8,388,608 → `fileTooLarge`;
  - header `{format, schema}` → `notASaveFile` or `newerSchema`;
  - full decode with the six fields required and the ISO `exportedAt` → `notASaveFile`;
  - lenient `decodeState` plus the legacy-key aliases, then `sanitize()`.
- `encodeEnvelope(st, now, host)`:
  - pretty-printed and sorted (Go sorts map keys; order the struct fields alphabetically, as `persist.go:108-115` already does);
  - **all dates truncated to UTC seconds** (hazard 1).
  - `createSnapshot` reuses it.
- `rebase(imported, current, in UsageInput) state`:
  - mirrors `ST:211-239` and the merges at `ST:241-268`;
  - additionally keeps `GrowthDifficulty`, `SchemaVersion` and `Language` from `current`.
- `(*Store).ApplyImport(env, in) error`:
  1. Write the pre-import backup **as an envelope** (decided deviation, §7 Q2: it reuses `encodeEnvelope`, so it costs nothing and the Import button can undo an import). Use `writeFileAtomic` (`persist.go:27-55`), the name `companion-state.pre-import-<local stamp>.json`, and keep 5. On error, return `backupFailed` and change nothing.
  2. `s.st = rebase(...)`.
  3. Reset the in-memory fields: `line = nil`, `prefetchedLineID = 0`, `hatchDelayed = false`, `warmPending = true`, `window = levelUpWindow{}`, `lastSeen*` and `pending` (`store.go:47-67`).
  4. `ensureProfiles()`, `seedDexCompletions()`, `save()` (today these run only in `Open`, `store.go:138-139`).
- `ListSnapshots() []SnapshotSummary`: `id`, `date`, `dexCount`, `lifetimeTokens`, `speciesId` (int | null), `shiny` (`displaysShiny`) and `form` (string | null, species-resolved; protocol §1.1 Presence). `snapshotInfo` has no summary fields today (`persist.go:117-121`). There is no bare-state legacy fallback: the port never wrote bare snapshots.
- `CreateSnapshot()`: the manual path, with no interval check.
- `RestoreSnapshot(id, in)`:
  1. Validate that `id` matches `^companion-snapshot-[0-9-]+\.json$` and is in the list; else `notFound`.
  2. Read and decode it **first**.
  3. Take the safety snapshot (error ignored).
  4. `ApplyImport`.

**Hatch races (`STT:354-456`) are N/A.** The Store runs on the daemon's owner goroutine over a memory-only data view; misses become `wants` that a background warm fills before `companionUpdate` re-runs (`daemon.go:852-871`, `store.go:30-35`).
- Workers fill only caches; menu-sprite results are generation-checked (`spritework.go:20-23`).
- After import, also clear `replayRng.queued` (`data.go:366-390`), so draws requeued for the old egg are not replayed for the new one. This is harmless but cleaner.

**Daemon commands** (additive under protocol §2.7). All are served on the owner goroutine. The mutating ones (`importSave`, `createSnapshot`, `restoreSnapshot`) join the "apply in arrival order" list of protocol §4.

| `cmd` | `args` | Reply `data` | Timeout |
|---|---|---|---|
| `exportSave` | `path` (absolute string) | `{path, bytes, summary: {dexCount, lifetimeTokens}}` | 10 s |
| `inspectSave` | `path` | `{digest (sha256 hex), schema, appVersion, exportedAt (ts), sourceDevice, incoming: Summary, current: Summary}` | 10 s |
| `importSave` | `path`, `digest` | `{summary: Summary}`; the `state` comes first | 10 s |
| `listSnapshots` | none | `{snapshots: [SnapshotSummary]}`, newest first, at most 10 | 5 s |
| `createSnapshot` | none | `{snapshot: SnapshotSummary}` | 10 s |
| `restoreSnapshot` | `id` | `{summary: Summary}`; the `state` comes first | 10 s |

- **Single writer.** The flock on `helper.lock` (protocol §2.1) means only the running helper may write the state dir, so import and restore must go through it, never through a second process.
- **`exportSave`** writes outside the state dir. It does so atomically via a temp file plus rename in the target directory.
  - **Uncertain:** whether rename works on a document-portal FUSE path (`/run/user/<uid>/doc/…`). Fall back to a direct write if `CreateTemp` fails there. (A Gio.File without a local path is refused before this point, §7 Q6.)
- **`importSave`** re-reads the path, re-validates it, and compares the sha256 against `digest`; a mismatch gives `fileChanged`. This makes the two phases stateless and catches a file edited between preview and confirm.
- **Rebase input** is `d.scanned ? d.usageInput(now) : UsageInput{}`, as in `daemon.go:858-862`.
  - **Divergence:** `usageInput` drops zero-token providers (`build.go:228-237`), while upstream keeps zero entries (`UsageStore.swift:337-343`).
  - On a zero-usage day the port therefore takes the *defer* branch: the first non-zero observation becomes the baseline and is not credited. This matches the port's fresh-install behaviour (`store.go:242-250`). **Decided (§7 Q4): keep the port's drop-zeros behaviour.**
- **After `importSave` and `restoreSnapshot`:**
  - `emitState(true)`, then `reply`, then `warmPasses = 0` and `companionUpdate()`, which loads the line and prefetches;
  - re-sync the menu sprite;
  - **emit no events.**

**New `result` error codes** (§11.2):
- import: `notASaveFile`, `newerSchema`, `fileTooLarge`, `backupFailed`, `fileChanged`;
- restore: `notFound`;
- file I/O, for every command: `fileNotFound`, `permissionDenied`, `noSpace` (`ENOSPC`, `EDQUOT`) and `ioError`;
- `badArgs` for a relative path or a bad id.

Each message is English for the log; the UI maps the code.

### 5.2 Where the UI lives: prefs.js (an Adw page) through a D-Bus relay
- **Why prefs.** A shell-process extension cannot conveniently show a file chooser: it has no GTK, and `St` has no chooser. `prefs.js` runs in a separate GTK4 and libadwaita process (`prefs.js:10-14`), so `Gtk.FileDialog` and `Adw.AlertDialog` are available there.
- **The relay.** prefs cannot reach the helper: the helper's stdio belongs to the shell extension (`helperClient.js:119-150`), and a second helper is blocked by the flock. So the **extension exports a second D-Bus interface** beside `Agent1`, on the same bus name `io.github.poketokenbar.Agent`, owned in `enable()` (`agentBus.js:17-51`, `extension.js:74`).
  - **Interface:** `io.github.poketokenbar.Save1` at `/io/github/poketokenbar/Save`.
  - **Methods:** `ExportSave(s path) → s`, `InspectSave(s path) → s`, `ImportSave(s path, s digest) → s`, `ListSnapshots() → s`, `CreateSnapshot() → s`, `RestoreSnapshot(s id) → s`.
  - The `→ s` value is the reply `data` as a JSON string. A helper error becomes the D-Bus error `io.github.poketokenbar.Save1.Error.<code>` with its message.
  - GJS `wrapJSObject` with `<Method>Async(params, invocation)`; reply via `invocation.return_value` or `return_dbus_error`.
  - It is a **whitelist**, not a generic passthrough, so no other local process can drive shop commands.
  - Any process of the same user can call it. That crosses no privilege boundary: the same user can already rewrite `~/.local/state/poketokenbar`.
- **Paths only, never file contents.**
  - The profile `seed` is a uint64 above 2^53, and `JSON.parse` would corrupt it.
  - An 8 MiB file exceeds the 1 MiB line limit (protocol §1).
  - If `Gtk.FileDialog` returns a `Gio.File` with no local path (`get_path() === null`, e.g. MTP or SMB through GVfs), show `Choose a local file.` **Decided (§7 Q6): such targets are refused**, for export and import alike; there is no temp-file-and-copy path.
- **Extension disabled or screen locked.** The name has no owner, so prefs shows an `Adw.Banner`: `Turn on PokeTokenBar to back up or transfer your save.` All rows are insensitive except the banner.
  - A CLI fallback (`ptb-helper export` without the lock, because saves are atomic) is possible for export only. It is not recommended (Drop).
- **Page:** an `Adw.PreferencesPage` "Backup & Transfer" with these groups:
  1. **Transfer.**
     - An `Adw.ActionRow` "Export save" with its hint and an `Export…` button: `Gtk.FileDialog.save()`, `initial_name` = `PokeTokenBar-Save-<local yyyy-MM-dd>.json`, a `*.json` filter.
     - An `Adw.ActionRow` "Import save" with an `Import…` button: `Gtk.FileDialog.open()`, a `*.json` filter.
  2. **Snapshots.**
     - An "Create snapshot" row with its hint.
     - An `Adw.ExpanderRow` "Automatic Backups (Snapshots)", collapsed on each page map.
     - Its child rows have the title = date (`Intl.DateTimeFormat`, medium date, short time), the subtitle = `Pokédex {dex} · {compact(tokens)} lifetime`, and a `Restore` suffix button. The list is refetched on map and after every action.
- **Dialogs and feedback:**
  - `Adw.AlertDialog` with responses `cancel` and `replace`/`restore`.
  - `default_response = close_response = 'cancel'`, and `replace` uses `Adw.ResponseAppearance.DESTRUCTIVE` (ports `ImportConfirmPolicy`).
  - Results are shown as `Adw.Toast`s on the prefs window. Errors use a toast, or an `Adw.AlertDialog` for import/restore failures.
- **Shared code.** `lib/saveTransferModel.js` is pure (no `gi://`, like `format.js:11` and `i18n.js:3`). It holds the file-name builder, the error-code → message map, the confirm-body builder (reusing `compact()` from `lib/format.js:37`), and the row model. Unit-test it on the host.
- **Wording (decided, §7 Q3).** Replace "Mac" with "computer", e.g. `Replace this computer's progress?` and `Pick a file exported from another computer (macOS or GNOME) and continue here`. Also change `update the app` to `update the extension`, and replace "state folder" with `~/.local/state/poketokenbar` in the body.

### 5.3 Atomic writes and the lock
- Every write uses `writeFileAtomic`: temp file in the same directory, fsync, rename, then fsync of the directory (`persist.go:27-55`).
  - It covers the save, the snapshots, the pre-import backup and the export (except for the portal fallback).
- All mutation happens on the owner goroutine of the lock holder. No extra mutex is needed (`store.go:30-35`).
- **Gap (resolved in phase 16):** protocol §11.3 said `meta.errors[] saveWrite` covers snapshot failures, but `autoSnapshotIfNeeded` only logs them (`persist.go:231-233`). The protocol text is corrected instead of wiring it: `saveWrite` covers the companion save only; an automatic snapshot failure is logged, a manual one returns the error from `createSnapshot`. (This is the one resolution here that is not in the plan's decision list.)
- The import order is: backup, then swap the state in memory, then `save()`. A crash after the backup leaves the old save intact; a crash after `save()` leaves a complete new save.

---

## 6. Tests to port (exact values)

**Envelope and validation** (`STT`)
- `:99-113` round trip.
  - Input: `usedSinceInstall` 8,000,000,000; `spentTokens` 3,500,000,000; `inventory` `{rareCandy: 2}`; 1 dex entry; `collectedFinals` `["1-3"]`; representative 2; `sourceDevice` "Old Mac".
  - Expect the same values after `encode` and `decode`.
- `:138-151`: `hasGrowthBoost` true and `usedAtStage` 12,000,000 survive; `phaseThreshold` is 62,500,000.
- `:155-167`: `{"some":"other app","dex":123}` gives `notASaveFile`. A bare state also gives `notASaveFile`.
- `:173-189`: a valid envelope whose `format` is `someotherapp.save` gives `notASaveFile`. This is the B-only branch; the struct still decodes.
- `:191-203`: schema 3 gives `newerSchema(3, 2)`.
- `:727-733`: `{"format":"poketokenbar.save","schema":99,"whatever":{"unknown":true}}` gives `newerSchema(99, 2)`.
- `:717-723`: 8,388,609 zero bytes give `fileTooLarge(8388609, 8388608)`.
- `:757-763`: Unix time 1,785,758,400 gives a name that starts `PokeTokenBar-Save-`, ends `.json` and contains `2026-08-03`. Port this in JS.
- **New in the port:**
  - The exported bytes contain no `.` fraction in any date, and the dates end in `Z`.
  - A macOS-shaped envelope fixture, with ISO dates, `unownForm` and no `growthDifficulty`, imports with the letter kept and the difficulty unchanged.

**Rebase** (`STT`; the today key is `2026-08-03`; the provider key is `"test"`)
- `:212-245`.
  - Control (raw load): an update to 8,000,000 credits **0**.
  - Rebased import with today 5,000,000: `claimed[test]` = 5,000,000, `lastDate` = today, baseline true. A later update to 8,000,000 credits **3,000,000**.
- `:249-270`: `hasUsageData` false gives baseline false. The first update at 40,000,000 credits 0 and sets `claimed` to 40,000,000. The next at 41,000,000 credits 1,000,000.
- `:275-301`: an empty map with `hasUsageData` true gives baseline false, ledger nil and `lastDate` `""`. Then the same 40M/41M sequence (+1,000,000).
- `:305-326`: progress is kept (8e9, 3.5e9, dex 1, `rareCandy` 2, representative 2); `candyGrantTier["five_hour|2026-08-03T00:00:00Z"]` = 100; `candyFeatureSeeded` true.
- `:575-594`: current `en` stays `en` when the save is `ja`.
- `:598-618`: current `{A:100, B:80}` and save `{B:50, C:100}` give A 100, B 80, C 100.
- `:551-571`: field classification. The Go version reflects over the json tags of `state` and adds `schemaVersion` and `growthDifficulty` (keep-current) plus the `json:"-"` trio.
- `PremiumEggTests.swift:377-384`: `eggTier` `uncommon` survives the rebase.
- **New:**
  - `mergedWindowEpochs`, all four branches (both granted gives the max epoch; one side; neither gives the max). Upstream has no test for it; take the expectations from `ST:248-268`.
  - The difficulty is kept: current 0.5, imported 2.0 or absent, gives 0.5. Then `setSettings(0.5)` does not rescale.

**Backup and apply** (`STT`)
- `:329-350`: the backup holds `usedSinceInstall` 123,456,789; the new state holds 8,000,000,000.
- `:624-647`: a directory squatting on the backup path gives `backupFailed`; the state keeps 123,456,789 and the dex stays empty.
- `:651-681`: two imports 60 s apart leave 2 backups, the oldest holding 111. The port additionally decodes them as envelopes.
- `:695-712`: with an active Pokémon the phase is not egg (`idle`); without one it is `egg`.
- `:460-483`: no usage data, active 403, baseline false; after an update the companion is still not shown as an egg.
- `:489-521`: `Int.max`/`Int.min` values give 10^15 and 0, `claimed` 0, `usedAtStage` 10^15, `totalForms` 12, `stageIndex` 0; then an update by 1,000 stays ≤ 10^15 + 1,000.
- `DexCompletionStoreTests.swift:110-160`: a legacy import without the completion keys ends with `dexCompletionsSeeded` true and persisted, `notified` = the pre-existing badges; hatching #251 then announces `region.johto` once.
- `ProfileGrowthIntegrationTests.swift:128-147`: source difficulty 0.1, level 52; destination difficulty 2. `instanceID`, IVs and level 52 are kept, and `tokensToNext` then graduates at level 100.
- `UnownTests.swift:504-528` and `:631-650`: forms `question` (shiny), `[a, exclamation]` and representative `exclamation` survive; a pending 201 with `exclamation` and `eggUsage` 1,234 survives.
- `PokemonProfileTests.swift:271-296`: a hostile profile is clamped to level 100, `growthTokens` 10^15, IVs `[31,0,31,31,0,31]`, 4 moves, names ≤ 80, move levels 0…100, a non-empty `instanceID`; level −99 becomes 5.
- `EggPrayerTests.swift:130-152`: with an active Pokémon, `eggPrayers` becomes nil; `Int.max` becomes 10^15; prayers survive the transfer.

**Snapshots** (`SST`; already ported: `:158-176` → `save_test.go:187`, `:178-197` → `:204`, `:199-225` → `:224`, `:312-320` → `:244`)
- `:40-59`: 12,500 tokens and 3 dex give a summary of dex 3, tokens 12,500, species 1, not shiny, form nil; the list holds 1 entry.
- `:61-82`: Shaymin 492 `sky`, shiny: the summary and the restored state keep `sky` and shiny.
- `:84-123`: form resolution — 492 sky → `sky`; missing → `land`; `future` → `land`; 201 `b` → `b`; 420 → nil; 25 → nil; egg → species nil.
- `:128-156`: a disguised Ditto (412, `trash`, shiny) lists `shiny = false` with form `trash`; once revealed (132), `shiny = true`.
- `:227-256`: restore rolls back to 10,000 tokens and 1 dex, and a snapshot with 90,000 and 4 exists.
- `:258-283`: with 10 snapshots, restoring the oldest (10,000) works. The result has tokens 10,000, dex 1, 10 snapshots, including the 99,000/12 safety snapshot.
- `:285-310`: an unreadable selection throws; the state stays 99,000/12; the remaining ids are the original set minus the selected one.

**UI policy** (JS, pure model)
- `STT:687-691`: Cancel is the default and close response.
- `STT:740-755`: every error code maps to a localized message; no raw code or "couldn't be completed" text leaks.

---

## 7. Open questions (all resolved)

Each answer is a Milestone 3 decision (`tasks/todo.md`, "Milestone 3 plan") unless it says otherwise.

1. **Disguised Ditto on import.** Decided: the roll and the reveal ship in M3 (`prayers-ditto.md`), so a disguise in
   an imported save resolves instead of stalling. Keep macOS import.
2. **Pre-import backup as an envelope.** Decided: yes, kept 5, and importable as an envelope. The plan's condition
   ("unless the spec shows it costs nothing") is met: the port already has `encodeEnvelope` and `writeFileAtomic`
   (the snapshot writer, `persist.go:183-206`), so writing an envelope instead of a bare state removes code rather than
   adding it, and the §4.3 date normalisation then applies to the backup for free. Upstream's bare file is **not**
   reproduced. File name `companion-state.pre-import-<local stamp>.json`, in the state dir (not the snapshots dir).
3. **Wording.** Decided: "computer" for "Mac" and "extension" for "app"; the body names
   `~/.local/state/poketokenbar` instead of "state folder".
4. **Zero-usage-day rebase.** Decided: keep the port's drop-zeros `usageInput`. The spec shows a mechanism difference
   (upstream seeds `{provider: 0}`, the port defers the baseline to the first non-zero observation), but the
   user-visible effect is bounded: only the tokens seen by the first scan that observes usage after an import on a zero
   day are not credited, at most one scan interval, and it equals the port's fresh-install behaviour. It is not
   proven to matter, so the port keeps its behaviour. A test pins it.
5. **`sourceDevice`.** Decided: `os.Hostname()`. `appVersion` is the helper version.
6. **Non-local `Gio.File` targets.** Decided: refused. If `get_path()` is null, prefs shows `Choose a local file.` and
   nothing is sent. The helper requires an absolute path and answers `badArgs` otherwise.
7. **Snapshot rows.** Decided: text-only (date, `Pokédex {dex} · {tokens} lifetime`, Restore). No sprite thumbnails and
   no `getSprite` relay over D-Bus.

## 8. Recommended scope

| Item | Keep / Drop | Reason |
|---|---|---|
| Export, as envelope schema 2 with normalized dates | **Keep** | core value, cheap; makes GNOME → macOS work |
| Import with the header-first validation and the 4 error kinds | **Keep** | trust boundary; upstream defect history |
| Confirm dialog with summaries, Cancel as default | **Keep** | protects against data loss |
| Mandatory pre-import backup, keep 5 | **Keep** | promised by the dialog |
| Rebase, merges and keep-current device fields (with `growthDifficulty`) | **Keep** | a silent mis-credit or a rescale otherwise |
| macOS → GNOME legacy key aliases | **Keep** | otherwise the Unown letter is lost silently |
| Manual snapshot, list and restore | **Keep** | the files are already written by the port |
| Restore that reads before the safety snapshot | **Keep** | `DL:1282-1289` |
| Bare-state legacy snapshot listing (`SS:84-96`, `:111-123`) | **Drop** | the port never wrote bare snapshots |
| Snapshot sprite thumbnails | **Drop (M3)** | relay cost; text rows suffice (§7 Q7) |
| Hatch-race tests (`STT:354-456`) | **Drop** | N/A: synchronous Store, memory-only data view |
| CLI export fallback while the extension is off | **Drop** | extra surface; a banner suffices |
| Ditto reveal | **Decided (Q1): ships in M3** | gates a perfect macOS import |

## 9. Work breakdown (about 6 days)

| # | Area | Files | Work | Days |
|---|---|---|---|---|
| 1 | Go, companion | new `companion/transfer.go`; `persist.go`, `state.go` | envelope decode and encode, date normalization, legacy aliases, `rebase`, both merges, `ApplyImport`, pre-import backup and pruning, summaries, `CreateSnapshot`, `RestoreSnapshot` | 2.0 |
| 2 | Go, tests | new `companion/transfer_test.go`; `save_test.go` | §6 ports plus the new tests (fractional dates, difficulty, epoch merge, macOS fixture) | 1.0 |
| 3 | Go, daemon | `daemon/commands.go`, new `daemon/transfer.go`, `contract_test.go` | 6 commands, the error-code mapping (`errors.Is` on `fs.ErrNotExist`, `fs.ErrPermission`, `syscall.ENOSPC`), state emission, `replayRng` clearing | 0.75 |
| 4 | Protocol and docs | `docs/protocol.md` §4, §7.6, §11.2, §14; fixtures `extension/test/fixtures/{inspect-save,list-snapshots}.json`; stub-helper | additive contract, decisions | 0.5 |
| 5 | JS, extension | new `lib/saveBus.js` (`Save1` relay); `extension.js`; `helperClient.js` timeouts | D-Bus export, async replies, error mapping | 0.5 |
| 6 | JS, prefs | `prefs.js`; new `lib/saveTransferModel.js`, plus a test | Adw page, `Gtk.FileDialog`, `Adw.AlertDialog`, snapshot expander, toasts, the disabled-extension banner, strings | 1.25 |
