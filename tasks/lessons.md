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
