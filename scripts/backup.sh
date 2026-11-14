#!/bin/bash
# Nightly backup of our Lumbung deployment (zaki-selfhost), run by launchd at
# 03:30 (ops/com.zakiego.lumbung-backup.plist) or by hand.
#
#   1. full engine backup inside the data volume: every database's default
#      branch, meta.db and the service token, with `lumbungd backup --to
#      /data/backups/nightly --keep 7` (consistent while the engine runs)
#   2. dashboard.db inside its volume with VACUUM INTO, to
#      /data/backups/nightly-dashboard-<UTC time>.db, keeping 7
#   3. copy the newest of each out of the volumes with `docker compose cp` to
#      ~/selfhost/lumbung-backups/{engine,dashboard}/, check the copies
#      (SHA-256 against the manifest, integrity_check) and keep 7 of each
#
# A copy outside the volume survives a deleted volume, not a dead disk: copy
# ~/selfhost/lumbung-backups somewhere else too. It holds the service token,
# so it is private to this user (mode 700).
#
# scripts/restore-drill.sh proves the newest host copy restores.
set -euo pipefail

LUMBUNG_DIR="${LUMBUNG_DIR:-$HOME/selfhost/lumbung}"
OUT="${BACKUP_DIR:-$HOME/selfhost/lumbung-backups}"
LOG="${BACKUP_LOG:-$HOME/selfhost/lumbung-backup.log}"
KEEP="${BACKUP_KEEP:-7}"
MIN_FREE_MB="${MIN_FREE_MB:-1024}"
ATTEMPTS=3
RETRY_DELAY="${RETRY_DELAY:-60}"

# launchd starts with a bare PATH. OrbStack puts docker in /usr/local/bin and
# ~/.orbstack/bin.
export PATH="/usr/local/bin:$HOME/.orbstack/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export COMPOSE_FILE="${LUMBUNG_COMPOSE_FILE:-compose.yaml:ops/compose.selfhost.yaml}"
umask 077

# Keep the log small: trim to the last 2000 lines once it passes 1 MB.
mkdir -p "$(dirname "$LOG")"
if [ -f "$LOG" ] && [ "$(stat -f %z "$LOG")" -gt 1048576 ]; then
  tail -n 2000 "$LOG" >"$LOG.tmp" && mv "$LOG.tmp" "$LOG"
fi
if [ -t 1 ]; then
  exec > >(tee -a "$LOG") 2>&1
else
  exec >>"$LOG" 2>&1
fi

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') $*"; }

partial=""
finish() {
  local status=$?
  [ -n "$partial" ] && rm -rf "$partial"
  if [ "$status" -eq 0 ]; then log "done"; else log "FAILED (exit $status)"; fi
}
trap finish EXIT

log "start"
command -v docker >/dev/null || { log "docker not found"; exit 1; }
command -v python3 >/dev/null || { log "python3 not found"; exit 1; }
command -v sqlite3 >/dev/null || { log "sqlite3 not found"; exit 1; }
[ -d "$LUMBUNG_DIR" ] || { log "no checkout at $LUMBUNG_DIR"; exit 1; }
free_mb=$(df -m /System/Volumes/Data | awk 'NR==2 {print $4}')
[ "$free_mb" -ge "$MIN_FREE_MB" ] || { log "only ${free_mb} MB free, need ${MIN_FREE_MB}"; exit 1; }

cd "$LUMBUNG_DIR"
mkdir -p "$OUT/engine" "$OUT/dashboard"
chmod 700 "$OUT"

# The containers can be mid-restart (a deploy, a Docker update) at 03:30.
# Give them a few minutes before calling the night a failure.
retry() {
  local attempt=1
  until "$@"; do
    if [ "$attempt" -ge "$ATTEMPTS" ]; then
      log "failed $attempt times, giving up: $*"
      return 1
    fi
    log "attempt $attempt failed, retrying in ${RETRY_DELAY}s"
    sleep "$RETRY_DELAY"
    attempt=$((attempt + 1))
  done
}

