# Porting spec: Usage recap, 16-week heatmap, streak badge, Activity click-cycle

Scope: PokeTokenBar (Swift/macOS) to the GNOME port (GJS extension + Go helper).
Status: research only; nothing in either repo was modified.

Citation conventions. Swift paths are relative to the upstream repo root (the Swift PokeTokenBar checkout). `UR` = `Sources/PokeTokenBar/Core/UsageRecap.swift`, `RV` = `Sources/PokeTokenBar/UI/UsageRecapView.swift`, `AH` = `Sources/PokeTokenBar/UI/ActivityHeatmap.swift`, `AV` = `Sources/PokeTokenBar/UI/ActivityVisualization.swift`, `SB` = `Sources/PokeTokenBar/UI/StreakBadge.swift`, `PV` = `Sources/PokeTokenBar/UI/PopoverView.swift`, `LZ` = `Sources/PokeTokenBar/Core/Localization.swift`, `US` = `Sources/PokeTokenBar/Core/UsageStore.swift`, `TF` = `Sources/PokeTokenBar/Core/TokenFormatter.swift`. `URT` = `Tests/PokeTokenBarTests/UsageRecapTests.swift`, `AVT` = `Tests/PokeTokenBarTests/ActivityVisualizationTests.swift`. Port paths are relative to this repo root.

Swift is not installed. Every number in section 5 was derived by hand from the Swift source and the Swift assertions; where a value is my derivation rather than a literal Swift assertion it is marked (derived).

---

## 0. What already exists in the port (read before sizing)

| Piece | State | Evidence |
|---|---|---|
| Day ledger (`Merge` never decreases, `CoveredSince`, `Prune`, `Sanitize`, `Record`, persistence to `usage-ledger.json`, written after the enrichment phase) | **Done in Go**, tests already cite Swift `URT:55,65,80,88,100,150,174` | `helper/internal/usage/ledger.go`, `ledger_test.go`; recorded at `helper/internal/daemon/daemon.go:717`; loaded+sanitized at `daemon.go:201-204` |
| Retention (Jan 1 of last year) | Done | `usage.LedgerCutoff`, `ledger.go` |
| Streak (`Streak(now)`, `{days, includesToday}`) | **Done in Go and on the wire** as `usage.combined.streak`; 7 Swift cases (`UsageStreakTests.swift:45-88`) ported | `ledger.go`; `helper/internal/daemon/build.go:100`; `docs/protocol.md:336` |
| Streak badge in the extension | Done, simplified (emoji, opacity instead of an orange/gray flame), sits in the "Today's tokens" caption row | `extension/lib/popover/usageSection.js:34-37,115-124`, `stylesheet.css:320` |
| Week start | Done: `weekStart` ISO 1-7 forwarded via `setSettings` (`protocol.md:790`); Go `Settings.weekday()` (`daemon/settings.go:81`), `usage.StartOfWeek(t, first)` (`usage/calendar.go:44`) | |
| Day arithmetic that is DST-safe | Done: `usage.midnight` (unexported), `StartOfDay/Week/Month`; JS `ChartModel.addDays/dayDifference` (UTC civil arithmetic) | `calendar.go`, `chartModel.js:60-74` |
| Bar chart as a Cairo `St.DrawingArea` with `Clutter.MotionController` hover and the shared tooltip | Done | `extension/lib/chart.js` |
| Form display name (`formDisplayName`, Unown letter rule), dex entries with `CaughtAt`/`ReleasedAt`, `Store.entryName` | Done in Go but **unexported** | `helper/internal/companion/forms.go:262`, `state.go:76,84`, `dex.go:248` |

**Missing (everything below is new work):**
1. Go: `RecapPeriod`, `UsageRecap.make` equivalent (buckets, totals, comparison, best day/streak, `canGoBack`), the heatmap window (`recentGrid` data), graduates query on the companion store, the `getRecap` command, the `usage.combined.heatmap` state field, arg validation, contract fixture.
2. Protocol: the two additions of section 4, a new GSettings key `activity-view`.
3. JS: heatmap model + Cairo widget, Activity panel (bars/heatmap container with page dots and click-cycle), recap screen (card, scope picker, period navigation), Details button, popover navigation (recap replaces tabs/scroll/footer), i18n strings, CSS.
4. Note that `ui.md:192,201` currently marks Details/recap and the click-cycle as LATER and says to hide them; this spec is the M2 content for those lines.

---

## 1. Usage recap screen

### 1.1 What it shows

A single read-only screen, opened from the **Details** capsule button (`PV:281-295`, `LZ:247` "Details"; tooltip and accessibility label `recapOpen` = "Usage recap"). It replaces the whole popover body exactly like Settings (`PV:100-117`; `showingRecap` at `PV:42`, cleared on every popover reopen by `PopoverNavigation.reset()` at `PV:56-59`). There is no persisted recap state: it always opens on scope Week, offset 0 (`RV:355-357`).

There is **no per-model, per-project or per-provider breakdown** anywhere in the recap. It is totals of tokens only (no cost). Aggregations: one total, one best day, active days, best streak, graduated count, plus a previous-period comparison chip. The data source is only the ledger (`store.dailyLedger`, `RV:24`); the live month series is **not** overlaid here upstream (contrast the heatmap, section 2.2); the port overlays it anyway (see Q2, resolved).

### 1.2 Scopes, periods, ranges

- Scopes: `week`, `month`, `year`, default `week` (`UR:100`, `RV:355`).
- A period is a **calendar** week, month or year, not a rolling window (`UR:112-143`). Weeks start on the user's first weekday (`UR:135-142`, `calendar.firstWeekday`); the port takes the helper's `weekStart` setting. Year runs Jan to Dec. Gregorian, local time zone.
- `offset`: 0 is the running period, -1 the one before, never positive (`RV:356-357,396`). The period for offset `k` is the running period shifted by `k` units of the scope (`UR:130-133`; `RV:22-23`). Month shifting starts from the 1st so "31 March - 1 month" is February, not March 3 (`URT:212-224`, test `testSteppingBackLandsOnWholePeriods`).
- Period `start` is the first instant, `end` the first instant after (`UR:116-119`). Ledger day keys are `yyyy-MM-dd`; all comparisons are string comparisons (`UR:36-37`).
- Retention bound: the ledger holds this year and last year only (`US:662-673`), so the Year view can step back exactly one year; Week/Month can step back until the ledger's `coveredSince` (see `canGoBack`).

### 1.3 Exact computation rules (`UsageRecap.make`, `UR:190-268`)

