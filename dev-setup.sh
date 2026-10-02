#!/usr/bin/env bash
#
# One-time (and safely repeatable) setup of a development machine.
#
# Takes a fresh copy of the repository to "./start.sh works": checks the
# prerequisites, creates .env, installs every app's dependencies, starts
# Postgres/MinIO/MailHog, provisions the database (schemas, roles, migrations,
# analytics publication and slot), seeds it, and fills analytics from the seed.
#
#   ./dev-setup.sh                 set up (or bring an existing checkout up to date)
#   ./dev-setup.sh --with-mobile   also install both Expo apps' dependencies
#   ./dev-setup.sh --reset         DESTROY the local database first and rebuild it
#
# Then:
#   ./start.sh                     run everything
#
# Re-running is safe: nothing is reinstalled that is already current, .env is
# never overwritten (only variables it lacks are added), migrations and seed
# are idempotent.
#
# ---------------------------------------------------------------------------
# PREREQUISITES — install these first. The script checks each one and stops
# with the fix if anything is missing.
#
#   Required
#     macOS or Linux, bash, curl, lsof
#     Go 1.25+            https://go.dev/dl/          (brew install go)
#     Node.js 20.12+      https://nodejs.org          (brew install node)  — npm ships with it
#     Docker + Compose v2 Docker Desktop or Colima    — and it must be RUNNING
#     uv                  brew install uv             — runs vm-analytics-api; it
#                                                       downloads its own Python 3.12
#     ~10 GB free disk, and these ports free:
#       8080-8085 (gateway + APIs), 5173-5176 (UIs), 5432, 9000, 9001, 1025, 8025
#
#   Optional — only for the features named
#     Razorpay TEST keys  real checkout. Put them in .env (RAZORPAY_KEY_ID,
#                         RAZORPAY_KEY_SECRET, RAZORPAY_WEBHOOK_SECRET and
#                         VITE_RAZORPAY_KEY_ID). Without them the storefront
#                         works but a payment cannot complete.
#     cloudflared/ngrok   Razorpay webhooks reaching localhost (./start.sh --tunnel)
#     Xcode / Android     the mobile apps (--with-mobile, then make -C infra mobile)
#     Studio, Expo Go
#
#   Not needed: Python (uv brings its own), sqlc (generated code is committed),
#   air and goose (installed into .tools/bin by this script), a local Postgres.
# ---------------------------------------------------------------------------
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

WITH_MOBILE=0
RESET=0
for arg in "$@"; do
    case "$arg" in
        --with-mobile) WITH_MOBILE=1 ;;
        --reset)       RESET=1 ;;
        -h|--help)     sed -n '2,45p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "unknown option: $arg (see --help)" >&2; exit 2 ;;
    esac
done

bold() { printf "\n\033[1m%s\033[0m\n" "$1"; }
ok()   { printf "  \033[32m✓\033[0m %s\n" "$1"; }
warn() { printf "  \033[33m!\033[0m %s\n" "$1"; }
fail() { printf "  \033[31m✗\033[0m %s\n" "$1" >&2; }
die()  { fail "$1"; exit 1; }

# Runs a step quietly; on failure shows its output, which is where the reason is.
LOG="${TMPDIR:-/tmp}/vayal-dev-setup.log"
: >"$LOG"
step() {
    local label="$1"; shift
    printf "  … %s" "$label"
    if "$@" >>"$LOG" 2>&1; then
        printf "\r  \033[32m✓\033[0m %s\n" "$label"
    else
        printf "\r  \033[31m✗\033[0m %s\n" "$label"
        echo "    ---- last lines of $LOG ----" >&2
        tail -25 "$LOG" | sed 's/^/    /' >&2
        exit 1
    fi
}

# version_ge 1.25.3 1.25 → true. Numeric, field by field; no `sort -V`.
version_ge() {
    awk -v a="$1" -v b="$2" 'BEGIN {
        n = split(a, x, "."); m = split(b, y, ".");
        for (i = 1; i <= (n > m ? n : m); i++) {
            if ((x[i] + 0) > (y[i] + 0)) exit 0;
            if ((x[i] + 0) < (y[i] + 0)) exit 1;
        }
        exit 0 }'
}

