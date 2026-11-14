#!/bin/bash
# Run by the zaki-selfhost deploy script from the checkout, after `git pull`
# and before the build. A non-zero exit stops the deploy. Our deployment only
# (see ops/README.md).
#
# This is the cheap check before a build. The backups themselves are taken by
# scripts/deploy-up.sh right before the swap, after both images are built, so
# nothing written during the build is missing from them.
#
#   1. docker answers, and the images the backups and a rollback need exist
#      when the volumes hold data
#   2. meta.db is one the running engine image can serve: a meta.db newer than
#      that image means there's no rollback point, so the deploy stops
#      (LUMBUNG_ALLOW_NO_ROLLBACK=1 deploys anyway, for when the new engine is
#      the fix)
#   3. If ops/backup.sh changed, reinstall the nightly backup agent so the
#      installed copy in ~/selfhost/bin doesn't drift from the repo.
set -euo pipefail

cd "$(dirname "$0")/.."
export COMPOSE_FILE="${LUMBUNG_COMPOSE_FILE:-compose.yaml:ops/compose.selfhost.yaml}"
ALLOW_NO_ROLLBACK="${LUMBUNG_ALLOW_NO_ROLLBACK:-0}"
# Overridable so a test never touches the real launchd agent.
INSTALLED_BACKUP="${LUMBUNG_INSTALLED_BACKUP:-$HOME/selfhost/bin/lumbung-backup.sh}"
INSTALL_BACKUP="${LUMBUNG_INSTALL_BACKUP:-./ops/install-backup.sh}"

log() { echo "[pre-deploy] $*"; }
# shellcheck source=ops/lib.sh
. ops/lib.sh

load_compose || { log "not deploying"; exit 1; }

# --- engine -------------------------------------------------------------------
if ! volume_exists "$DATA_VOLUME"; then
  log "no $DATA_VOLUME volume (first deploy), no engine backup needed"
else
  rc=0
  file_in_volume "$DATA_VOLUME" meta.db || rc=$?
  case "$rc" in
    0)
      image_exists "$ENGINE_IMAGE" || {
        log "$DATA_VOLUME holds a meta.db but there's no $ENGINE_IMAGE image to back it up and roll back to, not deploying"
        exit 1
      }
      known=$(known_meta_version "$ENGINE_IMAGE") || { log "not deploying"; exit 1; }
      current=$(meta_user_version) || { log "couldn't read meta.db's version, not deploying"; exit 1; }
      if [ "$current" -gt "$known" ]; then
        log "meta.db has $current migrations, but $ENGINE_IMAGE knows only $known: it can't serve it, so there's no rollback point"
        if [ "$ALLOW_NO_ROLLBACK" != 1 ]; then
          log "not deploying (LUMBUNG_ALLOW_NO_ROLLBACK=1 deploys without one)"
          exit 1
        fi
        log "LUMBUNG_ALLOW_NO_ROLLBACK=1: deploying without a meta.db rollback point"
      else
        log "meta.db has $current migrations, $ENGINE_IMAGE knows $known"
      fi
      ;;
    1) log "$DATA_VOLUME has no meta.db yet, no engine backup needed" ;;
    *) log "couldn't check for meta.db in $DATA_VOLUME, not deploying"; exit 1 ;;
  esac
fi

# --- dashboard ----------------------------------------------------------------
if volume_exists "$DASHBOARD_VOLUME" && ! image_exists "$DASHBOARD_IMAGE"; then
  log "there's a $DASHBOARD_VOLUME volume but no $DASHBOARD_IMAGE image to back it up with, not deploying"
  exit 1
fi

# --- nightly backup agent -----------------------------------------------------
if command -v launchctl >/dev/null && ! cmp -s ops/backup.sh "$INSTALLED_BACKUP"; then
  log "ops/backup.sh differs from $INSTALLED_BACKUP, reinstalling the nightly backup"
  # A launchd hiccup shouldn't block the deploy, but it must be loud.
  "$INSTALL_BACKUP" || log "WARNING: install-backup.sh failed; the nightly backup still runs the old script"
fi
