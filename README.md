# PokeTokenBar-GNOME

Port of [PokeTokenBar](https://github.com/chattymin/PokeTokenBar) (macOS) to GNOME Shell 51 on Fedora Linux.
It tracks Claude Code token usage and turns it into a Pokémon companion in the top bar.

**Status:** Milestone 1 in progress.

## Architecture

- `helper/`: `ptb-helper`, a Go process (stdlib only) that does log scanning, HTTP and game state.
- `extension/`: the GJS extension. It only renders state and sends commands.
- Transport: newline-delimited JSON (NDJSON) over the helper's stdin/stdout. See `docs/protocol.md`.

The helper exists because the extension runs on the compositor's main thread, where heavy parsing would freeze the session.
Specs live in `docs/spec/`; see `SPEC.md`.

## Build and install

```
make build     # helper binary + compiled schemas
make test
make lint
make install   # copies the extension to ~/.local/share/gnome-shell/extensions/poketokenbar@local
make pack      # dist/poketokenbar@local.zip
```

Then log out and back in (Wayland) and enable the extension.

### Install from RPM

Fedora, system-wide. Build requirements: `sudo dnf install rpm-build golang`.

```
make rpm
sudo dnf install dist/gnome-shell-extension-poketokenbar-*.rpm
```

Log out and back in (Wayland), then `gnome-extensions enable poketokenbar@local`.

A per-user copy from `make install` in `~/.local/share/gnome-shell/extensions/` takes
precedence over the system one, so run `make uninstall` first.

## Disclaimer

Unofficial, non-commercial fan project. Pokémon and Pokémon names are trademarks of Nintendo,
Creatures Inc. and GAME FREAK inc. Sprites and data are fetched at runtime from
[PokéAPI](https://pokeapi.co) and are not bundled.

## License

MIT, see `LICENSE`.
