# Floating desktop pet: upstream spec and GNOME Shell 51 port plan

Status: research draft, 2026-11-19. Nothing here is implemented yet.
Upstream baseline: PokeTokenBar `38e3e55` (v2.5.7). Target: GNOME Shell 51.1, Wayland, GJS 1.90.1.

## Citation keys

Swift paths are relative to the upstream repo root (the Swift PokeTokenBar checkout).

| Key | File |
|---|---|
| `FPP` | `Sources/PokeTokenBar/UI/FloatingPetPanel.swift` |
| `AA` | `Sources/PokeTokenBar/Core/AgentActivity.swift` |
| `US` | `Sources/PokeTokenBar/Core/UsageStore.swift` |
| `CS` | `Sources/PokeTokenBar/Core/CompanionStore.swift` |
| `APP` | `Sources/PokeTokenBar/PokeTokenBarApp.swift` |
| `SV` | `Sources/PokeTokenBar/UI/SettingsView.swift` |
| `L10N` | `Sources/PokeTokenBar/Core/Localization.swift` |
| `SL` | `Sources/PokeTokenBar/UI/SpriteLoader.swift` |
| `CV` | `Sources/PokeTokenBar/UI/CompanionView.swift` |
| `T/` | `Tests/PokeTokenBarTests/` |

GNOME Shell sources are cited as `ui/<file>.js:N`, meaning
`resource:///org/gnome/shell/ui/<file>.js` in the installed 51.1. They were extracted read-only from
`/usr/lib64/gnome-shell/libshell-51.so`. Port files are relative to
this repo root.

---

## 1. Upstream behaviour

### 1.1 What it is, and what it is not

- An **opt-in** borderless, non-activating `NSPanel` that shows the representative Pokémon over the
  desktop (`FPP:5-11`, `FPP:798-815`). It is off by default (`US:968`).
- **It never moves on its own.** The settings comment says so explicitly: "fixed overlay — does not
  move by itself, position changes only by drag" (`US:178`). There is **no walking, no wandering, no
  idle animation and no sleep animation**. Moods have no animation of their own.
- Upstream has exactly four kinds of motion:
  1. The sprite's own GIF frames, decimated by the Animation quality floor (`FPP:1041-1044`, `US:138-152`).
  2. A two-frame "bob" squash while an agent is **working** (`FPP:1188-1211`).
  3. A 0.6 s run to the cursor on agent **done**, and the run back home (`FPP:449-501`).
  4. One-shot effects: a prayer wobble with 🙏 particles over an egg (`FPP:1046-1057`, `FPP:1104-1186`),
     and a pixel-art heart bubble after stroking (`FPP:1297-1366`).
- Mood shows up in exactly two places:
  - the **back sprite** while sulking (`CS:18`: `facesBack = active != nil && (alwaysFacesBack || displayState == .sulk)`);
  - the hover text suffix "Sulking for N hours/days" (`FPP:1095-1098`).
  - The pet never shows tired, sleep, working or focus states.

### 1.2 Appearance and size

- The sprite is `SpriteView(speciesID, size, animated, shiny, minFrameDelay: animationQuality.frameFloor, form, back: companion.facesBack, reportsVisibleTop: true)` (`FPP:1041-1044`).
  The subject is `companion.representativeSubject`: the pinned dex entry, else the companion being
  raised, else the egg.
- **Size** is one side of a square box in pt: default 96 (`US:970`), slider 48–384 in steps of 8 (`SV:377`).
- **Fit.** The GIF canvas is aspect-fit and **centred** in the box (`SL:363-375`, `CV:221-223`) with
  nearest-neighbour interpolation (`.interpolation(.none)`, `CV:221`). Upscaling may be fractional.
- **Egg.** Without a sprite it draws the 🥚 glyph at `size * 0.62` (`CV:244`). The floating pet passes
  `bob: false` (`FPP:1041`), so its static fallback does not bob.
- **Window.** The panel is transparent, without a shadow, at level `.floating`. Its collection
  behaviour is `[.canJoinAllSpaces, .fullScreenAuxiliary]` (`FPP:802-806`): it shows on every Space and
  **over fullscreen apps**. It never takes focus (`.nonactivatingPanel`, `becomesKeyOnlyIfNeeded`,
  `FPP:800-810`).
- **Panel size** (`FPP:516-522`):
  - with no bubble: `petSize × petSize`;
  - while a bubble (speech or hearts) shows: `max(petSize, 180) × (petSize + 72)`.
    `bubbleMinWidth = 180` (`FPP:42`) and `bubbleHeadroom = 72` (`FPP:40`).
  - The pet stays at the bottom, centred horizontally. `panelOrigin` and `petOrigin` convert with
    `xInset = max(0, (panel.w − pet)/2)` (`FPP:524-532`).

### 1.3 Position, dragging, persistence

- **Persisted value:** the **pet's** bottom-left origin, not the panel's, in UserDefaults
  `floatingPetOriginX/Y` as doubles (`FPP:32-33`, `FPP:838-842`). This keeps the sprite still when a
  bubble grows the panel (`FPP:8`).
- **Default home:** `(visible.maxX − size − 24, visible.minY + 24)` on `NSScreen.main`, the
  bottom-right corner with a 24 pt inset. The fallback is `(120, 120)` (`FPP:793-796`).
- **Off-screen recovery:** if the target frame intersects no screen's `visibleFrame`, the default home
  is used (`FPP:786-789`).
- **Drag:** custom `mouseDown`/`mouseDragged`/`mouseUp` (`FPP:947-977`).
  - `mouseDownCanMoveWindow` is false (`FPP:915`).
  - The window follows the pointer delta from mouse-down (`FPP:963-964`).
  - A drag is recognised once the squared distance is ≥ 16 pt² (`clickThresholdSquared`, `FPP:36`,
    `isClick` `FPP:325-329`).
  - On mouse-up with no drag and still within the threshold, it is a click (`FPP:973-976`).
- **Saving:** every `windowDidMove` goes through `resolveMove` (`FPP:67-70`, `FPP:817-843`).
  - At home, the origin is persisted.
  - During an agent run (`away` or `returning`), animation frames are **not** persisted.
  - A user drag during a run cancels the return, stops the in-flight animation, and becomes the new
    home (`FPP:826-836`).
- **Hide and show:** a hidden pet comes back at home, not where it last ran to (`FPP:685-687`).
  Hiding also releases the hosting view, which stops the GIF loop (`FPP:679-692`).

### 1.4 Interactions

| Input | Effect | Source |
|---|---|---|
| Left click | First `tracker.dismissBubble()` (answers waiting agents, clears done). Then `PetClickRouter`: an **egg** is prayed over, a **hatched** pet opens the popover. | `FPP:504-507`, `FPP:260-262`, `FPP:849-891` |
| Prayer (egg click) | `companion.pray()` (count +1, saved; `CS:1406-1412`). Spawns a 🙏 particle and a wobble. Refreshes the hover text. At every 30th prayer it refreshes usage, at most once per 30 s (`FPP:266-290`). | `FPP:872-887` |
| Right click, or Ctrl+left click | Context menu: **Open Token Bar** (always opens, even for an egg, and also dismisses agent bubbles), **Turn off floating pet** (sets `floatingPetEnabled = false`), **Show back view** (a checkmark; toggles `alwaysFacesBack`). | `FPP:947-952`, `FPP:979-1007`, `FPP:509-513`, `APP:115` |
| Hover | Callout panel 6 pt above the pet, centred: 11 pt text, 8 pt padding, 8 pt corner radius, 0.5 pt border, ignores the mouse. It also sets the native tooltip. | `FPP:715-771`, `FPP:631` |
| Stroke (petting) | Moving the pointer side to side over the pet with **no button held** shows the heart bubble for 1.8 s after the last qualifying stroke. | `FPP:938-945`, `FPP:642-657`, `FPP:187` |

**Hover text** (`FPP:1086-1101`, strings `L10N:462-482`):
- line 1 is `Today: <grouped tokens> tokens`, or `Today: N tokens (limit P%)` when a limit
  utilisation exists;
  - `P` is in the user's display mode: `100 − u` and "P% left" in remaining mode (`US:472-474`);
  - the utilisation is `highestLimitUtilization`, the max over every window of every provider with
    tokens today (`US:749-770`);
  - ` · Sulking for N hours` is appended below 48 h, ` · Sulking for N days` from 48 h;
- line 2 (optional) is the latest event line;
- line 3 (optional) is the prayer line `N prayers · <quip>` (`L10N:1393-1399`).

