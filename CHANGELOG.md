# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

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
