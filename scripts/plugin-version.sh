#!/bin/sh
# The ONE place the plugin-manifest version extraction lives. Consumers:
# sync-binary.sh, commands/setup.md, release-preflight.sh. Works on any
# manifest carrying a "version" key (plugin.json, marketplace.json).
set -u

FILE="${1:?usage: plugin-version.sh <manifest.json>}"
VER=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$FILE" | head -1)
[ -n "$VER" ] || {
  echo "no version found in ${FILE}" >&2
  exit 1
}
printf '%s\n' "$VER"
