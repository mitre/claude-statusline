# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

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
