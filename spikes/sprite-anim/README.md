# Spike A: animated sprite in the GNOME Shell 51 top bar

Question: can the extension show a smoothly animated Gen-V Pokémon sprite in the top bar cheaply?
Exit criteria (plan, Phase 0.4): smooth at the 0.4 s floor, no relayout per frame, gnome-shell idle
CPU delta < 1 %.

| Path | What |
|---|---|
| `composite.go`, `main.go` | GIF → fully composited RGBA `frame-NNN.png` + `manifest.json` (Go stdlib) |
| `composite_test.go` | synthetic 4-frame GIF, exact pixels for every disposal mode |
| `extension/` | the spike extension `ptb-sprite-spike@local` |
| `extension/capFrameRate.js`, `test/` | port of the macOS frame-rate cap, tested with node |
| `measure-cpu.sh` | average gnome-shell CPU over a window |
| `out/` | downloaded GIFs and generated frames (gitignored) |

## How it renders

- **Load, once at enable.** Each PNG is decoded with GdkPixbuf and uploaded into its own
  `St.ImageContent`, which is backed by a GPU texture. This is the same call sequence that Shell 51's
  own `screenshot.js` and `qrCode.js` use:

  ```js
  const content = St.ImageContent.new_with_preferred_size(w, h);
  content.set_bytes(cogl, bytes, Cogl.PixelFormat.RGBA_8888, w, h, rowstride);
  // where cogl = global.stage.context.get_backend().get_cogl_context()
  ```

- **Per tick.** The tick calls `actor.set_content(frames[i])` on a `Clutter.Actor` whose size is fixed,
  with `set_content_scaling_filters(MIN_FILTER, NEAREST)`. There is no file IO, no decoding and no size
  change, so there is no relayout.
- **Timer.** `GLib.timeout_add` is re-armed with each frame's delay. It is removed while the actor is
  unmapped.

## 1. Generate the frames

```sh
cd spikes/sprite-anim
go test ./...
node test/capFrameRate.test.mjs
mkdir -p out
for id in 25 6 94; do
  curl -fsSL -o out/$id.gif \
    https://raw.githubusercontent.com/PokeAPI/sprites/master/sprites/pokemon/versions/generation-v/black-white/animated/$id.gif
  go run . -in out/$id.gif -out out/$id
done
```

## 2. Install the extension

```sh
EXT=~/.local/share/gnome-shell/extensions/ptb-sprite-spike@local
mkdir -p "$EXT"
cp extension/metadata.json extension/extension.js extension/capFrameRate.js "$EXT"/
cp -r out/25 "$EXT/frames"     # or set FRAMES_DIR in extension.js to an absolute path
```

These knobs are consts at the top of `extension.js`:

| Const | Default | Meaning |
|---|---|---|
| `FRAMES_DIR` | `null`, which means `$EXT/frames` | directory holding `manifest.json` |
| `FLOOR_SEC` | `0.4` | `0.4` Power saver, `0.2` Balanced, `0.1` Smooth, `0` native timing |
| `BOX_HEIGHT` / `MAX_WIDTH` | `16` / `29` | logical px. `16` is the panel icon size; also try `22`/`40`, close to macOS |
| `MIN_FILTER` | `NEAREST` | also try `Clutter.ScalingFilter.LINEAR` or `TRILINEAR` |
| `STATS_INTERVAL_SEC` | `10` | how often the tick and relayout counters are logged; `0` turns them off |

**On Wayland, edits only take effect after the shell restarts.** GJS caches ES modules, so `disable`
followed by `enable` keeps running the old code. Changing a const therefore means logging out and back
in. Swapping the frames directory needs only `disable`/`enable`.

## 3. Load it

### A. Real session (needed for the CPU gate)

A Wayland shell only discovers new extension directories at startup.

1. Log out and log back in.
2. Run:

   ```sh
   gnome-extensions enable ptb-sprite-spike@local
   gnome-extensions info ptb-sprite-spike@local   # State: ACTIVE; ERROR shows the message
   ```

### B. Nested shell (smoke test only; its CPU numbers do not count)

This needs `sudo dnf install mutter-devkit`. Do not use `gnome-extensions enable` here: it writes
`enabled-extensions` straight into your real dconf, so the spike would also load at your next login.
The recipe below gives the nested shell an in-memory settings backend and enables the extension over
the nested shell's D-Bus. As a side effect, none of your other extensions load inside it.

