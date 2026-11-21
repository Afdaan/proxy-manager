#!/usr/bin/env bash

set -Eeuo pipefail

log() {
    printf '\n\033[1;32m==> %s\033[0m\n' "$*"
}

die() {
    printf '\n\033[1;31mERROR: %s\033[0m\n' "$*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

builder_container=""
cleanup() {
    if [[ -n "$builder_container" ]]; then
        docker rm "$builder_container" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

[[ "$(id -u)" -ne 0 ]] || die "run this script as a normal sudo-capable user, not root"
[[ -d .git && -f Dockerfile && -f compose.yaml ]] \
    || die "run this script from the Proxy Manager repository root"

for command in sudo git docker curl nginx systemctl readlink; do
    require_command "$command"
done
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is required"

sudo -v

if [[ "${PROXY_MANAGER_AFTER_PULL:-0}" != "1" ]]; then
    [[ -z "$(git status --porcelain)" ]] \
        || die "repository has local changes; commit, stash, or remove them before updating"

    log "Pulling the latest code"
    git pull --ff-only

    export PROXY_MANAGER_AFTER_PULL=1
    exec "$(readlink -f "$0")"
fi

log "Building the host agent"
docker build --target server-build -t proxy-manager-builder:local .
mkdir -p bin
builder_container="$(docker create proxy-manager-builder:local /bin/true)"
docker cp "$builder_container:/out/proxy-manager-agent" ./bin/proxy-manager-agent
docker rm "$builder_container" >/dev/null
builder_container=""

log "Building the control-plane server and UI"
docker compose build server

log "Installing and restarting the host agent"
sudo install -m 0755 ./bin/proxy-manager-agent /usr/local/bin/proxy-manager-agent
sudo systemctl restart proxy-manager-agent

for attempt in {1..20}; do
    if sudo test -S /run/proxy-manager/agent.sock; then
        break
    fi
    if [[ "$attempt" -eq 20 ]]; then
        sudo systemctl status proxy-manager-agent --no-pager --full || true
        sudo journalctl -u proxy-manager-agent -n 50 --no-pager || true
        die "agent socket was not created"
    fi
    sleep 0.25
done

log "Starting the updated control plane"
docker compose up -d

for attempt in {1..30}; do
    if curl -fsS http://127.0.0.1:8080/api/v1/health >/dev/null 2>&1; then
        break
    fi
    if [[ "$attempt" -eq 30 ]]; then
        docker compose logs --tail=100 server
        die "server did not become healthy"
    fi
    sleep 2
done

log "Verifying NGINX and services"
sudo nginx -t
docker compose ps

printf '\nUpdate complete. Database volume was preserved.\n'
