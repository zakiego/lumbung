#!/bin/bash
# Restore drill: prove the newest nightly backup on the host restores into a
# working engine. Our deployment (zaki-selfhost); run on the Docker host.
#
#   scripts/restore-drill.sh            newest backup in ~/selfhost/lumbung-backups/engine
#   scripts/restore-drill.sh <folder>   one backup folder (holds manifest.json)
#
#   1. copy the backup into a scratch volume, owned by the engine's user
#      (65532), with the dashboard image (the engine image has no shell)
#   2. `lumbungd restore` it into a second, empty scratch volume
#   3. start a throwaway engine on that volume on a free 127.0.0.1 port
#   4. wait for /health 200, then with the restored service token list every
#      database and run PRAGMA integrity_check on each default branch through
#      the API
#   5. remove the container and both volumes, whatever happened
#
# Prints PASS or FAIL as the last line and exits 0 or 1. A backup with no
# databases fails (it proves nothing) unless DRILL_ALLOW_EMPTY=1.
# Never touches the running deployment: its own container, volumes and port.
set -euo pipefail

BACKUPS="${BACKUP_DIR:-$HOME/selfhost/lumbung-backups}/engine"
IMAGE="${LUMBUNG_ENGINE_IMAGE:-lumbung-engine:latest}"
HELPER_IMAGE="${LUMBUNG_PROBE_IMAGE:-lumbung-dashboard:latest}"
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
for image in "$IMAGE" "$HELPER_IMAGE"; do
  docker image inspect "$image" >/dev/null 2>&1 || { log "image $image not found on this host"; exit 1; }
done

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

# A free port in the zaki-selfhost range, picked from the top down so it
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

# --- 4. check every database -------------------------------------------------
# The token goes into a private file, never onto a command line.
docker exec "$container" lumbungd service-token >"$tmpdir/token"
[ -s "$tmpdir/token" ] || { log "the restored engine has no service token"; exit 1; }

ALLOW_EMPTY="$ALLOW_EMPTY" python3 - "http://127.0.0.1:$port" "$tmpdir/token" "$src/manifest.json" <<'PY'
import json, os, sys, urllib.parse, urllib.request

base, token_file, manifest_file = sys.argv[1:4]
token = open(token_file).read().strip()

def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base + path, data=data, method=method)
    req.add_header("Authorization", f"Bearer {token}")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=60) as res:
        return json.load(res)

dbs, cursor = [], ""
while True:
    q = {"limit": "200"}
    if cursor:
        q["cursor"] = cursor
    page = call("GET", "/v1/databases?" + urllib.parse.urlencode(q))
    dbs += page.get("data") or []
    cursor = page.get("next_cursor") or ""
    if not cursor:
        break

manifest = json.load(open(manifest_file))
files = sum(1 for f in manifest["files"] if f["path"].startswith("dbs/"))
print(f"[restore-drill] {len(dbs)} databases restored, {files} branch files in the backup")
if len(dbs) != files:
    print("[restore-drill] WARNING: the counts differ (a branch skipped while the backup ran?)")
if not dbs and os.environ.get("ALLOW_EMPTY") != "1":
    sys.exit("[restore-drill] the backup has no databases, so this proves nothing (DRILL_ALLOW_EMPTY=1 to accept)")

bad = 0
for db in dbs:
    res = call("POST", f"/v1/databases/{urllib.parse.quote(db['id'])}/query",
               {"sql": "PRAGMA integrity_check", "read_only": True})
    vals = [v.get("value") if isinstance(v, dict) else v for row in res.get("rows", []) for v in row]
    ok = vals == ["ok"]
    bad += not ok
    print(f"[restore-drill] {db['name']} ({db.get('default_branch')}): {'ok' if ok else vals}")
if bad:
    sys.exit(f"[restore-drill] {bad} database(s) failed integrity_check")
PY
