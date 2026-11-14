#!/usr/bin/env python3
"""Check a restored engine against the backup it came from (ops/restore-drill.sh).

    python3 ops/restore-check.py <engine url> <token file> <manifest.json>

Nothing missing: the backup's own meta.db (next to manifest.json) says which
workspaces, databases and buckets were live when it was taken. Each one
must be listed by the restored engine, in the workspace it was in. One that
didn't come back fails the drill, whatever else passes.

Workspaces: every workspace is walked, by id, so a database or a bucket
that sits outside the default workspace is checked like any other. What is
in a workspace in the trash is not checked: its files are in the backup,
and nothing lists them until the workspace is restored. They are counted.
Against an engine from before workspaces, the short URLs are walked instead.

Databases: every database is listed and its default branch passes
PRAGMA integrity_check through the API.

Buckets: every bucket is listed, every object in it is listed, the object
count matches the object files in the backup, and objects are downloaded
and checked: the size always, and the MD5 against the ETag for objects
uploaded in one piece (a multipart ETag is the MD5 of the parts' MD5s, so
only its size is checked). At most DRILL_MAX_OBJECT_CHECKS objects (default
500) and DRILL_MAX_OBJECT_BYTES bytes (default 512 MiB) are downloaded, so
the drill stays bounded on a large instance; every object is still listed.

A backup with no databases and no buckets fails (it proves nothing) unless
DRILL_ALLOW_EMPTY=1. Exits non-zero on any failure.
"""
import hashlib
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.parse
import urllib.request

base, token_file, manifest_file = sys.argv[1:4]
token = open(token_file).read().strip()
max_checks = int(os.environ.get("DRILL_MAX_OBJECT_CHECKS", "500"))
max_bytes = int(os.environ.get("DRILL_MAX_OBJECT_BYTES", str(512 * 1024 * 1024)))


def request(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base + path, data=data, method=method)
    req.add_header("Authorization", f"Bearer {token}")
    if data is not None:
        req.add_header("Content-Type", "application/json")
    return urllib.request.urlopen(req, timeout=120)


def call(method, path, body=None):
    with request(method, path, body) as res:
        return json.load(res)


def pages(path, extra=None):
    cursor = ""
    while True:
        q = {"limit": "200" if "/objects" not in path else "1000", **(extra or {})}
        if cursor:
            q["cursor"] = cursor
        page = call("GET", path + "?" + urllib.parse.urlencode(q))
        yield page
        cursor = page.get("next_cursor") or ""
        if not cursor:
            return


def log(msg):
    print(f"[restore-drill] {msg}", flush=True)


manifest = json.load(open(manifest_file))
failures = 0

# --- workspaces --------------------------------------------------------------
# An engine from before workspaces has no /v1/workspaces: it is one unnamed
# workspace, reached on the short URLs.
LEGACY = {"id": "", "name": "(no workspaces)", "default": True}
try:
    workspaces = [w for page in pages("/v1/workspaces") for w in page.get("data") or []]
    trashed = [w for page in pages("/v1/workspaces", {"trashed": "true"}) for w in page.get("data") or []]
except urllib.error.HTTPError as e:
    if e.code != 404:
        raise
    workspaces, trashed = [LEGACY], []
    log("this engine has no workspaces: checking the short URLs")
if not any(w.get("default") for w in workspaces):
    log("no default workspace among the restored ones")
    failures += 1
log(f"{len(workspaces)} workspaces restored" + (f", {len(trashed)} more in the trash" if trashed else ""))


def inside(w, path):
    return f"/v1/workspaces/{urllib.parse.quote(w['id'])}{path}" if w["id"] else "/v1" + path


