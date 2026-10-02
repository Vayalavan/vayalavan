#!/usr/bin/env bash
#
# Start one of the two mobile apps on its own.
#
#   vm-supplier-mobile-ui   "Vayalavan Supplier"      the grower's app
#   vm-client-mobile-ui     "Vayalavan"   the customer's app
#
# The counterpart to ./scripts/start.sh, which owns the backend and the three web UIs.
# This starts NOTHING but an Expo dev server. It never starts, stops, seeds or
# migrates anything server-side — that is ./scripts/start.sh's job and duplicating it
# here would give two scripts an opinion about the same processes.
#
# It does CHECK that the gateway is up, because the app is useless without it
# and a warning here is cheaper than a spinner on a handset. Run ./scripts/start.sh in
# another terminal first, then this one alongside it.
#
# It exists because every failure these apps have had on a real handset was
# environmental rather than a bug: the wrong LAN address baked into the build,
# Metro on a port another service owns, the backend not actually running. Those
# are all checkable before the QR code appears, so this checks them.
#
# The two apps have their own Metro ports (8090 and 8091) precisely so both can
# run at once — in two terminals, one each. They share MOBILE_API_BASE_URL,
# because there is one gateway.
#
#   ./scripts/start-mobile.sh                start the SUPPLIER app (the default)
#   ./scripts/start-mobile.sh customer       start the CUSTOMER app
#   ./scripts/start-mobile.sh supplier       the default, spelled out
#   ./scripts/start-mobile.sh customer --ios also open the iOS simulator (needs Xcode)
#   ./scripts/start-mobile.sh --tunnel       route Metro through a public tunnel (see below)
#   ./scripts/start-mobile.sh --android      also open an Android emulator (needs the SDK)
#   ./scripts/start-mobile.sh --clear        start with an empty Metro cache
#   ./scripts/start-mobile.sh --keep-url     do not touch MOBILE_API_BASE_URL
#   ./scripts/start-mobile.sh --stop         free BOTH Metro ports and exit
#
set -euo pipefail

# The repository root: this script lives in scripts/.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

bold() { printf "\033[1m%s\033[0m\n" "$1"; }
ok()   { printf "  \033[32m✓\033[0m %s\n" "$1"; }
warn() { printf "  \033[33m!\033[0m %s\n" "$1"; }
die()  { printf "  \033[31m✗\033[0m %s\n" "$1" >&2; exit 1; }

# Parsed, never sourced: .env holds values with spaces, & and <.
env_value() {
    grep -E "^$1=" "$ROOT/.env" 2>/dev/null | head -1 | cut -d= -f2- | tr -d ' '
}

port_or() {
    local value; value="$(env_value "$1")"
    echo "${value:-$2}"
}

free_metro_port() {
    local port="$1" pids
    pids=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null || true)
    [ -n "$pids" ] || return 0
    kill $pids 2>/dev/null || true
    sleep 1
    pids=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null || true)
    [ -n "$pids" ] && kill -9 $pids 2>/dev/null || true
    ok "freed port $port"
}

# --- which app? ------------------------------------------------------------
#
# Defaults to the supplier app: it was here first and it is what "./scripts/start-mobile.sh"
# has meant in every note and every habit built up so far. The customer app is
# always named explicitly.
APP_KEY="supplier"
TUNNEL=0 OPEN="" CLEAR=0 KEEP_URL=0 STOP=0

for arg in "$@"; do
    case "$arg" in
        supplier|grower)          APP_KEY="supplier" ;;
        customer|client|shop)     APP_KEY="customer" ;;
        --tunnel)   TUNNEL=1 ;;
        --ios)      OPEN="--ios" ;;
        --android)  OPEN="--android" ;;
        --clear)    CLEAR=1 ;;
        --keep-url) KEEP_URL=1 ;;
        --stop)     STOP=1 ;;
        # Prints the whole header block, however long it grows — a hardcoded
        # line range silently truncates the usage list the moment a flag is
        # added, which is exactly when someone is reading it.
        --help|-h) sed -n '2,/^set -/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) die "unknown option: $arg  (try --help)" ;;
    esac
