# Lessons

## 2026-11-18 — A headless/nested gnome-shell can break the live session's document portal

- **What happened.** The Spike A subagent smoke-tested its extension in a headless `gnome-shell`
  started under `dbus-run-session`. Its brief only said "do not install or enable anything in the live
  session". The private bus D-Bus-activated its own `xdg-document-portal` while sharing the live
  `XDG_RUNTIME_DIR`. When it exited, the live `/run/user/1000/doc` FUSE mount was gone. The live portal
  process (still running) no longer had a mount, so Flatpak apps lost portal file access until
  `systemctl --user restart xdg-document-portal.service`.
- **Why the brief didn't catch it.** It forbade writes to the extensions dir and to dconf, but not
  starting a second shell. A second shell shares `XDG_RUNTIME_DIR`, which is session-global state.
- **Rule.**
  - Subagent briefs must forbid launching any `gnome-shell` instance (headless, nested or devkit)
    unless the orchestrator explicitly authorizes it.
  - The only authorized launcher is `scripts/headless-shell.sh`. It uses a private
    `XDG_RUNTIME_DIR` under `.sandbox/run`, a short path that fits the 108-byte socket limit, private
    XDG dirs, `GSETTINGS_BACKEND=memory` and `PTB_STATE_DIR`. Afterwards it kills sandbox processes by
    environment, unmounts FUSE mounts lazily and checks isolation.
  - First verified 2026-11-18:
    - the live PID, the live `/run/user/$UID` and the absence of leftovers were all unchanged;
    - the portal and gvfs mounted *inside* the sandbox.
  - `make run-nested` must warn about the risk and check `findmnt /run/user/$UID/doc` afterwards,
    printing the restart command if the mount is gone.

## 2026-11-18 — A timing test that passed alone failed under `go test ./...`

- **What happened.** `TestMidnightTimerRollsOver` set the fake clock 700 ms before midnight. It then
  checked the scan counter once, at the first state showing the new day. Run alone it passed 10/10.
  Under the parallel load of the whole module, a state rebuild crossed midnight before the timer's
  rescan was counted, and the test failed. The subagent reported "all green" from isolated runs.
- **Rule.**
  - Run the **whole module** (`go test -race ./...`) as the acceptance check, not only the package.
  - Timing tests poll for the expected condition with a deadline instead of sampling once.
  - Fake-clock margins must leave room for a loaded machine (seconds, not hundreds of ms).

## 2026-11-19 — `isolation: worktree` targets the session's primary repo, not the project repo

- **What happened.** The orchestrator session's working directory is the upstream Swift repo
  (`../PokeTokenBar`). Four Phase 10–13 agents were spawned with `isolation: "worktree"`. The harness
  created their worktrees in the **Swift** repo, at `main`, with no `helper/` and no `feature/m2`. Each
  agent noticed and created its own worktree of this repo (`../PokeTokenBar-GNOME-wt/*`,
  `../ptbg-shop`). No damage was done, but it relied on luck: an agent could have edited the
  read-only Swift repo or the shared `feature/m2` working tree.
- **Rule.**
  - Don't use `isolation: "worktree"` from a session rooted elsewhere.
  - Each parallel brief names the exact worktree to create:
    `git -C <this repo> worktree add <short path> -b <branch> feature/m2`.
    Keep the path short so the sandbox's 108-byte socket path still fits.
  - The brief also forbids editing the shared working tree.

## 2026-11-19 — A fix for one actor must be swept to every `ease()` that destroys its actor

- **What happened.** Phase 20 fixed the 🙏 particle and the 🎭 mask, whose `onStopped` destroyed
  the actor even when the ease was interrupted. Two siblings in the same file kept the pattern:
  `popInOut` disposed on an interrupted pop-in, `fadeOutAndDestroy` disposed on any stop.
- **Why it was missed.** The phase 20 fix covered the instances the pet-pray run exposed, with no
  class sweep. No sandbox suite interrupted a `feedbackFx` effect: `shop-cycle` disabled with a Shop
  confirmation showing, never with an effect in flight.
- **What the probe showed (phase 22a).** `shop-cycle` with `PTB_SHOT_FX_HOLD=1` keeps the capsule,
  sparkles and mask in flight through a tab switch, a menu close and the `--cycle` disable.
  - On disable the card is destroyed before its effects stop. The `destroy` signal sets `_ptbGone`
    first, so the old code was a no-op there. These instances were latent, not a live CRITICAL.
  - Tab switches and menu closes do not stop transitions.
  - The CRITICAL needs a stop on a live actor. For the pet, `removeChrome` unstaged the root before
    destroying it. A forced `remove_all_transitions()` on live fx actors reached the old `dispose`
    from inside the stop; the fixed code does not.