def expected():
    """What was live when the backup was taken, from its own meta.db: (workspaces, databases, buckets), each {id: label}."""
    path = os.path.join(os.path.dirname(os.path.abspath(manifest_file)), "meta.db")
    db = sqlite3.connect(f"file:{urllib.parse.quote(path)}?mode=ro", uri=True)
    try:
        tables = {r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type = 'table'")}
        if "workspaces" not in tables:
            dbs = {r[0]: r[1] for r in db.execute("SELECT id, name FROM databases WHERE deleted_at IS NULL")}
            bks = {r[0]: r[1] for r in db.execute("SELECT id, name FROM buckets")} if "buckets" in tables else {}
            return {}, dbs, bks
        wss = {r[0]: r[1] for r in db.execute("SELECT id, name FROM workspaces WHERE deleted_at IS NULL")}
        live = "workspace_id IN (SELECT id FROM workspaces WHERE deleted_at IS NULL)"
        dbs = {r[0]: f"{wss[r[2]]}/{r[1]}" for r in db.execute(f"SELECT id, name, workspace_id FROM databases WHERE deleted_at IS NULL AND {live}")}
        bks = {r[0]: f"{wss[r[2]]}/{r[1]}" for r in db.execute(f"SELECT id, name, workspace_id FROM buckets WHERE {live}")}
        return wss, dbs, bks
    finally:
        db.close()


def missing(kind, want, got):
    """Fails for everything the backup held that the restored engine doesn't list."""
    lost = sorted(label for i, label in want.items() if i not in got)
    for label in lost:
        log(f"MISSING {kind} {label}: it is in the backup and the restored engine doesn't list it")
    return len(lost)


want_ws, want_dbs, want_buckets = expected()
if workspaces != [LEGACY]:
    failures += missing("workspace", want_ws, {w["id"] for w in workspaces})


# --- databases -------------------------------------------------------------
dbs = []
for w in workspaces:
    found = [d for page in pages(inside(w, "/databases")) for d in page.get("data") or []]
    for d in found:
        if d.get("workspace_id") != w["id"]:
            log(f"database {d['name']} is listed in {w['name']} but belongs to {d.get('workspace_id')}")
            failures += 1
    dbs += [(w, d) for d in found]
files = sum(1 for f in manifest["files"] if f["path"].startswith("dbs/"))
log(f"{len(dbs)} databases restored, {files} branch files in the backup")
if len(dbs) != files:
    log("NOTE: the counts differ (more than one branch per database, a workspace in the trash, or a branch skipped while the backup ran)")
failures += missing("database", want_dbs, {d["id"] for _, d in dbs})
for w, db in dbs:
    res = call("POST", inside(w, f"/databases/{urllib.parse.quote(db['id'])}/query"),
               {"sql": "PRAGMA integrity_check", "read_only": True})
    vals = [v.get("value") if isinstance(v, dict) else v for row in res.get("rows", []) for v in row]
    ok = vals == ["ok"]
    failures += not ok
    log(f"database {w['name']}/{db['name']} ({db.get('default_branch')}): {'ok' if ok else vals}")

# --- buckets ---------------------------------------------------------------
object_files = {}
for f in manifest["files"]:
    parts = f["path"].split("/")
    if parts[0] == "objects" and len(parts) == 4:
        object_files[parts[1]] = object_files.get(parts[1], 0) + 1
if not manifest.get("objects", True):
    log("the backup was made with --skip-objects: objects aren't checked")

buckets = [(w, b) for w in workspaces for page in pages(inside(w, "/buckets")) for b in page.get("data") or []]
log(f"{len(buckets)} buckets restored, {sum(object_files.values())} object files in the backup")
failures += missing("bucket", want_buckets, {b["id"] for _, b in buckets})
checked = 0
checked_bytes = 0
for w, b in buckets:
    at = inside(w, f"/buckets/{urllib.parse.quote(b['id'])}")
    objects = [o for page in pages(f"{at}/objects") for o in page.get("data") or []]
    want = object_files.get(b["id"], 0)
    before = failures
    line = f"bucket {w['name']}/{b['name']}: {len(objects)} objects"
    if manifest.get("objects", True) and len(objects) != want:
        line += f", but the backup holds {want} files for it"
        failures += 1
    if b.get("object_count") != len(objects):
        line += f", but the bucket counts {b.get('object_count')}"
        failures += 1
    counts_ok = failures == before
    bad = 0
    for o in objects:
        if checked >= max_checks or checked_bytes + o["size"] > max_bytes:
            break
        q = urllib.parse.urlencode({"key": o["key"]})
        h = hashlib.md5()
        n = 0
        with request("GET", f"{at}/object?{q}") as res:
            while chunk := res.read(1 << 20):
                h.update(chunk)
                n += len(chunk)
        checked += 1
        checked_bytes += n
        if n != o["size"] or ("-" not in o["etag"] and h.hexdigest() != o["etag"]):
            bad += 1
            log(f"  {o['key']}: {n} bytes, md5 {h.hexdigest()}; listed {o['size']} bytes, etag {o['etag']}")
    failures += bad
    log(line + (f", {bad} failed their check" if bad else ", ok" if counts_ok else ""))
log(f"downloaded and checked {checked} objects ({checked_bytes} bytes)")

if not dbs and not buckets and os.environ.get("DRILL_ALLOW_EMPTY") != "1":
    sys.exit("[restore-drill] the backup has no databases and no buckets, so this proves nothing (DRILL_ALLOW_EMPTY=1 to accept)")
if failures:
    sys.exit(f"[restore-drill] {failures} check(s) failed")
