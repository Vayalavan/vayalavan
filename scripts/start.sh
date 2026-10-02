#!/usr/bin/env bash
#
# Start everything locally, from a cold machine.
#
# One command that leaves you with three working UIs and prints where they are.
# It wraps the Makefile rather than duplicating it: `make doctor` still owns
# the environment checks, `make dev` still owns the process supervision. What
# this adds is the part a Makefile is bad at — waiting until things are
# genuinely reachable, and telling you what to open.
#
#   ./scripts/start.sh            start the stack
#   ./scripts/start.sh --reset    wipe the database and reseed first
#   ./scripts/start.sh --tunnel   also publish the gateway, so Razorpay can call back
#   ./scripts/start.sh --stop     stop everything
#
# --tunnel exists because Razorpay cannot reach localhost, and the webhook is
# the only thing that marks an order paid (CLAUDE.md §6.4). Without a public
# URL every test payment succeeds in Checkout and then sits in
# pending_payment, which looks exactly like a bug in the app. This runs
# cloudflared (or ngrok), waits for the hostname to answer, and prints the
# webhook URL to paste into the dashboard.
#
# The tunnel outlives a restart on purpose: it is bound to the gateway's port,
# not its processes, and a quick tunnel's hostname dies with the process and
# cannot be recovered — so killing it would mean re-registering the webhook in
# the dashboard every time. `./scripts/start.sh --stop` is what takes it down.
#
# That hostname is still random per cloudflared process. For a stable one,
# registered once, set in .env:
#   DEV_TUNNEL_NAME       a cloudflared named tunnel (needs a Cloudflare domain)
#   DEV_TUNNEL_HOSTNAME   the hostname it resolves to; also ngrok's reserved
#                         domain, which the free tier gives you one of
#
set -euo pipefail

# The repository root: this script lives in scripts/.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG="${TMPDIR:-/tmp}/vayal-dev.log"

bold() { printf "\033[1m%s\033[0m\n" "$1"; }
ok()   { printf "  \033[32m✓\033[0m %s\n" "$1"; }
warn() { printf "  \033[33m!\033[0m %s\n" "$1"; }
die()  { printf "  \033[31m✗\033[0m %s\n" "$1" >&2; exit 1; }

# Parsed, never sourced: .env holds values with spaces, & and <.
env_value() {
    grep -E "^$1=" "$ROOT/.env" 2>/dev/null | head -1 | cut -d= -f2- | tr -d ' '
}

gateway_port() {
    local value; value="$(env_value GATEWAY_PORT)"
    echo "${value:-8080}"
}

# Ports this stack owns. Read from .env so changing a port there does not
# leave this script killing the wrong thing — or missing the right one.
app_ports() {
    local names=(GATEWAY_PORT PROFILE_PORT CATALOG_PORT ORDERS_PORT EVENTS_CONSUMER_PORT ANALYTICS_API_PORT
                 CLIENT_UI_PORT ADMIN_UI_PORT SUPPLIER_UI_PORT ANALYTICS_UI_PORT)
    local defaults=(8080 8081 8082 8083 8084 8085 5173 5174 5175 5176)
    local i value
    for i in "${!names[@]}"; do
        value=$(env_value "${names[$i]}")
        echo "${value:-${defaults[$i]}}"
    done
}

TUNNEL_LOG="${TMPDIR:-/tmp}/vayal-tunnel.log"
TUNNEL_PID_FILE="${TMPDIR:-/tmp}/vayal-tunnel.pid"
PUBLIC_URL=""

