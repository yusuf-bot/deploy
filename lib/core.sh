#!/bin/bash
# deploy/lib/core.sh — Configuration, colors, helpers, nginx functions

# ── ANSI colors ──────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; DIM='\033[2m'; NC='\033[0m'
ok()   { echo -e " ${GREEN}[ok]${NC} $1"; }
info() { echo -e " ${CYAN}[..]${NC} $1"; }
warn() { echo -e " ${YELLOW}[!!]${NC} $1"; }
err()  { echo -e " ${RED}[!!]${NC} $1" >&2; }

# ── Config ───────────────────────────────────────────────────────────────────
DEPLOY_HOME="${DEPLOY_HOME:-$HOME/.deploy}"
DEPLOY_CONF="${DEPLOY_CONF:-$DEPLOY_HOME/deploy.conf}"

# Load config file if it exists
if [ -f "$DEPLOY_CONF" ]; then
  source "$DEPLOY_CONF"
elif [ -f "$HOME/.deploy/deploy.conf" ]; then
  source "$HOME/.deploy/deploy.conf"
elif [ -f "/etc/deploy.conf" ]; then
  source "/etc/deploy.conf"
fi

# Configuration (with defaults)
REGISTRY="${REGISTRY:-$DEPLOY_HOME/registry}"
NGINX_CONF="${NGINX_CONF:-/etc/nginx/sites-available/deploy}"
NGINX_DEV_CONF="${NGINX_DEV_CONF:-/etc/nginx/sites-available/deploy-dev}"
BASE_DOMAIN="${BASE_DOMAIN:-example.com}"
DEV_DOMAIN="${DEV_DOMAIN:-dev.${BASE_DOMAIN}}"
SSL_CERT="${SSL_CERT:-}"
SSL_KEY="${SSL_KEY:-}"
CUSTOM_DIR="${CUSTOM_DIR:-$DEPLOY_HOME/nginx-custom}"
LOG_DIR="${LOG_DIR:-$DEPLOY_HOME/logs}"
ENV_DIR="${ENV_DIR:-$DEPLOY_HOME/env}"
RELEASES_DIR="${RELEASES_DIR:-$DEPLOY_HOME/releases}"
PID_DIR="${PID_DIR:-$DEPLOY_HOME/pids}"
APPS_DIR="${APPS_DIR:-$DEPLOY_HOME/apps}"
MIN_PORT="${MIN_PORT:-8000}"
MAX_PORT="${MAX_PORT:-9000}"
DEV_MIN_PORT="${DEV_MIN_PORT:-9000}"
DEV_MAX_PORT="${DEV_MAX_PORT:-9900}"
HEALTH_RETRIES="${HEALTH_RETRIES:-30}"
HEALTH_INTERVAL="${HEALTH_INTERVAL:-1}"

# DNS config (optional)
DNS_PROVIDER="${DNS_PROVIDER:-}"
CLOUDFLARE_TOKEN="${CLOUDFLARE_TOKEN:-}"
CLOUDFLARE_ZONE="${CLOUDFLARE_ZONE:-}"

# Ensure directories exist
mkdir -p "$DEPLOY_HOME" "$CUSTOM_DIR" "$LOG_DIR" "$ENV_DIR" "$RELEASES_DIR" "$PID_DIR" "$APPS_DIR"
[ ! -f "$REGISTRY" ] && touch "$REGISTRY"

# ── PID Helpers ───────────────────────────────────────────────────────────────
# Persistent PID storage to fix "running but shows stopped" bug.

save_pid() {
  local name=$1 pid=$2
  echo "$pid" > "${PID_DIR}/${name}.pid"
}

load_pid() {
  local name=$1 pid_file="${PID_DIR}/${name}.pid"
  [ -f "$pid_file" ] && cat "$pid_file" || echo ""
}

clear_pid() {
  local name=$1
  rm -f "${PID_DIR}/${name}.pid"
}

# Check if a PID is alive (process exists)
pid_alive() {
  local pid=$1
  [ -n "$pid" ] && [ "$pid" != "running" ] && kill -0 "$pid" 2>/dev/null
}

# Get PID using multiple methods for reliability
get_pid() {
  local name=$1 port=${2:-$(get_app_port "$1")}
  local type=$(get_type "$name")
  local pid=""

  # Method 1: Check stored PID
  pid=$(load_pid "$name")
  if [ -n "$pid" ] && [ "$pid" != "running" ]; then
    if kill -0 "$pid" 2>/dev/null; then
      if lsof -p "$pid" -i TCP -s TCP:LISTEN 2>/dev/null | grep -q ":$port"; then
        echo "$pid"
        return 0
      fi
      # PID exists but maybe on different port — still alive
      echo "$pid"
      return 0
    fi
  fi

  # Method 2: Check lsof by port (handles orphaned/restarted + docker-proxy)
  pid=$(lsof -ti :"$port" 2>/dev/null | head -1)
  if [ -n "$pid" ]; then
    save_pid "$name" "$pid"
    echo "$pid"
    return 0
  fi

  # Method 3: Docker-specific status check (container running)
  if [ "$type" = "docker" ]; then
    local cid
    cid=$(docker ps -q -f "name=^/${name}$" 2>/dev/null)
    if [ -n "$cid" ]; then
      # Get actual PID from docker inspect
      local docker_pid
      docker_pid=$(docker inspect -f '{{.State.Pid}}' "$name" 2>/dev/null)
      if [ -n "$docker_pid" ] && [ "$docker_pid" != "0" ]; then
        save_pid "$name" "$docker_pid"
        echo "$docker_pid"
        return 0
      fi
      echo "running"
      return 0
    fi
  fi

  # Not running
  clear_pid "$name"
  echo ""
  return 1
}

