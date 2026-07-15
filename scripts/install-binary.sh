#!/bin/sh
# Download, checksum-verify, and install the claude-statusline release
# binary for this host. An unverified download is never installed — any
# verification failure refuses loudly. An existing binary is backed up
# beside itself (timestamped), matching the repo's live-gate convention.
set -u

VER="${1:?usage: install-binary.sh <version>}"
BIN="${HOME}/.claude/claude-statusline"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *)
    echo "unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

NAME="claude-statusline-${VER}-${OS}-${ARCH}.tar.gz"
BASE="https://github.com/mitre/claude-statusline/releases/download/v${VER}"

DL=$(mktemp -d) || exit 1
trap 'rm -rf "$DL"' EXIT

curl -fsSL --max-time 120 "${BASE}/${NAME}" -o "${DL}/${NAME}" || {
  echo "download failed: ${NAME}" >&2
  exit 1
}

WANT=$(curl -fsSL --max-time 30 "${BASE}/checksums.txt" | awk -v n="$NAME" '$2 == n { print $1 }')
[ -n "$WANT" ] || {
  echo "no checksum published for ${NAME}" >&2
  exit 1
}
if command -v sha256sum >/dev/null 2>&1; then
  GOT=$(sha256sum "${DL}/${NAME}" | awk '{ print $1 }')
else
  GOT=$(shasum -a 256 "${DL}/${NAME}" | awk '{ print $1 }')
fi
[ "$GOT" = "$WANT" ] || {
  echo "checksum mismatch for ${NAME} — refusing to install" >&2
  exit 1
}

tar -xzf "${DL}/${NAME}" -C "$DL" claude-statusline || {
  echo "extract failed: ${NAME}" >&2
  exit 1
}

mkdir -p "$(dirname "$BIN")"
if [ -e "$BIN" ]; then
  cp -p "$BIN" "${BIN}.backup-$(date +%Y%m%d-%H%M%S)" || exit 1
fi
chmod +x "${DL}/claude-statusline"
mv -f "${DL}/claude-statusline" "$BIN"
echo "installed ${BIN} ($("$BIN" --version 2>/dev/null))"
