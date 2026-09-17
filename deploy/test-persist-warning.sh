#!/usr/bin/env bash
# Verify the data-persistence warning fires in the case that matters and stays
# quiet in the case that is fine.
#
# Expects an image built from the working tree (the warning does not exist in any
# published image yet).
set -uo pipefail
IMG="${1:?usage: test-persist-warning.sh <image>}"

probe() {
  local label="$1"; shift
  docker rm -f pw-test >/dev/null 2>&1
  docker run -d --name pw-test -p 127.0.0.1:25920:25774 "$@" "$IMG" >/dev/null
  sleep 10
  echo "  --- ${label} ---"
  docker logs pw-test 2>&1 | grep -iE "data directory|anonymous|not a mount point" \
    | tail -3 | sed 's/^/    /' || echo "    (no persistence line logged)"
  docker rm -f pw-test >/dev/null 2>&1
}

echo "=== A. anonymous volume (the footgun) — expect a warning ==="
probe "no -v"

echo
echo "=== B. named volume — expect the ok line ==="
docker volume rm pw-named >/dev/null 2>&1
probe "named volume" -v pw-named:/app/data
docker volume rm pw-named >/dev/null 2>&1

echo
echo "=== C. bind mount — expect the ok line ==="
rm -rf /tmp/pw-bind && mkdir -p /tmp/pw-bind
probe "bind mount" -v /tmp/pw-bind:/app/data
rm -rf /tmp/pw-bind
