# Helper protocol (protocol version 1)

Contract between the Go helper `ptb-helper` and the GNOME Shell 51 extension `poketokenbar@local`,
Milestone 1. Implementers: Go daemon (plan Phase 5), Go companion (Phase 3), GJS UI (Phases 6–7).

**Division of work.** The helper does all data work: usage scanning, official limits, the companion
game, PokéAPI and sprite decoding. It sends raw values. The extension renders. It formats numbers,
countdowns and relative times, translates UI strings, owns GSettings, posts notifications and
restarts the helper. Game and usage rules live in `docs/spec/{usage,companion}.md`; screens live in
`docs/spec/ui.md`. This document only fixes the wire contract.

## 1. Transport

| Item | Rule |
|---|---|
| Process | `<extension dir>/bin/ptb-helper serve`, spawned with `Gio.Subprocess`, flags `STDIN_PIPE \| STDOUT_PIPE`. |
| stdout | Helper → extension messages (§3). |
| stdin | Extension → helper commands (§4). |
| stderr | **Not piped.** Inherited from gnome-shell, so it lands in the user journal. Human log, lines prefixed `ptb-helper:`. Never parsed. Never contains token values, `Authorization` headers, credential-file contents or HTTP bodies. |
| Framing | One JSON object per line. UTF-8, no BOM. The terminator is `\n` alone, never `\r\n`. Encoders escape control characters, so a line never contains a raw newline. |
| Line limit | 1 MiB (1,048,576 bytes) excluding the terminator, in both directions. A receiver discards a longer line through the next `\n` and logs it. The helper never produces one, because lists are paged and `state` is budgeted (§3). |
| Priority | The helper sets its own nice value to 10 at startup. |

### 1.1 Value conventions

| Kind | Encoding |
|---|---|
| Names | Field names are lowerCamelCase. Enum values are lowerCamelCase strings, except ids owned by others: provider ids (`claude_code`), raw server values (`weekly_scoped`) and PokéAPI names (`storm-drain`). |
| Presence | Every documented field is always present. A missing value is `null`, never an absent key. Empty lists are `[]`. |
| Integers | JSON integers, 0 ≤ n ≤ 2^53−1. The helper clamps anything larger. The profile `seed` (uint64) is never sent. |
| Tokens | Integers. `tokens = input + output + cacheWrite + cacheRead`; cache tokens are always included. |
| Cost | `{usd, coverage}`. `usd` is a number rounded to 4 decimals, and 0 when coverage is `empty` or `unavailable`. `coverage` is the enum in §12. |
| Timestamps (`ts`) | RFC 3339 UTC with `Z`, whole seconds: `2026-11-18T13:20:00Z`. Sub-second server values are truncated. |
| Day keys | `yyyy-MM-dd` in the session's local time zone. Both processes inherit `TZ` from gnome-shell. |
| Percent | A number from 0 to 100. Utilization may exceed 100 if the server says so. Fractions (`pace`) run from 0 to 1. |
| Formatting | Never done by the helper. Compact numbers, `$` strings, "Stage 2/3", "Hatch 42%" and countdowns are all built by the extension. |

## 2. Lifecycle

```
extension                                helper
 spawn ────────────────────────────────▶ flock state dir (waits up to 5 s)
                                         load save + last-applied settings
       ◀──────────────────────────────── hello
       ◀──────────────────────────────── state  (fromCache: true)   always sent
 setSettings (first command) ──────────▶ first scan waits up to 2 s for it
       ◀──────────────────────────────── result
       ◀──────────────────────────────── state  (refreshing: true)
       ◀──────────────────────────────── state  (fromCache: false)  fresh
 …commands, results, states, events…
 close stdin ──────────────────────────▶ flush, exit 0 within 2 s
 force_exit() if still alive after 3 s
```

### 2.1 Helper startup

1. Set nice 10. Resolve the directories:
   - state: `$XDG_STATE_HOME/poketokenbar/`;
   - cache: `$XDG_CACHE_HOME/poketokenbar/`;
   - `PTB_STATE_DIR` overrides both.
2. **Lock.** Take `flock(LOCK_EX|LOCK_NB)` on `<stateDir>/helper.lock`, retrying every 100 ms for up to 5 s.
   - The wait is longer than the extension's 3 s kill deadline. The shell disables extensions on every
     screen lock, so a helper spawned on unlock can overlap one that is still exiting. The newcomer
     wins once the old one is gone.
   - The kernel releases a flock when its holder dies, even on SIGKILL, so no stale-lock cleanup exists.
     The pid written into the file is informational only.
   - On timeout: `fatal` `alreadyRunning`, exit 3.
3. Load the save and `<stateDir>/settings-applied.json`, the last applied forwarded settings
   (defaults if absent).
4. Write `hello`.
5. Write the initial `state` with `meta.fromCache: true` (§2.3). It is sent every time, even on first run.
6. **Wait for the first `setSettings`**, at most 2 s.
   - Commands that arrive meanwhile are served normally. A `refresh` joins the first cycle.
   - On timeout, the settings from step 3 are used.
   - Growth is rescaled only when a received `growthDifficulty` differs from the persisted
     last-applied value, so a restart never rescales from defaults.
7. Run the first refresh cycle, then the scheduler (usage.md §6.1: interval, local midnight,
   coalescing, 20 s empty retry).

### 2.2 Extension side

- Send nothing before `hello`, and check `hello.protocol === 1` (§2.6).
- Send `setSettings` with the full forwarded set (§9.2) as the first command. Resend it whenever a
  forwarded key changes, debounced by 300 ms.
- **Command ids:**
  - positive integers, starting at 1 for each spawned helper and incremented per command;
  - scoped to one helper process: when the process exits, every pending command fails locally with
    `helperExited`;
  - every command has a timeout (§4), after which it fails locally with `timeout`.
- Read stdout with `Gio.DataInputStream.read_line_async` and `JSON.parse` each line. Log and skip a
  line that fails to parse.
- While the helper is down, keep rendering the last `state`.

### 2.3 Initial (cached) state

After each fresh state, the helper persists that state's **inputs** (usage snapshot and limits
snapshot) to `<stateDir>/last-snapshot.json`. The write is atomic, throttled to once per 60 s, and
also done on exit. At step 5 it runs the normal state builder on those inputs, the loaded save and
the current time. As a result:

- **`meta`.**
  - `generatedAt` = now;
  - `fromCache: true` and `refreshing: false`; the next `state` sets `refreshing: true` when the first
    cycle starts;
  - `lastUpdated` = the persisted value, or `null` on first run.
- **Time-derived fields are recomputed at `generatedAt`:**
  - `meta.isStale`, limits `isStale`, and every window's `pace`, `paceDelta`, `paceTier` and `gaugeTier`;
  - `activeBlock` becomes `null` if its `end` ≤ now;
  - `forecast` becomes `null` if its `depletionAt` or the 5 h reset is ≤ now;
  - `isLimitWarning` and `mood` follow from these.
- **The today guard applies.**
  - Totals whose day key ≠ today become zero.
  - `recentDaily` and `monthDaily` are re-windowed to end today: missing days are zero, and days
    outside the window are dropped.
