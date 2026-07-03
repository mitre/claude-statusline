#!/usr/bin/env bash
# Claude Code statusline — model, auth, branch, context bar, session time, churn
# Receives JSON session data via stdin

set -euo pipefail

INPUT=$(cat)

# Single jq call for all fields
eval "$(echo "$INPUT" | jq -r '
  @sh "MODEL=\(.model.display_name // "?")",
  @sh "CTX_PCT=\(.context_window.used_percentage // 0 | floor)",
  @sh "CTX_SIZE=\(.context_window.context_window_size // 200000)",
  @sh "LINES_ADD=\(.cost.total_lines_added // 0)",
  @sh "LINES_DEL=\(.cost.total_lines_removed // 0)",
  @sh "DURATION_MS=\(.cost.total_duration_ms // 0)",
  @sh "CWD=\(.workspace.current_dir // .cwd // "")",
  @sh "SID=\(.session_id // "")"
')"

# Build model label. Append "200k"/"1M"-style context size only if the
# display_name doesn't already state the context window (e.g. "Opus 4.8 (1M context)").
if (( CTX_SIZE >= 1000000 )); then
  CTX_LBL="$(( CTX_SIZE / 1000000 ))M"
else
  CTX_LBL="$(( CTX_SIZE / 1000 ))k"
fi
if [[ "$MODEL" != *ontext* ]]; then MODEL="${MODEL} ${CTX_LBL}"; fi

# --- Cache infrastructure ---
CACHE_DIR="/tmp/.claude-statusline-cache"
mkdir -p "$CACHE_DIR"
# auth/usage are account-wide (shared across sessions); branch/dirty are
# per-repo — key them by CWD so concurrent sessions in different repos
# don't clobber each other's cache.
CWD_KEY=$(md5 -qs "$CWD" 2>/dev/null || echo default)
AUTH_CACHE="$CACHE_DIR/auth"
BRANCH_CACHE="$CACHE_DIR/branch-$CWD_KEY"
DIRTY_CACHE="$CACHE_DIR/dirty-$CWD_KEY"

read_cache() {
  local file="$1" ttl="$2"
  if [[ -f "$file" ]] && [[ -s "$file" ]]; then
    local age=$(( $(date +%s) - $(stat -f%m "$file" 2>/dev/null || echo 0) ))
    if (( age < ttl )); then
      cat "$file"
      return 0
    fi
  fi
  return 1
}

