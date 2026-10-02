#!/usr/bin/env bash
#
# Pre-flight check. Reports EVERY problem it finds, with the exact command to
# fix each one, rather than failing on the first — a developer should need one
# pass, not five.
#
# Checks: required tooling, port availability, and that .env defines every
# variable .env.example does.
#
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TOOLS_BIN="$ROOT/.tools/bin"
export PATH="$TOOLS_BIN:$PATH"

GREEN=$'\033[32m'; RED=$'\033[31m'; YELLOW=$'\033[33m'; DIM=$'\033[2m'; RESET=$'\033[0m'
problems=0

ok()   { printf '  %sok%s    %-12s %s%s%s\n'   "$GREEN" "$RESET" "$1" "$DIM" "${2:-}" "$RESET"; }
warn() { printf '  %swarn%s  %-12s %s\n'       "$YELLOW" "$RESET" "$1" "${2:-}"; }
bad()  { printf '  %sFAIL%s  %-12s %s\n'       "$RED" "$RESET" "$1" "${2:-}"; problems=$((problems+1)); }

# ---------------------------------------------------------------------------
echo "Tooling"
# ---------------------------------------------------------------------------

check_tool() {
  local name="$1" version_cmd="$2" fix="$3"
  if command -v "$name" >/dev/null 2>&1; then
    ok "$name" "$(eval "$version_cmd" 2>&1 | head -1)"
  else
    bad "$name" "not installed — $fix"
  fi
}

check_tool go     "go version"     "https://go.dev/dl/ (need 1.25+)"
check_tool node   "node --version" "https://nodejs.org (need 20.12+)"
check_tool npm    "npm --version"  "ships with Node"
check_tool docker "docker --version" "install Docker Desktop or Colima"
check_tool uv     "uv --version"   "brew install uv (runs vm-analytics-api; it fetches its own Python 3.12)"

if docker compose version >/dev/null 2>&1; then
  ok "compose" "$(docker compose version | head -1)"
else
  bad "compose" "Docker Compose v2 required — upgrade Docker"
fi

# The daemon being installed is not the same as it running.
if docker info >/dev/null 2>&1; then
  ok "docker-eng" "daemon reachable"
else
  bad "docker-eng" "daemon not running — start Docker Desktop (or 'colima start')"
fi

# air and goose are installed into .tools/bin by `make tools`, so a miss here
# is a fixable step rather than a manual download.
check_tool air   "air -v"       "run 'make -C infra tools'"
check_tool goose "goose -version" "run 'make -C infra tools'"

# ---------------------------------------------------------------------------
echo
echo "Ports"
# ---------------------------------------------------------------------------

# Read the configured ports from .env when it exists, so this checks the ports
# actually in use rather than the defaults.
port_value() {
  local key="$1" fallback="$2"
  if [ -f "$ROOT/.env" ]; then
    local v
    v="$(grep -E "^${key}=" "$ROOT/.env" | tail -1 | cut -d= -f2- | tr -d ' ')"
    [ -n "$v" ] && { echo "$v"; return; }
  fi
  echo "$fallback"
}

check_port() {
  local label="$1" port="$2"
  local holder
  holder="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR==2{print $1" (pid "$2")"}')"
  if [ -z "$holder" ]; then
    ok "$label" "port $port free"
  else
    # Our own containers holding their ports is the expected steady state.
    case "$holder" in
      com.docke*|docker*|dockerd*)
        ok "$label" "port $port held by Docker (stack already up)" ;;
      *)
        bad "$label" "port $port is taken by $holder — stop it or change ${label}_PORT in .env" ;;
    esac
  fi
}

