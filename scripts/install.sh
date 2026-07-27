#!/bin/sh
# install.sh — deploy installation script
# Usage: curl -fsSL https://deploy.sh | sh
# Or:    sh scripts/install.sh [version]

set -eu

REPO="deploy"
BINARY="deploy"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${1:-latest}"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
    linux)  OS="linux" ;;
    darwin) OS="macos" ;;
    *)      echo "Unsupported OS: $OS"; exit 1 ;;
esac

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)          echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Resolve version to download URL
if [ "$VERSION" = "latest" ]; then
    echo "Fetching latest release..."
    URL="https://github.com/$REPO/$REPO/releases/latest/download/${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
    # Actually fetch the real URL from GitHub API
    LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | cut -d'"' -f4)
    if [ -n "$LATEST" ]; then
        VERSION="$LATEST"
    fi
fi

DOWNLOAD_URL="https://github.com/$REPO/$REPO/releases/download/${VERSION}/${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"

echo "Downloading $BINARY $VERSION ($OS/$ARCH)..."
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

curl -fsSL "$DOWNLOAD_URL" -o "$TMPDIR/$BINARY.tar.gz"
tar -xzf "$TMPDIR/$BINARY.tar.gz" -C "$TMPDIR"

# Install
if [ ! -w "$INSTALL_DIR" ]; then
    echo "Installing to $INSTALL_DIR (requires sudo)..."
    sudo cp "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
    sudo chmod 755 "$INSTALL_DIR/$BINARY"
else
    cp "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
    chmod 755 "$INSTALL_DIR/$BINARY"
fi

echo "deploy $VERSION installed to $INSTALL_DIR/$BINARY"
"$INSTALL_DIR/$BINARY" version