- **Rule.**
  - Only a finished ease may destroy its own actor (`onStopped: finished => { if (finished) … }`,
    or `onComplete`). An interrupted one is left to its parent's teardown.
  - A fix to one instance of a pattern greps the whole tree for the pattern in the same change.
  - Teardown checks have to keep an effect in flight while the disable lands, rather than relying
    on timing luck.

## 2026-11-19 — A shutdown hook that was never seen firing hid a shutdown JS ERROR

- **What happened.** At shell exit a helper event reached `Notifier._post` after the date menu was
  destroyed (`this._settings is null` in `dateMenu.js _sync`). Commit 7903471 had stopped the helper
  on `MetaContext::prepare-shutdown` and reported the error gone. It came back in phase 22.
- **Root cause (logged in the sandbox, GNOME Shell 51).**
  - After "Shutting down GNOME Shell" the shell destroys `uiGroup`, the panel (and our indicator),
    the date menu and the message tray, then emits `global::shutdown`.
  - `main.js`'s first `shutdown` handler spins a nested main loop. That loop still runs our
    stdout `read_line_async` callback, so an event line written around exit is dispatched there.
  - `prepare-shutdown` never reached our handler before the process exited.
- **Why the earlier fix and its check missed it.**
  - The fix assumed a signal order nobody observed: the handler had no log, so a passing run could
    not tell "hook stopped the helper" from "no event landed in the ~40 ms window".
  - The run passes only when an event line is in flight at exit. The stub emits one per second,
    so whether a run hits that window depends on timing.
- **Fix.**
  - Teardown hook on our own indicator's `destroy` when it fires outside `disable()` (disable
    disconnects it first). It closes the notifier, drops the indicator, then stops the helper, and
    logs one line.
  - `Notifier.close()` refuses every later post, including an icon lookup that settles after a stop.
    It leaves the tray alone, since destroying our source would reach the dead date menu.
  - `disable()` closes the notifier first as well.
- **Rule.**
  - Prove a lifecycle hook fires on the exact path before calling the bug fixed. Log the hook, and
    show the trigger in the same run: the stub's write time against "Shutting down", plus a log of
    the dispatch or the refusal.
  - Inject each defence alone (`p-nohook`, `p-nostop`, `p-noclose` in the phase 22a runs). Only
    the no-hook build reproduced the error.
  - For shell-exit behaviour, trust only signals seen in the sandbox log. On 51 the panel destroying
    our indicator is the first one we own.

## 2026-11-19 — The prefs process logs to session.log, which no check read

- **What happened.** `prefs.js _floatingPet` added the Size row with `group.add(size)` and again in
  the loop that binds the pet rows' visibility. Every prefs window logged an Adwaita-CRITICAL
  (`adw_preferences_group_add: gtk_widget_get_parent (child) == NULL`), from M2 until phase 22a.
- **Why it was missed.** Sandbox acceptance grepped `shell.log` for JS ERROR and CRITICAL. The
  prefs window is a separate bus-activated process (`org.gnome.Shell.Extensions`), and its stderr
  goes to `session.log`. The row still rendered, because the second add failed. So the screenshots
  looked right.
- **Fix and sweep.** One `group.add` per row. Every `add`/`add_row`/`add_suffix` in `prefs.js` and
  `lib/backupPage.js` was checked, and there are no other double adds. Before the fix, `backup-prefs`
  produced 1 CRITICAL in `session.log`; after it, 0. `pet-prefs` with the pet on shows Size, bubbles
  and agents once each.
- **Rule.** `scripts/headless-shell.sh` now writes `log-check.txt` and prints JS ERROR and
  CRITICAL counts for both `shell.log` and `session.log`. Acceptance quotes both lines.

## 2026-11-19 — Features built in parallel worktrees need a test of their combination

- **What happened.** Phase 17 (Ditto reveal) and phase 18 (save import) were built and tested in
  separate worktrees. Each suite seeded its own state, so neither covered the case of importing a
  save whose active Pokémon is an unrevealed disguised Ditto and then reaching the threshold. That
  case could only be tested after the merge, and nobody added the test.
- **Fix.** `TestImportedDisguisedDittoRevealsAtTheThreshold` (companion) goes through real bytes:
  `ExportSave` → `ReadSave` → `ApplyImport`. It then runs two `Update`s on the import day: no reveal
  at 100M of 125M, then exactly one `dittoReveal`, no `evolve`, and a carry of 5M. Injecting
  `rebase` dropping `DittoDisguise` makes the test fail.
- **Rule.** When parallel phases each change state that the other one reads (save fields, events),
  the merge phase lists their cross-product and adds one test per pair before it reports done.
