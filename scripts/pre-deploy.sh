#!/bin/bash
# Run by the deploy script from the checkout, after `git pull`
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
#   3. the nightly backup agent: when the installed copy of ops/backup.sh or
#      the plist differs from the repo, or launchd doesn't have the job
#      loaded, reinstall it (ops/install-backup.sh). A failure there warns
#      loudly but doesn't stop the deploy, and the next deploy tries again.
set -euo pipefail

cd "$(dirname "$0")/.."
export COMPOSE_FILE="${LUMBUNG_COMPOSE_FILE:-compose.yaml:ops/compose.selfhost.yaml}"
ALLOW_NO_ROLLBACK="${LUMBUNG_ALLOW_NO_ROLLBACK:-0}"
# Overridable so a test never touches the real launchd agent (see also the
# env ops/install-backup.sh reads).
INSTALL_BACKUP="${LUMBUNG_INSTALL_BACKUP:-./ops/install-backup.sh}"
LAUNCHCTL="${LUMBUNG_LAUNCHCTL:-launchctl}"

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
if command -v "$LAUNCHCTL" >/dev/null; then
  if ! "$INSTALL_BACKUP" --check; then
    log "reinstalling the nightly backup"
    # A launchd hiccup shouldn't block the deploy, but it must be loud.
    if ! "$INSTALL_BACKUP"; then
      if "$INSTALL_BACKUP" --check >/dev/null 2>&1; then
        log "WARNING: install-backup.sh failed, but the nightly backup is loaded and up to date"
      else
        log "WARNING: install-backup.sh failed and the nightly backup is not installed as the repo has it (see above)."
        log "WARNING: run ./ops/install-backup.sh by hand; the next deploy tries again"
      fi
    fi
  fi
else
  log "no launchctl here, not checking the nightly backup"
fi
