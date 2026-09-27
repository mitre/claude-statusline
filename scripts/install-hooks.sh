#!/bin/sh
# Install the repo's fast-gate pre-commit hook (idempotent).
#
# Appends a marker-delimited block to .git/hooks/pre-commit that dispatches
# to the versioned gate script. Appending — never replacing — because other
# tools (beads) keep their own managed sections in the same file, and
# core.hooksPath would silently disable them.
set -u

HOOK_DIR=$(git rev-parse --git-path hooks) || {
  echo "not inside a git repository" >&2
  exit 1
}
HOOK="${HOOK_DIR}/pre-commit"
MARKER='# --- BEGIN claude-statusline fast gates ---'

if [ -f "$HOOK" ] && grep -qF "$MARKER" "$HOOK"; then
  echo "pre-commit fast-gate block already installed in ${HOOK}"
  exit 0
fi

if [ ! -f "$HOOK" ]; then
  printf '#!/usr/bin/env sh\n' > "$HOOK" || exit 1
fi

{
  printf '%s\n' "$MARKER"
  # Escaped so the rev-parse runs at COMMIT time inside the hook, not now.
  printf '%s\n' "sh \"\$(git rev-parse --show-toplevel)/scripts/githooks/pre-commit\" \"\$@\" || exit \$?"
  printf '%s\n' '# --- END claude-statusline fast gates ---'
} >> "$HOOK" || exit 1

chmod +x "$HOOK" || exit 1
echo "installed fast-gate pre-commit block into ${HOOK}"
