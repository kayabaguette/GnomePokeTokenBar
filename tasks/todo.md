# PokeTokenBar-GNOME — Milestone 1 plan

Port of the macOS app [PokeTokenBar](https://github.com/chattymin/PokeTokenBar) (Swift, baseline
commit `38e3e55` / v2.5.7) to **Fedora / GNOME Shell 51 (Wayland)** as a GNOME Shell extension.
Full parity with the macOS app is the end goal; this file covers **Milestone 1**.

Status: **approved 2026-11-18** — Phase 0 in progress.

## Decisions (user, 2026-11-18)

| Topic | Decision |
|---|---|
| M1 scope | Core + companion: top-bar indicator, popover home (usage, chart, official limits), companion (egg → hatch → evolve → graduate), Pokédex |
| Repo | New separate repo: `~/Documents/01_Projects/PokeTokenBar-GNOME` |
| Backend | Go helper process; extension in GJS renders only |
| Providers | Claude Code only in M1; provider abstraction stays generic |

## Assumptions (stated, not asked)

- Local git repo, **no remote** until the user decides. Feature work on `feature/<name>` branches.
- Extension uuid `poketokenbar@local`, local install only. extensions.gnome.org is out of scope,
  because EGO rejects bundled binaries. `shell-version: ["51"]` only, the one version we can test.
- The Swift repo is a **read-only reference**. Nothing in `~/Documents/01_Projects/PokeTokenBar` is modified.
- No import of macOS saves. Save field names stay identical to upstream, so a later import of a macOS
  *export* stays cheap.
- macOS-only pieces are dropped: login item, Keychain, update checker, Homebrew, App Nap.
  Single-instance becomes a **state-dir lock** in the helper.
- English UI, with every string wrapped for gettext from day one. Pokémon names follow the session
  locale and fall back to English.
- Pokémon data and sprites are fetched at runtime from PokéAPI, as upstream does, and no Nintendo assets
  are bundled. **Deviation:** evolution lines and species are cached on disk (upstream keeps them in
  memory only, so after an offline restart nothing evolves).
- **Never refresh the Claude OAuth token.** Refreshing rotates the refresh token and logs Claude Code
  out (upstream `docs/reference/defect-log.md:727-748`). The helper only re-reads
  `$CLAUDE_CONFIG_DIR/.credentials.json`.
- Cost: prices for models missing from the upstream table (`claude-mythos-5-1`, `claude-mythos-5-2`,
  `claude-haiku-5-5`) come from the `claude-api` skill, not memory. Unknown models show as unpriced
  (`$—`). The upstream `cost-state` ledger attribution is **deferred**: upstream probably double-counts
  subagent cost (spec `usage.md` §3.5). M1 uses table estimates only, documented in `SPEC.md`.
- Ditto disguise, prayers and Rare Candy *use* are deferred. Candies **accrue** in M1 but can't be
  spent until the Bag ships.
- Dockerfile is **build-only**: it compiles the helper and packs the extension zip. A runtime container
  makes no sense for code that runs inside gnome-shell.

## Architecture

```
gnome-shell process                        child process (nice 10)
┌──────────────────────────────┐  stdin   ┌──────────────────────────────────────┐
│ extension/ (GJS, ESM)        │ ───────▶ │ helper/ ptb-helper (Go, stdlib only) │
│  indicator, popover, dex,    │  JSON    │  usage scan + incremental cache      │
│  notifications, prefs (Adw)  │  lines   │  official limits (OAuth, read-only)  │
│  renders state, sends cmds   │ ◀─────── │  companion game + save (single writer)│
└──────────────────────────────┘  stdout  │  PokéAPI + sprites (GIF → PNG frames)│
                                          └──────────────────────────────────────┘
```

- **Why a helper:** the extension runs on the compositor's main thread, and GJS has no worker threads.
  Parsing about 470 MB of JSONL there would freeze the whole Wayland session.
- **Transport:** the extension spawns the helper with `Gio.Subprocess` and talks newline-delimited JSON.
  The periodic `state` message stays a few KB. Pokédex pages and species details are served on request.
- **Lifecycle:** the shell disables extensions on every screen lock, so the helper is killed and
  restarted routinely.
  - On start, the helper takes an `flock` on the state dir and emits the last persisted state at once,
    then scans.
  - It flushes and exits on stdin EOF.
  - The per-provider credit ledger makes restarts idempotent, so it must be ported exactly.
