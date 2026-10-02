#!/bin/bash
# ---------------------------------------------------------------------------
# Asserts that schema isolation actually holds, at provisioning time.
#
# Cross-schema isolation is a security property that fails silently when it
# regresses — a stray GRANT in a later migration would go unnoticed until it
# mattered. This check runs on every fresh stack and fails startup loudly if
# one service role can reach another's schema.
# ---------------------------------------------------------------------------
set -euo pipefail

echo "[verify] checking cross-schema isolation"

# has_schema_privilege() answers the question directly, without needing to
# connect as each role (which would require their passwords in this context).
result="$(psql -qtAX -v ON_ERROR_STOP=1 \
  --username "${POSTGRES_USER}" --dbname "${POSTGRES_DB}" \
  -v profile_user="${PROFILE_DB_USER}" \
  -v catalog_user="${CATALOG_DB_USER}" \
  -v orders_user="${ORDERS_DB_USER}" <<'EOSQL'
SELECT string_agg(violation, ', ')
FROM (
    SELECT r.role || ' -> ' || s.schema AS violation
    FROM (VALUES (:'profile_user', 'profile'),
                 (:'catalog_user', 'catalog'),
                 (:'orders_user',  'orders')) AS r(role, own)
    CROSS JOIN (VALUES ('profile'), ('catalog'), ('orders')) AS s(schema)
    WHERE s.schema <> r.own
      AND (has_schema_privilege(r.role, s.schema, 'USAGE')
        OR has_schema_privilege(r.role, s.schema, 'CREATE'))
) v;
EOSQL
)"

if [ -n "${result}" ]; then
    echo "[verify] FAILED — cross-schema access detected: ${result}" >&2
    exit 1
fi

echo "[verify] ok: each service role can reach only its own schema"
