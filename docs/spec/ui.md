# PokeTokenBar UI inventory for a GNOME Shell 51 port (Milestone 1)

> **Architecture override (2026-11-18): read this before §7.** This inventory was written before
> the architecture was fixed. A separate **Go helper process** does all scanning, HTTP, PokéAPI access,
> GIF decoding and game state. The extension only renders state it receives over stdio JSON lines
> (see `docs/protocol.md`). The following rows in §7 are therefore **superseded**:
> - "Data layer" (JSONL parsing / `Soup` in the shell): **the helper does it.** The extension never
>   reads `~/.claude` and never makes HTTP calls.
> - "Animated sprite" via in-shell `GdkPixbuf.PixbufAnimation`: **the helper decodes GIFs into
>   composited PNG frames plus a delay manifest.** The extension only cycles preloaded frames.
> - "Prefs window → representative-Pokémon chooser": **the representative is set from the Pokédex
>   species detail in the popover.** `prefs.js` never needs dex data.
> - "Network fetch with `Soup`" (sprites): **the helper downloads and caches the sprites.**
>
> Shell 51 note: `resource:///org/gnome/shell/ui/animation.js` exports only `Spinner`; the old
> sliced-image `Animation`/`AnimatedIcon` classes are gone.

All paths are under `/home/user/Documents/01_Projects/PokeTokenBar/Sources/PokeTokenBar/` unless noted. I read all the files you listed and the 8 screenshots. I also read `Core/UsageStore.swift` (menu and notification parts), `Core/CompanionStore.swift` (notification and dex parts), `Core/PaceTier.swift`, `Core/TokenFormatter.swift` and `Core/OAuthLimitsProvider.swift`. Nothing was modified.

Conventions: `PokeTokenBarApp.swift` is abbreviated `App.swift`, `PopoverView.swift` is `Popover`, `CompanionView.swift` is `CV`, `SettingsView.swift` is `SV`.

## 0. Architecture and data sources

- **Shell.** There is no SwiftUI scene. `AppDelegate` owns an `NSStatusItem` and an `NSPopover` (`App.swift:5-27`).
- **Popover behaviour.** The popover is `.transient`. `OutsideClickMonitor` is a token holder that guarantees exactly one global mouse-down monitor, installed on `popoverDidShow` and removed on `popoverDidClose` (`OutsideClickMonitor.swift`, `App.swift:~590`).
  - In GNOME, `PopupMenu` / `PopupMenuManager` already handles outside-click dismissal, so this file can be dropped.
- **Reset on reopen.** The popover always reopens on the Home tab. `PopoverNavigation.reset()` (`Popover:56`) clears settings, recap and tab.
  - The `providerID` and `claudeAccountID` selections are kept across openings.
- **Hierarchy.** `PopoverView.body` (`Popover:96-120`) shows one of three screens: `SettingsView`, `RecapScreen`, or `mainContent`. Settings replaces the popover content, which avoids orphaned sheets.
- **Where the data comes from.**
  - Token usage: local scan of Claude Code JSONL (`Core/LocalUsageReader.swift`).
  - Official limits: `GET https://api.anthropic.com/api/oauth/usage` (`Core/OAuthLimitsProvider.swift:38`).
    - The OAuth token comes from the macOS Keychain, or from `~/.claude/.credentials.json` (`OAuthLimitsProvider.swift:208-212`).
    - On Linux only the credentials file path applies. The Keychain rows and prompts are macOS-only.
    - Response fields: `five_hour`, `seven_day`, `seven_day_opus`, `seven_day_sonnet`, and a `limits[]` array. Each has `utilization` and `resets_at` (`Core/Models.swift:246-270`).
  - Sprites: `https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon` (`UI/SpriteLoader.swift:6`), including animated Gen-V GIFs.
  - Species and move data and names: `https://pokeapi.co/api/v2` and `https://graphql.pokeapi.co/v1beta2` (`Core/PokeAPIClient.swift`).
- **Refresh.** Default 120 s. Presets are Manual, 1, 2, 5 and 15 minutes (`UsageStore.swift:221-223`).

---

## 1. Top-bar item

### 1.1 Content: sprite plus text lines

An `NSStatusItem` button with `imagePosition = .imageLeading` and a 13 pt monospaced-digit font (`App.swift:~96-103`). It shows **a sprite on the left and 0, 1 or 2 text lines on the right**.

Line assembly is `UsageStore.menuLines(growthText:)` (`UsageStore.swift:354-376`):
- Before the first usage refresh finishes (`lastUpdated == nil`), the result is `[growthText]` if growth is on, else `["—"]`.
- Otherwise `usage[]` is built in this order:
  1. Today's tokens (compact), if `showTokensInMenu` (default **true**).
  2. Today's cost (compact), if `showCostInMenu` (default false) and the provider reports cost.
  3. Growth text, if `showGrowthInMenu` (default false).
- The limit line is `menuLimitLine`, shown when `showLimitInMenu` is on (default false). Format: `"Claude 62%"`, with providers joined by ` · `.
  - Claude is shown only if Claude Code was used today and the 5h window exists, else the weekly window (`headlineWindow`).
  - The percentage follows `limitDisplayMode` (used or remaining), with no suffix.

Layout rules:

| Items | Lines |
|---|---|
| usage ≥ 2 and a limit line | line 1 = usage joined with ` · `, line 2 = limit |
| usage > 2 and no limit | line 1 = first n−1 joined, line 2 = last |
| otherwise | one line per item |
| all off | sprite only (the hint `allOffHint` is shown in settings) |

Examples:
- `1.2M` over `Growth 42%` (`assets/screenshot-menu-growth.png`).
- `19.4M · $5.09` over `Claude 62%`.

**Text formats** (`Core/TokenFormatter.swift`):
- `compact`: `987`, `12.3K`, `190.6M`, `1.24B`. K and M use 1 decimal and B uses 2. Trailing zeros are trimmed, and a value that rounds to `1000.0` is promoted to the next unit.
- Cost, compact: `$9.5` (under $100, one decimal), `$311` (under $10,000, whole), `$1.2K`.
- Cost in the popover and right-click menu: `$%.2f`. Unknown cost is `$—` compact, or the "Unavailable" string.
- Percent: `80%`, `88.3%` (the `.0` is dropped).
- Growth text: `l.menuGrowthProgress(percent, isEgg)` (`Localization.swift:348`).
  - Egg: `"Hatch 42%"`. Hatched: `"Growth 42%"`.
  - The percentage is `used*100/total` (integer floor). It tracks the companion being raised, not the pinned representative.

### 1.2 Limit coloring (`MenuLimitColoring.swift`, `UsageStore.swift:~425-460`)

- Each `"Label NN%"` item is colored in its pace-tier color. Separators and the token and cost line keep the system text color.
- The setting `menuLimitColorMode` (visible only when "Limit %" is on):
  - `gauge`: all items, six tiers (**default**).
  - `attention`: only items running faster than pace (tier above `onPace`).
  - `off`: none.
- The tier comes from `PaceTier.gauge(...)`, shared with the popover bars (section 6).

### 1.3 Sprite

- **Which sprite.** The representative Pokémon (`companion.representativeSubject`). That is the pinned Pokédex entry, or the one currently being raised. If it is an egg, an 🥚 emoji image is used.
  - It carries species ID, shiny flag, form and a "faces back" flag (`alwaysFacesBack`, shown in the right-click menu).
- **Size.** Canvas is 22 px high. Sprite width is capped at 36 px with `h-2` high, preserving aspect ratio, and pixel-art nearest-neighbour scaling (`App.swift:menuBarLayout`, `menuBarSpriteMaxWidth = 36`).
- **Loading order.**
  1. Cached static PNG shown immediately as a 2-frame "bob" (0.5 s per frame, 1 px vertical offset).
  2. Animated GIF fetched asynchronously and swapped in.
  3. If the GIF is unavailable, the PNG bob stays. If there is no cached PNG either, the egg bob is shown.