- **Settings:** GSettings holds UI preferences only and the extension forwards them to the helper. The
  helper persists the last-applied growth difficulty and does the progress rescale itself.
- **Paths (XDG):**
  - save and state: `$XDG_STATE_HOME/poketokenbar/`;
  - usage cache, PokéAPI cache and sprites: `$XDG_CACHE_HOME/poketokenbar/`;
  - `PTB_STATE_DIR` overrides both, for the nested dev shell and for tests.
- **Save format:** upstream's full field set and key names from the very first save, including the
  deferred features (Ditto, prayers, egg tier, inventory, shop ledger). Add `schemaVersion` and use
  ISO-8601 dates throughout.

Specs extracted from the Swift source, with file:line references:
- `docs/spec/usage.md`: Claude Code usage pipeline and official limits.
- `docs/spec/companion.md`: game rules, save model, RNG draw order, PokéAPI, sprites.
- `docs/spec/ui.md`: screens, settings, notifications, colors. Read the architecture override at its top.

## Phases

Legend: `[sub:<model>]` = delegated subagent and its model. `[user]` = needs a user action.

### Phase 0 — Bootstrap and spikes  `[sub:sonnet]` + review
- [x] 0.1–0.3 ✅ 2026-11-18. Verified by the orchestrator: `make build/test/lint` pass, `serve` does
  the ping/hello round trip and exits 0 on EOF, and `podman build --output` exports the zip. The
  extension has not been loaded in a shell yet.
- [ ] 0.1 `git init`.
  - Add LICENSE (MIT, keeping `Copyright (c) 2026 chattymin` plus a line for the port), a README stub
    with the fan-project disclaimer, CHANGELOG.md, SPEC.md (an index over `docs/spec/`) and
    `.gitignore` (`bin/`, `*.zip`, `.env`, …).
  - Makefile targets: `build test lint install uninstall pack run-nested clean`.
  - Build-only Dockerfile.
- [ ] 0.2 `helper/` Go module (stdlib only) with `cmd/ptb-helper` (`--version`, `serve`, `snapshot --json`).
- [ ] 0.3 `extension/` skeleton.
  - `metadata.json`, `extension.js`, a panel button and a helper client.
  - Handshake over JSON lines. On disable, closing stdin makes the helper exit.
- [ ] 0.4 **Spike A: animated sprite.** The helper decodes one Gen-V GIF with full disposal and
  transparency compositing into PNG frames plus a manifest. The extension cycles the preloaded frames on
  a fixed-size actor, with the scale factor respected and nearest-neighbour scaling.
  - **Exit criteria:** smooth at the 0.4 s floor (Power saver); no relayout per frame; gnome-shell idle
    CPU delta measured and acceptable (target < 1 %).
  - `[user]` reload the shell: nested devkit, or log out and back in.
- [x] 0.5 **Spike B: limits endpoint.** ✅ 2026-11-18: HTTP 200 with the Go client and the
  `poketokenbar-gnome/0.0.1` UA. Both `five_hour` and `seven_day` read 16 %, `limits[]` has 3 entries
  (session, weekly_all, weekly_scoped), and the response has ~34 top-level keys, so decoding must stay
  tolerant. Utilization looks consistent with the local volume (~60M tokens over 5 h), but that rests
  on a single sample. Code: `spikes/oauth-usage/`. One live `GET https://api.anthropic.com/api/oauth/usage` from
  Go, using the token in `~/.claude/.credentials.json`. Token values are never logged or printed.
  - Confirms that the Go User-Agent is accepted and that utilization tracks this account.
  - On failure, Phase 2 moves after Phase 3.
  - 2026-11-18 status:
    - Done, verified in a headless shell: Go compositing (4-frame disposal test, frames checked
      visually); tick rate OK; no per-frame relayout.
    - Rendering approach: `St.ImageContent` + `set_bytes(cogl, …, RGBA_8888, …)`, then `set_content`
      per tick on a fixed-size actor.
    - **Still to do:** live CPU delta, crispness and alignment (user).
    - **Incident:** the headless run unmounted the live document portal; see `tasks/lessons.md`.
    - **Follow-ups for Phase 4/6:**
      - Load hitch of 62–108 ms per enable from synchronous PNG decode. The helper should write raw
        RGBA, read via `load_contents_async`.
      - The sprite box size and the min filter need deciding after the live look test.
      - `make run-nested` needs the portal warning and check.
