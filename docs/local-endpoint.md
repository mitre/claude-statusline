# Local test endpoint and live check

A LiteLLM proxy on `127.0.0.1` that Claude Code can use as a custom
endpoint, and `make live-check`, which runs one real interactive Claude Code
turn through it and keeps the statusline payload Claude Code sent. Use them
to see what the statusline receives and renders in custom-endpoint and
gateway sessions without touching your own login, settings or sessions.

Developer-only: nothing here runs in `make check` or CI.

## Prerequisites

| Needed for | Tool |
|---|---|
| The endpoint (`make endpoint-up`) | [uv](https://docs.astral.sh/uv/getting-started/installation/) (installs the pinned LiteLLM into a cached virtualenv; nothing global) |
| The live check (`make live-check`) | `tmux`, and Claude Code (`claude`) on `PATH` |
| The `local` route only | [Ollama](https://ollama.com/download), running, with the route's model pulled (`make endpoint-pull`) |

The `mock` route needs neither Ollama nor network access.

## Quick start

```sh
make endpoint-up                 # start LiteLLM on 127.0.0.1:4000
make build                       # the ./claude-statusline the live check renders with
make live-check ROUTE=mock       # one Claude Code turn; prints the output directory and the rows
make endpoint-down               # stop it
```

For the `local` route, pull its model once, then run the check with it:

```sh
make endpoint-pull
make live-check ROUTE=local
```

To set extra variables for Claude Code during the check:

```sh
make live-check ROUTE=mock VARS="CLAUDE_CODE_USE_GATEWAY=1"
```

## Commands

| Command | Effect |
|---|---|
| `make endpoint-up` | Installs the locked LiteLLM (hash-checked) on first use, starts it on `127.0.0.1:4000` with a new key for this run, waits until it answers, and prints its address, routes and key file. If it is already running, prints the same line and starts nothing. |
| `make endpoint-status` | Prints that line when running. When stopped it prints `stopped` and fails: `scripts/local-endpoint.sh status` exits 3, which make reports as `Error 3`. |
| `make endpoint-down` | Stops the process `endpoint-up` started and removes its run files (key, pid, log). |
| `make endpoint-pull` | Starts the endpoint if needed, asks it which Ollama model the `local` route names, and runs `ollama pull` for that model. |
| `make live-check` | Runs one interactive Claude Code turn through the running endpoint; see [The live check](#the-live-check). |
| `make endpoint-lock` | Regenerates the lock from `LITELLM_VERSION`; see [The LiteLLM pin and lock](#the-litellm-pin-and-lock). |

`scripts/local-endpoint.sh` takes `start`, `stop`, `status`, `model` and
`run-dir` directly; the make targets above are the supported entry points.

## Routes

[`scripts/local-endpoint/litellm.yaml`](../scripts/local-endpoint/litellm.yaml)
defines two routes. Claude Code picks one with `ANTHROPIC_MODEL`; the live
check sets it from `ROUTE`.

| Route | Answers with |
|---|---|
| `mock` | A fixed reply, `mock reply from the local test endpoint`. No model, no Ollama, no network. |
| `local` | The Ollama model on `127.0.0.1:11434` that the config names (`gemma4:e2b` today). |

### Changing the local model

The model is named on one line of `litellm.yaml` (`model: ollama_chat/<model>`).
Change that line, restart the endpoint, and pull the new model:

```sh
make endpoint-down
make endpoint-up
make endpoint-pull
```

The model must list the `tools` capability in `ollama show <model>`: Claude
Code sends tool definitions on every request. `make live-check ROUTE=local`
refuses a model that is not pulled or lacks `tools`.

## The live check

`make live-check` takes these variables:

| Variable | Default | Meaning |
|---|---|---|
| `ROUTE` | `mock` | The route used for every model Claude Code asks for (`mock` or `local`). |
| `STATUSLINE` | `./claude-statusline` | The statusline binary that renders the captured payload. Run `make build` first for the default, or name another build. |
| `VARS` | empty | Extra `NAME=value` variables for Claude Code, separated by spaces. Names match `^[A-Z][A-Z0-9_]*$`; values cannot contain spaces and are never evaluated as shell. |

Two more variables tune it when set in the environment:
`LIVE_CHECK_TIMEOUT` (seconds for each wait, default 180) and
`LIVE_CHECK_OUT` (where output directories go).

What one run does:

1. Starts the installed `claude` in a private tmux server (its socket in a
   throwaway directory, started with no tmux configuration).
2. Gives Claude Code only these variables: `PATH`, `TERM`, a throwaway
   `HOME` and `CLAUDE_CONFIG_DIR`, `DISABLE_AUTOUPDATER=1`,
   `ANTHROPIC_BASE_URL` (the endpoint), `ANTHROPIC_AUTH_TOKEN` (the run's
   key), `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL` and
   `ANTHROPIC_DEFAULT_HAIKU_MODEL` (all set to the route), plus `VARS`.
   `VARS` cannot replace any of these.
3. Seeds the throwaway config dir only to skip the first-run screens
   (onboarding, theme, folder trust for the session's directory). Nothing is
   copied from your `~/.claude` or `~/.claude.json`.
4. Sends one prompt, waits until the turn has ended and the statusline has
   rendered its payload, then sends `/exit`.
5. Keeps the output directory and removes the throwaway directories and the
   tmux server, on success and on failure alike.

The output directory holds:

| File | Content |
|---|---|
| `capture.json` | The stdin JSON Claude Code sent the statusline after the turn |
| `rows.txt` | What `STATUSLINE` printed for that payload |
| `claude-version.txt` | `claude --version` |

Print mode (`claude -p`) never runs the statusline command, which is why the
check drives an interactive session.

It refuses before starting Claude Code when the endpoint is not running,
`tmux` or the `STATUSLINE` binary is missing, a `VARS` entry is invalid, or
(with `ROUTE=local`) the model is not pulled or lacks `tools`.

## Where files go

| What | Where |
|---|---|
| Endpoint run files: key (mode 0600), pid, port, log, the LiteLLM virtualenv | `~/Library/Caches/claude-statusline/local-endpoint` on macOS, `${XDG_CACHE_HOME:-~/.cache}/claude-statusline/local-endpoint` on Linux; `ENDPOINT_RUN_DIR` overrides |
| Live-check output directories (one per run, named by date and time) | `live-check` beside it in the same cache dir; `LIVE_CHECK_OUT` overrides |

The key is created per run by `make endpoint-up`, reaches LiteLLM through its
environment and Claude Code through a launcher inside the session, and is
never printed or put on a command line. `make endpoint-down` deletes it.

Settings for `make endpoint-up`: `ENDPOINT_PORT` (default 4000) and
`ENDPOINT_READY_TIMEOUT` (seconds, default 60). The endpoint listens on
`127.0.0.1` only and refuses any other `ENDPOINT_HOST`.

## The LiteLLM pin and lock

The Makefile pins `LITELLM_VERSION` and `LITELLM_PYTHON`.
[`scripts/local-endpoint/requirements.txt`](../scripts/local-endpoint/requirements.txt)
is the lock: every package pinned with hashes, resolved for all platforms.
`make endpoint-up` installs only from it (`--require-hashes`) and refuses to
start when the lock pins a different LiteLLM than `LITELLM_VERSION`.

To move to a new LiteLLM:

1. Wait until the release is at least 7 days old (a newer pin needs the
   owner's approval).
2. Change `LITELLM_VERSION` in the Makefile.
3. Run `make endpoint-lock` and commit the Makefile and the regenerated lock
   together.

Packages listed in
[`scripts/local-endpoint/excludes.txt`](../scripts/local-endpoint/excludes.txt)
are left out of the lock; the file gives the reason for each.

## Troubleshooting

- **`another local-endpoint command holds .../lock`**: a start or stop was
  killed part-way. If no `make endpoint-*` command is running, remove the
  directory it names with `rmdir`. It is never cleared automatically.
- **`LiteLLM did not answer ... within 60s`**: the last 20 log lines follow
  the message, with the key replaced by `[key]`.
- **`the endpoint is not running — run: make endpoint-up`** from
  `make live-check` or `make endpoint-pull`: start it first.

## What it never does

- Listen anywhere but `127.0.0.1`, or send its key through a proxy.
- Run Claude Code with your `~/.claude` config, settings or sessions, or
  change them or your keychain.
- Run in `make check` or CI.
