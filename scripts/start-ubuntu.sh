#!/usr/bin/env bash
#
# Start everything on an UBUNTU machine: Postgres/MinIO/MailHog, the gateway,
# the APIs, the analytics pipeline and the web UIs.
#
# The Mac equivalent is ./scripts/start.sh. This checks what is different on
# Ubuntu, then runs that same script with Ubuntu's stand-ins on PATH, so the
# two cannot drift apart. Every option is passed through:
#
#   ./scripts/start-ubuntu.sh            start the stack
#   ./scripts/start-ubuntu.sh --reset    wipe the database and reseed first
#   ./scripts/start-ubuntu.sh --tunnel   also publish the gateway for Razorpay webhooks
#   ./scripts/start-ubuntu.sh --stop     stop everything
#
# First time on this machine? Run ./scripts/dev-setup-ubuntu.sh.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=../infra/scripts/ubuntu/common.sh
. "$ROOT/infra/scripts/ubuntu/common.sh"

stopping=0 tunnel=0
for arg in "$@"; do
    case "$arg" in
        -h|--help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        --stop)    stopping=1 ;;
        --tunnel)  tunnel=1 ;;
    esac
done

ubuntu_enable_compat
ubuntu_find_uv

# Stopping needs none of the checks below — and must work even when Docker
# itself is what has gone wrong.
if [ "$stopping" -eq 0 ]; then
    u_bold "Ubuntu checks"
    ubuntu_check_os "./scripts/start.sh"
    ubuntu_check_docker || { u_fail "fix Docker access above, then re-run"; exit 1; }
    # A warning: the stack may well start, but a dev server that later dies
    # with ENOSPC is much harder to trace back to this.
    ubuntu_check_inotify || u_warn "continuing — raise the limits if a dev server crashes with ENOSPC"
    [ -f .env ] || { u_fail ".env is missing — run ./scripts/dev-setup-ubuntu.sh first"; exit 1; }

    if [ "$tunnel" -eq 1 ] && ! command -v cloudflared >/dev/null 2>&1 && ! command -v ngrok >/dev/null 2>&1; then
        arch="$(dpkg --print-architecture 2>/dev/null || echo amd64)"
        u_fail "--tunnel needs cloudflared (or ngrok). Install cloudflared:"
        echo   "      curl -fsSL -o /tmp/cloudflared.deb https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${arch}.deb"
        echo   "      sudo dpkg -i /tmp/cloudflared.deb"
        exit 1
    fi
fi

exec "$ROOT/scripts/start.sh" "$@"