Inputs: ledger, dex entries, period, `now`, calendar. `today = startOfDay(now)` (`UR:193`). Day stepping is by calendar day, never +86400 s (`UR:195`; the port computes in civil-date space, see 6.4).

Definitions:
- `known(key) = ledger.tokens(key) > 0 || ledger.covers(key)` (`UR:206`). `covers(key) = coveredSince != nil && key >= coveredSince` (`UR:20`).
- `periodDays` = every day of the period (`UR:208`, `days()` at `UR:196-205`).
- `elapsed` = the period days that are `<= today`, each as `{key, tokens = ledger.tokens(key)}` (`UR:209-212`). Future days are excluded.

Outputs:
1. `total = sum(elapsed.tokens)` (`UR:259`).
2. `isInProgress = period.start <= today < period.end` (`UR:240`).
3. `buckets` (`UR:220-238`):
   - **week, month**: one bucket per period day (7, 28-31). `key` = that day; `tokens = ledger.tokens(key)` (future days are 0 in a ledger anyway, no clamp is applied); `isCurrent = (day == today)`; `hasData = day <= today && known(key)` (`UR:223-227`).
   - **year**: 12 buckets, one per month. For month `i`: `start = periodStart + i months`, `end = start + 1 month`; `keys` = days of the month that are `<= today`; `tokens = sum(ledger.tokens(keys))`; `key` = first day of the month; `isCurrent = start <= today < end`; `hasData = any(known(k) for k in keys)` (`UR:229-237`). A month with no elapsed day (future month) therefore has `keys = []`, `hasData = false`, `tokens = 0`.
4. `bestDay` = the elapsed day with the highest tokens among days with tokens > 0, else nil (`UR:262`). Tie-break: Swift `Sequence.max(by:)` keeps the first maximal element, so the **earliest** day wins ties (derived from stdlib behaviour, not asserted by any test; implement "earliest wins").
5. `activeDays = count(elapsed where tokens > 0)` (`UR:263`). `countedDays = count(elapsed where known(key))` (`UR:264`). Displayed as `activeDays/countedDays` (`RV:211`).
6. `bestStreak` = longest run of consecutive elapsed days with tokens > 0, **inside the period only** (a run is not carried in from the previous period) (`UR:213-218`).
7. Comparison (`UR:241-248`):
   - `previous = period.shifted(-1)`; `previousDays` = its day keys.
   - `compared = isInProgress ? previousDays.prefix(elapsed.count) : previousDays` (same number of days while running, whole period otherwise, `UR:243-245`). Year compares by day-of-year position, so a leap year is off by one at most; keep as is.
   - `previousTotal = (ledger.covers(compared.first)) ? sum(tokens over compared) : nil`. Coverage is judged on the **oldest** day of the compared range; this was a regression fix, pinned by `testAHalfCoveredPreviousPeriodIsNotAComparison` (`URT:283-289`).
   - `delta = previousTotal > 0 ? (total - previousTotal) / previousTotal : nil` (`UR:185-188`). nil previous or previous 0 means no chip.
8. `canGoBack = previousDays.last is covered` i.e. `ledger.covers(lastDayOfPreviousPeriod)` (`UR:267`). It uses the **last** day of the previous period (any covered day in it), unlike the comparison which needs the first.
9. `graduated` = dex entries with `!isReleased && caughtAt != nil && period.start <= caughtAt < period.end`, sorted by `caughtAt` descending (`UR:250-254`). "Graduated" here = dex entries. Released entries are excluded (`isReleased` = `releasedAt != nil`).

### 1.4 Empty and degenerate states

- Empty ledger: still draws the period: 7/28-31/12 buckets all `hasData=false` (every bar fully dim), `total 0`, `bestDay nil` ("--"), `previousTotal nil` (no chip), `canGoBack false`, `activeDays 0/0` (`URT:320-330`).
- No chip when the ledger does not reach the compared range (`RV:233-246`).
- Graduates strip empty text: `recapNoGraduates` (`RV:316-318`).
- Bucket tooltip/aria for unknown days: `recapNoData` (`RV:109`); known-zero shows `0`.
- Footer lines under the card (caption, secondary) (`RV:401-409`): the `recapBestDayLine` sentence if `bestDay` exists; `recapCompareSoFar(scope)` only if `isInProgress && delta != nil`.

### 1.5 Navigation

- Open: Details button. Close: **Back** button (`RV:364-368`, label `back`, chevron.left icon). Upstream binds Esc to Back (`keyboardShortcut(.cancelAction)`); in GNOME Esc closes the whole menu, so the port has Back only (no Esc rebind).
- Scope picker: segmented Week / Month / Year at the top right, `controlSize(.small)`; changing the scope resets `offset = 0` (`RV:370-377`).
- Time row (caption, borderless buttons) (`RV:379-399`): left **Previous** (icon arrowtriangle.left.fill + text), disabled when `!canGoBack`; center button "This week/This month/This year" (`recapCurrentPeriod(scope)`), disabled when `offset == 0`, sets offset 0; right **Next** (text + arrowtriangle.right.fill), disabled when `offset >= 0`. Previous does `offset -= 1`, Next `offset += 1`.
- The period itself is not repeated in this row: it is printed on the card header (`RV:379`).

### 1.6 Layout (detail level of ui.md)

Popover content width is `PopoverMetrics.contentWidth`; in the port that is 332 px (the usage section comment, `usageSection.js:32`). Outer padding `PopoverMetrics.padding` (`RV:412`). Vertical stack, spacing 8: (1) top row, (2) time row, (3) card, (4) caption lines, (5) flexible spacer.

**Card** (`RecapCard`, `RV:118-347`): fixed size 332 x 226 (`RV:121,137`), padding 12, VStack spacing 8: header, screen, graduate strip. Colors are explicit and **do not follow the light/dark theme** (`RV:150` forces `.light`):
- shell gradient top to bottom `rgb(0.84,0.21,0.18)` = `#D6362E` to `rgb(0.66,0.12,0.11)` = `#A81F1C`; radius 10; a 5 px wide `black@0.14` strip on the left edge (the hinge) (`RV:122-123,138-149`).
- screen ink `rgb(0.05,0.07,0.08)` = `#0D1214`; LCD green `rgb(0.51,0.98,0.62)` = `#82FA9E`; amber `rgb(1.0,0.73,0.28)` = `#FFBA47` (`RV:124-126`).