- [x] 0.6 ✅ 2026-11-18. `docs/protocol.md` (913 lines). Fixtures are `state-egg.json`,
  `state-active.json`, `dex-page.json`, `species.json`, `catch-log.json` and `events.ndjson`, all
  ≤ 8 KB. §14 of the doc lists the deliberate choices: alert tiers persisted, forecast per provider,
  helper-computed `paceDelta`, week start resolved by the extension.
  Orchestrator rulings on the open questions:
  - (a) The skeleton aligns with the contract in Phase 5: `hello` carries `helperVersion` and `pid`,
    and an over-long line is skipped rather than fatal.
  - (b) Accent-insensitive search uses a small Latin fold table; Go stays stdlib only.
  - (c) **Sprite frames switch to raw RGBA**: one `frames.rgba` blob plus offsets in `manifest.json`,
    read with `load_contents_async`. This removes the 62–108 ms PNG decode hitch measured in Spike A.
    Phase 4 updates protocol §8.2.
- [ ] 0.6 Protocol contract.
  - `docs/protocol.md` defines the message types, commands and events.
  - `extension/test/fixtures/state-sample.json` lets UI work proceed in parallel with the Go phases.
- [ ] **Gate:** review the spike results with the user, then adjust the plan.
  - **2026-11-18 change:**
    - The user asked to be told "when the RPM is ready" and has not run the live CPU test.
    - The CPU gate therefore **moves to the first RPM install**, and the `measure-cpu.sh` step goes in
      the ready notification.
    - Headless CPU numbers don't count. The animator stays in one module, so a failed gate changes a
      default rather than forcing a rewrite.
  - Sandbox: `scripts/headless-shell.sh` is the only authorized shell launcher (see `tasks/lessons.md`).
    - Verified with the skeleton: enable → disable → enable works, with no orphan helper and no JS
      errors.
    - Verified with the **RPM payload** through `XDG_DATA_DIRS`: the helper is spawned via the relative
      `bin/ptb-helper` symlink.
  - Meaning of "ready": phases 1–6 integrated, with `VERSION` bumped so `dnf` upgrades over 0.0.1.

### Phase 1 — Go usage core: Claude Code  `[sub:opus]`
✅ 2026-11-18 in `helper/internal/{usage,claude}`, `cmd/ptb-usage-debug` and `scripts/oracle_usage.py`.
- About 60 Swift tests were ported, with fault injection on the dedupe, the watermark, the inode and
  the DST guards.
- The orchestrator re-checked against a **frozen copy** of today's logs: Go = oracle = 215,612,450
  tokens, with all five buckets equal.
- Timings: cold scan 0.25 s, warm 0.04 s.

Carry-overs for Phase 5:
- Build "today" from a single scan, so the `FetchDaily` and enrichment values can't diverge
  (protocol §5.2 invariant).
- `Coverage` can't be decoded, so persist the inputs rather than the encoded coverage.
- There is no shell-env fallback for `CLAUDE_CONFIG_DIR`. That is fine here, because the value equals
  the default.

Price note:
- Haiku 5.5 is priced ×5 above 100K prompt tokens, per the `claude-api` skill.
- The Mythos 5.1 cache-write price is inferred from the 1.25× rule.
- [ ] Roots:
  - `CLAUDE_CONFIG_DIR` (comma-split), `$XDG_CONFIG_HOME/claude`, `~/.config/claude`, `~/.claude`;
  - dedupe after `EvalSymlinks`, case-sensitive;
  - `subagents/` files included.
- [ ] JSONL parser:
  - byte prefilter;
  - number clamping;
  - ISO-8601 variants;
  - session id from the path;
  - `dedupKeepMax` with exact semantics: keep the max total, the earliest date, the union of
    sessions; dedupe globally.
- [ ] Pricing table plus local models (prices from the `claude-api` skill); unpriced models show `$—`.
- [ ] Time bucketing:
  - local day, week start (setting, default from locale), month;
  - 21-day rolling and month series (calendar-day stepping, DST-safe);
  - rolling 5 h block, tokens/min, burn tier, forecast to 100 %.
- [ ] Incremental cache:
  - per file: mtime (ns), size, **inode**, offset, entries;
  - append watermark; rotation detected by inode;
  - parser version; prune after 40 days; zlib JSON, written atomically.
