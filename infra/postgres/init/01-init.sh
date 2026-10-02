#!/bin/bash
# ---------------------------------------------------------------------------
# Provisions the `vayal` database for local development.
#
# Runs exactly once, on first boot of an empty pgdata volume. To re-run after
# editing this file: `make reset` (destroys the volume) then `make dev`.
#
# The database itself is created by the postgres entrypoint from POSTGRES_DB.
# This script adds the per-service schemas and roles.
#
# Isolation model (CLAUDE.md §3: "a service may only read/write its own
# schema"): each schema is OWNED by its service role. Postgres grants no
# cross-schema access by default, and we revoke the PUBLIC grants that would
# otherwise punch a hole in that, so profile physically cannot read catalog.
# ---------------------------------------------------------------------------
set -euo pipefail

echo "[init] provisioning schemas and per-service roles in ${POSTGRES_DB}"

psql -v ON_ERROR_STOP=1 \
  --username "${POSTGRES_USER}" \
  --dbname "${POSTGRES_DB}" \
  -v db="${POSTGRES_DB}" \
  -v profile_user="${PROFILE_DB_USER}" -v profile_pw="${PROFILE_DB_PASSWORD}" \
  -v catalog_user="${CATALOG_DB_USER}" -v catalog_pw="${CATALOG_DB_PASSWORD}" \
  -v orders_user="${ORDERS_DB_USER}"   -v orders_pw="${ORDERS_DB_PASSWORD}" <<'EOSQL'

-- Extensions used across schemas. CITEXT backs users.email (CLAUDE.md §5.1).
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pgcrypto;
-- Trigram index support for case-insensitive product name search
-- (vm-catalog-api). Created here because only the superuser may install an
-- extension; a service migration attempting it fails with permission denied.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Deny-by-default. Without these, every role inherits PUBLIC's grants and the
-- per-schema isolation below would be decorative.
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON DATABASE :"db" FROM PUBLIC;

-- --- profile ---------------------------------------------------------------
CREATE ROLE :"profile_user" LOGIN PASSWORD :'profile_pw';
CREATE SCHEMA profile AUTHORIZATION :"profile_user";
GRANT CONNECT ON DATABASE :"db" TO :"profile_user";
-- Resolves unqualified table names to the service's own schema. `public` is
-- appended so shared extension types (citext, gen_random_uuid) resolve.
ALTER ROLE :"profile_user" SET search_path = profile, public;
GRANT USAGE ON SCHEMA public TO :"profile_user";

-- --- catalog ---------------------------------------------------------------
CREATE ROLE :"catalog_user" LOGIN PASSWORD :'catalog_pw';
CREATE SCHEMA catalog AUTHORIZATION :"catalog_user";
GRANT CONNECT ON DATABASE :"db" TO :"catalog_user";
ALTER ROLE :"catalog_user" SET search_path = catalog, public;
GRANT USAGE ON SCHEMA public TO :"catalog_user";

-- --- orders ----------------------------------------------------------------
CREATE ROLE :"orders_user" LOGIN PASSWORD :'orders_pw';
CREATE SCHEMA orders AUTHORIZATION :"orders_user";
GRANT CONNECT ON DATABASE :"db" TO :"orders_user";
ALTER ROLE :"orders_user" SET search_path = orders, public;
GRANT USAGE ON SCHEMA public TO :"orders_user";

EOSQL

echo "[init] done: schemas profile, catalog, orders created with owner roles"