- **Animation.** A single one-shot timer per frame, with `tolerance = delay*0.1`.
  - Frames are decimated, not stretched, to a minimum frame duration chosen by the **Animation** setting (`GIFDecoder.capFrameRate`, `UI/SpriteAnimation.swift`). Loop length is preserved:

    | Setting | Frame floor | Approx. fps |
    |---|---|---|
    | `powerSaver` (default) | 0.4 s | 2.5 |
    | `balanced` | 0.2 s | 5 |
    | `smooth` | 0.1 s (inferred from the "smooth" label; the line was truncated in my read — verify at `UsageStore.swift:~141`) | 10 |

  - Low Power Mode forces the powerSaver floor.
  - Animation **stops** when any of these hold: the display sleeps, the popover is open (the popover sprite already animates), or the bar is occluded. It resumes on the reverse events (`menuShouldAnimate`, `App.swift:~478`).
- **Redraw cost.** Frame swaps replace a `CALayer.contents` (`spriteLayer`) rather than `button.image`, to avoid redrawing the text (`App.swift:30-43`). Measured at 0.27 ms/frame versus 2.00 ms/frame.
  - GNOME equivalent: swap `St.Icon` / `Clutter.Image` content or a `GdkPixbuf` texture only, and never relayout the label.

### 1.4 Click handling

- Left click toggles the popover.
- Right click, or Ctrl+left click, opens the secondary menu (`StatusItemMenu.opensMenu`, `StatusItemMenu.swift:104`).
- Clicking the sprite or tray never activates a window, but `NSApp.activate` is called on popover show.
- Tooltip (`App.swift:applyState`, `UsageStore.menuToolTip`): shown only when more than one Claude account exists. It names the account that the menu percentage currently follows.
- The stale state **does not dim** the bar. This was removed on purpose because users read dimming as "broken" (`App.swift:applyState` comment).

### 1.5 Right-click menu (`StatusItemMenu.swift:79-93`)

| # | Item | Shortcut | Action |
|---|---|---|---|
| 1 | Disabled info row: `"Today's tokens 19.4M · $5.09"` (cost omitted if no provider reports one) | none | none |
| 2 | separator | | |
| 3 | `refreshNow` | r | `store.refresh()` |
| 4 | `dexTitle` (Pokédex) | d | open the popover on Collection → Pokédex (`openRepresentativeDex`) |
| 5 | `settings…` | , | open the popover on Settings |
| 6 | `floatingPetHideLabel` or `floatingPetEnableLabel` | none | toggle the floating pet (**LATER**) |
| 7 | `backViewLabel` (checkmark when on) | none | toggle `alwaysFacesBack` |
| 8 | separator | | |
| 9 | `quit` | q | terminate (**drop**; a GNOME extension cannot quit itself) |

M1: items 1, 3, 4, 5, 7. Item 6 is LATER. Item 9 is dropped.

---

## 2. Popover (360 pt wide)

**Metrics** (`Popover:8-21`):
- Width 360, padding 14, content width 332, scroller lane 12 (right-hand gutter so the overlay scrollbar never covers right-aligned numbers). `scrollContentWidth` = 320.
- The Home scroll area has a fixed height of 520. Collection also uses 520. Settings uses 460.
- The popover grows no further, so it never leaves the screen.

### 2.0 Frame (`mainContent`, `Popover:~190`)

Top to bottom:
1. **Update banner**, only if an update is available and update notifications are enabled. A blue 12% tint, with text, "Skip this version" and "Update". **Probably DROP**: a GNOME extension updates through extensions.gnome.org.
2. **Segmented tab picker** (Home, Shop, Bag, Collection). M1 needs Home and Collection. Shop and Bag are LATER, so build the tab bar so tabs can be hidden.
3. **Content** by tab. Home is `ScrollView{...}` with a fixed height of 520.
4. **Footer** (always visible).

### 2.1 Home content (`Popover:150-176`), top to bottom, with `Divider`s as noted

**A. Companion header** (`CompanionHeader`, `CV:664-930`). A row with a 76×76 sprite tile on the left, which is `secondary.opacity(0.06)` with a 12 pt radius. On the right, a column:
- Display name (callout, semibold), plus ✨ if shiny, plus a **rarity badge** (uppercase, solid color capsule, 8 pt bold white text).
- Hatched state:
  - Type badges (18 type colors, see section 6). They show only when PokéAPI details are loaded. A "default form data note" appears for non-default forms.
  - `stageText` plus nature (`"Stage 2/3 · Adamant"`, or "Final form"). An orange "growth boost" badge appears when `growthMultiplier` is non-nil.
  - **LabeledProgressBar** (see below), labelled `"used / total · NN%"` as `"1.2M / 3M · 40%"`.
  - `toNextEvolution(amount)` or `toGraduation(amount)` in tertiary text.
- Egg state:
  - `"Incubating"` (secondary) switches to `"About to hatch!"` (orange) when progress ≥ 0.9, and the tile wobbles ±5° forever (easeInOut 0.35 s, auto-reversing).
  - A rarity-guarantee badge, if the egg has a guaranteed rarity.
  - Progress bar, `eggToHatch(amount)`, a delayed-hatch warning (orange), and a first-run hint if nothing has accrued yet.
- A **status line** (`CV:statusLine`) from `displayState`: egg, idle, working, focus, tired, sleep, sulk, levelUp (shows `statusEvolved(name)` or `statusGrew`).
- Below, if hatched and the evolution line has nodes: **EvoLineView**, a horizontal row of 40 px thumbnails with arrows between them. Unknown next stages show as a "?" node.
  - It scrolls horizontally with edge fades when wider than 320 px.
  - It carries sprites that link to the Pokédex entry (see click behaviour below).
- `justGraduated` produces an orange one-line message.

**LabeledProgressBar** (`CV:556`): an orange fill (`Color.orange`) on a `secondary.opacity(0.2)` track. It is 6 pt high collapsed and expands to 12 pt after a 300 ms hover, springing the label in over the fill. This is a **hover-reveal label that requires animated height** (medium).

Celebration effects, triggered by a `celebrationSeq` bump (`playCelebrationIfNeeded`, `CV:~870`):
- White flash at 0.85 opacity fading over 0.8 s.
- Scale from 0.6 to 1 with a spring.
- ✨ burst for a shiny hatch, a 🎭 burst for a Ditto reveal.
- "+XP" capsule for candy feedback (**LATER**, tied to the bag).
- Mint sparkle (**LATER**).

These are all cosmetic. For M1, flash plus scale is enough (Clutter implicit animation, easy).

Click: clicking the main sprite opens that species' Pokédex detail (`DexEntryLink`, `CV:608`). It has a hover highlight and a tooltip `dexOpenEntryHint`. An egg or unknown species has no link and no hover state.

**B. Divider**

**C. Header / usage totals** (`Popover:191-290`):
1. Caption `todayTokens` (secondary).
2. The big row: `TokenFormatter.compact(todayTotal)` in **28 pt bold monospaced**. Beside it a caption with the grouped number (`1,150,000`), shown only if ≥ 1,000. The locale's separators are used, not the app language (en/ko/ja `253,412,890`, es/de `253.412.890`, fr/ru `253 412 890`). On the right: today's cost in callout, secondary color, only if `showsCost`.
3. Period row (`ViewThatFits`): `"This week 3.2M $7.73"  "This month 3.2M $7.73"` (each: a tertiary caption label, a semibold compact value, a secondary cost). To the right: the **StreakBadge** and the **Details** button (`recapButton`, a capsule, opens the recap screen).
   - It collapses to a vertical layout if the row is too wide.
   - **Details/recap = LATER.** Hide the button for M1.
   - StreakBadge (`StreakBadge.swift`), an orange flame plus the day count:
     - Shown only when `streak.days >= 2`.
     - Flame is `.orange` when today is included, else `.secondary` (gray).
     - Tooltip `streakDays(n)`, or `streakDaysPending(n)` ("use it today to keep it going").
     - It is borderline M1/LATER. It is cheap (a `St.Icon` plus a `St.Label`), so keep it if the streak data is already computed.