**Petting detector** (`FPP:1254-1295`):
- It is fed the **screen** x of each `mouseMoved` (`FPP:942`), because the panel re-centres when a
  bubble appears.
- `minStroke = max(10, petSize × 0.15)` (`FPP:632`).
- A stroke counts once it travels `minStroke` in one direction. A reversal of at least `minStroke`
  counts as one reversal.
- It fires when ≥ 3 reversals fall within 1.2 s, and again on every further reversal.
- A gap of more than 1.2 s between samples resets it.
- It is reset on mouse exit, mouse down and drag (`FPP:933-936`, `FPP:948`, `FPP:959`).
- Hearts show only when no speech bubble is up (`FPP:190-193`).
- The native tooltip is cleared while hearts show (`FPP:203-205`).
- The hover callout is hidden while hearts show, then comes back if the pointer is still there
  (`FPP:646`, `FPP:655`).

**Heart art:** a 17×16 pixel grid, 2 pt per pixel, with colours O `#202020`, W white, R `#E0303C`,
D `#A8202C`, H `#FF9AA2` (`FPP:1306-1342`). It has the same colours in light and dark mode. Its tail tip
is anchored 3 pt inside the sprite's **visible top** (`FPP:1061-1067`, `SL:379-410`):
- the visible top is the first row whose alpha is above 16, within the central band 0.35–0.65 of the
  width, falling back to the full width;
- across frames, the **median** frame is used;
- the offset is rounded to whole points.

### 1.5 Speech bubbles

There is one bubble at a time, with this priority (`FPP:97-112`, `FPP:189-199`):

1. **Limit alert**, from `US.currentBubbleAlert`.
2. **Agent done** (the latest one).
3. **Agent waiting** (the latest one, plus a count of the others).
4. **Hearts**, only when 1–3 are empty.
5. The **hover or event callout** shows only when no bubble or hearts are up (`FPP:718`).

**Limit alerts** (`US:2105-2179`, `US:2263-2274`):
- They come from the same edge-triggered evaluation as notifications, but are gated independently by
  `floatingPetEnabled && floatingPetBubbleAlerts` (`US:2176`). They do **not** depend on
  `limitNotifications`.
- From one evaluation's alerts, the bubble shows the max: **critical first, then the higher
  utilisation** (`US:2153-2158`).
- It is set with a **6 s TTL** (`US:2268-2272`, `shouldDismissBubble` `US:2161-2163`). A newer alert
  replaces it and restarts the TTL (the `currentBubbleDate == now` guard).
- Title: `Limit imminent` (critical, drawn red) or `Limit warning` (`FPP:123`, `L10N:1788-1789`).
- Body: `<window> at <percent>`, with ` · <account>` dropped if it would not fit two lines
  (`FPP:537-541`, `US:2114-2118`).

**Agent bubble** (`FPP:126-133`, `FPP:143-164`, `L10N:497-515`):
- Title: `<label> · waiting` or `<label> · done`, plus ` +N` for other waiting agents.
  - Unlabeled agents use `Your agent needs you` or `Done!`.
  - The label (or the unlabeled title) is cut by binary search with `…` to fit one line, preferring a
    word boundary.
- Body: the message, or `It's waiting for your input.` / `Your agent finished its task.`.
  - It is cut with `…` (binary search) to fit **2 lines** (`FPP:166-184`).

**Chrome** (`FPP:1213-1250`):
- a fixed 164 pt text column (`180 − 2×8`);
- title 11 pt bold, red if critical; body 10 pt, secondary colour;
- 6 pt vertical padding, corner radius 12, a soft shadow, and a 10 pt-wide tail at the bottom-left;
- spring-in transition (`FPP:1079-1082`).

**Event callout** ("latestEvent") (`FPP:254-275`, `FPP:659-677`, `CS:1538-1546`):
- Every hatch, evolution, graduation, Ditto reveal and Dex badge pops the hover callout **once**,
  for 6 s, with the event body as line 2. Candy grants are excluded (`showsOnPet: false`, `CS:1521-1522`).
  Call sites: `CS:524`, `CS:1054`, `CS:1141`, `CS:1814`, `CS:1870`.
- It pops only if the pet is visible and no bubble or hearts are up. Otherwise it is dropped, not
  queued (`FPP:272-275`, `FPP:664`).
- Events from before the controller existed are ignored (`FPP:318`).

### 1.6 Agent activity

**Transport (macOS-only).** External tools open `poketokenbar://agent?state=<working|waiting|done|idle>[&label=…][&message=…]`
(`AA:12`).
- The app installs its own Apple Event `kAEGetURL` handler, so a URL never opens a window or
  activates the app (`APP:62-73`).
- An event that arrives before launch finishes is dropped (`APP:72`).
- The README hook script (`README.md:221-245`) wires Claude Code hooks:
  - `UserPromptSubmit` and `PostToolUse` send working;
  - `PreToolUse[AskUserQuestion]` and `Notification[permission_prompt|elicitation_dialog]` send waiting;
  - `Stop` sends done;
  - each call runs `open -g <url>`, guarded by `pgrep`.
- Nothing is detected passively. AgentActivity **only** reacts to URLs.

**Parsing** (`AA:30-45`):
- Scheme and host are case-insensitive. A missing host is rejected.
- `state` is lowercased, and the first `state` item wins.
- An unknown state gives `nil`, which means ignored.
- `message` is kept only for `waiting` and `done`.

**Sanitizing** (`AA:47-74`), for both message and label:
- control characters become spaces;
- format characters are dropped, except U+200D (ZWJ);
- whitespace runs collapse to one space, and the text is trimmed;
- empty text becomes nil;
- more than `4 × max` Unicode scalars are cut to `4 × max − 1`, trimmed, plus `…`;
- more than `max` **Characters** (grapheme clusters) are cut to `max − 1`, trimmed, plus `…`;
- the message max is 80 (`AA:18`, scalars 320, `AA:21`); the label max is 24 (`AA:23`).

**Tracker** (`AA:95-188`). It keeps one entry per label, with `""` for the unlabeled agent.
- `idle` removes the entry.
- `working` has a deadline of now + 600 s; `done`, now + 6 s; `waiting` has no deadline.
- Each `apply` first expires stale entries.
- With more than 8 labels, the one with the oldest sequence is evicted, but only when a new key arrives.
- `summary`:
  - `working` = any entry is working;
  - `done` = the max-sequence done;
  - `waiting` = the max-sequence waiting, with `otherWaitingCount` = the others.
- `deadline` = the earliest entry deadline.
- `dismissBubble()` keeps only working entries.

**Controller wiring** (`FPP:389-501`):
- Events are dropped unless `floatingPetEnabled && floatingPetAgentActivity` (`FPP:385`, `FPP:393`).
- Turning either off resets the tracker, the timer and the run phase (`FPP:439-447`, `FPP:593`).
- One `Timer` is armed at `tracker.deadline`. A fire before the deadline re-arms instead of being lost
  (`FPP:415-431`).
- **working** gives the bob, only if `motionAllowed = animated && !reduceMotion`
  (`FPP:86`, `FPP:93-95`). `animated` is `!LowPowerMode` (`FPP:376-383`).
  - The bob toggles a `scale(x: 1.03, y: 0.94, anchor: .bottom)` squash every 0.5 s, with no
    interpolation (`FPP:1193-1210`).
- **done** runs the panel to the cursor (`FPP:449-466`), using `runPanelOrigin` (`FPP:75-83`):
  - it goes below-right of the cursor with a 16 pt gap;
  - it flips left or up at screen edges;
  - it clamps to the cursor's screen `visibleFrame`, and the bottom-left (the pet) wins when the panel
    is larger than the screen;
  - the animation is 0.6 s ease-in-out (`FPP:52`, `FPP:488-501`).
  - A second done while `away` does not run again (`FPP:89-91`).
- When no `done` remains, the pet returns home, animated if motion is allowed (`FPP:468-486`).
- With motion off (Low Power or Reduce Motion), bubbles still show and nothing moves (`README.md:218`).

### 1.7 Settings and other surfaces

