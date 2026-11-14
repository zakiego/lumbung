package api_test

// The rest of PLAN 17.9: a deleted workspace, a moved database or bucket,
// the service token's narrowed access, and the MCP tools for a workspace
// that manages its own things. The world is the one in workspace_test.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 17.9 row 10: a deleted workspace. Its tokens, access keys, presigned
// URLs and public links stop at once and answer like ones that don't
// exist, on every door. Restoring brings all of it back.
func TestWorkspaceIsolationDeleted(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	const pOne = "/v1/workspaces/{ws}"
	// A public bucket and a presigned URL in B, made while it was alive.
	w.ok(w.do(b.admin.Token, "PATCH", "/v1/buckets/{bucket}", "/v1/buckets/"+b.onlyBucket, map[string]any{"public_read": true}), 200)
	public := w.http.URL + "/v1/workspaces/globex/s3/" + b.onlyBucket + "/doc.txt"
	presigned := w.presign(b.admin, "GET", "/v1/s3/files/doc.txt", time.Hour, time.Now())
	w.s3OK(w.fetch("GET", public, nil), 200)
	w.s3OK(w.fetch("GET", presigned, nil), 200)
	// An open transaction on a libSQL stream.
	hb := w.libsql(b.admin.Token, "notes", "main")
	hb.ok(hb.pipeline(exec("BEGIN"))[0])
	hb.ok(hb.pipeline(exec("INSERT INTO t VALUES ('in flight')"))[0])

	// Only an instance admin deletes a workspace, and never the default one.
	for _, tok := range []string{b.admin.Token, a.admin.Token, b.read.Token} {
		w.fails(w.do(tok, "DELETE", pOne, "/v1/workspaces/globex", nil), 403, "forbidden")
	}
	for _, ref := range []string{"default", w.defaultWS()} {
		w.fails(w.do(w.admin, "DELETE", pOne, "/v1/workspaces/"+ref, nil), 403, "forbidden")
	}
	gone := w.ok(w.do(w.admin, "DELETE", pOne, "/v1/workspaces/globex", nil), 200).json(t)
	if gone["deleted_at"] == nil || gone["purge_after"] == nil || gone["id"] != b.id {
		t.Fatalf("deleted workspace: %v", gone)
	}

	// Every credential of B, through every door. A token that never existed
	// gets the same answer.
	never := "lmb_aaaaaaaaaaaaaaaaaaaa_" + strings.Repeat("a", 40)
	none := w.do(never, "GET", pDBs, "/v1/databases", nil)
	for who, k := range map[string]key{"admin": b.admin, "write": b.write, "read": b.read, "bucket": b.bucket, "grants": b.grants,
		"database admin": {Token: b.dbAdmin}, "branch": {Token: b.branch}} {
		for _, o := range []restOp{
			{"GET", pDBs, "/v1/databases", nil}, {"POST", pQuery, "/v1/databases/notes/query", map[string]any{"sql": "SELECT * FROM t"}},
			{"GET", "/v1/buckets", "/v1/buckets", nil}, {"GET", "", "/v1/buckets/files/object?key=doc.txt", nil},
			{"GET", "/v1/workspace", "/v1/workspace", nil}, {"GET", pTokens, "/v1/tokens", nil},
			{"GET", "", "/v1/workspaces/globex/databases", nil}, {"GET", "", "/v1/workspaces/" + b.id + "/databases", nil},
			{"POST", pOne + "/restore", "/v1/workspaces/globex/restore", map[string]any{}},
		} {
			got := w.do(k.Token, o.method, o.template, o.path, o.body)
			if got.status != 401 || answer(t, got) != answer(t, none) {
				t.Fatalf("%s token of a deleted workspace: %s %s: %d %s", who, o.method, o.path, got.status, got.body)
			}
		}
		if r := w.one(k.Token, "notes", "main", "SELECT * FROM t"); errCode(t, r) != "unauthorized" {
			t.Fatalf("%s token over libSQL: %v", who, r)
		}
		if _, err := w.mcpAt("/mcp", k.Token); err == nil {
			t.Fatalf("%s token connected to MCP", who)
		}
		if k.AKID != "" {
			unknown := key{AKID: "LMBAAAAAAAAAAAAAAAAAAAA", Secret: strings.Repeat("s", 40)}
			for _, path := range []string{"/v1/s3/files/doc.txt", "/v1/s3", "/v1/workspaces/globex/s3/files/doc.txt"} {
				got, want := w.s3(k, "GET", path, nil), w.s3(unknown, "GET", path, nil)
				if got.status != 403 || s3Code(got) != "InvalidAccessKeyId" || s3Code(got) != s3Code(want) {
					t.Fatalf("%s key of a deleted workspace on %s: %d %s", who, path, got.status, got.body)
				}
			}
		}
	}
	w.s3Fails(w.fetch("GET", presigned, nil), 403, "InvalidAccessKeyId")
	w.s3Fails(w.fetch("GET", public, nil), 403, "AccessDenied")
	w.s3Fails(w.fetch("GET", w.http.URL+"/v1/workspaces/"+b.id+"/s3/"+b.onlyBucket+"/doc.txt", nil), 403, "AccessDenied")
	// The open stream and its transaction are gone.
	if r := hb.pipeline(exec("SELECT 1"))[0]; errCode(t, r) != "unauthorized" {
		t.Fatalf("a stream of a deleted workspace: %v", r)
	}
	// The instance admin can't reach into it either: it doesn't resolve.
	for _, ref := range []string{"globex", b.id} {
		w.fails(w.do(w.admin, "GET", "", "/v1/workspaces/"+ref+"/databases", nil), 404, "workspace_not_found")
		w.fails(w.do(w.admin, "GET", pOne, "/v1/workspaces/"+ref, nil), 404, "workspace_not_found")
		w.fails(w.do(w.admin, "DELETE", pOne, "/v1/workspaces/"+ref, nil), 404, "workspace_not_found")
	}
	// It is in the trash list, which only an instance admin reads, and out
	// of the live one.
	w.fails(w.do(a.admin.Token, "GET", "/v1/workspaces", "/v1/workspaces?trashed=true", nil), 403, "forbidden")
	trash := w.ok(w.do(w.admin, "GET", "/v1/workspaces", "/v1/workspaces?trashed=true", nil), 200).json(t)["data"].([]any)
	if len(trash) != 1 || trash[0].(map[string]any)["id"] != b.id {
		t.Fatalf("trash: %v", trash)
	}
	if got := len(w.ok(w.do(w.admin, "GET", "/v1/workspaces", "/v1/workspaces", nil), 200).json(t)["data"].([]any)); got != 2 {
		t.Fatalf("%d live workspaces", got)
	}
	// Workspace A never noticed, and the name is free.
	w.untouched(a)
	w.ok(w.do(w.admin, "POST", "/v1/workspaces", "/v1/workspaces", map[string]any{"name": "globex"}), 201)

	// Restore: the old name is taken now, so it needs a new one. Everything
	// comes back, tokens and keys included; the in-flight write was rolled
	// back with its transaction.
	w.fails(w.do(a.admin.Token, "POST", pOne+"/restore", "/v1/workspaces/"+b.id+"/restore", map[string]any{}), 403, "forbidden")
	w.fails(w.do(w.admin, "POST", pOne+"/restore", "/v1/workspaces/"+b.id+"/restore", map[string]any{}), 409, "name_taken")
	back := w.ok(w.do(w.admin, "POST", pOne+"/restore", "/v1/workspaces/"+b.id+"/restore", map[string]any{"name": "globex-back"}), 200).json(t)
	if back["id"] != b.id || back["name"] != "globex-back" || back["deleted_at"] != nil {
		t.Fatalf("restored: %v", back)
	}
	w.ok(w.do(b.admin.Token, "PATCH", "/v1/buckets/{bucket}", "/v1/buckets/"+b.onlyBucket, map[string]any{"public_read": false}), 200)
	w.untouched(b)
	w.s3OK(w.fetch("GET", presigned, nil), 200)
	if r := w.libsql(b.dbAdmin, "notes", "main").run("SELECT count(*) FROM t"); fmt.Sprint(rows(t, r)) != "[[map[type:integer value:1]]]" {
		t.Fatalf("rows after restore: %v", rows(t, r))
	}
	if _, msg := call(t, w.mcp(b.grants.Token), "query", map[string]any{"database": "notes", "sql": "SELECT v FROM t"}); msg != "" {
		t.Fatal(msg)
	}
	usage := w.ok(w.do(b.admin.Token, "GET", "/v1/workspace", "/v1/workspace", nil), 200).json(t)["usage"].(map[string]any)
	if usage["databases"] != 2.0 || usage["database_bytes"].(float64) <= 0 {
		t.Fatalf("usage after restore: %v", usage)
	}
}

