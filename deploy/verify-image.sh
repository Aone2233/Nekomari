#!/usr/bin/env bash
# Pull a published container image and prove it actually starts.
#
# The GHCR API needs read:packages to list tags, which this token lacks -- but
# listing tags is not the question. The question is whether the binary inside the
# image can execute on the image's own base, which is exactly what broke in
# v0.1.1: a glibc-linked binary in a musl base meant every `docker run` died with
# "exec /app/nekomari: no such file or directory" while CI was green.
#
# So: pull it, run it on a throwaway port with a throwaway volume, wait for the
# install endpoint to answer, and remove everything afterwards.
#
# Usage: verify-image.sh <tag> [port]
set -uo pipefail

TAG="${1:?usage: verify-image.sh <tag> [port]}"
PORT="${2:-25888}"
IMAGE="ghcr.io/aone2233/nekomari:${TAG}"
NAME="nekomari-imgcheck-$$"
VOL="nekomari-imgcheck-vol-$$"

cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1
  docker volume rm "$VOL" >/dev/null 2>&1
}
trap cleanup EXIT

echo "== pulling ${IMAGE} =="
if ! docker pull "$IMAGE" 2>&1 | tail -2; then
  echo "  FAIL: pull failed"; exit 1
fi

echo "== starting it =="
docker run -d --name "$NAME" -p "127.0.0.1:${PORT}:25774" -v "${VOL}:/app/data" "$IMAGE" >/dev/null

echo "== waiting for the install endpoint =="
ok=0
for _ in $(seq 1 30); do
  sleep 1
  if curl -fsS "http://127.0.0.1:${PORT}/api/install/status" 2>/dev/null | grep -q '"required":true'; then
    ok=1; break
  fi
done

state=$(docker inspect -f '{{.State.Status}} exit={{.State.ExitCode}}' "$NAME" 2>/dev/null)
echo "  container: ${state}"
if [ "$ok" = 1 ]; then
  echo "  PASS  image runs and serves the install guide"
  # The binary must be the published one, not a different build.
  v=$(docker run --rm --entrypoint /app/nekomari "$IMAGE" --help 2>&1 | head -1)
  case "$v" in
    *"$TAG"*) echo "  PASS  binary reports ${TAG}";;
    *) echo "  FAIL  binary reports: ${v}"; exit 1;;
  esac
else
  echo "  FAIL  never answered; logs:"
  docker logs "$NAME" 2>&1 | tail -10 | sed 's/^/    /'
  exit 1
fi

echo "== cleanup =="
cleanup
trap - EXIT
# `grep -c` exits 1 when the count is zero, which would make a clean run report
# failure. Capture the count and let the script's own exit status mean something.
left=$(docker ps -a --format '{{.Names}}' | grep -c "$NAME" || true)
echo "  leftover containers: ${left:-0}"
[ "${left:-0}" = 0 ] || exit 1
echo "  RESULT: ${TAG} image verified"
