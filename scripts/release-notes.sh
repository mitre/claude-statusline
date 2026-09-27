#!/bin/sh
# Print CHANGELOG.md's section for one version — the release-notes source
# of record (goreleaser's git-log changelog is disabled). Fails loudly when
# the section is missing or empty: a release must never ship blank notes.
set -u

VER="${1:?usage: release-notes.sh <version> [changelog-path]}"
FILE="${2:-CHANGELOG.md}"

OUT=$(awk -v ver="$VER" '
  /^## \[/ { insec = (index($0, "## [" ver "] - ") == 1); next }
  insec { print }
' "$FILE") || exit 1

[ -n "$(printf '%s' "$OUT" | tr -d '[:space:]')" ] || {
  echo "no CHANGELOG section found for version ${VER} in ${FILE}" >&2
  exit 1
}
printf '%s\n' "$OUT"