check_port GATEWAY     "$(port_value GATEWAY_PORT 8080)"
check_port PROFILE     "$(port_value PROFILE_PORT 8081)"
check_port CATALOG     "$(port_value CATALOG_PORT 8082)"
check_port ORDERS      "$(port_value ORDERS_PORT 8083)"
check_port EVENTS      "$(port_value EVENTS_CONSUMER_PORT 8084)"
check_port ANALYTICS   "$(port_value ANALYTICS_API_PORT 8085)"
check_port CLIENT_UI   "$(port_value CLIENT_UI_PORT 5173)"
check_port ADMIN_UI    "$(port_value ADMIN_UI_PORT 5174)"
check_port ANALYTICS_UI "$(port_value ANALYTICS_UI_PORT 5176)"
check_port SUPPLIER_UI "$(port_value SUPPLIER_UI_PORT 5175)"
check_port POSTGRES    "$(port_value POSTGRES_PORT 5432)"
check_port MINIO       "$(port_value MINIO_API_PORT 9000)"
check_port MAILHOG     "$(port_value MAILHOG_SMTP_PORT 1025)"

# ---------------------------------------------------------------------------
echo
echo "Environment"
# ---------------------------------------------------------------------------

if [ ! -f "$ROOT/.env.example" ]; then
  bad ".env.example" "missing from the repository root"
elif [ ! -f "$ROOT/.env" ]; then
  bad ".env" "missing — run 'cp .env.example .env' (or 'make -C infra env')"
else
  # Compare declared keys, not values. A variable added to .env.example in a
  # teammate's commit is the single most common cause of "it works on mine".
  keys_example="$(grep -oE '^[A-Z_0-9]+=' "$ROOT/.env.example" | tr -d '=' | sort -u)"
  keys_actual="$(grep -oE '^[A-Z_0-9]+=' "$ROOT/.env" | tr -d '=' | sort -u)"

  missing="$(comm -23 <(echo "$keys_example") <(echo "$keys_actual"))"
  extra="$(comm -13 <(echo "$keys_example") <(echo "$keys_actual"))"

  if [ -z "$missing" ]; then
    ok ".env" "$(echo "$keys_actual" | wc -l | tr -d ' ') variables, none missing"
  else
    bad ".env" "missing $(echo "$missing" | wc -l | tr -d ' ') variable(s) that .env.example defines:"
    echo "$missing" | sed 's/^/          /'
    echo "        add them to .env, or re-copy: cp .env.example .env"
  fi

  if [ -n "$extra" ]; then
    warn ".env" "defines variables absent from .env.example (undocumented):"
    echo "$extra" | sed 's/^/          /'
  fi

  # Empty values for variables that must not be empty.
  for key in INTERNAL_SERVICE_TOKEN JWT_SECRET POSTGRES_PASSWORD S3_ENDPOINT; do
    value="$(grep -E "^${key}=" "$ROOT/.env" | tail -1 | cut -d= -f2- | tr -d ' ')"
    if [ -z "$value" ]; then
      bad ".env" "$key is empty — services will refuse to start"
    fi
  done

  # The two hosts a handset has to reach must agree about what "this machine"
  # means. A LAN gateway with localhost storage is the silent version of this
  # going wrong: the apps work, and only the images are missing.
  mobile_url="$(grep -E '^MOBILE_API_BASE_URL=' "$ROOT/.env" | tail -1 | cut -d= -f2- | tr -d ' ')"
  s3_url="$(grep -E '^S3_ENDPOINT=' "$ROOT/.env" | tail -1 | cut -d= -f2- | tr -d ' ')"
  case "$mobile_url" in
    *localhost*|*127.0.0.1*|"") ;;
    *)
      case "$s3_url" in
        *localhost*|*127.0.0.1*)
          warn ".env" "MOBILE_API_BASE_URL is on the LAN but S3_ENDPOINT is $s3_url"
          echo "          product images are presigned against that host, so a phone would"
          echo "          fetch them from itself: no photos in the customer app, and failed"
          echo "          uploads in the supplier app. Fix: make -C infra mobile-ip"
          ;;
      esac
      ;;
  esac
fi

# ---------------------------------------------------------------------------
echo
if [ "$problems" -ne 0 ]; then
  echo "${RED}${problems} problem(s) found.${RESET} Fix the FAIL lines above, then re-run 'make -C infra doctor'."
  exit 1
fi
echo "${GREEN}All checks passed.${RESET} Run 'make -C infra dev'."
