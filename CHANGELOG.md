# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Scoped plan limits on the account row**: a plan limit the payload narrows
  to a scope — today a model — now renders as its own meter beside `5h` and
  `week`, named by the payload and carrying its own reset time (e.g.
  `Fable 100% (resets Mon 10:00a)`). Previously this limit was invisible: an
  exhausted model-specific weekly allowance could only be inferred from the
  alarm it triggered.
- **`[model] extra_budget_dollars`** (default `5.0`): extra-usage spend you
  accept before the badge alarms. Below it the badge is a dim `· extra $2.42`
  tally; at or above it the red `EXTRA USAGE` alarm shows. `0` alarms on any
  spend. The comparison happens in the payload's own minor units, so no
  floating-point rounding decides whether the alarm fires.

### Fixed

- **The extra-usage alarm no longer shouts about money that was never spent.**
  It fired whenever any active plan limit hit 100%, so an exhausted
  model-scoped allowance rendered as `⚠ EXTRA USAGE $0.00` — a billing alarm
  with nothing billed. Spend is now the only trigger; limit state is carried
  by the account meters, which turn red at 100% on their own.
- **The session's weekly model window is no longer matched against a
  compiled-in model list.** The window key is derived from the payload's own
  `seven_day_*` keys, so a model the vendor ships tomorrow is picked up with
  no code change; previously anything outside `opus`/`sonnet`/`haiku` showed
  no window even when the payload carried one. Matching normalizes both sides
  and prefers the more specific key, deterministically.
- **README corrected**: it stated that the Fable weekly limit is never
  exposed by the usage endpoint. It is — as a scoped entry in `limits[]`,
  which the new scoped meter renders.
- Standard MITRE publishing files: `LICENSE.md` aligned verbatim to the
  MITRE SAF license file, and the README now carries the `### NOTICE`
  section referencing `NOTICE.md` (Case Number 18-3678).

## [0.1.1] - 2026-07-16

### Added

- **Homebrew tap publishing**: every release renders its formula from the
  build's `checksums.txt` and pushes it to
  [mitre/homebrew-tap](https://github.com/mitre/homebrew-tap) —
  `brew install mitre/tap/claude-statusline` (macOS and Linux, all four
  architectures). Publishing authenticates via a short-lived GitHub App
  installation token minted per release run; the formula scripts are
  golden-tested, and goreleaser's deprecated `brews`/macOS-only
  `homebrew_casks` publishers are deliberately not used (see
  `.goreleaser.yaml` for the rationale).

- **Claude Code plugin**: the repo is its own marketplace
  (`/plugin marketplace add mitre/claude-statusline`). The setup command
  installs the release binary for your OS/arch with mandatory sha256
  verification against the release's `checksums.txt` (any mismatch refuses),
  backs up and edits `settings.json` only with explicit consent, and an
  uninstall command reverses both. A silent SessionStart hook keeps the
  installed binary version-pinned to the plugin — it never touches `dev`
  builds, so a developer's own HEAD build always wins.

## [0.1.0] - 2026-07-15

### Removed

- **The bash-compat contract is gone — this is a Go program, full stop.**
  The frozen reference script (`reference/statusline.sh`) is deleted (git
  history preserves it), cache files are no longer shared with or readable by
  the bash implementation, and every bash-ism written to honor that contract
  is removed: md5-of-cwd cache keys (now FNV-1a, deleting both gosec
  exclusions kept solely for the interop), command-substitution trailing-
  newline stripping in cache reads (now byte-exact), and the stale
  "first line only" branch-cache rule.

### Changed

- Default cache location moved from the world-shared
  `/tmp/.claude-statusline-cache` to the platform user-cache dir
  (`~/Library/Caches/claude-statusline` on macOS), auto-created on first
  render. Old `/tmp` caches are simply abandoned — caches are disposable
  and rebuild in one render.

- The `limits` row is now the **`account`** row — the label names the scope
  (all its meters are account-wide pools shared by every session). The
  `[rows] limits` config key remains honored as a deprecated alias.
- Reset times now show on **every** account meter by default;
  `[account] show_resets = "quiet"` restores the previous hot-only (≥80%)
  behavior (quiet ≠ never — hot windows still surface their reset time).
- The byte-parity gate against the bash reference is retired: this is the
  first intentional display divergence. `reference/statusline.sh` is frozen
  as the port-era spec; the Go golden tests are the display spec of record.

### Added

- **Live stdin fallback for the 5h/week meters**: Claude Code v2.1.210+
  carries `rate_limits` in the statusline payload, so when the usage
  endpoint is unreachable the two all-model meters render this render's
  live values instead of stale cache — and the account row now survives
  even with no cache at all (it used to collapse). Endpoint-only segments
  (per-model window, extra-usage badge) keep stale-good semantics, with
  the dim age marker scoped to stale data that is actually visible. Epoch
  reset moments share the same label formatting as the endpoint's RFC3339
  ones. The endpoint stays primary; hosts without `rate_limits` keep the
  previous behavior exactly.

- **goreleaser release pipeline + `--version` flag**: one artifact pipeline
  (darwin/linux × arm64/amd64 archives, `checksums.txt`, git changelog)
  replaces the hand-rolled cross-compile; `make snapshot` proves it locally
  without publishing, and a `v*` tag workflow runs the same make target on
  CI. `claude-statusline --version` reports goreleaser-injected build
  identity (`dev (none, unknown)` on plain `go build` — honest defaults),
  the seam the Homebrew formula test and the plugin's version-sync hook
  key off.

- **Session-signal segments from the stdin payload** (fields verified live in
  `docs/stdin-payload-inventory.md`): a dim reasoning-effort level and a
  yellow `⚡ fast` badge on the model row; the session's accumulated cost
  inside the `METERED BILLING` alarm (only while an API-key override is
  active — a zero/absent cost renders nothing); and a dim `>200k` marker on
  the context row once absolute tokens cross the long-context tier (distinct
  from the `/compact` pressure badge). Each segment has a config toggle
  (`[model] show_effort` / `show_fast_mode` / `show_metered_cost`,
  `[context] exceeds_200k_marker`), all defaulting on.

- **Cross-platform credential resolution** — the auth badge and usage
  meters now work on Linux, not just macOS. The subscription token resolves
  through Claude Code's own documented store precedence: the
  `.credentials.json` file when present (`$CLAUDE_CONFIG_DIR` honored, else
  `~/.claude/`) — which is the Linux/container store and the macOS fallback
  Claude Code itself honors — falling back to the macOS keychain item. An
  unusable file falls through rather than failing, mirroring Claude Code;
  no source at all degrades exactly as before (badge `?`, account row
  collapses). One parser serves both stores (identical `claudeAiOauth`
  JSON); the token is read per use, never cached, never logged.
- **Long-held `index.lock` badge on the project row** — when
  `.git/index.lock` has been held longer than `[project]
  lock_badge_after_s` (default 300 s; research found no established age
  convention in other tooling — git itself never expires the lock), the
  project row shows a yellow factual badge: `⚠ index.lock 14m`. Facts only,
  by design: a present lock is often legitimate (an editor-open `git
  commit` holds it for minutes), so there is no "stale" verdict, no removal
  command, and nothing is ever deleted. Detection is engine-independent and
  pure filesystem — an ancestor walk to the repository's git dir (following
  worktree/submodule `gitdir:` pointer files to the worktree's private git
  dir, where its own index.lock lives) plus one `stat`; no subprocess, no
  lock taken. `lock_badge = false` disables it. Motivated by a live
  incident: a concurrent session's SIGKILLed `git status` stranded a
  zero-byte lock that silently blocked commits.
