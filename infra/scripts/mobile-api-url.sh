#!/usr/bin/env bash
#
# Points MOBILE_API_BASE_URL and S3_ENDPOINT at this machine's current LAN
# address.
#
# The mobile app's API URL is baked into the build, and it has to be an address
# the HANDSET can reach — so it changes every time the laptop moves between a
# home network, an office network, or a phone's hotspot. Editing it by hand is
# how you end up on port 80 with a thirty-second timeout and no clue why.
#
# S3_ENDPOINT moves with it, and must. Product image URLs are presigned against
# that exact host (the signature covers the Host header, so nothing may rewrite
# it afterwards — see vm-catalog-api/internal/storage). Leave it on
# "localhost:9000" and every image URL the API hands out points the HANDSET at
# ITSELF: the customer app shows the placeholder on every card, and the supplier
# app's presigned PUT never lands, so a product saves with no photo at all. The
# gateway being on the LAN and storage being on localhost is the specific
# mismatch this keeps from happening.
#
# Rewrites exactly those two lines of the repository's .env, leaving comments,
# ordering and every other variable untouched.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_FILE="$ROOT/.env"

if [ ! -f "$ENV_FILE" ]; then
    echo "error: $ENV_FILE does not exist. Run 'make -C infra env' first." >&2
    exit 1
fi

# An explicit address wins, so a hotspot or a VPN address can be forced:
#   make -C infra mobile-ip IP=172.20.10.2
ip="${IP:-}"

if [ -z "$ip" ]; then
    # Whichever interface actually carries the default route — en0 is Wi-Fi on
    # most Macs but not all, and a hotspot or dock can land elsewhere.
    iface="$(route -n get default 2>/dev/null | awk '/interface: /{print $2; exit}')"
    if [ -n "$iface" ]; then
        ip="$(ipconfig getifaddr "$iface" 2>/dev/null || true)"
    fi
    # Fall back to any non-loopback IPv4.
    if [ -z "$ip" ]; then
        ip="$(ifconfig 2>/dev/null | awk '/inet /{if ($2 != "127.0.0.1") {print $2; exit}}')"
    fi
fi

if [ -z "$ip" ]; then
    echo "error: could not work out this machine's LAN address." >&2
    echo "       Pass one explicitly: make -C infra mobile-ip IP=192.168.1.37" >&2
    exit 1
fi

port="$(sed -n 's/^GATEWAY_PORT=//p' "$ENV_FILE" | head -1)"
port="${port:-8080}"

minio_port="$(sed -n 's/^MINIO_API_PORT=//p' "$ENV_FILE" | head -1)"
minio_port="${minio_port:-9000}"

# Sets one variable in .env to one value, in place, via a temp file so a
# failure cannot truncate the file. Prints what changed. The replacement is
# built with awk rather than sed so the URL's slashes need no escaping.
set_var() {
    local key="$1" value="$2" old tmp
    old="$(sed -n "s/^${key}=//p" "$ENV_FILE" | head -1)"

    if [ "$old" = "$value" ]; then
        echo "  $key already $value"
        return 0
    fi

    tmp="$(mktemp)"
    if grep -q "^${key}=" "$ENV_FILE"; then
        awk -v key="$key" -v value="$value" \
            'index($0, key "=") == 1 {print key "=" value; next} {print}' \
            "$ENV_FILE" >"$tmp"
    else
        cp "$ENV_FILE" "$tmp"
        printf '\n%s=%s\n' "$key" "$value" >>"$tmp"
    fi

    cat "$tmp" >"$ENV_FILE"
    rm -f "$tmp"

    echo "  $key"
    echo "    was: ${old:-<unset>}"
    echo "    now: $value"
}

set_var MOBILE_API_BASE_URL "http://${ip}:${port}/api"
# Same host, so a presigned image URL is reachable from the handset as well as
# from this machine. See the header — this is not optional on a LAN address.
set_var S3_ENDPOINT "http://${ip}:${minio_port}"

echo
echo "  Restart Expo AND vm-catalog-api — the app URL is read at build time and"
echo "  the storage endpoint at service startup, so neither picks this up while"
echo "  running: make -C infra dev, then make -C infra mobile"