Header row (`RV:154-173`, leading padding 2, spacing 6): lens (16 px circle: radial gradient `(0.72,0.88,1.0)` to `(0.15,0.45,0.85)` centered at (0.35,0.3) radius 11, white@0.9 1.5 px ring, a 4 px white@0.75 highlight offset (-3,-3), `RV:175-186`); three 6 px dots `(0.95,0.35,0.35)`, `(0.98,0.82,0.35)`, `(0.45,0.88,0.5)` each with a `black@0.25` 0.5 px ring; spacer; period label uppercase, 10 pt bold monospaced, tracking 1.2, white@0.95, one line, min scale 0.7.

Screen (`RV:188-230`): padding 10, radius 6, `ink` fill with a faint radial glow (`lcd@0.07` to clear, center (0.15,0.1), radius 180; cosmetic, may be skipped), 2 px `black@0.35` border, 2 px horizontal margin. Stack spacing 7:
1. Total row (baseline aligned, spacing 5): `compact(total)` 24 pt bold mono LCD; unit `tokens` uppercased 7 pt medium mono, tracking 0.6, `lcd@0.5` (`recapTokensUnit`); spacer; delta chip.
2. Meters (below).
3. Readout grid, two columns spacing 10: left column [`Best day` : `compact(bestDay.tokens)` or `--`] and [`Best streak` : `recapDays(bestStreak)`]; right column [`Active days` : `a/b`] and [`Graduated` : `graduated.count`]. Each readout is label (7 pt medium mono uppercased `lcd@0.5`) + spacer + value (9.5 pt bold mono LCD), both one line min scale 0.7 (`RV:295-308`). `Graduated` counts **all** graduates of the period, the strip shows at most 4.

Delta chip (`RV:232-245`): `up = delta >= 0`; text `"▲"` or `"▼"` followed by `TokenFormatter.percent(abs(delta)*100)` (1 decimal, trailing `.0` dropped, `TF:49-52`, port `format.js:93` `percent`); 9 pt bold mono; color LCD if up else amber; padding 5/2; 1 px border in the same color at 0.6, radius 2. Delta 0 renders as "▲0%".

Meters (`RV:247-293`): bottom-aligned row of equal-width columns; column spacing: week 6, month 1.5, year 4. `peak = max(1, max(bucket.tokens))`. Each column: 8 blocks (`meterBlocks = 8`), block height 4, gap 2 (46 px tall), then a 3 px gap and a 10 px label row.
- `lit = tokens > 0 ? max(1, round(tokens / peak * 8)) : 0` (`RV:281-282`; rounding is half away from zero, equal to `Math.round` for positives). Lit blocks fill from the bottom: block index `i` (0 = top) is lit iff `i >= 8 - lit`.
- Lit color: LCD if `isCurrent` else LCD@0.55. Unlit color: LCD@0.09 if `hasData`, else LCD@0.035 (`RV:287-289`). A bucket with usage always lights at least one block ("quiet" vs "nothing" stay distinct).
- Labels (`RV:262-267`): font 8 pt for week, 7 pt otherwise; bold + LCD for the current bucket, regular + LCD@0.45 otherwise; the label is laid out in a zero-width frame so a wide label ("15") never widens its column.
- Tooltip (`.help`) and accessibility label per column: `"<name>, <grouped tokens | no data>"` (`RV:102-110,273`); `name` = `dayLabel` (template "EEEE d MMM") for week/month, `monthNameFormatter` ("MMMM y") for year; `hasData ? grouped(tokens) : recapNoData` (known zero prints `0`).
- Label text (`RV:74-95`): week = the weekday's short name, first **two** characters uppercased (`SU`, `MO`; two letters because one collides: Tuesday/Thursday); month = day number only for days 1, 8, 15, 22, 29, else empty; year = very-short month initial uppercased.
- Period label (`RV:54-69`): week = locale date interval for `dMMMy` from `period.start` to `period.end - 1 day`, e.g. "Oct 11 - 17, 2026" (screenshot shows it uppercased); month = "MMMM y"; year = the 4-digit year. Port: `Intl.DateTimeFormat(locale, {day:'numeric', month:'short', year:'numeric'}).formatRange(start, lastDay)`, `{month:'long', year:'numeric'}`, and the year number. **Flag:** `formatRange` availability in gnome-shell 51's SpiderMonkey is unverified; fallback `"<start> - <end>"` with two `format()` calls.

Graduate strip (`RV:310-346`, spacing 5, leading padding 2): an 18 px sprite of the companion **currently being raised** (not the pinned representative: `RV:26-30`; egg when none; shiny and form applied), a 1 x 14 white@0.35 divider, then either `recapNoGraduates` (8 pt mono white@0.8, up to 2 lines) or up to **4** chips (newest first). Chip: 18 px sprite (shiny sparkle "✨" 7 pt badge at top-trailing offset (3,-2) if shiny, `RV:329-333`), name 8 pt semibold mono white, one line min scale 0.7; chip padding 4/2, radius 3, `black@0.22` fill. Name rule (`RV:31-35`): the stored chain name for `finalID` in the app language, else `#<finalID>`; then the form rule `formDisplayName(name, speciesId, form, label: nil)`: **only Unown gets a suffix** `" [<LETTER>]"` (letter uppercased; `exclamation` becomes `!`, `question` becomes `?`), every other form shows the bare name (`URT:415-437`).

Caption lines under the card: `.font(.caption)` secondary, VStack spacing 2 (`RV:401-409`).

### 1.7 Strings (English msgid, Swift key, source)

Port rule: gettext msgid = the English string; other languages come from `LZ` (it has ko/en/ja/es/fr/pt/de/ru) if the port wants parity. Existing port msgids to reuse: "This week", "This month", "Back" if present.

