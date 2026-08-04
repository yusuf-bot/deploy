#!/bin/sh
# install.sh — deploy installation script
# Usage: curl -fsSL https://deploy.openexplorer.xyz/install.sh | sh
# Or:    sh scripts/install.sh [version]
# Or:    sh scripts/install.sh --local /path/to/deploy [version]
#
# Download order:
#   1. GitHub release (kept for the future; the repo does not publish releases yet)
#   2. Self-hosted asset https://deploy.openexplorer.xyz/deploy-linux-${ARCH}.tar.gz
#      (fallback — only available for linux/amd64)
#
# Environment overrides:
#   INSTALL_DIR       install prefix (default /usr/local/bin)
#   DEPLOY_DATA_DIR   deploy data dir (default $HOME/.deploy)
#   PRUNE             set to 0 to skip prune timer setup (default on)
#   PRUNE_KEEP        image tarballs to keep (default 3)
#   PRUNE_TIME        systemd OnCalendar spec (default "Mon *-*-* 04:30:00")

set -eu

OWNER="yusuf-bot"
REPO="deploy"
BINARY="deploy"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${1:-latest}"
LOCAL_BINARY=""
DEPLOY_DIR="${DEPLOY_DATA_DIR:-$HOME/.deploy}"

# Self-hosted fallback artifacts (linux/amd64 only, built locally)
SELF_HOSTED_URLS="
https://deploy.openexplorer.xyz/deploy-linux-${ARCH}.tar.gz
https://deploy.openexplorer.xyz/deploy_linux_${ARCH}.tar.gz
"

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

# Try GitHub releases first. The repo does not publish releases today, so this
# normally fails and the caller falls back to the self-hosted asset.
download_from_github() {
    if [ "$VERSION" = "latest" ]; then
        info "Fetching latest release..."
        LATEST=$(curl -fsSL "https://api.github.com/repos/$OWNER/$REPO/releases/latest" 2>/dev/null | grep '"tag_name"' | cut -d'"' -f4 || true)
        if [ -n "$LATEST" ]; then
            VERSION="$LATEST"
            ok "Latest release: $VERSION"
        else
            warn "Could not fetch latest version from GitHub"
        fi
    fi

    DOWNLOAD_URL="https://github.com/$OWNER/$REPO/releases/download/${VERSION}/${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
    info "Downloading $BINARY $VERSION ($OS/$ARCH) from GitHub..."
    if curl -fsSL "$DOWNLOAD_URL" -o "$TMPDIR/$BINARY.tar.gz" 2>/dev/null \
        && tar -xzf "$TMPDIR/$BINARY.tar.gz" -C "$TMPDIR" 2>/dev/null \
        && [ -f "$TMPDIR/$BINARY" ]; then
        ok "Downloaded $BINARY $VERSION"
        return 0
    fi
    warn "GitHub release download failed: $DOWNLOAD_URL"
    return 1
}

# Fall back to the self-hosted asset. Only linux/amd64 has one; every other
# platform gets a clear "build locally" message instead of a raw curl 404.
download_from_self_hosted() {
    if [ "$OS" != "linux" ] || [ "$ARCH" != "amd64" ]; then
        err "No self-hosted binary available for $OS/$ARCH (linux/amd64 and linux/arm64 are published)"
        printf "\n%s\n" "Build locally instead:"
        printf "  %s\n" "cd <path-to-deploy-repo>"
        printf "  %s\n" "go build -o deploy ."
        printf "  %s\n" "sudo cp deploy /usr/local/bin/deploy"
        printf "  %s\n" "deploy init"
        exit 1
    fi

    for URL in $SELF_HOSTED_URLS; do
        info "Downloading $BINARY from $URL..."
        if curl -fsSL "$URL" -o "$TMPDIR/$BINARY.tar.gz" 2>/dev/null \
            && tar -xzf "$TMPDIR/$BINARY.tar.gz" -C "$TMPDIR" 2>/dev/null \
            && [ -f "$TMPDIR/$BINARY" ]; then
            ok "Downloaded $BINARY (self-hosted)"
            # Fallback artifacts are built locally; report the real binary version
            VERSION=$("$TMPDIR/$BINARY" version 2>/dev/null || echo "self-hosted")
            return 0
        fi
        warn "Download failed: $URL"
    done
    return 1
}

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
    if download_from_github; then
        :
    elif download_from_self_hosted; then
        warn "Used self-hosted fallback binary ($BINARY $VERSION)"
    else
        err "Download failed — tried GitHub releases and self-hosted assets"
        exit 1
    fi
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

# Set up prune timer (Linux + systemd only; default ON)
if [ "$OS" = "linux" ] && command -v systemctl >/dev/null 2>&1; then
    if [ "${PRUNE:-1}" = "0" ]; then
        warn "Prune timer skipped (PRUNE=0)"
    else
        PRUNE_KEEP="${PRUNE_KEEP:-3}"
        PRUNE_TIME="${PRUNE_TIME:-Mon *-*-* 04:30:00}"
        PRUNE_SERVICE="/etc/systemd/system/deploy-prune.service"
        PRUNE_TIMER="/etc/systemd/system/deploy-prune.timer"

        if [ "$PRUNE_TIME" = "Mon *-*-* 04:30:00" ]; then
            info "Installing prune timer (weekly Mon 04:30, keep=$PRUNE_KEEP)..."
        else
            info "Installing prune timer (custom time '$PRUNE_TIME', keep=$PRUNE_KEEP)..."
        fi

        sudo tee "$PRUNE_SERVICE" >/dev/null << PRUNEUNIT
[Unit]
Description=deploy: prune old image tarballs
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
User=$DAEMON_USER
ExecStart=/usr/local/bin/deploy prune --keep $PRUNE_KEEP
PRUNEUNIT
        sudo tee "$PRUNE_TIMER" >/dev/null << PRUNETIMER
[Unit]
Description=deploy: weekly image tarball prune

[Timer]
OnCalendar=$PRUNE_TIME
Persistent=true
Unit=deploy-prune.service

[Install]
WantedBy=timers.target
PRUNETIMER
        sudo systemctl daemon-reload
        sudo systemctl enable deploy-prune.timer 2>/dev/null || true
        sudo systemctl start deploy-prune.timer 2>/dev/null || true
        ok "Prune timer installed (keep=$PRUNE_KEEP, time='$PRUNE_TIME')"
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
