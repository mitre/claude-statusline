#!/bin/sh
# Release preflight: refuse to publish a malformed or inconsistent tag.
# Run from the repo root with the tag name (the release workflow's first
# job). Any v* tag used to ship a public release unchecked — rc tags,
# version mismatches, off-main commits, missing release notes.
set -u

TAG="${1:?usage: release-preflight.sh <tag>}"
# Overridable so tests can verify against a local branch; the default is
# the authoritative remote main (same seam idea as TAP_URL).
MAIN_REF="${MAIN_REF:-origin/main}"
SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

fail() {
  echo "preflight: $1" >&2
  exit 1
}

echo "$TAG" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' \
  || fail "tag '${TAG}' is not vMAJOR.MINOR.PATCH"
VER="${TAG#v}"

PLUGIN_VER=$(sh "${SCRIPT_DIR}/plugin-version.sh" .claude-plugin/plugin.json) \
  || fail "cannot read .claude-plugin/plugin.json version"
[ "$PLUGIN_VER" = "$VER" ] \
  || fail "plugin.json version '${PLUGIN_VER}' does not match tag version '${VER}'"

MARKET_VER=$(sh "${SCRIPT_DIR}/plugin-version.sh" .claude-plugin/marketplace.json) \
  || fail "cannot read .claude-plugin/marketplace.json version"
[ "$MARKET_VER" = "$VER" ] \
  || fail "marketplace.json version '${MARKET_VER}' does not match tag version '${VER}'"

grep -q "^## \[${VER}\] - " CHANGELOG.md \
  || fail "CHANGELOG.md has no '## [${VER}] - <date>' section"
sh "${SCRIPT_DIR}/release-notes.sh" "$VER" >/dev/null \
  || fail "CHANGELOG.md section for ${VER} is empty"

TAG_COMMIT=$(git rev-parse -q --verify "refs/tags/${TAG}^{commit}") \
  || fail "tag '${TAG}' not found in this clone"
git merge-base --is-ancestor "$TAG_COMMIT" "$MAIN_REF" \
  || fail "tagged commit ${TAG_COMMIT} is not on ${MAIN_REF#origin/} (${MAIN_REF})"

echo "preflight OK: ${TAG} — plugin.json, marketplace.json, CHANGELOG section, and ${MAIN_REF} ancestry all aligned"
