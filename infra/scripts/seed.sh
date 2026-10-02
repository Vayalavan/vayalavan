#!/usr/bin/env bash
#
# Loads development seed data by piping infra/seed/*.sql into the Postgres
# container, in filename order.
#
# Runs as the superuser because seed data legitimately spans schemas (an admin
# user in `profile`, sample produce in `catalog`). The per-service roles
# deliberately cannot do that — see infra/postgres/init/01-init.sh.
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INFRA="$ROOT/infra"
COMPOSE="docker compose --env-file $ROOT/.env -f $INFRA/docker-compose.yml"
SEED_DIR="$INFRA/seed"

# Read only the two values needed, rather than sourcing the whole file.
# Sourcing would execute it: a value containing '&', '<' or a space is a
# shell operator, and .env legitimately contains connection strings and a
# mail From header. Parse, never execute.
env_value() {
  grep -E "^$1=" "$ROOT/.env" | tail -1 | cut -d= -f2- | sed -e 's/^"//' -e 's/"$//'
}

POSTGRES_USER="$(env_value POSTGRES_USER)"
POSTGRES_DB="$(env_value POSTGRES_DB)"

if [ -z "$POSTGRES_USER" ] || [ -z "$POSTGRES_DB" ]; then
  echo "seed: POSTGRES_USER or POSTGRES_DB missing from .env — run 'make doctor'" >&2
  exit 1
fi

shopt -s nullglob
files=("$SEED_DIR"/*.sql)
shopt -u nullglob

if [ ${#files[@]} -eq 0 ]; then
  echo "no seed files in $SEED_DIR — nothing to do"
  exit 0
fi

# Seed credentials come from the environment, never from the .sql files
# (CLAUDE.md rule 4: no secrets in the repo, not even in seed data).
ADMIN_EMAIL="$(env_value SEED_ADMIN_EMAIL)"
ADMIN_PHONE="$(env_value SEED_ADMIN_PHONE)"
ADMIN_PASSWORD="$(env_value SEED_ADMIN_PASSWORD)"
SUPPLIER_PASSWORD="$(env_value SEED_SUPPLIER_PASSWORD)"
CUSTOMER_PASSWORD="$(env_value SEED_CUSTOMER_PASSWORD)"
ANALYST_EMAIL="$(env_value SEED_ANALYST_EMAIL)"
ANALYST_PHONE="$(env_value SEED_ANALYST_PHONE)"
ANALYST_PASSWORD="$(env_value SEED_ANALYST_PASSWORD)"

for required in ADMIN_EMAIL ADMIN_PHONE ADMIN_PASSWORD SUPPLIER_PASSWORD CUSTOMER_PASSWORD \
                ANALYST_EMAIL ANALYST_PHONE ANALYST_PASSWORD; do
  if [ -z "${!required}" ]; then
    echo "seed: SEED_${required} is not set in .env — run 'make doctor'" >&2
    exit 1
  fi
done

for file in "${files[@]}"; do
  echo "==> $(basename "$file")"
  # Values are passed as psql variables rather than interpolated into the SQL
  # text, so a password containing a quote cannot break out of the statement.
  $COMPOSE exec -T postgres \
    psql -v ON_ERROR_STOP=1 \
      --username "$POSTGRES_USER" \
      --dbname "$POSTGRES_DB" \
      -v admin_email="$ADMIN_EMAIL" \
      -v admin_phone="$ADMIN_PHONE" \
      -v admin_password="$ADMIN_PASSWORD" \
      -v supplier_password="$SUPPLIER_PASSWORD" \
      -v customer_password="$CUSTOMER_PASSWORD" \
      -v analyst_email="$ANALYST_EMAIL" \
      -v analyst_phone="$ANALYST_PHONE" \
      -v analyst_password="$ANALYST_PASSWORD" \
    < "$file"
done

echo "seed complete"
