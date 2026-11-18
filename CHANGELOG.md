# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `make rpm`: builds an installable Fedora RPM (`packaging/gnome-shell-extension-poketokenbar.spec`) from the
  working tree; the helper is installed in `/usr/libexec/poketokenbar/` and symlinked into the extension.
- Project skeleton: Go helper (`ptb-helper`: `--version`, `serve` NDJSON handshake and ping,
  `snapshot --json` placeholder, `internal/ipc` line reader/writer), GNOME Shell 51 extension
  skeleton (panel button and helper client), Makefile, build-only Dockerfile, LICENSE, README, SPEC index.
- `helper/internal/limits`: Claude official limits.
  - Reads `$CLAUDE_CONFIG_DIR/.credentials.json` or `~/.claude/.credentials.json`. The token is
    never refreshed.
  - Calls `oauth/usage`; on 401/403 it retries once if the token changed; on 429 it backs off from
    300 s to 3600 s.
  - Decodes the response tolerantly.
  - Computes pace tiers, edge-triggered alerts and Rare Candy windows.
- `helper/internal/usage` and `helper/internal/claude`: Claude Code usage core.
  - Roots: `CLAUDE_CONFIG_DIR`, XDG, `~/.config/claude`, `~/.claude`.
  - JSONL parser with upstream keep-max dedupe.
  - Pricing table, with prices for Mythos 5.1/5.2 and Haiku 5.5 taken from the `claude-api` reference.
  - Local-day, week and month bucketing; 21-day and month series; rolling 5 h block; burn tier;
    5 h forecast; daily history ledger and streak.
  - Incremental zlib JSON cache keyed by path, mtime, size and inode.
  - Totals match an independent Python oracle (`scripts/oracle_usage.py`) token for token.
- `helper/cmd/ptb-usage-debug`: prints today, week, month and series totals as JSON.
- `helper/internal/pokeapi`: PokéAPI client.
  - Base index via GraphQL, with a self-healing fallback to REST.
  - Species, evolution chains (SSRF-guarded), details and localized names.
  - Everything is cached on disk with a 30-day TTL and a stale fallback.
- `helper/internal/sprites`: sprite store.
  - Builds URLs and cache keys, including form-aware ones.
  - Fallbacks: shiny to plain, animated to static, and back to front only after a 404.
  - Composites GIFs into a raw RGBA `frames.rgba` blob plus a manifest; crops the egg sprite.

### Changed

- Sprite frames are stored as one raw RGBA blob instead of per-frame PNGs.
  - Why: Spike A measured a 62–108 ms compositor stall per enable, caused by synchronous PNG decoding.
  - Effect: the extension can now read the blob asynchronously.
