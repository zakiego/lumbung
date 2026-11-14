#!/usr/bin/env python3
"""Check a restored engine against the backup it came from (ops/restore-drill.sh).

    python3 ops/restore-check.py <engine url> <token file> <manifest.json>

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
import sys
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

# --- databases -------------------------------------------------------------
dbs = [d for page in pages("/v1/databases") for d in page.get("data") or []]
files = sum(1 for f in manifest["files"] if f["path"].startswith("dbs/"))
log(f"{len(dbs)} databases restored, {files} branch files in the backup")
if len(dbs) != files:
    log("WARNING: the counts differ (a branch skipped while the backup ran?)")
for db in dbs:
    res = call("POST", f"/v1/databases/{urllib.parse.quote(db['id'])}/query",
               {"sql": "PRAGMA integrity_check", "read_only": True})
    vals = [v.get("value") if isinstance(v, dict) else v for row in res.get("rows", []) for v in row]
    ok = vals == ["ok"]
    failures += not ok
    log(f"database {db['name']} ({db.get('default_branch')}): {'ok' if ok else vals}")

# --- buckets ---------------------------------------------------------------
object_files = {}
for f in manifest["files"]:
    parts = f["path"].split("/")
    if parts[0] == "objects" and len(parts) == 4:
        object_files[parts[1]] = object_files.get(parts[1], 0) + 1
if not manifest.get("objects", True):
    log("the backup was made with --skip-objects: objects aren't checked")

buckets = [b for page in pages("/v1/buckets") for b in page.get("data") or []]
log(f"{len(buckets)} buckets restored, {sum(object_files.values())} object files in the backup")
checked = 0
checked_bytes = 0
for b in buckets:
    objects = [o for page in pages(f"/v1/buckets/{urllib.parse.quote(b['id'])}/objects") for o in page.get("data") or []]
    want = object_files.get(b["id"], 0)
    line = f"bucket {b['name']}: {len(objects)} objects"
    if manifest.get("objects", True) and len(objects) != want:
        line += f", but the backup holds {want} files for it"
        failures += 1
    if b.get("object_count") != len(objects):
        line += f", but the bucket counts {b.get('object_count')}"
        failures += 1
    bad = 0
    for o in objects:
        if checked >= max_checks or checked_bytes + o["size"] > max_bytes:
            break
        q = urllib.parse.urlencode({"key": o["key"]})
        h = hashlib.md5()
        n = 0
        with request("GET", f"/v1/buckets/{urllib.parse.quote(b['id'])}/object?{q}") as res:
            while chunk := res.read(1 << 20):
                h.update(chunk)
                n += len(chunk)
        checked += 1
        checked_bytes += n
        if n != o["size"] or ("-" not in o["etag"] and h.hexdigest() != o["etag"]):
            bad += 1
            log(f"  {o['key']}: {n} bytes, md5 {h.hexdigest()}; listed {o['size']} bytes, etag {o['etag']}")
    failures += bad
    log(line + (f", {bad} failed their check" if bad else ", ok"))
log(f"downloaded and checked {checked} objects ({checked_bytes} bytes)")

if not dbs and not buckets and os.environ.get("DRILL_ALLOW_EMPTY") != "1":
    sys.exit("[restore-drill] the backup has no databases and no buckets, so this proves nothing (DRILL_ALLOW_EMPTY=1 to accept)")
if failures:
    sys.exit(f"[restore-drill] {failures} check(s) failed")