- `levelUpUntil`, `justEvolvedTo` and `justGraduated` are `null`; they live in memory only.
- `menuSubject.sprite` is re-validated on disk. If its manifest is gone, status is `loading` and
  `manifestPath` is `null`.
- **First run** (no snapshot): usage is all zero (`empty` coverage, zero-filled series), limits have
  `available: false` and `authState: "ok"`, and the companion comes from the save (a fresh egg).
  While `meta.lastUpdated` is `null`, the extension shows `—` in the top bar (ui.md §2.4).

`fromCache` stays `true` until this process completes its first refresh cycle.

### 2.4 When `state` is emitted

A `state` is emitted whenever any field other than `meta.generatedAt` would change:

- a refresh starts or ends;
- the companion changes (credit, hatch, evolve, graduate, candy);
- `setSettings` or `setRepresentative` is applied;
- `menuSubject.sprite` changes status;
- `levelUpUntil` expires;
- local midnight passes;
- the **60 s tick** (below) changes something.

**Time-derived fields** change with the clock alone: `pace`, `paceDelta`, the tiers, both `isStale`
flags, `activeBlock` and `forecast` expiry, and `mood` (sulk after 3 h idle). Every 60 s the helper
rebuilds the state from its in-memory inputs, without scanning. It emits the state if anything other
than `meta.generatedAt` differs. So these values are never more than 60 s old, even with
`refresh-interval` 0. The extension computes countdowns and relative times from its own clock.

States are coalesced to at most one per 250 ms (latest wins). Each one is a full snapshot that the
extension replaces wholesale.

**Ordering.**
- An `event` is written after the `state` that already reflects it. A `hatch` handler therefore
  finds the new Pokémon in the last state.
- A `result` is written after any `state` its command produced.
- Writing a `result` or an `event` first flushes any coalesced, pending `state`. Coalescing never
  delays a state past a message that depends on it.

**Back-pressure.** One writer goroutine owns stdout. If stdout backs up, unsent states are replaced
by the newest one. `hello`, `result`, `event` and `fatal` are never dropped or reordered.

### 2.5 Shutdown

- **Helper.**
  - Triggers: stdin EOF (the extension closed it, or gnome-shell died), or SIGTERM, SIGINT or SIGHUP.
  - It stops reading and cancels in-flight network work. Pending commands get no reply.
  - It flushes the save, the usage cache and `last-snapshot.json` with atomic renames, then exits 0
    within 2 s.
  - Because every write is atomic, a SIGKILL never corrupts a file.
- **Extension `disable()`.**
  - Close the stdin pipe and call `wait_async`.
  - If the process has not exited after 3 s, call `force_exit()`.
  - This 3 s reaper timer is the only source allowed to outlive `disable()`, and it references only
    the subprocess.

### 2.6 Crash, restart, version mismatch

| Situation | Extension action |
|---|---|
| The helper exits without being asked: a crash, a `fatal`, or any exit code | Restart after 1, 2, 4, 8, 16, 32, 60, 60… s. Reset the backoff to 1 s once a helper has stayed up 60 s. `alreadyRunning` is retried the same way, because the previous instance is usually still exiting. |
| No `hello` within 10 s of spawn | Treat it as a crash: `force_exit()`, then back off. |
| `hello.protocol !== 1` | Close stdin and do **not** restart. Show an error in the indicator's tooltip and footer ("helper protocol mismatch; reinstall the extension"). The helper ships inside the extension, so a mismatch means a broken install. |

Exit codes are 0 (normal), 2 (command-line usage), 3 (`alreadyRunning`), 4 (`stateDirUnavailable`)
and 70 (`internal`). The extension logs them and does not branch on them.

### 2.7 Compatibility within protocol 1

These changes are allowed without a version bump:
- new fields;
- new message `type`s and new event `kind`s;
- new enum values;
- new error codes;
- new optional command args.

Receivers ignore unknown fields, types and kinds. An unknown enum value takes the fallback listed in
§12. The helper ignores unknown command args. Any rename, removal, type change or change of meaning
bumps `protocol`.

## 3. Helper → extension messages

| `type` | Fields | When |
|---|---|---|
| `hello` | `protocol` (int, = 1), `helperVersion` (semver string), `pid` (int) | Once, as the first line after the lock is held |
| `state` | `meta`, `usage`, `limits`, `companion`, `settingsApplied` (§5) | §2.4 |
| `event` | `kind` plus kind-specific fields (§6) | Game, limit and auth transitions |
| `result` | `id` (int), `ok` (bool), plus `data` (object) if `ok`, or `error` `{code, message}` if not | Exactly once per command that carries an integer `id` |
| `fatal` | `code`, `message` (English, for the log) | Right before a non-zero exit. It may come before `hello`. |

```json
{"type":"hello","protocol":1,"helperVersion":"0.1.0","pid":48211}
{"type":"result","id":7,"ok":true,"data":{}}
{"type":"result","id":8,"ok":false,"error":{"code":"badArgs","message":"pageSize must be 1..64"}}
{"type":"fatal","code":"alreadyRunning","message":"another ptb-helper (pid 48190) holds /home/u/.local/state/poketokenbar/helper.lock"}
```

**Fatal codes:**
- `alreadyRunning`;
- `stateDirUnavailable`: the state dir cannot be created, locked or written;
- `internal`: a recovered panic.

**State budget.** A typical `state` is at most 8 KB compact. The `state-active.json` fixture is
6.3 KB on the wire, and a 31-day month adds about 0.9 KB. Dex lists, the catch log and species
details never appear in `state`; they are served on request (§7).

## 4. Commands (extension → helper)

The envelope is `{"id": <int>, "cmd": "<name>", "args": {…}}`, and `args` may be omitted when a
command takes none.

- Replies may arrive **out of order**; correlate them by `id`.
- The helper serves commands concurrently, except `setSettings` and `setRepresentative`, which apply in
  arrival order.

| `cmd` | `args` | Reply `data` | Reply sent | Ext. timeout |
|---|---|---|---|---|
| `ping` | none | `{}` | immediately | 5 s |
| `setSettings` | full forwarded set (§9.2) | the applied set, shaped like `settingsApplied` | after applying and persisting | 10 s |
| `refresh` | `manual` (bool, default `false`) | `{}` | when the refresh cycle it started or joined finishes; the fresh `state` comes first | 120 s |
| `wake` | none | `{}` | immediately; schedules a refresh and re-arms the midnight timer | 5 s |
| `networkUp` | none | `{}` | immediately; schedules a refresh and retries pending hatch, line and sprite work | 5 s |
| `setRepresentative` | `speciesId` (int 1–649 or `null`), `form` (string or `null`) | `{"representative": Representative \| null}` | after saving | 10 s |
| `getDexPage` | §7.1 | §7.1 | immediately (local data) | 10 s |
| `getSpecies` | `speciesId` (int 1–649) | §7.2 | once PokéAPI details load or fail (at most 25 s) | 30 s |
| `getCatchLog` | §7.3 | §7.3 | immediately | 10 s |
| `getSprite` | §8.1 | §8.1 | once the first download or decode attempt settles (at most 25 s) | 30 s |