// 17.9 row 11: moving a database or a bucket to another workspace. Only an
// instance admin moves. Every token that reached it is revoked, the old
// workspace no longer finds it, and the new one does.
func TestWorkspaceIsolationMoved(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	const pMoveDB, pMoveBucket = "/v1/databases/{db}/move", "/v1/buckets/{bucket}/move"
	to := func(ws string) map[string]any { return map[string]any{"workspace": ws} }
	// Closed to a workspace token.
	for _, tok := range []string{a.admin.Token, a.dbAdmin, b.admin.Token} {
		w.fails(w.do(tok, "POST", pMoveDB, "/v1/databases/notes/move", to("globex")), 403, "forbidden")
		w.fails(w.do(tok, "POST", pMoveBucket, "/v1/buckets/files/move", to("globex")), 403, "forbidden")
		w.fails(w.do(tok, "POST", pMoveDB, "/v1/databases/notes/move", to("nope")), 403, "forbidden")
	}
	// B already has a notes and a files: the names are taken there.
	w.fails(w.do(w.admin, "POST", "", "/v1/workspaces/acme/databases/notes/move", to("globex")), 409, "name_taken")
	w.fails(w.do(w.admin, "POST", "", "/v1/workspaces/acme/buckets/files/move", to("globex")), 409, "name_taken")
	w.fails(w.do(w.admin, "POST", "", "/v1/workspaces/acme/databases/notes/move", to("nope")), 404, "workspace_not_found")
	w.fails(w.do(w.admin, "POST", "", "/v1/workspaces/acme/databases/notes/move", to("acme")), 400, "bad_request")
	w.fails(w.do(w.admin, "POST", "", "/v1/workspaces/acme/databases/nope/move", to("globex")), 404, "database_not_found")
	// A refused move changes nothing: A's tokens still work.
	w.ok(w.sql(a.dbAdmin, "notes", "SELECT 1"), 200)
	w.untouched(a)

	// Rename them out of the way, then move A's notes and files to B.
	w.ok(w.do(a.admin.Token, "PATCH", pDB, "/v1/databases/notes", map[string]any{"name": "moved-notes"}), 200)
	w.ok(w.do(a.admin.Token, "PATCH", "/v1/buckets/{bucket}", "/v1/buckets/files", map[string]any{"name": "moved-files"}), 200)
	hOld := w.libsql(a.dbAdmin, "moved-notes", "main")
	hOld.ok(hOld.pipeline(exec("BEGIN"))[0])
	presigned := w.presign(a.bucket, "GET", "/v1/s3/moved-files/doc.txt", time.Hour, time.Now())
	w.s3OK(w.fetch("GET", presigned, nil), 200)

	movedDB := w.ok(w.do(w.admin, "POST", "", "/v1/workspaces/acme/databases/moved-notes/move", to("globex")), 200).json(t)
	movedBucket := w.ok(w.do(w.admin, "POST", "", "/v1/workspaces/acme/buckets/moved-files/move", to(b.id)), 200).json(t)
	if movedDB["workspace_id"] != b.id || movedDB["id"] != a.notesID || movedDB["branch_count"] != 2.0 || movedBucket["workspace_id"] != b.id || movedBucket["id"] != a.filesID {
		t.Fatalf("moved: %v %v", movedDB, movedBucket)
	}

	// Every token that reached them is revoked, whole: the database token,
	// the branch token, the bucket key and the grants token.
	for who, tok := range map[string]string{"database admin": a.dbAdmin, "branch": a.branch, "bucket": a.bucket.Token, "grants": a.grants.Token} {
		if r := w.do(tok, "GET", pDBs, "/v1/databases", nil); r.status != 401 {
			t.Fatalf("%s token still works after the move: %d %s", who, r.status, r.body)
		}
	}
	w.s3Fails(w.s3(a.bucket, "GET", "/v1/s3/moved-files/doc.txt", nil), 403, "InvalidAccessKeyId")
	w.s3Fails(w.fetch("GET", presigned, nil), 403, "InvalidAccessKeyId")
	if r := hOld.pipeline(exec("SELECT 1"))[0]; errCode(t, r) != "unauthorized" {
		t.Fatalf("a stream opened by a revoked token: %v", r)
	}
	// The old workspace's own tokens no longer find them, by name or id.
	for _, tok := range []string{a.admin.Token, a.read.Token} {
		for _, ref := range []string{"moved-notes", a.notesID} {
			w.fails(w.sql(tok, ref, "SELECT 1"), 404, "database_not_found")
		}
		for _, ref := range []string{"moved-files", a.filesID} {
			w.fails(w.do(tok, "GET", "/v1/buckets/{bucket}", "/v1/buckets/"+ref, nil), 404, "bucket_not_found")
		}
		if r := w.one(tok, a.notesID, "main", "SELECT 1"); errCode(t, r) != "database_not_found" {
			t.Fatalf("libSQL on the moved database: %v", r)
		}
	}
	w.s3Fails(w.s3(a.admin, "GET", "/v1/s3/moved-files/doc.txt", nil), 404, "NoSuchBucket")
	if got := len(w.ok(w.do(a.admin.Token, "GET", pDBs, "/v1/databases", nil), 200).json(t)["data"].([]any)); got != 1 {
		t.Fatalf("workspace A lists %d databases", got)
	}
	// The new workspace has them, with their data, branches and snapshots.
	if r := w.ok(w.sql(b.read.Token, "moved-notes", "SELECT v FROM t"), 200); !bytes.Contains(r.body, []byte(a.secret)) {
		t.Fatalf("moved rows: %s", r.body)
	}
	if got := len(w.ok(w.do(b.admin.Token, "GET", pBranches, "/v1/databases/moved-notes/branches", nil), 200).json(t)["data"].([]any)); got != 2 {
		t.Fatalf("moved database has %d branches", got)
	}
	if r := w.s3OK(w.s3(b.admin, "GET", "/v1/s3/moved-files/doc.txt", nil), 200); string(r.body) != a.secret {
		t.Fatalf("moved object: %s", r.body)
	}
	// A's unfinished upload moved with its bucket and is B's now.
	w.s3OK(w.s3(b.admin, "GET", "/v1/s3/moved-files/big?uploadId="+a.uploadID, nil), 200)
	// Usage follows.
	ua := w.ok(w.do(a.admin.Token, "GET", "/v1/workspace", "/v1/workspace", nil), 200).json(t)["usage"].(map[string]any)
	ub := w.ok(w.do(b.admin.Token, "GET", "/v1/workspace", "/v1/workspace", nil), 200).json(t)["usage"].(map[string]any)
	if ua["databases"] != 1.0 || ua["buckets"] != 1.0 || ub["databases"] != 3.0 || ub["buckets"] != 3.0 || ub["database_bytes"].(float64) <= ua["database_bytes"].(float64) {
		t.Fatalf("usage after the move: A %v, B %v", ua, ub)
	}
	// The moves are in the instance log, not in either workspace's.
	var moves int
	for _, raw := range w.ok(w.do(w.admin, "GET", "", "/v1/instance/audit?limit=200", nil), 200).json(t)["data"].([]any) {
		if e := raw.(map[string]any); (e["action"] == "database.move" || e["action"] == "bucket.move") && e["result"] == "ok" {
			if e["workspace_id"] != "" {
				t.Fatalf("a move was logged in a workspace: %v", e)
			}
			moves++
		}
	}
	if moves != 2 {
		t.Fatalf("%d moves in the instance log", moves)
	}

	// The target's own limits hold for a move too.
	w.ok(w.do(w.admin, "PATCH", "/v1/workspaces/{ws}", "/v1/workspaces/acme", map[string]any{"max_databases": 1, "max_buckets": 1}), 200)
	w.fails(w.do(w.admin, "POST", "", "/v1/workspaces/globex/databases/"+b.only+"/move", to("acme")), 409, "limit_reached")
	w.fails(w.do(w.admin, "POST", "", "/v1/workspaces/globex/buckets/"+b.onlyBucket+"/move", to("acme")), 409, "limit_reached")
	w.ok(w.do(w.admin, "PATCH", "/v1/workspaces/{ws}", "/v1/workspaces/acme", map[string]any{"max_databases": 0, "max_buckets": 0, "max_database_bytes": 1, "max_object_bytes": 1}), 200)
	w.fails(w.do(w.admin, "POST", "", "/v1/workspaces/globex/databases/"+b.only+"/move", to("acme")), 507, "workspace_quota_exceeded")
	w.fails(w.do(w.admin, "POST", "", "/v1/workspaces/globex/buckets/"+b.onlyBucket+"/move", to("acme")), 507, "workspace_quota_exceeded")
	w.ok(w.sql(b.admin.Token, b.only, "SELECT 1"), 200)
}

