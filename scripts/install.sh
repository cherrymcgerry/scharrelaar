#!/bin/sh
# Installs the scharrel binary for the current user.
#   curl -fsSL https://get.scharrelaar.dev | sh
# Until that domain exists, use the GitHub release URL directly:
#   curl -fsSL https://github.com/cherrymcgerry/scharrelaar/releases/latest/download/install.sh | sh
set -eu

REPO="cherrymcgerry/scharrelaar"
VERSION="${SCHARREL_VERSION:-latest}"
BIN_DIR="${SCHARREL_BIN_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  linux) ;;
  *) echo "this script is for Linux; on Windows run scripts/install.ps1" >&2; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n 1)
fi

url="https://github.com/$REPO/releases/download/v$VERSION/scharrelaar_${VERSION}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url" | tar -xz -C "$tmp"

mkdir -p "$BIN_DIR"
mv "$tmp/scharrel" "$BIN_DIR/scharrel"
chmod +x "$BIN_DIR/scharrel"

echo "installed: $BIN_DIR/scharrel (v$VERSION)"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "add it to your PATH: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac

# Under "curl | sh" stdin is the script itself, so setup reads from the terminal.
if [ -z "${SCHARREL_NO_SETUP:-}" ]; then
  if [ -r /dev/tty ]; then
    exec "$BIN_DIR/scharrel" setup </dev/tty
  fi
  echo "run '$BIN_DIR/scharrel setup' to configure it"
fi