- [ ] Daily history ledger (per-day max merge, prune before Jan 1 of the previous year), so history
  accrues from day 1.
- [ ] Tests:
  - ported groups from `docs/spec/usage.md` §10;
  - an **independent oracle script** comparing today's totals on the real logs.

### Phase 2 — Go official limits  `[sub:sonnet]` (after 0.5 passes)
✅ 2026-11-18 in `helper/internal/limits/`: 41 tests, 96.9 % coverage, fault injection done.
Orchestrator checks:
- no refresh endpoint is called anywhere;
- error strings carry only the code and HTTP status, never the token or the body;
- no live URL appears in tests.

Notes for later phases:
- A missing or invalid `expiresAt` counts as never expired, as Swift does.
- `limitAlert.label` is null for the legacy windows. **Phase 6 must label them from `windowKind`.**
- Swift has no `tokenExpired` code; we follow the protocol.
- [ ] Credentials: read the file on every poll; `expiresAt` in s or ms, expired if ≤ now + 60 s; never refresh.
- [ ] Usage endpoint:
  - `GET oauth/usage`; on 401/403, re-read the file and retry once;
  - 429 backoff 300 → 3600 s honouring Retry-After;
  - a manual refresh bypasses the backoff.
- [ ] `LimitStatus` with `five_hour`, `seven_day`, opus, sonnet and `limits[]`; null five_hour on the Ultra plan.
- [ ] Display rules: pace fraction; PaceTier (6 tiers); used or remaining; limits stale after 15 min.
- [ ] Alert tiers: warn 80 / crit 95 by default, edge-triggered per window.
- [ ] Rare Candy grant evaluation:
  - windows `claude.fiveHour` and `claude.sevenDay`;
  - first-run seeding;
  - `isNewEpoch` jitter rule (≥ 30 min for session, ≥ 24 h for weekly);
  - 1 candy per session cap, 5 per weekly cap.
- [ ] Tests ported from `RareCandyTests` and the limit-format parts of `SessionKeyLimitsTests`.

### Phase 3 — Go companion  `[sub:opus]` (parallel with 1, 2 and 4)
✅ 2026-11-18 in `helper/internal/companion/`: 151 tests, 99.9 % coverage, fault injection on the
rollover, the rounding chain and the draw order.

Orchestrator checks:
- all 28 upstream save keys are present;
- the balance constants match the spec;
- Ditto is off by default.

Carry-overs for Phase 5:
- Call `Update` after `networkUp`.
- Fill `CandyWindow.ProviderID`.
- The adapter keeps only `level-up` b2w2 moves for `Details`.
- `getSpecies` returns individuals of every form; the extension filters by form.

Accepted deviations:
- shiny odds fixed at 1/64 until the Charm ships;
- legacy `unownForm` keys are not read;
- the name sort uses folded strings.
- [ ] Save model:
  - upstream field names, `schemaVersion`, ISO dates;
  - lenient decode plus `sanitized()` clamps;
  - atomic write;
  - `.corrupt` copy with snapshot recovery;
  - snapshots every 12 h (max 10).
- [ ] Per-provider credit ledger, exactly as spec §2.2: baseline, rollover, regression rebase, late provider.
- [ ] Thresholds:
  - egg 5M, T per rarity, phase cost with rounding chain std → boost → difficulty;
  - difficulty clamp 0.1–2.0;
  - rescale that never completes a stage.
- [ ] Hatch:
  - weighted pool by capture rate (halved once collected);
  - draw order: species → shiny 1/64 → nature → [Ditto: disabled flag] → plan → profile seed → form;
  - `Rng` interface; SplitMix64 in tests.
- [ ] Evolution:
  - plan with a preference for uncollected finals;
  - one draw per non-leaf node;
  - excess carries over;
  - forms carried through the line (`lineBaseIDs`).
- [ ] Graduation and dex:
  - DexEntry; `collectedFinals`; excess discarded;
  - species cells; catch log; search;
  - regions and badge keys (persisted, never renamed);
  - representative pin and reconcile.
- [ ] Profile: IVs, enrichment (gender, ability, b2w2 moves), level from growth, stats.
- [ ] Display state: mood (egg, levelUp, tired, sulk, sleep, idle, working, focus) and growth text.
- [ ] Tests: the groups in `docs/spec/companion.md` §12, with exact expected values.

