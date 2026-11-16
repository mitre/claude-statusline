#!/bin/sh
# Publish a release: the version's CHANGELOG section becomes the release
# notes, and goreleaser builds, archives, checksums and uploads.
#
# The notes file lives outside dist/: `goreleaser release --clean` deletes
# dist/ before its changelog step reads --release-notes, which then fails
# the release. A version without notes refuses before goreleaser runs.
# Runs from the repository root (the release workflow's `make release`).
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
VER=$(sh "${SCRIPT_DIR}/plugin-version.sh" .claude-plugin/plugin.json)

NOTES=$(mktemp "${TMPDIR:-/tmp}/claude-statusline-notes.XXXXXX")
trap 'rm -f "$NOTES"' EXIT

# release-notes.sh fails for a missing or empty section.
sh "${SCRIPT_DIR}/release-notes.sh" "$VER" > "$NOTES" || {
  echo "no CHANGELOG notes for ${VER} — refusing to release without notes" >&2
  exit 1
}

goreleaser release --clean --release-notes="$NOTES"
