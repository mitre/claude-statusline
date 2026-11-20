#!/bin/sh
# Developer-only local test endpoint: the pinned LiteLLM proxy on 127.0.0.1,
# serving the routes in scripts/local-endpoint/litellm.yaml. Never part of
# make check or CI; run it through make endpoint-up / endpoint-down /
# endpoint-status, which pass LITELLM_VERSION and LITELLM_PYTHON.
#
#   local-endpoint.sh start    install (hash-checked) if needed, start, wait
#   local-endpoint.sh stop     end the process start began; remove run files
#   local-endpoint.sh status   exit 0 and describe it when running, else exit 3
#   local-endpoint.sh model    the Ollama model behind the local route
#
# Run state lives in the user cache directory (ENDPOINT_RUN_DIR overrides):
# the pid and its start time, a per-run key (mode 0600), the curl header
# file that carries it, the proxy log, and a virtualenv reused while the lock
# is unchanged. The key reaches LiteLLM through its environment and curl
# through a header file, never a command line, and is never printed.
set -u

HERE=$(cd "$(dirname "$0")" && pwd)
LOCK="$HERE/local-endpoint/requirements.txt"
CONFIG="$HERE/local-endpoint/litellm.yaml"
HOST=${ENDPOINT_HOST:-127.0.0.1}
PORT=${ENDPOINT_PORT:-4000}
TIMEOUT=${ENDPOINT_READY_TIMEOUT:-60}
# Each request to the proxy may take this long; a server that accepts a
# connection and never answers must not hold start past its deadline.
REQUEST_TIMEOUT=2

die() {
  echo "local-endpoint: $*" >&2
  exit 1
}

# cache_dir mirrors Go's os.UserCacheDir on macOS and Linux.
cache_dir() {
  if [ "$(uname -s)" = Darwin ]; then
    echo "$HOME/Library/Caches"
  else
    echo "${XDG_CACHE_HOME:-$HOME/.cache}"
  fi
}

RUN=${ENDPOINT_RUN_DIR:-$(cache_dir)/claude-statusline/local-endpoint}
VENV="$RUN/venv"

# sha prints the file's sha256, or fails: a missing hash tool must never
# yield an empty digest that matches another.
sha() {
  if command -v sha256sum >/dev/null 2>&1; then
    out=$(sha256sum "$1") || return 1
  else
    out=$(shasum -a 256 "$1") || return 1
  fi
  echo "${out%% *}"
}

started_at() {
  ps -o lstart= -p "$1" 2>/dev/null
}

# running succeeds when the recorded pid is alive and still the process
# start began: its start time must match the one recorded, so a reused pid
# is never mistaken for the proxy.
running() {
  [ -f "$RUN/pid" ] && [ -f "$RUN/started" ] || return 1
  pid=$(cat "$RUN/pid")
  have=$(started_at "$pid")
  [ -n "$have" ] && [ "$have" = "$(cat "$RUN/started")" ]
}

clear_run_files() {
  rm -f "$RUN/pid" "$RUN/started" "$RUN/key" "$RUN/auth-header" "$RUN/port"
}

end_process() {
  kill "$1" 2>/dev/null
  i=0
  while kill -0 "$1" 2>/dev/null && [ "$i" -lt 10 ]; do
    sleep 1
    i=$((i + 1))
  done
  kill -9 "$1" 2>/dev/null
  return 0
}

# redact copies stdin to stdout with every occurrence of the key replaced.
# The key is read from its file inside awk (never an argument) and matched
# as a plain string, not a pattern.
redact() {
  awk -v keyfile="$RUN/key" '
    BEGIN { getline key < keyfile }
    {
      out = ""
      while (key != "" && (i = index($0, key)) > 0) {
        out = out substr($0, 1, i - 1) "[key]"
        $0 = substr($0, i + length(key))
      }
      print out $0
    }'
}

# api requests a path from the proxy with the key. -q (first) ignores any
# .curlrc and --noproxy '*' any proxy variables: the Authorization header
# must never leave the loopback connection.
api() {
  curl -q --noproxy '*' --silent --max-time "$REQUEST_TIMEOUT" -H "@$RUN/auth-header" "$@"
}

# lock serializes start and stop for this run directory: one mkdir wins, a
# second command refuses and names the lock. A lock left by a killed command
# is never cleared automatically (two commands clearing it at once could
# both proceed).
lock() {
  umask 077
  mkdir -p "$RUN" || die "cannot create $RUN"
  chmod 700 "$RUN"
  mkdir "$RUN/lock" 2>/dev/null ||
    die "another local-endpoint command holds $RUN/lock — if none is running, remove it: rmdir $RUN/lock"
  trap 'rmdir "$RUN/lock" 2>/dev/null' EXIT
  trap 'exit 1' HUP INT TERM
}

describe() {
  routes=$(api "http://127.0.0.1:$(cat "$RUN/port")/v1/models" |
    tr ',{}[]' '\n\n\n\n\n' | sed -n 's/^ *"id": *"\([^"]*\)".*/\1/p' | tr '\n' ' ')
  echo "running: http://127.0.0.1:$(cat "$RUN/port") (pid $(cat "$RUN/pid")); routes: ${routes% }; key file: $RUN/key"
}