```sh
GSETTINGS_BACKEND=memory dbus-run-session -- bash -c '
  gnome-shell --devkit --wayland &
  sleep 8
  gdbus call --session --dest org.gnome.Shell --object-path /org/gnome/Shell \
    --method org.gnome.Shell.Extensions.EnableExtension ptb-sprite-spike@local
  wait'
```

Two details in this recipe matter:

- **Where the variable is set.** `GSETTINGS_BACKEND` goes *before* `dbus-run-session`, so that services the
  bus starts on demand inherit it too.
- **The `gdbus` address.** The shell serves the `org.gnome.Shell.Extensions` *interface* at
  `/org/gnome/Shell` under the bus name `org.gnome.Shell`. The bus *name* `org.gnome.Shell.Extensions`
  belongs to a separate prefs service.

The nested shell logs to that terminal.

**Side effect on your real session (observed in a headless run).** The private bus starts its own
`xdg-document-portal`. At startup that portal runs `fusermount3 -u -z $XDG_RUNTIME_DIR/doc`, which
detaches your session's `/run/user/$UID/doc` mount. Flatpak apps lose document-portal file access until
you restart the portal. After the nested shell exits, run:

```sh
systemctl --user restart xdg-document-portal.service
findmnt /run/user/$UID/doc        # should list fsname=portal again
```

## 4. Measure CPU (real session)

Keep the session idle the whole time: no mouse, no other animations, and the screen must not blank or
lock, because locking disables extensions. Run each state twice, alternating:

```sh
gnome-extensions disable ptb-sprite-spike@local; sleep 10; ./measure-cpu.sh 60   # baseline A
gnome-extensions enable  ptb-sprite-spike@local; sleep 10; ./measure-cpu.sh 60   # spike B
```

The delta is B − A, in % of one core. The target is under 1 %. To test `FLOOR_SEC` 0.2 or 0.1, edit the
const, log out and back in, and measure again.

## 5. What to look at

- **Smoothness.** At 0.4 s the cadence should be steady at about 2.3–2.5 fps, and one loop should take
  the native length: #25 Pikachu 6.0 s, #6 Charizard 7.3 s, #94 Gengar 4.4 s.
  - Watch for hitches, and for loops that run slow or fast.
  - Expected timelines at 0.4 s: #25 → 14 frames, #6 → 18, #94 → 11. At 0.1 s and native timing, every
    frame plays at about 10 fps.
- **Crispness.**
  - Pixel edges should be hard, with no blur.
  - At a 16 px box the 46–89 px canvases are downscaled 2.9–5.6× at scale 1, so NEAREST drops rows and
    columns. Judge whether that is acceptable; compare against a bigger box and against
    `LINEAR`/`TRILINEAR`.
- **Alignment.**
  - The sprite should be vertically centred in the panel with its feet on a fixed baseline.
  - Nothing should jitter or shift between frames.
  - Neighbouring indicators must not move.
- **Journal evidence.**
  - One `preloaded N frames … in X ms` line at enable. That is the synchronous decode-and-upload cost,
    paid on every enable, and the shell re-enables extensions on every unlock.
  - A stats line every 10 s. `ticks` must grow while `spriteRelayouts` and `buttonRelayouts` stay
    constant. In the headless run, both read 1 from the first stats line on: the one-off relayout when
    the indicator joins the panel.
- **Scale factor.** If you can, switch the display scale (100 % → 200 %).
  - The `size=` value in the stats line should double, and the sprite should stay sharp.
  - `spriteRelayouts` should rise once at the switch. That shows the counter is live.

## 6. Errors and logs

```sh
journalctl --user -f -o cat /usr/bin/gnome-shell | grep -i -E 'ptb-sprite-spike|JS ERROR|Gjs-CRITICAL'
gnome-extensions info ptb-sprite-spike@local
```

## 7. Remove

```sh
gnome-extensions disable ptb-sprite-spike@local
rm -rf ~/.local/share/gnome-shell/extensions/ptb-sprite-spike@local
```

The code stays cached in the running shell until the next log-out. The nested recipe stores nothing,
because its settings were in memory.