4. **ActivityPanel** (`ActivityVisualization.swift`). Click anywhere on it cycles between two views; the second is the 16-week heatmap (**LATER**).
   - The tooltip is `activitySwitchHint` ("Click to switch the view").
   - Two page dots (4 pt circles, `secondary` at 0.75 or 0.25 opacity) at the right of the caption row are the only hint that it cycles.
   - **M1: the daily bar chart only. Drop the cycle and the dots**, or leave them hidden until the heatmap exists.
5. Provider tabs (`ProviderTabBar`, capsule tabs) appear only when more than one provider exists. **Claude Code only for M1, so not shown.**
6. **Provider row** (`providerRow`, `Popover:320-362`) for the selected provider:
   - Name (callout semibold) left; today's compact tokens right, plus cost caption if the provider reports cost.
   - Two rows of token types: `Input X  Output Y` and `Cache write X  Cache read Y`. A tertiary label and a secondary monospaced value, caption2.
   - If the day used more than one model: one row per model sorted by tokens descending. The model name is the last `/`-separated component (caption2, middle-truncated), and the token value is tertiary on the right.

**D. Divider**

**E. Provider status banner** (`providerStatusBanner`, `Popover:~395`): a 7 pt dot plus label plus one-line description, **only if** the status page reports an incident. Display-only, never a notification. Color per indicator:

| Indicator | Color |
|---|---|
| operational | green |
| minor / maintenance | yellow |
| major | orange |
| critical | red |
| unknown | gray |

It is gated by the "Status checks" setting. It is borderline M1; cheap to defer.

**F. Limits section** (`limitsSection`, `Popover:~430`), shown when `selectedProviderHasLimits`. For Claude that is `!disableKeychainAccess || limits != nil || limitsAuthExpired || additionalLimits non-empty`.
- Header caption: `limitsOfficial` ("Limits (official)"), plus a `questionmark.circle` help button if no session key is configured. The button opens a 250 pt popover with a title, body and a "Register session key" button that jumps to Settings → Advanced. **Keychain-specific, so for Linux this becomes a "credentials file not found" hint.**
- Then, in this priority order, exactly one of:
  1. `sessionKeyExpiredNotice` (orange banner), if the auth expiry is `.sessionKey`.
  2. `claudeAuthExpiredNotice`: orange-triangle title plus a "Retry" button (spinner while busy) plus a hint caption2. Background is orange 12%, radius 8, padding 8.
  3. `claudeLimitsRefreshRow`: shown if the limits are missing or stale (and the Keychain is not disabled). It reads `staleLimits · <relative time>` in orange, or `limitsTapToLoad` in secondary, with a **Refresh** button. A failure message in orange caption2 appears under it so the button never looks inert. This row exists because auto-polling never touches the Keychain (to avoid prompts), so the user taps to load.
     - **On Linux, reading `~/.claude/.credentials.json` needs no prompt, so this row should auto-refresh and mostly vanish.** Keep a retry for the error case.
- **Account tabs**: a capsule tab bar, only when more than one Claude account exists. The tab for the tracked account (the one the menu bar follows) is marked with a dot. **LATER** (multi-account).
- **Per-account content** (`claudeAccountLimits`, `Popover:~944`), in order:
  - Stale badge (non-default accounts).
  - Plan line: `plan(planDisplay)` (caption, tertiary).
  - Account line: `limitsAccount(email)` (caption, tertiary).
  - Per-account usage row (multi-account only).
  - **Limit rows**, all via `quotaRow`:
    - `fiveHourSession` ("5-hour session") with an `idleHint` of `fiveHourNotStarted` when the window has not started.
    - Then the `forecastRow` for the tracked account (see below).
    - `weekly`.
    - `weeklyOpus` and `weeklySonnet` (only if present).
    - Every entry of `scopedLimitEntries` named by `claudeLimitEntry(kind, model)`.
    - The whole block is at **50% opacity when the session expired**, to show "not a current value".
  - **Current 5h block row**: `claudeCurrentBlock` label plus compact tokens on the left, and `"Reset <relative> (<clock>)"` on the right, shown if a block with an end date exists.
- **forecastRow**: shown only if `store.fiveHourForecast` exists.
  - Warning triangle (orange) plus `forecastReach("HH:mm")` ("At current rate, limit hit at 16:20") when the depletion is before the reset.
  - Otherwise a `checkmark.circle` plus `forecastNoReach` in tertiary.
  - This is a burn-rate extrapolation of the 5h window.
- Stale rows use `staleBadge`: `staleLimits · <relative>` in orange.

**quotaRow** (`Popover:758-797`), the most important custom widget. A VStack of:
- Row 1: left `name` (callout). Right, in order: optional `detail` (caption, secondary), then `reset` or `idleHint`, then the percentage text.
  - The reset text is `"<relative duration> (<clock>)"`, tertiary caption. Examples from the screenshots: `1h 10m (14:30)` and `4d 2h (Sep 16, 15:20)`; the second is the localized relative style, which on macOS prints `1 hr, 59 min`.
  - The clock suffix (`resetClockSuffix`, `Popover:713`): 24-hour `HH:mm` only if the reset is today or within 6 hours. Otherwise weekday, day and time (`EEEEdHHmm`) so "in 2 days" is unambiguous. It uses the app language's locale.
  - The percentage is `limitPercentText`: `"62%"` (mode `used`) or `"85% left"` (mode `remaining`, using `percentRemaining()`). It is callout, monospaced, and colored by `gauge.percentColor` (**the yellow tier uses the primary text color**, because yellow text is unreadable on a light background).
- Row 2: the **LimitProgressBar**: a native `ProgressView` (6 pt track, `.small`) filled to `displayPercent` and tinted by the gauge tier color. If a pace is known there is also a marker.
  - **Pace marker** (`Popover:1895-1940`): a 2.5 pt wide, 10 pt tall rounded vertical tick (`primary` at 0.55 opacity). The track is 6 pt and the tick overhangs 2 pt above and below, with a corner radius of half the width. Position is `(width-2.5)*fraction`.
  - The marker fraction goes through the **same `displayPercent` transform as the fill**, so in "remaining" mode both flip (the tick moves to `100 - pace`).
  - The marker appears only when both the reset time and the window span are known, and the elapsed fraction is within 0…1. There is no marker for Codex personal-spend rows, for the Cursor billing cycle, or for a stale `resets_at`.
- Whole-row tooltip (`paceHelp`): if a tier exists, line 1 is `"<tier name> · <delta>"` (e.g. `Under pace · …`), line 2 is `paceHint("62%")` ("Pace — an even burn across this window would sit at 62% now."). With no tier (the early-window hold) only the hint is shown. No pace means no tooltip.

Pace logic (`Core/PaceTier.swift`):
- `pace` = fraction of the window elapsed = `(span - timeUntilReset)/span`. It is **nil**, not clamped, if outside 0…1 or if the span is unknown. The span is 5 h for the 5h window and 7 d for the weekly one.
- The tier is held (nil) while `pace < 0.1` (30 min of a 5h window, about 17 h of a week). Callers fall back to absolute thresholds.
- Otherwise: `utilization >= critThreshold` gives `wayOver`. Else `delta = round(utilization - pace*100)`:

| delta | tier |
|---|---|
| < -25 | wayUnder |
| < -10 | under |
| < 5 | onPace |
| < 15 | slightlyOver |
| < 30 | over |
| ≥ 30 | wayOver |

- The no-pace fallback (`gauge`): `>= crit` gives `wayOver` (red), `>= warn` gives `over` (orange), else `onPace` (green).
- Defaults: warn 80, crit 95 (`UsageStore.swift:952-954`). The two always keep a gap (`thresholdGap`; the value is not shown in my excerpts).
- Tiers are judged on **actual** utilization, regardless of the used/remaining display mode.

**G. Divider**, then the footer.

### 2.2 Footer (`Popover:1103-1160`)

- Left, **Home tab only**: a refresh icon button (tooltip `refreshNow`). The button does not swap to a spinner, because local parsing updates the numbers immediately. Then `"Updated <relative>"` (caption, tertiary), and an orange triangle with the error text as its tooltip when `lastErrorDescription` is set.
- Right, always shown: floating pet eye toggle (`eye` or `eye.slash`, **LATER**), settings gear (tooltip `settings`), power button (**drop**).
- All icon buttons are borderless.

