#!/usr/bin/env bash
# install.sh — download, verify, and unpack a chrome-agent engine release.
#
# Usage:  curl -fsSL <BASE_URL>/install.sh | bash
#         BASE_URL=https://get.example.com/chrome-agent ./install.sh
#
# It refuses to install a binary whose sha256 does not match the published SHA256SUMS — an
# unverified browser download is not something to run.
set -euo pipefail
BASE_URL="${BASE_URL:-https://hel1.your-objectstorage.com/publicassets/chrome-agent}"
DEST="${CHROME_AGENT_ENGINE_DIR:-$HOME/.local/share/chrome-agent/engine}"

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64)  ART="chrome-agent-engine-152.0.7948.0+fork.2-linux-x64.tar.gz" ;;
  Darwin-arm64)  ART="chrome-agent-engine-152.0.7948.0+fork.2-mac-arm64.tar.gz" ;;
  Darwin-x86_64)
    echo "only Apple Silicon (arm64) macOS is published — Intel Macs are not built." >&2; exit 1 ;;
  *) echo "unsupported platform: $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
echo "downloading $ART"
curl -fSL "$BASE_URL/$ART"        -o "$tmp/$ART"
curl -fSL "$BASE_URL/SHA256SUMS"  -o "$tmp/SHA256SUMS"

echo "verifying checksum"
( cd "$tmp" && grep "$ART" SHA256SUMS | { command -v sha256sum >/dev/null && sha256sum -c - || shasum -a 256 -c -; } ) \
  || { echo "CHECKSUM FAILED — refusing to install" >&2; exit 1; }

mkdir -p "$DEST"
tar xzf "$tmp/$ART" -C "$DEST" --strip-components=1
echo "installed to $DEST"
echo "point the client at it:  export CHROMIUM_SENDKEYS_OUT=$DEST"
