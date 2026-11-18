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
  - `make run-nested` must warn about the risk and check `findmnt /run/user/$UID/doc` afterwards,
    printing the restart command if the mount is gone.