### 2.3 Daily bar chart (`MonthDailyTrend`, `Popover:1170-1480`)

- **Layout** (VStack spacing 3, top padding 4):
  1. **Caption row**: a period dropdown (a `Menu` with `l.title(for:)` and a checkmark on the active item) → readout → spacer → "Peak <compact>" (shown only if it carries information, `showsPeak`) → the page dots.
     - The readout is today's `"Thu, 11/5 1.1M $2.76"` (`readout()`: day stamp, compact tokens, optional cost). It is always **today's** value, not the hovered one. The hover shows a card instead.
  2. **Bar row**: track height **26 pt**, bar spacing 1.5, minimum "baseline" height 1.5 pt so a zero day is still a visible tick. Bar height is `max(1.5, tokens/peak*26)`.
     - Today's bar is accent color. Other days are `secondary` at 0.45. An empty day is 0.18. A hovered day is 0.8.
     - Future days (in the month view) are blank, with no hover target.
     - The bars are stacked per provider when more than one provider was used, with opacity 0.55 unless today or hovered. **Single-provider for M1, so plain bars.**
  3. **Weekend tick row**: 1.5 pt, `secondary` at 0.5, under weekend columns (the locale's calendar decides which days are the weekend).
  4. **Axis row**: tiny labels.
     - Today's day-of-month is always labelled, accent-colored. The 1st of the month is also labelled in the rolling windows. Rolling windows label every 7th day back from today, with collision suppression (minimum 3-day separation).
     - The month window labels the 1st, then each multiple of 7, but not within 3 days of today.
     - The pure function `axisLabel` (`Popover:1590-1646`) is what to port.
  5. **Legend** (only when stacked): 6 pt swatches plus names in caption2. Not needed in M1.
- **Windows** (`DailyTrendWindow`, `Core/Models.swift:854`): `7d`, `14d`, `21d`, `month`. **Default 14d.** The setting is persisted as `dailyTrendWindow`. The period can be switched from the chart's dropdown and from Settings.
- **Hover tooltip** (`tooltipCard`): a card floating above the bar row, clamped horizontally inside the chart.
  - First line: the day stamp in semibold, the tokens, the cost in secondary.
  - Then one row per provider (swatch, name, tokens, cost); for M1 with one provider this repeats the first line, so it can be omitted.
  - Card style: window background, 6 pt radius, 0.5 pt border `secondary` at 0.35, shadow `black` at 0.25, radius 4.
- **Empty and loading**: if `peak == 0` (every day zero in the window), the chart disappears and only the caption row with the dropdown remains. That happens only if the user has history in another window (`shouldShowSurface = peak > 0 || (selectable && hasAvailableHistory)`). With no history at all, nothing is drawn.

### 2.4 States summary

| State | Behaviour |
|---|---|
| First launch / loading | Top bar shows `—` until `lastUpdated` is set. The popover shows the egg with an incubating hint and the "first run" line. |
| No usage today | Big number `0`. The chart shows only baseline ticks. `companion` goes to a sleep or idle state. |
| Limits missing | Refresh row ("Load official limits" with a Refresh button, per the home screenshot). |
| Limits stale | Orange `staleLimits · <relative>`. |
| Auth expired | Orange banner with Retry. Rows at 50% opacity. |
| Update error | Orange triangle in the footer (tooltip is the error). |
| Provider incident | Colored dot plus text banner. |
| Dex empty | Pikachu sprite at 96 px, `dexEmptyTitle`, `dexEmptyHint` (centered). |

---

## 3. Pokédex (Collection tab)

Entered through the Collection tab (`CollectionView`, `CV:1049-1330`). It is 520 pt tall.

### 3.1 Structure and navigation

- If `dexEntries` is empty, show the empty state above.
- If `nav.dexDetailCollectionID` is set, show **`PokemonDetailView`** (see 3.4), full height.
- Otherwise show:
  1. A **segmented picker** "Pokédex | Catch log" (`nav.showingCollectionLog`).
  2. The **filter toolbar** (shared by both segments).
  3. The grid or the log.
- **Back behaviour.**
  - Detail has a "‹ Back" borderless button at top-left and `#<id>` in secondary at top-right (`CV:1752-1760`).
  - Back clears `dexDetailCollectionID`. Coming from the grid, it remembers the species as `dexReturnAnchor` so the grid reopens on the page that contained it.
  - Coming from the log, it scrolls the log back to the entry (`logReturnAnchor`, scroll to `.center`, run twice after 50 ms because lazy row heights are estimated).
  - Switching segment, leaving the tab, or opening Settings or Recap **drops the detail**.
  - Clicking a sprite on **Home** calls `openDexEntry`: it switches to the Collection tab and opens that species' detail. The segment is left as it was.
  - Settings → "Representative Pokémon → Choose from Pokédex" opens the Pokédex segment, forced to the grid.

### 3.2 Filter toolbar (`CV:1128-1240`)

Left to right:
- **Search field**: magnifier icon, placeholder `dexSearchPlaceholder` ("Search name or #…"), 13 pt. An `xmark.circle.fill` clear button appears when non-empty. Style: control background, 6 pt radius, 0.5 pt border `secondary` at 0.2. It matches by name or number.
- **Shiny-only toggle**: a 16 pt `sparkle` icon, which becomes `sparkles` bold yellow when active, with a yellow-18% background and a yellow-60% border. Tooltip `filterShinyOnly`.
- **Sort menu**: icon `arrow.up.arrow.down`. It is accent-tinted when the sort is non-default. Tooltip `sortTitle`. Items show a checkmark on the active one:
  - Dex segment (`DexSortOption`): `numberAsc` (default), `numberDesc`, `nameAsc`, `nameDesc`, `rarityDesc`.
  - Log segment (`CatchLogSortOption`): `recentFirst` (default), `oldestFirst`, `numberAsc`, `numberDesc`, `nameAsc`, `nameDesc`, `rarityDesc`.

### 3.3 Grid (`DexGridView`, `CV:1463-1720`)

- **Paging, not scrolling.** 4 columns × 4 rows = **16 per page** (`columns=4, rows=4`, spacing 4). The grid fills the remaining height, so cells flex vertically.
- **Header** (above the grid):
  - Row 1: `dexTitle` (callout semibold) + `dexSpeciesTotal(n)` (caption, secondary) + spacer + **earned medals** (emoji capsules; national and Unown tiers only, with 🏅 for a region badge, ✨ for a shiny badge, 🔤 for Unown, 🌍 for national; tooltip `dexBadgeLabel`).
  - **Region chips** (horizontally scrollable): `RegionTally`, one per region from `dexCompletions`: label plus `owned/total`. A completed region is green and shows 🏅 (✨ if shiny-complete). It is disabled when `owned == 0`. Tapping toggles the region filter. Tooltip `dexRegionProgressLabel`.
  - **Rarity chips** (scrollable), in order **legendary, rare, uncommon, common** (`rarityDisplayOrder`): a color dot, label, bold count, and a checkmark when selected. Disabled at zero count. Tapping toggles the filter. Tooltip `dexFilterHint`. The chip has a 0.12 tint (0.22 when selected), a 0.5 pt border at 0.35 opacity (1.5 pt solid when selected), and 0.4 opacity at zero.
- **Cells** (`DexSpeciesCell`, `CV:2080`): a 56 px sprite, name (12 pt, 2 lines, space reserved), `#id` capsule at top-left **colored by rarity**, ✨ capsule at top-right if the species was ever caught shiny, an orange "RAISING" badge over the sprite bottom if currently raised, a ⭐ star next to the number if it is the representative.
  - Forms: species with forms show `Name n/total` as the label, use the default-form sprite, and open a form picker on click.
  - Background: representative = accent at 0.16, selected = `secondary` at 0.16, else 0.06. Selected cells get a 1.5 pt accent border.
  - **Hover**: scale 1.04 with a card shadow behind it, 120 ms ease-out.
  - Tooltip: `"#25 Pikachu · Rare · ✨ Shiny · Raising · Representative"` (parts joined by ` · `, only present ones).
  - Right-click menu: "Choose form" for form species, else "Set / Follow current as representative".
- **Footer** (18 pt high): the selected cell's `"#id Name · Rarity"` in 11 pt secondary, plus a mini-bordered star button to set or clear the representative (not shown for form species). Then pagination, if more than one page: `‹  2 / 4  ›` with 28×28 hit targets (the buttons are disabled at the ends).
  - **Mouse wheel and trackpad paging**: one notch = one page; a precise gesture accumulates at least 24 pt then fires once per gesture, and ignores momentum (`DexScrollPager`, `CV:1381`).
  - Any filter, sort or search change returns to page 1 and clears the selection.
- **No results**: a magnifier icon, `noSearchResults`, and a "Clear filters" button (resets search, shiny, rarity, region and sort).
- Names are backfilled asynchronously (`backfillMissingDexNames`).

### 3.4 Species detail (`PokemonDetailView`, `CV:1722-2078`)

Scrolling column, top to bottom (with the 12 pt scroller lane on the right):
1. **Form picker**, if the species has forms: title `formsCollected(n, of: total)`, then a grid of 3-7 columns (`formPickerColumns`: ≤8 forms gives min(count, 4) columns, 9-20 gives 6, 21+ gives 7). Each cell is a 36 px sprite plus a short label; uncollected forms are at 0.25 opacity and disabled; the selected form has a 1.5 pt accent border; the representative form has an accent-16% background; ✨ if shiny.
2. **Appearance picker** (segmented Normal | ✨ Shiny), only if both a normal and a shiny exist.
3. **Identity header**: a 104 px animated sprite with the name (title3 bold), rarity label (callout semibold, secondary), type badges (size 11, padding 7/2), the default-form note, `✨ Shiny`, a "Raising" label in accent color if this individual is the active one, and the representative star button.
4. **Individual picker** (a menu), only if more than one individual exists. Label format: `"#1 · ✨ Shiny · Lv. 5 · 11/18/26"`, or `Raising` or `—` when there is no catch date. The date is formatted in the app language's locale.
5. If there are no individuals: `dexAppearancePreview` text.
6. **Details**, if the PokéAPI data is loaded:
   - **Individual card** (when the selected individual has a profile): the title `pokemonIndividual`, then `Level / Gender / Nature` pairs (caption label over semibold value), an optional prayer text (**LATER**; ignore), `Ability` (name via PokéAPI translation, with `· Hidden` suffix), **Actual stats** (6 rows) and **Known moves** (name and `Lv. N`).
   - Otherwise a **Base stats** card.
   - **Species data** card: height in m, weight in kg, base-stat total, plus Possible abilities (joined with ` · `, a hidden marker).
   - **Complete move list** card: each move with its learn methods right-aligned, with dividers, in a `LazyVStack`.
7. **Failure**: `pokemonDetailsUnavailable` plus a Retry button. **Loading**: spinner plus `loadingPokemonDetails`.

Stat row (`statRow`, 12 pt): a 74 pt wide stat name, a `ProgressView` (accent tint, value over `scaleMaximum`), a 30 pt right-aligned value, and a 38 pt `IV nn`. Base stats use scaleMaximum 300; actual stats use `displayScaleMaximum`.

All cards use the same style: padding 9, `secondary` at 0.07, radius 9.

### 3.5 Catch log (`CV:1264-1328`, `DexEntryRow` `CV:2212`)

- Header (`DexSummaryHeader`): "Catch log" (callout semibold) + `dexTotal(n)`, then the same rarity chips (they filter the log).
- Scrolling `LazyVStack` of cards (spacing 8). Each card (padding 8, `secondary` at 0.06, radius 10):
  - Row: rarity badge (uppercase, size 10, solid color), optional "RAISING" (accent tinted) or "RELEASED" (secondary tinted) badge, ✨ if shiny, spacer, nature name (11 pt, secondary).
  - **EvoLineView** of the individual's chain of species: 68 px thumbnails, with a name label under each (11 pt, up to 2 lines), arrows between them. Sprites link to the species detail.
  - `caughtAt` as a relative date (e.g. "2 yrs, 10 mths"), 11 pt, secondary.
- Rarity chip filter, shiny filter, search and sort all apply. Changing any of them scrolls to the top with a 0.2 s animation.
- Empty-search state is the same as for the grid.

---

## 4. Settings

Layout (`SV:55-100`): a 460 pt tall panel with a header (a "‹ Back" accent button, with Esc as the cancel shortcut, plus a centered "Settings" headline), a scrollable body (16 pt padding, 18 pt between groups), and a footer. The footer reads `v<version> · GitHub · Website · ♥ Sponsor` in caption2, tertiary. The group style (`settingsSection`, `SV:968`) is an uppercase caption heading and a rounded rectangle containing `groupRow`s separated by dividers. Each row has a label (callout) and a trailing control, with an optional caption2 tertiary hint below the label.

Order as shown: General, Difficulty, Show in menu bar, Tracked providers, Floating pet, Notifications, Updates, Save transfer, Advanced, About and support.

Legend: **M1** = build now. **LATER**. **DROP** = macOS-only or not applicable.

### General (`SV:185-293`)

| Row | Control | Default | Tag |
|---|---|---|---|
| Language | menu, 8 languages | system | M1 if you localize; otherwise follow the GNOME locale and drop it |
| Representative Pokémon | menu: "Follow current" (checkmark), "Choose from Pokédex…" (opens the dex segment), shows `#479 Rotom [Heat…]` style | follow | M1 (affects the top-bar sprite) |
| Refresh interval | menu: Manual / 1 / 2 / 5 / 15 min | 2 min | M1 |
| Animation (hint: "Smoother uses more battery") | menu: Power saver / Balanced / Smooth | Power saver | M1 (top-bar animation fps) |
| Limit display | segmented: Used / Remaining | Used | M1 |
| Daily trend period | menu: 7d / 14d / 21d / month | 14d | M1 |
| Tracked Claude account | menu (Automatic / Highest), only with more than one account | | LATER |
| Launch at login | switch, disabled when not an .app bundle | | **DROP** (GNOME extensions load with the shell) |

### Difficulty (`DifficultySettingsSection.swift`)

Two log-scale sliders, range 0.1× to 2.0×, default 1.0 (snaps to 1.0 near the middle). Growth ("Easy" to "Hard") and Shop prices ("Cheap" to "Expensive"). A value label (`difficultyValue`, e.g. `100%`) sits on the right. The values are held in a local **draft**, and a "Save" prominent button appears only when the draft differs from the stored values.
- Growth difficulty: **M1** (it scales the hatch and evolution thresholds).
- Shop prices: **LATER**.

### Show in menu bar (`SV:301-333`)

| Row | Default | Tag |
|---|---|---|
| Today's tokens | on | M1 |
| Today's cost ($) | off | M1 |
| Pokémon growth % (with hint) | off | M1 |
| Limit % | off | M1 |
| Limit color (menu: Gauge / Attention only / Off; shown only when Limit % is on, with a hint) | gauge | M1 |
| Hint line below the group: `allOffHint` | | M1 |

### Tracked providers (`SV:335-348`)

One toggle per registered provider, plus a hint. **LATER** (only Claude Code for now; hide the group).

### Floating pet (`SV:350-397`)

Enable, "Show back view" (`alwaysFacesBack`), size slider 48-384 px in steps of 8, bubble alerts, agent activity. **LATER**, with one exception: `alwaysFacesBack` also affects the top-bar sprite, so keep that toggle if you want parity (the right-click menu has it too).

### Notifications (`SV:399-435`)

| Row | Default | Tag |
|---|---|---|
| Limit alerts | on | M1 |
| Warning threshold slider, 50-95%, step 5 (shown if limit alerts are on) | 80 | M1 |
| Critical threshold slider, 80-100%, step 5 | 95 | M1 |
| Companion events (hatch / evolve / graduate) | on | M1 |
| Provider status checks (with hint) | | LATER |

### Updates (`SV:437-491`)

Update notifications, "Check now". **DROP** (distribution is via extensions.gnome.org).

### Save transfer (`SV:493-600`)

Export, import, create snapshot, snapshot history with restore. **LATER** (save backup screen).

### Advanced (collapsible; `SV:753-935`)

- Session key (`sk-ant-sid…`) with a save, delete and organization picker, plus per-account session keys: **LATER** (alternative auth for limits).
- Disable Keychain access, refresh limit token: **macOS-only DROP** (replace with "credentials file path" if desired).
- Custom scan roots: **LATER** (extra paths to scan for Claude data; may matter on Linux, e.g. a non-default `~/.claude` or `CLAUDE_CONFIG_DIR`).
- Tracked account mode, additional Claude config dirs: **LATER**.

### About and support (`SV:937`)

Report a problem (mail), show log file. **DROP or LATER.**

---

## 5. Notifications

Delivery: `UNUserNotificationCenter` with `trigger: nil`, which posts immediately. They are posted only for the bundled app (`AppEnv.isBundledApp`). Authorization is requested the first time the popover opens.

### 5.1 Companion events (`CompanionStore.notifyCompanionEvent`, `CompanionStore.swift:1542-1556`)

- Gate: `companionNotifications` (default on).
- Sound: default.
- The event is also recorded as `latestEvent` for the floating pet, **before** the gate. Use it for an in-popover toast if desired.
- Identifier `companion-event-<seq>`.

| Event | Trigger | Title key | Body key | Line |
|---|---|---|---|---|
| Hatch | egg usage reaches the hatch threshold | `notifHatchTitle` ("🥚 Hatched!") | `notifHatchBody(name)` ("`<name>` hatched from the egg!") | `CompanionStore.swift:1814` |
| Shiny hatch | same, shiny roll | `notifShinyHatchTitle` ("✨ Shiny Pokémon!") | `notifShinyHatchBody(name, odds)` ("A shiny `<name>` hatched! (1 in `<odds>`)") | same |
| Prayer hatch | hatch with prayers (**LATER**) | the same titles | `notifPrayerHatchBody` | same |
| Evolve | stage change | `notifEvolveTitle` ("✨ Evolved!") | `notifEvolveBody(from, to)` ("`<old>` evolved into `<new>`!") | `:1054` |
| Graduate | final-form threshold reached | `notifGraduateTitle` ("🎓 Graduated!") | `notifGraduateBody(name)` ("`<name>` — saved to your Pokédex! A new egg has arrived.") | `:1141` |
| Ditto reveal | disguised Ditto reveals | `notifDittoRevealTitle` or `notifShinyDittoRevealTitle` | `notifDittoRevealBody(disguise)` or `notifShinyDittoRevealBody` | `:1870` |
| Candy | rare candy granted when a limit window fills (**LATER**, bag) | `notifCandyTitle(item, count)` | `notifCandyBody(window)` | `:1521` (`showsOnPet: false`) |
| Dex completion | region, Unown, national, and their shiny variants | `notifDexRegionCompleteTitle(region)`, `notifDexRegionShinyCompleteTitle`, `notifDexUnownCompleteTitle`, `notifDexNationalCompleteTitle`, `notifDexNationalShinyCompleteTitle` | the matching `...Body` keys | `:524`; one-shot per badge key in `notifiedDexCompletions`; **LATER** (dex medals) |

### 5.2 Limit alerts (`UsageStore.swift:2165-2275`)

- Gate: `limitNotifications` (default on).
- Edge-triggered per window `key`. Tier 1 = utilization ≥ warn, tier 2 = utilization ≥ crit.
  - It fires only when the tier **rises**. Staying in the same tier does not re-fire.
  - Dropping below warn removes the key, which re-arms it. A window reset therefore re-arms.
  - Volatile fields such as `resets_at` are never used in the key, because that caused repeat notifications in the past.
- Title: `notifCritical` ("Limit imminent") or `notifWarning` ("Limit warning").
- Body: `notifBody(windowName, percent)` ("`<window>` at `<percent>`"), with ` · <account title>` appended when multiple Claude accounts exist.
- Sound: default for critical only; warnings are silent.
- Identifier: `<key>-warning` or `<key>-critical`.
- Windows covered (Claude): five-hour (`l.claudeFiveHour`), weekly (`l.claudeWeekly`), `"Claude Weekly Opus"`, `"Claude Weekly Sonnet"`, and every scoped limit entry. Codex and Antigravity are **LATER**.
- A second consumer exists: the floating-pet bubble (6 s TTL, picks critical first, then highest utilization), which is **LATER**.

### 5.3 Not in M1

Update-available notifications (DROP).

---

## 6. Colors and theme

The app uses **system semantic colors** almost everywhere, so there is no custom hex palette except the type chart and the provider palette. On GNOME use the Adwaita/GNOME Shell palette for the semantic names. Suggested mapping, which is my own choice, not from the source: `.green` to `#33d17a`, `.yellow` to `#f6d32d`, `.orange` to `#ff7800`, `.red` to `#e01b24`, `.blue` to `#3584e4`, `.teal` to `#2190a4`.

**Pace tiers** (`Popover:1941-1960`):

| Tier | Color name | macOS meaning |
|---|---|---|
| wayUnder | blue | far below pace |
| under | teal | below pace |
| onPace | green | on pace (also the absolute "calm" color) |
| slightlyOver | yellow (percent text uses the primary text color) | slightly over |
| over | orange (also absolute ≥ warn) | over |
| wayOver | red (also absolute ≥ crit) | way over |

**Rarity** (`CV:3-10`): common = gray, uncommon = green, rare = blue, legendary = orange. A nil rarity is gray. Order in chips: legendary, rare, uncommon, common.

**Pokémon type chart** (`Badge.swift:7-42`), classic type-chart hexes:

| Type | Hex | Type | Hex | Type | Hex |
|---|---|---|---|---|---|
| normal | AAAA99 | fire | FF4422 | water | 3399FF |
| electric | FFCC33 | grass | 77CC55 | ice | 66CCFF |
| fighting | BB5544 | poison | AA5599 | ground | DDBB55 |
| flying | 8899FF | psychic | FF5599 | bug | AABB22 |
| rock | BBAA66 | ghost | 6666BB | dragon | 7766EE |
| dark | 775544 | steel | AAAABB | fairy | EE99EE |

Anything outside the 18 types (stellar, shadow, unknown) is gray. A solid badge is white 8 pt bold text on the color, with a faint 0.5 px black shadow (`black` at 0.35) for type badges so white text stays legible on the light fills.

**Provider chart palette** (`Popover:1198-1215`; 16 colors, only the first used in M1). RGB triples ×255:
- `0.85,0.47,0.34` = Claude orange-brown (approx. `#D97857`).
- `0.36,0.62,0.95` = blue (approx. `#5C9EF2`), which the home screenshot shows as the Codex bars.
- The remaining 14 are only needed for multi-provider stacks (LATER).

**Other fixed colors**:
- Companion progress bar fill: orange. Track: `secondary` at 0.2.
- Today's bar in the chart: accent color. Axis label for today: accent.
- Streak flame: orange when today counts, gray otherwise.
- Banners: update banner = accent at 0.12 (DROP), auth-expired = orange at 0.12 (8 pt radius), small notice = orange at 0.1 (6 pt radius).
- Stale text is orange. Fine print is tertiary. Cost is secondary.
- Cards: `secondary` at 0.06 to 0.07 backgrounds.
- Shiny button active: yellow 0.18 fill, 0.6 border. Medals: yellow 0.14 fill, 0.5 border.
- Regions: complete = green, otherwise `secondary`.

---

## 7. M1 elements and proposed GNOME equivalents

Difficulty is easy, medium or hard, relative to St and GJS.

| M1 element | GNOME equivalent | Difficulty and reason |
|---|---|---|
| Top-bar button | `PanelMenu.Button` with a `St.BoxLayout` holding a sprite widget and a vertical `St.BoxLayout` of 1-2 `St.Label`s | **Medium.** The two-line stacked text at panel height (about 32 px in GNOME vs 22 on macOS) needs a small font (8-11 pt) and manual vertical centering. Per-item coloring means one `St.Label` per limit item rather than a single colored string, or `Pango` markup (`use_markup`). |
| Animated sprite | `St.Icon`/`St.Widget` with a `Clutter.Image`, or `Clutter.Actor.set_content` of a decoded frame, driven by a `GLib.timeout_add` at the frame delay | **Hard.** GJS has no built-in GIF decoder that yields frames with delays. Use `GdkPixbuf.PixbufAnimation` (which gives the frames and delays) and convert each to a `Clutter.Image`/`St.ImageContent`, caching them. Must reimplement `capFrameRate` decimation, pause on screen-off and while the menu is open (`menu.connect('open-state-changed')`), and `nearest` scaling (set the actor's `Clutter.ScalingFilter.NEAREST` filter). Network fetch with `Soup`. |
| Today's tokens, cost, growth, limit text | `St.Label` (via `GLib.Variant`-free formatting helpers) | **Easy**, formatter functions port directly. |
| Right-click menu | Override `vfunc_event` on the button to detect button 3 and open a second `PopupMenu.PopupMenu` (`PopupMenuItem`s, `PopupSeparatorMenuItem`, a `PopupSwitchMenuItem` or an `Ornament.CHECK` for the "back view" item) | **Medium.** GNOME has no built-in separate secondary menu on a `PanelMenu.Button`, so you add a second menu manager. Keyboard shortcuts in menu items aren't a GNOME convention; drop them. |
| Popover container (360 px) | `PanelMenu.Button.menu` with a custom `PopupBaseMenuItem` (`activate: false`) containing a `St.BoxLayout`, `set_width(360)`, and a `St.ScrollView` for the fixed-height areas | **Medium.** Fixed 520 px height risks exceeding small screens; clamp to the monitor's work area. Reserve a right gutter for the scrollbar (see `scrollerInset`). |
| Tab bar (Home / Collection) | A row of `St.Button`s with a `checked` style class, styled in `stylesheet.css`; or a `St.BoxLayout` with a pill-shaped background | **Easy to medium.** There is no native segmented control; style one with CSS. |
| Companion card | `St.BoxLayout` (horizontal): sprite tile (`St.Bin` with rounded CSS) + column of labels and badges | **Easy.** The hatch and evolve flash and wobble are optional; Clutter `ease()` on opacity, scale and `rotation_angle_z` is simple (**easy**). |
| Badge (rarity, type) | `St.Label` with CSS `border-radius: 999px; padding: 1px 5px; background-color: …; color: white; font-size: 8px; font-weight: bold;` | **Easy.** Generate per-color style strings or classes. |
| LabeledProgressBar with hover expand | `St.Widget`/`St.DrawingArea`, or two nested `St.Bin`s (track plus fill sized by the fraction); hover logic with `enter-event`/`leave-event` and a 300 ms `GLib.timeout_add`; animate `height` via `ease()` | **Medium.** The label-over-fill with a width-clipped text and the spring effect can be simplified to a fade. |
| EvoLineView (scrolling row with arrows, fade edges) | `St.ScrollView` (horizontal) containing a `St.BoxLayout`; arrows as `St.Icon` or labels; edge fades via a `Clutter.Effect` or just skip | **Medium.** Edge-fade gradients need a custom effect (**hard**) but are not essential, so omit them. |
| Big-number header, totals | `St.Label`s with CSS font sizes; `ViewThatFits` fallback → use `St.BoxLayout` with `x_expand`, and let it wrap or just fix at 360 | **Easy** (give up the adaptive collapse). |
| Streak badge | `St.BoxLayout` with an `St.Icon` (symbolic, colored via CSS) and a `St.Label` | **Easy.** |
| Daily bar chart | **`St.DrawingArea` with Cairo** (draw 7-31 rounded bars, baseline tick, weekend ticks, axis labels via `Pango`/`cr.showText`), pointer handling with `motion-event`/`leave-event` to compute the hovered column | **Hard.** Everything is custom: layout math, axis label logic (port `axisLabel`), future days, hover tooltip positioned and clamped over the chart (use a separate `St.Widget` card added to the menu's layout, or `Main.layoutManager.addTopChrome`), theme colors (read from `St.ThemeNode`/CSS since Cairo ignores CSS). Also needs a repaint (`queue_repaint`) on hover change. |
| Period dropdown | `PopupMenu.PopupSubMenuMenuItem`, or a `St.Button` that opens a small `PopupMenu.PopupMenu` anchored to it, with `Ornament.CHECK` on the active item | **Medium.** A popup menu inside a popup menu is awkward in GNOME; a cycling button or inline radio buttons are easier. |
| Limit rows (quotaRow) | Row `St.BoxLayout` (name left, reset text, percent right) over a **custom progress bar**: `St.DrawingArea` or `St.Widget` with a `St.Bin` fill, plus a thin `St.Bin` marker absolutely positioned (use a `Clutter.FixedLayout`/`Clutter.Actor` with manual `set_position`) | **Medium.** The pace marker overlapped on the bar is the trickiest piece. A single Cairo-drawn widget (track, fill, marker, in one `repaint`) is the simplest and gives pixel control. |
| Pace-tier colors | CSS classes `.pace-way-under` … `.pace-way-over`, or direct colors in a Cairo bar | **Easy.** |
| Tooltips (pace, streak, sprite hints) | **No built-in tooltip in St.** Implement a small hover tooltip: an `St.Label` added to `Main.uiGroup`, shown after about 600 ms (the app uses 600 ms, `App.swift:~92`), positioned near the pointer, hidden on leave. A shared helper function is advisable. | **Medium.** The app relies heavily on `.help()`; one helper covers all uses. |
| Reset countdown | Format `"1h 10m (14:30)"` in JS with a `GLib.timeout_add_seconds(30..60)` refresh, `GLib.DateTime.format('%H:%M')` and a weekday form when more than 6 h away; the macOS "relative" style updates live | **Easy.** Own the relative-time formatter (the macOS style prints `1 hr, 59 min`; the other screenshot shows `1h 10m`). |
| Footer buttons | `St.Button`s with `St.Icon` (`view-refresh-symbolic`, `emblem-system-symbolic`) | **Easy.** |
| Pokédex grid, 4×4 paged | `St.Widget` with a `Clutter.GridLayout` (or nested `St.BoxLayout`s) of cells; each cell is an `St.Button` with an `St.Icon`/image, name label and `#id` overlay | **Medium.** Cell overlays (number capsule top-left, ✨ top-right, raising badge bottom) need a `Clutter.Actor` with `FixedLayout` or `St.Widget` with absolute children. |
| Wheel/trackpad paging | `scroll-event` on the grid actor; accumulate `Clutter.ScrollDirection.SMOOTH` deltas to a threshold (24) per gesture; discrete events = one page | **Medium.** Smooth-scroll gestures and momentum ignoring have no phase info in Clutter, so use a time-based latch. |
| Search field | `St.Entry` with a `hint_text` and a clear icon (`St.Entry.set_secondary_icon`) | **Easy.** Text input inside a shell menu has focus quirks: call `entry.grab_key_focus()` on click, and make sure the menu doesn't close on key presses (e.g. set `menu.actor` key handling or use `St.Entry` inside a non-activatable `PopupBaseMenuItem`). Known gotcha: **medium.** |
| Filter chips (rarity, region), horizontal scroll | `St.ScrollView` with a horizontal `St.BoxLayout` of `St.Button`s styled with CSS | **Easy.** |
| Sort menu, shiny toggle | `St.Button` with a `checked` state; a small popup menu or cycle for sort | **Easy to medium.** |
| Species detail | `St.ScrollView` + vertical `St.BoxLayout`; "cards" are `St.BoxLayout` with a CSS background and radius; stat rows with the same custom progress bar | **Medium.** A long scrolling page with many widgets, but all standard. The move list can be hundreds of rows, so build lazily or cap. |
| Catch log | `St.ScrollView` of card `St.BoxLayout`s | **Medium.** There is no `LazyVStack`; a few hundred entries of image plus text can be heavy. Page or virtualize. |
| Prefs window | `prefs.js` with `Adw.PreferencesPage`/`PreferencesGroup`: `Adw.ComboRow` (language, interval, animation, trend period, color mode), `Adw.SwitchRow` (the toggles), `Adw.ActionRow` with a `Gtk.Scale` suffix for warn/crit thresholds (or `Adw.SpinRow`), `Adw.ToggleGroup`/linked `Gtk.ToggleButton`s for Used/Remaining (or a `ComboRow`), `Adw.ActionRow` that launches a chooser for the representative Pokémon | **Easy.** Standard libadwaita rows. The representative-Pokémon chooser needs access to the dex data from the prefs process (which is a separate process from the shell); share state through GSettings or a JSON file. The Difficulty draft with "Save" can become two `Adw.SpinRow`s that write immediately (prefs conventionally apply immediately), or keep the Save button in an `Adw.ActionRow`. |
| Settings storage | `Gio.Settings` with a schema XML (keys for every toggle above) | **Easy.** |
| Notifications | `Main.notify(title, body)`, or `MessageTray.Source` + `MessageTray.Notification` (to control urgency, icon and actions) | **Easy.** Both warning and critical can use urgency (`MessageTray.Urgency.NORMAL`/`HIGH`). One real gotcha: banners are suppressed by Do Not Disturb, while `Urgency.CRITICAL` stays visible, so map "Limit imminent" to `HIGH` or `CRITICAL`. A notification icon needs a `Gio.Icon`; use the sprite file. |
| Data layer | `Gio.File` + async JSONL parse of `~/.claude/projects/**/*.jsonl` (or `$CLAUDE_CONFIG_DIR`); `Soup.Session` for the OAuth usage endpoint with the token from `~/.claude/.credentials.json`; PokéAPI via `Soup` with an on-disk cache | **Medium to hard overall**, mostly because of volume. Large directories need chunked async reads (`Gio.File.enumerate_children_async`) to avoid freezing the shell, which runs everything on one thread. |

---

## 8. Localization

- **Structure** (`Core/Localization.swift`, 1,991 lines): a single `struct L` with `lang: AppLanguage` (`ko, en, ja, es, fr, pt, de, ru`; `Core/CompanionModel.swift:10`).
  - **Keys are Swift properties and functions, not string keys.** The one primitive is `t(ko, en, ja, es, fr, pt, de, ru)`, which returns the argument for the active language.
  - There are about **424** `var`/`func` accessors (**481** `t(` call sites; some accessors call `t` more than once, and some are helper switches).
  - Parameterized strings are functions with interpolation, for example `notifBody(name, percent)`.
  - Korean uses a particle helper (`KoreanParticle.subject.attach(to: name)`, `Core/KoreanParticle.swift`) to pick the correct postposition (이/가, 은/는, 을/를, 로/으로). Other languages need nothing special.
  - Plural handling is **not** implemented via a plural system; the strings are written to avoid plural forms (`"\(count)-day streak"`).
- **Pokémon, move, ability and type names** come from PokéAPI's multilingual data, resolved through `PokemonNameLocalization` and `PokemonNameLabel` (`PokemonNameLabel.swift`). They fall back to English if the language is missing; **Russian has no PokéAPI data yet**, so it falls back to English. The label shows `…` until loaded, or the identifier if the fetch failed.
- **Dates and clock**: the locale comes from `companion.language.displayLocale`, set as the SwiftUI environment locale on the popover, so weekday names follow the app language, not the system. Grouped-number separators follow the **system** region instead (`TokenFormatter.grouped`). Reset clocks are always 24 h.
- **Language selection**: an in-app setting (`companion.setLanguage`), mirrored into `store.localizationLanguage` so background notifications use the same language.

**Recommendation for gettext in GJS:**
1. Use English source strings as `msgid`s, wrapped in `_()` from `Extension`'s `gettext` helper (`import {gettext as _, ngettext} from 'resource:///org/gnome/shell/extensions/extension.js'` in the extension; in `prefs.js` use `ExtensionPreferences`' `gettext`). Declare `gettext-domain` in `metadata.json`.
2. Keep the translations in `locale/<lang>/LC_MESSAGES/<domain>.mo`, built from `po/*.po` with `xgettext`/`msgfmt` and shipped inside the extension zip (extensions.gnome.org compiles them if the `po` directory is present; if you ship `.mo` files, they must be compiled before packaging).
3. Convert the `t(ko, en, …)` tuples mechanically: the English column becomes the `msgid` and the rest become per-language `msgstr`s. A one-off script can emit `.po` files from the Swift source (regex on `t("…", "…", …)` and on the interpolated variants, mapping `\(name)` to `%s` or `{name}`).
4. For interpolation use positional placeholders so translators can reorder: `_("%s evolved into %s!").format(a, b)` or named placeholders via a tiny `format()` helper (`{old}`/`{new}`); avoid string concatenation. Add translator comments (`// Translators:`).
5. Use `ngettext` for the few count strings (streak days, forms collected) so plural rules are right per language (Russian has 3 forms; the Swift code sidesteps this with wording).
6. **The in-app language picker is probably unnecessary.** GNOME apps follow the session locale (`LANGUAGE`/`LANG`). Drop the picker, or keep it only for the Pokémon-name language by passing the chosen code into the PokéAPI name resolver. The shell does not honor a per-extension locale override cleanly, so keep the UI text on the session locale.
7. Korean particles will not follow automatically. Either keep `KoreanParticle` as a small JS port invoked only when the locale starts with `ko`, or reword the Korean strings to avoid particles.
8. Defer: M1 can ship with English plus whichever of the other 7 languages you convert first; the Pokémon names are independent of the UI-string catalogue.
9. Count: budget about 420-480 translatable strings in total for the full app. M1's subset (header, limits, dex, settings and notifications) is roughly **150-200 strings**, my estimate from the groups above (not counted exactly).

---

## 9. LATER (listed briefly)

Shop tab (`ShopView.swift`), Bag tab (`BagView.swift`: candies, mints, evolve, graduate controls), usage recap screen (`UsageRecapView.swift`, opened from the Details button), floating desktop pet (`FloatingPetPanel.swift`, with the bubble alerts and agent activity URL events), the 16-week activity heatmap (`ActivityHeatmap.swift`; the Activity panel click-cycle and page dots), multi-provider tabs (Codex, Antigravity, Cursor, OpenCode, Grok limits and their `ProviderTabBar`), Claude multi-account tabs, save backup screen (export, import, snapshots), the 8-language localization, prayers (born-with-prayers hatch text), candy "+XP" and mint sparkle effects, and Dex completion medals and notifications.

---

## Summary (hardest M1 parts in St)

1. **Animated top-bar sprite.** GJS has no ready GIF-frame pipeline. Frames and delays must be decoded with `GdkPixbuf.PixbufAnimation`, converted to textures, decimated for the fps setting, and paused when the menu opens or the screen sleeps. It has to run without relayout. Nearest-neighbour scaling needs an explicit filter.
2. **Daily bar chart.** There is no chart widget. It needs a Cairo `St.DrawingArea` with custom bar layout, an axis-label algorithm with collision rules, weekend ticks, hover hit-testing and a floating tooltip card. Cairo also ignores CSS, so theme colors must be passed in by hand.
3. **Limit progress bar with a pace marker.** The percent fill, tier color and an overhanging tick that flips with used/remaining mode are best drawn as one custom widget.
4. **Tooltips everywhere.** St has none, and the app's `.help()` is used across the home view, dex cells and chart. A single hover-tooltip helper is needed.
5. **The Pokédex.** The paged 4×4 grid with overlay badges, scroll-gesture paging, and a search `St.Entry` inside a popup menu (keyboard focus and menu-close quirks).
6. **The data layer.** Chunked async JSONL scanning must not freeze the shell's single thread.

Everything else, including the prefs window, notifications, badges and settings storage, is easy or medium.