- **`refresh`.**
  - `manual: true` bypasses the official-limits 429 backoff and the in-memory credential cache
    (usage.md §7.1).
  - The footer refresh button, the "Load official limits"/stale row and the auth-expired **Retry**
    button send `manual: true`. They show a spinner until the reply (ui.md §2.1 F, §2.2).
  - Coalescing: a refresh requested during a running cycle sets one pending rerun, and its reply comes
    after that rerun.
- **`setSettings`** is idempotent and may be resent at any time.
  - Growth is rescaled (companion.md §2.4) only when `growthDifficulty` differs from the persisted
    last-applied value.
  - A new `language` re-resolves all Pokémon names and emits a `state`.
- **`setRepresentative`.**
  - `speciesId: null` returns to following the companion currently being raised.
  - A species or form the player does not own fails with `notOwned`, and the previous choice stays
    (companion.md §5.5).
  - `form: null` means the default form.
- **`wake` and `networkUp`** relay system events the helper cannot see.
  - Send `wake` on logind `PrepareForSleep(false)`.
  - Send `networkUp` when NetworkManager connectivity reaches full (usage.md §6.1 triggers).
  - The helper runs its own local-midnight timer.

## 5. State schema

A `state` line is `{"type":"state","meta":…,"usage":…,"limits":…,"companion":…,"settingsApplied":…}`.
In the "Consumer" column, `§` refers to ui.md. "Helper logic" means the value feeds helper-side
rules and is sent for diagnostics.

### 5.1 `meta`

| Field | Type | Meaning | Consumer |
|---|---|---|---|
| `protocolVersion` | int | = 1 | sanity check |
| `helperVersion` | string | semver | diagnostics, prefs "About" |
| `generatedAt` | ts | when this state was built | base for "now" in pace and countdown math |
| `lastUpdated` | ts \| null | last successful usage refresh; `null` before the first one | top bar `—` (§1.1, §2.4), footer "Updated <relative>" (§2.2) |
| `isStale` | bool | `lastUpdated` is null, or older than 2 × interval (1800 s in manual mode); usage.md §6.5 | footer stale hint. The top bar is never dimmed (§1.4). |
| `fromCache` | bool | `true` until this process finishes its first refresh (§2.3) | optional "loading" hint |
| `refreshing` | bool | a refresh cycle is running | optional activity hint |
| `errors` | Error[] | ongoing conditions, `{code, message, providerId}` (§11.3) | footer orange triangle and its tooltip (§2.2, §2.4) |
| `providers` | Provider[] | enabled providers, in registry order | names, ordering, cost visibility |

A Provider is `{id, displayName, shortName, reportsCost}`:
- `claude_code`, `"Claude Code"`, `"Claude"`, `true` in M1;
- `displayName` labels the provider row (§2.1 C.6);
- `shortName` labels the top-bar limit item, "Claude 62%" (§1.1);
- cost is shown only if some provider has `reportsCost`.

### 5.2 `usage`

| Field | Type | Meaning | Consumer |
|---|---|---|---|
| `calendar.today` | day key | local today | chart readout and today's bar (§2.3) |
| `calendar.weekStart` | day key | first day of this week, per the forwarded `weekStart` | "This week" (§2.1 C.3) |
| `calendar.monthStart` | day key | 1st of this month | month chart window (§2.3) |
| `combined` | Aggregate | sum over all providers in `meta.providers` | top-bar tokens and cost (§1.1), right-click info row (§1.5), big number and period row (§2.1 C.1–3), daily chart (§2.3) |
| `providers` | object, provider id → ProviderUsage | one entry per enabled provider; `claude_code` is always present in M1 | provider row (§2.1 C.6); current-block and forecast rows (§2.1 F) |

**Totals** is the shape of `today`, `week` and `month`:

| Field | Type | Meaning |
|---|---|---|
| `tokens` | int | `input + output + cacheWrite + cacheRead` |
| `input`, `output`, `cacheWrite`, `cacheRead` | int | per bucket; feeds the provider row's "Input / Output / Cache write / Cache read" |
| `cost` | Cost | |

- `today` covers `calendar.today`, `week` covers weekStart…today, and `month` covers monthStart…today
  (both ends inclusive).
- With no usage today, `today` is all zero with `coverage: "empty"`, never `null`.

**Series** is the shape of `recentDaily` and `monthDaily`: parallel arrays with one index per local
day, stepping +1 calendar day from `start` (never +86,400 s, so DST is safe).

| Field | Type |
|---|---|
| `start` | day key of index 0 |
| `tokens` | int[] |
| `costUsd` | number[] (4 decimals) |
| `coverage` | Coverage[] |

- `recentDaily` has exactly **21** entries and ends today.
- `monthDaily` runs from `calendar.monthStart` to today, so its length is the day of the month.
- Missing days are `0` with `empty` coverage.
- Invariants:
  - the last entry equals `today.tokens`;
  - Σ `monthDaily.tokens` = `month.tokens`;
  - the last week-length entries sum to `week.tokens`.
- The extension takes the last 7, 14 or 21 days of `recentDaily`, or uses `monthDaily`, according to
  `daily-trend-window`. It computes the peak, axis labels, weekend ticks and the tooltip itself (§2.3).

**Aggregate** (`combined`) is `today`, `week`, `month` (Totals) and `recentDaily`, `monthDaily`
(Series), plus:

| Field | Type | Meaning | Consumer |
|---|---|---|---|
| `burnTier` | BurnTier | sum of every provider's `activeBlock.tokensPerMinute`: idle at ≤ 1,000, normal below 100,000, fast below 400,000, else blazing (usage.md §4) | helper logic (mood) |
| `streak` | `{days: int, includesToday: bool}` | consecutive active days (usage.md §6.7); `days` is 0 with no run | streak badge, shown when `days ≥ 2` (§2.1 C.3) |

**ProviderUsage** is `today`, `week`, `month`, `recentDaily` and `monthDaily` for that provider, plus:

| Field | Type | Meaning | Consumer |
|---|---|---|---|
| `activeBlock` | `{tokens: int, tokensPerMinute: number, start: ts, end: ts}` \| null | rolling 5 h block; `end` = `start` + 5 h (usage.md §4) | "Current 5h block" row: tokens, "Reset <relative> (<clock>)" (§2.1 F) |
| `forecast` | `{depletionAt: ts, beforeReset: bool}` \| null | 5 h depletion forecast (usage.md §4). `null` if the provider has no official limits or the preconditions fail. | forecast row: warning plus `HH:mm` when `beforeReset`, else the "won't reach" checkmark (§2.1 F) |

### 5.3 `limits`

| Field | Type | Meaning | Consumer |
|---|---|---|---|
| `isLimitWarning` | bool | some utilization ≥ crit, or a forecast with `beforeReset` (usage.md §7.3) | helper logic (mood `tired`) |
| `providers` | object, provider id → ProviderLimits | providers with an official-limits source; `claude_code` in M1 | limits section (§2.1 F) |

**ProviderLimits:**