# Prints the value of a top-level string field from one line of JSON.
json_field() { python3 -c 'import json,sys; print(json.loads(sys.stdin.read())[sys.argv[1]])' "$1"; }

# Deletes all but the newest $KEEP entries matching a glob in a folder. Names
# carry UTC timestamps, and a glob expands sorted, so the oldest come first.
prune() {
  local dir="$1" pattern="$2" names=() path drop i
  shopt -s nullglob
  for path in "$dir"/$pattern; do names+=("$(basename "$path")"); done
  shopt -u nullglob
  drop=$((${#names[@]} - KEEP))
  for ((i = 0; i < drop; i++)); do rm -rf "${dir:?}/${names[i]}"; done
}

# --- 1. engine ----------------------------------------------------------------
engine_backup() {
  engine_json=$(docker compose exec -T engine lumbungd backup --to /data/backups/nightly --keep "$KEEP" </dev/null)
}
retry engine_backup
log "engine: $engine_json"
remote=$(printf '%s' "$engine_json" | tail -n 1 | json_field backup)
stamp=$(basename "$remote")
case "$stamp" in
  [0-9]*T[0-9]*Z) ;;
  *) log "unexpected backup folder name: $remote"; exit 1 ;;
esac

# --- 2. dashboard -------------------------------------------------------------
dash_remote=""
dashboard_backup() {
  local rc=0
  dash_json=$(docker compose exec -T dashboard node - /data/backups nightly-dashboard "$KEEP" <scripts/dashboard-backup.mjs) || rc=$?
  # 2 means there's no dashboard.db yet: nothing to keep, not worth a retry.
  [ "$rc" -eq 2 ] && { dash_json=""; return 0; }
  return "$rc"
}
retry dashboard_backup
if [ -n "$dash_json" ]; then
  log "dashboard: $dash_json"
  dash_remote=$(printf '%s' "$dash_json" | tail -n 1 | json_field backup)
else
  log "dashboard: no dashboard.db yet, skipped"
fi

# --- 3. copy out and check ----------------------------------------------------
dest="$OUT/engine/$stamp"
if [ -e "$dest" ]; then
  log "$dest already exists"
  exit 1
fi
partial="$OUT/engine/.$stamp.partial"
rm -rf "$partial"
docker compose cp "engine:$remote" "$partial" >/dev/null
# Every file must match the SHA-256 sum lumbungd wrote in the manifest.
python3 - "$partial" <<'PY'
import hashlib, json, os, sys
root = sys.argv[1]
m = json.load(open(os.path.join(root, "manifest.json")))
for f in m["files"]:
    h = hashlib.sha256()
    with open(os.path.join(root, f["path"]), "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            h.update(chunk)
    if h.hexdigest() != f["sha256"]:
        sys.exit(f"{f['path']} doesn't match the manifest")
print(f"{len(m['files'])} files match the manifest")
PY
mv "$partial" "$dest"
partial=""
log "engine copy: $dest ($(du -sk "$dest" | cut -f1) KB)"
prune "$OUT/engine" '[0-9]*T[0-9]*Z'

if [ -n "$dash_remote" ]; then
  dash_dest="$OUT/dashboard/$(basename "$dash_remote")"
  partial="$dash_dest.partial"
  docker compose cp "dashboard:$dash_remote" "$partial" >/dev/null
  check=$(sqlite3 -readonly "$partial" 'PRAGMA integrity_check')
  if [ "$check" != "ok" ]; then
    log "dashboard copy failed its integrity check: $check"
    exit 1
  fi
  mv "$partial" "$dash_dest"
  partial=""
  log "dashboard copy: $dash_dest ($(stat -f %z "$dash_dest") bytes)"
  prune "$OUT/dashboard" 'nightly-dashboard-*.db'
fi