### Phase 4 — Go PokéAPI and sprites  `[sub:sonnet]` (parallel)
✅ 2026-11-18 in `helper/internal/{pokeapi,sprites}/`: 38 tests, 92 % / 90 % coverage, fault
injection done. The live smoke test took 7 requests and found 328 base species (Ditto excluded).
That settles the spec's "329 vs 328": the right number is 328. Protocol §8.2 was updated to the
`frames.rgba` format.

**Carry-overs for Phase 5:**
- Back-view fallback. `Store.Get` with `Back` returns `ErrUnavailable` when the back sprite fails
  transiently and isn't cached, even if the front is. Upstream falls back to the front.
  - The daemon retries with `Back=false` and schedules a recovery with `RecoveryDelay`.
- Protocol §8.1 still says `manifestPath` for statics. Statics are plain `sprites/<key>.png`; return
  `Result.Path` and fix the §8.1 wording.
- `Details` keeps every b2w2 learn method. The companion adapter keeps only `level-up` moves for
  profile enrichment.
- A 1-frame GIF is not retried forever. This deviates from upstream, and the deviation is accepted.
- [ ] PokéAPI client:
  - GraphQL base index with REST self-heal;
  - species, chain (SSRF guard: https and host `pokeapi.co` only), details, names;
  - **disk** cache with a 30-day TTL and stale fallback; in-flight dedupe.
- [ ] Sprite URL builder: asset and static asset, back, shiny, forms, items, egg crop.
- [ ] Download: requires 200 and a non-empty body; atomic cache writes.
- [ ] Fallbacks: shiny → plain; animated → static; back → front after a 404 only.
- [ ] Recovery loop: backoff 2 → 30 s.
- [ ] GIF pipeline from Spike A:
  - composite frames, honouring disposal None, Background and Previous;
  - delays: below 0.02 s → 0.1 s;
  - a 1-frame GIF counts as "not animated".
- [ ] Tests:
  - URL and cache-key cases (`25-a`, `25-sha`, `492-sky-s`, `10006.png`…);
  - SSRF cases;
  - a GIF disposal golden test.

### Phase 5 — Go daemon and protocol  `[sub:sonnet]`
- [ ] `serve` startup: state-dir lock, then emit the persisted state, then scan.
- [ ] Scheduler:
  - interval presets 0/60/120/300/900 s;
  - local-midnight timer;
  - coalesced refresh;
  - empty-retry after 20 s.
- [ ] Commands:
  - `refresh`, `setSettings`, `setRepresentative`, `getDexPage`, `getSpecies`, `getCatchLog`, `getSprite`;
  - `wake` and `networkUp`, both sent by the extension.
- [ ] Events: hatch, evolve, graduate, candy, badge, limitAlert.
- [ ] Shutdown and logging: stdin EOF → flush and exit; logs to stderr, without token values.

### Phase 6 — Extension: indicator and popover home  `[sub:opus + frontend-design skill]`
✅ 2026-11-18. 57 JS unit tests pass under `gjs -m`. Screenshots work in the sandbox through an
in-process test extension (`extension/test/shot-extension/`).

Orchestrator acceptance run (stub helper, `--cycle`):
- `ISOLATION OK`;
- helper count 1 while enabled, 0 while disabled;
- 0 `JS ERROR`, 0 `CRITICAL`.

Sprite defaults: a 22 px box capped at 40 px wide, TRILINEAR for minification, NEAREST only for
integer upscaling.

Accepted deviations:
- Week start falls back to GNOME's setting, then the locale, then Monday. `Intl` week info is absent
  in GJS 1.90.
- `refresh-interval` is a 0–900 range.
- Auth-expiry and candy notifications use LOW urgency.

Later:
- Ctrl+click opens the menu.
- Pause while the display sleeps.
- Hatch/evolve celebration flash.
- The notification source is destroyed on disable, so notifications vanish at screen lock. Accepted
  for M1.

**Isolation false positive fixed.** A rootless podman build from another session recreated
`/run/user/1000/crun` mid-run. Changes to container-runtime entries are now a WARN. Any other entry
change, or any **mount** change under the live runtime dir, is still a FAIL; the mount check would
have caught the original portal incident.
- [ ] Helper client:
  - **Fix from the Phase 0 review:** `send()` currently does a synchronous `write_all` on the stdin
    pipe from the compositor thread. A stuck helper with a full pipe would freeze the session.
    Replace it with a FIFO queue drained by `write_all_async`, one write in flight at a time.
  - Gio.Subprocess with an async line reader;
  - restart with backoff;
  - forwards GSettings.