| Field | Type | Meaning | Consumer |
|---|---|---|---|
| `available` | bool | data from a successful fetch is held, possibly stale | rows, versus the "Load official limits" row (§2.1 F, §2.4) |
| `updatedAt` | ts \| null | last successful fetch | stale badge "staleLimits · <relative>" |
| `isStale` | bool | `available`, and more than 15 min since `updatedAt` | orange stale badge |
| `authState` | AuthState | table below | which banner to show |
| `errorCode` | string \| null | message key of the last failure (§11.4); `null` after a success | text under the refresh row or banner |
| `httpStatus` | int \| null | HTTP status of the last failure, if it had one | message detail |
| `backoffUntil` | ts \| null | end of the 429 backoff | "retry later" hint |
| `plan` | string \| null | plan display string (usage.md §7.1, `planDisplay`) | plan line |
| `account` | string \| null | `"email · organization"`, or just the email (upstream `accountDisplay`) | account line |
| `headlineKey` | string \| null | the window narrow surfaces show: the 5 h window if present, else the 7 d one | top-bar limit item (§1.1). It is shown only if `show-limit-in-menu` is on and today's tokens for this provider are > 0. |
| `windows` | Window[] | display order: fiveHour, sevenDay, sevenDayOpus, sevenDaySonnet, then scoped in server order. A window with null utilization is omitted. | quota rows (§2.1 F) |

| AuthState | When | UI (§2.1 F) |
|---|---|---|
| `ok` | the last fetch succeeded, or none has been tried yet | rows, or the "Load official limits" row while `available` is false |
| `expired` | the token expired (`expiresAt` ≤ now + 60 s), or 401/403 persisted after the re-read retry | orange banner with Retry; rows at 50 % opacity |
| `missing` | no credentials file, or no `claudeAiOauth` object | "credentials file not found" hint, the Linux stand-in for the Keychain help |
| `error` | network, 429, other HTTP status, decode failure | refresh row with the error text |

**Window:**

| Field | Type | Meaning | Consumer |
|---|---|---|---|
| `key` | string | stable id: `claude.fiveHour`, `claude.sevenDay`, `claude.sevenDayOpus`, `claude.sevenDaySonnet`, or `claude.scoped.<kind>.<model>.<i>` (see below). It never includes `resets_at`. | row identity, `limitAlert` |
| `kind` | WindowKind | | row label (gettext) |
| `scopeKind` | string \| null | raw `limits[].kind` (`session`, `weekly_all`, `weekly_scoped`, …) for `scoped`, else `null` | scoped label |
| `label` | string \| null | server model display name (`scope.model.display_name`) for `scoped`, else `null` | scoped label, "Weekly <model>" |
| `utilization` | number | percent used, actual value (never flipped) | fill, percent text |
| `resetsAt` | ts \| null | | countdown "1h 10m (14:30)" |
| `notStarted` | bool | `utilization == 0` and `resetsAt` is null | idle hint `fiveHourNotStarted` |
| `pace` | number \| null | elapsed fraction of the window at `generatedAt`; `null` if outside 0…1 or the span is unknown | pace marker, tooltip line 2 |
| `paceDelta` | int \| null | `utilization − pace·100`, rounded half away from zero; `null` iff `pace` is `null`. Do not recompute it in JS: `Math.round(-2.5)` is −2. | tooltip "<tier> · <delta>" |
| `paceTier` | PaceTier \| null | `null` while pace is below 0.1 or unknown (PaceTier.swift rules, usage.md §7.3) | tooltip line 1 |
| `gaugeTier` | PaceTier | `paceTier` if set, else absolute: ≥ crit gives wayOver, ≥ warn gives over, else onPace | bar fill and percent color, top-bar limit coloring (§1.2, §6) |

- **Scoped key parts.**
  - `<kind>` is the raw kind, or `unknown`.
  - `<model>` is the model display name, or `any`.
  - `<i>` is the index in `limits[]`.
- **Spans.**
  - fiveHour is 5 h.
  - sevenDay, sevenDayOpus and sevenDaySonnet are 7 d.
  - A scoped window takes its span from `scopeKind`: `session` is 5 h, `weekly_*` is 7 d, and
    anything else gives a null `pace`.
- **Remaining mode.** The extension applies `limit-display-mode`. In `remaining` mode it shows
  `max(0, 100 − utilization)` and draws the marker at `1 − pace`; tiers never flip.

### 5.4 `companion`

| Field | Type | Meaning | Consumer |
|---|---|---|---|
| `phase` | `egg` \| `active` | | companion card layout (§2.1 A) |
| `mood` | Mood | companion.md §9 | status line (§2.1 A) |
| `egg` | Egg \| null | non-null iff `phase` is `egg` | egg card |
| `active` | Active \| null | non-null iff `phase` is `active` | hatched card |
| `growthPercent` | int 0–100 | `floor(used·100 / threshold)` of the egg or the current stage. It follows what is being raised, never the representative. | top-bar "Hatch 42%" / "Growth 42%" (§1.1) |
| `representative` | Representative \| null | pinned Pokédex choice; `null` follows the companion being raised | right-click and dex star, detail star (§3.3, §3.4) |
| `menuSubject` | MenuSubject | what the top bar draws | top-bar sprite (§1.3) |
| `inventory` | `{rareCandy: int}` | candies accrue in M1 but cannot be spent | informational (Bag is LATER) |
| `dexSummary` | DexSummary | | dex empty state (§2.4), Collection tab header and medals (§3.3) |
| `levelUpUntil` | ts \| null | end of the hatch, evolve or graduate window (4, 4 or 6 s) | celebration (§2.1 A) |
| `justEvolvedTo` | string \| null | display name of the new species during an evolve window | status line `statusEvolved(name)` |
| `justGraduated` | string \| null | display name during a graduate window | orange one-line message (§2.1 A) |

**Egg:**

| Field | Type | Meaning |
|---|---|---|
| `used` | int | tokens credited to the egg; 0 shows the first-run hint |
| `threshold` | int | effective hatch threshold, `round(5,000,000 × difficulty)` |
| `imminent` | bool | `used` ≥ 90 % of `threshold`: "About to hatch!" and the wobble |
| `hatchDelayed` | bool | the egg is ready, but the hatch waits for PokéAPI: orange warning |

**Active:**

| Field | Type | Meaning |
|---|---|---|
| `speciesId` | int | current species |
| `form` | string \| null | §5.5 |
| `displayName` | string | localized name, plus ` [<form label>]` for a non-default form (upstream `formDisplayName`); `#<id>` when no name is known |
| `rarity` | Rarity | the line's rarity |
| `nature` | Nature \| null | |
| `isShiny` | bool | `displaysShiny` (hidden while a Ditto is disguised) |
| `stageIndex` | int | 0-based |
| `totalForms` | int | k, the length of the planned path |
| `isFinalStage` | bool | the current node has no children: "Final form" and "to graduation" wording |
| `usedAtStage` | int | |
| `threshold` | int | effective threshold of the current stage (rarity, k, boost, difficulty; companion.md §2.3) |
| `level` | int 5–100 | profile level |
| `growthBoost` | bool | the repeat-line 2× boost: orange badge |
| `types` | `[{key, name}]` | PokéAPI types, `[]` until details load. `key` is one of the 18 type ids (colors in ui.md §6); `name` is localized. |
| `evoLine` | EvoNode[] | realized path, then the guaranteed linear prefix, then one mystery node if a branch follows (companion.md §4.2). `[]` while the line is not loaded. |

