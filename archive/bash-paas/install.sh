#!/bin/sh
# install.sh — install the deploy binary

set -e

BINDIR="${BINDIR:-/usr/local/bin}"

echo "Building deploy..."
cd "$(dirname "$0")"
go build -ldflags="-s -w" -o bin/deploy .

echo "Installing to $BINDIR/deploy..."
install -m 0755 bin/deploy "$BINDIR/deploy"

echo ""
echo "deploy installed successfully!"
echo ""
echo "Next steps:"
echo "  1. Run 'deploy init' to create ~/.deploy/"
echo "  2. Run 'deploy daemon' to start the daemon"
echo "     (or set up as a systemd service)"
echo "  3. Run 'deploy app create myapp --image nginx --port 8080'"
