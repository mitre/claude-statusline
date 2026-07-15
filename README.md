# claude-statusline

A fast, configurable status line for [Claude Code](https://claude.com/claude-code).
Single static Go binary, zero runtime dependencies, ~10× faster than the bash
script it replaces (~90 ms vs ~950 ms per render).

```
model    Fable 5 1M · Sub · session 0a1b2c3d
project  ~/projects/demo-app · ⎇ main ~2
context  ▓▓▓░░░░░░░ 30%
account  dev@example.com · 5h 28% (resets 1:30p) · week 18% (resets Mon 10a) · opus/wk 41% (resets Tue 3p)
activity 17h23m · +1,598/-8 lines
```

Rows collapse when they have nothing to say. Alarms are loud only when
abnormal: a `/compact` badge at ≥85% context, reset times once a usage window
runs hot (≥80%), an `⚠ EXTRA USAGE` badge while extra-usage credits are
actively billing, and an `⚠ API KEY SET — METERED BILLING` alarm whenever an
`ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` override is exported — carrying
the session's accumulated cost (`· $12.34`) once there is any.

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

| Key | Default | Effect |
|---|---|---|
| `[rows]` `model`/`project`/`context`/`account`/`activity` | all `true` | Show or hide whole rows (`limits` is accepted as a deprecated alias for `account`) |
| `[model] show_auth` | `true` | `Sub`/`API` auth badge |
| `[model] show_session` | `true` | Short session-id segment |
| `[model] show_context_size` | `true` | `1M`/`200k` context-window size after the model name |
| `[model] show_effort` | `true` | Dim reasoning-effort level (`xhigh`, …); omitted when the host sends none |
| `[model] show_fast_mode` | `true` | `⚡ fast` badge while fast mode is active |
| `[model] show_metered_cost` | `true` | Session cost inside the metered-billing alarm (only while the alarm shows) |
| `[context] exceeds_200k_marker` | `true` | Dim `>200k` once the session crosses the 200k-token tier |
| `[project] show_branch` | `true` | Git branch (`@sha` when detached) |
| `[project] show_dirty` | `true` | Changed-file count |
| `[project] tilde_home` | `true` | Shorten the `$HOME` prefix to `~` |
| `[project] git_timeout_ms` | `150` | Per-read git deadline; `0` disables the bound |
| `[project] git_engine` | `"auto"` | `"auto"`, `"gogit"`, or `"cli"` |
| `[project] lock_badge` | `true` | Long-held `index.lock` age badge |
| `[project] lock_badge_after_s` | `300` | Lock age before the badge appears |
| `[account] show_email` | `true` | Account email scope label |
| `[account] email_style` | `"normal"` | `"dim"` quiets the email to the furniture tier |
| `[account] show_resets` | `"always"` | `"quiet"` shows reset times only on hot (≥80%) meters |
| `[account] show_stale_age` | `true` | Dim age marker when stale known-good data is served |
| `[usage] enabled` | `true` | `false` skips the usage endpoint entirely |
| `[usage] ttl_seconds` | `180` | How long a fetched payload is served before re-fetching |
| `[cache] dir` | platform user-cache dir | Cache location override |

## What each row means

| Row | Content |
|-----|---------|
| `model` | Model name + context-window size, auth mode (`Sub`/`API`), reasoning-effort level, `⚡ fast` badge when fast mode is active, short session id (distinguishes concurrent sessions in one repo) |
| `project` | Current directory (`~`-shortened), git branch (`@sha` when detached), changed-file count, long-held `index.lock` age badge |
| `context` | Context-window usage bar; green <50%, yellow <80%, red ≥80%, dim `>200k` once absolute tokens cross the long-context tier, `/compact` badge ≥85% |
| `account` | ACCOUNT-scope subscription meters (Sub auth only, macOS and Linux; Windows uses the same credentials-file mechanism and is expected to work — unverified) — see meter table below |
| `activity` | Session duration and lines added/removed |

When `.git/index.lock` has been held longer than `[project]
lock_badge_after_s` (default 300 s), the project row shows a yellow factual
badge — `⚠ index.lock 14m`. It reports the observable age only: a present
lock is often **legitimate** (any index write takes it; a `git commit` with
an editor open holds it for minutes, a large rebase longer), so the badge is
information, never an instruction — no "stale" verdict, no removal command,
and nothing is ever deleted automatically. Detection is one `stat` of the
repository's own git dir (worktrees resolve to their private git dir), no
subprocess, no lock taken. `lock_badge = false` hides it.

The `account` row opens with the logged-in **account email** — it names
whose pools the meters describe, which matters when you run multiple
accounts. It is read from Claude Code's local state (`~/.claude.json`) on
every render — identity is deliberately never cached, so a login change
shows immediately — and omitted when unavailable;
`[account] show_email = false` hides it and `email_style = "dim"` quiets it to the furniture tier. The meters are all **account-wide percent-of-plan-allotment**
as reported by the usage API (shared by every session under your
subscription — two concurrent sessions correctly show the same pools):

| Meter | Window |
|---|---|
| `5h N%` | rolling 5-hour, all models |
| `week N%` | rolling 7-day, all models |
| `<model>/wk N%` | rolling 7-day pool for **this session's** model family (opus/sonnet/haiku) — a parallel weekly cap, not a slice of `week`; omitted when the payload has no window for the session's model. The usage endpoint values a per-model window only when that limit policy is active on your account (plans without model-specific caps report them as `null`, so the meter self-omits). The Fable weekly limit is never exposed by this endpoint — Claude Code reads it from API response headers that external tools can't see (upstream requests to forward it: anthropics/claude-code#73770, #69791) |

Reset times show on every meter by default; `show_resets = "quiet"` restores
the hot-only (≥80%) behavior.

When the usage endpoint can't be reached, the row serves the last
known-good payload and says so: a trailing dim `(data 6m old)` marker shows
the served payload's age, clearing on the first successful fetch. The
meters keep their true (old) values — the marker qualifies the data, it
never censors it. `show_stale_age = false` hides the marker.

## Design notes

- **Provenance:** began as a port of a bash statusline, verified
  byte-identical on the fixture corpus before any divergence. The byte-parity
  gate served through the port and local rollout, then retired with the first
  intentional display change (the `account` row, 2026-07-03). The Go golden
  tests are the display spec of record; the original script and its
  compatibility era are preserved in git history. Rollback is the previous
  binary.
- **Caches:** small TTL files under the platform user-cache dir
  (`~/Library/Caches/claude-statusline` on macOS; `[cache] dir` overrides),
  keyed per working directory by FNV-1a hash. Caches are disposable —
  deleting the directory costs one cold render.
- **Git engine — in-process by default, CLI as the huge-repo escape hatch:**
  branch and dirty count are read in-process via go-git, so no git
  installation is required and the default path spawns zero subprocesses.
  Every read runs under the render budget (`[project] git_timeout_ms`,
  default 150 ms, `0` disables), so a pathological worktree can never stall
  a render. The fallback exists because the statusline is a one-shot process:
  an in-process status walk that overruns is abandoned with no surviving
  progress, so a huge repo would degrade on every render forever — while the
  git CLI's fsmonitor daemon and on-disk caches persist *between*
  invocations. A repo that blows the budget is therefore escalated (per-repo
  marker, re-probed daily) to the CLI engine, which keeps the hard deadline
  with the child process killed on expiry. On very large repos, enable
  `git config core.fsmonitor true` so the CLI's `git status` stays fast;
  `[project] git_engine` forces `"gogit"` or `"cli"` explicitly.
- **Atomic cache writes** (temp file + rename): a bare `>` redirect truncates
  before writing, which let concurrent renders read empty files — the bug
  that silently ate the dirty badge in the bash version.
- **No fabricated zeros:** the usage endpoint's rate-limit error bodies are
  valid JSON; they are rejected by shape, never cached, and a failed fetch
  serves the last *good* payload instead of rendering `0%` — marked with a
  dim factual age (`(data 6m old)`) so stale-but-true is never mistaken for
  fresh.
- **Styling** goes through lipgloss v2 (`charm.land/lipgloss/v2`) with the
  ANSI 16-color palette, and the output is deliberately
  environment-independent: a statusline is always piped and the host
  interprets the sequences, so there is no tty detection and `NO_COLOR` is
  intentionally not honored (pinned by a test). This is the foundation for
  user-configurable theming later.
- **Never crashes the host:** unparseable stdin renders nothing; a malformed
  config falls back to defaults and complains on stderr.
- **Security posture:** stdin JSON and the workspace path are untrusted input —
  bad JSON renders nothing, and the path is only ever passed to direct `exec`
  as a directory argument (no shell anywhere; every subprocess argument is a
  compile-time constant). The usage fetch is bounded (2 s timeout, 1 MB body
  cap) and the OAuth token is resolved per fetch through Claude Code's own
  credential stores — the `.credentials.json` file when present (honoring
  `CLAUDE_CONFIG_DIR`), else the macOS keychain — mirroring Claude Code's
  documented precedence. Only the access token is read; it is sent only in
  the Authorization header, never logged and never cached. `make vuln`
  (govulncheck) and gosec (inside `make lint`) gate every change.

## Develop

```sh
make check    # the one gate: lint + vuln + race + cover + build
make test     # unit tests (all logic is exec/HTTP-injected — no network)
make lint     # golangci-lint: config schema verify + full run (zero-issue gate)
make vuln     # govulncheck
make race     # full suite under the race detector
make cover    # coverage floors: 90% total, 85% per package (override: COVER_MIN / PKG_COVER_MIN)
make build    # local binary
make release  # cross-compile darwin/linux × arm64/amd64 into dist/
```

## License

Apache-2.0
