#!/usr/bin/env bash
#
# One-time (and safely repeatable) setup of an UBUNTU development machine.
# The Mac equivalent is ./scripts/dev-setup.sh — this checks what is different
# on Ubuntu, then runs that same script, so both platforms set up identically.
#
#   ./scripts/dev-setup-ubuntu.sh                 set up
#   ./scripts/dev-setup-ubuntu.sh --with-mobile   also install the Expo apps
#   ./scripts/dev-setup-ubuntu.sh --reset         DESTROY the local database first
#   IP=192.168.1.40 ./scripts/dev-setup-ubuntu.sh use this LAN address
#
# Then:  ./scripts/start-ubuntu.sh
#
# ---------------------------------------------------------------------------
# PREREQUISITES (Ubuntu 22.04 / 24.04). The script checks each one and prints
# the exact command for anything missing. In one go, on a fresh machine:
#
#   # basics
#   sudo apt update && sudo apt install -y make curl git iproute2 procps ca-certificates
#
#   # Go 1.25+ — Ubuntu's own `golang` package is too old
#   sudo snap install go --classic
#
#   # Node.js 20.12+ (22 LTS) — Ubuntu's own `nodejs` package is too old
#   curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
#   sudo apt install -y nodejs
#
#   # Docker Engine + Compose plugin (official repo), and run it without sudo
#   #   https://docs.docker.com/engine/install/ubuntu/
#   sudo usermod -aG docker $USER      # then LOG OUT and back in
#
#   # uv — runs vm-analytics-api and downloads its own Python 3.12
#   curl -LsSf https://astral.sh/uv/install.sh | sh
#
#   # file-watch limits — ten dev servers watch the source tree
#   printf 'fs.inotify.max_user_watches=524288\nfs.inotify.max_user_instances=512\n' \
#     | sudo tee /etc/sysctl.d/60-vayal-inotify.conf && sudo sysctl --system
#
# Plus test.env at the repository root (it is committed), and these ports free:
#   8080-8085, 5173-5176, 5432, 9000, 9001, 1025, 8025
# A system Postgres (`sudo systemctl stop postgresql`) is the usual clash.
# ---------------------------------------------------------------------------
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=../infra/scripts/ubuntu/common.sh
. "$ROOT/infra/scripts/ubuntu/common.sh"

for arg in "$@"; do
    case "$arg" in -h|--help) sed -n '2,45p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;; esac
done

u_bold "Ubuntu checks"
ubuntu_check_os "./scripts/dev-setup.sh"
missing=0

# Basic tools, mapped to the apt package that provides each.
packages=""
for pair in make:make curl:curl git:git ss:iproute2 ps:procps; do
    tool="${pair%%:*}" pkg="${pair##*:}"
    command -v "$tool" >/dev/null 2>&1 || packages="$packages $pkg"
done
if [ -z "$packages" ]; then
    u_ok "make, curl, git, ss, ps"
else
    u_fail "missing basic tools. Install:  sudo apt update && sudo apt install -y$packages"
    missing=1
fi

if command -v go >/dev/null 2>&1 && ubuntu_version_ge "$(go env GOVERSION | sed 's/^go//')" 1.25; then
    u_ok "go $(go env GOVERSION | sed 's/^go//')"
else
    u_fail "Go 1.25+ needed (found: $(go env GOVERSION 2>/dev/null || echo none)). Ubuntu's own package is too old. Install:  sudo snap install go --classic"
    missing=1
fi

if command -v node >/dev/null 2>&1 && ubuntu_version_ge "$(node --version | sed 's/^v//')" 20.12; then
    u_ok "node $(node --version | sed 's/^v//')"
else
    u_fail "Node.js 20.12+ needed (found: $(node --version 2>/dev/null || echo none)). Ubuntu's own package is too old. Install:"
    echo   "      curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash - && sudo apt install -y nodejs"
    missing=1
fi

ubuntu_find_uv
if command -v uv >/dev/null 2>&1; then
    u_ok "uv $(uv --version | awk '{print $2}')"
else
    u_fail "uv not installed. Install:  curl -LsSf https://astral.sh/uv/install.sh | sh   (then open a new terminal)"
    missing=1
fi

ubuntu_check_docker || missing=1
# Not fatal for setup itself — it is the dev servers in start-ubuntu.sh that
# hit the limit — but far easier to fix now than to diagnose from a crash.
ubuntu_check_inotify || true

if [ "$missing" -ne 0 ]; then
    u_fail "install the prerequisites above, then run ./scripts/dev-setup-ubuntu.sh again"
    exit 1
fi

ubuntu_enable_compat
if [ -n "${IP:-}" ]; then
    u_ok "LAN address $IP (used for images and the mobile apps)"
else
    u_warn "could not detect a LAN address — .env will use localhost (web apps fine; mobile apps need IP=…)"
fi

# The shared setup, with Ubuntu's stand-ins on PATH and IP passed through,
# and the Ubuntu commands named in its closing summary.
export VAYAL_START_CMD="./scripts/start-ubuntu.sh"
export VAYAL_SETUP_CMD="./scripts/dev-setup-ubuntu.sh"
export VAYAL_MOBILE_CMD="./scripts/start-mobile-ubuntu.sh"
exec "$ROOT/scripts/dev-setup.sh" "$@"
