# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
