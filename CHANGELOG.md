# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