An **EvoNode** has these fields:
- `speciesId`: int, or `null` for the mystery node;
- `form`: string or `null`, resolved per species;
- `displayName`: string, or `null` for the mystery node;
- `reached`: bool;
- `current`: bool.

The extension requests evo-node and card sprites with `getSprite` (§8.1):
- `shiny` is `active.isShiny`;
- `back` is `menuSubject.back`, for the card only.

A **Representative** is `{speciesId: int, form: string|null, shiny: bool}`, where `shiny` means the
player owns that species shiny (`ownsShinySpecies`, companion.md §5.5).

**MenuSubject:**

| Field | Type | Meaning |
|---|---|---|
| `egg` | bool | draw the egg |
| `speciesId` | int \| null | `null` iff `egg` |
| `form` | string \| null | |
| `shiny` | bool | |
| `back` | bool | `facesBack`: an active Pokémon exists and (`alwaysFacesBack` or mood is `sulk`); never for an egg. The popover companion card uses it too. |
| `sprite` | `{key, manifestPath, status}` | the animated sprite the helper keeps ready for this subject (§8) |

- **Subject rule** (companion.md §5.5):
  - a pinned representative gives (pinned species, `representative.shiny`, pinned form);
  - otherwise an active Pokémon gives (current species, `active.isShiny`, active form);
  - otherwise the egg.
- **`sprite.status`:**
  - `ready`: the requested animation is on disk.
  - `fallback`: a substitute is on disk (static, plain or front), and the helper keeps retrying every
    2 s, then up to every 30 s. Draw it as a 2-frame bob (ui.md §1.3).
  - `loading`: nothing usable yet, and `manifestPath` is `null`. Draw the egg bob.

**DexSummary:**

| Field | Type | Meaning |
|---|---|---|
| `entries` | int | catch-log entries, the active Pokémon included; `0` shows the dex empty state |
| `ownedSpecies` | int | species owned, forms collapsed ("N species") |
| `shinySpecies` | int | species owned shiny |
| `nationalTotal` | int | 649 |
| `badges` | string[] | earned badge keys in display order: `region.<r>`, `regionShiny.<r>`, `unown`, `national`, `nationalShiny`. These keys are persisted and never renamed. |

### 5.5 Pokémon identifiers

- `speciesId`: national dex number, 1–649.
- `form`: a catalog form id (companion.md §4.4; upstream `PokemonForm`).
  - It is `null` iff the species has no catalogued forms.
  - Otherwise it is always the **resolved** form, the default included: `"west"` for Shellos.
  - Commands accept `null` to mean the default.
- `rarity` belongs to the line, so every species of a line carries its base species' rarity.
- Display names: §10.

### 5.6 `settingsApplied`

Same fields as the `setSettings` args (§9.2), after normalization: what the helper actually uses, for
example a clamped `growthDifficulty`. Before any `setSettings`, it holds the persisted last-applied
values.

## 6. Events

The envelope is `{"type":"event","kind":"<kind>", …}`.

- Each event is emitted after the `state` that reflects it (§2.4).
- Events are not persisted and not replayed.
- The extension gates notifications with its own settings (`companion-notifications`,
  `limit-notifications`).

| `kind` | Fields | Extension (ui.md §5) |
|---|---|---|
| `hatch` | `speciesId`, `form`, `displayName`, `rarity`, `isShiny`, `shinyOdds` (int; 64 in M1) | "Hatched!", or "Shiny Pokémon!" with odds taken from `shinyOdds`; celebration |
| `evolve` | `fromSpeciesId`, `fromDisplayName`, `speciesId`, `form`, `displayName`, `isShiny` | "Evolved!": `<from> evolved into <to>!`; celebration |
| `graduate` | `speciesId`, `form`, `displayName`, `rarity`, `isShiny`, `entryId` (catch-log id) | "Graduated!"; celebration |
| `candyGranted` | `item` (`rareCandy`), `count` (1 for session, 5 for weekly), `providerId`, `windowKey` (`claude.fiveHour` or `claude.sevenDay`), `windowKind` (`session` \| `weekly`) | M1: nothing, or a quiet notification (the Bag is LATER) |
| `badge` | `key`, `badge` (`region` \| `regionShiny` \| `unown` \| `national` \| `nationalShiny`), `region` (Region \| null) | M1: an optional notification (dex medals are LATER). The helper emits it once per key ever: it persists `notifiedDexCompletions`, and first-run seeding never emits. |
| `limitAlert` | `providerId`, `windowKey`, `windowKind` (WindowKind), `label` (Window.label), `tier` (`warn` \| `crit`), `utilization` | "Limit warning" or "Limit imminent", body `<window> at <percent>`. `crit` uses urgency HIGH or CRITICAL (CRITICAL pierces Do Not Disturb) and plays a sound; `warn` is silent. |
| `authExpired` | `providerId`, `errorCode` | Nothing required; the state banner covers it. Emitted when `authState` changes to `expired`. |
| `error` | `code`, `message` | Log to the journal only. These are one-shot background failures (PokéAPI unreachable, a save write failed). Ongoing conditions live in `meta.errors`. |

**`limitAlert` edge triggering** follows usage.md §7.3:
- per window `key`, tier 1 is ≥ warn and tier 2 is ≥ crit;
- an alert fires only when the tier rises;
- dropping below warn re-arms the key.

**Deviation from upstream:** the helper persists the per-key tier in the state dir, while upstream
keeps it in memory (usage.md §6.6). The helper restarts on every screen lock and would otherwise alert
again after each unlock. Keys missing from the latest successful fetch are pruned from that map.

## 7. Pokédex payloads

### 7.1 `getDexPage`

| Arg | Type | Default | Meaning |
|---|---|---|---|
| `page` | int ≥ 1 | 1 | clamped to `pageCount` |
| `pageSize` | int 1–64 | 16 | the 4 × 4 grid |
| `query` | string | `""` | a number, `#number` (partial match), or a name in any stored language, ignoring case and diacritics (companion.md §5.3) |
| `rarity` | Rarity \| null | null | |
| `region` | Region \| null | null | |
| `shinyOnly` | bool | false | |
| `sort` | `numberAsc` \| `numberDesc` \| `nameAsc` \| `nameDesc` \| `rarityDesc` | `numberAsc` | |
| `anchorSpeciesId` | int \| null | null | if set, returns the page holding that species and ignores `page`. It is used when coming back from the detail view (ui.md §3.1). |

| Data field | Type | Meaning | Consumer (ui.md §3.3) |
|---|---|---|---|
| `page` | int | the page actually served | pager `‹ 2 / 4 ›` |
| `pageSize`, `pageCount` | int | `pageCount` ≥ 1 | pager |
| `total` | int | species matching the filters; `0` gives "no results" | empty-search state |
| `cells` | Cell[] | this page, sorted | grid |
| `regions` | `[{key, owned, shinyOwned, total, complete, shinyComplete}]` | always the 5 regions in order, ignoring filters | region chips |
| `rarityCounts` | `{legendary, rare, uncommon, common}` | owned species per rarity, ignoring filters | rarity chips |

