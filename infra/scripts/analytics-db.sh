#!/usr/bin/env bash
#
# The superuser half of the analytics pipeline's database setup
# (CLAUDE.md §5.4), in the two steps that bracket the service migrations:
#
#   analytics-db.sh roles   schema `analytics` and its three roles — before
#                           migrations, which need the writer role to exist
#   analytics-db.sh cdc     the logical-replication publication and slot —
#                           after migrations, which create the outbox tables
#
# Both are idempotent and run on every `make migrate-up`. The roles step runs
# the SAME file Postgres runs on a fresh volume (init/03-analytics.sh), so an
# existing database and a new one cannot be provisioned differently.
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INFRA="$ROOT/infra"
COMPOSE="docker compose --env-file $ROOT/.env -f $INFRA/docker-compose.yml"

# Parsed, never sourced — see seed.sh.
env_value() {
  grep -E "^$1=" "$ROOT/.env" | tail -1 | cut -d= -f2- | sed -e 's/^"//' -e 's/"$//'
}

case "${1:-}" in
  roles)
    # The container's own environment carries the ANALYTICS_* variables
    # (docker-compose.yml), exactly as on first boot.
    $COMPOSE exec -T postgres bash /docker-entrypoint-initdb.d/03-analytics.sh
    ;;
  cdc)
    wal_level="$($COMPOSE exec -T postgres psql -qtAX \
      --username "$(env_value POSTGRES_USER)" --dbname "$(env_value POSTGRES_DB)" \
      -c 'SHOW wal_level')"
    if [ "$wal_level" != "logical" ]; then
      echo "  postgres is running with wal_level=$wal_level, not logical." >&2
      echo "  It was started before docker-compose.yml asked for logical replication." >&2
      echo "  Recreate it (data is kept): make -C infra up" >&2
      exit 1
    fi
    $COMPOSE exec -T postgres psql -qX -v ON_ERROR_STOP=1 \
      --username "$(env_value POSTGRES_USER)" --dbname "$(env_value POSTGRES_DB)" \
      -v cdc="$(env_value EVENTS_CDC_DB_USER)" \
      -v publication="$(env_value CDC_PUBLICATION)" \
      -v slot="$(env_value CDC_SLOT_NAME)" \
      < "$INFRA/postgres/cdc-setup.sql"
    ;;
  *)
    echo "usage: $(basename "$0") {roles|cdc}" >&2
    exit 2
    ;;
esac
