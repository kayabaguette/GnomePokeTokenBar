# PokeTokenBar → Go helper: porting spec for Claude Code usage and official limits

**Path legend (absolute):**
- `S/` = `/home/user/Documents/01_Projects/PokeTokenBar/Sources/PokeTokenBar/`
- `T/` = `/home/user/Documents/01_Projects/PokeTokenBar/Tests/PokeTokenBarTests/`
- `D/` = `/home/user/Documents/01_Projects/PokeTokenBar/docs/reference/`

The spec covers Claude only. Wherever a rule is generic, it says so, so the Go design can stay provider-agnostic.

---

## 0. Provider abstraction (keep it generic in Go)

| Concept | Swift location | Rule |
|---|---|---|
| `UsageProvider` protocol | `S/Core/UsageProvider.swift:4-17` | Members: `id`, `displayName`, `reportsCost` (default true, :19-21), `fetchDaily() throws -> DailyUsage?` (critical path), `fetchEnrichment() -> ProviderEnrichment` (best effort). |
| `ProviderEnrichment` | `S/Core/UsageProvider.swift:24-39` | Fields: `activeBlock` + `blocksOK`; `weekTotal`, `monthTotal`, `monthDaily`, `recentDaily`, `lastUsage` + `periodsOK`. A false OK flag means "keep previous values". |
| Shared local assembly | `S/Core/UsageProvider.swift:50-78` (`ProviderEnrichment.local`) | Every local provider builds block, week, month, month series, 21-day series and lastUsage from one entry set. Port this once. |
| Claude provider | `S/Core/LocalUsageProvider.swift:4-21` | id `claude_code`. `fetchDaily` scans files with mtime ≥ local midnight (:10). `fetchEnrichment` scans from `enrichmentScanStart` (:17-18). It never throws. |
| Registry | `S/Core/UsageStore.swift:894-901` | Ordered provider list. Snapshot/tab order follows it (:1256-1257). |
| Limits abstraction | `S/Core/OAuthLimitsProvider.swift:31-33` (`ClaudeLimitsProviding.fetch(allowKeychainPrompt:)`) | Per-provider limit sources are bespoke. Only official limits, the 5h forecast and the "current block" row may branch on provider id (`D/provider-extension.md:19-25`). |
| Extension rules | `D/provider-extension.md:14-69` | Aggregates must sum all providers. Custom roots key is `customScanRoots.<id>`. Env names live in a registry. Opt-out is via `disabledProviderIDs`. The throw/nil/[] semantics are below (§6). |

---

## 1. Data roots

### 1.1 What Swift scans (`computeClaudeProjectRoots`, `S/Core/LocalUsageReader.swift:101-129`), in priority order

1. **`CLAUDE_CONFIG_DIR`** (:108-115)
   - Split on `,` only, trim whitespace, skip empty pieces, expand `~`, then append `/projects`.
   - The value comes from `UsageEnvironment`. Process env wins; otherwise the login shell is queried once per process with `$SHELL -ilc` (`S/Core/UsageEnvironment.swift:48-82`, `S/Core/BinaryLocator.swift:119-199`). An empty value counts as unset (:65-69).
2. **Additional account roots** + `/projects` (:118), from `ClaudeAccountRoots.installedAccountRoots()` (`S/Core/ClaudeAccountRoots.swift:150-152`).
   - These are `CLAUDE_CONFIG_DIR` entries (split on comma or newline), plus `~/.claude-*` and `~/.claude_*` folders whose `.claude.json` has an `oauthAccount` object (:45-62), plus the Settings value `additionalClaudeConfigDirs` (:11, :155-159).
   - `~/.claude` and `~/.config/claude` are always excluded (:161-166).
   - Only computed in the bundled app (:140-147).
3. `~/.config/claude/projects` (:119; constant at :81)
4. `~/.claude/projects` (:120; constant at :80)
5. **macOS only:** Claude Desktop embedded sessions under `~/Library/Application Support/Claude/{local-agent-mode-sessions,claude-code-sessions}`, found recursively as `**/.claude/projects` (:122-125, :231-262).
   - Maximum depth 7. Prunes `node_modules`, `.git`, `venv`, `.venv` (:224-225).
   - Must NOT skip hidden directories.
6. **Custom roots** from the Settings key `customScanRoots.claude_code` (:128; `S/Core/CustomScanRoots.swift`)
   - Split on comma or newline, expand `~`, apply `fnmatch` globs per path segment, keep existing directories only (:26-58).
   - A custom root that is an ancestor of a default root, or is `/`, is dropped (:61-68, :123-130).

**Normalization (`normalizedRoots`, :266-289):**
- Resolve symlinks and standardize paths.
- Dedupe and drop nested roots, comparing **lowercased** paths; keep the shortest path; preserve the original order.

**Roots cache:** 300 s TTL (:179-212). It is invalidated when Settings change (`S/Core/UsageStore.swift:270`, :1452).

**File selection (`LocalUsageCache.collect`, `S/Core/LocalUsageCache.swift:386-444`):**
- Recursive enumeration with `skipsHiddenFiles`; `.jsonl` only (`.json` is excluded for Claude); mtime ≥ `since`.
- `<session>/subagents/agent-*.jsonl` files are included.

### 1.2 Linux deltas (observed on this machine; values not printed)

| Item | Swift behaviour | Linux reality / Go action |
|---|---|---|
| `CLAUDE_CONFIG_DIR` | Shell lookup because macOS GUI apps don't inherit the shell env | It is set here (`=/home/user/.claude`). A process spawned by a GNOME extension inherits gnome-shell's systemd user env, not `~/.bashrc`. Keep a one-time `$SHELL -ilc` (or `systemctl --user show-environment`) fallback. |
| `XDG_CONFIG_HOME` | Not honoured; `~/.config/claude` is hard-coded (:81) | Add `$XDG_CONFIG_HOME/claude/projects` when it is set. |
| Desktop embedded roots | macOS path (:122) | `~/.config/Claude` and `~/.config/claude` are both absent here. The Linux equivalent was not determined, so skip it. |
| Path compare | Lowercased, for APFS (:272-284; `S/Core/CustomScanRoots.swift:132-134`) | ext4/btrfs are case-sensitive. Compare exact paths after `filepath.EvalSymlinks`. |
| `.claude.json` (identity) | Default identity read from `~/.claude.json` (`S/Core/ClaudeAccountRoots.swift:77`) | Here `~/.claude.json` is absent and `~/.claude/.claude.json` exists with `oauthAccount{emailAddress, organizationName, organizationUuid, …}`. Go should read `$CLAUDE_CONFIG_DIR/.claude.json` first, then `~/.claude.json`. |
| `.credentials.json` | Default path hard-coded to `~/.claude/.credentials.json` (`S/Core/OAuthLimitsProvider.swift:206-209`) | Use `$CLAUDE_CONFIG_DIR/.credentials.json` if set, else `~/.claude/.credentials.json`. The file exists here with mode 0600. |
| `history.jsonl` | `<configDir>/history.jsonl` (`S/Core/ClaudeAccountRoots.swift:200-203`) | Present at `~/.claude/history.jsonl`. Used only for multi-account attribution. |

---

## 2. JSONL record format

### 2.1 Line scanning (`parseClaudeData`, `S/Core/LocalUsageReader.swift:498-534`)

- Lines are split on byte `0x0A`.
- **Cost-state lines first** (:506-512): if the line contains the bytes `"cost-state"`, try `parseClaudeCostStateLine`. On success, `costState = state` (the **last one in the file wins**), and the line is not considered as an assistant line.
- **Prefilter** (:513-516): the line must be non-empty and contain both the bytes `"usage"` and `"assistant"`.
- `consumed` = bytes through the last newline (:526). A trailing line without a newline is parsed but not consumed (:532).
- The file's entries are then passed through `dedupKeepMax` (:533).

### 2.2 Assistant record (`parseClaudeLine`, :564-580)

