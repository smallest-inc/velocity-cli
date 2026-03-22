#!/bin/bash
set -euo pipefail

REPO="smallest-inc/velocity-cli"
INSTALL_DIR="/usr/local/bin"

echo "Installing vctl..."

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin|linux) ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect arch
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d '"' -f 4)
if [ -z "$VERSION" ]; then
  echo "Failed to determine latest version"
  exit 1
fi

URL="https://github.com/${REPO}/releases/download/${VERSION}/vctl_${VERSION#v}_${OS}_${ARCH}.tar.gz"
echo "Downloading vctl ${VERSION} for ${OS}/${ARCH}..."

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

curl -fsSL "$URL" | tar xz -C "$TMPDIR"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMPDIR/vctl" "$INSTALL_DIR/vctl"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "$TMPDIR/vctl" "$INSTALL_DIR/vctl"
fi

chmod +x "${INSTALL_DIR}/vctl"
echo "vctl ${VERSION} installed to ${INSTALL_DIR}/vctl"
echo ""
echo "Get started:"
echo "  vctl auth login --token <your-pat>"
