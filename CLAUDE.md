# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

A fast, configurable status line for Claude Code: a single static Go binary. Claude Code passes session JSON on stdin; the program prints ANSI rows on stdout and exits. That is the entire contract — no shell, no daemon, ~90 ms render budget.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:6cd5cc61 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->


## Build & Test

```bash
make check    # THE one gate: lint + vuln + race + cover + build — must pass before any commit/card close
make test     # unit tests (no network — all OS/network edges are injected)
make lint     # golangci-lint: config schema verify + full run (zero-issue gate)
make vuln     # govulncheck
make race     # full suite under the race detector
make cover    # coverage with an 85% floor (override: COVER_MIN=90 make cover)
make build    # local binary
make release  # cross-compile darwin/linux × arm64/amd64 into dist/

go test ./internal/render/                    # single package
go test -run TestName ./internal/gitinfo/     # single test
```

CI (`.github/workflows/ci.yml`) runs exactly the make targets above — never add CI-only shell; add a make target instead. golangci-lint is pinned (v2.11.3, config `version: "2"`) with gofumpt, gosec, revive, gocritic, misspell.

### Test-hygiene traps (each has bitten before)

- Any test that exercises `config.Default()` MUST sandbox HOME (`t.Setenv("HOME", t.TempDir())`) — `Default()` resolves the cache dir from the PROCESS env, so an unsandboxed test reads/writes the developer's REAL user cache (a fixture email once surfaced on the live statusline).
- E2E tests that append TOML to the config file MUST assert `errOut == ""` — a TOML parse error (e.g. a duplicate `[section]` header) silently falls back to defaults, which points at the real cache and tests the wrong path.
- Go's test cache legitimately serves `(cached) ok` after a mutation+reversal (byte-identical state); use `-count=1` to force execution.

## Live Binary — this repo IS the owner's statusline

The binary installed at `~/.claude/claude-statusline` is the owner's live status line, kept at a HEAD build. Every behavior change ends with the **live gate**: `make check` → back up the installed binary (`cp` to a timestamped `.backup-*` beside it) → atomic swap (`cp` to `.new`, then `mv`) → verify shas match → the owner eyeballs the result. Rollback = the previous backup binary. Live verification is non-negotiable — it has caught real bugs unit fakes cannot see (orphaned-pipe hangs, index-lock races, cache-boundary staleness, real-cache test pollution).

## Architecture

### Dependency injection at the edges — the load-bearing pattern

`run.go` defines a `deps` struct holding every OS/network/env touchpoint: `stdin`, `getenv`, `runGit`, `keychainOK`, `fetchUsage`, `readFile`. `main.go` wires the real implementations (keychain lookup, HTTP fetch to the OAuth usage endpoint, `exec.CommandContext` git); tests wire fakes. **Any new OS, network, or subprocess access must be added to `deps` and injected** — this is why `make test` needs no network and why `run_test.go` can golden-test full end-to-end renders.

### Render pipeline (one frame per invocation)

`run()` in `run.go`: `input.Parse` (stdin JSON) → `config.Load` (optional `~/.claude/statusline.toml`, `$CLAUDE_STATUSLINE_CONFIG` overrides path) → `auth.Detect` (Sub/API badge) → `gitinfo.Get` (branch/dirty/lock-age) → `usage.Resolve`/`Parse` + `account.Email` (Sub auth only) → `render.Build`.

### Packages

- `internal/render` — builds the ANSI rows via lipgloss v2. **The golden tests in this package ARE the display spec of record** (byte-parity with the original bash script ended 2026-07-03; the script lives in git history). Deliberately no tty detection and `NO_COLOR` is NOT honored — a statusline is always piped; a test pins this.
- `internal/gitinfo` — two engines: in-process go-git by default (zero subprocesses, no git install required); a repo whose status walk blows the render budget (`[project] git_timeout_ms`, default 150 ms) is escalated per-repo to the git CLI via a cache marker, re-probed daily. The CLI child is killed on deadline expiry (`WaitDelay` closes pipes held by hook/fsmonitor descendants).
- `internal/usage` — fetches/parses the subscription-limits payload. Rate-limit error bodies are valid JSON: they are **rejected by shape, never cached** — a failed fetch serves the last good payload with a dim age marker rather than fabricating `0%`.
- `internal/auth` — Sub/API badge (cached 300 s); the `ANTHROPIC_API_KEY` metered-billing alarm is checked live, never cached.
- `internal/account` — account email from `~/.claude.json`. Identity is deliberately **never cached** (a stale label once mislabeled a live session); read every render.
- `internal/cache` — TTL file cache under the platform user-cache dir, keyed per-cwd by FNV-1a. Writes are atomic (temp file + rename); a bare `>` truncate caused torn reads in the bash version.
- `internal/config` — TOML overlaid onto zero-config defaults; `internal/input` — stdin JSON subset.

### Failure posture — never crash the host

Unparseable stdin renders nothing (empty stdout, empty stderr). A malformed config falls back to defaults and complains on stderr only. Every git read is deadline-bounded. `run()` returns `(stdout, stderr)` strings; it never exits non-zero for data problems.

## Conventions

- Display changes = golden-test changes, made intentionally in the same commit. If a golden fails unexpectedly, the code is wrong, not the golden.
- **Neutral fixture identity, always**: fixtures/goldens use `/Users/dev`, `~/projects/demo-app`, `dev@example.com`, session `0a1b2c3d-0000-4000-8000-000000000000` (cwd FNV key `e6531ce5fc09ac4f`) — never real paths, emails, or session ids. Attribution metadata (git author identity, `Authored by:` trailers) is standard and stays.
- **Config keys land in three places in one commit**: `fileSchema` in `internal/config`, `statusline.example.toml`, and the README — 1:1 parity, with defaults and invalid-value errors tested.
- Session state for Claude sessions lives in `.beads/recovery-context.md` (archives under `.beads/archive/`); cards are worked via `/project-tdd lite <card-id>`.
- Security posture: stdin JSON and the workspace path are untrusted. Subprocess arguments are compile-time constants; the workspace path is only ever passed as a directory argument via direct `exec` (no shell). The OAuth token is read from the keychain per fetch, sent only in the Authorization header, never logged or cached.
- gosec/lint suppressions live in `.golangci.yml` as narrow per-path exclusions with a written justification each — follow that pattern; no inline `//nolint` without the same treatment.
- `AGENTS.md` and `CLAUDE.md` are independent files — mirror substantive edits across both.
- Commit authorship: `Authored by: Aaron Lippold<lippold@gmail.com>` — no AI attribution.