# ---------------------------------------------------------------------------
bold "1/7  Prerequisites"
# ---------------------------------------------------------------------------
missing=0
need() {  # need <command> <how to install>
    if command -v "$1" >/dev/null 2>&1; then return 0; fi
    fail "$1 is not installed — $2"; missing=1; return 1
}

case "$(uname -s)" in
    Darwin|Linux) ok "$(uname -s) $(uname -m)" ;;
    *) die "unsupported OS $(uname -s): use macOS or Linux (on Windows, WSL2)" ;;
esac

need curl "install curl" && ok "curl"
need lsof "brew install lsof / apt install lsof" && ok "lsof"

if need go "https://go.dev/dl/ (brew install go) — 1.25 or newer"; then
    gov="$(go env GOVERSION | sed 's/^go//')"
    if version_ge "$gov" 1.25; then ok "go $gov"
    else fail "go $gov is too old — the modules need 1.25+ (brew upgrade go)"; missing=1; fi
fi

if need node "https://nodejs.org (brew install node) — 20.12 or newer"; then
    nodev="$(node --version | sed 's/^v//')"
    if version_ge "$nodev" 20.12; then ok "node $nodev"
    else fail "node $nodev is too old — need 20.12+"; missing=1; fi
    need npm "ships with Node.js — reinstall Node" && ok "npm $(npm --version)"
fi

if need docker "install Docker Desktop (or: brew install colima docker docker-compose)"; then
    if docker compose version >/dev/null 2>&1; then ok "docker compose $(docker compose version --short 2>/dev/null)"
    else fail "Docker Compose v2 is missing — update Docker Desktop"; missing=1; fi
    if docker info >/dev/null 2>&1; then ok "docker daemon running"
    else fail "Docker is installed but not running — start Docker Desktop (or: colima start)"; missing=1; fi
fi

need uv "brew install uv (or: curl -LsSf https://astral.sh/uv/install.sh | sh)" && ok "uv $(uv --version | awk '{print $2}')"

if [ "$WITH_MOBILE" -eq 1 ]; then
    command -v xcrun >/dev/null 2>&1 || warn "Xcode not found — iOS simulator builds will not work (Expo Go on a phone still does)"
fi

[ "$missing" -eq 0 ] || die "install the prerequisites above, then run ./dev-setup.sh again"

# Ports. The infrastructure ones are FATAL now — a Homebrew Postgres on 5432
# would make the Docker step fail with an unhelpful error — unless it is our
# own container holding them. App ports only matter for ./start.sh.
blocked=""
for port in 5432 9000 9001 1025 8025; do
    holder="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR==2{print $1}')"
    case "$holder" in
        ""|com.docke*|docker*|vpnkit*|limactl*|ssh) ;;   # free, or Docker/Colima
        *) blocked="$blocked $port($holder)" ;;
    esac
done
[ -z "$blocked" ] || die "ports needed by Postgres/MinIO/MailHog are taken:$blocked — stop those (e.g. brew services stop postgresql) and re-run"
ok "infrastructure ports available (5432, 9000, 9001, 1025, 8025: free or held by Docker)"

busy=""
for port in 8080 8081 8082 8083 8084 8085 5173 5174 5175 5176; do
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN -t >/dev/null 2>&1; then busy="$busy $port"; fi
done
[ -z "$busy" ] && ok "app ports free" \
    || warn "app ports in use:$busy — fine if it is this stack already running; otherwise free them before ./start.sh"

# ---------------------------------------------------------------------------
bold "2/7  Configuration (.env)"
# ---------------------------------------------------------------------------
if [ ! -f .env ]; then
    cp .env.example .env
    ok "created .env from .env.example (local-only development values)"
else
    # Never overwrite: it may hold the developer's own keys. Add only what a
    # newer .env.example declares and this .env lacks — the usual cause of
    # "it works on mine".
    added=0
    while IFS= read -r line; do
        key="${line%%=*}"
        if ! grep -qE "^${key}=" .env; then
            [ "$added" -eq 0 ] && printf '\n# Added by dev-setup.sh on %s from .env.example\n' "$(date +%F)" >> .env
            printf '%s\n' "$line" >> .env
            added=$((added + 1))
            warn "added missing $key"
        fi
    done < <(grep -E '^[A-Z][A-Z0-9_]*=' .env.example)
    [ "$added" -eq 0 ] && ok ".env up to date with .env.example" || ok "added $added variable(s) to .env"
fi

