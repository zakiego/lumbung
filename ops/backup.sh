#!/bin/bash
# Nightly backup of our Lumbung deployment (zaki-selfhost), run by launchd at
# 03:30 (ops/com.zakiego.lumbung-backup.plist) or by hand.
#
#   1. full engine backup inside the data volume: every database's default
#      branch, meta.db and the service token, with `lumbungd backup --to
#      /data/backups/nightly --keep 7` (consistent while the engine runs)
#   2. dashboard.db inside its volume with VACUUM INTO, to
#      /data/backups/nightly-dashboard-<UTC time>.db, with the dashboard's
#      generated sign-in secret next to it (.auth-secret), keeping 7
#   3. copy the newest of each out of the volumes with `docker compose cp` to
#      ~/selfhost/lumbung-backups/{engine,dashboard}/, check the copies
#      (SHA-256 against the manifest, integrity_check) and keep 7 of each
#
# A copy outside the volume survives a deleted volume, not a dead disk: copy
# ~/selfhost/lumbung-backups somewhere else too. It holds the service token
# and the dashboard's sign-in secret, so it is private to this user (mode
# 700, the secret files 600).
#
# ops/restore-drill.sh proves the newest host copy restores.
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

# Prints the value of a top-level string field from one line of JSON, or
# nothing when it's null.
json_field() { python3 -c 'import json,sys; v = json.loads(sys.stdin.read())[sys.argv[1]]; print("" if v is None else v)' "$1"; }

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

# A backup whose retention failed is complete: report it at the end, but
# never retry it, or every retry takes one more full backup.
retention_failed=""

# --- 1. engine ----------------------------------------------------------------
engine_backup() {
  local rc=0 done_path
  engine_json=$(docker compose exec -T engine lumbungd backup --to /data/backups/nightly --keep "$KEEP" </dev/null) || rc=$?
  [ "$rc" -eq 0 ] && return 0
  # When only retention fails, lumbungd still prints the JSON with the
  # finished backup's folder, then exits 1.
  done_path=$(printf '%s' "$engine_json" | tail -n 1 | json_field backup 2>/dev/null) || done_path=""
  if [ -n "$done_path" ]; then
    log "engine: $done_path is complete, but retention failed (exit $rc); not taking another"
    retention_failed="$retention_failed engine"
    return 0
  fi
  return "$rc"
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
dash_secret=""
dashboard_backup() {
  local rc=0
  dash_json=$(docker compose exec -T dashboard node - /data/backups nightly-dashboard "$KEEP" <ops/dashboard-backup.mjs) || rc=$?
  # 2 means there's no dashboard.db yet: nothing to keep, not worth a retry.
  [ "$rc" -eq 2 ] && { dash_json=""; return 0; }
  # 3 means the copy is complete but retention failed.
  if [ "$rc" -eq 3 ]; then
    log "dashboard: the copy is complete, but retention failed; not taking another"
    retention_failed="$retention_failed dashboard"
    return 0
  fi
  return "$rc"
}
retry dashboard_backup
if [ -n "$dash_json" ]; then
  log "dashboard: $dash_json"
  dash_remote=$(printf '%s' "$dash_json" | tail -n 1 | json_field backup)
  dash_secret=$(printf '%s' "$dash_json" | tail -n 1 | json_field auth_secret)
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
# Object files never change, and docker cp copies hard links as separate
# files: link each object file that the previous host copy holds with the
# same sum, so a night only adds the objects that are new.
prev=""
shopt -s nullglob
for path in "$OUT"/engine/[0-9]*T[0-9]*Z; do prev="$path"; done
shopt -u nullglob
if [ -n "$prev" ]; then
  python3 - "$partial" "$prev" <<'PY'
import json, os, sys
new, prev = sys.argv[1:3]
try:
    old = {f["path"]: f["sha256"] for f in json.load(open(os.path.join(prev, "manifest.json")))["files"]}
except (OSError, ValueError, KeyError):
    sys.exit(0)
linked = 0
for f in json.load(open(os.path.join(new, "manifest.json")))["files"]:
    p = f["path"]
    if not p.startswith("objects/") or old.get(p) != f["sha256"]:
        continue
    src, dst = os.path.join(prev, p), os.path.join(new, p)
    tmp = dst + ".link"
    try:
        os.link(src, tmp)
        os.replace(tmp, dst)
        linked += 1
    except OSError:
        if os.path.exists(tmp):
            os.remove(tmp)
print(f"{linked} object files linked from {os.path.basename(prev)}")
PY
fi
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
  if [ -n "$dash_secret" ]; then
    secret_dest="$OUT/dashboard/$(basename "$dash_secret")"
    partial="$secret_dest.partial"
    docker compose cp "dashboard:$dash_secret" "$partial" >/dev/null
    chmod 600 "$partial"
    [ -s "$partial" ] || { log "the copied auth secret is empty"; exit 1; }
    mv "$partial" "$secret_dest"
    partial=""
    log "dashboard auth secret: $secret_dest"
  else
    log "dashboard: no generated auth secret (LUMBUNG_AUTH_SECRET set?), none copied"
  fi
  prune "$OUT/dashboard" 'nightly-dashboard-*.db'
  prune "$OUT/dashboard" 'nightly-dashboard-*.auth-secret'
fi

if [ -n "$retention_failed" ]; then
  log "retention failed in the volume for:$retention_failed (old backups pile up there; see the lines above)"
  exit 1
fi