Required fields; a missing one means the line is skipped:
- `type == "assistant"`
- `message` is an object
- `message.usage` is an object
- `timestamp` is a string that `ISO8601Parser` accepts (`S/Core/Models.swift:874-914`). The parser tries fractional seconds first; otherwise it truncates or pads the fraction to 3 digits; otherwise it parses without a fraction. It accepts `Z` and `±HH:MM`.

| Entry field | Source |
|---|---|
| `model` | `message.model`, or `"unknown"` |
| `id` | `(message.id ?? "") + "|" + (requestId ?? "")` |
| `input` | `usage.input_tokens` |
| `output` | `usage.output_tokens` |
| `cacheWrite` | `usage.cache_creation_input_tokens` |
| `cacheRead` | `usage.cache_read_input_tokens` |
| `localDay` | Local-timezone `yyyy-MM-dd` of the timestamp (:2184-2190) |
| `sessionID` | From the **path**, not the JSON (:538-544): `<proj>/<S>.jsonl` → `S`; `<proj>/<S>/subagents/<a>.jsonl` → `S` |
| `total` | `input + output + cacheWrite + cacheRead` (:49) |

**Number extraction (`intValue`/`intOrNil`, :2204-2227):**
- Only JSON numbers count; `null`, strings and absent keys give 0.
- Negative values give 0.
- Values ≥ 1e18 clamp to `maxParsedTokenValue = 1_000_000_000_000_000`.
- Fractions are truncated.

**Not read at all:**
- `isSidechain`, `isApiErrorMessage`
- `usage.cache_creation.{ephemeral_5m_input_tokens, ephemeral_1h_input_tokens}` (the 5m/1h split is ignored)
- `output_tokens_details.thinking_tokens`, `server_tool_use.*`, `service_tier`, `speed`
- `requestedModel`, `sessionId`, `uuid`

### 2.3 Dedupe (`dedupKeepMax`, :380-397)

- Key is `id`. Keep the entry with the larger `total`; on a tie, keep the existing one.
- `date`/`localDay` become the **earliest** of the duplicates.
- Session ids become the sorted union: `sessionID` = first, `sessionIDs` = the list if there is more than one.
- It is applied:
  - within each file (:533),
  - when merging a tail into a cached blob (`S/Core/LocalUsageCache.swift:417`),
  - globally across all files and roots (`S/Core/LocalUsageCache.swift:252`).
- **Edge case:** records missing both `message.id` and `requestId` all collapse into one id, `"|"`.

### 2.4 Sidechains and synthetic records (what the code does)

- `isSidechain` is never read. Subagent transcripts are counted under the parent session id, and the global id dedupe prevents double tokens.
- `<synthetic>` records are parsed into entries. They are excluded only later, by `total > 0` checks:
  - cost and coverage are skipped for zero-token entries (:60);
  - `activeBlock` ignores them (:2120);
  - `lastUsage` ignores them (`S/Core/UsageProvider.swift:76`);
  - `daily()` returns nil when the day's total is 0 (:2025).

### 2.5 Observed local format (209 files; key names and counts only)

- **Record types:** `assistant`, `user`, `attachment`, `cost-state`, `system`, `api-request*`, `mode`, `permission-mode`, `last-prompt`, `ai-title`, `session-owner`, and others. Only `assistant` and `cost-state` matter.
- **Assistant top-level keys include** `message`, `requestId` (missing on 1 of 23,297), `timestamp` (always 24 characters, `…sss Z`), `isSidechain` (true on 15,394), `sessionId`, `agentId`, `isApiErrorMessage`.
- **Duplicate multiplicity** of `(message.id, requestId)` ranges from 1 to 17. Keep-max is mandatory.
- **`<synthetic>`:** 26 records, all with total 0.
- **Models seen:** `claude-opus-5`, `claude-opus-5-5`, `claude-opus-4-8`, `claude-sonnet-5-5`, `claude-haiku-5-5`, `claude-mythos-5-1`, `claude-mythos-5-2`, `<synthetic>`.
- **`cost-state` keys:** `type`, `sessionId` (equals the file stem in 148/148), `totalCostUSD`, `modelUsage{<model>: {inputTokens, outputTokens, thinkingTokens, cacheReadInputTokens, cacheCreationInputTokens, cacheCreationEphemeral5m/1hInputTokens, webSearchRequests, costUSD}}`, `hasUnknownModelCost`, plus duration and line counters.

---

## 3. Cost

### 3.1 Pricing table (USD per MTok: input / output / cache write (5m) / cache read)

From `S/Core/ModelPricing.swift:23-68`; converted per token at :13-16.

**Claude rows:**

| Model | In | Out | Cache write | Cache read |
|---|---|---|---|---|
| claude-opus-5 | 5 | 25 | 6.25 | 0.5 |
| claude-opus-5-5 | 4 | 20 | 5 | 0.2 |
| claude-sonnet-5 | 2 | 10 | 2.5 | 0.2 |
| claude-sonnet-5-5 | 2 | 10 | 2.5 | 0.1 |
| claude-opus-4-20250514 | 15 | 75 | 18.75 | 1.5 |
| claude-sonnet-4-20250514 | 3 | 15 | 3.75 | 0.3 |
| claude-sonnet-4-5-20250929 | 3 | 15 | 3.75 | 0.3 |
| claude-opus-4-8 | 5 | 25 | 6.25 | 0.5 |
| claude-opus-4-7 | 5 | 25 | 6.25 | 0.5 |
| claude-sonnet-4-6 | 3 | 15 | 3.75 | 0.3 |
| claude-haiku-4-5-20251001 | 1 | 5 | 1.25 | 0.1 |
| claude-fable-5 | 10 | 50 | 12.5 | 1.0 |
| claude-fable-5-1 | 10 | 50 | 12.5 | 0.25 |

**Other rows (same table; keep it generic):**
- gpt-6-astra 10/50/12.5/1
- gpt-6-sol 2/10/2.5/0.2
- gpt-6.1-sol 2/10/2.5/0.1
- gpt-6-luna 0.1/0.5/0.125/0.01
- gpt-5.6-sol 4/20/5/0.4
- gpt-5.6-terra 2/12/2.5/0.2
- gpt-5.6-luna 0.2/1.2/0.25/0.02
- gpt-5, gpt-5-codex, gpt-5.1, gpt-5.1-codex: 1.25/10/0/0.125
- gpt-5.2, gpt-5.2-codex, gpt-5.3-codex: 1.75/14/0/0.175
- gpt-5.4 2.5/15/0/0.25
- gpt-5.5 5/30/0/0.5
- gemini-2.5-pro 1.25/10/0/0.125
- gemini-2.5-flash 0.30/2.5/0/0.03
- gemini-2.5-flash-lite 0.10/0.40/0/0.01
- gemini-2.0-flash 0.10/0.4/0/0.025

**Aliases (:72-83):**
- `claude-sonnet-4`→`…-20250514`
- `claude-opus-4`→`…-20250514`
- `claude-sonnet-4-5`→`…-20250929`
- `claude-haiku-4-5`→`…-20251001`
- `gpt-5.6`→`gpt-5.6-sol`
- dated GPT ids map to their base

**Model key (:85-92):** trim, lowercase, strip one prefix among `openai/`, `anthropic/`, `google/`, `models/`, then apply the alias. No `[1m]` stripping happens here.

### 3.2 `estimatedCost` (:103-119)

- Returns nil if the model is not in the table or any bucket is negative.
- Returns nil if `cacheWrite > 0` and the row's write rate is 0 (:108).
- Long-context multipliers (input ×2, output ×1.5) apply only to the GPT keys listed above when prompt > 272k, and to `gemini-2.5-pro` when prompt > 200k. None apply to Claude.
- Formula: `(in·r.in + cw·r.cw + cr·r.cr)·inMult + out·r.out·outMult`.

### 3.3 Unknown models