is_running() {
  local result
  result=$(get_pid "$1")
  [ -n "$result" ]
}

get_dev_port() {
  local name=$1
  local port=$DEV_MIN_PORT
  # Check if already assigned (stored in app config)
  local existing=$(get_app_field "$name" "dev_port" 2>/dev/null || true)
  [ -n "$existing" ] && echo "$existing" && return 0

  # Find next available: check registry + lsof for conflicts
  while : ; do
    local in_registry=$(grep -qP "^$name\t$port\t" "$REGISTRY" 2>/dev/null && echo "1" || echo "0")
    local in_use=$(lsof -ti :"$port" 2>/dev/null | head -1)
    if [ "$in_registry" = "0" ] && [ -z "$in_use" ]; then
      echo "$port"
      return 0
    fi
    ((port++))
    [ "$port" -gt "$DEV_MAX_PORT" ] && { err "no dev ports available"; exit 1; }
  done
}

# ── Registry helpers ──────────────────────────────────────────────────────────
get_app_port() { grep "^$1\t" "$REGISTRY" | awk '$4=="app" {print $2}' | head -1; }
get_type()     { grep "^$1\t" "$REGISTRY" | awk '$4=="app" {print $3}' | head -1; }
get_path()     { grep "^$1\t" "$REGISTRY" | awk '$4=="app" {print $5}' | head -1; }
get_app_field() {
  local name=$1 field=$2
  local line=$(grep "^${name}\t" "$APPS_DIR/${name}/config" 2>/dev/null | grep "^${field}=" | head -1)
  echo "${line#*=}"
}

set_app_field() {
  local name=$1 field=$2 value=$3
  mkdir -p "$APPS_DIR/$name"
  local config_file="$APPS_DIR/$name/config"
  touch "$config_file"
  if grep -q "^${field}=" "$config_file" 2>/dev/null; then
    sed -i "s/^${field}=.*/${field}=${value}/" "$config_file"
  else
    echo "${field}=${value}" >> "$config_file"
  fi
}

next_port() {
  port=$MIN_PORT
  while grep -qP "^\S+\t$port\t" "$REGISTRY" 2>/dev/null || lsof -ti :"$port" &>/dev/null; do
    ((port++))
    if [ "$port" -gt "$MAX_PORT" ]; then
      err "no ports available in range $MIN_PORT-$MAX_PORT"
      exit 1
    fi
  done
  echo "$port"
}

# ── Nginx ─────────────────────────────────────────────────────────────────────
nginx_listen_block() {
  if [ -n "$SSL_CERT" ] && [ -n "$SSL_KEY" ]; then
    echo "    listen 443 ssl http2;"
    echo "    ssl_certificate ${SSL_CERT};"
    echo "    ssl_certificate_key ${SSL_KEY};"
  else
    echo "    # SSL not configured — set SSL_CERT and SSL_KEY"
  fi
}

websocket_block() {
  cat << 'WEB'
    # WebSocket support
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
WEB
}

default_server_block() {
  local name=$1 port=$2 ws=${3:-false}
  cat << BLOCK

server {
    server_name ${name}.${BASE_DOMAIN};
    listen 80;
$(nginx_listen_block)

    location / {
        proxy_pass http://127.0.0.1:${port}/;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_buffering off;
        proxy_read_timeout 120s;
$( [ "$ws" = "true" ] && websocket_block )
    }
}
BLOCK
}

dev_server_block() {
  local name=$1 port=$2 ws=${3:-false}
  cat << BLOCK

# Dev config for ${name}
server {
    server_name ${name}.${DEV_DOMAIN};
    listen 80;
$(nginx_listen_block)

    # Dev-specific: verbose logging, CORS, no cache
    add_header Access-Control-Allow-Origin "*";
    add_header X-Dev "true";

    location / {
        proxy_pass http://127.0.0.1:${port}/;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_buffering off;
        proxy_read_timeout 300s;
$( [ "$ws" = "true" ] && websocket_block )
    }
}
BLOCK
}

