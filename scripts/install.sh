#!/bin/bash
set -e

VERSION=${1:-latest}
BINARY_NAME="revtunnel"
INSTALL_DIR="/usr/local/bin"
BASE_URL="https://github.com/oluu-web/revtunnel/releases/download/$VERSION"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case $ARCH in
  x86_64)  ARCH="amd64" ;;
  arm64)   ARCH="arm64" ;;
  aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case $OS in
  darwin|linux) ;;
  *) echo "Unsupported OS: $OS. Use the Windows installer instead."; exit 1 ;;
esac

FILENAME="revtunnel-$OS-$ARCH"
URL="$BASE_URL/$FILENAME"

echo "Downloading $FILENAME..."
curl -fsSL "$URL" -o "/tmp/$BINARY_NAME"
chmod +x "/tmp/$BINARY_NAME"

echo "Installing to $INSTALL_DIR (may require sudo)..."
sudo mv "/tmp/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"

echo "✓ Installed successfully!"
echo "  Run: revtunnel login --api-key <your-key>"