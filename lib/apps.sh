#!/bin/bash
# deploy/lib/apps.sh — App lifecycle: add, remove, list, info, start, stop, restart, logs

cmd_list() {
  printf "\n${BOLD}%-15s %-6s %-10s %-8s %-40s %s${NC}\n" "APP" "PORT" "TYPE" "ROLE" "ENDPOINT" "STATUS"
  echo "──────────────────────────────────────────────────────────────────────────────────"
  while IFS= read -r line; do
    [[ "$line" =~ ^# || -z "$line" ]] && continue
    name=$(echo "$line" | awk '{print $1}')
    port=$(echo "$line" | awk '{print $2}')
    type=$(echo "$line" | awk '{print $3}')
    role=$(echo "$line" | awk '{print $4}')
    if [ "$role" = "app" ]; then
      endpoint="https://${name}.${BASE_DOMAIN}"
      if is_running "$name"; then
        status="${GREEN}running${NC}"
      else
        status="${RED}stopped${NC}"
      fi
    else
      endpoint=""
      status=""
    fi
    printf "%-15s %-6s %-10s %-8s %-40s ${status}\n" "$name" "$port" "$type" "$role" "$endpoint"
  done < "$REGISTRY"
  echo ""
}

cmd_add() {
  local name=$1 type=$2 path=$3
  [ -z "$name" ] && { err "Usage: deploy add <name> <type> <path>"; exit 1; }
  [ -z "$type" ] && { err "Usage: deploy add <name> <type> <path>"; exit 1; }
  [ -z "$path" ] && { err "Usage: deploy add <name> <type> <path>"; exit 1; }
  [ ! -d "$path" ] && { err "directory not found: $path"; exit 1; }

  # Normalize path to absolute
  path="$(cd "$path" && pwd)"

  if grep -q "^$name\t" "$REGISTRY" 2>/dev/null; then
    err "app '$name' already exists"
    exit 1
  fi
  port=$(next_port)
  printf "%s\t%s\t%s\tapp\t%s\n" "$name" "$port" "$type" "$path" >> "$REGISTRY"
  ok "registered $name on port $port -> https://$name.$BASE_DOMAIN"
  regen_nginx
}

cmd_remove() {
  local name=$1
  [ -z "$name" ] && { err "Usage: deploy remove <name>"; exit 1; }
  grep -q "^$name\t" "$REGISTRY" 2>/dev/null || { err "app '$name' not found"; exit 1; }

  # Stop if running
  is_running "$name" && cmd_stop "$name" >/dev/null 2>&1

  sed -i "/^$(echo "$name" | sed 's/[\/&]/\\&/g')\t/d" "$REGISTRY"
  # Clean up env and logs
  rm -f "${ENV_DIR}/${name}.env" "${LOG_DIR}/${name}.log"
  rm -f "${CUSTOM_DIR}/${name}.conf"
  ok "removed $name"
  regen_nginx
}

cmd_start() {
  local name=$1 daemonize=false
  [ -z "$name" ] && { err "Usage: deploy start <name> [-d]"; exit 1; }
  [ "$2" == "-d" ] && daemonize=true

  port=$(get_app_port "$name")
  type=$(get_type "$name")
  path=$(get_path "$name")
  [ -z "$port" ] && { err "app '$name' not found"; exit 1; }
  [ -z "$path" ] && { err "path not found for '$name'"; exit 1; }

  # Double-start protection
  if is_running "$name"; then
    warn "$name is already running (PID $(get_pid "$name"))"
    [ "$daemonize" = false ] && exit 0
  fi

  cd "$path" || exit 1
  log_file="${LOG_DIR}/${name}.log"

  # Load env vars
  load_env "$name"

  case $type in
    laravel) CMD="php artisan serve --host=0.0.0.0 --port=$port" ;;
    flask)   CMD="flask run --host=0.0.0.0 --port=$port" ;;
    node)    CMD="PORT=$port npm start" ;;
    static)  CMD="npx serve -s . -l $port" ;;
    docker)
      if [ -f "Dockerfile" ] && [ ! -f "docker-compose.yml" ]; then
        CMD="docker build -t $name . && docker run -d --restart unless-stopped -p $port:$port -e PORT=$port --name $name $name"
      else
        CMD="PORT=$port docker compose up --build -d"
      fi
      ;;
    *) err "unknown type: $type (supported: laravel, flask, node, static, docker)"; exit 1 ;;
  esac

  if $daemonize; then
    nohup bash -c "$CMD" > "$log_file" 2>&1 &
    pid=$!
    info "started $name (PID $pid) -> https://$name.$BASE_DOMAIN"
    echo "  Logs: tail -f $log_file"
    health_check "$name" "$port" "$HEALTH_RETRIES" "$HEALTH_INTERVAL"
  else
    info "starting $name (foreground)..."
    eval "$CMD"
  fi
}

cmd_stop() {
  local name=$1
  [ -z "$name" ] && { err "Usage: deploy stop <name>"; exit 1; }
  port=$(get_app_port "$name")
  [ -z "$port" ] && { err "app '$name' not found"; exit 1; }
  pid=$(lsof -ti :"$port" 2>/dev/null)
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null && ok "stopped $name" || warn "failed to stop $name"
    # Also stop docker containers if docker type
    type=$(get_type "$name")
    if [ "$type" = "docker" ]; then
      path=$(get_path "$name")
      [ -n "$path" ] && cd "$path" 2>/dev/null && docker compose down 2>/dev/null && ok "stopped docker containers for $name"
    fi
  else
    warn "$name is not running"
  fi
}

cmd_restart() {
  warn "restarting $1..."
  cmd_stop "$1"
  sleep 1
  cmd_start "$1" "-d"
}

cmd_logs() {
  local name=$1
  [ -z "$name" ] && { err "Usage: deploy logs <name>"; exit 1; }
  log_file="${LOG_DIR}/${name}.log"
  if [ ! -f "$log_file" ]; then
    err "no logs found for $name"
    exit 1
  fi
  tail -f "$log_file"
}

cmd_info() {
  local name=$1
  [ -z "$name" ] && { err "Usage: deploy info <name>"; exit 1; }
  port=$(get_app_port "$name")
  type=$(get_type "$name")
  path=$(get_path "$name")
  [ -z "$port" ] && { err "app '$name' not found"; exit 1; }

  echo ""
  echo -e " ${BOLD}${name}${NC}"
  echo " ----------------------------------"
  echo -e "  ${DIM}Port:${NC}     $port"
  echo -e "  ${DIM}Type:${NC}     $type"
  echo -e "  ${DIM}Path:${NC}     $path"
  echo -e "  ${DIM}URL:${NC}      https://${name}.${BASE_DOMAIN}"
  if is_running "$name"; then
    echo -e "  ${DIM}Status:${NC}   ${GREEN}running${NC} (PID $(get_pid "$name"))"
  else
    echo -e "  ${DIM}Status:${NC}   ${RED}stopped${NC}"
  fi
  echo -e "  ${DIM}Logs:${NC}     ${LOG_DIR}/${name}.log"

  # Show env vars
  env_file="${ENV_DIR}/${name}.env"
  if [ -f "$env_file" ] && [ -s "$env_file" ]; then
    echo -e "  ${DIM}Env vars:${NC}"
    while IFS='=' read -r key val || [ -n "$key" ]; do
      [[ "$key" =~ ^# || -z "$key" ]] && continue
      echo "    ${key}=${val%%"${val#?????????????}"}..."  # truncated
    done < "$env_file"
  fi
  echo ""
}
