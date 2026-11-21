#!/bin/sh
# Developer-only live check: runs the installed Claude Code interactively
# against the local test endpoint (make endpoint-up) and keeps the statusline
# payload of one finished turn. Print mode (claude -p) never runs the
# statusline command, so the session runs in a private tmux server: start,
# one prompt, wait for the turn to end, /exit. Run it through make live-check.
#
#   ROUTE        mock or local (the endpoint route used as every model)
#   STATUSLINE   the statusline binary that renders the captured payload
#   VARS         extra variables for Claude Code: NAME=value ... (no spaces
#                in values; never evaluated as shell)
#   LIVE_CHECK_TIMEOUT  seconds for each wait (default 180)
#   LIVE_CHECK_OUT      parent of the output directories (default: the user
#                       cache dir's claude-statusline/live-check)
#
# Claude Code gets only an allowlisted environment (env -i), a throwaway HOME
# and CLAUDE_CONFIG_DIR seeded only to skip the first-run screens, and the
# endpoint key, which a launcher reads from the endpoint's key file inside
# the session: the key is never on a command line or in a kept file. The
# throwaway directories and the tmux server are removed on every exit path;
# the output directory (capture.json, rows.txt, claude-version.txt) is kept.
set -u
set -f

die() {
  echo "live-check: $*" >&2
  exit 1
}

HERE=$(cd "$(dirname "$0")" && pwd)
ENDPOINT="$HERE/local-endpoint.sh"
PROMPT="Reply with one short sentence."
ROUTE=${ROUTE:-}
STATUSLINE=${STATUSLINE:-}
VARS=${VARS:-}
TIMEOUT=${LIVE_CHECK_TIMEOUT:-180}

# Variables the helper sets itself; VARS may not replace them.
MANAGED="PATH TERM HOME CLAUDE_CONFIG_DIR DISABLE_AUTOUPDATER ANTHROPIC_BASE_URL ANTHROPIC_AUTH_TOKEN ANTHROPIC_MODEL ANTHROPIC_SMALL_FAST_MODEL ANTHROPIC_DEFAULT_HAIKU_MODEL"

case "$ROUTE" in
mock | local) ;;
*) die "ROUTE must be mock or local, got '$ROUTE'" ;;
esac
command -v tmux >/dev/null 2>&1 || die "tmux is missing — install tmux (Claude Code runs the statusline only in an interactive session)"
CLAUDE=$(command -v claude) || die "claude is not on PATH"
[ -f "$STATUSLINE" ] && [ -x "$STATUSLINE" ] ||
  die "STATUSLINE '$STATUSLINE' is not an executable file — run: make build"
