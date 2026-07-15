#!/bin/sh
# SessionStart hook: keep the installed binary version-pinned to the plugin.
# - Never touches a missing binary (setup not run yet) or a dev build (a
#   developer's own HEAD build always takes precedence).
# - Downloads in the background — session start is never blocked.
# - Prints NOTHING: SessionStart stdout is injected into the session context.
set -u

BIN="${HOME}/.claude/claude-statusline"
[ -x "$BIN" ] || exit 0

PLUGIN_VER=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json" | head -1)
[ -n "$PLUGIN_VER" ] || exit 0

BIN_VER=$("$BIN" --version 2>/dev/null | awk '{ print $2 }')
[ -n "$BIN_VER" ] || exit 0
[ "$BIN_VER" = "dev" ] && exit 0
[ "$BIN_VER" = "$PLUGIN_VER" ] && exit 0

LOCK="${HOME}/.claude/.claude-statusline-sync.lock"
# Clear a stale lock from an abnormally terminated prior run.
if [ -d "$LOCK" ] && [ -n "$(find "$LOCK" -maxdepth 0 -mmin +10 2>/dev/null)" ]; then
  rmdir "$LOCK" 2>/dev/null
fi
mkdir "$LOCK" 2>/dev/null || exit 0

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
(
  trap 'rmdir "$LOCK" 2>/dev/null' EXIT
  sh "${SCRIPT_DIR}/install-binary.sh" "$PLUGIN_VER"
) >/dev/null 2>&1 &

exit 0