# Is the tunnel this script started still running?
tunnel_alive() {
    local pid
    [ -f "$TUNNEL_PID_FILE" ] || return 1
    pid=$(cat "$TUNNEL_PID_FILE" 2>/dev/null || true)
    [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

# DEV_TUNNEL_HOSTNAME as a bare host: no scheme, no path.
#
# The tempting mistake is to paste back the webhook URL the script printed, so
# strip that down rather than building requests against
# https://host/webhooks/razorpay/healthz and reporting a healthy tunnel dead.
tunnel_host() {
    local host; host="$(env_value DEV_TUNNEL_HOSTNAME)"
    host="${host#http://}"; host="${host#https://}"
    echo "${host%%/*}"
}

# The hostname it was given, read back from its own log.
tunnel_url() {
    local host; host="$(tunnel_host)"
    # A configured hostname is only really claimed when a named tunnel backs it,
    # or when ngrok reserved it. Otherwise whatever is running is a quick tunnel
    # and only its own log knows the hostname — the same rule start_tunnel uses,
    # so the two never disagree about what to print.
    if [ -n "$host" ] &&
       { [ -n "$(env_value DEV_TUNNEL_NAME)" ] ||
         [ "$(env_value DEV_TUNNEL_PROVIDER)" = "ngrok" ]; }; then
        echo "https://$host"
        return 0
    fi
    grep -Eo 'https://[a-zA-Z0-9.-]+\.(trycloudflare\.com|ngrok[a-z.-]*\.app|ngrok\.io)' \
        "$TUNNEL_LOG" 2>/dev/null | head -1 || true
}

# Does this URL actually serve our gateway right now?
#
# Falls back to a public resolver: some home routers refuse to resolve
# trycloudflare.com, and a tunnel that this laptop cannot look up is still
# perfectly reachable by Razorpay, which is the only resolver that matters.
tunnel_reachable() {
    local url="$1"
    [ "$(curl -s -o /dev/null -m 6 -w '%{http_code}' "$url/healthz" || echo 000)" = "200" ] && return 0
    [ "$(curl -s -o /dev/null -m 8 --doh-url https://cloudflare-dns.com/dns-query \
        -w '%{http_code}' "$url/healthz" 2>/dev/null || echo 000)" = "200" ]
}

# Returns 0 only if there was a tunnel of ours to stop.
stop_tunnel() {
    local pid port had=1; port="$(gateway_port)"
    if [ -f "$TUNNEL_PID_FILE" ]; then
        pid=$(cat "$TUNNEL_PID_FILE" 2>/dev/null || true)
        [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
        rm -f "$TUNNEL_PID_FILE"
        had=0
    fi
    # A crashed run leaves no pid file. Both patterns are scoped to OUR gateway
    # port, so a cloudflared or ngrok serving something else survives.
    pkill -f "cloudflared tunnel.*localhost:$port" 2>/dev/null || true
    pkill -f "ngrok http.*$port" 2>/dev/null || true
    return $had
}

# Starts the tunnel and leaves the public https URL in PUBLIC_URL.
start_tunnel() {
    local port provider name host protocol tries
    port="$(gateway_port)"
    provider="$(env_value DEV_TUNNEL_PROVIDER)"
    name="$(env_value DEV_TUNNEL_NAME)"
    # cloudflared prefers QUIC, which needs long-lived outbound UDP. Networks
    # that mangle or idle-drop it leave the control stream failing until the
    # edge deregisters the hostname, which looks exactly like the tunnel being
    # fine (the process is still there) until Razorpay cannot resolve it.
    # http2 is the documented fallback and is what this defaults to.
    protocol="$(env_value DEV_TUNNEL_PROTOCOL)"
    protocol="${protocol:-http2}"
    host="$(tunnel_host)"

    if [ -z "$provider" ]; then
        if command -v cloudflared >/dev/null 2>&1; then provider=cloudflared
        elif command -v ngrok >/dev/null 2>&1; then provider=ngrok
        else
            warn "no tunnel tool found. Install one:  brew install cloudflared"
            return 1
        fi
    fi
    command -v "$provider" >/dev/null 2>&1 || { warn "$provider is not installed"; return 1; }

    # DEV_TUNNEL_NAME and DEV_TUNNEL_HOSTNAME describe a hostname you OWN: they
    # are what the tunnel is told to claim. Neither is a place to record the URL
    # a quick tunnel generated — that URL is an output, it cannot be asked for
    # again, and putting it here sends the run down the named-tunnel path with a
    # name cloudflared rejects. Caught here because the failure otherwise looks
    # like a broken tunnel rather than a wrong setting.
    case "$name" in
        *://*|*/*)
            warn "DEV_TUNNEL_NAME looks like a URL. It is the NAME of a cloudflared"
            warn "named tunnel (e.g. vayal-dev) — ignoring it for this run."
            name="" ;;
    esac
    if [ -n "$host" ] && [ "$host" != "$(env_value DEV_TUNNEL_HOSTNAME)" ]; then
        warn "DEV_TUNNEL_HOSTNAME takes a bare hostname, not a URL — using $host"
    fi
    if [ -n "$host" ] && [ -z "$name" ] && [ "$provider" = "cloudflared" ]; then
        warn "DEV_TUNNEL_HOSTNAME is set but DEV_TUNNEL_NAME is not. cloudflared"
        warn "can only claim a hostname through a named tunnel, so this run gets a"
        warn "random one — see .env.example."
        host=""
    fi

    # A quick tunnel's hostname dies with its process and can never be
    # recovered, so restarting one that already works would silently invalidate
    # the URL registered in the Razorpay dashboard. Reuse it instead.
    if tunnel_alive; then
        PUBLIC_URL="$(tunnel_url)"
        if [ -n "$PUBLIC_URL" ] && tunnel_reachable "$PUBLIC_URL"; then
            ok "tunnel already running — same hostname, nothing to re-register"
            return 0
        fi
        # Cloudflare can tear down a quick tunnel's edge registration while the
        # process lives on retrying forever. The hostname stops resolving, and
        # Razorpay rejects it with "no such host" — so a running pid proves
        # nothing and this has to be checked, not assumed.
        warn "the running tunnel stopped answering — replacing it"
        PUBLIC_URL=""
        stop_tunnel
    fi

    stop_tunnel
    : > "$TUNNEL_LOG"

    case "$provider" in
        cloudflared)
            if [ -n "$name" ]; then
                nohup cloudflared tunnel run --protocol "$protocol" \
                    --url "http://localhost:$port" "$name" >"$TUNNEL_LOG" 2>&1 &
            else
                nohup cloudflared tunnel --protocol "$protocol" \
                    --url "http://localhost:$port" >"$TUNNEL_LOG" 2>&1 &
            fi ;;
        ngrok)
            if [ -n "$host" ]; then
                nohup ngrok http "$port" --url "https://$host" --log stdout \
                    >"$TUNNEL_LOG" 2>&1 &
            else
                nohup ngrok http "$port" --log stdout >"$TUNNEL_LOG" 2>&1 &
            fi ;;
        *) warn "unknown DEV_TUNNEL_PROVIDER=$provider (use cloudflared or ngrok)"
           return 1 ;;
    esac
    echo $! >"$TUNNEL_PID_FILE"

    # A named tunnel already knows its hostname; a quick one is assigned a
    # random hostname it only announces in its own log, so read it back rather
    # than guessing at it.
    tries=0
    while [ $tries -lt 30 ]; do
        if [ -n "$name" ] && [ -n "$host" ]; then
            PUBLIC_URL="https://$host"
            break
        fi
        PUBLIC_URL=$(grep -Eo 'https://[a-zA-Z0-9.-]+\.(trycloudflare\.com|ngrok[a-z.-]*\.app|ngrok\.io)' \
            "$TUNNEL_LOG" 2>/dev/null | head -1 || true)
        [ -n "$PUBLIC_URL" ] && break
        tries=$((tries + 1))
        sleep 1
    done

    if [ -z "$PUBLIC_URL" ]; then
        warn "$provider never announced a hostname — see $TUNNEL_LOG"
        return 1
    fi

    # Reachable, not merely printed: the hostname is announced before it has
    # finished propagating, and a URL that 502s is worth knowing about before
    # it is pasted into the dashboard.
    tries=0
    while [ $tries -lt 12 ]; do
        if tunnel_reachable "$PUBLIC_URL"; then
            ok "tunnel up ($provider/$protocol), gateway answering through it"
            return 0
        fi
        tries=$((tries + 1))
        sleep 1
    done

    # Never hand over a URL that has not answered: Razorpay resolves the host
    # when the webhook is saved and refuses it outright, so a hopeful guess here
    # costs a round trip through the dashboard to find out.
    warn "$PUBLIC_URL never answered — see $TUNNEL_LOG"
    PUBLIC_URL=""
    return 1
}

