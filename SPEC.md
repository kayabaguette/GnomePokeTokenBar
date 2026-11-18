# SPEC

Index of the specifications for PokeTokenBar-GNOME.

- [Usage pipeline and official limits](docs/spec/usage.md)
- [Companion game rules, save model, PokéAPI](docs/spec/companion.md)
- [UI, settings, notifications](docs/spec/ui.md)
- [Helper/extension protocol](docs/protocol.md)

Upstream baseline: PokeTokenBar commit `38e3e55` (v2.5.7).

## Milestone 1 scope

Claude Code only. Top-bar indicator, popover home (usage, chart, official limits), companion
(egg, hatch, evolve, graduate) and Pokédex. Plan: `tasks/todo.md`.

## Deliberate deviations from upstream

- Evolution lines and species are cached on disk. Upstream keeps them in memory only, so nothing evolves after an offline restart.
- The Claude OAuth token is never refreshed. Refreshing rotates the refresh token and logs Claude Code out. The helper only re-reads `$CLAUDE_CONFIG_DIR/.credentials.json`.
- The `cost-state` ledger attribution is deferred. Upstream probably double-counts subagent cost. M1 uses table estimates only; unknown models show as unpriced.
- Paths follow XDG: state in `$XDG_STATE_HOME/poketokenbar/`, caches in `$XDG_CACHE_HOME/poketokenbar/`; `PTB_STATE_DIR` overrides both.
- A state-dir lock in the helper replaces upstream's single-instance check.
- No import of macOS saves. Save field names stay identical to upstream so a later import stays cheap.