case "$STATUSLINE" in /*) ;; *) STATUSLINE="$(pwd)/$STATUSLINE" ;; esac
# A leading zero would make shell arithmetic read the value as octal.
case "$TIMEOUT" in '' | 0* | *[!0-9]*) die "LIVE_CHECK_TIMEOUT must be a positive whole number of seconds without leading zeros" ;; esac

seen=" "
for v in $VARS; do
  case "$v" in
  *=*) ;;
  *) die "VARS entries must be NAME=value, got '$v'" ;;
  esac
  name=${v%%=*}
  # Spelled-out classes: a range like A-Z depends on the locale.
  upper=ABCDEFGHIJKLMNOPQRSTUVWXYZ
  case "$name" in
  '' | [!$upper]* | *[!${upper}0123456789_]*) die "VARS name '$name' must match ^[A-Z][A-Z0-9_]*\$" ;;
  esac
  for m in $MANAGED; do
    [ "$name" = "$m" ] && die "VARS cannot set $name: the helper sets it"
  done
  case "$seen" in *" $name "*) die "VARS sets $name twice" ;; esac
  seen="$seen$name "
done

sh "$ENDPOINT" status >/dev/null || die "the endpoint is not running — run: make endpoint-up"
RUN=$(sh "$ENDPOINT" run-dir) || die "cannot find the endpoint's run directory"
PY="$RUN/venv/bin/python"
[ -f "$RUN/key" ] && [ -f "$RUN/port" ] || die "the endpoint's key or port file is missing in $RUN — run: make endpoint-up"
URL="http://127.0.0.1:$(cat "$RUN/port")"

if [ "$ROUTE" = local ]; then
  model=$(sh "$ENDPOINT" model) || exit 1
  command -v ollama >/dev/null 2>&1 || die "ollama is missing — install Ollama: https://ollama.com/download"
  info=$(ollama show "$model" 2>&1) ||
    die "model $model is not available in Ollama ($info) — run: make endpoint-pull"
  # Capability names sit four spaces in under the two-space "Capabilities"
  # heading; deeper lines are a capability's own settings.
  printf '%s\n' "$info" | awk '
    /^  [^ ]/ { section = $1 }
    section == "Capabilities" && /^    [^ ]/ && $1 == "tools" { found = 1 }
    END { exit !found }' ||
    die "model $model does not list the tools capability (ollama show); Claude Code sends tools on every request — pick a model with tools in scripts/local-endpoint/litellm.yaml"
fi

cache_dir() {
  if [ "$(uname -s)" = Darwin ]; then
    echo "$HOME/Library/Caches"
  else
    echo "${XDG_CACHE_HOME:-$HOME/.cache}"
  fi
}
PARENT=${LIVE_CHECK_OUT:-$(cache_dir)/claude-statusline/live-check}
mkdir -p "$PARENT" || die "cannot create $PARENT"
OUT=$(mktemp -d "$PARENT/$(date +%Y%m%d-%H%M%S).XXXXXX") || die "cannot create an output directory in $PARENT"

TMP=
SOCK=
cleanup() {
  [ -n "$SOCK" ] && tmux -S "$SOCK" kill-server >/dev/null 2>&1
  [ -n "$TMP" ] && rm -rf "$TMP"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
umask 077
base=${TMPDIR:-/tmp}
TMP=$(mktemp -d "${base%/}/live-check.XXXXXX") || die "cannot create a temporary directory"
mkdir "$TMP/home" "$TMP/config" "$TMP/work" "$TMP/renders" || die "cannot create the throwaway directories"
WORK=$(cd "$TMP/work" && pwd -P)
# The tmux server's socket lives in the throwaway directory: private to this
# run, and removed with it.
SOCK="$TMP/tmux.sock"

# shq quotes one value for the generated sh scripts.
shq() {
  printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

# Every statusline run gets its own directory under renders/: the payload
# Claude Code sent, the rows STATUSLINE printed and its stderr. The directory
# is renamed ok.* only when STATUSLINE succeeded (failed.* otherwise), so a
# capture is never paired with another run's rows or a failed render.
cat >"$TMP/statusline.sh" <<EOF
#!/bin/sh
renders=$(shq "$TMP/renders")
d=\$(mktemp -d "\$renders/run.XXXXXX") || exit 1
cat >"\$d/capture.json" || exit 1
if $(shq "$STATUSLINE") <"\$d/capture.json" >"\$d/rows.txt" 2>"\$d/stderr.txt"; then
  mv "\$d" "\$renders/ok.\${d##*/run.}" && cat "\$renders/ok.\${d##*/run.}/rows.txt"
else
  mv "\$d" "\$renders/failed.\${d##*/run.}"
  exit 1
fi
EOF
# pick.py reads renders/: "started" succeeds once any render succeeded;
# "turn" prints the successful render of the finished turn (output tokens
# above zero; the most tokens, then the newest); "failed" prints the stderr
# of failed renders of the turn.
cat >"$TMP/pick.py" <<'EOF'
import json, os, sys

mode, root = sys.argv[1], sys.argv[2]


def output_tokens(path):
    try:
        with open(os.path.join(path, "capture.json")) as f:
            d = json.load(f)
    except (OSError, ValueError):
        return None
    cw = d.get("context_window") if isinstance(d, dict) else None
    n = cw.get("total_output_tokens") if isinstance(cw, dict) else None
    return n if isinstance(n, int) and not isinstance(n, bool) else None


runs = [os.path.join(root, name) for name in sorted(os.listdir(root))]
ok = [p for p in runs if os.path.basename(p).startswith("ok.")]
if mode == "started":
    sys.exit(0 if ok else 1)
if mode == "turn":
    done = [(output_tokens(p), os.path.getmtime(p), p) for p in ok]
    done = [r for r in done if r[0] is not None and r[0] > 0]
    if not done:
        sys.exit(1)
    print(max(done)[2])
    sys.exit(0)
if mode == "failed":
    for p in runs:
        n = output_tokens(p)
        if os.path.basename(p).startswith("failed.") and n is not None and n > 0:
            with open(os.path.join(p, "stderr.txt")) as f:
                sys.stdout.write("STATUSLINE failed on the turn's payload: " + f.read())
    sys.exit(0)
sys.exit("pick.py: unknown mode " + mode)
EOF
cat >"$TMP/stop.sh" <<EOF
#!/bin/sh
cat >/dev/null
: >$(shq "$TMP/turn-done")
EOF
cat >"$TMP/launcher.sh" <<EOF
ANTHROPIC_AUTH_TOKEN=\$(cat $(shq "$RUN/key")) || exit 1
export ANTHROPIC_AUTH_TOKEN
exec $(shq "$CLAUDE") --settings $(shq "$TMP/settings.json")
EOF
chmod 700 "$TMP/statusline.sh" "$TMP/stop.sh"

# The settings and the seed are JSON, written by Python so any path is
# encoded correctly. Claude Code runs the statusline and hook commands
# through a shell, so their paths are shell-quoted as well. The seed only
# skips the first-run screens: onboarding, the theme picker and the
# folder-trust prompt for the session's directory.
"$PY" -I - "$TMP" "$WORK" <<'EOF' || die "cannot write the session settings"
import json, os, shlex, sys
tmp, work = sys.argv[1], sys.argv[2]
settings = {
    "statusLine": {"type": "command", "command": shlex.quote(os.path.join(tmp, "statusline.sh")), "padding": 0},
    "hooks": {"Stop": [{"hooks": [{"type": "command", "command": shlex.quote(os.path.join(tmp, "stop.sh"))}]}]},
}
seed = {
    "hasCompletedOnboarding": True,
    "theme": "dark",
    "projects": {work: {"hasTrustDialogAccepted": True}},
}
with open(os.path.join(tmp, "settings.json"), "w") as f:
    json.dump(settings, f)
with open(os.path.join(tmp, "config", ".claude.json"), "w") as f:
    json.dump(seed, f)
EOF

set -- PATH="$PATH" TERM=xterm-256color HOME="$TMP/home" CLAUDE_CONFIG_DIR="$TMP/config" \
  DISABLE_AUTOUPDATER=1 ANTHROPIC_BASE_URL="$URL" \
  ANTHROPIC_MODEL="$ROUTE" ANTHROPIC_SMALL_FAST_MODEL="$ROUTE" ANTHROPIC_DEFAULT_HAIKU_MODEL="$ROUTE"
for v in $VARS; do
  set -- "$@" "$v"
done

env -i "$@" "$CLAUDE" --version >"$OUT/claude-version.txt" 2>&1 ||
  die "claude --version failed: $(cat "$OUT/claude-version.txt")"

# -f /dev/null: the private server loads none of the owner's tmux
# configuration (a remain-on-exit there would keep the session after /exit).
unset TMUX
tmux -S "$SOCK" -f /dev/null new-session -d -s live -x 200 -y 50 -c "$WORK" \
  env -i "$@" /bin/sh "$TMP/launcher.sh" || die "tmux could not start the session"

# wait_for runs the named check until it succeeds or TIMEOUT passes; it
# gives up at once when the session has ended without the check passing.
# Liveness is sampled before the check, so a session that ends between the
# two is never reported as ending early.
wait_for() {
  deadline=$(($(date +%s) + TIMEOUT))
  while :; do
    alive=yes
    tmux -S "$SOCK" has-session -t live 2>/dev/null || alive=
    "$@" && return 0
    if [ -z "$alive" ]; then
      echo "live-check: the Claude Code session ended early" >&2
      return 1
    fi
    [ "$(date +%s)" -lt "$deadline" ] || return 1
    sleep 1
  done
}
started() { "$PY" -I "$TMP/pick.py" started "$TMP/renders"; }
# The turn is over once the Stop hook has fired and a render of a payload
# with the turn's output tokens has succeeded.
turn_done() {
  [ -f "$TMP/turn-done" ] && "$PY" -I "$TMP/pick.py" turn "$TMP/renders" >/dev/null
}
ended() { ! tmux -S "$SOCK" has-session -t live 2>/dev/null; }

# fail reports a wait that did not finish, with any failed renders of the
# turn and the session's last screen.
fail() {
  echo "live-check: $1" >&2
  "$PY" -I "$TMP/pick.py" failed "$TMP/renders" >&2
  tmux -S "$SOCK" capture-pane -p -t live 2>/dev/null | grep -v '^[[:space:]]*$' | tail -n 15 >&2
  exit 1
}

wait_for started || fail "Claude Code did not run the statusline within ${TIMEOUT}s of starting"
tmux -S "$SOCK" send-keys -t live -l "$PROMPT"
tmux -S "$SOCK" send-keys -t live Enter
wait_for turn_done || fail "the turn did not finish with a statusline capture within ${TIMEOUT}s"
tmux -S "$SOCK" send-keys -t live -l "/exit"
tmux -S "$SOCK" send-keys -t live Enter
wait_for ended || fail "Claude Code did not exit within ${TIMEOUT}s of /exit"

# Keep the render of the finished turn (picked again: a later successful
# render may have landed before the session ended).
best=$("$PY" -I "$TMP/pick.py" turn "$TMP/renders") || die "no successful render of the finished turn"
cp "$best/capture.json" "$best/rows.txt" "$OUT/" || die "cannot copy the render into $OUT"
echo "output: $OUT"
cat "$OUT/rows.txt"
# The last row has no newline of its own.
[ -n "$(tail -c 1 "$OUT/rows.txt")" ] && echo
exit 0
