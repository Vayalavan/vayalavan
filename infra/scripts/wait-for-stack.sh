#!/usr/bin/env bash
#
# Blocks until the backing containers are ready to accept work.
#
# `docker compose up -d` returns as soon as containers are created, not when
# Postgres can serve a query. Running migrations immediately after would fail
# intermittently — the classic flaky first-run — so this gates on the actual
# healthchecks instead of a fixed sleep.
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INFRA="$ROOT/infra"
COMPOSE="docker compose --env-file $ROOT/.env -f $INFRA/docker-compose.yml"

TIMEOUT_SECONDS="${WAIT_TIMEOUT_SECONDS:-120}"
deadline=$((SECONDS + TIMEOUT_SECONDS))

container_health() {
  local id
  id="$($COMPOSE ps -q "$1" 2>/dev/null || true)"
  if [ -z "$id" ]; then
    echo "missing"
    return
  fi
  # A container with no healthcheck reports empty; treat "running" as ready.
  docker inspect --format \
    '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' \
    "$id" 2>/dev/null || echo "missing"
}

is_ready() {
  case "$(container_health "$1")" in
    healthy|running) return 0 ;;
    *) return 1 ;;
  esac
}

echo "==> waiting for postgres and minio"

while [ $SECONDS -lt $deadline ]; do
  if is_ready postgres && is_ready minio; then
    echo "    postgres: $(container_health postgres)"
    echo "    minio:    $(container_health minio)"
    echo "stack ready"
    exit 0
  fi
  sleep 2
done

echo "timed out after ${TIMEOUT_SECONDS}s waiting for the stack" >&2
echo "  postgres: $(container_health postgres)" >&2
echo "  minio:    $(container_health minio)" >&2
echo "run 'make logs' to investigate" >&2
exit 1
