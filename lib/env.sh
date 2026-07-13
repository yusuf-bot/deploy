#!/bin/bash
# deploy/lib/env.sh — Environment variable management

# Load env vars for an app (supports plaintext and GPG-encrypted files)
load_env() {
  local name=$1 env_file="${ENV_DIR}/${name}.env" gpg_file="${ENV_DIR}/${name}.env.gpg"
  if [ -f "$env_file" ]; then
    while IFS='=' read -r key val || [ -n "$key" ]; do
      [[ "$key" =~ ^# || -z "$key" ]] && continue
      export "${key}=${val}"
    done < "$env_file"
  elif [ -f "$gpg_file" ]; then
    if command -v gpg &>/dev/null; then
      local decrypted
      decrypted=$(gpg --quiet --decrypt "$gpg_file" 2>/dev/null)
      if [ -n "$decrypted" ]; then
        while IFS='=' read -r key val || [ -n "$key" ]; do
          [[ "$key" =~ ^# || -z "$key" ]] && continue
          export "${key}=${val}"
        done <<< "$decrypted"
      fi
    fi
  fi
}

cmd_config_set() {
  local name=$1 kv=$2
  [ -z "$name" ] && { err "Usage: deploy config:set <name> KEY=VAL"; exit 1; }
  [ -z "$kv" ] && { err "Usage: deploy config:set <name> KEY=VAL"; exit 1; }
  grep -q "^$name\t" "$REGISTRY" 2>/dev/null || { err "app '$name' not found"; exit 1; }

  key="${kv%%=*}"
  val="${kv#*=}"
  env_file="${ENV_DIR}/${name}.env"

  if [ -f "$env_file" ]; then
    grep -v "^${key}=" "$env_file" > /tmp/deploy_env_$$ 2>/dev/null || true
    mv /tmp/deploy_env_$$ "$env_file"
  fi
  echo "${key}=${val}" >> "$env_file"
  ok "set ${key} for ${name}"
}

cmd_config_get() {
  local name=$1 key=$2
  [ -z "$name" ] && { err "Usage: deploy config:get <name> [KEY]"; exit 1; }
  env_file="${ENV_DIR}/${name}.env"
  [ ! -f "$env_file" ] && { err "no config found for $name"; exit 1; }
  if [ -n "$key" ]; then
    grep "^${key}=" "$env_file" || { err "key not found: $key"; exit 1; }
  else
    cat "$env_file"
  fi
}

cmd_config_unset() {
  local name=$1 key=$2
  [ -z "$name" ] && { err "Usage: deploy config:unset <name> KEY"; exit 1; }
  [ -z "$key" ] && { err "Usage: deploy config:unset <name> KEY"; exit 1; }
  env_file="${ENV_DIR}/${name}.env"
  [ ! -f "$env_file" ] && { err "no config found for $name"; exit 1; }
  grep -v "^${key}=" "$env_file" > /tmp/deploy_env_$$ 2>/dev/null || true
  mv /tmp/deploy_env_$$ "$env_file"
  ok "unset ${key} for ${name}"
}

cmd_config_encrypt() {
  local name=$1
  [ -z "$name" ] && { err "Usage: deploy config:encrypt <name>"; exit 1; }
  env_file="${ENV_DIR}/${name}.env"
  [ ! -f "$env_file" ] && { err "no config found for $name"; exit 1; }
  if ! command -v gpg &>/dev/null; then
    err "gpg is required. Install it: apt install gpg"
    exit 1
  fi
  gpg --symmetric --cipher-algo AES256 "$env_file" 2>/dev/null && {
    rm "$env_file"
    ok "encrypted ${env_file} -> ${env_file}.gpg"
  } || err "encryption failed"
}

cmd_config_decrypt() {
  local name=$1
  [ -z "$name" ] && { err "Usage: deploy config:decrypt <name>"; exit 1; }
  encrypted="${ENV_DIR}/${name}.env.gpg"
  [ ! -f "$encrypted" ] && { err "no encrypted config found for $name"; exit 1; }
  if ! command -v gpg &>/dev/null; then
    err "gpg is required. Install it: apt install gpg"
    exit 1
  fi
  gpg --decrypt "$encrypted" 2>/dev/null > "${ENV_DIR}/${name}.env" && {
    rm "$encrypted"
    ok "decrypted ${encrypted} -> ${ENV_DIR}/${name}.env"
  } || err "decryption failed (wrong password?)"
}

# ── env:setup — generate .env.example ─────────────────────────────────────────

cmd_env_setup() {
  local name=$1
  local path=""

  if [ -z "$name" ]; then
    # Try deploy.yml in current directory
    if [ -f "deploy.yml" ]; then
      local yml_name
      yml_name=$(parse_yml_value "deploy.yml" "name" 2>/dev/null | xargs)
      if [ -n "$yml_name" ] && grep -q "^${yml_name}\t" "$REGISTRY" 2>/dev/null; then
        name="$yml_name"
        path=$(get_path "$name")
      else
        path="$PWD"
        name="${yml_name:-$(basename "$PWD")}"
      fi
    else
      err "Usage: deploy env:setup <name>  (or run in a project with deploy.yml)"
      exit 1
    fi
  else
    grep -q "^$name\t" "$REGISTRY" 2>/dev/null || { err "app '$name' not found"; exit 1; }
    path=$(get_path "$name")
  fi

  local example_file="${path}/.env.example"

  info "generating .env.example for $name..."

  {
    echo "# Environment variables for $name"
    echo "# Copy to .env and fill in the values"
    echo ""

    # From existing env file
    local env_file="${ENV_DIR}/${name}.env"
    if [ -f "$env_file" ]; then
      echo "# Current values (commented out)"
      while IFS='=' read -r key val || [ -n "$key" ]; do
        [[ "$key" =~ ^# || -z "$key" ]] && continue
        echo "# ${key}=${val}"
      done < "$env_file"
      echo ""
    fi

    # Port
    local port
    port=$(get_app_port "$name" 2>/dev/null || echo "8000")
    echo "# Port (assigned automatically)"
    echo "PORT=${port}"
    echo ""

    # Common vars
    echo "# Environment"
    echo "APP_ENV=development"
    echo "APP_DEBUG=true"
    echo "APP_URL=https://${name}.${BASE_DOMAIN}"
  } > "$example_file"

  ok "created $example_file"
  echo "  Fill in the values, then:"
  echo "  cp .env.example .env"
}