stop_stack() {
    bold "Stopping Vayal"

    # The process manager supervises the apps, so killing it takes most of the
    # tree with it.
    pkill -f "concurrently" 2>/dev/null || true
    pkill -f "air -c" 2>/dev/null || true
    pkill -f "vite" 2>/dev/null || true
    sleep 1

    # Then free the ports directly. A crashed run can leave an orphan that
    # matches none of the patterns above, and "port already in use" on the next
    # start is the most annoying way to find that out. Scoped to OUR ports from
    # .env, so nothing else on the machine is touched.
    local port pids
    for port in $(app_ports); do
        pids=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null || true)
        [ -n "$pids" ] || continue
        # SIGTERM first so a service can close its database pool cleanly.
        kill $pids 2>/dev/null || true
        sleep 0.5
        pids=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null || true)
        if [ -n "$pids" ]; then
            kill -9 $pids 2>/dev/null || true
            warn "port $port needed SIGKILL"
        else
            ok "port $port freed"
        fi
    done

    # Deliberately NOT stopping the tunnel. It points at the gateway's PORT,
    # not at any process, so it survives a restart and reconnects by itself —
    # whereas killing it would burn the hostname the Razorpay dashboard is
    # pointed at, and every restart would mean re-registering the webhook.
    # Only --stop takes it down.
    if tunnel_alive; then
        ok "tunnel left up: $(tunnel_url)"
    fi

    # Docker keeps running: Postgres data survives a restart, which is the
    # whole point of `--reset` being a separate flag.
    ok "apps stopped (Postgres, MinIO and MailHog left running)"
    echo "  Stop those too with:  cd infra && docker compose down"
}

