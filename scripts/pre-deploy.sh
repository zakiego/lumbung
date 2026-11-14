#!/bin/bash
# Run by the zaki-selfhost deploy script from the checkout, after `git pull`
# and before the build. A non-zero exit stops the deploy. Our deployment only
# (see ops/README.md).
#
#   1. If the engine's data volume holds a meta.db, back it up with
#      `lumbungd backup --meta-only` to /data/backups/pre-deploy (in the
#      volume), keeping the newest 5. A new engine may migrate meta.db, and an
#      older one then refuses to serve it (PLAN 6.10), so this backup is how a
#      rollback works: `lumbungd restore --meta-only --from /data/backups/pre-deploy`.
#   2. If the dashboard volume holds a dashboard.db, copy it with VACUUM INTO
#      to /data/backups/pre-deploy-<UTC time>.db (in that volume), keeping 5.
#   3. If scripts/backup.sh changed, reinstall the nightly backup agent so the
#      installed copy in ~/selfhost/bin doesn't drift from the repo.
#
# A running service does its backup with `exec`. A stopped or crash-looping
# one does it in a one-off container (`run --rm --no-deps`) on its current
# image, which is still the old one at this point. That's safe for the
# engine: `lumbungd backup` copies with SQLite's online backup API and never
# takes the data folder's lock (only a serving lumbungd and `restore` do), so
# it neither waits for nor blocks an engine that is starting.
#
# Any failure stops the deploy. Only a missing volume or a volume without the
# file (first deploy) skips a backup.
set -euo pipefail

cd "$(dirname "$0")/.."
export COMPOSE_FILE="${LUMBUNG_COMPOSE_FILE:-compose.yaml:ops/compose.selfhost.yaml}"
KEEP=5

# Overridable so a test can run the first-deploy path against volumes that
# don't exist and never touch the real launchd agent.
DATA_VOLUME="${LUMBUNG_DATA_VOLUME:-lumbung_data}"
DASHBOARD_VOLUME="${LUMBUNG_DASHBOARD_VOLUME:-lumbung_dashboard}"
ENGINE_IMAGE="${LUMBUNG_ENGINE_IMAGE:-lumbung-engine:latest}"
# The engine image is distroless (no shell), so looking inside a volume uses
# the dashboard image, which has sh.
PROBE_IMAGE="${LUMBUNG_PROBE_IMAGE:-lumbung-dashboard:latest}"
INSTALLED_BACKUP="${LUMBUNG_INSTALLED_BACKUP:-$HOME/selfhost/bin/lumbung-backup.sh}"
INSTALL_BACKUP="${LUMBUNG_INSTALL_BACKUP:-./ops/install-backup.sh}"

log() { echo "[pre-deploy] $*"; }

# Docker errors are not hidden: if docker can't answer, the deploy stops here
# instead of migrating without a backup.
volume_exists() {
  local found
  found=$(docker volume ls --quiet --filter "name=^$1\$") || {
    log "docker didn't answer (volume ls), not deploying"
    exit 1
  }
  [ -n "$found" ]
}

# Whether a file exists in a volume: 0 yes, 1 no, 2 couldn't tell.
file_in_volume() {
  local volume="$1" file="$2" answer
  docker image inspect "$PROBE_IMAGE" >/dev/null 2>&1 || {
    log "no $PROBE_IMAGE image to look inside $volume with"
    return 2
  }
  answer=$(docker run --rm --network none --entrypoint sh -v "$volume:/v:ro" "$PROBE_IMAGE" \
    -c 'if [ -f "/v/$1" ]; then echo yes; else echo no; fi' _ "$file") || return 2
  case "$answer" in
    yes) return 0 ;;
    no) return 1 ;;
    *) return 2 ;;
  esac
}

is_running() {
  local id
  id=$(docker compose ps --status running --quiet "$1") || {
    log "docker didn't answer (compose ps $1), not deploying"
    exit 1
  }
  [ -n "$id" ]
}

backup_engine() {
  log "backing up meta.db to /data/backups/pre-deploy"
  if is_running engine; then
    docker compose exec -T engine lumbungd backup --meta-only --to /data/backups/pre-deploy --keep "$KEEP" </dev/null
  else
    log "engine isn't running, using a one-off engine container on the current image"
    # `run` without --build never builds, but a missing image would make
    # Compose try. Refuse instead of building in the middle of a backup.
    docker image inspect "$ENGINE_IMAGE" >/dev/null 2>&1 || {
      log "no $ENGINE_IMAGE image to back up with"
      return 1
    }
    # The image's entrypoint is lumbungd, so the arguments start at the
    # subcommand.
    docker compose run --rm --no-deps -T engine backup --meta-only --to /data/backups/pre-deploy --keep "$KEEP" </dev/null
  fi
}

backup_dashboard() {
  log "backing up dashboard.db to /data/backups/pre-deploy-<time>.db"
  local runner=(exec -T)
  if ! is_running dashboard; then
    log "dashboard isn't running, using a one-off dashboard container on the current image"
    runner=(run --rm --no-deps -T)
  fi
  docker compose "${runner[@]}" dashboard node - /data/backups pre-deploy "$KEEP" <scripts/dashboard-backup.mjs
}

# --- engine -------------------------------------------------------------------
if ! volume_exists "$DATA_VOLUME"; then
  log "no $DATA_VOLUME volume (first deploy), no engine backup needed"
else
  rc=0
  file_in_volume "$DATA_VOLUME" meta.db || rc=$?
  case "$rc" in
    0) backup_engine || { log "engine backup FAILED, not deploying"; exit 1; } ;;
    1) log "$DATA_VOLUME has no meta.db yet, no engine backup needed" ;;
    *) log "couldn't check for meta.db in $DATA_VOLUME, not deploying"; exit 1 ;;
  esac
fi

# --- dashboard ----------------------------------------------------------------
if ! volume_exists "$DASHBOARD_VOLUME"; then
  log "no $DASHBOARD_VOLUME volume (first deploy), no dashboard backup needed"
else
  rc=0
  file_in_volume "$DASHBOARD_VOLUME" dashboard.db || rc=$?
  case "$rc" in
    0) backup_dashboard || { log "dashboard backup FAILED, not deploying"; exit 1; } ;;
    1) log "$DASHBOARD_VOLUME has no dashboard.db yet, no dashboard backup needed" ;;
    *) log "couldn't check for dashboard.db in $DASHBOARD_VOLUME, not deploying"; exit 1 ;;
  esac
fi

# --- nightly backup agent -----------------------------------------------------
if command -v launchctl >/dev/null && ! cmp -s scripts/backup.sh "$INSTALLED_BACKUP"; then
  log "scripts/backup.sh differs from $INSTALLED_BACKUP, reinstalling the nightly backup"
  # A launchd hiccup shouldn't block the deploy, but it must be loud.
  "$INSTALL_BACKUP" || log "WARNING: install-backup.sh failed; the nightly backup still runs the old script"
fi