**Cell:**

| Field | Type | Meaning |
|---|---|---|
| `speciesId` | int | `#id` capsule |
| `displayName` | string | species name, without a form label |
| `rarity` | Rarity | capsule color |
| `isShiny` | bool | ever owned shiny: ✨ |
| `hasNormal` | bool | owned in normal colors |
| `isRaising` | bool | the current species of the active Pokémon: "RAISING" |
| `isRepresentative` | bool | ⭐ |
| `forms` | `{owned, total}` \| null | for species with forms: the label becomes "Name n/total", and a click opens the form picker |
| `sprite` | SpriteRequest | ready-to-send `getSprite` args. Static, default form, and shiny only when the species has no forms and was owned shiny (upstream cell rule). |
| `spriteKey` | string | the key of that request, so the extension can hit its texture cache without a round trip |

### 7.2 `getSpecies`

`speciesId` may be any id from 1 to 649. An unowned species returns `owned: false` with no
individuals and no form ownership.

| Data field | Type | Meaning | Consumer (ui.md §3.4) |
|---|---|---|---|
| `speciesId` | int | | `#id` |
| `displayName` | string | species name | identity header |
| `rarity` | Rarity \| null | the line's rarity; `null` if unknown | rarity label |
| `region` | Region | | |
| `owned`, `hasNormal`, `isShiny` | bool | | Normal/Shiny picker, shown when both exist |
| `isRaising` | bool | | "Raising" label |
| `isRepresentative` | bool | the species is pinned, in any form | star button |
| `forms` | `[{form, displayName, hasNormal, isShiny, isRepresentative}]` | catalog order, default first; `[]` for species without forms. A form counts as collected if `hasNormal` or `isShiny`. | form picker |
| `individuals` | Individual[] | entries whose final species is this one and that have a profile; the active Pokémon first, then newest `caughtAt` | individual picker and card |
| `detailsStatus` | `ready` \| `unavailable` | `unavailable` shows "details unavailable" with Retry, which resends `getSpecies` | |
| `details` | Details \| null | default-form data; alternate forms use it too, and the UI says so | species data and move cards |

**Individual:**

| Field | Type | Meaning |
|---|---|---|
| `id` | string | instance id, or `active-<base>-<current>` for the companion being raised |
| `form` | string \| null | |
| `displayName` | string | with a form label for a non-default form |
| `isRaising` | bool | |
| `released` | bool | |
| `isShiny` | bool | |
| `caughtAt` | ts \| null | `null` for the active individual |
| `nature` | Nature \| null | |
| `level` | int | |
| `gender` | `male` \| `female` \| `genderless` \| null | `null` until details enrich the profile |
| `ability` | `{name, displayName, isHidden}` \| null | |
| `ivs` | Stats | |
| `stats` | Stats \| null | actual stats at `level` (companion.md §3.4); `null` when details are unavailable |
| `moves` | `[{name, displayName, level}]` | known moves, at most 4 |

**Stats** is `{hp, attack, defense, specialAttack, specialDefense, speed}`, all ints.

**Details:**

| Field | Type | Meaning |
|---|---|---|
| `types` | `[{key, name}]` | |
| `heightM`, `weightKg` | number | |
| `baseStats` | Stats | |
| `abilities` | `[{name, displayName, isHidden}]` | possible abilities |
| `moves` | `[{name, displayName, methods: [{method, level}]}]` | the black-2-white-2 learnset. `method` is the PokéAPI move-learn-method name (`level-up`, `machine`, `egg`, `tutor`, …). `level` is an int for `level-up`, else `null`. |

The extension computes the base-stat total and the stat-bar scale (upstream `displayScaleMaximum`).

### 7.3 `getCatchLog`

| Arg | Type | Default |
|---|---|---|
| `offset` | int ≥ 0 | 0 |
| `limit` | int 1–100 | 20 |
| `query`, `rarity`, `shinyOnly` | as in §7.1 | |
| `sort` | `recentFirst` \| `oldestFirst` \| `numberAsc` \| `numberDesc` \| `nameAsc` \| `nameDesc` \| `rarityDesc` | `recentFirst` |

| Data field | Type | Meaning |
|---|---|---|
| `offset`, `limit` | int | echoed |
| `total` | int | entries matching the filters |
| `entries` | Entry[] | |
| `rarityCounts` | `{legendary, rare, uncommon, common}` | individuals per rarity, ignoring filters (log header chips, ui.md §3.5) |

**Entry:**

| Field | Type | Meaning |
|---|---|---|
| `id` | string | as for Individual |
| `isRaising` | bool | the synthetic active entry. It is pinned first under `recentFirst` and last under `oldestFirst` (companion.md §5.3). |
| `released` | bool | "RELEASED" badge; always `false` in M1 |
| `rarity`, `isShiny`, `nature`, `form` | | |
| `caughtAt` | ts \| null | shown as a relative date |
| `speciesId` | int | the final species reached |
| `displayName` | string | |
| `chain` | `[{speciesId, form, displayName}]` | the species reached, in order, with `form` resolved per species; drawn as the evolution line with names |

Static thumbnails for chain nodes: `{speciesId, form, shiny: entry.isShiny, back: false, animated: false}`.

## 8. Sprites

### 8.1 `getSprite`

- **SpriteRequest args.**
  - `speciesId`: 1–649.
  - `form`: string or `null`.
  - `shiny`: default `false`.
  - `back`: default `false`.
  - `animated`: default `true`.
  - Or `{"egg": true}` for the egg.
- **Reply `data`.**
  - `key`: the asset actually served.
  - `status`: `ready` or `fallback`.
  - `manifestPath`: absolute path.
- **Fallbacks** (companion.md §8.3).
  - A missing shiny falls back to plain, a missing animation to static.
  - A missing back sprite falls back to front, only after a 404.
  - `fallback` means `key` differs from the requested key.
  - When nothing can be obtained (offline and uncached), the error is `unavailable`.
- **Which surfaces animate** (the extension decides).
  - Animated: the top bar (frames decimated per `animation-quality`), the companion card and the
    species-detail header (native timing).
  - Static, as upstream: dex cells, evolution lines, the form picker and the catch log.
- **Keys** follow companion.md §8.2: `<asset>-[back-][sh](a|s)`.
  - `asset` is `<id>` for the default form, else `<id>-<form>`.
  - The egg's key is `egg`.
  - Examples: `25-a`, `25-sha`, `12-shs`, `423-s`, `492-sky-s`, `25-back-a`.
- **Notification icons.** For a notification icon, request the static sprite and use its frame file
  as a `Gio.FileIcon`.

### 8.2 Manifest on disk

Two on-disk shapes, both under `<cacheDir>/sprites/` (by default `$XDG_CACHE_HOME/poketokenbar/sprites/`).
Always use the path the helper returns; never build sprite paths.