| Setting | Key (UserDefaults) | Default | UI |
|---|---|---|---|
| Show floating pet | `floatingPetEnabled` | false (`US:968`) | `SV:353-361`; also the status-item menu (`StatusItemMenu.swift:31-32`), the popover footer eye button (`PopoverView.swift:1133-1141`), and the pet's own "Turn off" item |
| Show back view | `alwaysFacesBack` | false (`US:969`) | `SV:363-372`; also the status-item and pet menus. It applies to every surface. |
| Size | `floatingPetSize` | 96 (`US:970`) | slider 48–384, step 8, "N px" label (`SV:375-381`); shown only when enabled (`SV:373`) |
| Show notifications as bubbles | `floatingPetBubbleAlerts` | true (`US:971`) | `SV:383` |
| React to agent activity | `floatingPetAgentActivity` | true (`US:972`) | `SV:385-393`, with a hint pointing to the README |
| Origin | `floatingPetOriginX/Y` | unset | drag only |

Animation quality (`animationQuality`) is shared with the menu bar. The pet uses its floor too
(`FPP:1043`).

### 1.8 Power, display sleep, multi-monitor

- **Display sleep:** `screensDidSleep` calls `setDisplayAwake(false)`, which hides the pet and frees the
  hosting tree (`APP:683-690`, `APP:710`, `FPP:331-334`).
- **Low Power Mode:** rebuilds the host as static (no GIF) and disables motion (`FPP:622-629`, `FPP:376-383`).
- **Reduce Motion:** disables the bob and the run; bubbles remain (`FPP:368-383`).
- **Multi-monitor:**
  - The pet lives in global coordinates and can sit on any screen.
  - The run goes to the screen under the cursor (`FPP:450-452`, test `testRunStaysOnTheCursorScreen`).
  - Off-screen recovery uses any screen's visible frame (`FPP:786`).
  - It shows on all Spaces.

---

## 2. GNOME approach

### 2.1 Facts from the Shell 51 sources

**Stacking order of `Main.uiGroup` children, bottom to top:**
1. `global.window_group` (`ui/layout.js:224`);
2. the `addChrome` actors, each inserted just below `top_window_group` (`ui/layout.js:878-883`), so in
   insertion order:
   - `overviewGroup` (`:262`);
   - `screenShieldGroup` (`:274`);
   - `panelBox` (`:280-283`, `trackFullscreen: true`);
   - the message tray (`ui/messageTray.js:785`);
   - then any extension `addChrome` actor;
3. `top_window_group` (`:251`), which holds app popup surfaces;
4. `modalDialogGroup` (`:287-291`);
5. later `uiGroup.add_child` actors, including every `PanelMenu` menu (`ui/panelMenu.js:142`);
6. `addTopChrome` actors such as `screenshotUIGroup` (`:309`). `addTopChrome` appends to the very top
   (`:890-893`).

**Input region.**
- Shell 51 has **no `affectsInputRegion`**. The chrome params are only `trackFullscreen` and
  `affectsStruts` (`ui/layout.js:183-186`).
- `_updateRegions` only builds struts (`ui/layout.js:1054-1131`).
- On Wayland the compositor picks the topmost **reactive** actor. A non-reactive actor is click-through.
- A reactive actor's hit area is its whole allocation rectangle (default pick), not its alpha.
- This is mutter behaviour I did not read in source. **Verify it in the sandboxed shell.**

**`trackFullscreen`.**
- `_updateActorVisibility` **writes `actor.visible`** = `!(window_group.visible && monitor.inFullscreen)`
  (`ui/layout.js:982-990`).
- It runs on overview show and hide, fullscreen changes, session updates and monitor changes
  (`ui/layout.js:992-999`, `:338-339`, `:364-376`, `:632-641`).
- During the overview, `window_group` is hidden, so a tracked actor is forced **visible**. This
  overrides any hide of our own.
- The monitor is looked up once per update by the actor's current position (`ui/layout.js:986`). A
  drag to another monitor is not re-evaluated until the next update.

**Lock.**
- Extensions without `session-modes` default to `['user']` (`ui/extensionSystem.js:396`).
- They are disabled when the mode is no longer supported (`ui/extensionSystem.js:170-183`).
- `unlock-dialog` is a separate mode (`ui/sessionMode.js:67-68`).
- So the pet is **destroyed on every lock** and rebuilt on unlock, together with the helper (port
  `extension/extension.js:1-11`).

**Overview signals.**
- The overview emits `showing`, `shown`, `hiding` and `hidden` (`ui/overview.js:87-104`).
- Their order relative to `layoutManager.showOverview()` differs by path: `:371-373` emits `showing`
  before `showOverview()`, while `:499` calls `showOverview()` first. Do not rely on the order.

**Other APIs.**
- Work area: `Main.layoutManager.getWorkAreaForMonitor(i)` (`ui/layout.js:1001-1007`, from workspace 0).
- Monitor at a point: `findMonitorForPoint(x, y)` (`:1037`).
- `monitors-changed` (`:641`).
- Pointer: `global.get_pointer()` (`ui/messageTray.js:1004`).
- Display power-save: `global.backend.get_monitor_manager()` emits `power-save-mode-changed`
  (`ui/unlockDialog.js:395-398`).
- `Clutter.Actor.prototype.ease` scales durations through `adjustAnimationTime`
  (`ui/environment.js:236`, `:351`). That returns **0 when `enable-animations` is off**
  (`misc/animationUtils.js:22-32`). This is GNOME's equivalent of Reduce Motion.
  - It is **not** honoured for free. A zero-duration `ease()` would teleport the pet to the cursor,
    while upstream stays home and shows only the bubble (`FPP:89-91`, `FPP:474-477`).
  - So compute `motionAllowed = enable_animations && !power_saver_enabled` explicitly, and use it to
    gate `runsToCursor`, `shouldBob` and the animated return.
- Low-power equivalent: `Gio.PowerProfileMonitor.dup_default().power_saver_enabled`. It exists in this
  GJS (probed).

**Gestures (Shell 51).**
- `Clutter.PanGesture` with `begin_threshold`, and the signals `recognize`, `pan-update` and `end`
  (`ui/slider.js:52-56`, `ui/screenshot.js:292-299`); `get_centroid_abs()` (`ui/screenshot.js:640`).
- `Clutter.ClickGesture` with `set_required_button`. The port already uses it (`extension/lib/indicator.js:45-52`).
- A `ClickGesture` and a drag-start gesture coexist on one actor **without** explicit relations in
  `ui/windowPreview.js:126-141`. Only a long-press gets `can_not_cancel`. This suggests the default
  arbitration (the drag recognises past its threshold and cancels the click) is enough.
- `dnd.js` `makeDraggable` (`ui/dnd.js:87-118`, `:641`) is for **drag-and-drop to drop targets**. It
  runs `Main.pushModal`, builds a drag actor, and snaps back or reverts. It is not suited to moving the
  actor itself. Use `PanGesture`.

### 2.2 Options compared

| | (a) `addChrome` actor (re-stacked) | (a′) `addTopChrome` actor | (b) separate GTK4 window | (c) bare `Main.uiGroup` child |
|---|---|---|---|---|
| Above normal and "always on top" windows | yes (`window_group` is below all chrome) | yes | **no**: no layer-shell, see below | yes, appended at the top |
| Below app popups, modal dialogs, shell menus and our popover | yes (`addChrome` sits under `top_window_group`) | **no**: above everything, including the screenshot UI and polkit | n/a | **no**: above modal dialogs and earlier menus |
| Input pass-through outside the sprite | non-reactive children; the sprite box is a rectangle | same | `gdk_surface_set_input_region` works | same as (a) |
| Fullscreen hiding | `trackFullscreen` (with caveats, §2.1) or our own `in-fullscreen-changed` sync | same | it is a window: covered by the fullscreen app | manual |
| Overview, lock | must hide explicitly in the overview; destroyed on lock | same | appears **in the overview, Alt+Tab and the dock** unless an extension hides it | same as (a) |
| Positioning | free stage coordinates | free | **impossible from the client on Wayland** (no global coordinates, no keep-above in GTK4) | free |
| Process and CPU | in-shell, reuses `SpriteAnimator` and helper sprites | same | second process, its own GL context, duplicates sprite loading | in-shell |

**(b) does not work.**
- `libmutter-51.so.0` exports no `zwlr_layer_shell` interface. `strings` shows 0 hits for
  `layer_shell` or `zwlr_layer`. The xdg/wp protocols present include `xdg_wm_base`,
  `xdg_toplevel_drag_v1` and `xdg_session_manager_v1`, but none of them give a client stacking or
  absolute position.
- GTK4 removed `gtk_window_move` and keep-above.
- The only way to pin a client window is **compositor-side**: an extension finds its `Meta.Window` and
  calls `make_above()` and `move_frame()`, and must also hide it from the overview and Alt+Tab
  (the DING pattern).
- That needs the extension anyway, adds a process and IPC, and still loses to (a).