install_litellm() {
  want=$(sha "$LOCK") || die "cannot hash $LOCK"
  if [ -x "$VENV/bin/litellm" ] && [ "$(cat "$VENV/.lock-sha256" 2>/dev/null)" = "$want" ]; then
    return 0
  fi
  rm -rf "$VENV"
  uv venv --quiet --python "$LITELLM_PYTHON" "$VENV" || die "uv venv failed"
  uv pip install --quiet --python "$VENV/bin/python" --require-hashes -r "$LOCK" ||
    die "hash-checked install from $LOCK failed"
  echo "$want" >"$VENV/.lock-sha256"
}

start() {
  [ "$HOST" = 127.0.0.1 ] || die "refusing host $HOST: the endpoint listens on 127.0.0.1 only"
  [ -n "${LITELLM_VERSION:-}" ] || die "LITELLM_VERSION is not set — run: make endpoint-up"
  [ -n "${LITELLM_PYTHON:-}" ] || die "LITELLM_PYTHON is not set — run: make endpoint-up"
  locked=$(sed -n 's/^litellm==\([^ ;\\]*\).*/\1/p' "$LOCK" | head -n 1)
  [ "$locked" = "$LITELLM_VERSION" ] ||
    die "the lock pins litellm ${locked:-nothing}, the Makefile pins $LITELLM_VERSION — run: make endpoint-lock"
  case "$PORT" in '' | *[!0-9]*) die "ENDPOINT_PORT must be a number" ;; esac

  # A leading zero would make shell arithmetic read the value as octal
  # ("08" fails after the proxy has started), so only plain positive
  # decimals are accepted.
  case "$TIMEOUT" in '' | 0* | *[!0-9]*) die "ENDPOINT_READY_TIMEOUT must be a positive whole number of seconds without leading zeros" ;; esac

  lock
  if running; then
    describe
    return 0
  fi
  clear_run_files
  install_litellm

  key="sk-$(od -An -tx1 -N16 /dev/urandom | tr -d ' \n')"
  [ "${#key}" -eq 35 ] || die "could not generate a key"
  printf '%s' "$key" >"$RUN/key"
  printf 'Authorization: Bearer %s\n' "$key" >"$RUN/auth-header"
  echo "$PORT" >"$RUN/port"

  LITELLM_MASTER_KEY="$key" nohup "$VENV/bin/litellm" --config "$CONFIG" \
    --host 127.0.0.1 --port "$PORT" >"$RUN/litellm.log" 2>&1 &
  pid=$!
  unset key
  echo "$pid" >"$RUN/pid"
  started_at "$pid" >"$RUN/started"

  # A wall-clock deadline: each probe is bounded by REQUEST_TIMEOUT, so a
  # stalled server costs at most one probe beyond it.
  deadline=$(($(date +%s) + TIMEOUT))
  code=
  while [ "$(date +%s)" -lt "$deadline" ]; do
    code=$(api -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/v1/models")
    [ "$code" = 200 ] && break
    kill -0 "$pid" 2>/dev/null || break
    sleep 1
  done
  if [ "${code:-}" != 200 ]; then
    echo "local-endpoint: LiteLLM did not answer on 127.0.0.1:$PORT within ${TIMEOUT}s; last log lines:" >&2
    tail -n 20 "$RUN/litellm.log" | redact >&2
    end_process "$pid"
    clear_run_files
    exit 1
  fi
  describe
}

stop() {
  lock
  if running; then
    end_process "$(cat "$RUN/pid")"
  fi
  clear_run_files
  rm -f "$RUN/litellm.log"
  echo "stopped"
}

status() {
  if running; then
    describe
    return 0
  fi
  echo "stopped"
  return 3
}

# model prints the Ollama model behind the "local" route, as the running
# proxy reports it in /v1/model/info: LiteLLM has already parsed the YAML,
# so comments and key order in the config cannot change the answer. Exactly
# one "local" route with an ollama_chat/<model> model is accepted.
model() {
  running || die "the endpoint is not running — run: make endpoint-up"
  api "http://127.0.0.1:$(cat "$RUN/port")/v1/model/info" | "$VENV/bin/python" -I -c '
import json, sys
try:
    routes = json.load(sys.stdin)["data"]
except (ValueError, KeyError, TypeError):
    sys.exit("local-endpoint: /v1/model/info did not return the route list")
local = [r for r in routes if isinstance(r, dict) and r.get("model_name") == "local"]
if len(local) != 1:
    sys.exit("local-endpoint: expected one route named local, found %d" % len(local))
model = (local[0].get("litellm_params") or {}).get("model")
prefix = "ollama_chat/"
if not isinstance(model, str) or not model.startswith(prefix) or model == prefix:
    sys.exit("local-endpoint: the local route must name an ollama_chat/<model>, got %r" % (model,))
print(model[len(prefix):])
'
}

case "${1:-}" in
start) start ;;
stop) stop ;;
status) status ;;
model) model ;;
*)
  echo "usage: local-endpoint.sh start|stop|status|model" >&2
  exit 2
  ;;
esac
