#!/usr/bin/env bash
# upgrade-rehearsal: the upgrade to workspaces (PLAN 17.10, ops/README.md),
# run on this machine against nothing live.
#
#   ./scripts/upgrade-rehearsal.sh
#   REHEARSAL_OLD_REF=<commit> ./scripts/upgrade-rehearsal.sh
#
# 1. Builds lumbungd from before workspaces (meta.db version 5) and fills
#    a data folder the way our instance looks: databases testing and palui,
#    a public bucket photos, a bucket palui-files, an instance admin token,
#    a grants token for Palui, and the dashboard's service token.
# 2. Starts this checkout's lumbungd on the same data folder (migration 6)
#    and checks that nothing noticed: every token, the public link, MCP.
# 3. Walks the steps in ops/README.md: a workspace for Palui, its token,
#    the two moves, and what each token can reach afterwards.
# 4. Backs the upgraded instance up, restores it into an empty folder, and
#    runs ops/restore-check.py on the restored engine.
#
# Everything lives in a temp folder and on free local ports, and is removed
# at the end. Prints PASS or FAIL as the last line.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
# The last commit before migration 6.
old_ref="${REHEARSAL_OLD_REF:-370cdd9}"
work="$(mktemp -d "${TMPDIR:-/tmp}/lumbung-rehearsal.XXXXXX")"
chmod 700 "$work"
pids=()
steps=0

log() { printf '[rehearsal] %s\n' "$*"; }
ok() { steps=$((steps + 1)); printf 'PASS %2d. %s\n' "$steps" "$*"; }
die() { printf 'FAIL %s\n' "$*" >&2; exit 1; }

finish() {
  status=$?
  for pid in "${pids[@]:-}"; do [ -z "$pid" ] || kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
  rm -rf "$work"
  if [ "$status" -eq 0 ]; then echo "PASS"; else echo "FAIL"; fi
}
trap finish EXIT