**(c) has no advantage over (a)** in Shell 51. Tracking now only buys struts and `trackFullscreen`,
and a raw `uiGroup.add_child` lands above modal dialogs.

**Recommendation: (a), an `addChrome` actor re-stacked directly above `global.window_group`.**

```
Main.layoutManager.addChrome(this._root);               // tracked, trackFullscreen: false (see below)
Main.layoutManager.uiGroup.set_child_above_sibling(this._root, global.window_group);
```

- **Z-order.** Above every app window, including "always on top" ones, which matches upstream
  `.floating`. Below the overview, screen shield, top bar, notification banners, app popup menus,
  modal dialogs, and our own popover, menus and tooltip.
  - Plain `addChrome` would instead sit above `panelBox` and the message tray (§2.1), so the pet could
    cover the top bar and banners.
  - `_trackActor` does not care about sibling order (`ui/layout.js:952-967`).
  - The only shell code that inserts directly above `window_group` is the workspace-switch animation
    (`Main.uiGroup.insert_child_above(group, global.window_group)`, `ui/workspaceAnimation.js:420`).
    Its sliding groups land **below** the pet, so the pet stays put during a switch, as on all Spaces
    upstream.
  - **Unverified:** whether a later `addChrome` by another extension re-orders us. It would only push
    us lower, which is harmless.
- **Visibility ownership.** Do **not** pass `trackFullscreen` on the actor we hide ourselves.
  - Either use a two-level tree: an outer `_root` added with `trackFullscreen: true` (LayoutManager
    owns its `visible`) and an inner `_pet` (we own `visible`);
  - or skip the flag and compute everything in one `sync()`. **Recommended: one `sync()`**, because
    `trackFullscreen` forces the actor visible in the overview and goes stale after a cross-monitor drag.

  ```
  visible = enabled && !overviewVisible && !monitorOfPet.inFullscreen && displayOn
  ```

  - Inputs:
    - `global.display` `in-fullscreen-changed` (`ui/layout.js:338-339`);
    - `Main.overview` `showing` and `hidden`, plus `Main.overview.visible` at enable time, because the
      shell can start in the overview;
    - `Main.layoutManager` `monitors-changed`;
    - the monitor manager's `power-save-mode-changed`;
    - the pet's drag end and run end (the monitor may have changed);
    - the GSettings keys.
- **Fullscreen.** Hide over fullscreen. This is a **deliberate deviation**: upstream shows over
  fullscreen apps (`.fullScreenAuxiliary`, `FPP:806`).
  - Reasons: games and video.
  - Possibly also direct scanout, since a shell actor painted over a fullscreen surface likely
    prevents it (**unverified**).
  - It could become a setting later.
- **Overview.** Hide it. The overview has its own window previews, and upstream has no overview.
- **Lock screen.** Nothing to do: the extension is disabled and the pet destroyed (§2.1). The pet is
  rebuilt at the saved home on unlock, which matches upstream's "a hidden pet comes back at home"
  (`FPP:685-687`).
- **Input.**
  - The root and the bubble or hearts layer are `reactive: false`. Only the sprite actor is
    `reactive: true, track_hover: true`. Clicks on the headroom and on transparent margins pass
    through to windows.
  - The sprite's hit box is the **fitted image rectangle**, not the square box. A later refinement could
    override `vfunc_pick` with the opaque bounding box.
- **Drag.**
  - Add a `Clutter.PanGesture` with `begin_threshold = 4` (the upstream 16 pt², `FPP:36`).
  - On `recognize`, store the root's start position and `get_centroid_abs()`. On `pan-update`, set the
    position by the delta. On `end`, clamp, persist and `sync()`.
  - Primary click: a `Clutter.ClickGesture` with the primary button; it cancels when the pan recognises.
  - Secondary click: a separate `ClickGesture` with `recognize_on_press` (the port's pattern,
    `extension/lib/indicator.js:46-52`).
  - Drop Ctrl+click: it is not a GNOME convention.
- **Petting.** `vfunc_motion_event` on the sprite feeds `PettingDetector` with **stage x**
  (`event.get_coords()`) and `event.get_time()/1000` s.
  - Upstream's rule is "no button held" (`FPP:938`). Enforce it explicitly: ignore samples whose
    `event.get_state()` has any `BUTTON*_MASK`, or while the pan gesture is active.
  - **Unverified:** whether motion events still reach the actor while a gesture holds the sequence.
  - Reset the detector on `leave-event`, button press and pan `recognize`.
- **Work-area clamping.** Clamp to `getWorkAreaForMonitor(findMonitorForPoint(centre))`, which excludes
  the top-bar strut.
  - Clamp on drag end, on enable, on `monitors-changed` and on `workareas-changed`.
  - Upstream does not clamp a drag beyond what AppKit does. Here clamping stops the pet from hiding
    under the panel, which it would otherwise draw over.
- **Multi-monitor.** Stage coordinates are global, so the pet can sit on any monitor. Chrome is on
  every workspace, which matches `.canJoinAllSpaces`.
  - The run uses the work area of the monitor under `global.get_pointer()`.
  - Recovery: if the pet rectangle intersects no monitor's work area, use the default home on the
    primary monitor.
- **CPU budget.**
  - Reuse `SpriteAnimator` (`extension/lib/spriteAnimator.js`). Its timer stops whenever the actor is
    unmapped (`:399-404`), so every hide path above stops it with no extra code.
  - Frame floor: `floorForQuality(animation-quality)`, the same key as the top bar (`FPP:1043`).
  - Power saver: `setPaused(true)` shows a static frame and motion is off (`FPP:622-629`).
  - Bob: one `GLib.timeout_add` at 500 ms toggles `scale_x`/`scale_y` on a wrapper with
    `pivot_point (0.5, 1)`, only while working and motion is allowed. That is 2 redraws a second.
  - The run uses `ease()`, with no timer of ours.
  - With the popover open, the top-bar sprite pauses (`extension/lib/indicator.js:177`). The pet keeps
    animating, as upstream does.
  - The real-session CPU gate for one animated sprite is still open (port `tasks/todo.md` "Still open").
    The pet doubles the steady-state cost, so measure both together.

### 2.3 Sprite source and fitting

- Use `state.companion.menuSubject.sprite.manifestPath`. It already reflects the representative,
  shiny, form and **`back`** (sulk or `alwaysFacesBack`), protocol §5.4. The pet needs no extra helper
  sprite work.
  - Handle the `onLoadFailed` retry the same way as the indicator (`extension/lib/indicator.js:274-291`).
- `SpriteAnimator` today only upscales by **integers** and **bottom-aligns**
  (`extension/lib/spriteAnimator.js:132`, `:337-360`). At 96 px a typical 40–80 px Gen-V canvas
  stays at 1× (or 2× if it is ≤ 48 px), so the 48–384 slider would barely change anything.
  Upstream aspect-fits fractionally and centres (`SL:363-375`, `CV:221-223`).
- **Proposal:** add a `fit: 'aspect'` mode to `SpriteAnimator` (fractional scale, NEAREST mag filter,
  centred) and use it only for the pet.
  - Risk: uneven pixel columns at non-integer scales (the reason for the current rule, `:347`).
    Accept this for parity, or snap to integer scales when the scale is ≥ 2 (open question 6).

---

## 3. Feature cut for this milestone

