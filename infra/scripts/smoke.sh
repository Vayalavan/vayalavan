#!/usr/bin/env bash
#
# Post-deploy smoke test.
#
# Run this against a freshly deployed environment before sending traffic to it.
# It is deliberately READ-MOSTLY: it creates nothing, charges nothing, and
# moves no stock, so it is safe to run against production at any time.
#
#   ./infra/scripts/smoke.sh https://api.vayalmikrogreenz.com
#   ./infra/scripts/smoke.sh http://localhost:8080          # local check
#
# Exit code 0 means safe to route traffic. Anything else means do not.
#
# What it will NOT catch: anything requiring a sign-in, and anything that
# writes. Those are covered by the e2e suite against a staging database — see
# `make test-e2e`. Never point the e2e suite at production; it places orders.
set -uo pipefail

BASE="${1:-}"
if [ -z "$BASE" ]; then
    echo "usage: $0 <api-base-url>   e.g. https://api.example.com" >&2
    exit 2
fi
BASE="${BASE%/}"

# The gateway serves health probes at the ROOT and the JSON API under /api.
# Getting this split wrong is exactly the bug that once made every UI request
# 404 while curl against the same host looked fine, so both are checked.
API="$BASE/api"

pass=0; fail=0
ok()   { printf "  \033[32m✓\033[0m %s\n" "$1"; pass=$((pass+1)); }
bad()  { printf "  \033[31m✗\033[0m %s\n" "$1"; fail=$((fail+1)); }
info() { printf "    %s\n" "$1"; }

req() { curl -sS --max-time 10 -o /tmp/smoke.body -w '%{http_code}' "$@" 2>/dev/null || echo 000; }

echo
echo "Smoke test against $BASE"
echo

# --- 1. liveness and readiness --------------------------------------------
echo "Health"
code=$(req "$BASE/healthz")
[ "$code" = "200" ] && ok "gateway /healthz" || bad "gateway /healthz returned $code"

code=$(req "$BASE/readyz")
if [ "$code" = "200" ]; then
    ok "gateway /readyz — every upstream reachable"
else
    # readyz checks the Go services and their database pools, so a failure
    # here names the broken dependency in the body.
    bad "gateway /readyz returned $code"
    info "$(head -c 300 /tmp/smoke.body)"
fi

# --- 2. the API is actually mounted ---------------------------------------
echo
echo "API"
code=$(req "$API/catalog")
if [ "$code" = "200" ]; then
    count=$(python3 -c "import json,sys;print(len(json.load(open('/tmp/smoke.body'))['products']))" 2>/dev/null || echo "?")
    ok "GET /api/catalog — $count product(s) listed today"
    if [ "$count" = "0" ]; then
        info "note: empty catalogue. Correct before suppliers declare stock,"
        info "      wrong after — check supplier approvals and today's availability."
    fi
else
    bad "GET /api/catalog returned $code"
    info "if this is 404, PUBLIC_API_BASE_URL is probably missing the /api suffix"
fi

code=$(req "$API/cutoff")
if [ "$code" = "200" ]; then
    ok "GET /api/cutoff — the server can compute the IST business day"
    info "$(python3 -c "import json;d=json.load(open('/tmp/smoke.body'));print('cutoff '+d['cutoff_at']+'  before_cutoff='+str(d['before_cutoff']))" 2>/dev/null || true)"
else
    bad "GET /api/cutoff returned $code"
fi

# --- 3. authentication is enforced ----------------------------------------
echo
echo "Access control"
code=$(req "$API/orders")
[ "$code" = "401" ] && ok "GET /api/orders without a token → 401" \
    || bad "GET /api/orders without a token returned $code, expected 401"

code=$(req "$API/admin/dashboard")
[ "$code" = "401" ] && ok "GET /api/admin/dashboard without a token → 401" \
    || bad "admin dashboard returned $code without a token, expected 401"

# The Go services must not be reachable from outside (CLAUDE.md rule 5). This
# only proves the gateway does not proxy to them; the network split is what
# actually enforces it.
code=$(req "$BASE/internal/suppliers/approved-ids")
if [ "$code" = "404" ] || [ "$code" = "401" ]; then
    ok "internal service routes are not exposed through the gateway"
else
    bad "an internal route answered $code — internal endpoints may be public"
fi

# --- 4. security headers ---------------------------------------------------
echo
echo "Headers"
headers=$(curl -sSI --max-time 10 "$BASE/healthz" 2>/dev/null)
check_header() {
    if grep -qi "^$1:" <<<"$headers"; then ok "$1 present"; else bad "$1 missing"; fi
}
check_header "x-content-type-options"
check_header "x-frame-options"

if grep -qi "^access-control-allow-origin: \*" <<<"$headers"; then
    bad "CORS allows any origin — these requests carry credentials"
else
    ok "CORS is not wide open"
fi

# A server header naming the framework and version is free reconnaissance.
if grep -qi "^x-powered-by:" <<<"$headers"; then
    bad "X-Powered-By is exposed"
else
    ok "X-Powered-By is suppressed"
fi

# --- 5. TLS ----------------------------------------------------------------
if [[ "$BASE" == https://* ]]; then
    echo
    echo "TLS"
    if grep -qi "^strict-transport-security:" <<<"$headers"; then
        ok "HSTS present"
    else
        bad "HSTS missing — set it on the TLS terminator"
    fi
elif [[ "$BASE" != http://localhost* && "$BASE" != http://127.* ]]; then
    echo
    bad "this is a plain-HTTP non-local URL; production must be HTTPS"
fi

echo
if [ "$fail" -eq 0 ]; then
    printf "\033[32m%d checks passed. Safe to route traffic.\033[0m\n\n" "$pass"
    exit 0
fi
printf "\033[31m%d of %d checks FAILED. Do not route traffic.\033[0m\n\n" "$fail" "$((pass+fail))"
exit 1