RESET=0 TUNNEL=0

for arg in "$@"; do
    case "$arg" in
        --reset)  RESET=1 ;;
        --tunnel) TUNNEL=1 ;;
        --stop)   stop_stack; stop_tunnel && ok "tunnel stopped"; exit 0 ;;
        # Prints the whole header block, however long it grows — a hardcoded
        # line range silently truncates the usage list the moment a flag is
        # added, which is exactly when someone is reading it.
        --help|-h) sed -n '2,/^set -/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) die "unknown option: $arg  (try --help)" ;;
    esac
done

cd "$ROOT"

# --- 1. anything already running? -----------------------------------------
#
# Before the port checks, not after: `make doctor` fails on a busy port, and a
# previous run of this very script is the most likely thing holding one. It
# would be absurd to refuse to restart because we are already running.
for port in 8080 8081 8082 8083 8084 8085 5173 5174 5175 5176; do
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN -t >/dev/null 2>&1; then
        warn "a stack is already running — restarting it"
        stop_stack
        echo
        break
    fi
done

# --- 2. environment --------------------------------------------------------
bold "Checking your environment"
[ -f .env ] || die ".env is missing. Copy it with:  cp .env.example .env"
if ! make -C infra doctor >/dev/null 2>&1; then
    warn "make doctor reported problems:"
    make -C infra doctor || true
    die "fix the above, then run ./scripts/start.sh again"
fi
ok "tooling, ports and .env look good"

# --- 3. dependencies -------------------------------------------------------
if [ ! -d node_modules ]; then
    bold "Installing JavaScript dependencies (first run only)"
    npm install
fi

# The three UIs import the built package, not its source, so a stale dist is
# invisible until something behaves oddly at runtime. Cheap to rebuild.
bold "Building the shared UI kit"
npm run build --workspace @vayal/ui-kit >/dev/null
ok "@vayal/ui-kit built"

# --- 4. infrastructure -----------------------------------------------------
bold "Starting Postgres, MinIO and MailHog"
make -C infra up >/dev/null
ok "containers up"

if [ "$RESET" -eq 1 ]; then
    bold "Resetting the database"
    make -C infra reset
    ok "database wiped, migrated and reseeded"
else
    make -C infra migrate-up >/dev/null 2>&1 && ok "migrations up to date" \
        || warn "migrations did not run — try ./scripts/start.sh --reset"
fi

# --- 5. the apps -----------------------------------------------------------
bold "Starting the services and UIs"
: > "$LOG"
( cd "$ROOT/infra" && nohup make dev >"$LOG" 2>&1 & )

# Poll rather than sleep a fixed amount: Go builds are slow on a cold cache and
# fast afterwards, and a fixed wait is either wrong or wasteful.
wait_for() {
    local port="$1" name="$2" tries=0
    while [ $tries -lt 90 ]; do
        if lsof -nP -iTCP:"$port" -sTCP:LISTEN -t >/dev/null 2>&1; then
            ok "$name on :$port"
            return 0
        fi
        tries=$((tries + 1))
        sleep 1
    done
    warn "$name did not come up on :$port — see $LOG"
    return 1
}

