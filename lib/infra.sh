#!/bin/bash
# deploy/lib/infra.sh — Infrastructure management

cmd_init() {
  mkdir -p "$DEPLOY_HOME" "$CUSTOM_DIR" "$LOG_DIR" "$ENV_DIR"
  [ ! -f "$REGISTRY" ] && touch "$REGISTRY"
  if [ ! -f "$DEPLOY_CONF" ]; then
    cat > "$DEPLOY_CONF" << CONF
# deploy configuration
# BASE_DOMAIN=myapp.com
# SSL_CERT=/etc/ssl/certs/myapp.crt
# SSL_KEY=/etc/ssl/private/myapp.key
# NGINX_CONF=/etc/nginx/sites-available/deploy
# MIN_PORT=8000
# MAX_PORT=9000
CONF
    ok "created $DEPLOY_CONF"
  fi
  ok "initialized deploy in $DEPLOY_HOME"
}

cmd_prod() {
  local name=$1
  [ -z "$name" ] && { err "Usage: deploy prod <name>"; exit 1; }
  path=$(get_path "$name")
  [ -z "$path" ] && { err "app '$name' not found"; exit 1; }

  cd "$path"
  load_env "$name"

  if [ -f "Dockerfile" ]; then
    # Tag current image for rollback before building new one
    old_id=$(docker images -q "$name" 2>/dev/null | head -1)
    if [ -n "$old_id" ]; then
      local ts
      ts=$(date +%s)
      docker tag "$old_id" "${name}:previous-${ts}" 2>/dev/null || true
      info "saved current image as ${name}:previous-${ts} for rollback"
    fi

    if [ -f "docker-compose.yml" ]; then
      docker compose up --build -d
    else
      docker build -t "$name" .
      port=$(get_app_port "$name")
      docker run -d --restart unless-stopped --name "$name" \
        -p "$port:$port" \
        --env-file "${ENV_DIR}/${name}.env" 2>/dev/null \
        "$name"
    fi
    pid=$(docker ps -q -f name="$name" 2>/dev/null)
    if [ -n "$pid" ]; then
      ok "deployed $name -> https://$name.$BASE_DOMAIN"
    else
      err "deployment failed for $name"
      exit 1
    fi
  else
    docker compose up --build -d
    ok "deployed $name -> https://$name.$BASE_DOMAIN"
  fi
}

cmd_rollback() {
  local name=$1
  [ -z "$name" ] && { err "Usage: deploy rollback <name>"; exit 1; }
  path=$(get_path "$name")
  [ -z "$path" ] && { err "app '$name' not found"; exit 1; }
  type=$(get_type "$name")

  if [ "$type" != "docker" ]; then
    warn "rolling back $name to previous git state..."
    cd "$path"
    if git log --oneline -2 2>/dev/null | tail -1; then
      cmd_stop "$name" 2>/dev/null || true
      git checkout HEAD~1 -- . 2>/dev/null && {
        ok "reverted code to previous commit"
        cmd_start "$name" "-d"
      } || err "no previous commit to rollback to"
    else
      err "not a git repository — cannot rollback"
      exit 1
    fi
  else
    warn "rolling back $name to previous docker image..."
    prev_tag=$(docker images --format "{{.Repository}}:{{.Tag}}" | grep "^${name}:" | grep -v latest | sort | tail -1 2>/dev/null)
    if [ -z "$prev_tag" ]; then
      err "no previous image found for $name"
      exit 1
    fi
    cmd_stop "$name" 2>/dev/null || true
    port=$(get_app_port "$name")
    docker run -d --restart unless-stopped --name "$name" \
      -p "$port:$port" \
      --env-file "${ENV_DIR}/${name}.env" 2>/dev/null \
      "$prev_tag" && ok "rolled back $name to $prev_tag" || err "rollback failed"
  fi
}

cmd_ssl() {
  local name=$1
  [ -z "$name" ] && { err "Usage: deploy ssl <name>"; exit 1; }
  if ! command -v certbot &>/dev/null; then
    err "certbot is required. Install it: apt install certbot python3-certbot-nginx"
    exit 1
  fi
  grep -q "^$name\t" "$REGISTRY" 2>/dev/null || { err "app '$name' not found"; exit 1; }
  info "provisioning SSL for ${name}.${BASE_DOMAIN}..."
  sudo certbot --nginx -d "${name}.${BASE_DOMAIN}" --non-interactive --agree-tos --register-unsafely-without-email 2>&1 || {
    warn "certbot failed. Try: sudo certbot --nginx -d ${name}.${BASE_DOMAIN}"
    exit 1
  }
  ok "SSL provisioned for ${name}.${BASE_DOMAIN}"
  grep -q "^SSL_CERT=" "$DEPLOY_CONF" 2>/dev/null || {
    echo "SSL_CERT=/etc/letsencrypt/live/${name}.${BASE_DOMAIN}/fullchain.pem" >> "$DEPLOY_CONF"
    echo "SSL_KEY=/etc/letsencrypt/live/${name}.${BASE_DOMAIN}/privkey.pem" >> "$DEPLOY_CONF"
    info "updated $DEPLOY_CONF with SSL cert paths"
  }
}

cmd_ssl_renew() {
  info "renewing all Let's Encrypt certificates..."
  if command -v certbot &>/dev/null; then
    sudo certbot renew && sudo nginx -s reload && ok "certificates renewed and nginx reloaded"
  else
    err "certbot is required. Install it: apt install certbot python3-certbot-nginx"
    exit 1
  fi
}

cmd_uninstall() {
  warn "This will stop all apps and remove deploy data from $DEPLOY_HOME"
  echo -n "Are you sure? [y/N] "; read -r confirm
  [ "$confirm" != "y" ] && [ "$confirm" != "Y" ] && { info "cancelled"; exit 0; }

  # Stop all apps
  while IFS= read -r line; do
    [[ "$line" =~ ^# || -z "$line" ]] && continue
    name=$(echo "$line" | awk '{print $1}')
    cmd_stop "$name" 2>/dev/null || true
  done < "$REGISTRY"

  # Remove nginx config
  if [ -f "$NGINX_CONF" ]; then
    sudo rm -f "$NGINX_CONF" && ok "removed nginx config"
    sudo nginx -s reload 2>/dev/null || true
  fi

  rm -rf "$DEPLOY_HOME"
  ok "removed $DEPLOY_HOME"
  ok "deploy uninstalled"
}

# 'nginx' and 'custom' are called directly by the dispatcher via regen_nginx
# and inline code in bin/deploy
