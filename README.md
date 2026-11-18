# claude-statusline

A fast, configurable status line for [Claude Code](https://claude.com/claude-code).
Single static Go binary, zero runtime dependencies, ~10× faster than the bash
script it replaces (~90 ms vs ~950 ms per render).

```
model    Fable 5 1M · Sub · session 0a1b2c3d
project  ~/projects/demo-app · ⎇ main ~2
context  ▓▓▓░░░░░░░ 30%
account  dev@example.com · 5h 28% (resets 1:30p) · week 18% (resets Mon 10a) · Fable 100% (resets Mon 10a)
activity 17h23m · +1,598/-8 lines
```

Rows collapse when they have nothing to say. Alarms are loud only when
abnormal: a `/compact` badge at ≥85% context, reset times once a usage window
runs hot (≥80%), an `⚠ EXTRA USAGE` badge once extra-usage spend reaches the
budget you accept (`[model] extra_budget_dollars`, default `5.0`; below it a
dim `· extra $2.42` tally), and an `⚠ API KEY SET — METERED BILLING` alarm whenever an
`ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` override is exported — carrying
the session's accumulated cost (`· $12.34`) once there is any.

## Install

As a Claude Code plugin (installs the release binary, checksum-verified, and
wires `settings.json` with your consent; a SessionStart hook keeps the binary
version-pinned to the plugin from then on):

```
/plugin marketplace add mitre/claude-statusline
/plugin install claude-statusline@claude-statusline
/claude-statusline:setup
```

Or with Homebrew (the formula is generated and pushed by the release
pipeline; its sha256 pins match the release's `checksums.txt`):

```sh
brew install mitre/tap/claude-statusline
```

The binary lands at `/opt/homebrew/opt/claude-statusline/bin/claude-statusline`
(Apple silicon) — point `statusLine` there, or at a copy wherever you prefer.

Or with Go:

```sh
go install github.com/mitre/claude-statusline@latest
```

Or grab a prebuilt binary from the
[releases page](https://github.com/mitre/claude-statusline/releases), or build
from a clone:

```sh
make build
cp claude-statusline ~/.claude/claude-statusline
```

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

### Google Antigravity CLI (`agy`)

`claude-statusline` natively supports [Google Antigravity CLI](https://github.com/google-deepmind) (`agy`), which shares the exact same `statusLine` specification (`type: command`, sends session telemetry over `stdin`, renders ANSI on `stdout`).

Point Antigravity CLI at the binary in `~/.gemini/antigravity-cli/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "/Users/you/.claude/claude-statusline"
  }
}
```

When running inside Antigravity (`"product": "antigravity"`):
- **Model row auth badge**: Auto-detects the plan tier (e.g. `Google AI Ultra` renders as green `Ultra` badge, `Pro` renders as `Pro`, or `Sub`).
- **Account identity & plan tier**: Reads user email and plan tier directly from the stdin payload (e.g. `lippold@example.com · Google AI Ultra`).
- **Live quota meters**: Maps the quota pools of the family the session's model draws from (`3p-5h`/`3p-weekly` for Claude, GPT and 3p models; `gemini-5h`/`gemini-weekly` for the others) into live 5h/week meters with local timezone reset times; each pool shows only when the quota carries it. The other family's five-hour pool, while in use, shows under its own name (`gemini-5h 10%`), never as the session's `5h`.
- **Model family routing**: Dynamically selects Gemini or 3p quota pools based on the active model family, and renders secondary pools as scoped meters when utilized.
- **API key safety**: Bypasses Anthropic OAuth endpoints and suppresses false metered-billing warnings when `ANTHROPIC_API_KEY` is present in the host environment.


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
| `[model] extra_budget_dollars` | `5.0` | Extra-usage spend you accept before the alarm fires. Below it, a dim `· extra $2.42` tally; at or above it, the `EXTRA USAGE` alarm. `0` alarms on any spend |
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
| `[account] show_stale_age` | `true` | Dim age when stale known-good data is served: the account-row marker and the age on the extra-usage badge |
| `[account] bars` | `false` | Draw each meter as a 10-segment bar like the context row (`5h ▓▓░░░░░░░░ 28%`) |
| `[usage] enabled` | `true` | `false` skips the usage endpoint entirely |
| `[usage] ttl_seconds` | `200` | How long a fetched payload is served before re-fetching. Floor 200 (a lower value is raised to 200, with a stderr note): the usage endpoint rate-limits faster polling, and a failed fetch is not retried before 200 s (or the server's `Retry-After`, capped at 1 hour) have passed since the response arrived |
| `[colors]` `red`/`green`/`yellow`/`blue`/`cyan` | `"1"`/`"2"`/`"3"`/`"4"`/`"6"` (terminal ANSI) | Remap a palette slot to an ANSI index (`"0"`–`"255"`) or a truecolor `"#rrggbb"` hex; see [Colors](#colors) |
| `[cache] dir` | platform user-cache dir | Cache location override. The usage fetch gate and its lock stay in the platform user-cache dir; see "How often it fetches" under [What each row means](#what-each-row-means) |

### Colors

The status line draws its colors from five palette slots. By default each
slot is one of your terminal's own ANSI colors, so your terminal theme
already restyles the line; `[colors]` overrides a slot for the status line
alone.

| Slot | Default | Paints |
|---|---|---|
| `red` | `"1"` | Context bar and percentage at ≥80% (the percentage bold), account meters at ≥80%, lines removed, and the background of the `/compact`, `EXTRA USAGE` and `METERED BILLING` alarm badges |
| `green` | `"2"` | Context bar and percentage below 50%, account meters below 50%, the `Sub` (or plan-tier) auth badge, lines added |
| `yellow` | `"3"` | Context bar and percentage 50–79%, account meters 50–79%, the `API` auth badge, `⚡ fast`, the changed-file count, the `index.lock` badge |
| `blue` | `"4"` | Git branch |
| `cyan` | `"6"` | Model name (bold) |

Everything else is drawn in your terminal's default foreground: dim text
(row labels, separators, effort, session id, `>200k`, reset times, age
markers, session duration, the `extra $` tally), the bold project path and
the account email. The alarm badges' text is always ANSI `7` (white in most
themes); `[colors]` does not change it.

Each value is a quoted string in one of two forms:

- an ANSI color index, `"0"` to `"255"`: `"0"`–`"15"` are your terminal's
  theme colors, `"16"`–`"255"` the fixed 256-color palette;
- a truecolor hex, `"#rrggbb"`, which needs a terminal with 24-bit color
  support.

For example, colors from the Okabe–Ito palette, which was designed to stay
distinguishable with the common forms of color blindness:

```toml
[colors]
red = "#D55E00"    # vermillion
green = "#009E73"  # bluish green
yellow = "#F0E442"
blue = "#0072B2"
cyan = "#56B4E9"   # sky blue
```

Slots you leave out keep their defaults. An invalid value (`"lime"`,
`"#abc"`, `"256"`, or an unquoted number such as `green = 46`) is a config
error: the whole file is ignored, every setting falls back to its default,
and the error is printed on stderr, e.g.
`claude-statusline: config error: colors.green: "lime" is not valid (use an ANSI index 0-255 or a "#rrggbb" hex)`.

## What each row means

| Row | Content |
|-----|---------|
| `model` | Model name + context-window size, auth mode (`Sub`/`API`, or plan tier badge like `Ultra`/`Pro`), reasoning-effort level, `⚡ fast` badge when fast mode is active, short session id (distinguishes concurrent sessions in one repo) |
| `project` | Current directory (`~`-shortened), git branch (`@sha` when detached), changed-file count, long-held `index.lock` age badge |
| `context` | Context-window usage bar; green <50%, yellow <80%, red ≥80%, dim `>200k` once absolute tokens cross the long-context tier, `/compact` badge ≥85% |
| `account` | ACCOUNT-scope subscription meters (Sub auth and Antigravity CLI, macOS and Linux; Windows uses the same credentials-file mechanism and is expected to work — unverified) — see meter table below |
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
accounts. It is read from Claude Code's account record, found the way Claude Code finds it:
`.claude.json` in `$CLAUDE_CONFIG_DIR`, else in your home directory
(`.claude-custom-oauth.json` when `CLAUDE_CODE_CUSTOM_OAUTH_URL` is set), unless a
legacy `.config.json` in Claude Code's config directory takes precedence, as it does
for Claude Code itself. A session started with its own `CLAUDE_CONFIG_DIR` therefore
names that account. With neither `CLAUDE_CONFIG_DIR` nor `HOME` set to an absolute path,
no identity is shown. Antigravity sessions read it directly from the session payload.
It is read on every render — identity is deliberately never cached, so a login change
shows immediately — and omitted when unavailable;
`[account] show_email = false` hides it and `email_style = "dim"` quiets it to the furniture tier. For subscription plans carrying a recognized plan tier name (such as Antigravity's `Google AI Ultra`), the tier is rendered alongside the email. The meters are all **account-wide percent-of-plan-allotment**
as reported by the usage API or session telemetry (shared by every session under your
subscription — two concurrent sessions correctly show the same pools):

| Meter | Window |
|---|---|
| `5h N%` | rolling 5-hour, all models; omitted while no five-hour window is in progress (never shown as 0%) |
| `week N%` | rolling 7-day, all models; omitted while the usage endpoint reports no weekly window in progress and stdin carries none |
| `<model>/wk N%` | rolling 7-day pool for **this session's** model — a parallel weekly cap, not a slice of `week`; omitted when the payload has no window for it. The window is matched against the payload's own `seven_day_*` keys, so a model the vendor ships tomorrow is picked up with no code change. The usage endpoint values a per-model window only when that limit policy is active on your account (plans without model-specific caps report them as `null`, so the meter self-omits) |
| `<scope> N%` | a plan limit the payload narrows to a **scope** — today a model, named by the payload (e.g. `Fable 100%`). A parallel cap like the one above, shown whenever the payload carries it, with its own reset time. This is how a model-specific weekly limit surfaces even when no `seven_day_*` window exists for it |

`[account] bars = true` draws every meter (`5h`, `week`, the per-model
window and scoped limits) with the context row's 10-segment bar between its
name and percentage, one segment per full 10%:

```
account  dev@example.com · 5h ▓▓░░░░░░░░ 28% (resets 1:30p) · week ▓░░░░░░░░░ 18% (resets Mon 10a) · Fable ▓▓▓▓▓▓▓▓▓▓ 100% (resets Mon 10a)
```

Each bar takes its meter's color (green below 50%, yellow from 50%, red from
80%). The default, `false`, keeps the text-only row.

Reset times show on every meter by default; `show_resets = "quiet"` restores
the hot-only (≥80%) behavior.

When the usage endpoint can't be reached, the row degrades in two stages.
Claude Code v2.1.210+ sends the two all-model meters in the statusline
payload itself, so each of `5h` and `week` switches to the **live** value
when the payload carries that window; either one alone is a reading, and a
window the payload does not carry keeps the endpoint's reading or is omitted,
never shown as 0% (no network needed — even when no cached payload exists at
all). A fresh payload keeps its readings; a live stdin
reading only fills a window that payload reports as not in progress.
Endpoint-only account segments (a weekly reading
stdin did not replace, plan-scoped limits, the per-model window) keep
serving the last known-good payload, qualified by a trailing dim
`(data 6m old)` age marker whenever such stale data is actually visible; a
row of only live meters carries no marker, and a row with no meter is not
rendered. The model row's extra-usage badge also comes only from that
payload, so a stale badge carries its own dim age
(`⚠ EXTRA USAGE $12.34 (6m old)`, `· extra $3.10 (6m old)`), whether or not
the account row is shown; the alarm stays, since spend only grows within a
billing period. On hosts whose payload lacks
`rate_limits`, the old behavior remains: the whole row serves the stale
payload with the marker, and is omitted when there is no data it can show —
values are never fabricated. `show_stale_age = false` hides both ages.

**Whose usage is shown.** The fetched payload is cached per account and
token: the file is named by a hash of the account ID in Claude Code's
account record and a one-way fingerprint (a sha256 prefix, never the token)
of the access token that fetched it, so two logins of one account keep
separate entries. A cached payload is shown, fresh or
stale, only while both the account record and the current token still
match, so after `/login` to another account the row never shows the
previous account's numbers or alarm; it shows the live numbers from the
session payload until a fetch for the new account succeeds. If the account record or
token changes while a request is out, that render shows only the session
payload: neither the response nor the cached payload is shown, and the
response is not cached. The request itself is sent only with the token the
render started with. Sessions started with `CLAUDE_CODE_OAUTH_TOKEN` skip the
usage endpoint and the saved email entirely, because the saved login does
not describe that token; they show only the session payload's numbers.

**How often it fetches.** One fetch gate is shared by every session and
account on the machine: at most one request per opening, whichever session
renders first. The next opening is 200 s after the response arrived, or the
server's `Retry-After` when longer (in seconds or as an HTTP date), capped
at 1 hour. A gate more than 2 hours ahead of the clock was left by a clock
that moved back by hours, and counts as open, so the next session fetches
instead of waiting for the clock to catch up. The extra hour keeps a real
hold in place through a clock step back of under an hour.
The gate (`usage.next-attempt`) and its lock (`usage.lock`) live in the
platform user-cache dir (`~/Library/Caches/claude-statusline` on macOS),
even for a session whose `[cache] dir` points elsewhere, so every session
waits on the same gate; payloads follow `[cache] dir`. When the user-cache
dir cannot be determined (no home directory, or a relative
`XDG_CACHE_HOME`), the gate and lock stay in `[cache] dir`. When it is
determined but cannot be created, that session does not fetch.
Only one session fetches at a time: it first takes an exclusive lock on
`usage.lock` in the gate directory, without waiting. Holding the lock, it
checks the gate, moves the gate 200 s ahead, sends the request, records the
result and releases the lock. A session that cannot write the gate sends no
request. A gate file that exists but cannot be read, or holds no time, is
rewritten 200 s ahead and no request is sent, so the next opening fetches.
Sessions that find the lock held show their own cached numbers
without waiting. A session killed while holding the lock frees it at once;
the gate it wrote still holds the next fetch until it passes. A stopped
session (suspended, or paused in a debugger) keeps the lock, and the others
keep showing cached numbers until it continues or exits. A session that
cannot create or lock the file (a read-only gate directory, for example)
does not fetch. Releases up to 0.3.1 do not take the lock; they still
honour the gate. Only a plain file at `usage.lock` is locked: if that name
is a link, a folder or a special file, no session fetches until it is
replaced by a plain file, and the entry is left as it is. The gate
directory is created with mode 0700 when missing, and is assumed to be
yours: anyone who can write to it can stop the fetch or defeat the lock.
The lock needs the gate directory on a local disk. When
several accounts are in use, one account can wait more than one opening for
its own fetch while another account's session wins; there is no
wall-clock bound on that wait.

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
  intentionally not honored (pinned by a test). `[colors]` remaps the
  palette slots (ANSI index or truecolor hex) for users who want a
  statusline-specific theme.
- **Never crashes the host:** unparseable stdin renders nothing; a malformed
  config falls back to defaults and complains on stderr.
- **Security posture:** stdin JSON and the workspace path are untrusted input —
  bad JSON renders nothing, and the path is only ever passed to direct `exec`
  as a directory argument (no shell anywhere; every subprocess argument is a
  compile-time constant or, for the keychain lookup, a derived name that must
  match its exact expected pattern before `security` runs). The usage fetch
  is bounded (2 s timeout, 1 MB body cap) and the OAuth token is resolved per
  fetch the way Claude Code resolves it. First the macOS keychain item: its
  account is your user name, and its service name carries a hash of the
  config directory in effect — `CLAUDE_SECURESTORAGE_CONFIG_DIR` when that
  is set (set but empty means no hash), else a non-empty
  `CLAUDE_CONFIG_DIR`. An item that parses as a non-null JSON value is final,
  whether or not it holds a token. Otherwise the plaintext `.credentials.json` in the
  storage directory: `CLAUDE_SECURESTORAGE_CONFIG_DIR` when set (set but
  empty means `~/.claude`), else `CLAUDE_CONFIG_DIR`, else `~/.claude`; a
  relative directory is never read. Only the access token is read; it is sent only in
  the Authorization header, never logged and never cached; the usage cache stores only a one-way fingerprint of it (a sha256 prefix). `make vuln`
  (govulncheck) and gosec (inside `make lint`) gate every change.

## Develop

One-time setup per clone:

```sh
make tools    # install golangci-lint / govulncheck / goreleaser at the pinned versions
make hooks    # install the pre-commit fast gate (runs make lint; skippable only via --no-verify)
```

Tool versions are pinned once in the Makefile (`*_VERSION` variables) and CI
installs the same way; the gate targets refuse a mismatched local binary
rather than lint with the wrong version.

```sh
make check    # the one gate: lint + vuln + race + cover + build
make test     # unit tests (all logic is exec/HTTP-injected — no network)
make lint     # golangci-lint: config schema verify + full run (zero-issue gate)
make vuln     # govulncheck
make race     # full suite under the race detector
make cover    # coverage floors: 90% total, 85% per package (override: COVER_MIN / PKG_COVER_MIN)
make build    # local binary (claude-statusline --version reports build identity; "dev" on un-injected builds)
make dev-install # build HEAD and install it as the dev copy ~/.claude/claude-statusline-dev (run make check first; see AGENTS.md "Live Binary")
make snapshot # local no-publish proof of the release pipeline: darwin/linux × arm64/amd64 archives + checksums into dist/
make preflight TAG=v0.2.0  # the release gate, runnable locally: tag shape, manifest parity, CHANGELOG section, tag-on-main
```

Releases are cut by pushing a `v<major>.<minor>.<patch>` tag; everything
after that is pipeline. [docs/release-pipeline.md](docs/release-pipeline.md)
is the full reference — jobs, gates, artifacts, the Homebrew formula path,
and the release runbook.

## Contributing

Pull requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers setup,
the conventions a change is reviewed against, what a new config key needs,
and the pull request process (please keep "Allow edits by maintainers"
checked). Participation follows the [Code of Conduct](CODE_OF_CONDUCT.md);
report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

Licensed under the Apache License, Version 2.0 — see [LICENSE.md](LICENSE.md).

### NOTICE

© 2026 The MITRE Corporation. Approved for Public Release; Distribution
Unlimited. Case Number 18-3678.

See [NOTICE.md](NOTICE.md) for full terms.