- There is **no family fallback** (removed in #289). An unknown model gives nil, which yields coverage `unavailable` and amount 0.
- `noteUnpriced` logs each model once per process (:124-134).
- Local models with no table row: `claude-mythos-5-1`, `claude-mythos-5-2`, `claude-haiku-5-5`. They are priced only if a `cost-state` ledger covers them.

### 3.4 Provenance (`Bucket.add`, `S/Core/LocalUsageReader.swift:57-73`; `S/Core/UsageCost.swift:5-41`)

Applied per entry, only when `total > 0`:

1. If `explicitCost` is finite and ≥ 0: add it, with coverage `estimate` if `costIsEstimate == true`, else `reported` (`.source`).
2. Else, if `costUnavailable != true` and `estimatedCost != nil`: add the estimate, coverage `estimated`.
3. Else: coverage `unknown`.

**Coverage merge** is an OR of the flags.

**Display (`UsageCost.text`, :32-35):** only unknown → "Unavailable" (compact form `$—`). Otherwise show the amount. The hint is "partial" when unknown is mixed with known.

**DailyUsage/PeriodUsage legacy decoding:** when coverage is missing, it is `empty` if tokens == 0, `estimate` if cost > 0, else `unavailable` (`S/Core/Models.swift:33-34`, :158-159).

### 3.5 Claude's own ledger (`cost-state`)

**Parse (`S/Core/LocalUsageReader.swift:418-432`):**
- Requires `type == "cost-state"` and a `modelUsage` object.
- For each entry, `costUSD` (finite, ≥ 0) is added to `costByModel[base]`, where `base` cuts the model key at the first `[` (:413-416), so `claude-opus-5[1m]` and `claude-opus-5` pool together.
- If `costByModel` is empty, the result is nil.
- `hasUnknownModelCost` is parsed but **used nowhere** (grep: only :407 and :424).

**Attribution (`applyReportedCost`, :439-462):**
- Applied **per file, at read time.** The blob stores raw entries plus the ledger and recomputes via `attributedEntries`, `S/Core/LocalUsageCache.swift:35-38`.
- Group the file's entries with `total > 0` by base model.
- For each ledger model that has entries: `share = cost · total_i / Σtotal`. The last entry in array order takes the remainder, so the sum is exact. Set `explicitCost = max(0, share)` and `costIsEstimate = false`.
- Ledger models with no entries are dropped. Entries of models absent from the ledger keep the table estimate.

**Risk observed locally (raw numbers; mechanism undetermined; only input/output were compared):**
- 32 of 114 main transcripts have a ledger; 0 of 95 subagent transcripts do.
- In 5 sampled sessions with subagents, the last ledger's summed `outputTokens` exceeded the deduped output of main + subagent files. Examples: 5.61M vs 2.28M; 1.12M vs 1.07M. Input was also far larger.
- **Swift consequence:** the main file's entries absorb the entire per-model ledger, while subagent entries (no ledger) are table-estimated. Subagent spend is therefore probably counted twice in **cost**; tokens are unaffected.
- Go needs an explicit decision here (for example, attribute the ledger across the session's main + subagent entries).

---

## 4. Time bucketing

| Item | Rule | Ref |
|---|---|---|
| Local day key | `yyyy-MM-dd`, `TimeZone.current`, `en_US_POSIX`. It is baked into the Entry at parse time and cached, so a timezone change leaves stale keys until reparse. | `S/Core/LocalUsageReader.swift:2184-2190` |
| Today | `todayKey()` at call time | :2182 |
| Week start | `Calendar.current.dateInterval(of: .weekOfYear)`, so it uses the **locale's first weekday**. Go must choose explicitly (config, or the locale's `first_weekday`). | :2141-2143 |
| Month start / key | Calendar `[year, month]`; key `yyyy-MM` | :2137-2139, :2176-2180 |
| `daily(entries, day)` | Sum entries whose `localDay == day`; nil if total 0. `models` breakdown only for opt-in providers (not Claude). | :2018-2029 |
| `period(from, to)` | Inclusive string compare on `localDay`. Week = weekStartDay…today; month = monthStartDay…today. | :2032-2036; `S/Core/UsageProvider.swift:60-65` |
| `dailySeries(start, end)` | Axis runs from `startOfDay(start)` to `startOfDay(end)`, stepping **+1 calendar day** (never 86400 s). Every day is present, zero-filled. Entries off-axis are dropped. | :2061-2089 |
| Month series | monthStart → now. Invariant: Σ series == monthTotal. | :2094-2101 |
| Rolling series | 21 days: `startOfRollingWindow` = startOfDay(today) − (days−1) calendar days | :2104-2111, :2145, :2151-2158 |
| Enrichment scan lower bound | `min(monthStart, weekStart, now−5h, now−7d, rolling21Start)` | :2168-2174 |
| Recent-use window | 7 days (keeps an idle provider's tab and limits) | :21 |
| **5h block** | Rolling, not ccusage-style hour-floored blocks. `recent` = entries with `date ≥ now−5h` and `total > 0`, sorted; nil if empty. `first` = earliest. Tokens and cost via Bucket. `minutes = max(1, (now−first)/60)`; `tokensPerMinute = total/minutes`. `id = "block-<unix secs of first>"`; `start` = ISO8601(first); `end` = first + 5h; `isActive = true`. | :18, :2114-2132 |
| Burn tier | Sum of tpm over all providers' active blocks: ≤1000 idle; <100k normal; <400k fast; else blazing. | `S/Core/UsageStore.swift:682-684`, :878-884 |
| Forecast to 100% | See below. | `S/Core/UsageStore.swift:693-722` |
| Per-account block | With more than one account: if the account's five_hour has a reset in the future, block = that account's turns since reset−5h, with `endTime = reset`. Running-but-past or not-started windows give no block. No window gives the rolling block. | `S/Core/ClaudeAccountUsage.swift:104-118`; `S/Core/UsageStore.swift:707-709` |
| Pace fraction | `(span − (reset − now))/span`; nil if outside [0,1] or span ≤ 0 (no clamping) | `S/Core/UsageStore.swift:485-490` |
| Window spans | five_hour = 5h; seven_day / opus / sonnet = 7d; `limits[].kind`: `session` = 5h, `weekly_all`/`weekly_scoped` = 7d | `S/Core/Models.swift:206-224` |

**Forecast to 100% (`S/Core/UsageStore.swift:693-722`):**
- Needs the tracked account's `five_hour` with both utilization and reset.
- If u ≥ 100: depletion = now, `beforeReset = true`.
- Otherwise use the block (the account's own if there are several accounts, else the `claude_code` snapshot's block) and require u ≥ 5, u < 100, blockTokens > 0, tpm ≥ 10,000.
- `tokensPerPercent = blockTokens/u`; `minutesLeft = (100−u)·tokensPerPercent/tpm`. It must be finite and < 1440.
- depletion = now + minutesLeft; `beforeReset = depletion < reset`.

---

## 5. Incremental cache (`LocalUsageCache`, `S/Core/LocalUsageCache.swift`)

| Aspect | Rule | Ref |
|---|---|---|
| Key / hit | Blob keyed by absolute path. A hit needs `mtime ==` and `size ==` (no reparse). | :386-409 |
| Append (watermark) | If `size > prior.size` and `prior.offset != nil`, parse from `offset` to EOF. Entries = `dedupKeepMax(prior.entries + tail)`. `costState = tail.costState ?? prior.costState`. New offset = through the last newline. | :410-424; reader :475-490 |
| Rewrite / truncate / legacy | Same-size rewrite, a shrunk file, or a blob without an offset: full reparse from 0. The ledger is **discarded** (taken from the fresh parse only). | :414, :419 |
| Rotation | No inode or prefix check. A replaced file that is **larger** is misread as an append. Go: also store the inode and/or a hash of the first N bytes. | :414 |
| Read failure | Reuse the prior blob without updating its signature (so it retries next time); no prior means skip the file. | :425-429 |
| Legacy session id | Old blobs missing `sessionID` are annotated from the path when returned, never when cached. | :235-247 |
| Version invalidation | `claudeParserVersion = 1`; a mismatch drops all Claude blobs. Bump it when bucket or ledger semantics change, **not** for price-only changes (`D/defect-log.md:102-104`). | :129, :565-568 |
| Prune | On save, drop blobs with mtime < now − 40 days. | :602-612 |
| Save | Only if dirty. Throttled to ≥ 60 s since the last save (the first save is immediate). JSON is zlib-compressed and written atomically. Load falls back to plain JSON. Only writes in the bundled app (`persistsToDisk`). | :615-645, :546-554, :214 |
| Location | `~/Library/Application Support/PokeTokenBar/usage-cache.json` | :219-224 |
| On-disk schema | `{claude:{path:Blob}, codex, codexSessionIDs, gemini, grok, pi, omp, omo, kimi, claudeParserVersion, …}`. Blob: `{mtime (Swift Date, seconds since 2001-01-01 as a double), size, entries[], offset?, claudeCostState?{costByModel, hasUnknownModelCost}}`. Entry: `{id, date, localDay, model, input, output, cacheWrite, cacheRead, explicitCost?, costIsEstimate?, costUnavailable?, sessionID?, sessionIDs?}` | :11-124; `S/Core/LocalUsageReader.swift:27-50` |
| Scans per refresh | Daily (since midnight) + enrichment (since `enrichmentScanStart`) + multi-account attribution (since `min(monthStart, now−5h)`, `S/Core/UsageStore.swift:1654-1656`). All go through the same cache. | |

**Go recommendations:**
- Store mtime as int64 nanoseconds. Swift's equality depends on an exact Date round-trip; its tests use whole-second mtimes.
- Use your own format: Apple's `.zlib` is raw DEFLATE; there's no need to read the macOS file.
- Put the cache under `$XDG_CACHE_HOME/poketokenbar/`.

---

## 6. UsageStore

### 6.1 Cadence and triggers

- `refreshInterval` defaults to 120 s. Presets: 0 (manual), 60, 120, 300, 900 (`S/Core/UsageStore.swift:951`, :221-223). The timer has 10% tolerance (:1030-1040).
- **Triggers:**
  - local midnight (`NSCalendarDayChanged`, :990-994)
  - system wake (:996-1000, :1088-1091)
  - display sleep suspends polling, with a 300 s probe (:1002-1011, :1047-1086, :307)
  - display wake resumes and refreshes
  - network reconnect (:1014-1023)
  - Settings changes
- **Coalescing:** a refresh requested during a refresh sets `pending`, and exactly one rerun happens afterwards (:1099, :1109-1112).
- **Empty retry:** if there are no errors and no snapshots after phase 1, retry once after 20 s (:1311-1323).

### 6.2 Phase 1: critical path (:1118-1213)

- Parallel `fetchDaily` for enabled providers.
- Per provider:
  - fetched → `today`, and a regression (decrease) is logged;
  - error → keep the previous `today` **only if** its date equals `todayKey`;
  - nil → no today.
- Carry over the previous block, week, month, series and lastUsage.
- Keep a snapshot only if `hasDisplayableUsage` (:2305-2315). That means any of:
  - `today != nil`,
  - block / week / month tokens > 0,
  - any series day > 0,
  - `lastUsage` ≤ 7 days old.
- `lastUpdated = now` only if there were no errors (or it's the first time and snapshots exist).

**Failure semantics** (`D/provider-extension.md:60-66`):

| `fetchDaily` result | Effect |
|---|---|
| throws | Recorded in `lastErrorDescription`; `lastUpdated` stops for the whole app. |
| nil | "Unused": the snapshot is dropped. |
| enrichment OK = false | Previous values are kept. |

### 6.3 Phase 2: enrichment (:1216-1260)

- Parallel. `blocksOK` updates the block; `periodsOK` updates week, month, series and lastUsage.
- New carrier snapshots are created when `hasDisplayableUsage`. Non-displayable snapshots are removed, and the list is sorted by registry order.
- **Then** `recordDailyLedger()` runs. It must run after phase 2 (`D/defect-log.md:648-654`).

### 6.4 Limits (after usage, :1262-1308)

- Claude disabled → clear limits.
- Keychain disabled and no session key → clear limits (macOS concept).
- 429 backoff active → skip.
- Otherwise `fetch(allowKeychainPrompt: false)`.
  - **Success:** set `limits`, `limitsAvailable`, `limitsUpdatedAt = now`; clear `authExpiry`, backoff and the manual error.
  - **Failure:** keep previous `limits` (`limitsAvailable = false` only if there were none); `updateAuthExpired`; apply 429 backoff.
- Then additional accounts, other providers, `checkLimitAlerts`, `onRefresh` (companion update + `grantCandies`; `S/PokeTokenBarApp.swift:286-289`).

### 6.5 Stale detection

- **Usage:** `isStale` if `lastUpdated` is nil, or `now − lastUpdated > (interval > 0 ? 2·interval : 1800 s)` (:886-890).
- **Limits:** 15 min since `limitsUpdatedAt` (:2086-2089); per additional account: `S/Core/ClaudeAccountRoots.swift:317-320`.
- **Today guard:** only snapshots whose `today.date == todayKey` count toward today (:323-343).

### 6.6 Persisted settings (UserDefaults)

| Key | Default |
|---|---|
| `refreshInterval` | 120 |
| `warnThreshold` | 80 |
| `critThreshold` | 95 (gap 5 enforced, :90-102, :952-954) |
| `showTokensInMenu` | true |
| `showCostInMenu` | false |
| `showLimitInMenu` | false |
| `showGrowthInMenu` | false |
| `limitDisplayMode` | used |
| `dailyTrendWindow` | 14d |
| `menuLimitColorMode` | gauge |

Also persisted, without simple defaults: `disabledProviderIDs`, `customScanRoots.<id>`, `additionalClaudeConfigDirs`, `claudeTrackedAccount`, `claudeAccountCandyArmed`, `disableKeychainAccess`, `dailyTokenLedger`, `dailyTokenLedgerSince`.

**Not persisted:**
- Snapshots (rebuilt from the cache).
- `notifiedTier` (in memory, so alerts can re-fire after a restart).

Parity file: `last-snapshot.json` (:2278-2302).

### 6.7 History and streak

- **Ledger (`S/Core/UsageRecap.swift:9-62`):**
  - Merge cross-provider `monthDailyTotals` with per-day **max** (a day never decreases).
  - `coveredSince` = min series date.
  - Prune days before Jan 1 of the previous year (`S/Core/UsageStore.swift:661-673`).
  - Load keeps only keys that parse as dates and values > 0.
- **Streak (`S/Core/UsageRecap.swift:83-97`):**
  - Gregorian calendar in the local timezone.
  - Start from today if today > 0, else yesterday.
  - Count consecutive days with ledger tokens > 0, stepping −1 calendar day.
  - `includesToday` = whether today was active.

### 6.8 Rare Candy (exact rule)

**Eligible windows (`S/Core/UsageStore.swift:776-845`):** for each Claude account:
- `five_hour` (if utilization is non-nil) → key `<prefix>.fiveHour`, kind session.
- `seven_day` → key `<prefix>.sevenDay`, kind weekly.
- `prefix` is `claude` for the default account and `claude.<first 8 hex of SHA-256(path)>` for additional accounts (`S/Core/ClaudeAccountRoots.swift:180-187`, :302-311).
- `epoch` = `ISO8601DateFormatter().string(resetDate)` (whole seconds, UTC `Z`), else the raw `resets_at`.
- Additional accounts qualify only after being seen below 100 once (`armedCandyWindows`, :850-862).
- Opus, Sonnet and scoped windows are excluded.

**`grantCandies` (`S/Core/CompanionStore.swift:1493-1531`):**
- Requires `limitsReady` (any provider's limits loaded, `S/Core/UsageStore.swift:871-874`).
- **First run:** seed tier = 1 (and the epoch) for windows ≥ 100; grant nothing.
- **Afterwards, `evaluateCandyGrants` (:1454-1488), per window:**
  1. If there is an epoch and a previous epoch, and `isNewEpoch` → clear the tier and store the epoch. A first-seen epoch is just stored.
  2. If u < 100 → clear the tier (rearm), store the epoch.
  3. Else, if the tier is unset → tier = 1; grant **1** (session) or **5** (weekly, `RareCandy.weeklyGrant`, `S/Core/CompanionModel.swift:227`).
- **`isNewEpoch` (:1436-1444):**
  - Strings must differ.
  - Parse each epoch: a number > 1e12 is ms, > 1e8 is seconds, else ISO8601.
  - If either side fails to parse → true.
  - Otherwise require `cur − prev` ≥ 1800 s (session) or ≥ 86400 s (weekly).
- Persist the state if anything changed. Each candy is worth 100M XP (:225).

### 6.9 Menu bar

**Lines (`S/Core/UsageStore.swift:354-373`):**
- Before the first refresh: the growth text or `—`.
- Usage items:
  - tokens: `compact(todayTotalTokens)`;
  - cost: `todayUsageCost.text(compact)`, only if some snapshot `reportsCost`;
  - growth text (companion).
- Limit line: `menuLimitParts` joined with ` · `.
- Layout:
  - limit present and ≥ 2 usage items → `[usage joined " · ", limit]`;
  - more than 2 usage items → `[first n−1 joined, last]`;
  - otherwise one line per item, then the limit.

**Claude limit item (:392-403, :456-462)** requires all of:
- `showLimitInMenu`;
- the `claude_code` snapshot has today tokens > 0;
- the tracked account has a `headlineWindow` (five_hour if its utilization is non-nil, else seven_day; `S/Core/Models.swift:321-325`).

Text: `"Claude " + percent(displayPercent(u))`, where remaining mode shows `max(0, 100−u)` (:472-478).

**Colors (:445-453):** `PaceTier.gauge(u, pace, warn, crit)`. Attention mode drops calm tiers; off shows none.

**Formatters (`S/Core/TokenFormatter.swift`):**
- `compact`: <1000 raw; <999,950 → `x.xK`; <999,950,000 → `x.xM`; else `x.xxB`; trailing zeros trimmed (:5-19, :54-59).
- `costCompact`: rounded to `%.1f` < 100 → `$x.x`; rounded `%.0f` < 10000 → `$x`; else `$x.xK` (:40-46).
- `percent`: `%.1f`, strip `.0` (:49-52).
- `cost`: `$%.2f`.

---

## 7. Official limits

### 7.1 OAuth path (`S/Core/OAuthLimitsProvider.swift`)

**Request:**
- `GET https://api.anthropic.com/api/oauth/usage` (:38).
- Headers: `Authorization: Bearer <accessToken>`, `anthropic-beta: oauth-2025-04-20` (:88-89). No explicit User-Agent, so Swift sends URLSession's default UA.
- Timeout 15 s.

**Response handling (:91-98):**
- 200 → decode `LimitStatus`.
- 429 → `rateLimited(Retry-After)`. Retry-After is parsed as seconds only, must be > 0, capped at 3600 (:103-108).
- Anything else → `httpStatus(code)`.

**401/403 (:58-68):**
- Invalidate the in-memory cache and re-acquire with `bypassCache`.
- If it's the same token → rethrow. Otherwise retry once.

**Profile (:143-153, :161-173):**
- `GET https://api.anthropic.com/api/oauth/profile` with the same headers.
- Requires a non-empty `account.email`. Optional: `organization.name`, `organization.plan_display_name`.
- Cached per token (at most 8 entries, cleared when full; failures are not cached).
- Best effort.

**Plan:** `subscriptionType` and `rateLimitTier` come from the credential (:70-72). The display rule is at `S/Core/Models.swift:281-317`:
- prefer `plan_display_name`;
- otherwise capitalize the subscription type and add a multiplier token from the tier: Max 5x → "Max", Max 20x → "Ultra", other multipliers → "Max Nx".

**`LimitStatus` JSON (`S/Core/Models.swift:228-369`):**
- `five_hour`, `seven_day`, `seven_day_opus`, `seven_day_sonnet`: each `{utilization: number 0–100, resets_at: ISO|null}` or null.
- `limits[]`: `{kind, group, percent, severity, resets_at, scope.model.display_name, is_active}`.
- `resets_at` example format: `2026-08-21T17:20:00.186770+00:00` (`T/SessionKeyLimitsTests.swift:31-37`).
- `hasNotStarted` = u == 0 and no reset (:238).
- `scopedLimitEntries`: all of `limits[]` if both legacy fields are nil, else excluding `session` and `weekly_all` (:330-334).
- `allUtilizations` = 5h, 7d, opus, sonnet + scoped percents (`S/Core/ClaudeAccountRoots.swift:250-254`).
- The Ultra plan sends `five_hour: null` (`D/defect-log.md:668-677`).

**Token acquisition (`accessToken`, :211-252):**
1. **Credentials file first** (default or `<root>/.credentials.json`). Requires `claudeAiOauth.accessToken` non-empty and not expired; then cache and return.
   - `expiresAt` may be a number or numeric string; > 1e10 means ms (:570-587).
   - "Expired" means `expiresAt ≤ now + 60 s` (:535-538).
2. If `!bypassCache` and the cached credential is unexpired → return it.
3. If no prompt is allowed → throw:
   - `credentialMissingAccountOAuth` if the file is valid JSON but `claudeAiOauth` is not an object (`null` counts too). For the default root this check runs only when the process env `CLAUDE_CONFIG_DIR` is empty (:329-343, :549-552).
   - otherwise `keychainInteractionNotAllowed`.
4. Keychain (macOS only; §8).

**Token refresh: deliberately none** (`D/defect-log.md:727-748`).
- Claude Code rotates the refresh token on use, so an external refresh would log Claude Code out.
- The repo names only `POST {baseURL}/v1/oauth/token` with `grant_type=refresh_token` and a `client_id`. **The `client_id` value and base URL are not in the repo**, and the app never writes credentials.
- **Linux consequence:** when the file token expires (~5 h lifetime per the defect log), automatic polls fail until Claude Code rewrites the file. Limits go stale after 15 min.
- **Go:** re-read the file on every poll (optionally watch it with inotify). Do not implement refresh.

**Errors and backoff (`S/Core/UsageStore.swift:1959-1994`):**
- 429 backoff: 300 → 600 → 1200 → 2400 → 3600 s (cap). Delay = Retry-After if given, else the interval.
- During backoff, automatic polls are skipped; a manual refresh bypasses it; success resets it.
- `authExpiry`: `sessionKeyInvalid` → `.sessionKey`; 401/403 → `.oauth`; other errors leave it unchanged.
- User-facing messages: `friendlyLimitError` (:1998-2021).

### 7.2 Session-key path (`S/Core/SessionKeyLimitsProvider.swift`)

**Storage:**
- `<stateDir>/session-key.json` = `{"key": "...", "organizationID": "..."?}`, mode 0600 (:72-118).
- `stateDir` = `PTB_STATE_DIR` or `~/Library/Application Support/PokeTokenBar` (`S/Core/AppStatePaths.swift`).
- Per additional account: `session-key-<pathKey>.json` (:87-90).

**Validation (:122-128):** trim; must start with `sk-ant-`; length 40–2048; no control characters.

**HTTP (:31-56):**
- Base `https://claude.ai/api` (:156).
- `GET /organizations` and `GET /organizations/{uuid}/usage`.
- Headers: `Cookie: sessionKey=<key>`, `Accept: application/json`, `User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15`, `Referer: https://claude.ai`, `Origin: https://claude.ai`.
- Cookie jar disabled; timeout 15 s.
- **Cloudflare challenges curl (`cf-mitigated: challenge`) but lets URLSession through (:14-15).** Whether Go's `net/http` (TLS fingerprint) passes was not determined.

**Status mapping (:263-270):** 200 ok; 401 → `sessionKeyInvalid`; 429 → `rateLimited(retryAfter)`; anything else → `httpStatus`.

**Fetch (:169-207):**
- No stored key → `sessionKeyMissing`.
- If an org is cached → fetch its usage. A 403 there → rediscover.
- Rediscovery (:219-252):
  - Decode the org list `[{uuid, name, capabilities}]`; a decode failure → `credentialFormat`.
  - Keep orgs whose `capabilities` contain `chat` (rows with no capabilities are kept). If none remain, use all rows.
  - Fetch usage for each org in parallel; drop orgs whose usage call fails with an HTTP status.
  - Return them in list order.
- A 403 on the org list → `sessionKeyInvalid`.
- **Pick** the first org with usage data (any utilization/percent > 0 or any `resets_at`, :273-277), else the first org. None → `sessionKeyNoOrganization`. Save the picked org id.
- The response schema matches `LimitStatus`; there is no profile or plan on this path.

**Chain (`ChainedLimitsProvider`, :297-337):**
- Session key first.
- On any error, fall back to OAuth. If a key is configured, the fallback runs with `allowKeychainPrompt = false`.
- The error surfaced is the session-key error if a key is configured, else the fallback's error.

### 7.3 Presentation

- **Used vs remaining:** `max(0, 100−u)` (`S/Core/UsageStore.swift:472-474`).
- **Reset countdown:** SwiftUI relative-date style (`Text(reset, style: .relative)`) plus ` (HH:mm)` if the reset is today or ≤ 6 h away, else ` (EEEE d HH:mm)` localized (`S/UI/PopoverView.swift:704-723`). **There is no exact string to port.**
- **Popover rows:** 5h (+ forecast), weekly, opus, sonnet, scoped entries, current block (`S/UI/PopoverView.swift:980-1010`).
- **PaceTier (`S/Core/PaceTier.swift`):**
  - nil if there is no pace or pace < 0.1 (:12-16).
  - u ≥ crit → wayOver.
  - Otherwise `delta = round(u − pace·100)` (half away from zero): `<−25` wayUnder; `<−10` under; `<5` onPace; `<15` slightlyOver; `<30` over; else wayOver (:15-31).
  - `gauge` fallback without a pace: ≥ crit wayOver, ≥ warn over, else onPace (:36-41).
  - Colors: blue, teal, green, yellow, orange, red (`S/UI/PopoverView.swift:1950-1958`).
- **Alerts (`S/Core/UsageStore.swift:2129-2149`, :2185-2247):** edge-triggered per window key. Tier 0 = below warn (removed from the map), 1 = warn, 2 = crit. An alert fires only when the tier rises. Window keys look like `<prefix>.fiveHour`, `.sevenDay`, `.sevenDayOpus`, `.sevenDaySonnet`, `.scoped.<kind>.<model>.<i>`.
- **`isLimitWarning`:** any utilization of the tracked account ≥ crit, or a forecast with `beforeReset` (:727-744).
- **Multi-account:**
  - Tracked account modes: automatic (latest `history.jsonl` mtime), default, highest, pinned (:1396-1431).
  - Duplicate identities (same email + org) are merged (:1514-1540).
  - Usage attribution: `S/Core/ClaudeAccountUsage.swift:43-120`. A turn belongs to the account whose latest prompt in that session is ≤ the turn time; otherwise to the account with the earliest prompt.

---

## 8. macOS-specific pieces → Linux replacement

| Swift piece | Ref | Linux replacement |
|---|---|---|
| Keychain reads (no-UI queries, account prioritization, service-name hashing, prompt-declined logic) | `S/Core/KeychainAccess.swift:3-74`; `S/Core/OAuthLimitsProvider.swift:243-302`, :353-524; `S/Core/ClaudeAccountRoots.swift:173-176`; `S/Core/UsageStore.swift:1524-1554`, :1678-1681 | Drop all of it. Read the credentials file only. A "manual refresh" just bypasses the in-memory cache and re-reads the file. |
| `disableKeychainAccess` / `KeychainAccessGate` | `S/Core/UsageStore.swift:199-219`; `S/Core/KeychainAccess.swift:8-15` | Drop, or turn into an "official limits off" toggle. |
| App Support files (`usage-cache.json`, `session-key.json`, `last-snapshot.json`) | `S/Core/LocalUsageCache.swift:219-224`; `S/Core/AppStatePaths.swift` | `$XDG_CACHE_HOME/poketokenbar/` for the cache; `$XDG_STATE_HOME` (or `$XDG_CONFIG_HOME`) `/poketokenbar/` for the key (0600) and state. Keep a `PTB_STATE_DIR` override. |
| UserDefaults | `S/Core/UsageStore.swift:949-980` | JSON config file, or GSettings owned by the extension. |
| Login-shell env lookup | `S/Core/UsageEnvironment.swift`; `S/Core/BinaryLocator.swift:119-199` | Process env, then a one-time `$SHELL -ilc` or `systemctl --user show-environment`. |
| Claude Desktop embedded roots | `S/Core/LocalUsageReader.swift:122-125` | Not applicable; Linux equivalent not determined. |
| `NSCalendarDayChanged` / `didWake` / `screensDidSleep`/`Wake` / reachability | `S/Core/UsageStore.swift:990-1023` | Timer to next local midnight; logind `PrepareForSleep(false)`; `org.gnome.ScreenSaver.ActiveChanged`; NetworkManager `StateChanged`. |
| App Nap activity | :1103-1104 | Not applicable. |
| `UNUserNotificationCenter` | :2249-2261 | `org.freedesktop.Notifications`, or let the extension notify. |
| `AppEnv.isBundledApp` gates (live fetch, persistence, discovery) | `S/Core/AppEnv.swift` | An explicit flag; tests inject roots and HTTP. |
| Lowercased path dedupe (APFS) | §1.2 | Case-sensitive. |
| URLSession default UA on `oauth/usage` | `S/Core/OAuthLimitsProvider.swift:87-91` | Go sends `Go-http-client/1.1`; whether that is accepted is unverified. |
| `Calendar.current.firstWeekday` | `S/Core/LocalUsageReader.swift:2141-2143`; `S/Core/UsageRecap.swift:137-142` | Explicit week-start setting. |
| `~/.claude.json` identity, default creds path | §1.2 | Honour `CLAUDE_CONFIG_DIR`. |

---

## 9. Defect-log entries relevant to this pipeline (`D/defect-log.md`)

- **:47-56** A disabled provider must also skip and clear its official limits and auth notices.
- **:93-104** Price tests need an independent bucket-level oracle (Sonnet 5.5 cache-read 0.1, not 0.2). Price-only changes don't bump the parser version.
- **:148-158** An idle day must not erase history: carriers are kept for any positive block, period or series.
- **:159-170** Calendar totals aren't a "recently used" signal: keep the tab if `lastUsage` ≤ 7 days, and order by registry.
- **:240-250** Unavailable cost is not zero: keep source, estimate and unknown separately through every aggregation.
- **:252-257** Incremental parsing must keep the latest cumulative ledger with the raw entries and re-attribute after merge; drop it on a full reparse.
- **:266-273** An entry existing doesn't prove usage. Zero-token `<synthetic>` records must not create blocks or tabs; test with non-empty zero-token input.
- **:274-280** JSON `null` is not "present". Use typed casts (`claudeAiOauth: null` means logged out). Tests need absent, null and valid cases.
- **:307-321** Clamp parsed numbers at one boundary to a sum-safe cap (1e18), not `Int.max`.
- **:323-333** The ledger stores a coverage start date separately and validates keys by parsing them.
- **:381-392** Failure semantics: throw stops `lastUpdated`; nil drops the tab; OK = false keeps old values.
- **:477-497** No family price fallback. Add exact rows for every model actually seen in logs; never assert a current model is unpriced.
- **:503-509** Copies and replays: dedupe on a global turn id, use the earliest turn time, keep all session ids, and count subagents once.
- **:510-515** There are several log roots (`CLAUDE_CONFIG_DIR` comma list, XDG-style path, Desktop); add new roots in one place.
- **:516-528** Custom roots only add to the defaults; drop ancestors and `/`.
- **:529-550** GUI processes don't inherit the shell env. Use one registry and one shell spawn; an empty value means unset.
- **:551-563, :575-579** Desktop scan: depth 7 (`>=` check); name-pruning only for `node_modules`/`.git`/`venv`/`.venv`.
- **:564-567, :583-586** Never hold a lock while waiting on a shell or a filesystem walk; don't trigger the shell lookup from incidental paths.
- **:587-598** Tests must be hermetic (inject roots and home), and multi-root seams must exercise the loop.
- **:599-613** Never cache decisions that depend on other files, or transient read failures; keep the old blob under its old signature.
- **:630-646** Don't read unbounded files whole. Swift's Claude path still uses `readToEnd` from the offset; Go should stream.
- **:648-654** Derived state (ledger) is written after the phase that fills it; test the first refresh.
- **:668-677** Plans vary (Ultra has no 5h). Narrow surfaces use `headlineWindow`.
- **:707-721** No app-owned secret store; automatic polls never touch a prompting credential store.
- **:727-748** Never use Claude's `refreshToken`: it rotates and logs Claude Code out.
- **:749-765** The token cache must re-peek the file before using a cached token (account switch, #227); a manual refresh bypasses the cache (#300).
- **:783-787** "No credential" and "account OAuth missing" get distinct messages.
- **:1246-1261** Candy rearm uses the epoch, normalized to seconds, with a window-sized advance threshold (30 min / 24 h); first-seen epochs don't rearm.
- **:1263-1268** Never put volatile fields (`resets_at`) in alert keys; alerts are edge-triggered and tested as pure functions.

---

## 10. Tests worth porting (input → expected)

### Parsing, dedupe, roots: `T/LocalUsageReaderTests.swift`

The line fixture helper is at :86-90.

| Test (lines) | Input → expected |
|---|---|
| `testClaudeDedupKeepsMaxOutput` (92-107) | A/R1 twice (out 5, then 200; in 100; cr 1000) + B/R2 → 2 entries; A.output = 200, cacheRead = 1000. |
| `testClaudeDailyAndCost` (109-121) | opus-4-8, 1M input → daily total 1,000,000, cost $5.00; another day → nil. |
| `testConfigDirParsingHandlesCommasWhitespaceAndTilde` (211-224) | `" /a/one , ,~/two "` → `/a/one/projects`, `~/two/projects`, defaults present, no `/projects`. |
| `testNormalizedRootsDropsDuplicatesAndNestedRootsKeepingOrder` (240-253) | 5 roots → `[.claude/projects, .config/claude/projects, .claude/projects-other]`. |
| `testNormalizedRootsFoldSymlinkedDuplicates` (227-236) | Real root + symlinked root → 1. |
| `testMultipleRootsSumButShareGlobalDedup` (257-273) | A in both roots + B → 2 entries, total 120; first root alone → 1. |
| `testActiveBlockIgnoresZeroTokenSyntheticEntries` (1080-1097) | Synthetic only → nil; + real (10+5) → 15 and id `block-<real secs>`. |
| `testPeriodAndActiveBlock` (1054-1075) | Noon; 30 min ago 600k, 10 h ago 999 → block 600,000 and tpm > 0. |
| `testEnrichmentScanStartCoversAllWindows` (1104-1134) | Lower bound ≤ every window. Late month (7/28) → monthStart; mid month (7/20) → rolling start. |
| `testClaudeParsingClampsAbsurdTokenCountsInsteadOfTrapping` (1142-1150) | 1e30 → 1e18. |
| `testParsingStillFoldsMissingAndNegativeToZero` (1177-1185) | −5, null, `"nope"` → 0. |
| `testRollingDailySeriesReturnsExactWindowCountAndFillsMissingDaysWithZero` (1242-1272) | now 10-15: 7d series 10-09…10-15, last 280, [5] = 150, [0] = 0; 14d starts 10-02; 21d starts 09-25. |
| `testPricingExactAndFallbackAndZero` (60-82) | opus-4-8 1M in = 5, 1M out = 25; fable-5 all four buckets = 73.5; fable-5-1 cache read 0.25; unknown → 0; `claude-opus-6` → zero. |

### Cache: `T/LocalUsageCacheTests.swift`

| Test (lines) | Input → expected |
|---|---|
| `testUnchangedFileIsNotReparsed` (235-247) | Same size and mtime, content swapped → old value 111. |
| `testMultipleRootsAreScannedAndDedupedAcrossRoots` (251-272) | Outputs {10, 7}; shared id counted once. |
| `testChangedFileIsReparsed` (275-284) | New mtime → 999. |
| `testAppendedFileParsesOnlyTheNewTail` (298-311) | Prefix rewritten in place 111→222 + appended 333 → {111, 333}. |
| `testTruncatedFileIsReparsedFromStart` (314-325) | → [333]. |
| `testHalfWrittenTrailingLineIsRetriedOnNextRefresh` (329-342) | → [111], then {111, 222}. |
| `testDiskRoundTripAcrossInstances` (800-812) | Reload keeps 42. |
| `testPruneDropsBlobsOlderThan40Days` (815-829) | 45-day-old blob is absent from disk; both still returned. |
| `testSaveThrottle60s` (832-850) | +30 s no write; +61 s write. |
| `testModifiedSinceFilters` (853-860) | → [2]. |
| `testEnrichmentScanStartCatchesWeekStraddlingMonthBoundary` (958-993) | monthStart bound misses the file; enrichment bound includes it; week > 0. |
| `testFormatterEdges` / `testFormatterRoundingBoundaries` (895-921) | `$48.10`, `$9.5`, `$311`, `$12.3K`, `88%`, `88.3%`, `80%` (79.96), `100%` (99.97), `$100` (99.96), `$99.9`, `$10.0K` (9999.6), `$9999`. |

### Cost: `T/ClaudeReportedCostTests.swift`

Helpers at :15-27. All 12 tests are worth porting:

| Test (lines) | Input → expected |
|---|---|
| Incremental re-attribution (42-65) | Ledger 1 → 9; 2 entries at 4.5 each. |
| Ledger kept when the tail has none (67-86) | 4 → 2 + 2. |
| Truncation drops the ledger (88-104) | nil and unknown. |
| Half-written ledger (106-122) | Retried → 9. |
| Unpriced model with `[1m]` key (126-141) | 12.5, reported. |
| Variant pooling (144-150) | 4 + 1 = 5. |
| Ledger beats table (153-164) | 7.25. |
| Last ledger wins (167-175) | 9. |
| Split by tokens (179-195) | 300/100 tokens → 7.5 / 2.5. |
| Unattributable model dropped (198-204) | 2. |
| No ledger (207-221) | Haiku 1M → 1.0 estimate; unpriced → 0 unknown. |
| Zero-token entries (224-231) | 6. |

**Also:**
- `T/UsageCostTests.swift:140-192`: sonnet-5-5 in/out/cw/cr = 100/10/40/1000 → 1,150 tokens, $0.0005 estimate, for cold, warm and persisted caches and every aggregate.
- `T/ModelPricingTests.swift:18-46`: Claude rates; every model seen in logs priced for in/out/cw/cr = 2/175/26,939/42,676. Add the mythos and haiku-5-5 models in Go.

### Series: `T/MonthDailyTrendTests.swift`

| Test (lines) | Input → expected |
|---|---|
| `testCrossMonthSessionDoesNotLeakLastMonthIntoTheSeries` (101-140) | One file with 6/29 and 7/8 turns, now 7/10 → series 07-01…07-10; 7/8 output 300; Σ = month total. |
| Series vs month total (144-160) | Equal. |
| `lastUsage` (164-175) | 7/6, or nil for synthetic only. |
| Explicit zeros (180-190) | All days present. |
| Stops at today (193-198) | 10 entries. |
| DST and month lengths (210-235) | Seoul, LA, Lord Howe, Chatham, Berlin × 12 months + 2028-02-29 → count == day of month, no duplicates. |
| `testTwentyOneDayScanIncludesEarlyMorningTranscriptOnFirstDayAcrossDSTFallBack` (726-794) | LA, now 2026-11-02T07:59Z; transcript 2026-10-12T07:30Z with 123 tokens → scanStart 2026-10-12T07:00Z; 21-day series first day 2026-10-12 = 123, last 2026-11-01. |

### Limits: `T/SessionKeyLimitsTests.swift`

Fixtures at :11-41 (orgs: personal/chat, api-only, team/chat; idle usage; active usage 7/30; forbidden).

| Test (lines) | Input → expected |
|---|---|
| Skip forbidden org, pick the active one (163-180) | 5h = 7, 7d = 30; org-team saved. |
| Cached org skips listing (193) | Org list not requested. |
| Cached org gets 403 (209) | Rediscovery runs. |
| API-only orgs (226) | Not probed. |
| Org list 403 (333) | `sessionKeyInvalid`. |
| Usage 401 (358-372) | `sessionKeyInvalid`, no rediscovery. |
| 429 (375-388) | `rateLimited(120)`. |
| Malformed key (145) | Rejected. |
| Storage (131) | File mode 0600. |
| Chain tests (428-512) | Key preferred; fallback without key; key error surfaced; configured key never lets the fallback prompt. |

### Credential cache: `T/CredentialSwitchCacheTests.swift`

| Test (lines) | Input → expected |
|---|---|
| Account switch in place (34-52) | Token A, then the file is rewritten with B → B, and the plan updates. |
| File deleted (56-67) | Cached A kept. |
| `{"claudeAiOauth":null}` (72-83) | Cached A kept. |

### Store, alerts, candy, streak

**`T/UsageStoreTests.swift`:**

| Test (lines) | Input → expected |
|---|---|
| Limit alerts once per tier (509-521) | 80 → warn; 81–94 → none; 95 → crit; 96, 99 → none. |
| Rearm (534-541) | 82, 40, then 85 → warn again. |
| `testMenuShowsTheWeeklyLimitWhenThePlanHasNoSessionLimit` (1849-1866) | Weekly 66%, reset in 3.5 d → `Claude 66%`, pace 0.5; remaining mode `Claude 34%`. |
| `testHeadlineWindowPicksTheSessionThenTheWeeklyWindow` (1880-1893) | Session first, then weekly, else nil. |
| `testLimitWarningFromForecastAtFullUtilization` (915-923) | u = 100, crit = 101 → warning. |
| `testBurnTierThresholds` (982-999) | 500 / 50k / 200k / 500k → idle / normal / fast / blazing. |
| `testMidnightCarrierSnapshotFromActiveBlock` (1013-1028) | Carrier kept, today nil, week total 90M, burn fast. |
| `testWarningStaysBelowCritical` (948-963) | Thresholds kept 5 apart. |
| `testIsStaleBeforeFirstRefreshThenFreshAfter` (1513-1519) | Stale before the first refresh, fresh after. |

Also port the carrier, recent-window and stale-date tests (808, 847, 1100, 1121, 1188) and the auth-expiry tests (1425-1512).

**`T/RareCandyTests.swift`:**
- Session → 1 (34-41); weekly → 5 (43-49); 99.9 → none (51-58).
- No double grant at the same epoch (61-69); rearm after < 100 (72-83).
- Epoch advance from 10:00Z to 15:00Z → regrant (87-100); microsecond change → none (103-116); Codex ±1 s → none (119-137); 1- and 5-minute drift → none (140-157).
- +18000 s → regrant (160-173); +7 d weekly → 5 (176-189); first-seen epoch → none (192-201).
- First run seeds without granting (290-296); no seed when limits aren't ready (307-312).
- Persistence across restart (332-365).

**`T/UsageStreakTests.swift`:**
- 09-16…18 active, now the 18th → 3 including today (45-50).
- Today unused → 2, not including today (53-57).
- Gap on the 16th → 2; a lapsed run → none (59-66).
- Across the 1st of the month → 4 (70-74).
- DST in LA: spring 03-09 → 1; fall 10-31…11-02 → 3 (79-88).

**`T/UsageRecapTests.swift`:** `testPruningForgetsTheCoverageItDrops` (:80) and `testTheFirstRefreshAlreadyFillsTheLedger` (:150). Bodies not read.

**`T/PaceTierTests.swift`:**
- At pace 0.5, crit 95: 0/24 → wayUnder; 25/39 → under; 40/54 → onPace; 55/64 → slightlyOver; 65/79 → over; 80 → wayOver (13-31).
- 54.6 → slightlyOver, 54.4 → onPace (33-38).
- Crit overrides pace (41-44).
- Pace below 0.1 → nil (51-56).

**`T/LimitPaceTests.swift`:**
- Pace fraction = 0, 0.5, 0.75, 1 for reset in 5h, 2.5h, 1.25h, 0 (56-67).
- nil for a reset in the past, a reset beyond the span, or span ≤ 0 (70-81).

---

## Summary

**Logic to port (Claude-relevant LOC, approx.):**

| File | LOC |
|---|---|
| `LocalUsageReader` | ~800 of 2239 |
| `LocalUsageCache` | ~250 of 646 |
| `UsageStore` | ~1200 of 2315 |
| `OAuthLimitsProvider` | ~250 of 588 (without the Keychain) |
| `SessionKeyLimitsProvider` | 337 |
| `Models` | ~370 |
| `ClaudeAccountRoots` | 329 |
| `ClaudeAccountUsage` | 207 |
| `ModelPricing` | 143 |
| `CustomScanRoots` | 135 |
| `UsageEnvironment` | 83 |
| Ledger/streak (`UsageRecap`) | ~100 |
| Candy logic | ~110 |
| `PaceTier` + `TokenFormatter` + `UsageCost` | ~150 |

**Top 5 risks:**
1. The session-key path depends on getting past Cloudflare, and a Go TLS/UA fingerprint may be challenged. `oauth/usage` will also see a different UA. Neither was verified; don't probe with real credentials.
2. Without a Keychain and without a refresh flow (deliberate: refresh rotates the token and breaks Claude Code), limits go stale until Claude Code rewrites the credentials file.
3. Cost-state attribution is per file. Subagent costs are probably double-counted. Three local models are unpriced (`claude-mythos-5-1`, `claude-mythos-5-2`, `claude-haiku-5-5`), and the 5m/1h cache-write split is ignored.
4. Dedupe has exact semantics to preserve: keep the max total, use the earliest date, union the sessions, dedupe globally, and `"|"` collapses ids. The tail watermark keeps the ledger, and cache hits require exact mtime equality.
5. Calendar semantics: day keys are baked into the cache, week start depends on the locale, day stepping must be DST-safe, and the scan lower bound is the min of five windows. There are also Linux path deltas (`CLAUDE_CONFIG_DIR` for `.claude.json`/`.credentials.json`, case-sensitive paths).

**Could not determine:**
- The refresh `client_id` and base URL (not in the repo, intentionally unused).
- The Linux Claude Desktop root.
- Whether Go clients pass claude.ai's Cloudflare.
- What the cost-state totals cover.
- An exact countdown string (SwiftUI-rendered).