- [ ] Indicator:
  - sprite animator (from Spike A);
  - 1–2 line labels (tokens, cost, limit %, growth %), pace colors;
  - right-click menu.
- [ ] Popover home:
  - companion card: sprite, rarity and nature badges, progress bar, evolution line;
  - today: big number and cost; week and month;
  - **daily bar chart** (Cairo, periods 7/14/21 days or the month, hover tooltip);
  - **limit rows** (Cairo bar with pace marker, reset countdown);
  - footer: refresh, settings, stale indicator.
- [ ] Shared hover-tooltip helper. Notifications through MessageTray (critical limit → HIGH urgency).
- [ ] Pure JS logic unit-tested with `gjs -m`: formatters, fps decimation, axis labels, pace colors.

### Phase 7 — Extension: Pokédex and prefs  `[sub:opus]`
- [ ] Pokédex grid:
  - 4×4 paged, with scroll-wheel paging;
  - search `St.Entry` (focus quirks inside a menu);
  - rarity, region and shiny filters; sort.
- [ ] Species detail: individuals, forms, profile and stats, **Set as representative**.
- [ ] Catch log (paged); region badges.
- [ ] `prefs.js` (Adw), with the GSettings schema:
  - refresh interval, menu-bar items, animation quality;
  - used or remaining, warn/crit thresholds, chart period;
  - growth difficulty, notifications, week start.

### Phase 8 — Integration and delivery  `[sub:sonnet]` + review
- [ ] End-to-end run in the nested shell and in the real session.
  - Compare today's totals with the oracle.
  - Measure CPU with the extension idle, while scanning and with the popover open.
- [ ] Docs: README (install, architecture, screenshots), CHANGELOG, final `SPEC.md`, `tasks/lessons.md`.
- [ ] `make pack` → `poketokenbar@local.zip`.
- [ ] `make rpm` → `gnome-shell-extension-poketokenbar` (requested by the user 2026-11-18). `[sub:sonnet]`
  - rpmbuild builds from source.
  - The extension goes in `/usr/share/gnome-shell/extensions/poketokenbar@local/`.
  - The helper goes in `/usr/libexec/poketokenbar/`, reached through a `bin/ptb-helper` symlink.
  - No scriptlets.

## Parity backlog (M2+, ordered by value)

1. Bag and Rare Candy use (bulk plan).
2. Shop: Mint, Shiny Charm, premium eggs.
3. Floating desktop pet.
4. Additional providers, each with verified Linux paths: Codex, Gemini CLI, OpenCode, Cursor
   (pure-Go SQLite), and the rest.
5. Usage recap.
6. Activity heatmap and streak badge.
7. Multi-account Claude.
8. Session-key limits (Cloudflare risk with Go TLS).
9. Ditto disguise.
10. Prayers.
11. Save export and import.
12. 8-language translations (generated from the Swift `t(ko,en,…)` tuples into `.po`).

## Validation (every phase)

- `go vet ./...` and `go test -race ./...`.
- New branches must be actually executed: run `go test -coverprofile` and inspect the uncovered
  blocks. A coverage percentage alone is not evidence.
- When a test was ported from Swift, keep the Swift `file:line` in a comment. The Swift assertions are
  the only oracle, since Swift is not installed here.
- The orchestrator reviews every subagent result before reporting anything as done.

## Risks and open points

| Risk | Mitigation |
|---|---|
| GIF animation CPU in the shell | Spike A gate; decimated fps; pause while hidden |
| `oauth/usage` is undocumented, and the Go UA is unverified | Spike B; limits degrade gracefully |
| Token expires (~5–7 h) and must never be refreshed | Limits go stale while Claude Code is idle; show a stale state, as upstream does |
| PokéAPI GraphQL endpoint is `v1beta2` | REST self-heal; disk cache with stale fallback |
| Reloading the extension on Wayland needs a logout | `[user]` `sudo dnf install mutter-devkit`, then `make run-nested` |

## User actions needed

- [ ] Approve this plan.
- [ ] Optional but strongly recommended: `sudo dnf install mutter-devkit` for nested-shell testing.
  Without it, every extension reload needs a logout.
- [ ] After bootstrap: open a Claude Code session in `~/Documents/01_Projects/PokeTokenBar-GNOME`, or
  add it to this session.

## Results

_(filled in as phases complete)_