- **Animated sprite (a GIF source):** a directory `sprites/<key>/` holding three files.
  - `frames.rgba`: all frames concatenated, **non-premultiplied RGBA8888**, row stride `width * 4`,
    so each frame is `width * height * 4` bytes. Frames are already composited (disposal None,
    Background and Previous, and transparency are applied) and every frame has the full canvas size.
    Read it with `load_contents_async`; there is no decode step.
  - `frame-000.png`: the first frame as a PNG, for static uses (notification icon).
  - `manifest.json`:
    ```json
    {"width": 50, "height": 46, "animated": true, "rgba": "frames.rgba",
     "frames": [{"offset": 0, "delayMs": 200}, {"offset": 9200, "delayMs": 100}]}
    ```
- **Static sprite (a PNG source: dex cells, fallbacks, items, egg):** a plain file `sprites/<key>.png`,
  with no manifest. Item keys are `item-<name>`; the egg key is `egg` (a square crop around its content).

**Manifest fields**

- `width`, `height`: the GIF logical screen in pixels. These canvases are tight and not square, so
  fit with the aspect ratio kept.
- `rgba`: the blob's file name, relative to the manifest's directory.
- `frames[].offset`: byte offset of the frame in the blob, `index * width * height * 4`.
- `frames[].delayMs`: the GIF delay (centiseconds times 10); a delay under 20 ms becomes 100 ms
  (companion.md §8.4).
- `animated`: `false` for a 1-frame GIF (exactly one entry in `frames`). A 1-frame GIF counts as
  `fallback` when an animation was requested.

**Rules**

- **Frame-rate decimation** belongs to the extension: floors of 400, 200 or 100 ms per
  `animation-quality`, native timing in the popover.
- **Atomicity.**
  - The helper writes the directory into a temp entry inside `sprites/` (manifest last) and renames it
    to `<key>/`. A directory containing `manifest.json` is complete and immutable, so a reader never
    sees a manifest without its blob.
  - Static PNGs are written to a temp file and renamed.
  - Raw GIF downloads are not kept.
- **Missing files.** If a manifest, blob or PNG disappears (cache cleared), the extension calls
  `getSprite` once more.

## 9. Settings

### 9.1 GSettings keys

Schema id `org.gnome.shell.extensions.poketokenbar`. Enum keys are type `s` with `<choices>`.
**Forwarded** means the extension sends the value in `setSettings`. **UI-only** means only the
extension reads it.

| Key | Type | Default | Range / choices | Scope | Spec |
|---|---|---|---|---|---|
| `refresh-interval` | `i` (s) | 120 | 0 (manual), 60, 120, 300, 900 | forwarded as `refreshInterval` | ui.md §4 General; usage.md §6.1 |
| `animation-quality` | `s` | `'powerSaver'` | `powerSaver`, `balanced`, `smooth` | UI-only | ui.md §4 General, §1.3 |
| `limit-display-mode` | `s` | `'used'` | `used`, `remaining` | UI-only | ui.md §4 General, §2.1 F |
| `daily-trend-window` | `s` | `'14d'` | `7d`, `14d`, `21d`, `month` | UI-only | ui.md §2.3 |
| `week-start` | `s` | `'locale'` | `locale`, `monday`, `sunday`, `saturday` | forwarded as `weekStart` (resolved, §9.2) | usage.md §4 |
| `growth-difficulty` | `d` | 1.0 | 0.1–2.0 | forwarded as `growthDifficulty` | ui.md §4 Difficulty; companion.md §2.4 |
| `show-tokens-in-menu` | `b` | true | | UI-only | ui.md §4 Show in menu bar |
| `show-cost-in-menu` | `b` | false | | UI-only | same |
| `show-growth-in-menu` | `b` | false | | UI-only | same |
| `show-limit-in-menu` | `b` | false | | UI-only | same |
| `menu-limit-color-mode` | `s` | `'gauge'` | `gauge`, `attention`, `off` | UI-only | ui.md §1.2, §4 |
| `always-faces-back` | `b` | false | | forwarded as `alwaysFacesBack` | ui.md §1.5 item 7, §4 Floating pet |
| `limit-notifications` | `b` | true | | UI-only | ui.md §4 Notifications, §5.2 |
| `warn-threshold` | `i` (%) | 80 | 50–95, step 5 | forwarded as `warnThreshold` | ui.md §4 Notifications; usage.md §6.6 |
| `crit-threshold` | `i` (%) | 95 | 80–100, step 5 | forwarded as `critThreshold` | same |
| `companion-notifications` | `b` | true | | UI-only | ui.md §5.1 |

These are not GSettings keys:
- the representative Pokémon, which lives in the helper's save and is set with `setRepresentative`;
- the language, which follows the session locale (§10).

LATER keys: shop difficulty, tracked providers, status checks, floating pet.

**Threshold rule.** GSettings cannot enforce the 5-point gap.
1. Clamp `warn` to 50–95 and `crit` to 80–100.
2. If `warn > crit − 5`, set `warn = crit − 5`. Crit wins, as in upstream's load path.

The helper applies this rule, and prefs use the same rule after moving either slider.

### 9.2 `setSettings` args (the forwarded set)

Every field is required. A missing field or a wrong JSON type gives `badArgs`, and nothing is applied.
The helper persists the applied set as `<stateDir>/settings-applied.json`.

| Field | Type | Normalization |
|---|---|---|
| `refreshInterval` | int (s) | not a preset: the nearest preset |
| `warnThreshold` | int | threshold rule (§9.1) |
| `critThreshold` | int | threshold rule (§9.1) |
| `growthDifficulty` | number | clamped to 0.1–2.0 |
| `weekStart` | int 1–7 | ISO weekday the week starts on (1 = Monday … 7 = Sunday); out of range gives `badArgs` |
| `language` | string | §10 |
| `alwaysFacesBack` | bool | |

`weekStart` is resolved by the extension from `week-start`:
- `monday` → 1, `sunday` → 7, `saturday` → 6;
- `locale` → the session locale's first weekday.

The extension tries `new Intl.Locale(tag).getWeekInfo?.().firstDay`, then the `weekInfo` getter.
**Unverified** in gnome-shell 51's SpiderMonkey: fall back to 1 if neither exists. Phase 7 must check it.

## 10. Localization

- **UI strings** belong to the extension (gettext). The helper's English `message` fields are for
  logs and a last-resort tooltip only.
- **`language`.** Take the first entry of `GLib.get_language_names()`, strip the codeset and modifier,
  replace `_` with `-` and lowercase it, so `fr_FR.UTF-8` becomes `fr-fr`. `C` and `POSIX` become `en`.
- **Name resolution.** The helper resolves PokéAPI names for species, form labels, types, abilities
  and moves in this order:
  1. the exact PokéAPI language code, compared case-insensitively;
  2. the primary subtag, with upstream's preferences: `ja` → `ja-hrkt` then `ja`; `pt` → `pt-br` then `pt`;
  3. `en`;
  4. `#<id>` for species, or the API identifier for everything else.
- **Never persisted:** a `#<id>` placeholder (companion.md §5.3). Names are re-resolved when
  `language` changes.