done

SUPPLIER_PORT="$(port_or MOBILE_METRO_PORT 8090)"
CUSTOMER_PORT="$(port_or MOBILE_CLIENT_METRO_PORT 8091)"

# --stop frees both ports rather than only the app named. Someone reaching for
# it wants the phone tooling out of the way, and remembering which of two dev
# servers is holding a port is not a thing anyone should have to do.
if [ "$STOP" -eq 1 ]; then
    bold "Stopping the Expo dev servers"
    pkill -f "expo start" 2>/dev/null || true
    sleep 1
    free_metro_port "$SUPPLIER_PORT"
    free_metro_port "$CUSTOMER_PORT"
    ok "stopped"
    exit 0
fi

if [ "$APP_KEY" = "customer" ]; then
    APP="$ROOT/vm-client-mobile-ui"
    APP_NAME="Vayalavan (customer)"
    PORT="$CUSTOMER_PORT"
    OTHER_PORT="$SUPPLIER_PORT"
    OTHER_HINT="./scripts/start-mobile.sh supplier"
    SIGN_IN_EMAIL="customer@vayal.test"
    SIGN_IN_VAR="SEED_CUSTOMER_PASSWORD"
    SIGN_IN_NOTE="Browsing needs no account — sign in only to order."
else
    APP="$ROOT/vm-supplier-mobile-ui"
    APP_NAME="Vayalavan Supplier"
    PORT="$SUPPLIER_PORT"
    OTHER_PORT="$CUSTOMER_PORT"
    OTHER_HINT="./scripts/start-mobile.sh customer"
    SIGN_IN_EMAIL="greens@vayal.test"
    SIGN_IN_VAR="SEED_SUPPLIER_PASSWORD"
    SIGN_IN_NOTE="This app is sign-in only."
fi

[ -d "$APP" ] || die "$(basename "$APP") is missing"
cd "$APP"

# --- 1. environment --------------------------------------------------------
bold "Starting $APP_NAME"
[ -f "$ROOT/.env" ] || die ".env is missing. Create it with:  ./scripts/dev-setup.sh"
ok ".env found"

if [ "$PORT" = "$OTHER_PORT" ]; then
    die "MOBILE_METRO_PORT and MOBILE_CLIENT_METRO_PORT are both $PORT — the two
      apps cannot share one Metro port. Change one in .env."
fi

# --- 2. the API address ----------------------------------------------------
#
# The single most common way these apps fail: MOBILE_API_BASE_URL is baked into
# the build and has to name an address the HANDSET can reach, so it goes stale
# every time this laptop changes network. Both apps read the same variable —
# one gateway — so fixing it here fixes it for the other one too.
#
# Only rewritten when it already points somewhere private. A staging or
# production host is left alone — this script has no business redirecting a
# build at a developer's laptop.
API_URL="$(env_value MOBILE_API_BASE_URL)"
API_HOST="$(printf '%s' "$API_URL" | sed -E 's#^[a-z]+://##; s#[:/].*$##')"

is_private_host() {
    case "$1" in
        localhost|127.0.0.1|::1|10.*|192.168.*) return 0 ;;
        172.1[6-9].*|172.2[0-9].*|172.3[01].*)  return 0 ;;
        *) return 1 ;;
    esac
}

if [ "$KEEP_URL" -eq 1 ]; then
    ok "API left as ${API_URL:-<unset>} (--keep-url)"
elif [ -z "$API_URL" ] || is_private_host "$API_HOST"; then
    bold "Pointing the app at this machine"
    "$ROOT/infra/scripts/mobile-api-url.sh" | sed 's/^/  /'
    API_URL="$(env_value MOBILE_API_BASE_URL)"
else
    ok "API is ${API_URL} — a public host, left untouched"
fi

