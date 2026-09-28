#!/bin/sh
# Build and (re)start the documentation container on the panel host.
#
#     sh deploy/docs/deploy.sh            # build and start
#     sh deploy/docs/deploy.sh --no-build # restart the existing image
#
# ## What it is
#
# A second container next to the panel, serving static HTML on a loopback port. Deliberately not part of the
# panel image: the documentation changes on its own schedule, and coupling it to the panel would mean
# redeploying the panel to fix a typo — or worse, that a documentation build failure blocks a panel fix.
#
# ## Why it builds locally instead of pulling
#
# The site is generated from this repository's Markdown, so there is nothing to pull. The build stage needs
# network access for the base images; the run stage does not.
#
# ## TLS
#
# This container speaks plain HTTP on loopback. TLS and the hostname are the reverse proxy's job — see
# README.md in this directory.
set -eu

NAME="${NAME:-nekomari-docs}"
PORT="${PORT:-25775}"
IMAGE="${IMAGE:-nekomari-docs:local}"
REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

log() { printf '  %s\n' "$*"; }

cd "$REPO_ROOT"

if [ "${1:-}" != "--no-build" ]; then
  log "building $IMAGE from $REPO_ROOT"
  if ! docker build -f deploy/docs/Dockerfile -t "$IMAGE" . ; then
    # Named as the failure it is: the previous container, if any, is still running the previous site, which is
    # usually better than no site at all.
    log "BUILD FAILED — the running container (if any) was left untouched"
    exit 1
  fi
fi

# Replaced rather than restarted, so the new image is actually picked up. `restart` reuses the old container
# and therefore the old image, which looks like a successful deploy that changed nothing.
if docker ps -a --format '{{.Names}}' | grep -qx "$NAME"; then
  log "removing the existing container"
  docker rm -f "$NAME" >/dev/null
fi

log "starting $NAME on 127.0.0.1:$PORT"
docker run -d --name "$NAME" \
  --restart unless-stopped \
  -p "127.0.0.1:$PORT:80" \
  "$IMAGE" >/dev/null

# Waited for, because "docker run returned 0" only means the container was created. A port that never
# answers is the failure mode worth catching here.
i=0
while [ "$i" -lt 20 ]; do
  if curl -fsS -o /dev/null --max-time 3 "http://127.0.0.1:$PORT/"; then
    log "serving on http://127.0.0.1:$PORT/ ($(curl -fsS --max-time 3 "http://127.0.0.1:$PORT/" | wc -c) bytes)"
    log "DONE"
    exit 0
  fi
  i=$((i + 1))
  sleep 1
done

log "the container started but does not answer on 127.0.0.1:$PORT"
docker logs --tail 20 "$NAME" 2>&1 | sed 's/^/    /' || true
exit 1