- **Stale-data age marker on the account row** — when the usage fetch fails
  and the last known-good payload is served instead, the row now says so: a
  trailing dim `(data 6m old)` shows the served payload's factual age
  (minute granularity, in the activity row's compact unit style). The meters
  keep their true — old — values; nothing is zeroed or hidden, and the
  marker never uses alarm styling. It disappears on the first successful
  fetch; `[account] show_stale_age = false` hides it. Previously this state
  was indistinguishable from fresh data without reading cache file mtimes.
- **Account email on the account row** — the row now opens with the
  logged-in email as a scope label (regular weight; `email_style = "dim"` available) (`account dev@example.com · 5h 5% …`),
  naming whose pools the meters describe. Sourced from Claude Code's local
  state (`~/.claude.json` → `oauthAccount.emailAddress`; the statusline stdin
  payload carries no identity and the keychain holds tokens only), read fresh
  on every render — identity is deliberately never cached, so a login change
  shows immediately. Omitted entirely when unavailable, never fabricated;
  `[account] show_email = false` hides it.
- **lipgloss v2 styling engine** (`charm.land/lipgloss/v2`): all ANSI
  styling flows through composed styles instead of raw escape constants,
  opening the door to user-configurable theming. Output stays
  environment-independent by design (no tty detection; `NO_COLOR`
  deliberately not honored — the host renders the sequences), pinned by a
  test. Rendered bytes changed encoding only (`\x1b[m` resets, merged SGR
  params like `\x1b[1;36m`) — visually identical, same 16-color palette.
- **In-process git engine** (go-git): branch and dirty count are read without
  any subprocess by default — no git installation required. Repos whose
  in-process status overruns the `git_timeout_ms` budget are escalated
  per-repo (marker with daily re-probe) to the git CLI engine, which keeps
  the hard deadline and benefits from `core.fsmonitor` on huge worktrees —
  a one-shot statusline process cannot carry an abandoned in-process walk
  across renders, but the git binary's caches persist. `[project]
  git_engine = "auto" | "gogit" | "cli"` selects explicitly.
- **Per-model weekly meter** on the account row: `opus/wk 41%` — the rolling
  7-day pool for the session's model family (opus/sonnet/haiku), parsed from
  the payload's `seven_day_<family>` window. A parallel weekly cap, not a
  slice of `week`; omitted entirely when the payload carries no window for
  the session's model (never a fabricated 0%).
- **Bounded git subprocess latency** (Starship's `command_timeout` pattern):
  each git call runs under a configurable deadline — `[project]
  git_timeout_ms`, default 150 ms, `0` = unbounded — with the child process
  killed on expiry (`exec.CommandContext`), falling back to the cached
  branch/dirty values so a pathological worktree can never stall a render.
  Very large repos are pointed at git's own `core.fsmonitor` daemon.

- Go port of the bash statusline (`reference/statusline.sh`), verified
  **byte-identical** on the fixture corpus before any divergence; ~90 ms per
  render vs ~950 ms for the bash original
- Rows: `model` (name + context size, auth badge, short session id), `project`
  (tilde-shortened cwd, git branch, dirty count), `context` (10-segment bar),
  `limits` (subscription 5-hour / 7-day windows), `activity` (duration, churn)
- Alarm badges, loud only when abnormal: `/compact` at ≥85% context, reset
  times once a usage window runs hot (≥80%), `⚠ EXTRA USAGE` while credits are
  actively billing, `⚠ API KEY SET — METERED BILLING` on env-key overrides
- Optional TOML configuration (`~/.claude/statusline.toml`, overridable via
  `$CLAUDE_STATUSLINE_CONFIG`): per-row and per-segment toggles; zero config
  renders exactly the reference layout
- Shared cache compatibility with the bash reference (same files, same
  md5-of-cwd keys) — the binary and the script can be swapped freely
- Quality gates, all wired into `make check` and GitHub Actions CI:
  golangci-lint v2 (gosec included), govulncheck, race detector, 85% coverage
  floor, byte-parity harness against the reference script

### Fixed

- The account row lagged up to a full cache-TTL (3 minutes) behind usage-window
  resets: a payload fetched just before a boundary stayed "fresh" while showing
  pre-reset numbers with a reset time already in the past. A cached payload
  whose `resets_at` has passed — and whose fetch provably predates that reset —
  is now treated as expired regardless of TTL, so the row flips on the first
  render after the boundary. Post-boundary payloads still reporting a past
  reset (API lag) keep plain TTL cadence — no fetch storms.
- The statusline's `git status` took git's optional index lock on every
  render, intermittently racing the user's interactive git for
  `.git/index.lock` (observed live: a `git commit` failed while a concurrent
  session's render held the lock). Status now runs with
  `--no-optional-locks`, git's documented mechanism for background tooling.
  Inherited from the bash implementation.

Inherited from the bash implementation, found during the port, and pinned by
regression tests:

- Rate-limit error payloads from the usage endpoint were cached as if they were
  data and rendered as a fabricated `5h 0% · week 0%` — error bodies are now
  rejected by shape, never cached, and the last known-good payload is served
  when a fetch fails
- The dirty-file badge silently never rendered: the background refresh
  truncated the cache file at spawn, so same-render reads saw an empty file —
  all cache writes are now atomic (temp file + rename)
- Trailing newlines in bash-written cache files broke value comparisons in the
  port (`"Sub\n" != "Sub"` killed the auth badge and limits row) — cache reads
  now mirror bash command-substitution semantics

### Security

- Go toolchain bumped 1.26.4 → 1.26.5 for GO-2026-5856 (crypto/tls Encrypted
  Client Hello privacy leak, fixed upstream in 1.26.5). Our code reaches the
  affected path through the usage-endpoint HTTPS fetch; govulncheck flagged it
  in CI the day the advisory landed, exactly as the `make vuln` gate intends.

### Testing

- The real edges are now under test: `fetchUsage` against an in-process HTTP
  server (auth headers, credential-miss short circuit, 1 MiB body cap, client
  timeout), the keychain reader and git runner through PATH-shim executables
  (including the deadline actually killing a hung child). Root package
  coverage 63% → 89.5%, total 91.4% → 95.2%.
- Coverage gates ratcheted: the total floor rises 85% → 90% and a new
  per-package 85% floor stops a weak package hiding behind the average
  (`COVER_MIN` / `PKG_COVER_MIN` override both; a package without test files
  fails outright).
- The two untrusted-JSON parsers (stdin session payload, usage-endpoint
  bodies) now carry native Go fuzz targets with seed corpora covering the
  fixtures, rate-limit error bodies, and mangled/truncated variants. Every
  `go test` run replays the seeds as regression tests; `make fuzz` explores
  further (`FUZZTIME` overrides the default 30s per parser).
- `make bench` measures the in-process compute path (heaviest-frame
  `render.Build` and the full `run()` frame over fakes, per fixture) —
  observe-only guards for the ~90 ms wall budget, which itself includes
  real IO the benchmarks deliberately exclude.
