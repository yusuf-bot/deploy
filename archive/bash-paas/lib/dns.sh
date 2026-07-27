#!/bin/bash
# deploy/lib/dns.sh — DNS provider abstraction
# Each provider exports: dns_add_record, dns_remove_record, dns_list_records
# The dispatch functions auto-detect the provider from $DNS_PROVIDER.

# ── Provider dispatch ─────────────────────────────────────────────────────────

# Auto-detect and load the right provider
dns_init() {
  case "${DNS_PROVIDER:-}" in
    cloudflare)
      dns_provider="cloudflare"
      ;;
    manual|"")
      dns_provider="manual"
      ;;
    *)
      err "unknown DNS provider: $DNS_PROVIDER"
      err "supported: cloudflare, manual"
      exit 1
      ;;
  esac
}

# ── Cloudflare Provider ───────────────────────────────────────────────────────

dns_cloudflare_api() {
  local method=$1 endpoint=$2 data=${3:-}
  local url="https://api.cloudflare.com/client/v4${endpoint}"
  local cmd=(curl -s -X "$method" "$url"
    -H "Authorization: Bearer ${CLOUDFLARE_TOKEN}"
    -H "Content-Type: application/json")
  [ -n "$data" ] && cmd+=(-d "$data")
  "${cmd[@]}"
}

dns_cloudflare_zone_id() {
  # Fetch zone ID if not already cached
  if [ -z "${_CF_ZONE_ID:-}" ]; then
    _CF_ZONE_ID=$(dns_cloudflare_api GET "/zones?name=${CLOUDFLARE_ZONE}" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['result'][0]['id'] if d.get('result') else '')" 2>/dev/null)
  fi
  echo "$_CF_ZONE_ID"
}

dns_add_record_cloudflare() {
  local domain=$1 type=$2 value=$3
  [ -z "$CLOUDFLARE_TOKEN" ] && { warn "CLOUDFLARE_TOKEN not set — skipping DNS"; return 1; }
  [ -z "$CLOUDFLARE_ZONE" ] && { warn "CLOUDFLARE_ZONE not set — skipping DNS"; return 1; }

  local zone_id
  zone_id=$(dns_cloudflare_zone_id)
  [ -z "$zone_id" ] && { warn "could not find Cloudflare zone for ${CLOUDFLARE_ZONE}"; return 1; }

  # Determine the DNS name (strip zone from domain)
  local dns_name="${domain%.${CLOUDFLARE_ZONE}}"
  [ "$dns_name" = "$domain" ] && dns_name="$domain"  # not a subdomain of our zone

  info "adding DNS record: ${dns_name}.${CLOUDFLARE_ZONE} → ${value:-<auto>}..."

  # Get server IP automatically
  local ip="${value:-$(curl -s https://api.ipify.org 2>/dev/null)}"
  [ -z "$ip" ] && { warn "could not detect server IP"; return 1; }

  local data
  data=$(cat << JSON
{
  "type": "${type:-A}",
  "name": "${dns_name}",
  "content": "${ip}",
  "ttl": 120,
  "proxied": true
}
JSON
)

  local result
  result=$(dns_cloudflare_api POST "/zones/${zone_id}/dns_records" "$data")
  if echo "$result" | python3 -c "import sys,json; d=json.load(sys.stdin); sys.exit(0 if d.get('success') else 1)" 2>/dev/null; then
    ok "DNS record created: ${domain} → ${ip}"
  else
    local err_msg
    err_msg=$(echo "$result" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('errors',[{}])[0].get('message','unknown'))" 2>/dev/null)
    warn "DNS record may already exist (${err_msg}) — continuing"
  fi
}

dns_remove_record_cloudflare() {
  local domain=$1 type=$2 value=$3
  [ -z "$CLOUDFLARE_TOKEN" ] && return 0
  [ -z "$CLOUDFLARE_ZONE" ] && return 0

  local zone_id
  zone_id=$(dns_cloudflare_zone_id)
  [ -z "$zone_id" ] && return 0

  local dns_name="${domain%.${CLOUDFLARE_ZONE}}"
  [ "$dns_name" = "$domain" ] && dns_name="$domain"

  # Find and delete the record
  local search
  search=$(dns_cloudflare_api GET "/zones/${zone_id}/dns_records?type=${type}&name=${domain}")
  local record_id
  record_id=$(echo "$search" | python3 -c "import sys,json; d=json.load(sys.stdin); rs=[r for r in d.get('result',[]) if r['name']=='${domain}']; print(rs[0]['id'] if rs else '')" 2>/dev/null)

  if [ -n "$record_id" ]; then
    dns_cloudflare_api DELETE "/zones/${zone_id}/dns_records/${record_id}" >/dev/null
    ok "removed DNS record: ${domain}"
  fi
}

# ── Manual Provider (prints instructions, no API calls) ───────────────────────

dns_add_record_manual() {
  local domain=$1 type=$2 value=$3
  local ip="${value:-<SERVER_IP>}"
  echo ""
  warn "DNS_PROVIDER not configured — add this record manually:"
  echo ""
  echo "  Type:  ${type:-A}"
  echo "  Name:  ${domain}"
  echo "  Value: ${ip}"
  echo ""
  echo "  Then run: deploy nginx && deploy ssl ${domain%%.*}"
  echo ""
}

dns_remove_record_manual() {
  local domain=$1 type=$2 value=$3
  warn "remove DNS record manually: ${domain} (${type})"
}

# ── Public Dispatch ───────────────────────────────────────────────────────────

# Initialize the provider
dns_init

# Wrapper functions that dispatch to the active provider
dns_add_record() {
  case "$dns_provider" in
    cloudflare) dns_add_record_cloudflare "$@" ;;
    manual|*)   dns_add_record_manual "$@" ;;
  esac
}

dns_remove_record() {
  case "$dns_provider" in
    cloudflare) dns_remove_record_cloudflare "$@" ;;
    manual|*)   dns_remove_record_manual "$@" ;;
  esac
}