# --- 3. is the backend actually up? ---------------------------------------
#
# A warning, not a failure: someone may deliberately want the UI up against a
# server that is not running yet. But finding out from a spinner on a phone is
# the slowest possible way to learn it.
GATEWAY_PORT="$(port_or GATEWAY_PORT 8080)"
if curl -fsS -m 5 -o /dev/null "http://localhost:${GATEWAY_PORT}/readyz" 2>/dev/null; then
    ok "gateway is up on :${GATEWAY_PORT}"
else
    warn "the gateway is NOT responding on :${GATEWAY_PORT}"
    warn "the app will load but every request will fail — start it with ./scripts/start.sh"
fi

# Prove the API answers on the address the PHONE will use, not just on
# localhost. A gateway bound only to the loopback passes the check above and
# fails on every handset.
if [ -n "$API_URL" ]; then
    if curl -fsS -m 5 -o /dev/null "${API_URL%/}/cutoff" 2>/dev/null; then
        ok "API reachable at ${API_URL}"
    else
        warn "could not reach ${API_URL} from this machine"
        warn "if that address is wrong, fix it with:  make -C infra mobile-ip"
    fi
fi

# --- 4. dependencies -------------------------------------------------------
if [ ! -d node_modules ]; then
    bold "Installing $(basename "$APP") dependencies (first run only)"
    # Its own node_modules, deliberately outside the npm workspaces — see the
    # app's README, "Why this is not an npm workspace".
    npm install --no-audit --no-fund
    ok "dependencies installed"
fi

# --- 5. the port -----------------------------------------------------------
# Metro's own default is 8081, which vm-profile-api owns. Each app has its own
# port instead; free this one if a previous run left it held.
#
# Only THIS app's port, and by pid — pkill on "expo start" would take down the
# other app's dev server, which is exactly what someone running both does not
# want.
if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t >/dev/null 2>&1; then
    warn "port $PORT is in use — freeing it"
    free_metro_port "$PORT"
fi

# --- 6. go -----------------------------------------------------------------
LAN_IP="$(printf '%s' "$API_URL" | sed -E 's#^[a-z]+://##; s#[:/].*$##')"

echo
bold "Starting Expo — $APP_NAME"
cat <<EOF

  Metro          http://${LAN_IP}:${PORT}
  API            ${API_URL:-<unset>}
  Other app      ${OTHER_HINT}  (port ${OTHER_PORT}, run it in another terminal)

  On the phone, open Expo Go and scan the QR code below.

  If it times out, the handset cannot see this machine. Check, in order:
    1. the phone is on the same Wi-Fi (Settings > Wi-Fi > (i) > IP Address
       should start 192.168.1.)
    2. iOS only: Settings > Privacy & Security > Local Network > Expo Go is ON
    3. open http://${LAN_IP}:${PORT}/status in the phone's browser — it should
       say "packager-status:running"
    4. still stuck? Use the phone's Personal Hotspot, join this Mac to it, and
       re-run this script. That removes the router from the picture entirely.

  Sign in as ${SIGN_IN_EMAIL}
  (password = ${SIGN_IN_VAR} in .env)
  ${SIGN_IN_NOTE}

  Stop with Ctrl-C, or ./scripts/start-mobile.sh --stop

EOF

ARGS=(start --port "$PORT")
[ "$TUNNEL" -eq 1 ] && ARGS+=(--tunnel)
[ "$CLEAR" -eq 1 ] && ARGS+=(--clear)
[ -n "$OPEN" ] && ARGS+=("$OPEN")

if [ "$TUNNEL" -eq 1 ]; then
    warn "tunnel mode routes METRO only — ${API_URL} is still a LAN address,"
    warn "so the app will load and then fail every request unless the gateway"
    warn "is exposed publicly too."
fi

# Foreground on purpose: Expo owns the terminal from here, so the QR code
# renders and Ctrl-C stops it.
exec npx expo "${ARGS[@]}"
