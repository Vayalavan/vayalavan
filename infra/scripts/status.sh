#!/usr/bin/env bash
#
# Reports what is actually up: containers and application processes.
#
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INFRA="$ROOT/infra"
COMPOSE="docker compose --env-file $ROOT/.env -f $INFRA/docker-compose.yml"

port_value() {
  local key="$1" fallback="$2" v
  if [ -f "$ROOT/.env" ]; then
    v="$(grep -E "^${key}=" "$ROOT/.env" | tail -1 | cut -d= -f2- | tr -d ' ')"
    [ -n "$v" ] && { echo "$v"; return; }
  fi
  echo "$fallback"
}

echo "Containers"
$COMPOSE ps --format '  {{.Service}}\t{{.Status}}' 2>/dev/null || echo "  (compose not running)"

echo
echo "Application processes"
printf '  %-12s %-6s %-10s %s\n' SERVICE PORT STATE ENDPOINT

probe() {
  local name="$1" port="$2" kind="$3" url state
  if [ "$kind" = "api" ]; then
    url="http://localhost:$port/healthz"
    if curl -fsS --max-time 3 "$url" >/dev/null 2>&1; then state="healthy"
    elif lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then state="unhealthy"
    else state="down"; fi
  else
    url="http://localhost:$port"
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then state="up"; else state="down"; fi
  fi
  printf '  %-12s %-6s %-10s %s\n' "$name" "$port" "$state" "$url"
}

probe gateway     "$(port_value GATEWAY_PORT 8080)"     api
probe profile     "$(port_value PROFILE_PORT 8081)"     api
probe catalog     "$(port_value CATALOG_PORT 8082)"     api
probe orders      "$(port_value ORDERS_PORT 8083)"      api
probe events      "$(port_value EVENTS_CONSUMER_PORT 8084)" api
probe analytics   "$(port_value ANALYTICS_API_PORT 8085)" api
probe client-ui   "$(port_value CLIENT_UI_PORT 5173)"   ui
probe admin-ui    "$(port_value ADMIN_UI_PORT 5174)"    ui
probe analytics-ui "$(port_value ANALYTICS_UI_PORT 5176)" ui
probe supplier-ui "$(port_value SUPPLIER_UI_PORT 5175)" ui

echo
echo "Consoles"
echo "  minio     http://localhost:$(port_value MINIO_CONSOLE_PORT 9001)"
echo "  mailhog   http://localhost:$(port_value MAILHOG_UI_PORT 8025)"