# --- Auth (cached 300s — doesn't change mid-session) ---
if ! AUTH=$(read_cache "$AUTH_CACHE" 300); then
  if [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
    AUTH="API"
  elif security find-generic-password -s "Claude Code-credentials" >/dev/null 2>&1; then
    AUTH="Sub"
  else
    AUTH="?"
  fi
  echo "$AUTH" > "$AUTH_CACHE"
fi

# --- Git branch + dirty (cached 5s, sync on first miss then background) ---
# Use `git branch --show-current` (clean name, empty when detached) — NOT
# `git rev-parse --abbrev-ref HEAD`, which prints the literal "HEAD" in detached
# or mid-operation states. Fall back to a short SHA when detached. Never cache
# an empty value, and write with printf (no trailing newline).
git_branch() {
  local b sha
  b=$(git branch --show-current 2>/dev/null)
  if [[ -z "$b" ]]; then
    sha=$(git rev-parse --short HEAD 2>/dev/null) && b="@${sha}" || b=""
  fi
  printf '%s' "$b"
}

# Cache writes go through a temp file + atomic mv: a bare `> "$CACHE"` truncates
# the file the moment the (background) writer starts, so the SAME render's
# foreground read races it and sees an empty file — the dirty badge vanishes.
write_cache() { # file content
  printf '%s' "$2" > "$1.$$" && mv "$1.$$" "$1"
}

if ! BRANCH=$(read_cache "$BRANCH_CACHE" 5); then
  if [[ -n "$CWD" ]] && cd "$CWD" 2>/dev/null; then
    BRANCH=$(git_branch)
    if [[ -n "$BRANCH" ]]; then write_cache "$BRANCH_CACHE" "$BRANCH"; else BRANCH="?"; fi
    write_cache "$DIRTY_CACHE" "$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')" &
  else
    BRANCH="?"
  fi
else
  # Background refresh for next call
  (
    if [[ -n "$CWD" ]] && cd "$CWD" 2>/dev/null; then
      b=$(git_branch)
      if [[ -n "$b" ]]; then write_cache "$BRANCH_CACHE" "$b"; fi
      write_cache "$DIRTY_CACHE" "$(git status --porcelain 2>/dev/null | wc -l | tr -d ' ')"
    fi
  ) &
fi

# Sanitize: first line only, in case a stale multiline cache exists
BRANCH="${BRANCH%%$'\n'*}"
DIRTY=$(cat "$DIRTY_CACHE" 2>/dev/null || echo "0")
DIRTY="${DIRTY%%$'\n'*}"
DIRTY="${DIRTY:-0}"

# --- ANSI colors ---
RST='\033[0m'; B='\033[1m'; D='\033[2m'
RED='\033[31m'; GRN='\033[32m'; YLW='\033[33m'
BLU='\033[34m'; MAG='\033[35m'; CYN='\033[36m'
WHT='\033[37m'; BG_RED='\033[41m'; BG_YLW='\033[43m'; BLK='\033[30m'

# --- Subscription usage limits (cached 180s; only on Sub auth) ---
# stdin does NOT carry usage data — fetch from the OAuth /usage endpoint.
# Shows 5h + 7d pacing, and a loud badge when a plan limit is maxed AND
# extra-usage credits are enabled (i.e. new work is billing extra usage).
USAGE=""; XTRA=""
if [[ "$AUTH" == "Sub" ]]; then
  USAGE_CACHE="$CACHE_DIR/usage"
  if ! USAGE_JSON=$(read_cache "$USAGE_CACHE" 180); then
    TOKEN=$(security find-generic-password -s "Claude Code-credentials" -w 2>/dev/null \
      | python3 -c "import sys,json;print(json.load(sys.stdin).get('claudeAiOauth',{}).get('accessToken',''))" 2>/dev/null || echo "")
    USAGE_JSON=""
    if [[ -n "$TOKEN" ]]; then
      RESP=$(env curl -s --max-time 2 https://api.anthropic.com/api/oauth/usage \
        -H "Authorization: Bearer $TOKEN" -H "anthropic-beta: oauth-2025-04-20" 2>/dev/null || echo "")
      # Cache ONLY real usage payloads. {"error":...} is valid JSON too — caching
      # it made the // 0 defaults below fabricate a "5h 0% · week 0%" row.
      if echo "$RESP" | jq -e '.five_hour.utilization' >/dev/null 2>&1; then
        USAGE_JSON="$RESP"
        write_cache "$USAGE_CACHE" "$USAGE_JSON"
      else
        # Fetch failed (rate-limited/offline): serve the last GOOD payload even
        # past its TTL — stale truth beats fabricated zeros.
        USAGE_JSON=$(cat "$USAGE_CACHE" 2>/dev/null || echo "")
      fi
    fi
  fi

  # Render only when the payload has the expected shape — never default to 0%.
  if echo "$USAGE_JSON" | jq -e '.five_hour.utilization' >/dev/null 2>&1; then
    eval "$(echo "$USAGE_JSON" | jq -r '
      @sh "U5=\(.five_hour.utilization // 0 | floor)",
      @sh "U7=\(.seven_day.utilization // 0 | floor)",
      @sh "XU=\(.extra_usage.is_enabled // false)",
      @sh "CRED_MINOR=\(.spend.used.amount_minor // 0)",
      @sh "CRED_EXP=\(.spend.used.exponent // 2)",
      @sh "MAXACT=\([.limits[]? | select(.is_active==true) | .percent] | max // 0)"
    ')"
    # Local-time reset labels (today -> "1:30p"; other day -> "Mon 10:00a").
    RESETS=$(echo "$USAGE_JSON" | python3 -c '
import sys,json,datetime
d=json.load(sys.stdin)
def fmt(iso):
    if not iso: return ""
    try: t=datetime.datetime.fromisoformat(iso.replace("Z","+00:00")).astimezone()
    except Exception: return ""
    now=datetime.datetime.now().astimezone()
    h=t.hour%12 or 12; ap="a" if t.hour<12 else "p"
    tm=f"{h}:{t.minute:02d}{ap}"
    return tm if t.date()==now.date() else t.strftime("%a ")+tm
print("R5="+json.dumps(fmt(d.get("five_hour",{}).get("resets_at",""))))
print("R7="+json.dumps(fmt(d.get("seven_day",{}).get("resets_at",""))))
' 2>/dev/null) || RESETS=""
    eval "${RESETS:-}"
    : "${R5:=}"; : "${R7:=}"

    if (( U5>=80 )); then c5="$RED"; elif (( U5>=50 )); then c5="$YLW"; else c5="$GRN"; fi
    if (( U7>=80 )); then c7="$RED"; elif (( U7>=50 )); then c7="$YLW"; else c7="$GRN"; fi
    FIVE="${c5}5h ${U5}%${RST}"
    (( U5>=80 )) && [[ -n "$R5" ]] && FIVE+=" ${D}(resets ${R5})${RST}"
    WEEK="${c7}week ${U7}%${RST}"
    (( U7>=80 )) && [[ -n "$R7" ]] && WEEK+=" ${D}(resets ${R7})${RST}"
    USAGE="${FIVE} ${D}·${RST} ${WEEK}"
    # Extra-usage indicator rides the model line (frontline). Loud red badge
    # while actively burning credits; dim tally when enabled but within limits.
    if [[ "$XU" == "true" ]]; then
      CRED=$(awk "BEGIN{printf \"%.2f\", ${CRED_MINOR}/(10^${CRED_EXP})}")
      if (( U5>=100 || U7>=100 || MAXACT>=100 )); then
        XTRA="  ${BG_RED}${WHT}${B} ⚠ EXTRA USAGE \$${CRED} ${RST}"
      elif (( CRED_MINOR>0 )); then
        XTRA="${D} · extra \$${CRED}${RST}"
      fi
    fi
  fi
fi

# --- Context bar (10 segments, color-coded) ---
filled=$(( CTX_PCT / 10 ))
empty=$(( 10 - filled ))

if (( CTX_PCT >= 80 )); then
  BC="$RED"; CL="${RED}${B}${CTX_PCT}%${RST}"
elif (( CTX_PCT >= 50 )); then
  BC="$YLW"; CL="${YLW}${CTX_PCT}%${RST}"
else
  BC="$GRN"; CL="${GRN}${CTX_PCT}%${RST}"
fi

BAR=""
for ((i=0; i<filled; i++)); do BAR+="▓"; done
for ((i=0; i<empty; i++)); do BAR+="░"; done
BAR="${BC}${BAR}${RST}"

# --- Session duration ---
DUR=""
if (( DURATION_MS > 60000 )); then
  ts=$(( DURATION_MS / 1000 ))
  if (( ts >= 3600 )); then
    DUR="${D}$(( ts / 3600 ))h$(( (ts % 3600) / 60 ))m${RST}"
  else
    DUR="${D}$(( ts / 60 ))m$(( ts % 60 ))s${RST}"
  fi
fi

# --- Code churn ---
CHURN=""
(( LINES_ADD > 0 || LINES_DEL > 0 )) && \
  CHURN="${GRN}+${LINES_ADD}${RST}/${RED}-${LINES_DEL}${RST}"

# --- Dirty file count ---
DIRT=""
(( DIRTY > 0 )) && DIRT=" ${YLW}~${DIRTY}${RST}"

# --- Auth badge ---
AUTH_B=""
[[ "$AUTH" == "Sub" ]] && AUTH_B="${D} · ${RST}${GRN}Sub${RST}"
[[ "$AUTH" == "API" ]] && AUTH_B="${D} · ${RST}${YLW}API${RST}"

# --- Session badge (rides the model row: model/auth/session = this instance) ---
SESS=""
[[ -n "$SID" ]] && SESS="${D} · session ${SID:0:8}${RST}"

# --- Compact warning ---
WARN=""
(( CTX_PCT >= 85 )) && WARN=" ${BG_RED}${WHT}${B} /compact ${RST}"

# --- Metered-billing guard ---
# ANTHROPIC_API_KEY (or AUTH_TOKEN) overrides the subscription → per-token API charges.
# Checked live (NOT cached) so an accidental export shows up immediately.
APIWARN=""
if [[ -n "${ANTHROPIC_API_KEY:-}" || -n "${ANTHROPIC_AUTH_TOKEN:-}" ]]; then
  APIWARN=" ${BG_RED}${WHT}${B} ⚠ API KEY SET — METERED BILLING ${RST}"
fi

# --- Output (grouped rows; dim left label column explains the values) ---
# Grouping: row 1 = this Claude instance, row 2 = the workspace, rows 3-4 =
# gauges (session context vs account limits — distinct scopes, distinct rows),
# row 5 = ambient activity. Rows collapse when their value is empty.
#   model     <name> · <auth> · session <id8>   [+ ⚠ EXTRA USAGE / API-key alerts]
#   project   <~/path/to/cwd> · ⎇ <branch> ~<dirty>
#   context   <bar> <pct>                [+ /compact alert]
#   limits    5h % (resets …) · week %   (Sub only)
#   activity  <duration> · +added / -removed lines
lbl() { printf "${D}%-9s${RST}" "$1"; }
LADD=$(LC_ALL=en_US.UTF-8 printf "%'d" "$LINES_ADD" 2>/dev/null || echo "$LINES_ADD")
LDEL=$(LC_ALL=en_US.UTF-8 printf "%'d" "$LINES_DEL" 2>/dev/null || echo "$LINES_DEL")

ROWS="$(lbl model)${B}${CYN}${MODEL}${RST}${AUTH_B}${SESS}${XTRA}${APIWARN}"
# Workspace identity on one line: path · branch · dirty ("branch of WHAT").
PROJ="${B}${CWD/#$HOME/\~}${RST} ${D}·${RST} ${BLU}⎇ ${BRANCH}${RST}${DIRT}"
ROWS+=$'\n'"$(lbl project)${PROJ}"
ROWS+=$'\n'"$(lbl context)${BAR} ${CL}${WARN}"
[[ -n "$USAGE" ]] && ROWS+=$'\n'"$(lbl limits)${USAGE}"

ACT=""
[[ -n "$DUR" ]] && ACT="$DUR"
if (( LINES_ADD > 0 || LINES_DEL > 0 )); then
  [[ -n "$ACT" ]] && ACT+=" ${D}·${RST} "
  ACT+="${GRN}+${LADD}${RST}/${RED}-${LDEL}${RST} ${D}lines${RST}"
fi
[[ -n "$ACT" ]] && ROWS+=$'\n'"$(lbl activity)${ACT}"

printf '%b' "$ROWS"
