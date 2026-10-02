#!/bin/bash
# ---------------------------------------------------------------------------
# Provisions the analytics schema and its three roles (CLAUDE.md §5.4).
#
# Runs on first boot of an empty volume like 01-init.sh, AND is safe to re-run
# against an existing database: `make db-analytics` executes this same file
# inside the running container. Every statement is idempotent, and passwords
# are re-applied so a changed .env takes effect.
#
#   ANALYTICS_DB_USER         owns schema `analytics`. vm-events-consumer-api
#                             migrates and writes it; it can reach no other schema.
#   ANALYTICS_READER_DB_USER  SELECT on `analytics` only, read-only by default.
#                             This is the role vm-analytics-api (Python) uses —
#                             the analytics user never sees profile, catalog
#                             or orders, so never a name, phone or address.
#   EVENTS_CDC_DB_USER        LOGIN REPLICATION, for the consumer's logical
#                             replication stream. Granted NO table at all: it
#                             reads the event outbox tables through the
#                             publication (infra/postgres/cdc-setup.sql), never
#                             by SELECT.
# ---------------------------------------------------------------------------
set -euo pipefail

for required in ANALYTICS_DB_USER ANALYTICS_DB_PASSWORD \
                ANALYTICS_READER_DB_USER ANALYTICS_READER_DB_PASSWORD \
                EVENTS_CDC_DB_USER EVENTS_CDC_DB_PASSWORD; do
    if [ -z "${!required:-}" ]; then
        echo "[analytics] ${required} is not set" >&2
        exit 1
    fi
done

echo "[analytics] provisioning schema analytics and its roles in ${POSTGRES_DB}"

psql -v ON_ERROR_STOP=1 \
  --username "${POSTGRES_USER}" \
  --dbname "${POSTGRES_DB}" \
  -v db="${POSTGRES_DB}" \
  -v writer="${ANALYTICS_DB_USER}"          -v writer_pw="${ANALYTICS_DB_PASSWORD}" \
  -v reader="${ANALYTICS_READER_DB_USER}"   -v reader_pw="${ANALYTICS_READER_DB_PASSWORD}" \
  -v cdc="${EVENTS_CDC_DB_USER}"            -v cdc_pw="${EVENTS_CDC_DB_PASSWORD}" <<'EOSQL'

-- A re-run is expected; "already exists, skipping" is not news.
SET client_min_messages = warning;

-- Roles. CREATE ROLE has no IF NOT EXISTS, hence the \gexec form; the
-- ALTERs then make a re-run converge on what .env says.
SELECT format('CREATE ROLE %I LOGIN', :'writer')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'writer') \gexec
SELECT format('CREATE ROLE %I LOGIN', :'reader')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'reader') \gexec
SELECT format('CREATE ROLE %I LOGIN REPLICATION', :'cdc')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'cdc') \gexec

ALTER ROLE :"writer" WITH LOGIN PASSWORD :'writer_pw';
ALTER ROLE :"reader" WITH LOGIN PASSWORD :'reader_pw';
ALTER ROLE :"cdc"    WITH LOGIN REPLICATION PASSWORD :'cdc_pw';

-- --- analytics (writer) -----------------------------------------------------
CREATE SCHEMA IF NOT EXISTS analytics AUTHORIZATION :"writer";
GRANT CONNECT ON DATABASE :"db" TO :"writer";
ALTER ROLE :"writer" SET search_path = analytics, public;
GRANT USAGE ON SCHEMA public TO :"writer";

-- --- reader ---------------------------------------------------------------
-- SELECT on what exists now and, through default privileges, on every table
-- or view the writer creates later. Read-only by default as a second line:
-- even a mistaken GRANT could not be used without an explicit override.
GRANT CONNECT ON DATABASE :"db" TO :"reader";
GRANT USAGE ON SCHEMA analytics TO :"reader";
GRANT SELECT ON ALL TABLES IN SCHEMA analytics TO :"reader";
ALTER DEFAULT PRIVILEGES FOR ROLE :"writer" IN SCHEMA analytics
    GRANT SELECT ON TABLES TO :"reader";
ALTER ROLE :"reader" SET search_path = analytics;
ALTER ROLE :"reader" SET default_transaction_read_only = on;

-- --- cdc ------------------------------------------------------------------
GRANT CONNECT ON DATABASE :"db" TO :"cdc";

-- --- verify ---------------------------------------------------------------
-- Neither analytics role may reach a service schema. The CDC role is checked
-- in cdc-setup.sql, once the publication exists. psql variables do not reach
-- inside a DO body, so the role names travel as settings.
SELECT set_config('vayal.writer', :'writer', false),
       set_config('vayal.reader', :'reader', false) \gset
DO $$
DECLARE
    leak TEXT;
BEGIN
    SELECT string_agg(r.role || ' -> ' || s.schema, ', ') INTO leak
    FROM (VALUES (current_setting('vayal.writer')), (current_setting('vayal.reader'))) AS r(role)
    CROSS JOIN (VALUES ('profile'), ('catalog'), ('orders')) AS s(schema)
    WHERE EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = s.schema)
      AND (has_schema_privilege(r.role, s.schema, 'USAGE')
        OR has_schema_privilege(r.role, s.schema, 'CREATE'));
    IF leak IS NOT NULL THEN
        RAISE EXCEPTION 'analytics role reaches a service schema: %', leak;
    END IF;
END
$$;
EOSQL

echo "[analytics] done: schema analytics, roles ${ANALYTICS_DB_USER}, ${ANALYTICS_READER_DB_USER}, ${EVENTS_CDC_DB_USER}"
