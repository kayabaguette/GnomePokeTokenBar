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

Plan and history: `tasks/todo.md`.

## Not yet ported

Ordered by value, mirroring the parity backlog in `tasks/todo.md`:

1. Item sprites in the Shop and Bag (emoji today).
2. Prayers.
3. Ditto disguise reveal.
4. Passive agent detection (from session logs, without hooks).
5. Additional providers (Codex, Gemini CLI, OpenCode, Cursor, others).
6. Multi-account Claude.
7. Session-key limits.
8. Save export and import.
9. Translations beyond English.

## Deliberate deviations from upstream

- Evolution lines and species are cached on disk. Upstream keeps them in memory only, so nothing evolves after an offline restart.
- The Claude OAuth token is never refreshed. Refreshing rotates the refresh token and logs Claude Code out. The helper only re-reads `$CLAUDE_CONFIG_DIR/.credentials.json`.
- The `cost-state` ledger attribution is deferred. Upstream probably double-counts subagent cost. M1 uses table estimates only; unknown models show as unpriced.
- Paths follow XDG: state in `$XDG_STATE_HOME/poketokenbar/`, caches in `$XDG_CACHE_HOME/poketokenbar/`; `PTB_STATE_DIR` overrides both.
- A state-dir lock in the helper replaces upstream's single-instance check.
- No import of macOS saves. Save field names stay identical to upstream so a later import stays cheap.
