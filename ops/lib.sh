# Shared helpers for the deployment scripts. Sourced, not run,
# by scripts/pre-deploy.sh, scripts/deploy-up.sh and ops/rollback-meta.sh,
# from the repo root with COMPOSE_FILE set. Callers define log().
#
# Image and volume names come from `docker compose config`, so a scratch
# project (COMPOSE_PROJECT_NAME plus its own image names) never touches ours.
# shellcheck shell=bash

# Where pre-deploy meta.db backups go, inside the engine's data volume.
META_BACKUPS=/data/backups/pre-deploy

# Sets ENGINE_IMAGE, DASHBOARD_IMAGE (both :latest), ENGINE_PREV,
# DASHBOARD_PREV, DATA_VOLUME and DASHBOARD_VOLUME. A docker that doesn't
# answer is an error, never "nothing there".
# shellcheck disable=SC2034 # the callers use these
load_compose() {
  local out
  out=$(docker compose config --format json | python3 -c '
import json, sys
d = json.load(sys.stdin)
def image(name):
    i = d["services"][name].get("image") or d["name"] + "-" + name
    return i if ":" in i.rsplit("/", 1)[-1] else i + ":latest"
print(image("engine"))
print(image("dashboard"))
print(d["volumes"]["data"]["name"])
print(d["volumes"]["dashboard"]["name"])
') || {
    log "couldn't read the compose config (is docker running?)"
    return 1
  }
  {
    read -r ENGINE_IMAGE
    read -r DASHBOARD_IMAGE
    read -r DATA_VOLUME
    read -r DASHBOARD_VOLUME
  } <<<"$out"
  ENGINE_PREV="${ENGINE_IMAGE%:*}:prev"
  DASHBOARD_PREV="${DASHBOARD_IMAGE%:*}:prev"
}

image_exists() { docker image inspect "$1" >/dev/null 2>&1; }

# Exits the script when docker can't answer, so a broken docker never reads
# as a first deploy.
volume_exists() {
  local found
  found=$(docker volume ls --quiet --filter "name=^$1\$") || {
    log "docker didn't answer (volume ls)"
    exit 1
  }
  [ -n "$found" ]
}

# Looking inside a volume needs node, which the engine image (distroless)
# doesn't have. Any dashboard image does.
probe_image() {
  local image
  for image in "$DASHBOARD_IMAGE" "$DASHBOARD_PREV"; do
    if image_exists "$image"; then
      echo "$image"
      return 0
    fi
  done
  log "no $DASHBOARD_IMAGE or $DASHBOARD_PREV image to look inside a volume with"
  return 1
}

# Runs node in a throwaway container with a volume at /v, as the engine's
# user (65532, which owns its files). Arguments after the volume go to node.
volume_node() {
  local volume="$1" image
  shift
  image=$(probe_image) || return 1
  docker run --rm -i --network none --user 65532:65532 -v "$volume:/v" --entrypoint node "$image" "$@"
}

# Whether a file exists in a volume: 0 yes, 1 no, 2 couldn't tell.
file_in_volume() {
  local answer
  answer=$(volume_node "$1" -e 'console.log(require("fs").existsSync("/v/" + process.argv[1]) ? "yes" : "no")' "$2" </dev/null) || return 2
  case "$answer" in
    yes) return 0 ;;
    no) return 1 ;;
    *) return 2 ;;
  esac
}

# PRAGMA user_version of meta.db in the data volume: how many migrations it
# has.
meta_user_version() {
  volume_node "$DATA_VOLUME" -e '
const { DatabaseSync } = require("node:sqlite")
const db = new DatabaseSync("/v/meta.db", { timeout: 10000 })
console.log(db.prepare("PRAGMA user_version").get().user_version)
db.close()' </dev/null 2>/dev/null
}

# meta_version recorded in the manifest of a backup in the data volume. The
# path is the one lumbungd printed, under /data.
manifest_meta_version() {
  volume_node "$DATA_VOLUME" -e '
const m = JSON.parse(require("fs").readFileSync("/v/" + process.argv[1] + "/manifest.json", "utf8"))
if (!Number.isInteger(m.meta_version)) process.exit(1)
console.log(m.meta_version)' "${1#/data/}" </dev/null
}

# Removes one backup folder from the data volume.
remove_backup() {
  volume_node "$DATA_VOLUME" -e 'require("fs").rmSync("/v/" + process.argv[1], { recursive: true, force: true })' \
    "${1#/data/}" </dev/null
}

# Keeps the newest $1 meta-only backups in $META_BACKUPS. Every one there
# is a good rollback point: deploy-up.sh removes a copy the rollback image
# couldn't restore as soon as it's taken, so it never pushes a good one out.
prune_meta_backups() {
  volume_node "$DATA_VOLUME" -e '
const fs = require("fs"), dir = "/v/" + process.argv[1], keep = Number(process.argv[2])
const all = fs.readdirSync(dir).filter((n) => /^\d{8}T\d{6}Z-meta$/.test(n)).sort().reverse()
for (const n of all.slice(keep)) fs.rmSync(dir + "/" + n, { recursive: true, force: true })' \
    "${META_BACKUPS#/data/}" "$1" </dev/null
}

# Prints the newest meta-only backup in $META_BACKUPS whose meta_version is
# at most $1, as a /data path. Fails when there's none.
# shellcheck disable=SC2016 # JavaScript template strings, not shell
pick_meta_backup() {
  volume_node "$DATA_VOLUME" -e '
const fs = require("fs"), rel = process.argv[1], known = Number(process.argv[2])
let names = []
try { names = fs.readdirSync("/v/" + rel) } catch {}
for (const n of names.filter((n) => /^\d{8}T\d{6}Z-meta$/.test(n)).sort().reverse()) {
  try {
    const m = JSON.parse(fs.readFileSync(`/v/${rel}/${n}/manifest.json`, "utf8"))
    if (Number.isInteger(m.meta_version) && m.meta_version <= known) {
      console.log(`/data/${rel}/${n}`)
      process.exit(0)
    }
  } catch {}
}
process.exit(1)' "${META_BACKUPS#/data/}" "$1" </dev/null
}

# How many meta migrations an engine image knows: start it on an empty tmpfs
# and read meta_version from /health. A meta.db with more than that is one
# the image refuses to serve (PLAN 6.10).
known_meta_version() {
  local image="$1" cid port v="" i
  cid=$(docker run -d --rm --label lumbung.probe=1 --tmpfs /data:uid=65532,gid=65532,mode=0700 \
    -p 127.0.0.1::4870 "$image") || return 1
  port=$(docker port "$cid" 4870/tcp | head -n 1)
  for ((i = 0; i < 60; i++)); do
    v=$(curl -fsS -m 2 "http://$port/health" 2>/dev/null |
      python3 -c 'import json, sys; print(int(json.load(sys.stdin)["meta_version"]))' 2>/dev/null) && break
    v=""
    sleep 0.5
  done
  docker rm -f "$cid" >/dev/null 2>&1 || true
  [ -n "$v" ] || {
    log "couldn't read the meta version $image knows"
    return 1
  }
  echo "$v"
}

# Prints a top-level field of the last JSON line on stdin.
json_field() { tail -n 1 | python3 -c 'import json, sys; print(json.loads(sys.stdin.read())[sys.argv[1]])' "$1"; }