free_port() { python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'; }

# start <binary> <data dir> <port>: starts an engine and waits for /health.
start() {
  LUMBUNG_DATA_DIR="$2" LUMBUNG_ADDR="127.0.0.1:$3" LUMBUNG_MIN_FREE_DISK=1 "$1" >"$work/engine-$3.log" 2>&1 &
  pids+=("$!")
  for _ in $(seq 1 100); do
    if curl -sf -m 2 "http://127.0.0.1:$3/health" >/dev/null 2>&1; then return 0; fi
    sleep 0.2
  done
  tail -n 30 "$work/engine-$3.log" >&2
  die "lumbungd on port $3 didn't answer /health"
}

stop_last() {
  local n=$(( ${#pids[@]} - 1 ))
  kill "${pids[$n]}" 2>/dev/null || true
  wait "${pids[$n]}" 2>/dev/null || true
  pids[$n]=""
}

# api <token> <method> <path> [json body]: prints the body, fails on an HTTP error.
api() {
  local token="$1" method="$2" path="$3" body="${4:-}"
  if [ -n "$body" ]; then
    curl -sS -f -m 60 -X "$method" "$base$path" -H "Authorization: Bearer $token" -H "Content-Type: application/json" -d "$body"
  else
    curl -sS -f -m 60 -X "$method" "$base$path" -H "Authorization: Bearer $token"
  fi
}

# status <token> <method> <path> [json body]: prints only the HTTP status.
status() {
  local token="$1" method="$2" path="$3" body="${4:-}"
  if [ -n "$body" ]; then
    curl -s -o /dev/null -w '%{http_code}' -m 60 -X "$method" "$base$path" -H "Authorization: Bearer $token" -H "Content-Type: application/json" -d "$body"
  else
    curl -s -o /dev/null -w '%{http_code}' -m 60 -X "$method" "$base$path" -H "Authorization: Bearer $token"
  fi
}

want() { [ "$1" = "$2" ] || die "$3: got $1, want $2"; }
names() { python3 -c 'import json, sys; print(",".join(sorted(x["name"] for x in json.load(sys.stdin)["data"])))'; }
field() { python3 -c 'import json, sys; print(json.load(sys.stdin)[sys.argv[1]])' "$1"; }
rows() { python3 -c 'import json, sys; print(json.dumps([[v["value"] if isinstance(v, dict) else v for v in row] for row in json.load(sys.stdin)["rows"]]))'; }

# mcp <token> <path> <tool> <json arguments>: one stateless MCP tool call, prints the result text.
mcp() {
  curl -sS -f -m 60 "$base$2" -H "Authorization: Bearer $1" -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$3\",\"arguments\":$4}}" |
    python3 -c 'import json, sys
raw = sys.stdin.read()
line = next((l[5:] for l in raw.splitlines() if l.startswith("data:")), raw)
msg = json.loads(line)
print(json.dumps(msg.get("result", msg)))'
}

# --- 1. the instance as it is today -------------------------------------------
log "building lumbungd from $old_ref (before workspaces)"
mkdir -p "$work/old"
git -C "$root" archive "$old_ref" apps/engine | tar -x -C "$work/old"
(cd "$work/old/apps/engine" && go build -o "$work/lumbungd-old" ./cmd/lumbungd)
log "building lumbungd from this checkout"
(cd "$root/apps/engine" && go build -o "$work/lumbungd-new" ./cmd/lumbungd)

data="$work/data"
port="$(free_port)"
base="http://127.0.0.1:$port"
start "$work/lumbungd-old" "$data" "$port"
want "$(curl -s "$base/health" | field meta_version)" "5" "the old engine's meta version"
service="$(cat "$data/secrets/service-token")"

admin="$(api "$service" POST /v1/tokens '{"name":"mcporter","access":"admin"}' | field token)"
for db in testing palui; do
  api "$admin" POST /v1/databases "{\"name\":\"$db\"}" >/dev/null
  api "$admin" POST "/v1/databases/$db/batch" '{"statements":[{"sql":"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)"},{"sql":"INSERT INTO notes (body) VALUES ('"'"'kept across the upgrade'"'"')"}]}' >/dev/null
done
api "$admin" POST /v1/buckets '{"name":"photos"}' >/dev/null
api "$admin" PATCH /v1/buckets/photos '{"public_read":true}' >/dev/null
api "$admin" POST /v1/buckets '{"name":"palui-files"}' >/dev/null
put() { curl -sS -f -m 60 -X PUT "$base/v1/buckets/$1/object?key=$2" -H "Authorization: Bearer $admin" -H "Content-Type: text/plain" --data-binary "$3" >/dev/null; }
put photos cat.txt "a very good cat"
put palui-files memory.txt "what Palui remembers"
grants="$(api "$admin" POST /v1/tokens '{"name":"palui","grants":[{"database":"palui","access":"write"},{"bucket":"palui-files","access":"write"}]}' | field token)"
want "$(api "$grants" GET /v1/databases | names)" "palui" "the grants token's databases before the upgrade"
want "$(curl -s -m 10 "$base/v1/s3/photos/cat.txt")" "a very good cat" "the public link before the upgrade"
stop_last
ok "an instance at meta version 5: testing, palui, photos (public), palui-files, three kinds of token"

# --- 2. deploy: the new engine on the same data --------------------------------
start "$work/lumbungd-new" "$data" "$port"
want "$(curl -s "$base/health" | field meta_version)" "6" "the new engine's meta version"
ok "the new engine starts on that data and migrates it to version 6"

want "$(api "$admin" GET /v1/workspaces | names)" "default" "workspaces after the upgrade"
want "$(api "$admin" GET /v1/databases | names)" "palui,testing" "the instance token's databases"
want "$(api "$admin" GET /v1/buckets | names)" "palui-files,photos" "the instance token's buckets"
want "$(api "$service" GET /v1/databases | names)" "palui,testing" "the service token's databases"
ok "everything is in the default workspace, and the instance and service tokens see it on the old URLs"

want "$(api "$grants" GET /v1/databases | names)" "palui" "the grants token's databases"
want "$(api "$grants" POST /v1/databases/palui/query '{"sql":"SELECT body FROM notes"}' | rows)" '[["kept across the upgrade"]]' "Palui's rows"
want "$(curl -sS -f -m 10 "$base/v1/buckets/palui-files/object?key=memory.txt" -H "Authorization: Bearer $grants")" "what Palui remembers" "Palui's file"
want "$(status "$grants" GET /v1/databases/testing/branches)" "404" "the grants token on another database"
api "$grants" POST /v1/databases/palui/query '{"sql":"INSERT INTO notes (body) VALUES ('"'"'written after the upgrade'"'"')"}' >/dev/null
mcp "$grants" /mcp list_databases '{}' | grep -q '"palui' || die "MCP list_databases with the grants token doesn't name palui"
mcp "$grants" /mcp list_databases '{}' | grep -q 'testing' && die "MCP list_databases with the grants token names testing"
ok "Palui's grants token still reads, writes and uses MCP, and still reaches nothing else"

want "$(curl -s -m 10 "$base/v1/s3/photos/cat.txt")" "a very good cat" "the public link after the upgrade"
want "$(curl -s -o /dev/null -w '%{http_code}' -m 10 "$base/v1/s3/palui-files/memory.txt")" "403" "an anonymous read of a private bucket"
ok "the public link to photos still opens, and a private bucket is still closed"

# --- 3. Palui's own workspace (ops/README.md, steps 3 to 6) --------------------
api "$admin" POST /v1/workspaces '{"name":"palui"}' >/dev/null
token="$(api "$admin" POST /v1/workspaces/palui/tokens '{"name":"palui","access":"admin"}' | field token)"
want "$(api "$token" GET /v1/databases | names)" "" "the new token before the move"
ok "a workspace for Palui and its admin token, made before anything moves"

api "$admin" POST /v1/databases/palui/move '{"workspace":"palui"}' >/dev/null
want "$(status "$grants" GET /v1/databases)" "401" "the grants token after the first move"
api "$admin" POST /v1/buckets/palui-files/move '{"workspace":"palui"}' >/dev/null
want "$(api "$admin" GET /v1/databases | names)" "testing" "the default workspace's databases after the moves"
want "$(api "$admin" GET /v1/buckets | names)" "photos" "the default workspace's buckets after the moves"
ok "the moves revoke the old grants token and leave testing and photos where they were"

want "$(api "$token" GET /v1/databases | names)" "palui" "the workspace token's databases"
want "$(api "$token" GET /v1/buckets | names)" "palui-files" "the workspace token's buckets"
want "$(api "$token" GET /v1/workspaces | names)" "palui" "the workspaces the token can see"
want "$(api "$token" POST /v1/databases/palui/query '{"sql":"SELECT body FROM notes ORDER BY id"}' | rows)" '[["kept across the upgrade"], ["written after the upgrade"]]' "Palui's rows after the move"
want "$(curl -sS -f -m 10 "$base/v1/buckets/palui-files/object?key=memory.txt" -H "Authorization: Bearer $token")" "what Palui remembers" "Palui's file after the move"
mcp "$token" /mcp list_databases '{}' | grep -q '"palui' || die "MCP list_databases with the new token doesn't name palui"
ok "the new token reaches Palui's rows and file on the same URLs, MCP included"

api "$token" POST /v1/databases '{"name":"scratch"}' >/dev/null
api "$token" POST /v1/buckets '{"name":"scratch-files"}' >/dev/null
api "$token" DELETE /v1/databases/scratch >/dev/null
api "$token" DELETE /v1/buckets/scratch-files >/dev/null
# A name the default workspace uses is free here, and says nothing about there.
api "$token" POST /v1/databases '{"name":"testing"}' >/dev/null
api "$token" DELETE /v1/databases/testing >/dev/null
ok "Palui makes and deletes its own databases and buckets, a name taken elsewhere included"

want "$(status "$token" GET /v1/workspaces/default/databases)" "404" "the new token naming the default workspace"
want "$(status "$token" GET /v1/workspaces/no-such-workspace/databases)" "404" "the new token naming a workspace that doesn't exist"
want "$(status "$token" GET /v1/instance/tokens)" "403" "the new token on the instance's tokens"
want "$(status "$token" POST /v1/workspaces '{"name":"more"}')" "403" "the new token making a workspace"
want "$(status "$token" PATCH /v1/workspaces/palui '{"max_databases":0}')" "403" "the new token changing its own limits"
mcp "$token" /mcp list_databases '{}' | grep -q 'testing' && die "MCP list_databases with the new token names testing"
want "$(api "$admin" GET /v1/workspaces/palui/databases | names)" "palui" "the instance token inside Palui's workspace"
mcp "$admin" /v1/workspaces/palui/mcp list_databases '{}' | grep -q '"palui' || die "MCP at the workspace URL with the instance token doesn't name palui"
want "$(curl -s -m 10 "$base/v1/s3/photos/cat.txt")" "a very good cat" "the public link after the moves"
ok "nothing outside its workspace exists for the new token, and the instance token still reaches inside"

# --- 4. a backup of the upgraded instance restores -----------------------------
LUMBUNG_DATA_DIR="$data" LUMBUNG_MIN_FREE_DISK=1 "$work/lumbungd-new" backup --to "$work/backups" --all-branches >"$work/backup.log" 2>&1 || { cat "$work/backup.log" >&2; die "the backup failed"; }
backup="$(find "$work/backups" -name manifest.json -maxdepth 3 | head -n 1)"
[ -n "$backup" ] || die "the backup wrote no manifest"
stop_last
restored="$work/restored"
LUMBUNG_MIN_FREE_DISK=1 "$work/lumbungd-new" restore --from "$(dirname "$backup")" --to "$restored" >"$work/restore.log" 2>&1 || { cat "$work/restore.log" >&2; die "the restore failed"; }
port2="$(free_port)"
base="http://127.0.0.1:$port2"
start "$work/lumbungd-new" "$restored" "$port2"
python3 "$root/ops/restore-check.py" "$base" "$restored/secrets/service-token" "$backup" | tee "$work/check.log"
grep -q "database palui/palui (main): ok" "$work/check.log" || die "the restore check didn't reach Palui's database in its workspace"
grep -q "bucket palui/palui-files: 1 objects, ok" "$work/check.log" || die "the restore check didn't reach Palui's bucket in its workspace"
grep -q "database default/testing (main): ok" "$work/check.log" || die "the restore check didn't reach the default workspace"
want "$(api "$token" GET /v1/databases | names)" "palui" "the workspace token on the restored engine"
want "$(status "$grants" GET /v1/databases)" "401" "the revoked grants token on the restored engine"
ok "a backup of the upgraded instance restores with both workspaces, their data and their tokens"

log "$steps steps passed"
