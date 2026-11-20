# SPEC

Index of the specifications for PokeTokenBar-GNOME.

- [Usage pipeline and official limits](docs/spec/usage.md)
- [Companion game rules, save model, PokéAPI](docs/spec/companion.md)
- [UI, settings, notifications](docs/spec/ui.md)
- [Helper/extension protocol](docs/protocol.md)

Upstream baseline: PokeTokenBar commit `38e3e55` (v2.5.7).

## Milestone 1 delivered (v0.1.1)

Claude Code only. Delivered:

- Top-bar indicator with animated sprite and configurable labels.
- Popover home: companion card, today / week / month usage and cost, daily chart, official limits
  with pace, countdown and forecast.
- Companion: egg, hatch, evolve, graduate; Rare Candy accrual.
- Pokédex: grid, filters, search, species detail, representative pin, catch log, region badges.
- Notifications, libadwaita preferences, Go helper with disk-cached PokéAPI data and sprites,
  Fedora RPM.

## Milestone 2 delivered (v0.2.0)

Claude Code only. Delivered:

- Shop and Bag tabs: Mint, Rare Candy, Shiny Charm, premium eggs, with the shop-prices setting.
- Usage recap (week, month, year) and the 16-week heatmap, with the Activity click-cycle.
- Floating desktop pet: drag, hover callout, petting hearts, limit-alert and event bubbles.
- Agent activity over D-Bus (`io.github.poketokenbar.Agent`), fed by user-installed Claude Code hooks.
- Indicator polish: Ctrl+click menu, power-save pause, hatch/evolve flash.

## Milestone 3 delivered (v0.3.0)

Claude Code only. Delivered:

- Item sprites in the Shop and Bag cards (`getSprite {item}`, fetched on demand).
- Prayers: left-click the egg pet to pray (wobble, 🙏 particles, quips, prayer count carried into the
  hatched Pokémon and the hatch text).
- Ditto disguise: the 1/128 roll and the reveal at the growth threshold, with a `dittoReveal` event,
  a notice and a card effect.
- Save export and import (`docs/spec/save-transfer.md`): schema 2 envelope, macOS envelope exports
  import, a mandatory pre-import backup (keep 5), automatic snapshots every 12 h (keep 10) plus
  manual ones, restore with a safety snapshot.
- *Backup & Transfer* preferences page, reaching the helper through the extension's `Save1` D-Bus
  relay (paths only, never save contents).
- Pokédex completion medals were already delivered in Milestone 1; M3 only added tests and docs.

Plan and history: `tasks/todo.md`.

## Not yet ported

Ordered by value, mirroring the parity backlog in `tasks/todo.md`:

1. Passive agent detection (from session logs, without hooks).
2. Additional providers (Codex, Gemini CLI, OpenCode, Cursor, others).
3. Multi-account Claude.
4. Session-key limits.
5. Translations beyond English.
6. A GNOME-to-macOS import has not been tried on a real Mac.

## Deliberate deviations from upstream

- Evolution lines and species are cached on disk. Upstream keeps them in memory only, so nothing evolves after an offline restart.
- The Claude OAuth token is never refreshed. Refreshing rotates the refresh token and logs Claude Code out. The helper only re-reads `$CLAUDE_CONFIG_DIR/.credentials.json`.
- The `cost-state` ledger attribution is deferred. Upstream probably double-counts subagent cost. M1 uses table estimates only; unknown models show as unpriced.
- Paths follow XDG: state in `$XDG_STATE_HOME/poketokenbar/`, caches in `$XDG_CACHE_HOME/poketokenbar/`; `PTB_STATE_DIR` overrides both.
- A state-dir lock in the helper replaces upstream's single-instance check.
- Milestone 1 did not import macOS saves. Milestone 3 supersedes that for envelope exports only (protocol §14 decision 44): a save exported by the macOS app imports, a bare `companion-state.json` copied from macOS does not.
