# Shared by dev-setup-ubuntu.sh, start-ubuntu.sh and start-mobile-ubuntu.sh.
# Sourced, not executed.
#
# The Ubuntu scripts are thin: they check what is different on Ubuntu, then run
# the SAME dev-setup.sh / start.sh / start-mobile.sh a Mac runs, with:
#   - infra/scripts/ubuntu/bin first on PATH (an `lsof` built on `ss`), and
#   - IP set to this machine's LAN address, which the shared scripts honour in
#     place of their macOS-only detection (route -n get / ipconfig).
# So a fix to the shared scripts lands on both platforms at once.

UBUNTU_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

u_bold() { printf "\n\033[1m%s\033[0m\n" "$1"; }
u_ok()   { printf "  \033[32m✓\033[0m %s\n" "$1"; }
u_warn() { printf "  \033[33m!\033[0m %s\n" "$1"; }
# stdout, like the fix lines printed after it, so the two never interleave.
u_fail() { printf "  \033[31m✗\033[0m %s\n" "$1"; }

# Linux only. Other Debian-family distros generally work; say so rather than refuse.
ubuntu_check_os() {
    if [ "$(uname -s)" != "Linux" ]; then
        u_fail "this is the Ubuntu script — on a Mac use ${1:-the script without -ubuntu}"
        exit 1
    fi
    local id="" version="" like=""
    if [ -r /etc/os-release ]; then
        id="$(. /etc/os-release; echo "${ID:-}")"
        version="$(. /etc/os-release; echo "${VERSION_ID:-}")"
        like="$(. /etc/os-release; echo "${ID_LIKE:-}")"
    fi
    if [ "$id" = "ubuntu" ]; then
        u_ok "Ubuntu $version"
    elif printf '%s' "$like" | grep -qw debian; then
        u_warn "$id $version (Debian family) — not Ubuntu, but should work"
    else
        u_warn "${id:-unknown Linux} — written for Ubuntu; package commands below are apt"
    fi
}

# This machine's LAN address: the source address of the default route.
ubuntu_lan_ip() {
    if [ -n "${IP:-}" ]; then echo "$IP"; return; fi
    local ip
    ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") {print $(i + 1); exit}}')"
    [ -z "$ip" ] && ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
    echo "$ip"
}

# Docker installed, the daemon up, and THIS user allowed to use it. On Linux
# "permission denied" is the common failure, and it is not "Docker is down".
ubuntu_check_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        u_fail "Docker Engine is not installed. Install it (official repo, includes Compose):"
        echo   "      https://docs.docker.com/engine/install/ubuntu/"
        echo   "      then: sudo usermod -aG docker \$USER   and log out and back in"
        return 1
    fi
    local out
    if out="$(docker info 2>&1)"; then
        u_ok "Docker daemon reachable as $(id -un)"
    elif printf '%s' "$out" | grep -qi "permission denied"; then
        u_fail "Docker is running but $(id -un) may not use it. Fix once:"
        echo   "      sudo usermod -aG docker \$USER   then log out and back in (or: newgrp docker)"
        return 1
    else
        u_fail "the Docker daemon is not running:  sudo systemctl enable --now docker"
        return 1
    fi
    if docker compose version >/dev/null 2>&1; then
        u_ok "docker compose $(docker compose version --short 2>/dev/null)"
    else
        u_fail "Docker Compose v2 plugin missing:  sudo apt install docker-compose-plugin"
        return 1
    fi
}

# Ten dev servers watch the source tree for changes. Ubuntu's default inotify
# limits are low enough that Vite/air then die with "ENOSPC: System limit for
# number of file watchers reached" or "too many open files".
ubuntu_check_inotify() {
    local watches instances
    watches="$(cat /proc/sys/fs/inotify/max_user_watches 2>/dev/null || echo 0)"
    instances="$(cat /proc/sys/fs/inotify/max_user_instances 2>/dev/null || echo 0)"
    if [ "$watches" -ge 524288 ] && [ "$instances" -ge 512 ]; then
        u_ok "file-watch limits OK (watches $watches, instances $instances)"
        return 0
    fi
    u_warn "file-watch limits are low (watches $watches, instances $instances) — the dev servers can crash."
    echo   "      Raise them once (survives reboots):"
    echo   "        printf 'fs.inotify.max_user_watches=524288\\nfs.inotify.max_user_instances=512\\n' \\"
    echo   "          | sudo tee /etc/sysctl.d/60-vayal-inotify.conf && sudo sysctl --system"
    return 1
}

# Put the Ubuntu stand-ins first on PATH and pass the LAN address through.
ubuntu_enable_compat() {
    export PATH="$UBUNTU_DIR/bin:$PATH"
    local ip
    ip="$(ubuntu_lan_ip)"
    if [ -n "$ip" ]; then
        export IP="$ip"
    fi
}

# version_ge 1.25.3 1.25 → true. Numeric, field by field (mawk-safe).
ubuntu_version_ge() {
    awk -v a="$1" -v b="$2" 'BEGIN {
        n = split(a, x, "."); m = split(b, y, ".");
        for (i = 1; i <= (n > m ? n : m); i++) {
            if ((x[i] + 0) > (y[i] + 0)) exit 0;
            if ((x[i] + 0) < (y[i] + 0)) exit 1;
        }
        exit 0 }'
}

# uv's installer puts it in ~/.local/bin, which a fresh Ubuntu login shell
# often does not have on PATH until the next login.
ubuntu_find_uv() {
    if ! command -v uv >/dev/null 2>&1 && [ -x "$HOME/.local/bin/uv" ]; then
        export PATH="$HOME/.local/bin:$PATH"
        u_warn "using uv from ~/.local/bin — add it to PATH for good: echo 'export PATH=\$HOME/.local/bin:\$PATH' >> ~/.bashrc"
    fi
}