failed=0
wait_for 8081 "profile-api " || failed=1
wait_for 8082 "catalog-api " || failed=1
wait_for 8083 "orders-api  " || failed=1
wait_for 8084 "events-cons." || failed=1
wait_for 8085 "analytics   " || failed=1
wait_for 8080 "gateway     " || failed=1
wait_for 5173 "client UI   " || failed=1
wait_for 5174 "admin UI    " || failed=1
wait_for 5175 "supplier UI " || failed=1
wait_for 5176 "analytics UI" || failed=1

# Listening is not the same as healthy: a service can hold the port while its
# database pool is still failing.
bold "Health"
for svc in "8080 gateway" "8081 profile" "8082 catalog" "8083 orders" "8084 events-consumer" "8085 analytics"; do
    set -- $svc
    code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:$1/readyz" || echo 000)
    if [ "$code" = "200" ]; then ok "$2 ready"; else warn "$2 /readyz returned $code"; failed=1; fi
done

# --- 6. the public tunnel (--tunnel only) ----------------------------------
if [ "$TUNNEL" -eq 1 ]; then
    bold "Publishing the gateway for Razorpay"
    start_tunnel || failed=1

    # A webhook that arrives and fails its signature check is worse than no
    # webhook: it looks like Razorpay's fault. Cheap to catch here.
    secret="$(env_value RAZORPAY_WEBHOOK_SECRET)"
    if [ -z "$secret" ] || [ "$secret" = "local_dev_razorpay_webhook_secret" ]; then
        warn "RAZORPAY_WEBHOOK_SECRET is still the placeholder — set it in .env"
        warn "and in the dashboard webhook to the SAME value, or every event 401s"
    fi
elif tunnel_alive; then
    # Started by an earlier run and survived this restart, which is the whole
    # point. Say so, or it looks like the webhook URL went away with the stack.
    PUBLIC_URL="$(tunnel_url)"
fi

echo
if [ "$failed" -ne 0 ]; then
    warn "some things did not start cleanly. Logs: tail -f $LOG"
else
    bold "Vayalavan is running"
fi

cat <<EOF

  Shop (customer)     http://localhost:5173
  Admin               http://localhost:5174
  Supplier            http://localhost:5175
  Analytics (remote)  http://localhost:5176   loaded by Admin → Analytics

  API gateway         http://localhost:8080/api
  Analytics pipeline  events-consumer :8084 → analytics-api :8085 (internal;
                      reports at http://localhost:8080/api/analytics/summary)
  Email inbox         http://localhost:8025
  File storage        http://localhost:9001

  Sign in with the seeded accounts — passwords are the SEED_*_PASSWORD
  values in your .env:

    admin@vayal.test        admin
    analyst@vayal.test      analyst — admin console, Analytics tab only
    farm@vayal.test         supplier (Nilgiri Microfarms)
    customer@vayal.test     customer

EOF

if [ -n "$PUBLIC_URL" ]; then
    cat <<EOF
  Razorpay webhook    $PUBLIC_URL/webhooks/razorpay

  Register that URL in the Razorpay dashboard (TEST mode) under
  Settings -> Webhooks, subscribed to payment.captured, payment.failed and
  refund.processed, with the secret set to your RAZORPAY_WEBHOOK_SECRET.

  Until an event arrives there a paid order stays in pending_payment — the
  webhook is the only thing that marks it paid. Test card 4111 1111 1111 1111,
  any future expiry, any CVV, OTP 1234.

EOF
    if [ -z "$(env_value DEV_TUNNEL_NAME)$(env_value DEV_TUNNEL_HOSTNAME)" ]; then
        warn "that hostname belongs to this cloudflared process. It survives"
        warn "./scripts/start.sh restarts, but ./scripts/start.sh --stop burns it for good and the"
        warn "next one differs. DEV_TUNNEL_NAME / DEV_TUNNEL_HOSTNAME pin a fixed"
        warn "one you register once — see .env.example."
        echo
    fi
fi

echo "  Logs    tail -f $LOG"
if [ -n "$PUBLIC_URL" ]; then
    echo "  Tunnel  tail -f $TUNNEL_LOG"
fi
echo "  Stop    ./scripts/start.sh --stop"
echo

