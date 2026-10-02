#!/usr/bin/env bash
#
# Wipe trading data, keep the shop.
#
# Deletes every order, cart, payment, payout and audit entry, and resets the
# stock counters those orders moved. Products, pack sizes, today's declared
# quantities, suppliers, customers and addresses are all left alone — so after
# this you have the same shop with no sales in it, and earnings read zero.
#
#   ./infra/scripts/reset-orders.sh            # asks first
#   ./infra/scripts/reset-orders.sh --yes      # for scripts
#
# THIS IS DESTRUCTIVE AND CANNOT BE UNDONE. It refuses to run against anything
# that is not a local development database — see the guard below.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONTAINER="${POSTGRES_CONTAINER:-vayal-postgres}"

bold() { printf "\033[1m%s\033[0m\n" "$1"; }
ok()   { printf "  \033[32m✓\033[0m %s\n" "$1"; }
warn() { printf "  \033[33m!\033[0m %s\n" "$1"; }
die()  { printf "  \033[31m✗\033[0m %s\n" "$1" >&2; exit 1; }

[ -f "$ROOT/.env" ] || die ".env not found. Run this from a development checkout."

# Parsed, never sourced: .env holds values with spaces, & and <.
envval() { grep -E "^$1=" "$ROOT/.env" 2>/dev/null | head -1 | cut -d= -f2- | tr -d ' '; }

PGUSER="$(envval POSTGRES_USER)"
PGDB="$(envval POSTGRES_DB)"
APP_ENV="$(envval APP_ENV)"
: "${PGDB:=vayal}"

# --- refuse to run anywhere that might be real ------------------------------
#
# Three independent guards, because one is a typo away from being wrong. This
# script exists to clear test data; running it against live orders would
# destroy payment records that money actually moved through.
[ "$APP_ENV" = "production" ] && die "APP_ENV is production. Refusing."
docker ps --format '{{.Names}}' | grep -qx "$CONTAINER" \
    || die "container '$CONTAINER' is not running. This only targets the local stack."
for url_var in PROFILE_DATABASE_URL ORDERS_DATABASE_URL; do
    url="$(envval "$url_var")"
    case "$url" in
        *@localhost:*|*@127.0.0.1:*|*@postgres:*) ;;
        *) die "$url_var does not point at a local database. Refusing." ;;
    esac
done

psql_do() { docker exec -i "$CONTAINER" psql -U "$PGUSER" -d "$PGDB" -v ON_ERROR_STOP=1 "$@"; }

# --- show what will go -----------------------------------------------------
bold "About to delete"
psql_do -At -F' | ' -c "
SELECT 'orders', count(*) FROM orders.orders
UNION ALL SELECT 'order_items', count(*) FROM orders.order_items
UNION ALL SELECT 'payments', count(*) FROM orders.payments
UNION ALL SELECT 'supplier_payouts', count(*) FROM orders.supplier_payouts
UNION ALL SELECT 'stock_reservations', count(*) FROM orders.stock_reservations
UNION ALL SELECT 'webhook_events', count(*) FROM orders.webhook_events
UNION ALL SELECT 'outbox', count(*) FROM orders.outbox
UNION ALL SELECT 'carts', count(*) FROM orders.carts
UNION ALL SELECT 'admin_audit_log', count(*) FROM orders.admin_audit_log
UNION ALL SELECT 'catalog stock_holds', count(*) FROM catalog.stock_holds
ORDER BY 1;" | sed 's/^/  /'

echo
bold "Will be KEPT"
psql_do -At -F' | ' -c "
SELECT 'products', count(*) FROM catalog.products
UNION ALL SELECT 'product_units', count(*) FROM catalog.product_units
UNION ALL SELECT 'daily_availability rows', count(*) FROM catalog.daily_availability
UNION ALL SELECT 'suppliers', count(*) FROM profile.suppliers
UNION ALL SELECT 'users', count(*) FROM profile.users
UNION ALL SELECT 'addresses', count(*) FROM profile.addresses
ORDER BY 1;" | sed 's/^/  /'

echo
warn "Declared quantities are kept; the sold/reserved counters against them reset to zero."

if [ "${1:-}" != "--yes" ]; then
    echo
    printf "Type %s to continue: " "RESET"
    read -r reply
    [ "$reply" = "RESET" ] || die "Cancelled."
fi

# --- do it, in ONE transaction ---------------------------------------------
#
# Orders and the catalogue's stock counters must move together. Deleting orders
# but leaving reserved_grams and sold_grams behind would mark stock as sold for
# orders that no longer exist — permanently unsellable phantom inventory, and
# the exact reason "just delete from orders" is not enough here. Stock movement
# lives in the catalogue schema by design (no transaction may span two
# schemas), so this is the one place that reconciles both sides.
echo
bold "Resetting"
psql_do <<'SQL'
BEGIN;

-- Orders and everything hanging off them. TRUNCATE ... CASCADE rather than
-- DELETE: it is faster, it resets nothing we depend on, and it cannot leave
-- orphans behind.
TRUNCATE
    orders.order_items,
    orders.stock_reservations,
    orders.payments,
    orders.supplier_payouts,
    orders.webhook_events,
    orders.outbox,
    orders.order_idempotency,
    orders.admin_audit_log,
    orders.cart_items,
    orders.carts,
    orders.orders
CASCADE;

-- The catalogue's side of the same story.
TRUNCATE catalog.stock_holds;

-- Declared quantities survive; only what customers took is cleared. A supplier
-- who said "50 kg today" still has 50 kg, all of it sellable again.
UPDATE catalog.daily_availability
   SET reserved_grams = 0,
       sold_grams     = 0;

COMMIT;
SQL

ok "orders, payments, payouts and carts deleted"
ok "stock holds cleared and sold/reserved counters zeroed"

# --- prove it ---------------------------------------------------------------
echo
bold "After"
psql_do -At -F' | ' -c "
SELECT 'orders', count(*) FROM orders.orders
UNION ALL SELECT 'supplier_payouts', count(*) FROM orders.supplier_payouts
UNION ALL SELECT 'stock_holds', count(*) FROM catalog.stock_holds
UNION ALL SELECT 'availability rows with stock moved',
       count(*) FROM catalog.daily_availability WHERE reserved_grams > 0 OR sold_grams > 0
UNION ALL SELECT 'products (kept)', count(*) FROM catalog.products
UNION ALL SELECT 'suppliers (kept)', count(*) FROM profile.suppliers
ORDER BY 1;" | sed 's/^/  /'

echo
ok "Done. Earnings read zero; the catalogue is intact and fully sellable again."
echo
