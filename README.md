# claude-statusline

A fast, configurable status line for [Claude Code](https://claude.com/claude-code).
Single static Go binary, zero runtime dependencies, ~10× faster than the bash
script it replaces (~90 ms vs ~950 ms per render).

```
model    Fable 5 1M · Sub · session 0a1b2c3d
project  ~/github/mitre/ts-inspec-profile-parser · ⎇ main ~2
context  ▓▓▓░░░░░░░ 30%
account  5h 28% (resets 1:30p) · week 18% (resets Mon 10a) · opus/wk 41% (resets Tue 3p)
activity 17h23m · +1,598/-8 lines
```

Rows collapse when they have nothing to say. Alarms are loud only when
abnormal: a `/compact` badge at ≥85% context, reset times once a usage window
runs hot (≥80%), an `⚠ EXTRA USAGE` badge while extra-usage credits are
actively billing, and an `⚠ API KEY SET — METERED BILLING` alarm whenever an
`ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` override is exported.

## Install

From a clone (primary path until the repo is published):

```sh
make build
cp claude-statusline ~/.claude/claude-statusline
```

Once published to GitHub, `go install github.com/mitre/claude-statusline@latest`
also works.

Point Claude Code at the binary in `~/.claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "/Users/you/.claude/claude-statusline",
    "padding": 0
  }
}
```

Claude Code passes session JSON on stdin; the program prints ANSI rows on
stdout. That is the entire contract — no shell involved.

## Configure

Optional. Copy [`statusline.example.toml`](statusline.example.toml) to
`~/.claude/statusline.toml` and flip what you want; every key defaults to the
behavior shown above. `$CLAUDE_STATUSLINE_CONFIG` overrides the path.

## What each row means

| Row | Content |
|-----|---------|
| `model` | Model name + context-window size, auth mode (`Sub`/`API`), short session id (distinguishes concurrent sessions in one repo) |
| `project` | Current directory (`~`-shortened), git branch (`@sha` when detached), changed-file count |
| `context` | Context-window usage bar; green <50%, yellow <80%, red ≥80%, `/compact` badge ≥85% |
| `account` | ACCOUNT-scope subscription meters (Sub auth only) — see meter table below |
| `activity` | Session duration and lines added/removed |

The `account` row's meters are all **account-wide percent-of-plan-allotment**
as reported by the usage API (shared by every session under your
subscription — two concurrent sessions correctly show the same pools):

| Meter | Window |
|---|---|
| `5h N%` | rolling 5-hour, all models |
| `week N%` | rolling 7-day, all models |
| `<model>/wk N%` | rolling 7-day pool for **this session's** model family (opus/sonnet/haiku) — a parallel weekly cap, not a slice of `week`; omitted when the payload has no window for the session's model |

Reset times show on every meter by default; `show_resets = "quiet"` restores
the hot-only (≥80%) behavior.

## Design notes

- **Provenance:** port of `reference/statusline.sh`, verified byte-identical
  on the fixture corpus before any divergence. The byte-parity gate served
  through the port and local rollout, then retired with the first intentional
  display change (the `account` row, 2026-07-03); the Go golden tests are the
  display spec of record, and the reference script is frozen in-repo as the
  port-era spec and functional rollback.
- **Shared caches:** uses the same `/tmp/.claude-statusline-cache` files and
  md5-of-cwd keys as the bash reference, so the two can be swapped freely.
- **Bounded git latency** (Starship's `command_timeout` pattern): every git
  call runs under a deadline — `[project] git_timeout_ms`, default 150 ms,
  `0` disables — with the child process killed on expiry, and the cached
  branch/dirty values served instead. A pathological worktree (huge repo,
  NFS, cold caches) can never stall a render. On very large repos, also
  enable git's built-in fsmonitor daemon (`git config core.fsmonitor true`)
  so `git status` itself stays fast.
- **Atomic cache writes** (temp file + rename): a bare `>` redirect truncates
  before writing, which let concurrent renders read empty files — the bug
  that silently ate the dirty badge in the bash version.
- **No fabricated zeros:** the usage endpoint's rate-limit error bodies are
  valid JSON; they are rejected by shape, never cached, and a failed fetch
  serves the last *good* payload instead of rendering `0%`.
- **Never crashes the host:** unparseable stdin renders nothing; a malformed
  config falls back to defaults and complains on stderr.
- **Security posture:** stdin JSON and the workspace path are untrusted input —
  bad JSON renders nothing, and the path is only ever passed to direct `exec`
  as a directory argument (no shell anywhere; every subprocess argument is a
  compile-time constant). The usage fetch is bounded (2 s timeout, 1 MB body
  cap) and the OAuth token is read from the keychain per fetch, sent only in
  the Authorization header, never logged and never cached. `make vuln`
  (govulncheck) and gosec (inside `make lint`) gate every change.

## Develop

```sh
make check    # the one gate: lint + vuln + race + cover + build
make test     # unit tests (all logic is exec/HTTP-injected — no network)
make lint     # golangci-lint: config schema verify + full run (zero-issue gate)
make vuln     # govulncheck
make race     # full suite under the race detector
make cover    # coverage with an 85% floor (override: COVER_MIN=90 make cover)
make build    # local binary
make release  # cross-compile darwin/linux × arm64/amd64 into dist/
```

## License

Apache-2.0
