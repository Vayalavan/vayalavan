#!/usr/bin/env bash
#
# Runs the application processes in ONE terminal with prefixed, interleaved,
# colour-coded logs, via `concurrently`.
#
#   run.sh all      gateway + 3 Go APIs + analytics pipeline + 4 UIs  (10 processes)
#   run.sh api      gateway + 3 Go APIs + analytics pipeline  (6 processes)
#   run.sh client   gateway + catalog + orders + client UI  (4 processes)
#
# Everything hot-reloads: air for Go, tsx watch for the gateway, vite for the
# UIs. Ctrl-C stops the whole group.
#
# concurrently rather than overmind/foreman: it installs from the workspace's
# own package.json, so `make dev` needs no brew install and no Ruby.
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TOOLS_BIN="$ROOT/.tools/bin"

# Locally-installed air/goose take precedence over anything global, so every
# developer runs the same versions.
export PATH="$TOOLS_BIN:$PATH"

PROFILE="${1:-all}"

case "$PROFILE" in
  all)    SELECTED=(gateway profile catalog orders events analytics client-ui admin-ui analytics-ui supplier-ui) ;;
  api)    SELECTED=(gateway profile catalog orders events analytics) ;;
  client) SELECTED=(gateway catalog orders client-ui) ;;
  *)
    echo "usage: $(basename "$0") {all|api|client}" >&2
    exit 2
    ;;
esac

# name -> directory, command and log colour.
dir_for() {
  case "$1" in
    gateway)     echo "vm-gateway-api" ;;
    profile)     echo "vm-profile-api" ;;
    catalog)     echo "vm-catalog-api" ;;
    orders)      echo "vm-orders-api" ;;
    events)      echo "vm-events-consumer-api" ;;
    analytics)   echo "vm-analytics-api" ;;
    client-ui)   echo "vm-client-ui" ;;
    admin-ui)    echo "vm-admin-ui" ;;
    analytics-ui) echo "vm-analytics-ui" ;;
    supplier-ui) echo "vm-supplier-ui" ;;
  esac
}

cmd_for() {
  case "$1" in
    # air reads .air.toml in the service directory.
    profile|catalog|orders|events) echo "air -c .air.toml" ;;
    # The one Python service; uv provides its pinned interpreter and reload.
    analytics) echo "uv run python src/main.py" ;;
    # tsx watch for the gateway, vite for the UIs — both via npm run dev.
    *) echo "npm run dev" ;;
  esac
}

color_for() {
  case "$1" in
    gateway)     echo "magenta" ;;
    profile)     echo "green" ;;
    catalog)     echo "yellow" ;;
    orders)      echo "blue" ;;
    events)      echo "green.dim" ;;
    analytics)   echo "yellow.dim" ;;
    client-ui)   echo "cyan" ;;
    admin-ui)    echo "red" ;;
    analytics-ui) echo "white" ;;
    supplier-ui) echo "gray" ;;
  esac
}

if ! command -v air >/dev/null 2>&1; then
  case "$PROFILE" in
    all|api|client)
      echo "air is not installed — Go services cannot hot-reload." >&2
      echo "Run 'make deps' (installs it into .tools/bin), or 'make doctor'." >&2
      exit 1
      ;;
  esac
fi

names=""
colors=""
commands=()

for name in "${SELECTED[@]}"; do
  names="${names:+$names,}$name"
  colors="${colors:+$colors,}$(color_for "$name")"
  commands+=("cd '$ROOT/$(dir_for "$name")' && $(cmd_for "$name")")
done

echo "==> starting ${#SELECTED[@]} processes (profile: $PROFILE) — Ctrl-C stops all"
echo

exec npx --yes concurrently \
  --names "$names" \
  --prefix-colors "$colors" \
  --prefix "[{name}]" \
  --timestamp-format "HH:mm:ss" \
  --handle-input \
  "${commands[@]}"