- **Ids sent raw**, for the extension to translate with gettext: natures, move-learn methods, regions,
  rarities and window kinds.
- **Number grouping, dates and clocks** are formatted by the extension (ui.md §8). Clocks are 24 h;
  grouping follows the system region.

## 11. Error model

### 11.1 Malformed input (extension → helper)

| Input | Helper action |
|---|---|
| Not valid JSON, not an object, or longer than 1 MiB | Log the first 200 bytes and ignore the line. No reply is possible. |
| An object without a positive integer `id` (missing, string, float, ≤ 0) | Log and ignore. |
| An integer `id` with a missing or unknown `cmd` | `result` with error `unknownCommand`. |
| A known `cmd` with invalid `args` | `result` with error `badArgs`. The message names the field, and nothing is applied. |

### 11.2 `result` error codes

| `code` | Meaning | Extension |
|---|---|---|
| `unknownCommand` | `cmd` not recognized | log it; it is a bug |
| `badArgs` | an arg is missing, out of range or of the wrong type | log it; it is a bug |
| `notOwned` | `setRepresentative` for a species or form the player does not own | keep the previous UI state |
| `notReady` | **Reserved; no M1 command returns it.** It means data the command depends on is still loading. It exists so a later command (for example Rare Candy use, which needs the evolution line) can say so without a protocol bump. | retry after the next `state` |
| `unavailable` | remote data is unobtainable and nothing is cached (`getSprite` while offline) | placeholder; retry when the view reopens or after `networkUp` |
| `internal` | unexpected helper error | log it and show a generic error |

`timeout` and `helperExited` are produced only by the extension; they never appear on the wire.

### 11.3 Ongoing conditions (`meta.errors[]`)

Each entry is `{code, message, providerId}` and stays present while the condition lasts. Unknown codes
show `message`.

| `code` | Condition |
|---|---|
| `usageScan` | a provider's daily scan failed, so `lastUpdated` stops advancing (usage.md §6.2) |
| `saveWrite` | the companion save or a snapshot could not be written |
| `cacheWrite` | the usage cache could not be written (not fatal) |

### 11.4 Limits error codes (`errorCode`, `authExpired.errorCode`)

| `errorCode` | `authState` | Condition |
|---|---|---|
| `credentialsMissing` | `missing` | no readable credentials file |
| `accountOAuthMissing` | `missing` | the file exists but `claudeAiOauth` is absent or `null`, meaning logged out. Its message is distinct (usage.md §9). |
| `tokenExpired` | `expired` | `expiresAt` ≤ now + 60 s. The helper never refreshes the token; it waits for Claude Code to rewrite the file. |
| `unauthorized` | `expired` | 401 or 403 after one re-read retry; `httpStatus` is set |
| `rateLimited` | `error` | 429; `backoffUntil` and `httpStatus` are set |
| `network` | `error` | DNS, TLS or timeout |
| `httpStatus` | `error` | any other non-200; `httpStatus` is set |
| `decode` | `error` | unexpected response body |

## 12. Enumerations

| Name | Values | Fallback for an unknown value |
|---|---|---|
| Coverage | `empty`, `reported`, `estimated`, `unavailable`, `partial` | `unavailable` |
| PaceTier | `wayUnder`, `under`, `onPace`, `slightlyOver`, `over`, `wayOver` | `onPace` |
| BurnTier | `idle`, `normal`, `fast`, `blazing` | `normal` |
| Mood | `egg`, `levelUp`, `tired`, `sulk`, `sleep`, `idle`, `working`, `focus` | `idle` |
| AuthState | `ok`, `expired`, `missing`, `error` | `error` |
| WindowKind | `fiveHour`, `sevenDay`, `sevenDayOpus`, `sevenDaySonnet`, `scoped` | `scoped` |
| SpriteStatus | `ready`, `fallback`, `loading` | `loading` |
| Rarity | `common`, `uncommon`, `rare`, `legendary` | `common` (gray) |
| Region | `kanto` (1–151), `johto` (152–251), `hoenn` (252–386), `sinnoh` (387–493), `unova` (494–649) | none |
| Nature | the 25 natures in lowercase, `hardy` … `quirky` (companion.md §3.4) | show the id |

**Coverage** merges the per-entry flags with an OR (usage.md §3.4):
- unknown mixed with an estimated or reported cost gives `partial`;
- unknown alone gives `unavailable`;
- estimated gives `estimated`;
- reported alone gives `reported`;
- no tokens give `empty`.

How it displays:
- `unavailable` shows `$—` or "Unavailable";
- `partial` shows the amount plus a "partial" hint.

M1 prices from the pricing table only, so `reported` does not occur yet.

## 13. Fixtures

They live in `extension/test/fixtures/`.
- Each file holds one wire message, pretty-printed; `events.ndjson` is the exception and is compact.
- Sums, thresholds, tiers and the forecast were generated under assertions.
- Pokémon details (moves, IVs) are realistic but illustrative.
- The time zone is Europe/Paris (UTC+1), the week starts Monday, and the date is 2026-11-18.

| File | Scenario |
|---|---|
| `state-egg.json` (6.0 KB) | Fresh install at 07:50 local. Egg 3.1M/5M (62 %), dex empty, no Claude OAuth credentials (`authState: missing`), 4.1M tokens today. |
| `state-active.json` (7.7 KB on disk, 6.3 KB on the wire) | 14:20 local, 187.3M tokens today. Pidgeotto, a common 3-stage line at stage 2/3 (162.4M/250M = 64 %, level 41), mood `focus`. The representative is pinned to a shiny Butterfree, so `menuSubject` differs from `active`. Max plan: 5 h window 47 % `onPace`, 7 d window 83 % `over` (warn tier), scoped Opus 31 % `wayUnder`; the forecast does not reach the reset. |
| `dex-page.json` | `getDexPage` result for the same world: 14 species, one species pair with forms, 3 species owned only shiny |
| `species.json` | `getSpecies` result for Gastrodon (#423): West/East Sea forms, one East Sea individual, details ready |
| `catch-log.json` | `getCatchLog` result: the active entry first, then 5 graduations, newest first |
| `events.ndjson` | One compact line per event kind. These are standalone examples, and the `badge` line does not match the fixture world. |

## 14. Decisions taken here (versus upstream and the specs)

1. The limit-alert edge state is **persisted** (§6), because the helper restarts on every screen lock.
2. `forecast` is per provider, at `usage.providers.<id>.forecast`. Upstream has a single
   store-level, Claude-only `fiveHourForecast`.
3. The initial `state` is always sent, even on first run, built by the normal builder from persisted
   inputs (§2.3).
4. `today` totals are zero-filled, never `null`. Series are always complete windows.
5. The helper computes `paceDelta`, because rounding differs between Swift and JS. It computes every
   tier with the forwarded thresholds.
6. `weekStart` is resolved to an ISO weekday by the extension, which has the locale. The helper never
   reads locale data.
7. `alwaysFacesBack` is forwarded because the helper prepares the menu-subject sprite, back view
   included.
8. Dex cells, evolution lines and the catch log use static sprites, as upstream does.
9. Windows with a null utilization are omitted, as upstream's `limitRow` does.
