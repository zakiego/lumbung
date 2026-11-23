#!/bin/bash
# Roll meta.db back to a pre-deploy backup, so an older engine serves it
# again after a newer one migrated it (PLAN 6.10).
# scripts/deploy-up.sh runs it on a failed deploy, and it
# works by hand too (ops/README.md). Run from anywhere on the Docker host.
#
#   ops/rollback-meta.sh [--from /data/backups/pre-deploy/<time>-meta] [--force]
#
#   1. read meta.db's version and the migrations the engine image knows.
#      When the image can serve meta.db as it is, there's nothing to roll
#      back: exit 0 without touching anything (--force rolls back anyway).
#   2. stop the dashboard and the engine
#   3. take a full backup of every branch first (`lumbungd backup
#      --all-branches`) to /data/backups/pre-rollback, kept until removed by
#      hand. A meta-only restore forgets every database, branch and snapshot
#      created after the meta backup, and the engine deletes their files on
#      its next start; this copy is how to get them back. No full backup, no
#      restore.
#   4. pick the backup: --from, or the newest in /data/backups/pre-deploy
#      whose manifest meta_version the image knows. A newer one is refused.
#   5. `lumbungd restore --meta-only` from it with the image
#
# It leaves both services stopped. Start them with the image it rolled back
# to: docker compose up -d --no-build --wait
#
# Env:
#   LUMBUNG_ROLLBACK_IMAGE  the engine image that serves afterwards (default
#                           the compose engine image, :latest)
#   LUMBUNG_BACKUP_IMAGE    the image to take the full backup with, tried
#                           before the rollback image (deploy-up.sh passes
#                           the new engine, which knows the newer meta.db)
#   LUMBUNG_COMPOSE_FILE    compose files (default ours)
set -euo pipefail

cd "$(dirname "$0")/.."
export COMPOSE_FILE="${LUMBUNG_COMPOSE_FILE:-compose.yaml:ops/compose.selfhost.yaml}"
FULL_BACKUPS=/data/backups/pre-rollback

log() { echo "[rollback-meta] $*"; }
# shellcheck source=ops/lib.sh
. ops/lib.sh

from=""
force=0
while [ $# -gt 0 ]; do
  case "$1" in
    --from) from="${2:?--from needs a folder}"; shift 2 ;;
    --force) force=1; shift ;;
    *) echo "usage: ops/rollback-meta.sh [--from /data/backups/pre-deploy/<time>-meta] [--force]" >&2; exit 2 ;;
  esac
done
case "$from" in
  "" | /data/backups/*) ;;
  *) log "--from must be a folder in the data volume, under /data/backups"; exit 2 ;;
esac

load_compose
target="${LUMBUNG_ROLLBACK_IMAGE:-$ENGINE_IMAGE}"
backup_image="${LUMBUNG_BACKUP_IMAGE:-}"

volume_exists "$DATA_VOLUME" || { log "no $DATA_VOLUME volume"; exit 1; }
image_exists "$target" || { log "no $target image"; exit 1; }

# --- 1. is there anything to roll back? --------------------------------------
known=$(known_meta_version "$target")
current=$(meta_user_version) || { log "couldn't read meta.db's version in $DATA_VOLUME"; exit 1; }
log "meta.db has $current migrations, $target knows $known"
if [ "$current" -le "$known" ] && [ "$force" -ne 1 ]; then
  log "$target can serve this meta.db, nothing to roll back (--force rolls back anyway)"
  exit 0
fi

# --- 2. stop -------------------------------------------------------------------
log "stopping the dashboard and the engine"
docker compose stop dashboard engine

# --- 3. full backup first --------------------------------------------------
full=""
images=()
[ -z "$backup_image" ] || images+=("$backup_image")
images+=("$target")
for image in "${images[@]}"; do
  log "full backup of every branch to $FULL_BACKUPS with $image"
  if json=$(docker run --rm --network none -v "$DATA_VOLUME:/data" "$image" \
    backup --all-branches --to "$FULL_BACKUPS" </dev/null); then
    full=$(printf '%s\n' "$json" | json_field backup)
    break
  fi
  log "full backup with $image failed"
done
[ -n "$full" ] || { log "no full backup, so no meta-only restore: nothing was changed (both services are stopped)"; exit 1; }
log "full backup: $full"

# --- 4. pick ---------------------------------------------------------------
if [ -n "$from" ]; then
  version=$(manifest_meta_version "$from") || { log "couldn't read meta_version from $from/manifest.json"; exit 1; }
  if [ "$version" -gt "$known" ]; then
    log "$from has $version migrations, but $target knows only $known: it can't restore it"
    exit 1
  fi
else
  from=$(pick_meta_backup "$known") || {
    log "no backup in $META_BACKUPS that $target can restore (all newer than $known migrations, or none)"
    exit 1
  }
fi
log "restoring meta.db from $from"

# --- 5. restore ------------------------------------------------------------
docker run --rm --network none -v "$DATA_VOLUME:/data" "$target" restore --meta-only --from "$from" </dev/null
log "meta.db is back at $from. Everything created after that backup is in $full;"
log "the engine removes those files from the data folder when it starts (ops/README.md: Getting data back)"
