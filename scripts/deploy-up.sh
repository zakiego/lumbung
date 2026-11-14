#!/bin/bash
# Build and swap our deployment (zaki-selfhost). deploy.sh runs this in place
# of its own tag, build and rollback, after scripts/pre-deploy.sh; it still
# guards the disk before and prunes images and build cache after.
#
#   1. tag lumbung-engine:latest and lumbung-dashboard:latest as :prev
#   2. build the engine, then the dashboard, one after the other: the OrbStack
#      VM has 3 GB, and the Go build and the Nitro build together don't fit
#   3. up -d --no-build --wait
#   4. on failure: print the last 50 log lines of both services, put :prev
#      back as :latest and bring that up
#
# Retagging :prev is enough when the new engine never got as far as
# migrating meta.db. When it did, the old engine refuses to serve the newer
# meta.db (PLAN 6.10: /health answers 503), so the rollback below fails its
# health check too. Recover from the pre-deploy backup, not by retagging:
#
#   docker compose stop engine
#   docker compose run --rm --no-deps engine restore --meta-only --from /data/backups/pre-deploy
#   docker compose up -d --no-build --wait
#
# (with COMPOSE_FILE set as below; see ops/README.md).
#
# Env: WAIT_TIMEOUT (seconds, default 180), passed in by deploy.sh.
set -euo pipefail

cd "$(dirname "$0")/.."
export COMPOSE_FILE="${LUMBUNG_COMPOSE_FILE:-compose.yaml:ops/compose.selfhost.yaml}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-180}"
IMAGES=(lumbung-engine lumbung-dashboard)

log() { echo "[deploy-up] $*"; }

# 1. Keep what's there now for rollback. A first deploy has nothing to keep.
had_prev=0
for image in "${IMAGES[@]}"; do
  if docker image inspect "$image:latest" >/dev/null 2>&1; then
    docker tag "$image:latest" "$image:prev"
    had_prev=1
  fi
done

# Puts :prev back as :latest for every image that has one.
restore_prev() {
  local image
  for image in "${IMAGES[@]}"; do
    if docker image inspect "$image:prev" >/dev/null 2>&1; then
      docker tag "$image:prev" "$image:latest"
    fi
  done
}

# 2. Build one at a time. A failed build changes nothing that runs, but the
# engine may already be retagged by then, so :latest goes back to :prev to
# keep the tags matching the running containers.
for service in engine dashboard; do
  log "building $service"
  if ! docker compose build "$service"; then
    log "build of $service failed, nothing was swapped"
    restore_prev
    exit 1
  fi
done

# 3. Swap. --wait blocks until both services are healthy (the dashboard waits
# for a healthy engine).
log "starting the new images"
if docker compose up -d --no-build --wait --wait-timeout "$WAIT_TIMEOUT"; then
  log "engine and dashboard are healthy"
  exit 0
fi

# 4. Roll back.
log "the new images didn't become healthy within ${WAIT_TIMEOUT}s"
docker compose logs --tail 50 engine dashboard >&2 || true
if [ "$had_prev" -eq 0 ]; then
  log "first deploy, nothing to roll back to"
  exit 1
fi
restore_prev
if docker compose up -d --no-build --wait --wait-timeout "$WAIT_TIMEOUT"; then
  log "rolled back to the previous images"
else
  log "rollback also failed. If the new engine migrated meta.db, the old one refuses it:"
  log "  docker compose stop engine"
  log "  docker compose run --rm --no-deps engine restore --meta-only --from /data/backups/pre-deploy"
  log "  docker compose up -d --no-build --wait"
  log "(run from $(pwd) with COMPOSE_FILE=$COMPOSE_FILE)"
fi
exit 1