| Upstream behaviour | Decision | Reason |
|---|---|---|
| Pet overlay, size 48–384, the representative sprite, back sprite on sulk or setting | **Keep** | Core of the feature. The sprite path is already in `state`. |
| Drag, persisted home, default bottom-right, off-screen recovery | **Keep (adapted)** | GSettings instead of UserDefaults. Clamp to the work area (§2.2). Save on drag **end**, not every move, since GSettings writes go to dconf on disk. |
| Hover callout (today's tokens, limit %, sulk suffix) | **Keep** | Reuse `lib/tooltip.js` `showAt()` or a pet-owned label. It needs the sulk idle duration from the helper (§4). |
| Event callout (`latestEvent`, 6 s, once, not queued) | **Keep** | The event body comes from existing helper events through `V.notificationText(event).body` (`extension/lib/viewModel.js:339-381`). It covers `hatch`, `evolve`, `graduate` and `badge`, not `candyGranted` (`CS:1521-1522`). No protocol change is needed. |
| Limit-alert speech bubble (6 s TTL, critical first, then utilisation) | **Keep (adapted)** | It is fed from `limitAlert` events, gated by `floating-pet-bubble-alerts`, **independent of `limit-notifications`** (`US:2176`). The port emits alerts one per event, so batch them (§4.3). There is no account suffix: the port has a single account (protocol §5.3 `account` is always null). |
| Agent bubbles, multi-label tracker, bob while working, click to dismiss | **Keep** | Pure logic with a complete Swift test oracle (§5). The transport changes (§4.1). |
| Run to the cursor on done and back home | **Keep** | `ease()` for 600 ms with `EASE_IN_OUT_QUAD`, plus the run-generation guard. It is skipped when `enable-animations` is off or power saver is on. |
| Petting and heart bubble | **Keep** | It is cheap: a pure detector and static pixel art (upload the 17×16 grid once as an `St.ImageContent` scaled ×2 NEAREST). Anchoring to the visible top needs `visibleTopFraction` (§4.2). |
| Context menu: Open Token Bar, Turn off floating pet, Show back view | **Keep** | A `PopupMenu` sourced on the sprite, added to `Main.uiGroup` with its own `PopupMenuManager` (the port's pattern, `extension/lib/indicator.js:126-156`). "Open" calls `indicator.menu.open()`. |
| Ctrl+click opens the menu | **Drop** | A macOS one-button-mouse convention. |
| Click on an egg prays (count, 🙏 particles, wobble, refresh every 30 prayers, prayer line in the hover) | **Ported in Milestone 3: an egg click prays; the context menu *Open Token Bar* opens the popover** | Superseded the M2 adaptation (egg click opened the popover). The helper `pray` command, the prayer hatch text and the quips ship in 0.3.0 (`docs/spec/prayers-ditto.md`). |
| Display sleep: hide the pet | **Adapt** | Locking already destroys it. For blanking without a lock, hide on monitor-manager `power-save-mode-changed`. |
| Low Power Mode: static and no motion | **Adapt** | `Gio.PowerProfileMonitor.power_saver_enabled`. |
| Reduce Motion: no bob or run | **Adapt** | `St.Settings.get().enable_animations` (`ease()` already honours it). |
| Shows over fullscreen apps | **Deviate: hide over fullscreen** | §2.2. |
| Shows on all Spaces | **Keep (free)** | Chrome is not per-workspace. |
| `poketokenbar://` URL scheme | **Replace** | §4.1. Optionally keep URL compatibility through `OpenUrl`. |
| Native AppKit tooltip duplicating the callout | **Drop** | One callout is enough in St. The hearts rule "clear the tooltip" becomes "hide the callout". |
| Spring transitions on bubbles | **Simplify** | Use `ease()` on scale and opacity for 200 ms, honouring `enable-animations`. |
| Status-item "Show floating pet" and popover footer eye toggle | **Keep** | ui.md lists both as LATER (`docs/spec/ui.md:129`, `:283`). They belong with this feature. |

---

## 4. Protocol, GSettings, prefs

### 4.1 Agent activity transport (Linux)

**Constraints.**
- The helper's stdio belongs to the extension, and the helper holds the state-dir flock (protocol §2.1).
  A hook process therefore cannot reach it without a new rendezvous.
- The helper is stdlib-only Go, with no grapheme segmentation. Upstream's caps count Characters. GJS
  has `Intl.Segmenter` (probed: `'family 👨‍👩‍👧'` gives 8 graphemes).
- The tracker needs timers, click dismissal and the run animation. Upstream keeps it in memory, in the UI.

So **parsing, sanitizing and the tracker live in the extension (JS)**, and the transport ends there.

**Recommended: a session-bus D-Bus object exported by the extension.**
- Own a well-known name with `Gio.bus_own_name_on_connection(Gio.DBus.session, …)` in `enable()` and
  release it in `disable()`. The name is to be decided (open question 3), for example
  `io.github.poketokenbar.Agent` at path `/io/github/poketokenbar/Agent`.
- Interface `io.github.poketokenbar.Agent1`:
  - `Report(s state, s label, s message)`. Empty label or message means none. No URL encoding is
    needed in hooks.
  - `OpenUrl(s url)`. It takes the upstream `poketokenbar://agent?…` grammar, so the Swift parse
    tests port verbatim.
  - Both are fire-and-forget. They reply immediately and drop silently when the pet or the setting is
    off (`FPP:393`).
- Hook, replacing `open -g` (`README.md:223-231`):

  ```sh
  busctl --user call io.github.poketokenbar.Agent /io/github/poketokenbar/Agent \
    io.github.poketokenbar.Agent1 Report sss "$1" "${CLAUDE_PROJECT_DIR##*/}" "" >/dev/null 2>&1 || true
  ```

  - Use **`busctl`, not `gdbus`**. `gdbus call` parses each argument as GVariant text when it can.
    Checked: `NameHasOwner "'org.freedesktop.DBus'"` returned `true`, so the quotes were stripped. A
    label like `'x'` would silently lose its quotes.
  - `busctl … s "'org.freedesktop.DBus'"` passes the string literally (`false`). An empty string is
    fine.
  - There is no service file, so there is no D-Bus activation. When the extension is off, busctl exits
    1 at once ("The name is not activatable", checked), and `|| true` swallows it. This replaces the
    `pgrep` guard.
  - Claude Code hooks are platform-independent, so the `settings.json` block from `README.md:235-245`
    carries over unchanged.
- Optional: an `x-scheme-handler/poketokenbar` `.desktop` file in the RPM whose `Exec` runs `busctl …
  OpenUrl s %u`, so `xdg-open 'poketokenbar://agent?…'` works for third-party tools. That costs one
  process spawn per event.
- Trust model: any local process can call it, the same as any app opening a URL upstream. The
  sanitizer and caps are the defence (`AA:16-23`).
- **Lock-time loss.** Events sent while the screen is locked are lost, because the extension is
  disabled, and the in-memory tracker is reset on every unlock.
  - Upstream keeps the tracker in memory too, but macOS does not tear the app down on lock.
  - Mitigation, if the open question decides it is needed: persist the tracker's entries (with
    absolute deadlines) to `$XDG_RUNTIME_DIR/poketokenbar/agents.json` on change, and reload it on
    `enable()`. `XDG_RUNTIME_DIR` is tmpfs, survives the lock and is cleared at logout.

**Rejected or deferred alternatives.**
- **Helper-side unix socket plus `ptb-helper agent …` CLI.** It puts the tracker in Go, which lacks
  grapheme segmentation, and the helper also restarts on lock. The tracker state would have to reach
  the extension through `state`, adding protocol churn for no gain.
- **Passive detection from `~/.claude/projects/**/*.jsonl` mtime in the Go helper.**
  - It can infer **working** at best: a session file grew within the last N seconds. The label would
    be the project directory, from the path (`docs/spec/usage.md:97`).
  - The documented JSONL inventory (`docs/spec/usage.md:97-139`) has no field that reliably signals
    *waiting for permission* or *turn finished*. Do not rely on unlisted fields.
  - The helper scans every 120 s by default (protocol §9.1). A 2–5 s mtime poll of recently active
    files would be new work.
  - **Deferred**, as an optional "zero-config working bob" later. Hooks remain the way to get waiting
    and done.

### 4.2 Helper and state additions (protocol 1, additive per §2.7)

| Addition | Where | Why |
|---|---|---|
| `companion.sulkIdleSeconds` (int, 0 unless `mood == "sulk"`) | §5.4 | The hover suffix "Sulking for N hours/days" (`FPP:1095-1098`). The helper already computes `idleFor` (`helper/internal/daemon/build.go:200-222`) and sulk (≥ 3 h). The extension cannot derive it. |
| `visibleTopFraction` (number 0–1; median over frames of the first opaque row in the central 0.35–0.65 band, alpha > 16, falling back to the full width) | sprite `manifest.json` (§8.2); for the egg `.png`, a sidecar or a fixed value | Anchors the hearts on the visible sprite (`SL:379-449`). The helper decodes GIFs already, so the scan costs nothing at runtime, and it gets Go tests ported from `T/FloatingPetPettingTests.swift:180-235`. |
| *(none)* agent events | — | Agent events stay extension-side (§4.1). |
| *(none)* limit bubble | — | Fed from the existing `limitAlert` events (§4.3). |
| *(none)* event callout | — | Fed from the existing `hatch`, `evolve`, `graduate` and `badge` events. |
| *(deferred)* `pray` command | §4 | Only if prayers enter this milestone (open question 1). |

### 4.3 Limit bubble from `limitAlert` events

- **Tap events before the Notifier's gate.** Today `extension.js` routes `onEvent` only to
  `this._notifier.handle()` (`extension/extension.js:50-55`, the `onEvent` line is `:53`). Route events to the pet as well, so
  `limit-notifications = false` does not mute the bubble (`US:2173-2178`).
- **Batching.** Upstream picks one alert per evaluation (`US:2153-2158`).
  - The helper collects every `limitAlert` of one limits pass into one slice
    (`helper/internal/daemon/daemon.go:763-792`).
  - `sendEvents` writes them back to back, after flushing the state that reflects them
    (`helper/internal/daemon/daemon.go:613-621`, protocol §2.4 "Ordering").
  - **Rule:** collect `limitAlert` events for 250 ms after the first one, then pick the max (critical
    first, then utilisation), replace the current bubble and restart the 6 s TTL.
  - A pure `pickBubbleAlert(alerts)` ports the Swift rule exactly.
- Bubble text: reuse `V.notificationText(event)` for the title and body (`extension/lib/viewModel.js:370-376`).

### 4.4 GSettings keys (UI-only, not forwarded)

| Key | Type | Default | Range | Upstream |
|---|---|---|---|---|
| `floating-pet-enabled` | `b` | `false` | | `US:968` |
| `floating-pet-size` | `i` | `96` | 48–384 (step 8 enforced by prefs) | `US:970`, `SV:377` |
| `floating-pet-bubble-alerts` | `b` | `true` | | `US:971` |
| `floating-pet-agent-activity` | `b` | `true` | | `US:972` |
| `floating-pet-origin` | `m(dd)` | `nothing` | pet bottom-left in logical stage px | `FPP:32-33` |

- `always-faces-back` already exists and stays forwarded (port `extension/lib/settingsForward.js:8-11`).
- `floating-pet-origin` stores the **pet's** bottom-left, as upstream does, so a size change grows the
  sprite up and right.
  - Checked: `glib-compile-schemas --strict --dry-run` accepts `type="m(dd)"` with
    `<default>nothing</default>` in a one-key test schema.
- Write it on drag end only.

### 4.5 Prefs rows (Adw)

Add a new "Floating pet" `Adw.PreferencesGroup`, built with the existing `_switchRow` helper
(`extension/prefs.js:69`):

1. SwitchRow `floating-pet-enabled`, "Show floating pet", subtitle "Your Pokémon floats over the
   screen — drag to reposition" (`L10N:434-444`).
2. SwitchRow `always-faces-back`, "Show the back view". **Move** it from the Top bar group
   (`extension/prefs.js:142-143`), where it sits today. Upstream lists it under Floating pet
   (`SV:363-372`) and its hint says it applies to every surface (`L10N:1366`).
3. A size row with a scale: 48–384, step 8, and an "N px" label (`SV:375-381`).
4. SwitchRow `floating-pet-bubble-alerts`, "Show notifications as bubbles" (`L10N:452-453`).
5. SwitchRow `floating-pet-agent-activity`, "React to agent activity", subtitle "Coding agents drive
   it over D-Bus — see the README". This changes the upstream hint `L10N:487-495`.

Rows 3–5 are visible only while `floating-pet-enabled` is true, via
`settings.bind(…, 'visible', GET)` as for the limit colour row (`extension/prefs.js:139`). This
mirrors `SV:373`.

Also add:
- an indicator right-click menu item "Show/Hide floating pet" (ui.md §1.5 item 6);
- a popover footer eye toggle (`docs/spec/ui.md:283`).

---

## 5. Work list and tests

### 5.1 Module layout

Keep the logic in pure modules, as `extension/lib/prefsModel.js:1-6` does, so it runs under plain
`gjs -m` (`Makefile` `test`).

| # | Item | Lang | Estimate |
|---|---|---|---|
| 1 | `lib/pet/agentActivity.js` (pure): `eventFromUrl`, `eventFromReport`, `sanitized` (grapheme-aware with `Intl.Segmenter`; `\p{Cc}` becomes a space; `\p{Cf}` is dropped except U+200D; `\s+` collapses), `AgentActivityTracker` with an injected `now` | JS | 0.75 d |
| 2 | `lib/pet/petModel.js` (pure): `resolveMove`, `runPanelOrigin` (y-down, §5.3), `allowsMotion`, `runsToCursor`, `shouldBob`, `bubble`, `isShowingBubble`, `heartsShowing`, `shouldPopEvent`, `panelSize`, `panelOrigin`/`petOrigin` (y-down), `defaultHome`, `recoverHome`, `isClick`, `PettingDetector`, `hoverText`, `pickBubbleAlert`, `shouldDismissBubble`, `clickAction`, `visibleTopInset` and `bubbleTopOffset` (Swift rounding) | JS | 1 d |
| 3 | `lib/pet/petController.js` (pure, no `gi://St`/`Clutter`): the state machine of `FPP:389-513`, `:592-692` and `:824-843`, with injected callbacks (`openPopover`, `animateTo`, `persistHome`, timer factory, clock). It hosts the wiring-test port. | JS | 1 d |
| 4 | `lib/pet/bubbleText.js`: Pango measuring (title 11 px bold, body 10 px, 164 px column, 2-line limit), `agentTitle` and `fittedBubbleBody` binary searches. Use `PangoCairo.FontMap.get_default().create_context()`. Probed under plain `gjs -m`: 80 × `W` at 10 px in a 164 px column gives 5 lines. Use the shell's actual font for the view (`clutter_text` font) and pass the description in. | JS | 0.75 d |
| 5 | `lib/pet/floatingPet.js` (St view): the chrome root and re-stack, the `sync()` visibility sources, sprite and `SpriteAnimator`, gestures, petting motion, bob, run `ease()`, speech bubble actor, heart actor, hover and event callout, context menu, clamping and multi-monitor, power-saver and `enable-animations` | JS | 2.5 d |
| 6 | `SpriteAnimator` `fit: 'aspect'` option (centred, fractional, NEAREST) | JS | 0.25 d |
| 7 | `lib/pet/agentBus.js`: name ownership, `Report` and `OpenUrl` export, disposal in `disable()` | JS | 0.5 d |
| 8 | `extension.js`: construct and destroy the pet; fan events out to the pet before the Notifier; pass `state` through; indicator menu item; popover footer eye | JS | 0.5 d |
| 9 | Schema keys, prefs group, row move, `prefsModel` size step helper | JS/XML | 0.5 d |
| 10 | Helper: `companion.sulkIdleSeconds`; `visibleTopFraction` in the manifest writer; contract fixtures and `stub-helper.py` fixture update; Go tests | Go | 1 d |
| 11 | Docs: protocol §5.4/§8.2 additions, ui.md floating-pet section (LATER → done), README Linux hook, CHANGELOG | md | 0.5 d |
| 12 | Sandbox QA through `scripts/headless-shell.sh` only (per `tasks/lessons.md`). Cover: drag, click, menu, overview hide, fullscreen hide, `--cycle` lock path, agent `busctl` calls, and the CPU gate with both sprites. | — | 1 d |

**Total: about 10 developer days.** JS is about 8 d, Go 1 d, docs and QA about 1.5 d. The St view
(#5) carries most of the uncertainty.

### 5.2 Swift tests to port

Swift is not installed, so its assertions are the only oracle. Keep the Swift `file:line` in each JS
test comment (port `tasks/todo.md` "Validation").

**A. Verbatim** (same inputs and outputs; JS only changes syntax).

`T/AgentActivityTests.swift`, into `agentActivity.test.js`, through `eventFromUrl`:
- `:11-17`: the four states parse; `POKETOKENBAR://Agent?state=DONE` gives `done(nil)`. GLib.Uri keeps
  the host's case (probed: `Agent`), so lowercase it yourself.
- `:19-26`: `state=sleeping`, no state, message only, host `settings`, `poketokenbar:agent?…` (no host;
  GLib.Uri gives `host = null`, path `agent`), and the `https` scheme all give null.
- `:28-35`:
  - `Pick%20a%20branch` decodes;
  - `working&message=ignored` gives working with no message;
  - `message=first&state=done&state=working` gives `done("first")` (the first `state` wins).
  - Parse the query by hand: split on `&`, then `GLib.Uri.unescape_string`.
  - **Never** use a form decoder, because URLComponents keeps `+` literal. GJS has no `URL` or
    `URLSearchParams` anyway (probed).
- `:37-47`:
  - `%20%20line%201%0A%0D%09line%202%20%20` gives `"line 1 line 2"`;
  - `"ab‮cd​ef\u0007g"` gives `"abcdef g"`;
  - the ZWJ family emoji is unchanged;
  - whitespace-only, empty and null inputs give null;
  - `message=` gives `waiting(nil)`.
- `:49-65`:
  - 80 × `"a"` is unchanged;
  - `"word " × 100` gives ≤ 80 graphemes, a `…` suffix and no space before the `…`;
  - the same text percent-encoded through a URL gives an equal result.
- `:69-76`: 79 × (`a` + 2000 × U+0301) gives ≤ 320 scalars, ≤ 80 graphemes and a `…` suffix.
  Precondition: the raw string has 79 graphemes.
- `:96-106` (`t0` = any epoch):
  - working gives the deadline `t0+600`;
  - `expire(t0+599)` is false and the activity stays working;
  - `expire(t0+600)` is true, the activity becomes idle and the deadline null;
  - `expire(t0+10000)` is false.
- `:108-114`: working at t0 and again at t0+500, then `expire(t0+700)` is false and the activity is
  still working.
- `:116-123`: `done("ok")`; `expire(t0+5.9)` is false; `expire(t0+6)` is true and the activity idle.
- `:125-139`:
  - waiting after working has a null deadline;
  - `expire(t0+86400)` is false;
  - `dismissBubble()` is true and the activity becomes idle;
  - waiting then working gives working.
- `:141-153`:
  - `dismissBubble()` with only working returns false and keeps the deadline;
  - after a done, it returns true and the deadline becomes null;
  - a second call returns false.
- `:155-162`: idle empties the agents and nulls the deadline.
- `:166-181`:
  - `label=%20my%0A%20project%E2%80%AE%20` gives `"my project"`;
  - no label and a space-only label both give null;
  - 100 × `x` gives 24 graphemes with a `…` suffix;
  - 20 × (`a` + 500 × U+0301) gives ≤ 96 scalars.
- `:184-195`:
  - web working and api done: `working` is true and `done` = `{api, nil}`;
  - `expire(t0+7)` is true, `done` becomes null, the summary stays working and the activity is working.
- `:197-203`: unlabeled working, then done, leaves `working` false and 1 agent.
- `:205-219`:
  - waiting a("first"), working b, waiting c("latest") give `waiting` = `{c, "latest"}`, others = 1,
    working true, activity `waiting("latest")`;
  - then working c gives `waiting` = `{a, "first"}`, others = 0.
- `:221-229`: the done overlays waiting, then `expire(t0+6)` brings back `waiting(nil)`.
- `:232-240`: done a at t0, b at t0+2, c at t0+1 gives `done` = `{c,"c"}`. Then `expire(t0+7)` gives
  `{b,"b"}`.
- `:242-250`: a at t0 and b at t0+300 give the deadline t0+600; expire leaves keys `["b"]` and the
  deadline t0+900.
- `:252-261`: dismissing leaves keys `["d"]`; a second dismiss returns false.
- `:263-269`: idle a leaves keys `["b"]`.
- `:272-285`:
  - 50 labels give 8 agents, the set `agent42…agent49`;
  - refreshing agent42 evicts nothing;
  - a new label evicts agent43 and keeps agent42.

`T/FloatingPetPettingTests.swift`, `PettingDetectorTests` `:8-85`. Samples every 0.05 s from t = 100,
with `minStroke = 14`:
- `[10,25,40,25,10,25,40,25,10,25,40]` gives **2** triggers;
- a straight pass from 0 to 200 by 10 gives 0;
- `[10,40,10,40,10,40,10,40]` at a step of 0.8 s gives 0;
- after `[10,40,10,40]`, a pause of `window + 1` s, then 10, 40, 10, all three return false;
- 30 samples alternating 10/18 give 0;
- `[10,30,10,30,10,30,10]` gives > 0 at a minimum of 14 and 0 at 40;
- after `reset()`, `[10,40,10]` gives 0.

`StrokeHeartBubbleTests` `:90-136`, `:237-241`:
- `heartsShowing(false)` is true and `heartsShowing(true)` is false;
- the duration is 1800 ms;
- `isShowingBubble`: (false, true) is true, (true, false) is true, (false, false) is false;
- every heart row has width 17;
- each row is a palindrome after mapping R, D and H to R;
- the last row has a single `O` at index 8;
- the art width `17×2 = 34` is ≤ 180;
- `height − overlap = 32 − 3 = 29` is ≤ 72;
- `bubbleTopOffset(0, 32, 3) = −29` is ≥ −72;
- the cell set is exactly `{O,W,R,D,H,.}` and every non-`.` cell has a colour.
- Drop `:105-109` (native tooltip) and `:111-113` (Observable), which have no equivalent.

`T/FloatingPetAgentTests.swift`, pure parts:
- `:12-22` `resolveMove`: home/false → (home, true); home/true → (home, true); away/false →
  (away, false); returning/false → (returning, false); away/true → (home, true); returning/true →
  (home, true).
- `:76-83` `runsToCursor`: home → true; returning → true; away → false; motion off → false; not
  visible → false.
- `:85-93`:
  - `allowsMotion`: (true, false) is true, (false, false) is false, (true, true) is false;
  - `shouldBob`: (working, motion) is true, (working, no motion) is false, (not working, motion) is false;
  - `agentBobInterval` is > 0.
- `:97-109` bubble priority: limit beats waiting and empty; waiting gives `agentWaiting(a, x, 2)`;
  done plus waiting gives `agentDone(b, nil)`; working only and empty give null.
- `:111-127` (pure parts):
  - agent-only speech gives `isShowingBubble` true, `heartsShowing` false and `shouldPopEvent(2, 1, true, true)` false;
  - working only gives `heartsShowing` true, `isShowingBubble(false, true)` true, `(false, false)` false,
    and `heartsShowing(alert: true)` false.
- `:144-150` (needs `bubbleText` for fitting): `agentWaiting(nil, nil, 0)` gives the title
  "Your agent needs you" and the body "It's waiting for your input."; `agentDone(nil, "Tests pass")`
  gives the title "Done!" and the body "Tests pass".
- `:152-157`: titles `"web · waiting"`, `"web · waiting +2"` and `"api · done"`. These are exact
  because they fit the column.

`T/PetEventLineTests.swift`:
- `:46-57` `shouldPopEvent`: (1, nil) is true; (2, 1) is true; (1, 1) is false; (nil, nil) is false;
  not visible is false; a bubble is false; the duration is 6 s.
- `:59-67` hover text: with an event line and prayers 3 it is `plain + "\nEVENT\n" + prayerLine(3)`;
  with a null event it is `plain`.
  - Without prayers in this milestone, port only the null-event and event-only halves:
    `plain + "\nEVENT"`.
- `:71-153` are companion-store tests, helper-side and partly deferred (Ditto, prayers). They are out of
  pet scope.

Upstream `US` bubble rules, for which there is no dedicated Swift test in the files read:
- port `bubbleAlert` (`US:2153-2158`) and `shouldDismissBubble(ttl 6)` (`US:2161-2163`) as tests that
  derive from the code;
- mark them "derived, no Swift oracle".

**B. Geometry: same cases, mirrored to y-down.** AppKit uses a bottom-left origin with y up; the
Clutter stage uses a top-left origin with y down. A verbatim `runPanelOrigin` in stage coordinates
would put "below" above the cursor and let the **top** win the oversized clamp, pushing the pet off
screen.

- Port the function in y-down form:

  ```
  fitsRight = c.x + gap + w <= wa.x + wa.w
  fitsBelow = c.y + gap + h <= wa.y + wa.h
  x = fitsRight ? c.x + gap : c.x - gap - w
  y = fitsBelow ? c.y + gap : c.y - gap - h
  x = max(min(x, wa.x + wa.w - w), wa.x)          // left wins, as in Swift
  y = min(max(y, wa.y), wa.y + wa.h - h)          // BOTTOM wins: max first, then min
  ```

- Convert each Swift fixture with `y′ = H − y` (points) and `y′ = H − (y + h)` (rect origins), using
  the fixture screen's top as H:
  - `:34-39`: screen `(0, 25, 1440, 875)` y-up becomes `(0, 0, 1440, 875)` (H = 900). Panel 180×168,
    cursor `(600, 500)` becomes `(600, 400)`. Expected `(616, 316)` y-up becomes **`(616, 416)`**, and
    the panel does not contain the cursor.
  - `:41-58`:
    - the corner cursors `(1430,500)`, `(600,40)`, `(1435,30)`, `(5,895)` become `(1430,400)`,
      `(600,860)`, `(1435,870)`, `(5,5)`; each result is inside the work area and does not contain the
      cursor;
    - right edge: **`(1234, 416)`**;
    - bottom edge (`(600,860)`): **`(616, 676)`**.
  - `:60-64`: secondary `(−1920, 0, 1920, 1080)` (H = 1080), cursor `(−10, 1075)` becomes `(−10, 5)`;
    the result is inside.
  - `:66-71`: work area `(0, 0, 300, 200)`, panel 384×456, cursor `(150, 100)` gives **`(0, −256)`**:
    the panel bottom sits on the work-area bottom.
- `panelSize` is unchanged (`FPP:516-522`). `panelOrigin` and `petOrigin` with the pet's **bottom-left**
  are `x = pet.x − inset`, `y = pet.bottom − panel.h`, and the inverse.
- Wiring `:339-358` mirrored (H = 900): drop the panel at y-up `(900, 100)`, so its top is
  `900 − 268 = 632`, at `(900, 632)`, with the bubble showing. The saved pet bottom-left is
  **`(942, 800)`**.
- `SpriteVisibleTop` (`T/FloatingPetPettingTests.swift:147-186`), helper and JS. Verbatim **only if the
  pet centres the fitted sprite** (`fit: 'aspect'`, §2.3); bottom alignment changes `inset`:
  - `inset(96, 36×66, 0.05) = 4.8`;
  - `inset(96, 60×30, 0.1) = 28.8`;
  - `inset(96, 30×30, 0.1) = 9.6`;
  - `bubbleTopOffset(24, 32, 3) + 32 = 27`;
  - `bubbleTopOffset(3.84, 32, 3) = −25`;
  - `anchorFraction([0,0,.3×7]) = 0.3`, `([.2]) = 0.2`, `([]) = 0`.
  - The opaque-row scan (`:188-235`) becomes Go tests on 10×10 RGBA: one pixel on row 3 gives 0.3; side
    columns with the centre from row 5 give 0.5; side columns only from row 2 give 0.2.
  - Rounding: Swift `.rounded()` is half away from zero. Use
    `Math.sign(v) * Math.round(Math.abs(v))`, not `Math.round`.

**C. Controller wiring** (`T/FloatingPetAgentTests.swift:239-492`), ported against the pure
`petController.js` with fake timers. Same scenarios and expectations:
- `:284-296`: events are dropped while off;
- `:298-314`: turning the setting off resets the tracker and the run phase, and leaves no timer;
- `:317-337`: the run never overwrites home: saved `(300, 200)` stays `(300, 200)`;
- `:339-358`: a drag during the run becomes home, with mirrored values;
- `:360-374`: a click dismisses the bubble and still opens the popover (`opened` 1, then 2; working
  survives);
- `:378-415`: hosting click for an egg and for a hatched pet.
  - For an egg, M3 prays (`pray` command, 🙏 particles, wobble); the M2 adaptation, which opened the popover, is superseded. The context menu *Open Token Bar* opens it.
  - The context-menu Open still dismisses a done.
- `:417-434`:
  - the expiry fire date equals the deadline;
  - an early fire 0.5 s before keeps `done(nil)` and re-arms at the same deadline;
  - the on-time fire gives idle and no timer;
  - waiting arms no timer.
- `:443-460`:
  - working web plus done api gives the bubble `agentDone(api)` while still bobbing;
  - api's expiry gives `runPhase` home, no bubble, and the bob continues.
- `:463-476`: waiting web, working api, done api gives `agentDone(api)`; after expiry, the bubble is
  `agentWaiting(web, nil, 0)` again.
- `:478-492`:
  - waiting web, waiting api, working cli gives `agentWaiting(api, nil, 1)`;
  - a click leaves no bubble, working stays true, the bob continues and `opened = 1`.

**D. Not portable as values; keep the property assertions only.**
- These measure with NSFont and AppKit:
  - `T/FloatingPetAgentTests.swift:130-142` (limit content unchanged; the account is absent in M1);
  - `:158-178` (word-boundary cut, `…`, a label cut that keeps ` · waiting +7`);
  - `:183-220` (no truncation in any language, titles ≤ 164 px, bubble ≤ 70 px);
  - `:222-233` (`fittedBubbleBody`: the `…` suffix, a prefix property, and that one more character
    must not fit).
- Port each as a property test over the Pango measurer: the same inputs, with "does not truncate",
  "ends with …", "is a prefix" and "is maximal" checks. The cut points depend on fonts.
- Not portable at all: NSImage renders (`T/FloatingPetPettingTests.swift:250-366`, opt-in renders),
  NSPanel and observation timing.

---

## 6. Risks and open questions

### Risks

1. **The input hit area is rectangular.** A reactive St actor picks on its allocation, not its alpha.
   Mitigation: only the fitted sprite rectangle is reactive. Refinement: a `vfunc_pick` with the
   opaque bounding box. Verify pass-through in the sandbox.
2. **Gesture arbitration.** Click, pan and secondary click share one actor. `ui/windowPreview.js:133-141`
   shows ClickGesture plus a drag gesture without explicit relations, but no shell code combines
   `PanGesture` with `ClickGesture` on the same actor. Verify that a 4 px pan cancels the click. The
   fallback is raw `vfunc_button_*`/`vfunc_motion_event` with `global.stage.grab()`.
3. **`trackFullscreen` and z-order surprises** (§2.1). A single `sync()` owner avoids the first. The
   re-stack above `window_group` must be checked against overview, screen-shield and lightbox ordering
   in the sandbox.
4. **CPU.** The live-session CPU gate for one animated sprite is still open (port `tasks/todo.md`). The
   pet adds a second, larger sprite, plus the bob, at 2 Hz. Measure both together. `SpriteAnimator`'s
   unmapped pause covers every hide path.
5. **Agent state is lost on lock**, because the extension is disabled. A `done` sent while locked is
   never seen. See the `XDG_RUNTIME_DIR` persistence option in §4.1.
6. **Pixel art at fractional scales** (§2.3).
7. **Fonts.** Bubble cut points differ from macOS (Pango versus AppKit). Only the property tests guard
   them. The 72 px headroom was sized for Japanese copy on macOS (`FPP:38-40`); recheck it with Cantarell
   or Adwaita Sans, and with the gettext languages when translations land.
8. **D-Bus name ownership from inside gnome-shell.** It is fine for a local extension.
   extensions.gnome.org review could object, but EGO is out of scope (port `tasks/todo.md`
   "Assumptions").
9. **A coordinate-flip bug class.** Every geometry function is y-down. Keep the mirrored fixtures (§5.2 B)
   as the guard.
10. **Hover text needs the sulk idle duration** from the helper (§4.2). Without it, drop the suffix
    rather than guess.

### Open questions (for the orchestrator or the user)

1. **Prayers:** stay deferred, so an egg click opens the popover? Or ship a `pray` helper command plus
   particles in this milestone (about +1.5 d: helper command, 11 quips, prayer hatch text, refresh
   throttle)?
   **Resolved (2026-11-19):** prayers stay deferred; an egg click opens the popover. **Superseded (2026-11-20, Milestone 3):** prayers shipped in 0.3.0; an egg click prays and the context menu *Open Token Bar* opens the popover.
2. **Fullscreen:** hide over fullscreen (recommended), or keep upstream parity with an opt-in setting?
   **Resolved (2026-11-19):** hide over fullscreen. One `sync()` hides the pet for the overview, fullscreen, power-save and settings; `addChrome` is restacked just above `global.window_group`.
3. **D-Bus name and interface:** `io.github.poketokenbar.Agent` / `…Agent1`, or a name under the
   extension uuid? Ship the `x-scheme-handler` `.desktop` file in the RPM for URL compatibility?
   **Resolved (2026-11-19):** name `io.github.poketokenbar.Agent`, interface `io.github.poketokenbar.Agent1`, methods `Report(sss)` and `OpenUrl(s)`; hooks use `busctl`; no URL handler and no `.desktop` file (protocol §15).
4. **Lock persistence** of agent state in `$XDG_RUNTIME_DIR` (§4.1): needed in this milestone?
   **Resolved (2026-11-19):** agent state is not persisted across a lock.
5. **Passive JSONL "working" detection** as a no-hook fallback: defer (recommended) or include?
   **Resolved (2026-11-19):** passive JSONL detection is deferred.
6. **Sprite fit:** fractional NEAREST for parity, or integer snapping when the scale is ≥ 2?
   **Resolved (2026-11-19):** integer sprite scaling when possible.
7. **The `always-faces-back` row:** move it from "Top bar" to the new "Floating pet" group (upstream
   placement), or keep it in both?
