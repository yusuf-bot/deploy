#!/bin/bash
# deploy/lib/yml.sh — deploy.yml parser
#
# Parses a flat YAML subset into environment variables.
# Supports:
#   key: value           → export yml_key="value"
#   websocket: true      → export yml_websocket="true"
#   health.path: /health → export yml_health_path="/health"
#   env:                 → prefix with env_.
#     KEY: val           → export yml_env_KEY="val"
#
# Arrays are comma-joined:
#   domains: a, b, c     → export yml_domains="a,b,c"

# Parse a deploy.yml file, exports variables as yml_*
parse_deploy_yml() {
  local file=$1
  [ ! -f "$file" ] && return 1

  local section=""
  local IFS_backup="$IFS"

  while IFS= read -r line || [ -n "$line" ]; do
    # Skip comments and blank lines
    [[ "$line" =~ ^[[:space:]]*# ]] && continue
    [[ "$line" =~ ^[[:space:]]*$ ]] && continue

    # Detect section headers (e.g., "env:" or "health:")
    if [[ "$line" =~ ^[a-zA-Z_][a-zA-Z0-9_-]*:[[:space:]]*$ ]]; then
      section="${line%%:*}"
      continue
    fi

    # Indented key-value in a section (e.g., "  DATABASE_URL: postgres://...")
    if [[ "$line" =~ ^[[:space:]]+[a-zA-Z_][a-zA-Z0-9_-]*:[[:space:]]* ]]; then
      local key val
      key=$(echo "$line" | sed 's/^[[:space:]]*//' | cut -d: -f1 | xargs)
      val=$(echo "$line" | sed 's/^[[:space:]]*[^:]*:[[:space:]]*//' | sed 's/^"//;s/"$//' | xargs)
      [ -z "$key" ] && continue
      if [ -n "$section" ]; then
        export "yml_${section}_${key}=${val}"
      fi
      continue
    fi

    # Top-level key: value
    if [[ "$line" =~ ^[a-zA-Z_][a-zA-Z0-9_-]*: ]]; then
      local key val
      key=$(echo "$line" | cut -d: -f1 | xargs)
      val=$(echo "$line" | sed "s/^[^:]*:[[:space:]]*//" | sed 's/^"//;s/"$//' | xargs)
      [ -z "$key" ] && continue

      # Handle dots in key names (e.g., health.path → yml_health_path)
      local export_key="yml_${key//./_}"
      export "${export_key}=${val}"
      section=""
    fi
  done < "$file"

  return 0
}

# Get a single value from deploy.yml
parse_yml_value() {
  local file=$1 key=$2
  [ ! -f "$file" ] && return 1
  # Simple grep-based extraction for single values
  local val
  val=$(grep -E "^${key}:" "$file" 2>/dev/null | sed "s/^${key}:[[:space:]]*//" | head -1)
  echo "$val"
}

# ── deploy up: deploy from current directory ──────────────────────────────────

cmd_up() {
  local dir="${1:-$(pwd)}"
  local yml_file="$dir/deploy.yml"

  if [ ! -f "$yml_file" ]; then
    err "no deploy.yml found in $dir"
    err "create one or use: deploy add <name> <type> <path>"
    exit 1
  fi

  info "found deploy.yml in $dir"
  parse_deploy_yml "$yml_file"

  # Extract settings
  local name="${yml_name:-}"
  local type="${yml_type:-}"
  local ws="${yml_websocket:-false}"

  [ -z "$name" ] && { err "deploy.yml missing 'name'"; exit 1; }
  [ -z "$type" ] && { err "deploy.yml missing 'type'"; exit 1; }

  # Register if not already
  if ! grep -q "^${name}\t" "$REGISTRY" 2>/dev/null; then
    info "registering $name ($type)..."
    cmd_add "$name" "$type" "$dir"
  else
    info "$name already registered"
  fi

  # Set custom start command if defined
  local start_cmd="${yml_start:-}"
  if [ -n "$start_cmd" ]; then
    set_app_field "$name" "start_cmd" "$start_cmd"
  fi

  # Set websocket flag
  set_app_field "$name" "websocket" "$ws"

  # Set additional domains
  local domains="${yml_domains:-}"
  [ -n "$domains" ] && set_app_field "$name" "domains" "$domains"

  # Load env vars from deploy.yml
  local env_keys
  env_keys=$(compgen -v | grep "^yml_env_" 2>/dev/null || true)
  for var in $env_keys; do
    local key="${var#yml_env_}"
    local val="${!var}"
    cmd_config_set "$name" "${key}=${val}" 2>/dev/null || true
  done

  # Create DNS record if provider configured
  if [ -n "$DNS_PROVIDER" ]; then
    dns_add_record "${name}.${BASE_DOMAIN}" "A" ""
  fi

  # Start the app
  info "starting $name..."
  cmd_start "$name" "-d"

  # Regenerate nginx
  regen_nginx

  # SSL (optional)
  if command -v certbot &>/dev/null; then
    cmd_ssl "$name" 2>/dev/null || true
  fi

  ok "$name is deployed → https://${name}.${BASE_DOMAIN}"
}