regen_nginx() {
  local name_filter=$1  # optional: only regen for one app
  [ "$1" = "--reset" ] && name_filter="" && shift

  info "regenerating nginx config..."
  cat > /tmp/deploy_nginx << 'NGINXEOF'
# Auto-generated by deploy
NGINXEOF

  grep -v '^#' "$REGISTRY" | grep -v '^$' | awk '$4=="app" {print $1, $2}' | sort -u | while read -r name port; do
    # If filtering by name, skip others
    [ -n "$name_filter" ] && [ "$name" != "$name_filter" ] && continue

    custom_file="${CUSTOM_DIR}/${name}.conf"
    if [ "$1" != "--reset" ] && [ -f "$custom_file" ]; then
      info "using custom block for $name"
      cat "$custom_file" >> /tmp/deploy_nginx
    else
      # Check per-app config for WebSocket flag
      local ws="false"
      local ws_val
      ws_val=$(get_app_field "$name" "websocket" 2>/dev/null)
      [ "$ws_val" = "true" ] && ws="true"

      # Check for additional domains
      local extra_domains
      extra_domains=$(get_app_field "$name" "domains" 2>/dev/null)

      default_server_block "$name" "$port" "$ws" >> /tmp/deploy_nginx
    fi
  done

  sudo cp /tmp/deploy_nginx "$NGINX_CONF"
  if sudo nginx -t 2>/dev/null; then
    sudo nginx -s reload && ok "nginx reloaded"
  else
    err "nginx config test failed"
    exit 1
  fi
}

health_check() {
  local name=$1 port=$2 retries=$3 interval=$4
  local url="http://127.0.0.1:${port}/"
  for i in $(seq 1 "$retries"); do
    if curl -sf -o /dev/null "$url" 2>/dev/null; then
      ok "$name responded after ${i}s"
      return 0
    fi
    sleep "$interval"
  done
  warn "$name did not respond within $((retries * interval))s — continuing anyway"
  return 1
}

# ── Usage ────────────────────────────────────────────────────────────────────
print_usage() {
  cat << 'EOF'
Usage: deploy <command> [options]

Commands:
  init                          Create default config and directories
  ls                            Alias for list

  App lifecycle:
  add <name> <type> <path>      Register a new app (auto-assigns port)
  remove|rm <name>              Unregister an app
  start <name> [-d]             Start an app (-d for background)
  stop <name>                   Stop an app (SIGTERM → wait → SIGKILL)
  restart <name>                Stop then start
  dev <name>                    Start in dev mode with hot reload
  dev:init <name> <tmpl>       Scaffold a new project (flask/express/node/static)
  dev:logs <name>              Tail dev mode logs
  dev:url <name> [--open]      Show or open dev URL
  logs <name>                   Tail app logs
  info <name>                   Show app details
  status [name]                 Show status for all or one app
  list|ls                       Show all apps and their status

  Deployment:
  up [path]                     Deploy from deploy.yml in current dir
  prod <name>                   Build and deploy via Docker Compose
  custom <name>                 Create/edit custom nginx snippet

  Config:
  config:set <name> KEY=VAL     Set an environment variable
  config:get <name> [KEY]       List all or one env var
  config:unset <name> KEY       Remove an environment variable
  config:encrypt <name>         Encrypt env file with GPG
  config:decrypt <name>         Decrypt env file with GPG
  nginx [--reset]               Regenerate nginx config
  rollback <name>               Rollback to previous deployment

  Environment setup:
  env:setup [name]              Generate .env.example from app config

  SSL:
  ssl <name>                    Provision Let's Encrypt SSL (requires certbot)
  ssl:renew                     Renew all Let's Encrypt certificates

  Other:
  uninstall                     Remove all apps and deploy data
  help                          Show this message

Types: laravel | flask | node | static | docker | custom

App config (deploy.yml):
  Place a deploy.yml in your app's root directory. Supported keys:
    name, type, start, dev, port, websocket, domains, health.path, env.*

Config file:
  Settings in $DEPLOY_CONF, ~/.deploy/deploy.conf, or /etc/deploy.conf

Environment variables:
  DEPLOY_HOME       Working directory (default: ~/.deploy)
  BASE_DOMAIN       Domain suffix for subdomains (default: example.com)
  DEV_DOMAIN        Dev domain suffix (default: dev.$BASE_DOMAIN)
  SSL_CERT          Path to SSL certificate (optional)
  SSL_KEY           Path to SSL key (optional)
  NGINX_CONF        Output nginx config path
  DNS_PROVIDER      DNS provider: cloudflare | manual (default: manual)
  CLOUDFLARE_TOKEN  Cloudflare API token (for dns automation)
  CLOUDFLARE_ZONE   Cloudflare zone name
  MIN_PORT          Minimum port number (default: 8000)
  MAX_PORT          Maximum port number (default: 9000)

Examples:
  deploy init
  deploy add myapp flask /home/user/myapp
  deploy config:set myapp DATABASE_URL=postgres://...
  deploy start myapp -d
  deploy ls

  # Using deploy.yml:
  cd ~/myapp
  deploy up

  # Dev mode:
  deploy dev myapp
  deploy dev:init myapp express
  deploy dev:logs myapp
  deploy dev:url myapp --open
EOF
}