// 17.9 row 14: X-Lumbung-Actor-Access. With the service token it lowers
// the request to a workspace principal at that level, so the engine
// enforces a dashboard user's role. Any other token's header is ignored,
// and it never raises anything.
func TestWorkspaceActorAccess(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	const h = "X-Lumbung-Actor-Access"
	as := func(level string) []string {
		return []string{h, level, "X-Lumbung-Actor", "dashboard:member@example.com"}
	}
	base := "/v1/workspaces/acme"

	// read: reads, and nothing else.
	w.ok(w.do(w.admin, "GET", "", base+"/databases", nil, as("read")...), 200)
	w.ok(w.do(w.admin, "POST", "", base+"/databases/notes/query", map[string]any{"sql": "SELECT * FROM t"}, as("read")...), 200)
	w.fails(w.do(w.admin, "POST", "", base+"/databases/notes/query", map[string]any{"sql": "INSERT INTO t VALUES ('x')"}, as("read")...), 403, "read_only")
	w.fails(w.do(w.admin, "PUT", "", base+"/buckets/files/object?key=x", []byte("x"), as("read")...), 403, "forbidden")
	w.fails(w.do(w.admin, "POST", "", base+"/databases", map[string]any{"name": "x"}, as("read")...), 403, "forbidden")
	// write: data, not management.
	w.ok(w.do(w.admin, "PUT", "", base+"/buckets/files/object?key=w", []byte("w"), as("write")...), 200)
	for _, o := range []restOp{
		{"POST", "", base + "/databases", map[string]any{"name": "x"}}, {"DELETE", "", base + "/databases/notes", nil},
		{"POST", "", base + "/buckets", map[string]any{"name": "xxx"}}, {"GET", "", base + "/tokens", nil},
		{"POST", "", base + "/tokens", map[string]any{"name": "x", "access": "read"}}, {"GET", "", base + "/audit", nil},
	} {
		w.fails(w.do(w.admin, o.method, "", o.path, o.body, as("write")...), 403, "forbidden")
	}
	// admin: the workspace, and not the instance. The audit log names the user.
	w.ok(w.do(w.admin, "POST", "", base+"/databases", map[string]any{"name": "made-by-member"}, as("admin")...), 201)
	w.ok(w.do(w.admin, "POST", "", base+"/tokens", map[string]any{"name": "x", "access": "read"}, as("admin")...), 201)
	newest := w.ok(w.do(w.admin, "GET", "", base+"/audit?limit=1", nil, as("admin")...), 200).json(t)["data"].([]any)[0].(map[string]any)
	if newest["actor"] != "dashboard:member@example.com" || newest["workspace_id"] != a.id {
		t.Fatalf("audit entry: %v", newest)
	}
	for _, level := range []string{"read", "write", "admin"} {
		for _, o := range []restOp{
			{"POST", "/v1/workspaces", "/v1/workspaces", map[string]any{"name": "x"}},
			{"PATCH", "/v1/workspaces/{ws}", "/v1/workspaces/acme", map[string]any{"max_databases": 0}},
			{"DELETE", "/v1/workspaces/{ws}", "/v1/workspaces/acme", nil},
			{"GET", "/v1/instance/tokens", "/v1/instance/tokens", nil},
			{"POST", "/v1/instance/tokens", "/v1/instance/tokens", map[string]any{"name": "x", "access": "admin"}},
			{"GET", "/v1/instance/audit", "/v1/instance/audit", nil},
			{"GET", "/v1/workspaces", "/v1/workspaces?trashed=true", nil},
			{"POST", "", base + "/databases/notes/move", map[string]any{"workspace": "globex"}},
		} {
			w.fails(w.do(w.admin, o.method, o.template, o.path, o.body, as(level)...), 403, "forbidden")
		}
		// It acts in the one workspace the URL names. The others don't
		// exist for it: its list is a list of one, and B's things aren't
		// found.
		ws := w.ok(w.do(w.admin, "GET", "", base+"/databases", nil, as(level)...), 200)
		if bytes.Contains(ws.body, []byte(b.notesID)) || bytes.Contains(ws.body, []byte(b.only)) {
			t.Fatalf("narrowed to %s in A sees B: %s", level, ws.body)
		}
		list := w.ok(w.do(w.admin, "GET", "/v1/workspaces", "/v1/workspaces", nil, as(level)...), 200).json(t)["data"].([]any)
		if len(list) != 1 || list[0].(map[string]any)["id"] != w.defaultWS() {
			t.Fatalf("narrowed on a short URL lists %v", list)
		}
		w.fails(w.do(w.admin, "GET", "", base+"/databases/"+b.notesID, nil, as(level)...), 404, "database_not_found")
		w.fails(w.do(w.admin, "GET", "", base+"/buckets/"+b.onlyBucket, nil, as(level)...), 404, "bucket_not_found")
		w.fails(w.do(w.admin, "GET", "/v1/workspaces/{ws}", "/v1/workspaces/globex", nil, as(level)...), 404, "workspace_not_found")
	}
	// The other doors.
	post := func(level, sql string) (int, string) {
		body, _ := json.Marshal(map[string]any{"baton": nil, "requests": []any{exec(sql), closeReq}})
		req, _ := http.NewRequest("POST", w.http.URL+base+"/libsql/notes/main/v2/pipeline", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+w.admin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(h, level)
		r := w.send(req)
		return r.status, string(r.body)
	}
	if status, body := post("read", "INSERT INTO t VALUES ('x')"); status != 200 || !strings.Contains(body, "read_only") {
		t.Fatalf("libSQL write as read: %d %s", status, body)
	}
	if status, body := post("write", "INSERT INTO t VALUES ('by a member')"); status != 200 || strings.Contains(body, `"error"`) {
		t.Fatalf("libSQL write as write: %d %s", status, body)
	}
	if r := w.fetch("PUT", w.http.URL+base+"/s3/files/s3-read", []byte("x"), "Authorization", "Bearer "+w.admin, h, "read"); r.status != 403 || s3Code(r) != "AccessDenied" {
		t.Fatalf("S3 write as read: %d %s", r.status, r.body)
	}
	if r := w.fetch("PUT", w.http.URL+base+"/s3/made-by-s3", nil, "Authorization", "Bearer "+w.admin, h, "write"); r.status != 403 {
		t.Fatalf("S3 create bucket as write: %d %s", r.status, r.body)
	}
	if r := w.fetch("PUT", w.http.URL+base+"/s3/files/s3-write", []byte("x"), "Authorization", "Bearer "+w.admin, h, "write"); r.status != 200 {
		t.Fatalf("S3 write as write: %d %s", r.status, r.body)
	}

	// A value that isn't a level is refused, not treated as no header.
	for _, bad := range []string{"owner", "ADMIN", "admin,read", "*", "instance"} {
		w.fails(w.do(w.admin, "GET", "", base+"/databases", nil, h, bad), 400, "bad_request")
	}
	// On any other token the header does nothing, in either direction.
	w.fails(w.do(a.read.Token, "POST", pDBs, "/v1/databases", map[string]any{"name": "x"}, h, "admin"), 403, "forbidden")
	w.fails(w.do(a.read.Token, "POST", pQuery, "/v1/databases/notes/query", map[string]any{"sql": "INSERT INTO t VALUES (1)"}, h, "write"), 403, "read_only")
	w.ok(w.do(a.admin.Token, "POST", pDBs, "/v1/databases", map[string]any{"name": "still-admin"}, h, "read"), 201)
	inst := w.ok(w.do(w.admin, "POST", "/v1/instance/tokens", "/v1/instance/tokens", map[string]any{"name": "op", "access": "admin"}), 201).json(t)["token"].(string)
	w.ok(w.do(inst, "GET", "/v1/instance/tokens", "/v1/instance/tokens", nil, h, "read"), 200)
	w.ok(w.do(inst, "GET", "", base+"/databases", nil, h, "owner"), 200)
	// Without the header the service token is the instance admin it was.
	w.ok(w.do(w.admin, "GET", "/v1/instance/tokens", "/v1/instance/tokens", nil), 200)
}

// The MCP tools for a workspace that manages its own things.
func TestWorkspaceMCPTools(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	s := w.mcp(a.admin.Token)
	ws, msg := call(t, s, "get_workspace", nil)
	if msg != "" || ws["id"] != a.id || ws["name"] != "acme" || ws["limits"].(map[string]any)["max_databases"] != 20.0 || ws["usage"].(map[string]any)["databases"] != 2.0 {
		t.Fatalf("get_workspace: %v %s", ws, msg)
	}
	// A token for part of the workspace gets its name, not its totals.
	part, msg := call(t, w.mcp(a.dbAdmin), "get_workspace", nil)
	if _, has := part["usage"]; msg != "" || has || part["limits"] != nil || part["name"] != "acme" {
		t.Fatalf("get_workspace with a database token: %v %s", part, msg)
	}

	// Delete and restore a database, and a snapshot's round trip.
	out, msg := call(t, s, "delete_database", map[string]any{"database": a.only})
	if msg != "" || out["deleted"] != a.only || out["purge_after"] == "" {
		t.Fatalf("delete_database: %v %s", out, msg)
	}
	w.fails(w.sql(a.admin.Token, a.only, "SELECT 1"), 404, "database_not_found")
	if out, msg = call(t, s, "restore_database", map[string]any{"database": a.only}); msg != "" || out["name"] != a.only {
		t.Fatalf("restore_database: %v %s", out, msg)
	}
	w.ok(w.sql(a.admin.Token, a.only, "SELECT 1"), 200)
	if out, msg = call(t, s, "restore_snapshot", map[string]any{"database": "notes", "snapshot": a.snapshotID, "name": "from-snapshot"}); msg != "" || out["name"] != "from-snapshot" {
		t.Fatalf("restore_snapshot: %v %s", out, msg)
	}
	if out, msg = call(t, s, "delete_snapshot", map[string]any{"database": "notes", "snapshot": a.snapshotID}); msg != "" || out["deleted"] != a.snapshotID {
		t.Fatalf("delete_snapshot: %v %s", out, msg)
	}
	// A bucket must be empty to go.
	if _, msg = call(t, s, "delete_bucket", map[string]any{"bucket": a.onlyBucket}); !strings.Contains(msg, "bucket_not_empty") {
		t.Fatalf("delete_bucket on a bucket with objects: %q", msg)
	}
	if _, msg = call(t, s, "delete_object", map[string]any{"bucket": a.onlyBucket, "key": "doc.txt"}); msg != "" {
		t.Fatal(msg)
	}
	if out, msg = call(t, s, "delete_bucket", map[string]any{"bucket": a.onlyBucket}); msg != "" || out["deleted"] != a.onlyBucket {
		t.Fatalf("delete_bucket: %v %s", out, msg)
	}
	// They are in the workspace's audit log.
	seen := map[any]bool{}
	for _, raw := range w.ok(w.do(a.admin.Token, "GET", "", "/v1/audit?limit=20", nil), 200).json(t)["data"].([]any) {
		seen[raw.(map[string]any)["action"]] = true
	}
	for _, action := range []string{"database.delete", "database.restore", "snapshot.restore", "snapshot.delete", "bucket.delete"} {
		if !seen[action] {
			t.Errorf("%s isn't in the audit log", action)
		}
	}

	// Below admin, and across workspaces.
	for who, tok := range map[string]string{"write": a.write.Token, "read": a.read.Token, "database admin": a.dbAdmin, "grants": a.grants.Token} {
		ms := w.mcp(tok)
		for tool, args := range map[string]map[string]any{
			"delete_database":  {"database": "notes"},
			"restore_database": {"database": "notes"},
			"delete_bucket":    {"bucket": "files"},
		} {
			// A database token doesn't see the bucket at all.
			if _, msg := call(t, ms, tool, args); !strings.Contains(msg, "forbidden") && !(who == "database admin" && strings.Contains(msg, "bucket_not_found")) {
				t.Errorf("%s token: %s: %q", who, tool, msg)
			}
		}
	}
	if _, msg := call(t, w.mcp(a.read.Token), "restore_snapshot", map[string]any{"database": "notes", "snapshot": b.snapshotID, "name": "x"}); !strings.Contains(msg, "forbidden") {
		t.Fatalf("restore_snapshot with a read token: %q", msg)
	}
	for tool, args := range map[string]map[string]any{
		"delete_database":  {"database": b.only},
		"restore_database": {"database": b.only},
		"delete_bucket":    {"bucket": b.onlyBucket},
		"delete_snapshot":  {"database": b.notesID, "snapshot": b.snapshotID},
		"restore_snapshot": {"database": b.notesID, "snapshot": b.snapshotID, "name": "x"},
	} {
		if _, msg := call(t, s, tool, args); !strings.Contains(msg, "_not_found") {
			t.Errorf("%s on workspace B: %q", tool, msg)
		}
	}
	// A's own database with a snapshot of B's.
	for tool, args := range map[string]map[string]any{
		"delete_snapshot":  {"database": "notes", "snapshot": b.snapshotID},
		"restore_snapshot": {"database": "notes", "snapshot": b.snapshotID, "name": "x"},
	} {
		if _, msg := call(t, s, tool, args); !strings.Contains(msg, "snapshot_not_found") {
			t.Errorf("%s with B's snapshot id: %q", tool, msg)
		}
	}
	w.untouched(b)
}
