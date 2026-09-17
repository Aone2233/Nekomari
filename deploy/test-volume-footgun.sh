#!/usr/bin/env bash
# Does a container recreated WITHOUT an explicit volume lose its settings?
#
# This is the failure the Dockerfile's `VOLUME ["/app/data"]` invites: with no -v
# flag Docker creates an anonymous volume, and the next `docker run` of the same
# image gets a *different* anonymous volume, so the panel comes up with an empty
# data directory and shows the install wizard again. From the user's side that
# looks exactly like "the update reset my settings".
#
# Compares three cases: no volume, named volume, and the compose-style bind mount.
set -uo pipefail
PORT=25910
IMG="${1:-ghcr.io/aone2233/nekomari:v0.1.3}"

wait_ready() {
  for _ in $(seq 1 30); do
    sleep 1
    curl -fsS "http://127.0.0.1:${PORT}/api/public" >/dev/null 2>&1 && return 0
  done
  return 1
}

install_status() {
  curl -fsS "http://127.0.0.1:${PORT}/api/install/status" 2>/dev/null \
    | sed -n 's/.*"required":\(true\|false\).*/\1/p'
}

run_once() {
  local name="$1"; shift
  docker rm -f "$name" >/dev/null 2>&1
  docker run -d --name "$name" -p "127.0.0.1:${PORT}:25774" "$@" "$IMG" >/dev/null
  wait_ready || { echo "  (never became ready)"; return 1; }
}

echo "=== case A: no -v flag (anonymous volume) ==="
echo "  first run:"
run_once anon-test || true
echo "    install wizard required = $(install_status)"
# Complete the install so there is a setting to lose.
curl -fsS -X POST "http://127.0.0.1:${PORT}/api/install/complete" \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Persist-Test-2026","sitename":"KEEP-ME","description":"x","metric_dsn":"./data/metrics.db"}' \
  >/dev/null 2>&1
sleep 3
echo "    sitename now = $(curl -fsS "http://127.0.0.1:${PORT}/api/public" 2>/dev/null | sed -n 's/.*"sitename":"\([^"]*\)".*/\1/p')"

echo "  recreating the container the way an image update does (docker rm + run):"
docker rm -f anon-test >/dev/null 2>&1
run_once anon-test || true
echo "    install wizard required = $(install_status)"
echo "    sitename now = $(curl -fsS "http://127.0.0.1:${PORT}/api/public" 2>/dev/null | sed -n 's/.*"sitename":"\([^"]*\)".*/\1/p')"
docker rm -f anon-test >/dev/null 2>&1

echo
echo "=== case B: named volume ==="
docker volume rm persist-named >/dev/null 2>&1
run_once named-test -v persist-named:/app/data || true
curl -fsS -X POST "http://127.0.0.1:${PORT}/api/install/complete" \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Persist-Test-2026","sitename":"KEEP-ME","description":"x","metric_dsn":"./data/metrics.db"}' \
  >/dev/null 2>&1
sleep 3
docker rm -f named-test >/dev/null 2>&1
run_once named-test -v persist-named:/app/data || true
echo "    after recreate: wizard=$(install_status) sitename=$(curl -fsS "http://127.0.0.1:${PORT}/api/public" 2>/dev/null | sed -n 's/.*"sitename":"\([^"]*\)".*/\1/p')"
docker rm -f named-test >/dev/null 2>&1
docker volume rm persist-named >/dev/null 2>&1

echo
echo "=== leftover anonymous volumes created by case A ==="
docker volume ls --filter dangling=true --format '  {{.Name}}' | head -5
