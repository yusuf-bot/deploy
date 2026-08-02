#!/bin/sh
# install.sh — deploy installation script
# Usage: curl -fsSL https://deploy.openexplorer.xyz/install.sh | sh
# Or:    sh scripts/install.sh [version]
# Or:    sh scripts/install.sh --local /path/to/deploy [version]

set -eu

OWNER="yusuf-bot"
REPO="deploy"
BINARY="deploy"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${1:-latest}"
LOCAL_BINARY=""
DEPLOY_DIR="${DEPLOY_DATA_DIR:-$HOME/.deploy}"

# Parse --local flag
for arg in "$@"; do
    case "$arg" in
        --local=*)
            LOCAL_BINARY="${arg#*=}"
            ;;
        --local)
            # Next arg is the path
            ;;
    esac
done

# If --local was the second-to-last arg, the last arg is the path
if [ "${1:-}" = "--local" ] && [ -n "${2:-}" ]; then
    LOCAL_BINARY="$2"
    VERSION="${3:-latest}"
elif [ "${1:-}" != "--local" ]; then
    # Normal version argument
    if [ "${1:-}" != "" ] && [ "${1%%--*}" = "$1" ]; then
        VERSION="$1"
    fi
fi

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { printf "${BLUE}%s${NC}\n" "$1"; }
ok()    { printf "${GREEN}✓ %s${NC}\n" "$1"; }
warn()  { printf "${YELLOW}! %s${NC}\n" "$1"; }
err()   { printf "${RED}✗ %s${NC}\n" "$1"; }

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
    linux)  OS="linux" ;;
    darwin) OS="darwin" ;;
    *)      err "Unsupported OS: $OS (linux/darwin only)"; exit 1 ;;
esac

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)          err "Unsupported architecture: $ARCH (amd64/arm64 only)"; exit 1 ;;
esac

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

if [ -n "$LOCAL_BINARY" ]; then
    # Local install — copy the binary directly
    if [ ! -f "$LOCAL_BINARY" ]; then
        err "Local binary not found: $LOCAL_BINARY"
        exit 1
    fi
    info "Installing from local binary: $LOCAL_BINARY"
    cp "$LOCAL_BINARY" "$TMPDIR/$BINARY"
    VERSION_FALLBACK=$("$LOCAL_BINARY" version 2>/dev/null || echo "local")
    if [ "$VERSION" = "latest" ] || [ "$VERSION" = "--local" ]; then
        VERSION="$VERSION_FALLBACK"
    fi
    ok "Using $BINARY $VERSION"
else
    # Download from GitHub
    if [ "$VERSION" = "latest" ]; then
        info "Fetching latest release..."
        LATEST=$(curl -fsSL "https://api.github.com/repos/$OWNER/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | cut -d'"' -f4)
        if [ -n "$LATEST" ]; then
            VERSION="$LATEST"
            ok "Latest release: $VERSION"
        else
            warn "Could not fetch latest version, using 'latest' tag"
        fi
    fi

    DOWNLOAD_URL="https://github.com/$OWNER/$REPO/releases/download/${VERSION}/${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"

    info "Downloading $BINARY $VERSION ($OS/$ARCH)..."
    curl -fsSL "$DOWNLOAD_URL" -o "$TMPDIR/$BINARY.tar.gz"
    tar -xzf "$TMPDIR/$BINARY.tar.gz" -C "$TMPDIR"
    ok "Downloaded $BINARY $VERSION"
fi

# Install binary
if [ ! -w "$INSTALL_DIR" ]; then
    info "Installing to $INSTALL_DIR..."
    sudo cp "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
    sudo chmod 755 "$INSTALL_DIR/$BINARY"
else
    cp "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
    chmod 755 "$INSTALL_DIR/$BINARY"
fi
ok "$BINARY installed to $INSTALL_DIR/$BINARY"

# Set up systemd service (Linux only)
if [ "$OS" = "linux" ] && command -v systemctl >/dev/null 2>&1; then
    SERVICE_PATH="/etc/systemd/system/$BINARY.service"
    DAEMON_USER="${SUDO_USER:-${USER:-root}}"
    if [ "$DAEMON_USER" = "root" ]; then
        DOCKER_GROUP=""
    else
        DOCKER_GROUP="SupplementaryGroups=docker"
    fi
    DAEMON_UID=$(id -u "$DAEMON_USER" 2>/dev/null || echo "1000")
    if [ ! -f "$SERVICE_PATH" ]; then
        info "Creating systemd service..."
        sudo tee "$SERVICE_PATH" >/dev/null << SERVICEUNIT
[Unit]
Description=deploy daemon
After=docker.service
Requires=docker.service

[Service]
ExecStart=/usr/local/bin/deploy daemon
Restart=always
RestartSec=10
User=$DAEMON_USER
$DOCKER_GROUP

[Install]
WantedBy=multi-user.target
SERVICEUNIT
        sudo systemctl daemon-reload
        ok "systemd service created (running as $DAEMON_USER)"
    else
        ok "systemd service already exists"
    fi
fi

# Initialize
if [ ! -f "$DEPLOY_DIR/master.key" ]; then
    info "Initializing deploy environment..."
    "$INSTALL_DIR/$BINARY" init 2>/dev/null || warn "Run 'deploy init' manually"
    if [ -f "$DEPLOY_DIR/master.key" ]; then
        ok "Deploy environment initialized"
    fi
else
    ok "Deploy environment already initialized"
fi

# Start daemon (Linux with systemd)
if [ "$OS" = "linux" ] && command -v systemctl >/dev/null 2>&1; then
    if ! systemctl is-enabled --quiet "$BINARY.service" 2>/dev/null; then
        info "Starting deploy daemon..."
        sudo systemctl enable "$BINARY.service" 2>/dev/null || true
        sudo systemctl start "$BINARY.service" 2>/dev/null || true
        sleep 1
        if systemctl is-active --quiet "$BINARY.service"; then
            ok "Daemon running"
        else
            warn "Run 'sudo systemctl start deploy' to start the daemon"
        fi
    else
        sudo systemctl start "$BINARY.service" 2>/dev/null || true
        ok "Daemon running"
    fi
fi

# Verify
if "$INSTALL_DIR/$BINARY" version >/dev/null 2>&1; then
    VERSION_OUTPUT=$("$INSTALL_DIR/$BINARY" version 2>/dev/null || echo "$VERSION")
    printf "\n${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
    printf "${GREEN}  deploy $VERSION installed successfully${NC}\n"
    printf "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}\n"
    printf "\n${BLUE}Next steps:${NC}\n"
    printf "  ${YELLOW}1.${NC} Configure domains:     ${GREEN}deploy init${NC}\n"
    printf "  ${YELLOW}2.${NC} Deploy your first app:  ${GREEN}deploy up myapp${NC}\n"
    printf "  ${YELLOW}3.${NC} View status:            ${GREEN}deploy status${NC}\n"
    printf "  ${YELLOW}4.${NC} Read the docs:          ${BLUE}https://deploy.openexplorer.xyz/docs${NC}\n"
    printf "\n"
else
    err "Installation failed — binary not found at $INSTALL_DIR/$BINARY"
    exit 1
fi
