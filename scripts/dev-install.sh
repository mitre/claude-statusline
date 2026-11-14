#!/bin/sh
# Install a local build as the dev copy of the status line:
# ~/.claude/claude-statusline-dev. Only sessions whose settings point at that
# path run it (this repo's .claude/settings.local.json); release copies
# (Homebrew's bin, the plugin's ~/.claude/claude-statusline) are never touched.
#
# One install at a time (a lock directory beside the dev copy, never
# reclaimed automatically). The previous dev copy is backed up beside itself
# (timestamped); the build is copied into a freshly created staging file and
# renamed into place, so a render never sees a partial file; the installed
# sha256 must equal the build's; the newest 3 backups are kept.
set -u

SRC="${1:?usage: dev-install.sh <built-binary>}"
DEST="${HOME}/.claude/claude-statusline-dev"
DIR=$(dirname "$DEST")
NAME=$(basename "$DEST")
LOCK="${DEST}.lock"
KEEP=3

[ -f "$SRC" ] && [ -x "$SRC" ] || {
  echo "no build at ${SRC} — run: make build (or make check)" >&2
  exit 1
}

# sha prints the file's sha256, or fails: a missing or failing hash tool
# must never yield an empty digest that compares equal to another.
sha() {
  if command -v sha256sum >/dev/null 2>&1; then
    out=$(sha256sum "$1") || return 1
  else
    out=$(shasum -a 256 "$1") || return 1
  fi
  d=${out%% *}
  case "$d" in
    *[!0-9a-f]* | '') return 1 ;;
  esac
  [ "${#d}" -eq 64 ] || return 1
  printf '%s\n' "$d"
}

mkdir -p "$DIR" || exit 1

# The lock is never reclaimed automatically: two installers clearing a stale
# lock at once could both proceed. Every exit path below releases it; only a
# killed install leaves it behind, and the owner removes it by hand.
mkdir "$LOCK" 2>/dev/null || {
  echo "another dev install holds ${LOCK}; if none is running, remove it: rmdir ${LOCK}" >&2
  exit 1
}
STAGE=""
trap '[ -z "$STAGE" ] || rm -f "$STAGE"; rmdir "$LOCK" 2>/dev/null' EXIT
trap 'exit 1' HUP INT TERM

WANT=$(sha "$SRC") || {
  echo "cannot compute the sha256 of ${SRC} — refusing to install" >&2
  exit 1
}

if [ -e "$DEST" ]; then
  # Within one second the names run T, T-01 … T-99, each above the highest
  # suffix still present for T, so a pruned name is never reused and the
  # names sort in install order; a 100th would sort out of order, so refuse.
  base="${DEST}.backup-$(date +%Y%m%d-%H%M%S)"
  n=-1
  for f in "$base" "$base"-[0-9][0-9]; do
    [ -e "$f" ] || continue
    k=0
    [ "$f" = "$base" ] || k=$((1${f##*-} - 100))
    [ "$k" -gt "$n" ] && n=$k
  done
  [ "$n" -lt 99 ] || {
    echo "99 dev installs within one second already backed up — retry in a second" >&2
    exit 1
  }
  backup="$base"
  [ "$n" -lt 0 ] || backup="${base}-$(printf '%02d' $((n + 1)))"
  cp -p "$DEST" "$backup" || exit 1
fi

# mktemp creates a new regular file, so a planted name cannot redirect
# the copy, and concurrent installs never share a staging file.
STAGE=$(mktemp "${DEST}.new.XXXXXX") || exit 1
cp "$SRC" "$STAGE" || exit 1
chmod 755 "$STAGE" || exit 1
[ "$(sha "$STAGE")" = "$WANT" ] || {
  echo "staged copy does not match ${SRC} — refusing to install" >&2
  exit 1
}
mv -f "$STAGE" "$DEST" || exit 1
STAGE=""
[ "$(sha "$DEST")" = "$WANT" ] || {
  echo "installed ${DEST} does not match ${SRC}" >&2
  exit 1
}

# Keep the newest $KEEP backups; the timestamped names sort oldest first.
backups=$(find "$DIR" -maxdepth 1 -name "${NAME}.backup-*" | sort) || exit 1
count=$(printf '%s\n' "$backups" | grep -c .)
if [ "$count" -gt "$KEEP" ]; then
  printf '%s\n' "$backups" | head -n $((count - KEEP)) |
    while IFS= read -r old; do rm -f "$old"; done
fi

echo "installed ${DEST} sha256 ${WANT} ($("$DEST" --version 2>/dev/null))"