| Swift key | English | Source |
|---|---|---|
| `recapOpen` | Usage recap | LZ:246 (Details button tooltip + a11y, segmented picker's hidden label) |
| `recapDetails` | Details | LZ:247 (button text) |
| `recapTokensUnit` | tokens | LZ:248 (shown uppercased) |
| `recapScopeName(.week/.month/.year)` | Week / Month / Year | LZ:249-255 |
| `recapCurrentPeriod(.week/.month/.year)` | This week / This month / This year | LZ:270-276 (`thisWeek`, `thisMonth`, "This year") |
| `recapPrevious` / `recapNext` | Previous / Next | LZ:277-278 |
| `recapBestDay` | Best day | LZ:279 |
| `recapActiveDays` | Active days | LZ:280 |
| `recapBestStreak` | Best streak | LZ:281 |
| `recapGraduates` | Graduated | LZ:299 |
| `recapNoData` | no data | LZ:300 |
| `recapDays(n)` | `%dd` (en "6d"; ko "6일", ja "6日", fr "6 j", de "6 T") | LZ:301-303 |
| `recapBestDayLine(day, tokens)` | Your best day was %s — %s tokens. | LZ:304-313 (`day` = "EEEE d MMM" long form; `tokens` = grouped) |
| `recapCompareSoFar(week)` | Compared with the same days last week | LZ:257-262 |
| `recapCompareSoFar(month)` | Compared with the same days last month | LZ:263-266 |
| `recapCompareSoFar(year)` | Compared with the same days last year | LZ:267-269 |
| `recapNoGraduates` | No graduation in this period. | LZ:314-318 |
| `back` | Back | LZ:243 |
| `streakDays(n)` | %d-day streak | LZ:284-290 |
| `streakDaysPending(n)` | %d-day streak — use it today to keep it going | LZ:291-298 |
| `activitySwitchHint` | Click to switch the view | LZ:92-97 |
| `recentHeatmap` | 16 weeks | LZ:86-91 |
| `peakDay` | Peak | LZ:82 (port already has "Peak") |

---

## 2. 16-week activity heatmap

### 2.1 Grid geometry

- `weeks = 16` (`AH:80`), 7 rows (weekdays), columns = weeks, **oldest on the left, today's week on the right**.
- Row 0 is the user's first weekday: `calendar.firstWeekday` (`AH:36-39`); the weekday labels column uses the same start (`AH:27-33`, `AH:256-258`). Port: `weekStart` (ISO 1-7) from the helper setting; with the section 4 payload the grid start already encodes it.
- Window (`recentGrid`, `AH:88-112`): `thisWeek = start of the week containing today`; `firstWeek = thisWeek - 15 weeks`; `windowStart = key(firstWeek)`; columns are generated from `windowStart` while `cursor <= today` (`AH:58-74`) giving exactly 16 columns (`thisWeek <= today`), each 7 days (`columns`, `AH:44-75`). The window **rolls** with today, it does not reset on the 1st (`AH:82-83`). The last column holds today and the days after it in that week.
- Each cell: `{date, tokens, isOutsideRange}`. `first = max(windowStart, recordedSince ?? today)` (`AH:100`). A cell is outside the range if `date < first || date > today` (`AH:67-68,107-110`). Outside cells hold their place and draw **nothing** ("not data, not zero", `AH:5-13`). So days before the ledger began (unknown) and the future part of the last week are transparent.
- `recordedSince = min(ledger.coveredSince, series.firstDate)` of the non-nil ones (`AH:127-129`); `series` is this month's live combined series (`store.monthDailyTotals`, passed as `heatmapSeries`, independent of the bar window selection, `AV:52-53`, `PV:236`; test `AVT:391-410`).
- Day tokens (`AH:117-123`): `days = ledger.tokensByDay`, then for each series day with tokens > 0, `days[date] = max(days[date], series.tokens)`. The ledger is only written once per refresh, so today's ledger value can trail the live series; the max keeps heatmap and bars agreeing (and a day never goes down, `AVT:295-303`).
- Cell metrics: row/cell height 10, gap 2 between cells and between rows (`AH:21-22`); the weekday-label column is separated from the grid by gap 4 (`spacing + 2`, `AH:254`). Cells take equal flexible width (`maxWidth: .infinity`, `AH:287`): at 332 px with a ~10 px label column, 16 columns and 15 gaps of 2, a cell is about 18 x 10 (matches the screenshot). Corner radius 2 (continuous). Weekday labels: 8 pt, tertiary, height 10 each, one narrow letter per row (`AH:27-33`, `AH:259-262`).
- The panel draws nothing at all unless `peak > 0` (`AH:201-203`), where `peak = max(tokens)` over **in-range** cells with tokens > 0. Caption layout has 3 pt spacing and the whole block has 4 pt top padding (`AH:204-209`).

### 2.2 Intensity buckets (exact)

`level(tokens, peak)` (`AH:135-142`):
```
tokens <= 0                -> 0     (unused day, drawn)
ratio = tokens / peak (floating-point division)
ratio <= 0.25 -> 1
ratio <= 0.50 -> 2
ratio <= 0.75 -> 3
else          -> 4
```
Boundaries are inclusive. Levels are quartiles of the **window peak** (not data quantiles). Outside-range cells use level -1 = clear (`AH:278-279`). There is no guard for `peak == 0` because the view only draws when `peak > 0` (`AH:131-134`); the port must keep the same gate or guard division.

### 2.3 Colors

GitHub contribution greens, separate scales for light and dark so level 1 never vanishes (`AH:149-165`); not the accent color. Index = level, `light / dark`:

| level | light | dark |
|---|---|---|
| 0 | `#EBEDF0` | `#161B22` |
| 1 | `#9BE9A8` | `#0E4429` |
| 2 | `#40C463` | `#006D32` |
| 3 | `#30A14E` | `#26A641` |
| 4 | `#216E39` | `#39D353` |

Port: define `-ptb-heat-0` to `-ptb-heat-4` on a `.ptb-heatmap` style class with the `.ptb-light` override, as `.ptb-chart` does (`stylesheet.css:385-403`), read with `lookup_color` as in `chart.js:25-28`. Level 0 must differ from the popover background (an unused day is drawn).

### 2.4 Today marker

Today's cell gets a 1 px inner stroke in `primary@0.55` (foreground color at alpha 0.55; white in dark, black in light), radius 2 (`AH:282-286`). Hovered cells get no highlight (readout only).

### 2.5 Hover and readout (there is no tooltip popup)

Cells are too small for a native tooltip, so the **caption readout follows the pointer and falls back to today** (`AH:192-194,223`). Hover is ignored on outside-range cells (`AH:290-293`); leaving a cell resets to today. Readout text (`AH:245-251`):
- If the day is in this month's live series: the bars' readout, `"<dayStamp> <compact tokens>"` plus `" <cost>"` when `showsCost` (`PV:1305-1311`; `dayStamp` template "MdE", e.g. "Thu, 11/5", `PV:1663-1669`, port `format.js:209` `dayStamp`).
- Else if the day is in the grid: `"<dayStamp> <compact tokens>"`, tokens only (the ledger keeps no cost). A zero day reads "... 0".
- Else empty.
- **Note the port's bar chart passes `showsCost=false` to its readout** (`usageSection.js:143`, "cost is on the big row"), unlike upstream where bars and heatmap both show cost (screenshot: "Thu, 11/5 1.1M $2.76"). The heatmap must follow whichever the bars do; with `monthDaily` in state it can show cost for current-month days if the caller decides to (open question Q4).

### 2.6 Caption row

`HStack(spacing 5)` (`AH:215-241`): title `recentHeatmap` ("16 weeks", `.caption`, tertiary, one line, gives way first); readout (`.caption` medium, secondary, monospaced digits, one line, never truncated); spacer; if `showsPeak` the label `Peak` (`.caption2` tertiary) and `compact(peak)` (`.caption2` semibold secondary mono); then the page dots. `showsPeak = usedCells.count > 1` (the peak is hidden for a single used day, as for the bars, `AH:205-206`).

### 2.7 Click-cycle, page dots, persistence

- Enum order is the cycle: `monthlyBars` (default, first) then `recentHeatmap`, wrapping (`AV:15-31`; tests `AVT:27-39`). `next` wraps modulo count so a future case joins automatically.
- The whole `ActivityPanel` (chart **and caption row and gaps**) is one click target: `.contentShape(Rectangle()).onTapGesture(onCycle)` with tooltip `activitySwitchHint` (`AV:67-72`). One view at a time; never both (`AV:41-46`).
- Page dots (`AV:95-108`): one 4 px circle per case, spacing 3, `secondary@0.75` for the current, `secondary@0.25` otherwise; the only hint that the panel cycles. They sit at the right end of the caption row of **both** views (`trailing:`).
- Persistence: UserDefaults key `activityVisualization`, raw values `monthlyBars` / `recentHeatmap`, written in `didSet` (`US:160-161`), read at launch with unknown values falling back to the default (`US:962-963`; `AVT:53-68`). Persists across popover reopen and app restarts. Port: GSettings key `activity-view` (`s`, choices `bars`, `heatmap`, default `bars`, UI-only), validated through a JS `normalizeActivityView` like `normalizeWindow` (`chartModel.js:23`).
- Interaction with the bar window picker: in Swift an inner `Button` wins over the parent tap gesture. In St, verify that a `Clutter.ClickAction` on the container does not swallow the clicks of the `St.Button` window picker (`usageSection.js:63-77`). **Uncertain**: Clutter ClickAction may act in the capture phase. Fallback: handle `button-release-event` on the container and ignore events whose source is inside the window button.

---

## 3. Streak badge

- Rule (already in Go, `ledger.go` `Streak`; Swift `UR:74-97`): the run of consecutive days with ledger tokens > 0. Start at today if today is active (`includesToday = true`), else at yesterday (`includesToday = false`: the run is intact but not yet extended today); count backwards while active; stepping by calendar day, reaching back across month starts through the ledger. "Active" = any tokens > 0, the same rule as the recap's best streak. A fully empty yesterday and today gives `{0, false}`.
- Display (`SB:9-35`): shown only when `days >= 2` (`SB:15-17`, a one-day run says "used today", which the totals already say, and would flicker). A flame icon (orange if `includesToday`, secondary gray otherwise) plus the number (caption medium, secondary, monospaced digits), gap 2. Tooltip and a11y label: `streakDays(n)`, or `streakDaysPending(n)` when today has no usage yet.
- Where upstream puts it: in the period row left of the Details button (`PV:214-232`), collapsing to a vertical layout when narrow. The port has it in the "Today's tokens" caption row (`usageSection.js:34-37`), keep it there; put the **Details** capsule next to it in that same caption row (the port's period table is its own two-row table that needs the full 332 px, `usageSection.js:32-33`).
- Port gap vs upstream: current badge uses an emoji and 110/255 opacity for "pending"; upstream uses an orange vs gray flame. Optional polish: `St.Icon` with `-st-icon-style: symbolic` colored by CSS (ui.md:585 already says "Easy"). Not required for M2 correctness. Plurals: `ngettext` allowed (`ui.md:624`).
- No new Go work for the streak.

---

## 4. Protocol additions

Goal: keep `state` small; put anything large behind a command.

### 4.1 `usage.combined.heatmap` (in `state`, heatmap data only)

```json
"heatmap": {"start": "2026-07-06", "first": "2026-08-01", "tokens": [0, 0, ... 112 ints]}
```
- `start`: day key of cell (column 0, row 0): the first day of the week (per the applied `weekStart`) that is 15 weeks before the week containing today. Always a `weekStart` weekday.
- `tokens`: exactly 112 ints (16 x 7), index `col*7 + row`, covering `start` through `start + 111`. Value = `max(ledger.tokens(day), combined monthDaily tokens(day))` (AH:117-123). Days after today are `0`.
- `first`: first day the grid can vouch for = `max(start, recordedSince ?? today)` (AH:100, AH:127-129), where `recordedSince = min(non-empty of ledger.CoveredSince, combined.monthDaily.start)`. Cells with `date < first` or `date > usage.calendar.today` are outside-range.
- Only totals (no provider split, no cost): the ledger has none (`AH:174-177`).
- Size: 112 ints, worst case about 9 digits plus comma = about 1.1 KB, typical about 0.8 KB. The documented state budget is 8 KB compact with the `state-active.json` fixture at 6.3 KB (`protocol.md:215-218`); this brings it to about 7.4 KB. Raise the stated budget to 9 KB and update the fixture. If that is unacceptable, send it only when `peak > 0` (it is `null` otherwise) or move it to a `getHeatmap` command refetched when the state's `generatedAt` day changes; I recommend state because it changes with every refresh and needs no refetch logic.
  **Resolved (2026-11-19):** the budget is 9 KB and `heatmap` stays in `state`, always present (never `null`, no `getHeatmap`).
- Putting the window into the helper means the extension needs neither `weekStart` nor week math for the heatmap, and the DST-sensitive part stays in tested Go.
- The cached initial state (protocol §2.3) must recompute `heatmap` from the loaded ledger, same as `streak`.
- Weekday row labels: derived in JS from `start`'s weekday with `Intl.DateTimeFormat(locale, {weekday: 'narrow'})`.

### 4.2 `getRecap` command

Args: `{"scope": "week"|"month"|"year", "offset": int}`. `offset` in `[-1000, 0]`, otherwise `badArgs` (same style as `protocol.md:826-831`). Served immediately from local data, ext. timeout 10 s, concurrent like `getDexPage`.

Reply `data`:
```json
{
  "scope": "week", "start": "2026-10-11", "end": "2026-10-17",
  "total": 3500000, "isInProgress": true,
  "previousTotal": 8960000,
  "bestDay": {"key": "2026-10-11", "tokens": 910000},
  "activeDays": 6, "countedDays": 6, "bestStreak": 6, "canGoBack": true,
  "buckets": [{"key":"2026-10-11","tokens":910000,"isCurrent":false,"hasData":true}],
  "graduated": 3,
  "graduates": [{"entryId":"...","speciesId":479,"form":null,"isShiny":false,"name":"Rotom"}]
}
```
- `start`/`end` are inclusive day keys (the extension builds the period label from them).
- `previousTotal` and `bestDay` are `null` when absent. `delta` is not sent: the extension computes `previousTotal > 0 ? (total-previousTotal)/previousTotal : null` so it cannot disagree.
- `buckets`: 7, 28-31 or 12 entries; the year bucket key is the first of the month.
- `graduated` = total count; `graduates` = at most 4, newest first, name already localized with the Unown rule. `entryId` matches the catch-log id so the extension can fetch sprites via the existing `getSprite`.
- Companion strip sprite: no new data. The extension uses `state.companion.active` (`speciesId`, `form`, `isShiny`), or the egg when `phase == egg` (protocol §5.4).
- Size is irrelevant (1 MiB line limit, `protocol.md:21`); a year reply is about 1.5 KB.
- Refresh: the extension re-sends `getRecap` when the recap is visible and a new `state` arrives with a changed `usage.combined.today.tokens` or day (cheap; do not poll).
- Alternative (not recommended): `getLedger` returning up to about 730 days (about 7 KB) and doing the recap in JS. It splits authority for the DST-safe date math and puts graduates in JS. Chosen design: Go computes, JS renders.

### 4.3 GSettings

Add `activity-view` (`s`, `bars`|`heatmap`, default `bars`, UI-only) to `protocol.md` §9.1 and `extension/schemas/*.xml`. Recap scope/offset are not persisted (matches upstream).

---

## 5. Work lists and tests

### 5.1 Go work list (`helper/`, about 430 LOC of code plus about 450 LOC of tests)

1. `internal/usage/recap.go` (about 220 LOC): `RecapScope`, `Period{Scope, Start, End time.Time}` with `NewPeriod(scope, containing, weekStart, loc)` and `Shifted(n, ...)` using `StartOfWeek/StartOfMonth/midnight`; `Recap` struct; `MakeRecap(ledger, graduates, period, now, weekStart)`. Compute in civil-date space: convert `now` to `{y,m,d}`, iterate with a local `addDays` on a UTC-normalized date (the `midnight` trick, `calendar.go:20-28`), format keys with `DayKey`. Only the graduates filter needs instants (`caughtAt` vs `period.Start/End`, local midnights). Year period: `midnight(y,1,1)`; month shift via `midnight(y, m+n, 1)`. `Ledger.Covers(day)` is not exported yet: add it (`coveredSince != "" && day >= coveredSince`). Return `Bucket` slices and counts exactly per section 1.3.
2. `internal/usage/heatmap.go` (about 60 LOC): `Heatmap(ledger, monthDaily, now, weekStart) HeatmapView{Start, First string; Tokens [112]int64}`; includes `recordedSince` and the max-overlay.
3. `internal/companion`: exported `(*Store).Graduates(from, to time.Time, limit int) (count int, items []Graduate)`; filter `ReleasedAt == nil && CaughtAt in [from,to)`, newest first; name via `entryName` + `formDisplayName(name, finalID, form, "")` (Unown suffix only). About 40 LOC.
4. `internal/daemon/wire.go`: `heatmapView` in `aggregateView` (`wire.go:112`), `recapView`; `build.go:100` fills `Heatmap`. `commands.go:25-65`: add `case "getRecap"` plus arg parsing in `args.go` (about 90 LOC combined).
5. Contract/fixtures: extend `contract_test.go` and the state fixtures; document the new fields in `protocol.md` and bump the state-size note.
6. Tests: below.

### 5.2 JS work list (about 1000 LOC of code plus about 300 LOC of tests)

1. `lib/heatmapModel.js` (pure, about 120): `buildGrid(heatmap, today)` giving `columns[16][7] = {date, tokens, outside}` (from `start`, `addDays`), `level(tokens, peak)`, `peakOf`, `showsPeak`, `cellAt(x, y, width)`, `weekdayLabels(start, locale)`, `readout(grid, day, ...)`, `LEVEL_COUNT`.
2. `lib/heatmap.js` (about 200): `St.DrawingArea` + Cairo like `chart.js` (colors from CSS props `-ptb-heat-0..4`, today stroke, rounded cells r=2), `MotionController` hover returning the hovered date to the caption readout; no popup tooltip.
3. `lib/popover/activityPanel.js` (about 120): owns caption row + current view (bars or heatmap), page dots (two 4 px `St.Widget` circles, alpha 0.75/0.25), click-cycle writing `activity-view`, tooltip hint on hover, empty-heatmap behavior (Q1). Refactor `usageSection.js` (bar caption + `DailyChart`) into this panel; the bar caption gets the dots too.
4. `lib/recapModel.js` (pure, about 150): `meterLit(tokens, peak)`, `bucketLabels(buckets, scope, locale)`, `periodLabel`, `bucketDescription`, `delta(recap)`, `chipText`, scope list, `nextPeriod/prev` clamps.
5. `lib/popover/recapView.js` (about 330): top row (Back + 3-button segmented control as `St.Button`s with `checked` pseudo class, as in `popover.js:42-52`), time row, card (`St.BoxLayout` with a gradient background via `background-gradient-*`, header, screen, graduate strip), meters in one `St.DrawingArea` with per-column hover tooltip through the shared `tooltip.showAt`, delta chip, caption lines, sprites via the existing sprite pipeline. Request `getRecap` on scope/offset change and on relevant state changes; keep the previous reply visible while loading; ignore stale replies by request counter.
6. `lib/popover/popover.js` (+40): a recap screen state that hides tabs/scroll/footer (like Settings upstream), `onOpen()` resets it (`popover.js:106-124`), Details button wiring.
7. `lib/popover/usageSection.js`: the Details capsule in the caption row beside the streak.
8. `extension.js`/`prefs.js`/schema/`stylesheet.css` (about 100 combined): `activity-view` key (`extension.js:125,162` pattern), recap card styles, heatmap colors.
9. `format.js`: reuse `compact`, `grouped`, `percent`, `dayStamp`; add an Intl-based `dayLabel` ("EEEE d MMM") if missing.
10. Tests: `test/heatmapModel.test.js`, `test/recapModel.test.js`, plus the stub-helper (`test/stub-helper.py`) answering `getRecap` for sandbox screenshots.

### 5.3 Go tests to port (oracle: Swift assertions)

Common fixtures (`URT:7-48`): zone UTC, week starts Monday, `now = 2026-09-18 12:00 UTC` (Friday), `ledger(since, days)` = a ledger after `Merge([day(since,0)] + days)`; `key(y,m,d)` = `yyyy-MM-dd`.

**Already ported, do not redo:** `URT:55,65,80,88,100,150,174`, `UsageStreakTests.swift:45-88`.

**Periods** (`URT:200-224`):
1. Monday-first: period(week, now) start = 2026-09-14 00:00, end = 2026-09-21 00:00. Sunday-first: start = 2026-09-13 00:00.
2. `shifted(-1)`: month containing 2026-03-31 gives start 2026-02-01 00:00, end 2026-03-01 00:00. Year containing now shifted -1: start 2025-01-01, end 2026-01-01.

**Recap** (`URT:227-330`), each with the exact inputs:
3. `testEachScopeDrawsItsOwnMeters`: ledger since 2026-01-01 with 09-02:10, 09-17:20, 08-30:5. Week: first bucket key 2026-09-14, 7 buckets, `isCurrent` keys == [2026-09-18]. Month: 30 buckets; month at offset -7: 28 buckets. Year: 12 buckets; `buckets[8].tokens == 30`; `buckets[7].tokens == 5`; `buckets[8].isCurrent`; `year.total == 35`.
4. `testFutureAndUnrecordedDaysAreNotZeros`: ledger since 09-16 with 09-17:50, week: `hasData == [F,F,T,T,T,F,F]`; `activeDays == 1`; `countedDays == 3`. Year with ledger since 09-01 and no days: `hasData` = 8 x false, then true (September), then 3 x false.
5. `testARunningPeriodIsComparedOverTheSameDays`: ledger since 2026-08-01 with 09-10:100, 08-18:40, 08-19:1000; month: `isInProgress`, `previousTotal == 40`, `delta == 1.5` (+-0.0001). (total 100, derived)
6. `testAFinishedPeriodIsComparedWhole`: ledger since 07-01 with 08-05:300, 07-31:100; month offset -1: not in progress, `previousTotal == 100`, `delta == 2.0`.
7. `testAHalfCoveredPreviousPeriodIsNotAComparison`: ledger since 09-09 with 09-15:150, 09-10:50; week: `previousTotal == nil`, `delta == nil`.
8. `testTheBestStreakStaysInsideThePeriod`: ledger since 09-01 with 5 tokens on 09-11,12,13,14,15,16,18; week `bestStreak == 3`; month `bestStreak == 6`.
9. `testGraduationsInThePeriodOnly`: dex entries (caughtAt): `this-week` 09-17 12:00; `monday` 09-14 00:00 (shiny); `let-go` 09-16 12:00 (released); `last-week` 09-13 23:00. Ledger since 09-01, no days. Week graduated ids == `["this-week","monday"]`; month graduated count == 3.
10. `testGoingBackStopsWhereTheLedgerStarts`: ledger since 09-01 with 09-02:10; month `canGoBack == false`; week offset -1 true; week offset -2 false; year false.
11. `testAnEmptyLedgerStillDrawsThePeriod`: empty ledger, week: 7 buckets, none `hasData`, total 0, `bestDay == nil`, `previousTotal == nil`, `canGoBack == false`.
12. (Rendering analog, `URT:343-367`, `testBucketDescriptions...` logic goes to JS) Graduate names (`URT:415-437`): Unown (species 201, form `q`) named "Unown" gives "Unown [Q]"; Wormadam sandy (final 413) named "Wormadam" gives "Wormadam" (form ignored).
13. Go-only additions: DST (America/Los_Angeles): week/month period starts across 2026-03-08 and 2026-11-01 are local midnights; a month recap has the right bucket count (31 for March, 30 for November, 28 for February at offset -7 from now).

**Heatmap window** (`AVT:190-310`). In Swift the tests drive `recentGrid`; the Go equivalent is `Heatmap(...)`. Fixtures (`AVT:220-254`): zone America/Los_Angeles; `series(through: "YYYY-MM-DD")` = days 1..DD of that month at 1000 tokens; `ledger(months:)` merges full months (28 for Feb, 30/31 otherwise) at 1000/day; `today` strings as below.
14. `testGridIsAlwaysSixteenWeeksEndingWithTodaysWeek`: ledger months 2026-06..2026-10, series `[]`; for (today, firstWeekday) in [(2026-10-01, Mon), (2026-10-19, Mon), (2026-11-03, Sun), (2026-10-31, Sun)]: `tokens` has 112 entries; today's day lies in the last 7 entries; the last in-range day (`first <= d <= today`) is today; 112 unique dates; `start` is a `weekStart` weekday.
15. `testTheFirstOfTheMonthKeepsLastMonthsDays`: ledger months 2026-08, 2026-09; series `[2026-10-01:1000]`; today 2026-10-01 (Mon-first). Used (in-range, tokens > 0) includes 2026-09-30 and 2026-10-01; count > 56 (derived exactly 62: Aug 1 to Sep 30 plus Oct 1; window starts 2026-06-15, `first` = 2026-08-01).
16. `testDaysBeforeRecordingBeganAreOutsideTheRange`: empty ledger, series Oct 1..19; today 2026-10-19: 16 columns; first in-range day == 2026-10-01; 2026-09-30 is outside.
17. `testLiveSeriesWinsWhenTheLedgerTrails`: ledger `{2026-10-19:500}`; series 2000 gives 2000; series 100 gives 500.
18. DST contiguity (`AVT:115-137`): LA zone, both `weekStart` Sunday and Monday, with today = 2026-03-31 and 2026-11-30: dates unique, each date equals the previous plus one calendar day, 112 entries (the Swift test used `columns(first,last)` over the month; the port invariant is the same). Add the Swift fall-back trap: Sunday-first, today in the week of 2026-11-01.
19. Heatmap sorts keys with `max` overlay: `tokens` for a ledger day with larger value than the series keeps the ledger value (covered by 17).

### 5.4 JS tests to port

**heatmapModel** (`AVT:41-68,170-310`):
- `level(tokens, 100_000_000)`: 0 gives 0; 1 gives 1; 25_000_000 gives 1; 25_000_001 gives 2; 50_000_000 gives 2; 75_000_000 gives 3; 100_000_000 gives 4. (`AVT:194-206`; add 50_000_001 gives 3 and 75_000_001 gives 4, derived.)
- Palette: 5 distinct colors per scheme; level -1 is clear (`AVT:208-218`).
- `normalizeActivityView`: default `bars`; unknown values give `bars`; cycle `bars` to `heatmap` to `bars` after one lap (`AVT:19-68`; a "title non-empty in every language" test has no port analog).
- Weekday labels (`AVT:180-188`): English, Monday-first start gives `["M","T","W","T","F","S","S"]`; Korean, Sunday-first gives first `일`, last `토`. (JS derives from `start`: use `start = 2026-09-14` (Mon) and `2026-09-13` (Sun).)
- Grid build: 16 columns x 7; outside cells flagged for `date < first` and `date > today`; a bad `start` gives `[]` (`AVT:170-178`).
- Peak/gate: peak over in-range used cells only; `showsPeak` only when more than one used cell.
- `recentHeatmap` msgid contains "16" (`AVT:305-310`).
- Readout rules: day in `monthDaily` uses the bar readout, else `"<dayStamp> <compact>"`, fallback to today when not hovering.

**recapModel** (`URT:332-437`, English only):
- `recapCurrentPeriod`: week gives "This week", month "This month", year "This year" (`URT:369-382`).
- `bucketDescription` with fixture now = 2026-09-18, scopes week/month/year, a ledger since 2026-08-01 with 12_345 on 09-17 (`URT:384-413`): a used bucket ends `", 12,345"` (`grouped`); the first bucket with `hasData && tokens == 0` ends `", 0"`; the last bucket with `!hasData` ends `", no data"`; week/month prefix = the localized day label; year prefix "September 2026, " and the year's first bucket (January) ends ", no data".
- `bucketLabels.length == buckets.length` for every scope; week labels are 2 letters uppercase; month labels non-empty only for days 1, 8, 15, 22, 29; year labels one letter (`URT:343-367`).
- `meterLit`: tokens 0 gives 0; tokens > 0 never 0; `peak` floor of 1; rounding `round(t/peak*8)` with minimum 1 (`RV:281-282`); e.g. (100, 100) gives 8, (1, 100) gives 1, (50, 100) gives 4, (6, 100) gives 1 (0.48 rounds to 0 so min 1), (7, 100) gives 1 (0.56 rounds to 1) (derived).
- Delta chip text: 1.5 gives "▲150%", -0.609 gives "▼60.9%", 0 gives "▲0%", `previousTotal == 0` or null gives no chip (`RV:232-245`).
- Navigation clamp: Previous disabled iff `!canGoBack`; Next disabled iff `offset >= 0`; scope change resets offset to 0 (`RV:377,384,396`).

Swift rendering tests (`ImageRenderer`, `AVT:312-413`, `URT:343-367`) are not portable: pixel-equality of wrappers has no GJS analogue. Their intent (the panel passes the ledger, the whole month series and the provider stack to its children; the empty bar window keeps picker and dots) must be covered by Go test 15/16 (the helper builds the heatmap from the full month series regardless of the bar window) and a sandbox screenshot of an empty window.

---

## 6. Open questions and upstream issues

**Q1 / upstream bug: heatmap with `peak == 0` strands the user.** `ActivityHeatmap.body` renders nothing when `peak == 0` (`AH:201-203`), including the page dots and the click target (`AV:68-70`: `contentShape` on an empty view). If `activityVisualization == .recentHeatmap` is persisted and no usage exists in 16 weeks (fresh install after choosing the heatmap, or a long idle), the panel is gone and the user cannot cycle back. The bars handle the equivalent case by keeping the caption (`AVT:378-389`, `testPanelKeepsThePickerForAnEmptySelectedWindow`). Proposed port behavior: keep the caption row (title + dots) and click target, draw no grid. Needs a decision.

**Resolved (2026-11-19):** the caption row (title and page dots) and the click target stay; only the grid is not drawn when `peak == 0`. The user can always cycle back.

**Q2: heatmap and recap disagree on the data source.** Heatmap overlays the live series on the ledger (`AH:117-123`), the recap uses the ledger only (`RV:24`). Equal after every refresh because the ledger is recorded after phase 2, but a recap opened right after a midnight tick can differ from the heatmap for "today" until the next refresh. Accept, or add the same max-overlay in `MakeRecap` (small, makes them agree). Recommend adding the overlay.

**Resolved (2026-11-19):** the recap overlays the live series: per day, `tokens = max(ledger, combined monthDaily)`, and a day counts as covered (`known`) from `recordedSince` (§4.1) on. `MakeRecap` and the heatmap therefore agree.

**Q3: `bestDay` tie-break** is the earliest day (Swift `max(by:)` keeps the first maximum); not covered by a Swift test. Confirm or choose latest.

**Resolved (2026-11-19):** ties go to the earliest day.

**Q4: cost in readouts.** Upstream shows cost in the bar and heatmap readouts for current-month days (`PV:1305-1311`); the port's bar readout passes `showsCost=false` on purpose (`usageSection.js:143`). Decide once for both.

**Resolved (2026-11-19):** no cost in any readout, bars or heatmap (`showsCost=false`).

**Q5: week-start in GJS.** `protocol.md:795-801` already flags `Intl.Locale.getWeekInfo` as unverified in gnome-shell 51. Heatmap row 0 and the recap week depend on it (fallback Monday). `Intl.DateTimeFormat.formatRange` for the week title is unverified too.

**Resolved (2026-11-19):** for `formatRange`: use it only if GJS supports it, else build the period label from two formatted dates. The `getWeekInfo` half stays open (protocol §9.2 fallback to 1, Phase 7 check).

**Q6: clicks.** Whether a `Clutter.ClickAction` on the panel steals clicks from the bar window button (section 2.7). Verify in the sandbox, not by launching gnome-shell here.

**Q7: egg or missing companion in the recap strip.** Upstream draws `SpriteView(speciesID: nil)` (an egg). Confirm the port's sprite widget can show an egg without a species id.

**Q8: Details button placement and the recap's Esc.** Placement beside the streak (caption row) is my proposal; the period row it occupies upstream does not exist in the port's table layout. Esc is not rebound (GNOME closes the menu).

**Q9: localization scope.** Upstream has 8 languages (`LZ`); the port's i18n is gettext with English msgids. Strings in 1.7 give the English text and the source lines; importing the other seven languages into `.po` is optional.

**Upstream quirks noted (no fix needed for parity):**
- `UsageLedger.load` accepts only `[String: Int]` values (`UR:49`); a hand-edited value outside Int range drops the whole dictionary. The Go `Sanitize` is per-entry, which is more tolerant.
- `ActivityCell.tokens` for outside-range cells is retained but never used (`AH:107-110`); do not compute a peak over them (`AH:201`).
- The year comparison aligns by position in the year, not by calendar date, so Feb 29 shifts the same-days window by one day in leap years (`UR:245`).
- Comments in `testGoingBackStopsWhereTheLedgerStarts` (`URT:311`) attribute the true result to "the week of 31 August" while the asserted period is the week of 7 September (offset -1) whose previous week (31 Aug - 6 Sep) holds the covered 1 September; values are right, the comment is loose.
