#!/usr/bin/env bash
#
# Start one of the two mobile apps' Expo dev servers on an UBUNTU machine.
#
# The Mac equivalent is ./scripts/start-mobile.sh. This checks what is
# different on Ubuntu, then runs that same script with this machine's LAN
# address passed in (the shared script detects it with macOS-only commands).
# Every option is passed through:
#
#   ./scripts/start-mobile-ubuntu.sh               the SUPPLIER app (default)
#   ./scripts/start-mobile-ubuntu.sh customer      the CUSTOMER app
#   ./scripts/start-mobile-ubuntu.sh --android     also open an Android emulator (needs the SDK)
#   ./scripts/start-mobile-ubuntu.sh --tunnel      route Metro through a public tunnel
#   ./scripts/start-mobile-ubuntu.sh --stop        free both Metro ports
#
# Run ./scripts/start-ubuntu.sh in another terminal first: the app needs the
# backend. On a phone, scan the QR code with Expo Go — iPhones included; only
# the iOS SIMULATOR needs a Mac.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=../infra/scripts/ubuntu/common.sh
. "$ROOT/infra/scripts/ubuntu/common.sh"

stopping=0 android=0
for arg in "$@"; do
    case "$arg" in
        -h|--help) sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        --ios)
            u_fail "the iOS simulator needs macOS and Xcode. On Ubuntu, run without --ios and"
            echo   "      scan the QR code with Expo Go on the iPhone instead."
            exit 1 ;;
        --stop)    stopping=1 ;;
        --android) android=1 ;;
    esac
done

ubuntu_enable_compat

if [ "$stopping" -eq 0 ]; then
    u_bold "Ubuntu checks"
    ubuntu_check_os "./scripts/start-mobile.sh"
    ubuntu_check_inotify || u_warn "continuing — raise the limits if Metro crashes with ENOSPC"

    if [ -n "${IP:-}" ]; then
        u_ok "LAN address $IP — the app will call the API there"
    else
        u_fail "could not detect this machine's LAN address. Pass it:  IP=192.168.1.40 $0 $*"
        exit 1
    fi

    # Ubuntu's firewall, when on, blocks a phone from reaching this machine
    # even though everything works in a browser here.
    if command -v ufw >/dev/null 2>&1 && systemctl is-active --quiet ufw 2>/dev/null; then
        u_warn "the ufw firewall is active. If the phone cannot connect, allow the app ports once:"
        echo   "      sudo ufw allow 8080,9000,8090,8091/tcp"
    fi

    if [ "$android" -eq 1 ] && ! command -v adb >/dev/null 2>&1; then
        u_warn "--android needs the Android SDK (adb) — install Android Studio, or use Expo Go on a phone"
    fi
fi

exec "$ROOT/scripts/start-mobile.sh" "$@"
