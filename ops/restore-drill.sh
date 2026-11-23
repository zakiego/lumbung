#!/bin/bash
# Restore drill: prove the newest nightly backup on the host restores into a
# working engine. Run on the Docker host.
#
#   ops/restore-drill.sh            newest backup in ~/selfhost/lumbung-backups/engine
#   ops/restore-drill.sh <folder>   one backup folder (holds manifest.json)
#
#   1. copy the backup into a scratch volume, owned by the engine's user
#      (65532), with a small alpine image (the engine and dashboard images
#      have no shell)
#   2. `lumbungd restore` it into a second, empty scratch volume
#   3. start a throwaway engine on that volume on a free 127.0.0.1 port
#   4. wait for /health 200, then with the restored service token run
#      ops/restore-check.py: every database's default branch passes PRAGMA
#      integrity_check, and every bucket lists the objects the backup holds,
#      with objects downloaded and checked against their size and ETag
#   5. remove the container and both volumes, whatever happened
#
# Prints PASS or FAIL as the last line and exits 0 or 1. A backup with no
# databases and no buckets fails (it proves nothing) unless
# DRILL_ALLOW_EMPTY=1.
# Never touches the running deployment: its own container, volumes and port.
set -euo pipefail

BACKUPS="${BACKUP_DIR:-$HOME/selfhost/lumbung-backups}/engine"
IMAGE="${LUMBUNG_ENGINE_IMAGE:-lumbung-engine:latest}"
HELPER_IMAGE="${LUMBUNG_HELPER_IMAGE:-alpine:3.24.4}"
HEALTH_TIMEOUT="${DRILL_HEALTH_TIMEOUT:-60}"
ALLOW_EMPTY="${DRILL_ALLOW_EMPTY:-0}"

export PATH="/usr/local/bin:$HOME/.orbstack/bin:/opt/homebrew/bin:$PATH"

log() { echo "[restore-drill] $*"; }

name="lumbung-drill-$(date +%s)-$$"
stage="$name-stage"
target="$name-data"
container="$name"
tmpdir=""
created=0

finish() {
  local status=$?
  if [ "$created" -eq 1 ]; then
    log "tearing down $name"
    docker rm -f "$container" >/dev/null 2>&1 || true
    docker volume rm "$stage" "$target" >/dev/null 2>&1 ||
      log "WARNING: couldn't remove the scratch volumes, run: docker volume rm $stage $target"
  fi
  [ -n "$tmpdir" ] && rm -rf "$tmpdir"
  if [ "$status" -eq 0 ]; then echo "PASS"; else echo "FAIL"; fi
}
trap finish EXIT

for tool in docker curl python3 lsof; do
  command -v "$tool" >/dev/null || { log "$tool not found"; exit 1; }
done
docker image inspect "$IMAGE" >/dev/null 2>&1 || { log "image $IMAGE not found on this host"; exit 1; }
if ! docker image inspect "$HELPER_IMAGE" >/dev/null 2>&1; then
  log "pulling $HELPER_IMAGE"
  docker pull -q "$HELPER_IMAGE" >/dev/null || { log "couldn't pull $HELPER_IMAGE"; exit 1; }
fi

# --- which backup -------------------------------------------------------------
if [ $# -ge 1 ]; then
  src="$1"
else
  [ -d "$BACKUPS" ] || { log "no backup folder at $BACKUPS"; exit 1; }
  newest=""
  shopt -s nullglob
  for path in "$BACKUPS"/[0-9]*T[0-9]*Z; do newest="$path"; done
  shopt -u nullglob
  [ -n "$newest" ] || { log "no backups in $BACKUPS"; exit 1; }
  src="$newest"
fi
[ -f "$src/manifest.json" ] || { log "$src isn't a backup folder (no manifest.json)"; exit 1; }
src="$(cd "$src" && pwd)"
log "backup: $src"

# A free port in the 4800s range, picked from the top down so it
# stays clear of the apps' ports.
port="${DRILL_PORT:-}"
if [ -z "$port" ]; then
  for p in 4899 4898 4897 4896 4895 4894 4893 4892 4891 4890; do
    if ! lsof -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then port="$p"; break; fi
  done
  [ -n "$port" ] || { log "no free port in 4890-4899"; exit 1; }
elif lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
  log "port $port is already in use"
  exit 1
fi

tmpdir=$(mktemp -d /tmp/lumbung-drill.XXXXXX)
chmod 700 "$tmpdir"

# --- 1. stage the backup -------------------------------------------------------
created=1
docker volume create --label lumbung.drill=1 "$stage" >/dev/null
docker volume create --label lumbung.drill=1 "$target" >/dev/null
docker run --rm --network none --user 0 --entrypoint sh \
  -v "$src:/src:ro" -v "$stage:/stage" "$HELPER_IMAGE" \
  -c 'cp -R /src/. /stage/ && chown -R 65532:65532 /stage'

# --- 2. restore ------------------------------------------------------------------
# A fresh volume mounted at the image's /data starts empty and owned by the
# engine's user, which is what restore wants.
log "restoring into volume $target"
docker run --rm --network none -v "$stage:/backup:ro" -v "$target:/data" "$IMAGE" \
  restore --from /backup --to /data

# --- 3. start a throwaway engine ---------------------------------------------
log "starting a throwaway engine on 127.0.0.1:$port"
docker run -d --name "$container" --label lumbung.drill=1 --memory 512m \
  -p "127.0.0.1:$port:4870" -v "$target:/data" "$IMAGE" >/dev/null

deadline=$((SECONDS + HEALTH_TIMEOUT))
code=""
until [ "$code" = "200" ]; do
  if [ "$SECONDS" -ge "$deadline" ]; then
    log "/health didn't answer 200 within ${HEALTH_TIMEOUT}s (last: ${code:-no answer})"
    docker logs --tail 50 "$container" >&2 || true
    exit 1
  fi
  sleep 1
  code=$(curl -s -o "$tmpdir/health.json" -w '%{http_code}' -m 5 "http://127.0.0.1:$port/health" || true)
done
log "/health is green: $(head -c 300 "$tmpdir/health.json")"

# --- 4. check every database and bucket ---------------------------------------
# The token goes into a private file, never onto a command line.
docker exec "$container" lumbungd service-token >"$tmpdir/token"
[ -s "$tmpdir/token" ] || { log "the restored engine has no service token"; exit 1; }

DRILL_ALLOW_EMPTY="$ALLOW_EMPTY" python3 "$(dirname "$0")/restore-check.py" \
  "http://127.0.0.1:$port" "$tmpdir/token" "$src/manifest.json"
