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

cmd_ls() { cmd_list; }

cmd_add() {
  local name=$1 type=$2 path=$3
  [ -z "$name" ] && { err "Usage: deploy add <name> <type> <path>"; exit 1; }
  [ -z "$type" ] && { err "Usage: deploy add <name> <type> <path>"; exit 1; }
  [ -z "$path" ] && { err "Usage: deploy add <name> <type> <path>"; exit 1; }
  [ ! -d "$path" ] && { err "directory not found: $path"; exit 1; }

  # Normalize path to absolute
  path="$(cd "$path" && pwd 2>/dev/null)" || { err "cannot access path: $path"; exit 1; }

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

  # Remove DNS record if provider is configured
  if [ -n "$DNS_PROVIDER" ]; then
    dns_remove_record "${name}.${BASE_DOMAIN}" "A" "" 2>/dev/null || true
    dns_remove_record "${name}.${DEV_DOMAIN}" "A" "" 2>/dev/null || true
  fi

  sed -i "/^$(echo "$name" | sed 's/[\/&]/\\&/g')\t/d" "$REGISTRY"
  # Clean up
  rm -f "${ENV_DIR}/${name}.env" "${LOG_DIR}/${name}.log"
  rm -f "${CUSTOM_DIR}/${name}.conf"
  rm -f "${PID_DIR}/${name}.pid"
  rm -rf "${APPS_DIR}/${name}"
  ok "removed $name"
  regen_nginx
}

cmd_start() {
  local name=$1 daemonize=false mode="${3:-prod}"
  [ -z "$name" ] && { err "Usage: deploy start <name> [-d]"; exit 1; }
  [ "$2" = "-d" ] && daemonize=true

  port=$(get_app_port "$name")
  type=$(get_type "$name")
  path=$(get_path "$name")
  [ -z "$port" ] && { err "app '$name' not found"; exit 1; }
  [ -z "$path" ] && { err "path not found for '$name'"; exit 1; }

  # Double-start protection
  if is_running "$name"; then
    warn "$name is already running (PID $(get_pid "$name"))"
    [ "$daemonize" = false ] && return 0
  fi

  cd "$path" || exit 1
  log_file="${LOG_DIR}/${name}.log"

  # Load env vars
  load_env "$name"

  # Check for deploy.yml overrides
  if [ -f "$path/deploy.yml" ]; then
    local cmd_override
    cmd_override=$(parse_yml_value "$path/deploy.yml" "start")
    [ -n "$cmd_override" ] && type="custom"
  fi

  case $type in
    laravel) CMD="php artisan serve --host=0.0.0.0 --port=$port" ;;
    flask)   CMD="flask run --host=0.0.0.0 --port=$port" ;;
    node)    CMD="PORT=$port npm start" ;;
    static)  CMD="npx serve -s . -l $port" ;;
    custom)
      CMD=$(parse_yml_value "$path/deploy.yml" "start")
      CMD="${CMD/\$PORT/$port}"
      ;;
    docker)
      if [ -f "docker-compose.yml" ]; then
        CMD="PORT=$port docker compose up --build -d"
      else
        CMD="docker build -t $name . && docker run -d --restart unless-stopped -p $port:$port -e PORT=$port --name $name $name"
      fi
      ;;
    *) err "unknown type: $type (supported: laravel, flask, node, static, docker)"; exit 1 ;;
  esac

  if $daemonize; then
    nohup bash -c "$CMD" > "$log_file" 2>&1 &
    local bg_pid=$!
    # Wait for process to bind to port, then save the real PID
    sleep 1
    local real_pid
    for i in $(seq 1 10); do
      real_pid=$(lsof -ti :"$port" 2>/dev/null | head -1)
      [ -n "$real_pid" ] && break
      sleep 1
    done
    # If lsof didn't find anything yet for docker, wait longer (docker may be slow)
    if [ -z "$real_pid" ] && [ "$type" = "docker" ]; then
      for i in $(seq 1 15); do
        real_pid=$(lsof -ti :"$port" 2>/dev/null | head -1)
        [ -n "$real_pid" ] && break
        sleep 1
      done
    fi
    save_pid "$name" "${real_pid:-$bg_pid}"
    info "started $name (PID ${real_pid:-$bg_pid}) -> https://$name.$BASE_DOMAIN"
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
  type=$(get_type "$name")

  # Docker stop
  if [ "$type" = "docker" ]; then
    path=$(get_path "$name")
    if [ -n "$path" ] && [ -f "$path/docker-compose.yml" ]; then
      cd "$path" 2>/dev/null && docker compose down 2>/dev/null && ok "stopped docker containers for $name"
    else
      docker stop "$name" 2>/dev/null && ok "stopped docker container $name" || true
    fi
    clear_pid "$name"
    return 0
  fi

  port=$(get_app_port "$name")
  [ -z "$port" ] && { err "app '$name' not found"; exit 1; }

  local pid
  pid=$(get_pid "$name")
  if [ -z "$pid" ]; then
    warn "$name is not running"
    clear_pid "$name"
    return 0
  fi

  # SIGTERM first
  info "stopping $name (PID $pid)..."
  kill "$pid" 2>/dev/null || true

  # Wait up to 10s for graceful shutdown
  local waited=0
  while [ "$waited" -lt 10 ]; do
    if ! pid_alive "$pid"; then
      ok "stopped $name gracefully"
      clear_pid "$name"
      return 0
    fi
    sleep 1
    ((waited++))
  done

  # Force kill if still alive
  warn "$name did not stop gracefully — sending SIGKILL"
  kill -9 "$pid" 2>/dev/null || true
  sleep 1
  if ! pid_alive "$pid"; then
    ok "stopped $name (forced)"
    clear_pid "$name"
  else
    err "failed to stop $name"
    exit 1
  fi
}

cmd_restart() {
  local name=$1
  [ -z "$name" ] && { err "Usage: deploy restart <name>"; exit 1; }
  warn "restarting $1..."
  cmd_stop "$1"
  sleep 2
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
  if [ -f "$path/deploy.yml" ]; then
    echo -e "  ${DIM}Config:${NC}   deploy.yml"
  fi
  echo -e "  ${DIM}Path:${NC}     $path"
  echo -e "  ${DIM}URL:${NC}      https://${name}.${BASE_DOMAIN}"
  echo -e "  ${DIM}Dev:${NC}      https://${name}.${DEV_DOMAIN}"
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