if grep -qE '^RAZORPAY_KEY_ID=rzp_test_placeholder' .env 2>/dev/null; then
    warn "Razorpay keys are placeholders — checkout will not complete until you add test keys to .env"
fi

# ---------------------------------------------------------------------------
bold "3/7  Dependencies"
# ---------------------------------------------------------------------------
# One Makefile target owns this, so a new app is added in one place:
# npm workspaces, the Go workspace, air/goose into .tools/bin, uv for the
# Python service (and its Python 3.12), and the shared UI kit build.
step "npm, Go modules, air/goose, uv/Python, @vayal/ui-kit" make -C infra deps
step "Go modules downloaded for every service" go mod download all
if [ "$WITH_MOBILE" -eq 1 ]; then
    step "mobile apps (Expo, both)" make -C infra mobile-deps
fi

# ---------------------------------------------------------------------------
bold "4/7  Local infrastructure (Docker)"
# ---------------------------------------------------------------------------
if [ "$RESET" -eq 1 ]; then
    warn "--reset: destroying the local database and object storage volumes"
    step "removing containers and volumes" docker compose --env-file .env -f infra/docker-compose.yml down -v --remove-orphans
fi
# `up` recreates Postgres if its config changed (e.g. a checkout that predates
# logical replication) — the data volume is kept.
step "postgres, minio, mailhog up and healthy" make -C infra up

# ---------------------------------------------------------------------------
bold "5/7  Database"
# ---------------------------------------------------------------------------
# migrate-up: analytics roles → every service's migrations in dependency order
# → the analytics publication and replication slot (CLAUDE.md §5.4).
step "roles, migrations, CDC publication + slot" make -C infra migrate-up
step "seed data (admin, analyst, suppliers, customer, produce)" make -C infra seed

# ---------------------------------------------------------------------------
bold "6/7  Analytics backfill"
# ---------------------------------------------------------------------------
# The seed writes rows directly, so no events exist for them yet. These write
# one snapshot event per existing supplier, product and order; the consumer
# applies them when ./start.sh first runs it (the slot made above holds them).
# Re-running is harmless: the projections upsert.
step "supplier snapshots" bash -c 'cd vm-profile-api && go run ./cmd/api -emit-snapshots'
step "product snapshots"  bash -c 'cd vm-catalog-api && go run ./cmd/api -emit-snapshots'
step "order snapshots"    bash -c 'cd vm-orders-api && go run ./internal/devtools -emit-snapshots'

# ---------------------------------------------------------------------------
bold "7/7  Check"
# ---------------------------------------------------------------------------
# Builds every Go service and type-checks the gateway and UIs: catches a broken
# checkout now rather than as one red line among ten processes later.
step "Go services build" bash -c 'for d in vm-profile-api vm-catalog-api vm-orders-api vm-events-consumer-api; do (cd "$d" && go build ./...) || exit 1; done'
step "gateway, UIs type-check" bash -c 'for w in vm-gateway-api vm-client-ui vm-admin-ui vm-supplier-ui vm-analytics-ui; do npm run lint --workspace "$w" --silent || exit 1; done'
step "analytics API imports" bash -c 'cd vm-analytics-api && uv run python -c "import fastapi, psycopg, psycopg_pool"'

env_value() { grep -E "^$1=" .env | tail -1 | cut -d= -f2- | sed -e 's/^"//' -e 's/"$//'; }

cat <<EOF

$(printf "\033[1m%s\033[0m" "Setup complete.") Start everything with:

    ./start.sh

  Shop        http://localhost:5173
  Admin       http://localhost:5174
  Supplier    http://localhost:5175
  Email       http://localhost:8025     MailHog: every outgoing email lands here

  Seeded logins (passwords are the SEED_*_PASSWORD values in .env):
    $(env_value SEED_ADMIN_EMAIL)        admin
    $(env_value SEED_ANALYST_EMAIL)      analyst (admin console, Analytics tab only)
    greens@vayal.test       supplier (Kaveri Greens)
    farm@vayal.test         supplier (Nilgiri Microfarms)
    customer@vayal.test     customer

  Optional next steps:
    • Real checkout: add Razorpay TEST keys to .env, then ./start.sh --tunnel
    • Mobile apps:   ./dev-setup.sh --with-mobile, then make -C infra mobile
    • Health check:  make -C infra doctor   (while nothing is running)

  Full log of this run: $LOG
EOF
