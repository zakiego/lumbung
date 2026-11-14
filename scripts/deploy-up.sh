#!/bin/bash
# Build and swap our deployment (zaki-selfhost). deploy.sh runs this in place
# of its own tag, build and rollback, after scripts/pre-deploy.sh (the cheap
# checks); it still guards the disk before and prunes images and build cache
# after.
#
#   1. tag the engine and dashboard :latest images as :prev
#   2. build the engine, then the dashboard, one after the other: the OrbStack
#      VM has 3 GB, and the Go build and the Nitro build together don't fit
#   3. back up right before the swap, so the copies miss nothing written
#      during the build:
#      - meta.db with the old engine (`lumbungd backup --meta-only`) to
#        /data/backups/pre-deploy in its volume. A new engine may migrate
#        meta.db, and the old one then refuses to serve it (PLAN 6.10), so
#        this copy is the rollback point. Its manifest's meta_version must be
#        one the old engine knows; a copy it couldn't restore is removed and
#        the deploy stops (LUMBUNG_ALLOW_NO_ROLLBACK=1 goes on without one).
#        The newest 5 good copies are kept.
#      - dashboard.db with VACUUM INTO (ops/dashboard-backup.mjs) to
#        /data/backups/pre-deploy-<UTC time>.db in its volume, keeping 5
#   4. up -d --no-build --wait
#   5. on failure: print the last 50 log lines of both services, put :prev
#      back as :latest and bring that up
#
# Every step before 4 changes nothing that runs: a failure there puts the
# tags back and stops.
#
# Env: WAIT_TIMEOUT (seconds, default 180), passed in by deploy.sh.
set -euo pipefail

cd "$(dirname "$0")/.."
export COMPOSE_FILE="${LUMBUNG_COMPOSE_FILE:-compose.yaml:ops/compose.selfhost.yaml}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-180}"
ALLOW_NO_ROLLBACK="${LUMBUNG_ALLOW_NO_ROLLBACK:-0}"
KEEP=5

log() { echo "[deploy-up] $*"; }
# shellcheck source=ops/lib.sh
. ops/lib.sh

load_compose
IMAGES=("$ENGINE_IMAGE" "$DASHBOARD_IMAGE")

# 1. Keep what's there now for rollback. A first deploy has nothing to keep.
had_prev=0
for image in "${IMAGES[@]}"; do
  if image_exists "$image"; then
    docker tag "$image" "${image%:*}:prev"
    had_prev=1
  fi
done

# Puts :prev back as :latest for every image that has one.
restore_prev() {
  local image
  for image in "${IMAGES[@]}"; do
    if image_exists "${image%:*}:prev"; then
      docker tag "${image%:*}:prev" "$image"
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

# 3. Back up with the old images, which are :prev now.
META_BACKUP=""
backup_meta() {
  local json known version
  known=$(known_meta_version "$ENGINE_PREV") || return 1
  log "backing up meta.db to $META_BACKUPS ($ENGINE_PREV knows $known migrations)"
  # `lumbungd backup` copies with SQLite's online backup API and never takes
  # the data folder's lock, so it runs next to a serving engine or a
  # crash-looping one. No --keep here: retention runs only once the copy is
  # known to be good.
  json=$(docker run --rm --network none -v "$DATA_VOLUME:/data" "$ENGINE_PREV" \
    backup --meta-only --to "$META_BACKUPS" </dev/null) || return 1
  META_BACKUP=$(printf '%s\n' "$json" | json_field backup) || return 1
  version=$(manifest_meta_version "$META_BACKUP") || {
    log "couldn't read meta_version from $META_BACKUP/manifest.json"
    return 1
  }
  if [ "$version" -gt "$known" ]; then
    log "$META_BACKUP has $version migrations, but $ENGINE_PREV knows only $known: it can't restore it, removing it"
    remove_backup "$META_BACKUP" || log "WARNING: couldn't remove $META_BACKUP"
    META_BACKUP=""
    if [ "$ALLOW_NO_ROLLBACK" != 1 ]; then
      log "no rollback point for meta.db (LUMBUNG_ALLOW_NO_ROLLBACK=1 deploys without one)"
      return 1
    fi
    log "LUMBUNG_ALLOW_NO_ROLLBACK=1: going on without a meta.db rollback point"
    return 0
  fi
  log "meta.db backup: $META_BACKUP ($version migrations)"
  prune_meta_backups "$KEEP"
}

backup_dashboard() {
  local image="$DASHBOARD_PREV" rc=0
  image_exists "$image" || image="$DASHBOARD_IMAGE"
  log "backing up dashboard.db to /data/backups/pre-deploy-<time>.db"
  docker run --rm -i --network none -v "$DASHBOARD_VOLUME:/data" --entrypoint node "$image" \
    - /data/backups pre-deploy "$KEEP" <ops/dashboard-backup.mjs || rc=$?
  # 2 means there's no dashboard.db yet: nothing to keep.
  [ "$rc" -eq 2 ] && { log "no dashboard.db yet, no dashboard backup needed"; return 0; }
  return "$rc"
}

if volume_exists "$DATA_VOLUME"; then
  rc=0
  file_in_volume "$DATA_VOLUME" meta.db || rc=$?
  case "$rc" in
    0)
      if ! image_exists "$ENGINE_PREV"; then
        log "no $ENGINE_PREV image to back up meta.db with, nothing was swapped"
        restore_prev
        exit 1
      fi
      backup_meta || { log "meta.db backup FAILED, nothing was swapped"; restore_prev; exit 1; }
      ;;
    1) log "$DATA_VOLUME has no meta.db yet, no engine backup needed" ;;
    *) log "couldn't check for meta.db in $DATA_VOLUME, nothing was swapped"; restore_prev; exit 1 ;;
  esac
else
  log "no $DATA_VOLUME volume (first deploy), no engine backup needed"
fi
if volume_exists "$DASHBOARD_VOLUME"; then
  backup_dashboard || { log "dashboard.db backup FAILED, nothing was swapped"; restore_prev; exit 1; }
else
  log "no $DASHBOARD_VOLUME volume (first deploy), no dashboard backup needed"
fi

# 4. Swap. --wait blocks until both services are healthy (the dashboard waits
# for a healthy engine).
log "starting the new images"
if docker compose up -d --no-build --wait --wait-timeout "$WAIT_TIMEOUT"; then
  log "engine and dashboard are healthy"
  exit 0
fi

# 5. Roll back.
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
  log "  ./ops/rollback-meta.sh${META_BACKUP:+ --from $META_BACKUP}"
  log "  docker compose up -d --no-build --wait"
  log "(run from $(pwd) with COMPOSE_FILE=$COMPOSE_FILE)"
fi
exit 1
