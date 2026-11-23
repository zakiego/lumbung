package api_test

// Workspace isolation (PLAN 17.9, ADR 0008). Each test is a row of that
// table, run through the real handlers for every door it applies to: REST,
// the S3 API, the libSQL endpoint and MCP.
//
// The world is two workspaces, acme and globex, that hold the same things
// under the same names (a database notes, a bucket files) plus one database
// and one bucket whose names exist only on their side. The same names prove
// a token always lands in its own workspace; the unique ones give an
// attacker something to aim at by name.

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

type side struct {
	name, id string
	// Workspace tokens, each with an S3 access key.
	admin, write, read key
	dbAdmin            string // admin of the database notes
	branch             string // write on notes, branch dev only
	bucket             key    // admin of the bucket files
	grants             key    // notes at admin and files at admin

	only, onlyBucket      string // names that exist only in this workspace
	notesID, onlyID       string
	mainID, devID         string
	snapshotID            string
	filesID, onlyBucketID string
	uploadID              string // a multipart upload in progress in files
	secret                string // what its rows and objects hold
}

type wsWorld struct {
	*env
	a, b side
}

// mint creates a token with the given token as the caller, on a short URL.
func (e *env) mint(caller string, body map[string]any) key {
	e.t.Helper()
	if body["name"] == nil {
		body["name"] = "t"
	}
	r := e.ok(e.do(caller, "POST", pTokens, "/v1/tokens", body), 201)
	var k key
	if err := json.Unmarshal(r.body, &k); err != nil || k.Token == "" {
		e.t.Fatalf("token: %s", r.body)
	}
	return k
}

func newWSWorld(t testing.TB, mod func(*config.Config)) *wsWorld {
	t.Helper()
	// These tests send hundreds of requests per token in a moment; the
	// per-token rate limit isn't what they are about.
	e := newEnv(t, func(c *config.Config) {
		c.RateLimit, c.RateBurst = 100000, 100000
		// Nor is the limit on failed logins: a deleted workspace's tokens
		// fail on purpose, many times.
		c.AuthFailuresPerMin = 100000
		if mod != nil {
			mod(c)
		}
	})
	w := &wsWorld{env: e}
	for _, s := range []*side{&w.a, &w.b} {
		s.name = "acme"
		if s == &w.b {
			s.name = "globex"
		}
		s.only, s.onlyBucket, s.secret = "only-"+s.name, "only-"+s.name+"-files", "secret of "+s.name
		ws := e.ok(e.do(e.admin, "POST", "/v1/workspaces", "/v1/workspaces", map[string]any{"name": s.name}), 201).json(t)
		s.id = ws["id"].(string)

		// The instance admin makes the workspace's first token through a
		// qualified URL. Everything after is done by the workspace itself,
		// on short URLs.
		r := e.ok(e.do(e.admin, "POST", "", "/v1/workspaces/"+s.name+"/tokens", map[string]any{"name": "admin", "access": "admin", "access_key": true}), 201)
		if err := json.Unmarshal(r.body, &s.admin); err != nil || s.admin.AKID == "" {
			t.Fatalf("workspace admin: %s", r.body)
		}
		if m := r.json(t); m["scope"] != "workspace" || m["workspace_id"] != s.id {
			t.Fatalf("workspace admin token: %v", m)
		}
		adm := s.admin.Token
		s.write = e.mint(adm, map[string]any{"name": "write", "access": "write", "access_key": true})
		s.read = e.mint(adm, map[string]any{"name": "read", "access": "read", "access_key": true})

		for _, db := range []string{"notes", s.only} {
			m := e.ok(e.do(adm, "POST", pDBs, "/v1/databases", map[string]any{"name": db}), 201).json(t)
			if m["workspace_id"] != s.id {
				t.Fatalf("database made in %v, want %s", m["workspace_id"], s.id)
			}
			if db == "notes" {
				s.notesID, s.mainID = m["id"].(string), m["default_branch_id"].(string)
			} else {
				s.onlyID = m["id"].(string)
			}
			e.ok(e.sql(adm, db, "CREATE TABLE t(v)"), 200)
			e.ok(e.sql(adm, db, "INSERT INTO t VALUES (?)", s.secret), 200)
		}
		s.devID = e.ok(e.do(adm, "POST", pBranches, "/v1/databases/notes/branches", map[string]any{"name": "dev"}), 201).json(t)["id"].(string)
		s.snapshotID = e.ok(e.do(adm, "POST", "/v1/databases/{db}/branches/{branch}/snapshots", "/v1/databases/notes/branches/main/snapshots",
			map[string]any{"label": "s"}), 201).json(t)["id"].(string)

		for _, b := range []string{"files", s.onlyBucket} {
			m := e.ok(e.do(adm, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": b}), 201).json(t)
			if m["workspace_id"] != s.id {
				t.Fatalf("bucket made in %v, want %s", m["workspace_id"], s.id)
			}
			if b == "files" {
				s.filesID = m["id"].(string)
			} else {
				s.onlyBucketID = m["id"].(string)
			}
			e.ok(e.putObject(adm, b, "doc.txt", s.secret), 200)
		}
		var up struct {
			UploadID string `xml:"UploadId"`
		}
		_ = xml.Unmarshal(e.s3OK(e.s3(s.admin, "POST", "/v1/s3/files/big?uploads", nil), 200).body, &up)
		if s.uploadID = up.UploadID; s.uploadID == "" {
			t.Fatal("no upload id")
		}

		s.dbAdmin = e.mint(adm, map[string]any{"name": "db", "database": "notes", "access": "admin"}).Token
		s.branch = e.mint(adm, map[string]any{"name": "branch", "database": "notes", "branch": "dev", "access": "write"}).Token
		s.bucket = e.mint(adm, map[string]any{"name": "bucket", "bucket": "files", "access": "admin", "access_key": true})
		s.grants = e.mint(adm, map[string]any{"name": "grants", "access_key": true, "grants": []map[string]any{
			{"database": "notes", "access": "admin"}, {"bucket": "files", "access": "admin"},
		}})
	}
	return w
}

// answer is what a caller can tell apart: the status, the error code and
// the message with the names it sent taken out.
func answer(t testing.TB, r resp, names ...string) string {
	t.Helper()
	title := ""
	if len(r.body) > 0 && strings.Contains(r.header.Get("Content-Type"), "json") {
		title, _ = r.json(t)["title"].(string)
	}
	for _, n := range names {
		title = strings.ReplaceAll(title, n, "<name>")
	}
	return fmt.Sprintf("%d %s %s", r.status, r.code(t), title)
}

type restOp struct {
	method, template, path string
	body                   any
}

// databaseOps is every REST operation on a database, its branches and its
// snapshots.
func databaseOps(db, branch, snapshot string) []restOp {
	d, b := "/v1/databases/"+db, "/v1/databases/"+db+"/branches/"+branch
	const tb = "/v1/databases/{db}/branches/{branch}"
	q := map[string]any{"sql": "SELECT * FROM t"}
	return []restOp{
		{"GET", pDB, d, nil},
		{"PATCH", pDB, d, map[string]any{"name": "renamed"}},
		{"DELETE", pDB, d, nil},
		{"POST", "/v1/databases/{db}/restore", d + "/restore", map[string]any{}},
		{"POST", pQuery, d + "/query", q},
		{"POST", pQuery, d + "/query", map[string]any{"sql": "INSERT INTO t VALUES ('pwned')"}},
		{"POST", pBatch, d + "/batch", map[string]any{"statements": []any{q}}},
		{"GET", pBranches, d + "/branches", nil},
		{"POST", pBranches, d + "/branches", map[string]any{"name": "evil"}},
		{"GET", tb, b, nil},
		{"PATCH", tb, b, map[string]any{"protected": true}},
		{"DELETE", tb, b, nil},
		{"POST", tb + "/reset", b + "/reset", nil},
		{"POST", tb + "/promote", b + "/promote", nil},
		{"GET", tb + "/diff", b + "/diff?against=main", nil},
		{"GET", tb + "/schema", b + "/schema", nil},
		{"POST", tb + "/query", b + "/query", q},
		{"POST", tb + "/batch", b + "/batch", map[string]any{"statements": []any{q}}},
		{"GET", tb + "/stats", b + "/stats", nil},
		{"GET", tb + "/snapshots", b + "/snapshots", nil},
		{"POST", tb + "/snapshots", b + "/snapshots", map[string]any{"label": "evil"}},
		{"GET", tb + "/snapshot-schedule", b + "/snapshot-schedule", nil},
		{"PUT", tb + "/snapshot-schedule", b + "/snapshot-schedule", map[string]any{"every_minutes": 60}},
		{"DELETE", tb + "/snapshot-schedule", b + "/snapshot-schedule", nil},
		{"GET", "", b + "/export", nil},
		{"POST", "", b + "/import?format=sql", []byte("CREATE TABLE evil(x);")},
		{"POST", "/v1/databases/{db}/snapshots/{snapshot}/restore", d + "/snapshots/" + snapshot + "/restore", map[string]any{"name": "evil"}},
		{"DELETE", "/v1/databases/{db}/snapshots/{snapshot}", d + "/snapshots/" + snapshot, nil},
	}
}

func bucketRESTOps(bucket string) []restOp {
	b := "/v1/buckets/" + bucket
	return []restOp{
		{"GET", "/v1/buckets/{bucket}", b, nil},
		{"PATCH", "/v1/buckets/{bucket}", b, map[string]any{"public_read": true}},
		{"DELETE", "/v1/buckets/{bucket}", b, nil},
		{"GET", "/v1/buckets/{bucket}/objects", b + "/objects", nil},
		{"GET", "/v1/buckets/{bucket}/object", b + "/object?key=doc.txt", nil},
		{"HEAD", "", b + "/object?key=doc.txt", nil},
		{"PUT", "/v1/buckets/{bucket}/object", b + "/object?key=new.txt", []byte("x")},
		{"DELETE", "/v1/buckets/{bucket}/object", b + "/object?key=doc.txt", nil},
		{"POST", "/v1/buckets/{bucket}/copy", b + "/copy", map[string]any{"from_key": "doc.txt", "key": "c.txt"}},
		{"POST", "/v1/buckets/{bucket}/presign", b + "/presign", map[string]any{"key": "doc.txt"}},
	}
}

// untouched fails the test if anything in a workspace changed: its rows,
// branches, snapshots, databases, buckets and objects, read with its own
// admin token.
func (w *wsWorld) untouched(s side) {
	w.t.Helper()
	adm := s.admin.Token
	for _, db := range []string{"notes", s.only} {
		r := w.ok(w.sql(adm, db, "SELECT v FROM t"), 200).json(w.t)
		if rows := r["rows"].([]any); len(rows) != 1 || !strings.Contains(fmt.Sprint(rows[0]), s.secret) {
			w.t.Fatalf("%s/%s rows changed: %v", s.name, db, rows)
		}
	}
	list := w.ok(w.do(adm, "GET", pDBs, "/v1/databases", nil), 200).json(w.t)["data"].([]any)
	if len(list) != 2 {
		w.t.Fatalf("%s has %d databases", s.name, len(list))
	}
	for _, raw := range list {
		d := raw.(map[string]any)
		wantBranches := 1.0
		if d["name"] == "notes" {
			wantBranches = 2
		}
		if d["branch_count"] != wantBranches || (d["name"] != "notes" && d["name"] != s.only) {
			w.t.Fatalf("%s database changed: %v", s.name, d)
		}
	}
	if sn := w.ok(w.do(adm, "GET", "/v1/databases/{db}/branches/{branch}/snapshots", "/v1/databases/notes/branches/main/snapshots", nil), 200).json(w.t)["data"].([]any); len(sn) != 1 {
		w.t.Fatalf("%s has %d snapshots", s.name, len(sn))
	}
	buckets := w.ok(w.do(adm, "GET", "/v1/buckets", "/v1/buckets", nil), 200).json(w.t)["data"].([]any)
	if len(buckets) != 2 {
		w.t.Fatalf("%s has %d buckets", s.name, len(buckets))
	}
	for _, raw := range buckets {
		b := raw.(map[string]any)
		if b["object_count"] != 1.0 || b["public_read"] != false {
			w.t.Fatalf("%s bucket changed: %v", s.name, b)
		}
		r := w.ok(w.do(adm, "GET", "", "/v1/buckets/"+b["name"].(string)+"/object?key=doc.txt", nil), 200)
		if string(r.body) != s.secret {
			w.t.Fatalf("%s object changed: %q", s.name, r.body)
		}
	}
}

// 17.9 row 1, REST: a token of workspace A on a database of B, by name or
// by id, gets the answer a database that doesn't exist gets.
func TestWorkspaceIsolationDatabasesREST(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	attackers := map[string]string{"admin": a.admin.Token, "write": a.write.Token, "read": a.read.Token,
		"database admin": a.dbAdmin, "branch": a.branch, "bucket": a.bucket.Token, "grants": a.grants.Token}
	// B's database by its unique name, by id, and B's notes by id (A has a
	// notes of its own, so the name would be A's).
	for _, target := range []string{b.only, b.onlyID, b.notesID} {
		for who, tok := range attackers {
			for _, branch := range []string{"main", b.devID, b.mainID} {
				ops := databaseOps(target, branch, b.snapshotID)
				missing := databaseOps("nope", branch, b.snapshotID)
				for i, o := range ops {
					got := w.do(tok, o.method, o.template, o.path, o.body)
					want := w.do(tok, missing[i].method, missing[i].template, missing[i].path, missing[i].body)
					if got.status < 400 {
						t.Fatalf("%s token: %s %s reached workspace B: %d %s", who, o.method, o.path, got.status, got.body)
					}
					if g, m := answer(t, got, target), answer(t, want, "nope"); g != m {
						t.Fatalf("%s token: %s %s\n  B's database: %s\n  no database:  %s", who, o.method, o.path, g, m)
					}
					if who == "admin" && (got.status != 404 || got.code(t) != "database_not_found") {
						t.Fatalf("workspace admin: %s %s: %d %s", o.method, o.path, got.status, got.body)
					}
				}
			}
		}
	}
	// A's own database with B's branch or snapshot id: nothing either.
	for _, o := range []restOp{
		{"GET", "/v1/databases/{db}/branches/{branch}", "/v1/databases/notes/branches/" + b.devID, nil},
		{"POST", "/v1/databases/{db}/branches/{branch}/query", "/v1/databases/notes/branches/" + b.devID + "/query", map[string]any{"sql": "SELECT * FROM t"}},
		{"DELETE", "/v1/databases/{db}/branches/{branch}", "/v1/databases/notes/branches/" + b.devID, nil},
		{"POST", "/v1/databases/{db}/branches", "/v1/databases/notes/branches", map[string]any{"name": "x", "from": b.devID}},
		{"POST", "/v1/databases/{db}/branches", "/v1/databases/notes/branches", map[string]any{"name": "x", "from_snapshot": b.snapshotID}},
		{"POST", "/v1/databases/{db}/snapshots/{snapshot}/restore", "/v1/databases/notes/snapshots/" + b.snapshotID + "/restore", map[string]any{"name": "x"}},
		{"DELETE", "/v1/databases/{db}/snapshots/{snapshot}", "/v1/databases/notes/snapshots/" + b.snapshotID, nil},
	} {
		if r := w.do(a.admin.Token, o.method, o.template, o.path, o.body); r.status != 404 {
			t.Fatalf("%s %s with an id from workspace B: %d %s", o.method, o.path, r.status, r.body)
		}
	}
	// The same name in two workspaces is two databases: each token reads
	// its own rows.
	for _, s := range []side{a, b} {
		for _, tok := range []string{s.admin.Token, s.read.Token, s.dbAdmin, s.grants.Token} {
			r := w.ok(w.sql(tok, "notes", "SELECT v FROM t"), 200)
			if !bytes.Contains(r.body, []byte(s.secret)) || bytes.Contains(r.body, []byte("secret of "+map[string]string{"acme": "globex", "globex": "acme"}[s.name])) {
				t.Fatalf("%s token read %s", s.name, r.body)
			}
		}
	}
	w.untouched(b)
	w.untouched(a)
}

// 17.9 row 2, REST: buckets and objects.
func TestWorkspaceIsolationBucketsREST(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	attackers := map[string]string{"admin": a.admin.Token, "write": a.write.Token, "read": a.read.Token,
		"bucket admin": a.bucket.Token, "database admin": a.dbAdmin, "grants": a.grants.Token}
	for _, target := range []string{b.onlyBucket, b.onlyBucketID, b.filesID} {
		ops, missing := bucketRESTOps(target), bucketRESTOps("nope-bucket")
		for who, tok := range attackers {
			for i, o := range ops {
				got := w.do(tok, o.method, o.template, o.path, o.body)
				want := w.do(tok, missing[i].method, missing[i].template, missing[i].path, missing[i].body)
				if got.status < 400 {
					t.Fatalf("%s token: %s %s reached workspace B: %d %s", who, o.method, o.path, got.status, got.body)
				}
				if o.method == "HEAD" {
					if got.status != want.status {
						t.Fatalf("%s token: HEAD %s: %d, no bucket: %d", who, o.path, got.status, want.status)
					}
					continue
				}
				if g, m := answer(t, got, target), answer(t, want, "nope-bucket"); g != m {
					t.Fatalf("%s token: %s %s\n  B's bucket: %s\n  no bucket:  %s", who, o.method, o.path, g, m)
				}
				if who == "admin" && (got.status != 404 || got.code(t) != "bucket_not_found") {
					t.Fatalf("workspace admin: %s %s: %d %s", o.method, o.path, got.status, got.body)
				}
			}
		}
		// Copying out of B into a bucket of A: the source isn't found.
		for who, tok := range map[string]string{"admin": a.admin.Token, "write": a.write.Token, "bucket admin": a.bucket.Token} {
			w.fails(w.do(tok, "POST", "/v1/buckets/{bucket}/copy", "/v1/buckets/files/copy",
				map[string]any{"from_bucket": target, "from_key": "doc.txt", "key": "stolen.txt"}), 404, "bucket_not_found")
			if r := w.do(a.admin.Token, "GET", "", "/v1/buckets/files/object?key=stolen.txt", nil); r.status != 404 {
				t.Fatalf("%s token copied an object out of workspace B", who)
			}
		}
	}
	// Same name, two buckets.
	for _, s := range []side{a, b} {
		for _, tok := range []string{s.admin.Token, s.read.Token, s.bucket.Token, s.grants.Token} {
			if r := w.ok(w.do(tok, "GET", "", "/v1/buckets/files/object?key=doc.txt", nil), 200); string(r.body) != s.secret {
				t.Fatalf("%s token read %q", s.name, r.body)
			}
		}
	}
	w.untouched(b)
	w.untouched(a)
}

// 17.9 row 3, REST: no list shows a workspace token anything of another
// workspace.
func TestWorkspaceIsolationLists(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	// Put something in B's trash and audit log too.
	w.ok(w.do(b.admin.Token, "POST", pDBs, "/v1/databases", map[string]any{"name": "trashed-b"}), 201)
	w.ok(w.do(b.admin.Token, "DELETE", pDB, "/v1/databases/trashed-b", nil), 200)

	foreign := []string{b.id, b.name, b.only, b.onlyBucket, b.notesID, b.onlyID, b.filesID, b.onlyBucketID, "trashed-b",
		b.admin.ID, b.write.ID, b.read.ID, b.bucket.ID, b.grants.ID, b.snapshotID, b.devID, b.mainID}
	for who, tok := range map[string]string{"admin": a.admin.Token, "write": a.write.Token, "read": a.read.Token,
		"database admin": a.dbAdmin, "bucket admin": a.bucket.Token, "grants": a.grants.Token, "branch": a.branch} {
		for _, path := range []string{"/v1/databases", "/v1/trash", "/v1/buckets", "/v1/tokens", "/v1/audit", "/v1/audit?limit=200", "/v1/workspaces", "/v1/workspace",
			"/v1/databases/notes/branches", "/v1/databases/notes/branches/main/snapshots", "/v1/buckets/files/objects"} {
			r := w.do(tok, "GET", "", path, nil)
			for _, f := range foreign {
				if bytes.Contains(r.body, []byte(f)) {
					t.Fatalf("%s token: GET %s shows %q of workspace B: %s", who, path, f, r.body)
				}
			}
		}
		// The workspace list is a list of one.
		list := w.ok(w.do(tok, "GET", "/v1/workspaces", "/v1/workspaces", nil), 200).json(t)["data"].([]any)
		if len(list) != 1 || list[0].(map[string]any)["id"] != a.id {
			t.Fatalf("%s token lists workspaces %v", who, list)
		}
		if cur := w.ok(w.do(tok, "GET", "/v1/workspace", "/v1/workspace", nil), 200).json(t); cur["id"] != a.id || cur["name"] != "acme" || cur["default"] != false {
			t.Fatalf("%s token's workspace: %v", who, cur)
		}
		// Limits and usage are totals over the whole workspace. A token for
		// one database or bucket would learn from them what else is there,
		// so only a token for the whole workspace sees them.
		whole := who == "admin" || who == "write" || who == "read"
		for _, path := range []string{"/v1/workspace", "/v1/workspaces/acme", "/v1/workspaces"} {
			m := w.ok(w.do(tok, "GET", "", path, nil), 200).json(t)
			if path == "/v1/workspaces" {
				m = m["data"].([]any)[0].(map[string]any)
			}
			_, hasLimits := m["limits"]
			_, hasUsage := m["usage"]
			if hasLimits != whole || hasUsage != whole {
				t.Fatalf("%s token: GET %s shows limits %v, usage %v: %v", who, path, hasLimits, hasUsage, m)
			}
		}
	}
	// A workspace admin sees all of its own, and only that.
	names := func(path string) string {
		var out []string
		for _, raw := range w.ok(w.do(a.admin.Token, "GET", "", path, nil), 200).json(t)["data"].([]any) {
			out = append(out, raw.(map[string]any)["name"].(string))
		}
		return strings.Join(out, ",")
	}
	if got := names("/v1/databases"); got != "notes,only-acme" {
		t.Fatalf("databases: %s", got)
	}
	if got := names("/v1/buckets"); got != "files,only-acme-files" {
		t.Fatalf("buckets: %s", got)
	}
	if got := names("/v1/tokens"); got != "grants,bucket,branch,db,read,write,admin" {
		t.Fatalf("tokens: %s", got)
	}
	if got := names("/v1/trash"); got != "" {
		t.Fatalf("trash: %s", got)
	}
	// The instance admin on a short URL acts in the default workspace: it
	// sees neither A's nor B's there, and reaches each through its
	// qualified URL.
	if r := w.ok(w.do(w.admin, "GET", pDBs, "/v1/databases", nil), 200); len(r.json(t)["data"].([]any)) != 0 {
		t.Fatalf("default workspace databases: %s", r.body)
	}
	if got := len(w.ok(w.do(w.admin, "GET", "", "/v1/workspaces/"+b.id+"/databases", nil), 200).json(t)["data"].([]any)); got != 2 {
		t.Fatalf("instance admin sees %d databases in B", got)
	}
	if got := len(w.ok(w.do(w.admin, "GET", "/v1/workspaces", "/v1/workspaces", nil), 200).json(t)["data"].([]any)); got != 3 {
		t.Fatalf("instance admin lists %d workspaces", got)
	}
}

// 17.9 row 4: a name another workspace uses is free; a create never fails,
// or says anything, because of it. REST, the S3 API and MCP.
func TestWorkspaceIsolationNames(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	adm := a.admin.Token
	// REST.
	w.ok(w.do(adm, "POST", pDBs, "/v1/databases", map[string]any{"name": b.only}), 201)
	w.ok(w.do(adm, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": b.onlyBucket}), 201)
	w.fails(w.do(adm, "POST", pDBs, "/v1/databases", map[string]any{"name": "notes"}), 409, "name_taken")
	w.fails(w.do(adm, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "files"}), 409, "name_taken")
	// Renaming onto a name B uses works, onto one of A's own doesn't.
	w.ok(w.do(adm, "PATCH", pDB, "/v1/databases/"+a.only, map[string]any{"name": "trashed-b"}), 200)
	w.fails(w.do(adm, "PATCH", pDB, "/v1/databases/trashed-b", map[string]any{"name": "notes"}), 409, "name_taken")
	// The S3 API.
	w.s3OK(w.s3(a.admin, "PUT", "/v1/s3/s3-only-in-b", nil), 200)
	w.s3OK(w.s3(b.admin, "PUT", "/v1/s3/s3-only-in-b", nil), 200)
	w.s3Fails(w.s3(a.admin, "PUT", "/v1/s3/files", nil), 409, "BucketAlreadyOwnedByYou")
	// MCP.
	s := w.mcp(adm)
	if _, msg := call(t, s, "create_database", map[string]any{"name": "mcp-db"}); msg != "" {
		t.Fatal(msg)
	}
	if _, msg := call(t, w.mcp(b.admin.Token), "create_database", map[string]any{"name": "mcp-db"}); msg != "" {
		t.Fatalf("the same name in workspace B: %s", msg)
	}
	if _, msg := call(t, s, "create_database", map[string]any{"name": "notes"}); !strings.Contains(msg, "name_taken") {
		t.Fatalf("a name A already uses: %q", msg)
	}
	if _, msg := call(t, s, "create_bucket", map[string]any{"name": b.onlyBucket + "-2"}); msg != "" {
		t.Fatal(msg)
	}
	// B never noticed.
	if got := len(w.ok(w.do(b.admin.Token, "GET", pDBs, "/v1/databases", nil), 200).json(t)["data"].([]any)); got != 3 {
		t.Fatalf("workspace B has %d databases", got)
	}
}

// 17.9 row 5: minting and managing tokens stops at the workspace, and the
// instance endpoints are closed to a workspace token.
func TestWorkspaceIsolationTokens(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	adm := a.admin.Token
	create := func(tok string, body map[string]any) resp { return w.do(tok, "POST", pTokens, "/v1/tokens", body) }
	// A grant can't name anything of B, by name or id.
	for _, db := range []string{b.only, b.onlyID, b.notesID} {
		got := create(adm, map[string]any{"name": "x", "database": db, "access": "read"})
		want := create(adm, map[string]any{"name": "x", "database": "nope", "access": "read"})
		if got.status != 404 || answer(t, got, db) != answer(t, want, "nope") {
			t.Fatalf("grant on B's database %s: %s", db, got.body)
		}
		w.fails(create(adm, map[string]any{"name": "x", "grants": []map[string]any{
			{"database": "notes", "access": "read"}, {"database": db, "access": "read"}}}), 404, "database_not_found")
	}
	for _, bk := range []string{b.onlyBucket, b.onlyBucketID, b.filesID} {
		w.fails(create(adm, map[string]any{"name": "x", "bucket": bk, "access": "read"}), 404, "bucket_not_found")
		w.fails(create(adm, map[string]any{"name": "x", "grants": []map[string]any{
			{"bucket": "files", "access": "read"}, {"bucket": bk, "access": "read"}}}), 404, "bucket_not_found")
	}
	w.fails(create(adm, map[string]any{"name": "x", "database": "notes", "branch": b.devID, "access": "read"}), 404, "branch_not_found")
	// Revoking a token of B, or an instance token: the answer a token that
	// doesn't exist gets, and the token still works.
	inst := w.ok(w.do(w.admin, "POST", "/v1/instance/tokens", "/v1/instance/tokens", map[string]any{"name": "op", "access": "admin"}), 201).json(t)
	none := w.do(adm, "DELETE", pToken, "/v1/tokens/tok_aaaaaaaaaaaaaaaaaaaa", nil)
	for _, id := range []string{b.admin.ID, b.read.ID, b.grants.ID, inst["id"].(string)} {
		got := w.do(adm, "DELETE", pToken, "/v1/tokens/"+id, nil)
		if got.status != 404 || answer(t, got) != answer(t, none) {
			t.Fatalf("revoking %s: %d %s", id, got.status, got.body)
		}
	}
	w.ok(w.do(b.read.Token, "GET", pDBs, "/v1/databases", nil), 200)
	w.ok(w.do(inst["token"].(string), "GET", "/v1/workspaces", "/v1/workspaces", nil), 200)
	// The instance admin can't reach B's token through A's door either.
	w.fails(w.do(w.admin, "DELETE", "", "/v1/workspaces/acme/tokens/"+b.read.ID, nil), 404, "token_not_found")
	w.ok(w.do(w.admin, "DELETE", "", "/v1/workspaces/globex/tokens/"+b.read.ID, nil), 204)
	w.fails(w.do(b.read.Token, "GET", pDBs, "/v1/databases", nil), 401, "unauthorized")

	// A token made by a workspace token is in that workspace and no wider.
	child := w.ok(create(adm, map[string]any{"name": "child", "access": "admin"}), 201).json(t)
	if child["scope"] != "workspace" || child["workspace_id"] != a.id {
		t.Fatalf("child token: %v", child)
	}
	w.fails(w.sql(child["token"].(string), b.only, "SELECT 1"), 404, "database_not_found")
	// Below workspace admin nothing mints a workspace token.
	for who, tok := range map[string]string{"write": a.write.Token, "read": a.read.Token, "database admin": a.dbAdmin, "bucket admin": a.bucket.Token, "grants": a.grants.Token} {
		if r := create(tok, map[string]any{"name": "x", "access": "read"}); r.status != 403 {
			t.Fatalf("%s token minted a workspace token: %d %s", who, r.status, r.body)
		}
	}
	// A database admin still mints inside its database, and sees only the
	// tokens it covers.
	w.ok(create(a.dbAdmin, map[string]any{"name": "ci", "database": "notes", "access": "read"}), 201)
	for _, raw := range w.ok(w.do(a.dbAdmin, "GET", pTokens, "/v1/tokens", nil), 200).json(t)["data"].([]any) {
		if m := raw.(map[string]any); m["scope"] != "database" || m["database_id"] != a.notesID {
			t.Fatalf("database admin sees token %v", m)
		}
	}

	// The instance side is closed, whoever asks from inside a workspace.
	for who, tok := range map[string]string{"admin": adm, "write": a.write.Token, "read": a.read.Token, "database admin": a.dbAdmin} {
		for _, o := range []restOp{
			{"GET", "/v1/instance/tokens", "/v1/instance/tokens", nil},
			{"POST", "/v1/instance/tokens", "/v1/instance/tokens", map[string]any{"name": "x", "access": "admin"}},
			{"DELETE", "/v1/instance/tokens/{token_id}", "/v1/instance/tokens/" + inst["id"].(string), nil},
			{"GET", "/v1/instance/audit", "/v1/instance/audit", nil},
			{"POST", "/v1/workspaces", "/v1/workspaces", map[string]any{"name": "mine"}},
			{"PATCH", "/v1/workspaces/{ws}", "/v1/workspaces/acme", map[string]any{"max_databases": 0}},
			{"PATCH", "/v1/workspaces/{ws}", "/v1/workspaces/globex", map[string]any{"name": "taken-over"}},
			{"PATCH", "/v1/workspaces/{ws}", "/v1/workspaces/nope", map[string]any{"name": "x"}},
		} {
			w.fails(w.do(tok, o.method, o.template, o.path, o.body), 403, "forbidden")
			_ = who
		}
		// Another workspace by name or id answers like one that doesn't exist.
		none := w.do(tok, "GET", "/v1/workspaces/{ws}", "/v1/workspaces/nope", nil)
		for _, ref := range []string{"globex", b.id, "default", w.defaultWS()} {
			got := w.do(tok, "GET", "/v1/workspaces/{ws}", "/v1/workspaces/"+ref, nil)
			if got.status != 404 || answer(t, got, ref) != answer(t, none, "nope") {
				t.Fatalf("%s token: GET /v1/workspaces/%s: %d %s", who, ref, got.status, got.body)
			}
		}
		for _, ref := range []string{"acme", a.id} {
			if got := w.ok(w.do(tok, "GET", "/v1/workspaces/{ws}", "/v1/workspaces/"+ref, nil), 200).json(t); got["id"] != a.id {
				t.Fatalf("own workspace: %v", got)
			}
		}
	}
	if got := w.ok(w.do(adm, "GET", "/v1/workspace", "/v1/workspace", nil), 200).json(t); got["name"] != "acme" {
		t.Fatalf("workspace A was renamed or changed: %v", got)
	}
	if got := w.ok(w.do(w.admin, "GET", "/v1/workspaces/{ws}", "/v1/workspaces/globex", nil), 200).json(t); got["id"] != b.id {
		t.Fatalf("workspace B was renamed: %v", got)
	}
}

// 17.9 row 6: a qualified URL that names another workspace, or one that
// doesn't exist, gets the same answer, on every door.
func TestWorkspaceIsolationQualifiedURLs(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	tokens := map[string]key{"admin": a.admin, "write": a.write, "read": a.read, "bucket admin": a.bucket, "grants": a.grants,
		"database admin": {Token: a.dbAdmin}, "branch": {Token: a.branch}}
	paths := []restOp{
		{"GET", "", "/databases", nil}, {"POST", "", "/databases", map[string]any{"name": "x"}},
		{"GET", "", "/databases/notes", nil}, {"POST", "", "/databases/notes/query", map[string]any{"sql": "SELECT * FROM t"}},
		{"GET", "", "/databases/" + b.notesID, nil}, {"DELETE", "", "/databases/notes", nil},
		{"GET", "", "/databases/notes/branches", nil}, {"GET", "", "/trash", nil},
		{"GET", "", "/buckets", nil}, {"POST", "", "/buckets", map[string]any{"name": "xxx"}},
		{"GET", "", "/buckets/files/object?key=doc.txt", nil}, {"PUT", "", "/buckets/files/object?key=x", []byte("x")},
		{"GET", "", "/tokens", nil}, {"POST", "", "/tokens", map[string]any{"name": "x", "access": "admin"}},
		{"DELETE", "", "/tokens/" + b.read.ID, nil}, {"GET", "", "/audit", nil},
	}
	for who, k := range tokens {
		for _, o := range paths {
			none := w.do(k.Token, o.method, "", "/v1/workspaces/nope"+o.path, o.body)
			if none.status != 404 || none.code(t) != "workspace_not_found" {
				t.Fatalf("%s token: %s %s in a workspace that doesn't exist: %d %s", who, o.method, o.path, none.status, none.body)
			}
			for _, ref := range []string{"globex", b.id, "default", w.defaultWS()} {
				got := w.do(k.Token, o.method, "", "/v1/workspaces/"+ref+o.path, o.body)
				if answer(t, got, ref) != answer(t, none, "nope") {
					t.Fatalf("%s token: %s /v1/workspaces/%s%s\n  got  %s\n  none %s", who, o.method, ref, o.path, answer(t, got, ref), answer(t, none, "nope"))
				}
			}
		}
		// Its own workspace, by name and by id, is the short URL.
		for _, ref := range []string{"acme", a.id} {
			short := w.do(k.Token, "GET", "", "/v1/databases", nil)
			long := w.do(k.Token, "GET", "", "/v1/workspaces/"+ref+"/databases", nil)
			if short.status != long.status || !bytes.Equal(short.body, long.body) {
				t.Fatalf("%s token: own qualified URL differs: %d %s", who, long.status, long.body)
			}
		}

		// libSQL.
		h := &hrana{e: w.env, token: k.Token, path: "/v1/workspaces/nope/libsql/notes/main/v2"}
		none := h.pipeline(exec("SELECT * FROM t"), closeReq)[0]
		if h.status != 404 || errCode(t, none) != "workspace_not_found" {
			t.Fatalf("%s token: libSQL in a workspace that doesn't exist: %d %v", who, h.status, none)
		}
		for _, ref := range []string{"globex", b.id} {
			for _, db := range []string{"notes", b.only, b.notesID} {
				h := &hrana{e: w.env, token: k.Token, path: "/v1/workspaces/" + ref + "/libsql/" + db + "/main/v2"}
				got := h.pipeline(exec("SELECT * FROM t"), closeReq)[0]
				if h.status != 404 || errCode(t, got) != "workspace_not_found" {
					t.Fatalf("%s token: libSQL on %s in %s: %d %v", who, db, ref, h.status, got)
				}
			}
		}

		// MCP: the connection itself is refused, the same for both.
		for _, ref := range []string{"nope", "globex", b.id} {
			if _, err := w.mcpAt("/v1/workspaces/"+ref+"/mcp", k.Token); err == nil {
				t.Fatalf("%s token connected to MCP in workspace %s", who, ref)
			}
		}

		// The S3 API: a bucket request says NoSuchBucket, the bucket list
		// AccessDenied, for another workspace and for none alike.
		if k.AKID == "" {
			continue
		}
		for _, ref := range []string{"nope", "globex", b.id, "default"} {
			base := "/v1/workspaces/" + ref + "/s3"
			for _, o := range []bucketOp{
				{"get", "GET", base + "/files/doc.txt", nil, nil},
				{"put", "PUT", base + "/files/new.txt", []byte("x"), nil},
				{"delete", "DELETE", base + "/files/doc.txt", nil, nil},
				{"list", "GET", base + "/files?list-type=2", nil, nil},
				{"head bucket", "HEAD", base + "/files", nil, nil},
				{"create bucket", "PUT", base + "/brand-new", nil, nil},
				{"delete bucket", "DELETE", base + "/files", nil, nil},
				{"get by id", "GET", base + "/" + b.filesID + "/doc.txt", nil, nil},
			} {
				r := w.s3(k, o.method, o.path, o.body, o.hdr...)
				if r.status != 404 || (o.method != "HEAD" && s3Code(r) != "NoSuchBucket") {
					t.Fatalf("%s key: S3 %s in workspace %s: %d %s", who, o.name, ref, r.status, r.body)
				}
			}
			w.s3Fails(w.s3(k, "GET", base, nil), 403, "AccessDenied")
			w.s3Fails(w.fetch("GET", w.presign(k, "GET", base+"/files/doc.txt", time.Minute, time.Now()), nil), 404, "NoSuchBucket")
		}
		own := w.s3(k, "GET", "/v1/workspaces/acme/s3/files/doc.txt", nil)
		if own.status != 200 || string(own.body) != a.secret {
			t.Fatalf("%s key on its own qualified S3 URL: %d %s", who, own.status, own.body)
		}
	}
	w.untouched(b)
	w.untouched(a)
	if got := len(w.ok(w.do(w.admin, "GET", pDBs, "/v1/databases", nil), 200).json(t)["data"].([]any)); got != 0 {
		t.Fatalf("something was created in the default workspace: %d databases", got)
	}
}

// mcpAt connects an MCP client to a path, returning the connection error
// instead of failing the test.
func (e *env) mcpAt(path, token string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             e.http.URL + path,
		HTTPClient:           &http.Client{Transport: bearer{token, http.DefaultTransport}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	s, err := client.Connect(context.Background(), transport, nil)
	if err == nil {
		e.t.Cleanup(func() { _ = s.Close() })
	}
	return s, err
}

// 17.9 row 7: a database, branch, bucket or grants token inside a workspace
// keeps its limits there, on top of the workspace boundary.
func TestWorkspaceIsolationNarrowTokens(t *testing.T) {
	w := newWSWorld(t, nil)
	a := w.a
	// Another database or bucket of the same workspace doesn't exist for them.
	for who, tok := range map[string]string{"database admin": a.dbAdmin, "branch": a.branch, "grants": a.grants.Token, "bucket admin": a.bucket.Token} {
		w.fails(w.sql(tok, a.only, "SELECT 1"), 404, "database_not_found")
		w.fails(w.do(tok, "GET", "/v1/buckets/{bucket}", "/v1/buckets/"+a.onlyBucket, nil), 404, "bucket_not_found")
		// None of them is an admin of the workspace, at any level.
		for _, o := range []restOp{
			{"POST", pDBs, "/v1/databases", map[string]any{"name": "x"}},
			{"POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "xxx"}},
			{"GET", "/v1/trash", "/v1/trash", nil},
			{"GET", "/v1/audit", "/v1/audit", nil},
		} {
			if r := w.do(tok, o.method, o.template, o.path, o.body); r.status != 403 || r.code(t) != "forbidden" {
				t.Fatalf("%s token: %s %s: %d %s", who, o.method, o.path, r.status, r.body)
			}
		}
	}
	w.fails(w.do(a.dbAdmin, "PATCH", pDB, "/v1/databases/notes", map[string]any{"name": "mine"}), 403, "forbidden")
	w.fails(w.do(a.dbAdmin, "DELETE", pDB, "/v1/databases/notes", nil), 403, "forbidden")
	w.fails(w.do(a.bucket.Token, "DELETE", "/v1/buckets/{bucket}", "/v1/buckets/files", nil), 403, "forbidden")
	w.fails(w.do(a.bucket.Token, "POST", pQuery, "/v1/databases/notes/query", map[string]any{"sql": "SELECT 1"}), 404, "database_not_found")
	w.fails(w.do(a.dbAdmin, "GET", "/v1/buckets/{bucket}", "/v1/buckets/files", nil), 404, "bucket_not_found")
	// The branch token reaches its branch and nothing else of the database.
	w.ok(w.do(a.branch, "POST", pBQuery, "/v1/databases/notes/branches/dev/query", map[string]any{"sql": "SELECT * FROM t"}), 200)
	w.fails(w.sql(a.branch, "notes", "SELECT 1"), 403, "forbidden")

	// The database list tells a branch token only about its branch: one
	// branch, that branch's size, and no default branch, which it can't
	// reach. GET on the database still refuses it.
	list := w.ok(w.do(a.branch, "GET", pDBs, "/v1/databases", nil), 200).json(t)["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("branch token lists %d databases", len(list))
	}
	d := list[0].(map[string]any)
	full := w.ok(w.do(a.admin.Token, "GET", pDB, "/v1/databases/notes", nil), 200).json(t)
	if d["branch_count"] != 1.0 || d["default_branch"] != "" || d["default_branch_id"] != "" || d["size_bytes"].(float64) >= full["size_bytes"].(float64) {
		t.Fatalf("branch token's view of the database: %v (whole database: %v)", d, full)
	}
	if full["branch_count"] != 2.0 || full["default_branch"] != "main" {
		t.Fatalf("the whole database: %v", full)
	}
	w.fails(w.do(a.branch, "GET", pDB, "/v1/databases/notes", nil), 403, "forbidden")
	// MCP gives the same limited view.
	out, msg := call(t, w.mcp(a.branch), "list_databases", nil)
	if msg != "" {
		t.Fatal(msg)
	}
	md := out["databases"].([]any)[0].(map[string]any)
	if md["branch_count"] != 1.0 || md["default_branch"] != "" {
		t.Fatalf("branch token's view over MCP: %v", md)
	}
	w.untouched(a)
}

// 17.9 row 8: levels. read can't write, write can't manage, and a
// workspace admin can't touch the workspace itself.
func TestWorkspaceIsolationLevels(t *testing.T) {
	w := newWSWorld(t, nil)
	a := w.a
	// read: lists and reads everything in the workspace.
	for _, db := range []string{"notes", a.only} {
		w.ok(w.sql(a.read.Token, db, "SELECT * FROM t"), 200)
	}
	w.ok(w.do(a.read.Token, "GET", "", "/v1/buckets/"+a.onlyBucket+"/object?key=doc.txt", nil), 200)
	for _, o := range []restOp{
		{"POST", pQuery, "/v1/databases/notes/query", map[string]any{"sql": "INSERT INTO t VALUES ('x')"}},
		{"POST", pBranches, "/v1/databases/notes/branches", map[string]any{"name": "x"}},
		{"POST", "/v1/databases/{db}/branches/{branch}/snapshots", "/v1/databases/notes/branches/main/snapshots", map[string]any{}},
		{"PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/files/object?key=x", []byte("x")},
		{"DELETE", "/v1/buckets/{bucket}/object", "/v1/buckets/files/object?key=doc.txt", nil},
		{"PATCH", "/v1/buckets/{bucket}", "/v1/buckets/files", map[string]any{"public_read": true}},
	} {
		if r := w.do(a.read.Token, o.method, o.template, o.path, o.body); r.status != 403 {
			t.Fatalf("read token: %s %s: %d %s", o.method, o.path, r.status, r.body)
		}
	}
	// write: data in every database and bucket, present and future.
	w.ok(w.do(a.admin.Token, "POST", pDBs, "/v1/databases", map[string]any{"name": "later"}), 201)
	w.ok(w.sql(a.write.Token, "later", "CREATE TABLE t(v)"), 200)
	w.ok(w.sql(a.read.Token, "later", "SELECT * FROM t"), 200)
	w.ok(w.do(a.write.Token, "PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/"+a.onlyBucket+"/object?key=w.txt", []byte("w")), 200)
	w.ok(w.do(a.write.Token, "POST", pBranches, "/v1/databases/later/branches", map[string]any{"name": "wb"}), 201)
	for _, tok := range []string{a.write.Token, a.read.Token} {
		for _, o := range []restOp{
			{"POST", pDBs, "/v1/databases", map[string]any{"name": "x"}},
			{"PATCH", pDB, "/v1/databases/notes", map[string]any{"name": "x"}},
			{"DELETE", pDB, "/v1/databases/notes", nil},
			{"POST", "/v1/databases/{db}/restore", "/v1/databases/notes/restore", map[string]any{}},
			{"GET", "/v1/trash", "/v1/trash", nil},
			{"POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "xxx"}},
			{"DELETE", "/v1/buckets/{bucket}", "/v1/buckets/files", nil},
			{"PATCH", "/v1/buckets/{bucket}", "/v1/buckets/files", map[string]any{"public_read": true}},
			{"GET", pTokens, "/v1/tokens", nil},
			{"POST", pTokens, "/v1/tokens", map[string]any{"name": "x", "database": "notes", "access": "read"}},
			{"DELETE", pToken, "/v1/tokens/" + a.read.ID, nil},
			{"GET", "/v1/audit", "/v1/audit", nil},
		} {
			r := w.do(tok, o.method, o.template, o.path, o.body)
			if r.status != 403 && !(o.method == "DELETE" && o.template == pToken && r.status == 404) {
				t.Fatalf("%s %s below admin: %d %s", o.method, o.path, r.status, r.body)
			}
		}
	}
	w.s3Fails(w.s3(a.write, "PUT", "/v1/s3/brand-new", nil), 403, "AccessDenied")
	w.s3Fails(w.s3(a.write, "DELETE", "/v1/s3/"+a.onlyBucket, nil), 403, "AccessDenied")
	w.s3Fails(w.s3(a.read, "PUT", "/v1/s3/files/x", []byte("x")), 403, "AccessDenied")
	for tool, args := range map[string]map[string]any{"create_database": {"name": "x"}, "create_bucket": {"name": "xxx"}} {
		if _, msg := call(t, w.mcp(a.write.Token), tool, args); !strings.Contains(msg, "forbidden") {
			t.Fatalf("%s with a write token: %q", tool, msg)
		}
	}
	// admin: everything inside, by every door.
	adm := a.admin.Token
	w.ok(w.do(adm, "PATCH", pDB, "/v1/databases/later", map[string]any{"name": "later2"}), 200)
	w.ok(w.do(adm, "DELETE", pDB, "/v1/databases/later2", nil), 200)
	if got := len(w.ok(w.do(adm, "GET", "/v1/trash", "/v1/trash", nil), 200).json(t)["data"].([]any)); got != 1 {
		t.Fatalf("trash holds %d", got)
	}
	w.ok(w.do(adm, "POST", "/v1/databases/{db}/restore", "/v1/databases/later2/restore", map[string]any{}), 200)
	w.s3OK(w.s3(a.admin, "PUT", "/v1/s3/made-by-s3", nil), 200)
	w.s3OK(w.s3(a.admin, "DELETE", "/v1/s3/made-by-s3", nil), 204)
	w.ok(w.do(adm, "GET", "/v1/audit", "/v1/audit", nil), 200)
	// The workspace itself is the instance admin's (TestWorkspaceIsolationTokens
	// covers the endpoints): its limits didn't move.
	cur := w.ok(w.do(adm, "GET", "/v1/workspace", "/v1/workspace", nil), 200).json(t)
	if cur["limits"].(map[string]any)["max_databases"] != 20.0 {
		t.Fatalf("limits: %v", cur["limits"])
	}
}

// 17.9 rows 1 to 3 and 12 on the S3 API: another workspace's buckets, by
// name or id, don't exist; copies and upload ids don't cross.
func TestWorkspaceIsolationS3(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	keys := map[string]key{"admin": a.admin, "write": a.write, "read": a.read, "bucket admin": a.bucket, "grants": a.grants}
	for who, k := range keys {
		for _, target := range []string{b.onlyBucket, b.onlyBucketID, b.filesID} {
			for _, op := range bucketOps(target) {
				r := w.s3(k, op.method, op.path, op.body, op.hdr...)
				missing := w.s3(k, op.method, strings.Replace(op.path, target, "bucket-x", 1), op.body, replaceAll(op.hdr, target, "bucket-x")...)
				if r.status != http.StatusNotFound || r.status != missing.status || s3Code(r) != s3Code(missing) {
					t.Fatalf("%s key: %s on B's bucket %s: %d %s, missing bucket: %d %s", who, op.name, target, r.status, s3Code(r), missing.status, s3Code(missing))
				}
			}
			w.s3Fails(w.fetch("GET", w.presign(k, "GET", "/v1/s3/"+target+"/doc.txt", time.Minute, time.Now()), nil), 404, "NoSuchBucket")
			w.s3Fails(w.fetch("PUT", w.presign(k, "PUT", "/v1/s3/"+target+"/x", time.Minute, time.Now()), []byte("x")), 404, "NoSuchBucket")
			// Copy with a source in B into a bucket of A. A read key is
			// refused for the destination before the source is looked at.
			if copied := w.s3(k, "PUT", "/v1/s3/files/stolen.txt", nil, "x-amz-copy-source", target+"/doc.txt"); who == "read" {
				w.s3Fails(copied, 403, "AccessDenied")
			} else {
				w.s3Fails(copied, 404, "NoSuchBucket")
			}
		}
		// An upload id of B, used under A's bucket of the same name and key.
		for _, o := range []bucketOp{
			{"upload part", "PUT", "/v1/s3/files/big?partNumber=1&uploadId=" + b.uploadID, []byte("x"), nil},
			{"list parts", "GET", "/v1/s3/files/big?uploadId=" + b.uploadID, nil, nil},
			{"complete", "POST", "/v1/s3/files/big?uploadId=" + b.uploadID, []byte(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"x"</ETag></Part></CompleteMultipartUpload>`), nil},
			{"abort", "DELETE", "/v1/s3/files/big?uploadId=" + b.uploadID, nil, nil},
		} {
			r := w.s3(k, o.method, o.path, o.body)
			if who == "read" && o.method != "GET" {
				w.s3Fails(r, 403, "AccessDenied")
				continue
			}
			w.s3Fails(r, 404, "NoSuchUpload")
		}
		var list struct {
			Names []string `xml:"Buckets>Bucket>Name"`
		}
		_ = xml.Unmarshal(w.s3OK(w.s3(k, "GET", "/v1/s3", nil), 200).body, &list)
		want := "[files only-acme-files]"
		if who == "bucket admin" || who == "grants" {
			want = "[files]"
		}
		if fmt.Sprint(list.Names) != want {
			t.Fatalf("%s key lists %v", who, list.Names)
		}
		// The same name is its own bucket.
		if r := w.s3OK(w.s3(k, "GET", "/v1/s3/files/doc.txt", nil), 200); string(r.body) != a.secret {
			t.Fatalf("%s key read %q", who, r.body)
		}
	}
	w.s3Fails(w.s3(a.admin, "HEAD", "/v1/s3/files/stolen.txt", nil), 404, "")
	// B's upload is still there and still B's.
	w.s3OK(w.s3(b.admin, "GET", "/v1/s3/files/big?uploadId="+b.uploadID, nil), 200)
	w.untouched(b)
	w.untouched(a)
}

func replaceAll(in []string, from, to string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ReplaceAll(s, from, to)
	}
	return out
}

// 17.9 row 13: without a token, a short URL only ever reaches the default
// workspace. Another workspace's public bucket needs its qualified URL, and
// private, missing and other-workspace all look the same.
func TestWorkspaceIsolationAnonymous(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	// A public bucket in B, and one with the same name in the default
	// workspace holding something else.
	w.ok(w.do(b.admin.Token, "PATCH", "/v1/buckets/{bucket}", "/v1/buckets/"+b.onlyBucket, map[string]any{"public_read": true}), 200)
	w.ok(w.do(b.admin.Token, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "shared-name"}), 201)
	w.ok(w.do(b.admin.Token, "PATCH", "/v1/buckets/{bucket}", "/v1/buckets/shared-name", map[string]any{"public_read": true}), 200)
	w.ok(w.putObject(b.admin.Token, "shared-name", "doc.txt", b.secret), 200)
	w.ok(w.do(w.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "shared-name"}), 201)
	w.ok(w.do(w.admin, "PATCH", "/v1/buckets/{bucket}", "/v1/buckets/shared-name", map[string]any{"public_read": true}), 200)
	w.ok(w.putObject(w.admin, "shared-name", "doc.txt", "public in default"), 200)

	get := func(path string) resp { return w.fetch("GET", w.http.URL+path, nil) }
	// Short URLs: the default workspace, never B.
	if r := get("/v1/s3/shared-name/doc.txt"); r.status != 200 || string(r.body) != "public in default" {
		t.Fatalf("short public URL: %d %s", r.status, r.body)
	}
	for _, path := range []string{"/v1/s3/" + b.onlyBucket + "/doc.txt", "/v1/s3/" + b.onlyBucketID + "/doc.txt", "/v1/s3/files/doc.txt", "/v1/s3/nope/doc.txt"} {
		w.s3Fails(get(path), 403, "AccessDenied")
	}
	// Qualified URLs: B's public bucket opens, by workspace name and id.
	for _, ref := range []string{"globex", b.id} {
		for _, bucket := range []string{b.onlyBucket, "shared-name"} {
			if r := get("/v1/workspaces/" + ref + "/s3/" + bucket + "/doc.txt"); r.status != 200 || string(r.body) != b.secret {
				t.Fatalf("qualified public URL %s/%s: %d %s", ref, bucket, r.status, r.body)
			}
		}
		// Private, missing, and the same public bucket through the wrong
		// workspace all say the same.
		for _, path := range []string{"/files/doc.txt", "/nope/doc.txt", "/" + a.onlyBucket + "/doc.txt"} {
			w.s3Fails(get("/v1/workspaces/"+ref+"/s3"+path), 403, "AccessDenied")
		}
	}
	for _, path := range []string{"/v1/workspaces/acme/s3/" + b.onlyBucket + "/doc.txt", "/v1/workspaces/nope/s3/" + b.onlyBucket + "/doc.txt",
		"/v1/workspaces/nope/s3/shared-name/doc.txt", "/v1/workspaces/acme/s3/shared-name/doc.txt"} {
		w.s3Fails(get(path), 403, "AccessDenied")
	}
	// Reading is all a public bucket gives: no list, no write, no delete.
	for _, o := range []bucketOp{
		{"list", "GET", "/v1/workspaces/globex/s3/" + b.onlyBucket, nil, nil},
		{"list buckets", "GET", "/v1/workspaces/globex/s3", nil, nil},
		{"put", "PUT", "/v1/workspaces/globex/s3/" + b.onlyBucket + "/x", []byte("x"), nil},
		{"delete", "DELETE", "/v1/workspaces/globex/s3/" + b.onlyBucket + "/doc.txt", nil, nil},
	} {
		w.s3Fails(w.fetch(o.method, w.http.URL+o.path, o.body), 403, "AccessDenied")
	}
	// No other door opens without a token, qualified or not.
	for _, path := range []string{"/v1/workspaces/globex/databases", "/v1/workspaces/globex/buckets", "/v1/workspaces/globex/tokens", "/v1/workspaces/nope/databases", "/v1/workspaces", "/v1/workspace"} {
		w.fails(w.do("", "GET", "", path, nil), 401, "unauthorized")
	}
	for _, path := range []string{"/v1/workspaces/globex/libsql/notes/main/v2/pipeline", "/v1/workspaces/globex/mcp"} {
		if r := w.fetch("POST", w.http.URL+path, []byte(`{}`), "Content-Type", "application/json"); r.status != 401 {
			t.Fatalf("POST %s without a token: %d %s", path, r.status, r.body)
		}
	}
}

// 17.9 rows 1 and 12 on the libSQL endpoint.
func TestWorkspaceIsolationLibSQL(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	for who, tok := range map[string]string{"admin": a.admin.Token, "write": a.write.Token, "read": a.read.Token, "database admin": a.dbAdmin, "grants": a.grants.Token} {
		none := w.one(tok, "nope", "main", "SELECT * FROM t")
		wantErr(t, none, "database_not_found", who+" on a database that doesn't exist")
		for _, db := range []string{b.only, b.onlyID, b.notesID} {
			for _, branch := range []string{"main", b.devID, b.mainID} {
				got := w.one(tok, db, branch, "SELECT * FROM t")
				wantErr(t, got, "database_not_found", who+" on B's database "+db)
				gm, _ := got["error"].(map[string]any)["message"].(string)
				nm, _ := none["error"].(map[string]any)["message"].(string)
				if strings.ReplaceAll(gm, db, "X") != strings.ReplaceAll(nm, "nope", "X") {
					t.Fatalf("%s: B's database %q, no database %q", who, gm, nm)
				}
			}
		}
		// A's notes with a branch id of B.
		if got := w.one(tok, "notes", b.devID, "SELECT * FROM t"); errCode(t, got) != "branch_not_found" && errCode(t, got) != "forbidden" {
			t.Fatalf("%s: A's database with B's branch id: %v", who, got)
		}
		// The same name is its own database.
		res := w.libsql(tok, "notes", "main").run("SELECT v FROM t")
		if fmt.Sprint(rows(t, res)) != fmt.Sprintf("[[map[type:text value:%s]]]", a.secret) {
			t.Fatalf("%s read %v", who, rows(t, res))
		}
	}
	// A baton from a stream B opened, used by A on its own database of the
	// same name: no stream. And B's transaction is unharmed.
	hb := w.libsql(b.admin.Token, "notes", "main")
	hb.ok(hb.pipeline(exec("BEGIN"))[0])
	hb.ok(hb.pipeline(exec("INSERT INTO t VALUES ('in flight')"))[0])
	for _, tok := range []string{a.admin.Token, a.dbAdmin} {
		ha := w.libsql(tok, "notes", "main")
		ha.baton = hb.baton
		if r := ha.pipeline(exec("SELECT * FROM t"))[0]; ha.status != 400 || errCode(t, r) != "invalid_baton" {
			t.Fatalf("B's baton used by A: %d %v", ha.status, r)
		}
		hq := &hrana{e: w.env, token: tok, path: "/v1/workspaces/acme/libsql/notes/main/v2", baton: hb.baton}
		if r := hq.pipeline(exec("SELECT * FROM t"))[0]; hq.status != 400 || errCode(t, r) != "invalid_baton" {
			t.Fatalf("B's baton used by A on a qualified URL: %d %v", hq.status, r)
		}
	}
	hb.ok(hb.pipeline(exec("ROLLBACK"), closeReq)[0])
	w.untouched(b)
	w.untouched(a)
}

// 17.9 rows 1 to 3 on MCP.
func TestWorkspaceIsolationMCP(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	for who, tok := range map[string]string{"admin": a.admin.Token, "write": a.write.Token, "read": a.read.Token, "grants": a.grants.Token} {
		s := w.mcp(tok)
		out, msg := call(t, s, "list_databases", nil)
		if msg != "" {
			t.Fatal(msg)
		}
		raw, _ := json.Marshal(out)
		for _, f := range []string{b.only, b.onlyID, b.notesID, b.id} {
			if bytes.Contains(raw, []byte(f)) {
				t.Fatalf("%s: list_databases shows %s: %s", who, f, raw)
			}
		}
		out, msg = call(t, s, "list_buckets", nil)
		if msg != "" {
			t.Fatal(msg)
		}
		if raw, _ := json.Marshal(out); bytes.Contains(raw, []byte(b.onlyBucket)) {
			t.Fatalf("%s: list_buckets shows B's bucket: %s", who, raw)
		}
		for _, db := range []string{b.only, b.onlyID, b.notesID} {
			for tool, args := range map[string]map[string]any{
				"get_database":    {"database": db},
				"list_branches":   {"database": db},
				"create_branch":   {"database": db, "name": "evil"},
				"delete_branch":   {"database": db, "branch": "dev"},
				"reset_branch":    {"database": db, "branch": "dev"},
				"diff_branches":   {"database": db, "branch": "dev", "against": "main"},
				"get_schema":      {"database": db},
				"query":           {"database": db, "sql": "SELECT * FROM t"},
				"execute":         {"database": db, "sql": "DELETE FROM t"},
				"list_snapshots":  {"database": db},
				"create_snapshot": {"database": db},
			} {
				_, got := call(t, s, tool, args)
				args["database"] = "nope"
				_, none := call(t, s, tool, args)
				if got == "" || strings.ReplaceAll(got, db, "X") != strings.ReplaceAll(none, "nope", "X") {
					t.Fatalf("%s: %s on B's database %s: %q, on no database: %q", who, tool, db, got, none)
				}
				if who == "admin" && !strings.Contains(got, "database_not_found") {
					t.Fatalf("workspace admin: %s on B's database: %q", tool, got)
				}
			}
		}
		for _, bk := range []string{b.onlyBucket, b.onlyBucketID, b.filesID} {
			for tool, args := range map[string]map[string]any{
				"list_objects":   {"bucket": bk},
				"get_object":     {"bucket": bk, "key": "doc.txt"},
				"put_object":     {"bucket": bk, "key": "x", "text": "x"},
				"delete_object":  {"bucket": bk, "key": "doc.txt"},
				"presign_object": {"bucket": bk, "key": "doc.txt"},
			} {
				_, got := call(t, s, tool, args)
				args["bucket"] = "nope-bucket"
				_, none := call(t, s, tool, args)
				if got == "" || strings.ReplaceAll(got, bk, "X") != strings.ReplaceAll(none, "nope-bucket", "X") {
					t.Fatalf("%s: %s on B's bucket %s: %q, on no bucket: %q", who, tool, bk, got, none)
				}
			}
		}
		// Its own, under the same names.
		out, msg = call(t, s, "query", map[string]any{"database": "notes", "sql": "SELECT v FROM t"})
		if raw, _ := json.Marshal(out); msg != "" || !bytes.Contains(raw, []byte(a.secret)) || bytes.Contains(raw, []byte(b.secret)) {
			t.Fatalf("%s: query on its own notes: %s %s", who, raw, msg)
		}
		out, msg = call(t, s, "get_object", map[string]any{"bucket": "files", "key": "doc.txt"})
		if msg != "" || out["text"] != a.secret {
			t.Fatalf("%s: get_object on its own files: %v %s", who, out, msg)
		}
	}
	w.untouched(b)
	w.untouched(a)
}

// The proof the plan asks for (goal 7): a workspace token used through MCP
// makes two databases and a bucket, writes to them and reads them back, and
// a token of another workspace can't see or touch any of it, through MCP,
// REST, libSQL or S3.
func TestWorkspaceAgentMakesItsOwn(t *testing.T) {
	e := newEnv(t, nil)
	mk := func(name string) key {
		e.ok(e.do(e.admin, "POST", "/v1/workspaces", "/v1/workspaces", map[string]any{"name": name}), 201)
		var k key
		r := e.ok(e.do(e.admin, "POST", "", "/v1/workspaces/"+name+"/tokens", map[string]any{"name": "agent", "access": "admin", "access_key": true}), 201)
		if err := json.Unmarshal(r.body, &k); err != nil {
			t.Fatal(err)
		}
		return k
	}
	agent, other := mk("agent"), mk("someone-else")

	s := e.mcp(agent.Token)
	if ws, msg := call(t, s, "list_databases", nil); msg != "" || len(ws["databases"].([]any)) != 0 {
		t.Fatalf("a new workspace isn't empty: %v %s", ws, msg)
	}
	for _, db := range []string{"memory", "tasks"} {
		if _, msg := call(t, s, "create_database", map[string]any{"name": db}); msg != "" {
			t.Fatal(msg)
		}
		if _, msg := call(t, s, "execute", map[string]any{"database": db, "sql": "CREATE TABLE items(id INTEGER PRIMARY KEY, body TEXT)"}); msg != "" {
			t.Fatal(msg)
		}
		if _, msg := call(t, s, "execute", map[string]any{"database": db, "sql": "INSERT INTO items(body) VALUES (?)", "args": []any{"kept in " + db}}); msg != "" {
			t.Fatal(msg)
		}
		out, msg := call(t, s, "query", map[string]any{"database": db, "sql": "SELECT body FROM items"})
		if raw, _ := json.Marshal(out); msg != "" || !bytes.Contains(raw, []byte("kept in "+db)) {
			t.Fatalf("read back %s: %s %s", db, raw, msg)
		}
	}
	if _, msg := call(t, s, "create_bucket", map[string]any{"name": "attachments"}); msg != "" {
		t.Fatal(msg)
	}
	if _, msg := call(t, s, "put_object", map[string]any{"bucket": "attachments", "key": "notes/a.md", "text": "# kept"}); msg != "" {
		t.Fatal(msg)
	}
	if out, msg := call(t, s, "get_object", map[string]any{"bucket": "attachments", "key": "notes/a.md"}); msg != "" || out["text"] != "# kept" {
		t.Fatalf("read back the object: %v %s", out, msg)
	}

	// The other workspace's token, through every door.
	o := e.mcp(other.Token)
	if out, msg := call(t, o, "list_databases", nil); msg != "" || len(out["databases"].([]any)) != 0 {
		t.Fatalf("the other workspace sees databases: %v", out)
	}
	if out, msg := call(t, o, "list_buckets", nil); msg != "" || len(out["buckets"].([]any)) != 0 {
		t.Fatalf("the other workspace sees buckets: %v", out)
	}
	for tool, args := range map[string]map[string]any{
		"query":         {"database": "memory", "sql": "SELECT * FROM items"},
		"execute":       {"database": "tasks", "sql": "DROP TABLE items"},
		"get_database":  {"database": "memory"},
		"get_object":    {"bucket": "attachments", "key": "notes/a.md"},
		"put_object":    {"bucket": "attachments", "key": "x", "text": "x"},
		"delete_object": {"bucket": "attachments", "key": "notes/a.md"},
		"list_objects":  {"bucket": "attachments"},
	} {
		if _, msg := call(t, o, tool, args); !strings.Contains(msg, "_not_found") {
			t.Fatalf("the other workspace's %s: %q", tool, msg)
		}
	}
	e.fails(e.sql(other.Token, "memory", "SELECT * FROM items"), 404, "database_not_found")
	e.fails(e.do(other.Token, "DELETE", pDB, "/v1/databases/tasks", nil), 404, "database_not_found")
	e.fails(e.do(other.Token, "GET", "", "/v1/buckets/attachments/object?key=notes/a.md", nil), 404, "bucket_not_found")
	wantErr(t, e.one(other.Token, "memory", "main", "SELECT * FROM items"), "database_not_found", "libSQL from the other workspace")
	e.s3Fails(e.s3(other, "GET", "/v1/s3/attachments/notes/a.md", nil), 404, "NoSuchBucket")
	e.s3Fails(e.s3(other, "DELETE", "/v1/s3/attachments/notes/a.md", nil), 404, "NoSuchBucket")

	// And the agent's own token through the other doors sees the same data.
	if r := e.ok(e.sql(agent.Token, "memory", "SELECT body FROM items"), 200); !bytes.Contains(r.body, []byte("kept in memory")) {
		t.Fatalf("REST: %s", r.body)
	}
	if res := e.libsql(agent.Token, "tasks", "main").run("SELECT body FROM items"); !strings.Contains(fmt.Sprint(rows(t, res)), "kept in tasks") {
		t.Fatalf("libSQL: %v", res)
	}
	if r := e.s3OK(e.s3(agent, "GET", "/v1/s3/attachments/notes/a.md", nil), 200); string(r.body) != "# kept" {
		t.Fatalf("S3: %s", r.body)
	}
}

// Audit entries stay in the workspace they happened in. Instance actions
// are in no workspace's log.
func TestWorkspaceAudit(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	entries := func(tok, path string) []map[string]any {
		var out []map[string]any
		for _, raw := range w.ok(w.do(tok, "GET", "", path, nil), 200).json(t)["data"].([]any) {
			out = append(out, raw.(map[string]any))
		}
		return out
	}
	for _, s := range []side{a, b} {
		got := entries(s.admin.Token, "/v1/audit?limit=200")
		if len(got) == 0 {
			t.Fatalf("%s has no audit entries", s.name)
		}
		for _, e := range got {
			if e["workspace_id"] != s.id || e["action"] == "workspace.create" {
				t.Fatalf("%s's audit log holds %v", s.name, e)
			}
		}
	}
	// Through S3 and MCP too.
	w.s3OK(w.s3(a.admin, "PUT", "/v1/s3/audited-s3", nil), 200)
	if _, msg := call(t, w.mcp(a.admin.Token), "create_database", map[string]any{"name": "audited-mcp"}); msg != "" {
		t.Fatal(msg)
	}
	newest := entries(a.admin.Token, "/v1/audit?limit=2")
	if newest[0]["action"] != "database.create" || newest[1]["action"] != "bucket.create" || newest[0]["workspace_id"] != a.id || newest[1]["workspace_id"] != a.id {
		t.Fatalf("newest entries: %v", newest)
	}
	for _, e := range entries(b.admin.Token, "/v1/audit?limit=200") {
		if e["workspace_id"] != b.id {
			t.Fatalf("B's log got %v", e)
		}
	}
	// The instance log has everything, and filters by workspace.
	all := entries(w.admin, "/v1/instance/audit?limit=200")
	seen := map[any]int{}
	for _, e := range all {
		seen[e["workspace_id"]]++
		if e["action"] == "workspace.create" && e["workspace_id"] != "" {
			t.Fatalf("an instance action was logged in a workspace: %v", e)
		}
	}
	if seen[a.id] == 0 || seen[b.id] == 0 || seen[""] != 2 {
		t.Fatalf("instance audit by workspace: %v", seen)
	}
	for _, ref := range []string{"acme", a.id} {
		for _, e := range entries(w.admin, "/v1/instance/audit?limit=200&workspace="+ref) {
			if e["workspace_id"] != a.id {
				t.Fatalf("filtered instance audit holds %v", e)
			}
		}
	}
	w.fails(w.do(w.admin, "GET", "/v1/instance/audit", "/v1/instance/audit?workspace=nope", nil), 404, "workspace_not_found")
	// The default workspace's own log is the instance admin's short URL.
	for _, e := range entries(w.admin, "/v1/audit?limit=200") {
		if e["workspace_id"] != w.defaultWS() {
			t.Fatalf("default workspace log holds %v", e)
		}
	}
}

// An instance token reaches a workspace through its qualified URL on every
// door, and a URL it presigns names the workspace, since its key belongs to
// none. An Idempotency-Key is kept per workspace.
func TestWorkspaceInstanceTokenDoors(t *testing.T) {
	w := newWSWorld(t, nil)
	a, b := w.a, w.b
	var inst key
	r := w.ok(w.do(w.admin, "POST", "/v1/instance/tokens", "/v1/instance/tokens", map[string]any{"name": "op", "access": "admin", "access_key": true}), 201)
	if err := json.Unmarshal(r.body, &inst); err != nil || inst.AKID == "" {
		t.Fatal(string(r.body))
	}
	for _, s := range []side{a, b} {
		for _, ref := range []string{s.name, s.id} {
			base := "/v1/workspaces/" + ref
			if r := w.ok(w.do(inst.Token, "POST", "", base+"/databases/notes/query", map[string]any{"sql": "SELECT v FROM t"}), 200); !bytes.Contains(r.body, []byte(s.secret)) {
				t.Fatalf("REST in %s: %s", ref, r.body)
			}
			h := &hrana{e: w.env, token: inst.Token, path: base + "/libsql/notes/main/v2"}
			if res := h.run("SELECT v FROM t"); !strings.Contains(fmt.Sprint(rows(t, res)), s.secret) {
				t.Fatalf("libSQL in %s: %v", ref, res)
			}
			if r := w.s3OK(w.s3(inst, "GET", base+"/s3/files/doc.txt", nil), 200); string(r.body) != s.secret {
				t.Fatalf("S3 in %s: %s", ref, r.body)
			}
			ms, err := w.mcpAt(base+"/mcp", inst.Token)
			if err != nil {
				t.Fatalf("MCP in %s: %v", ref, err)
			}
			out, msg := call(t, ms, "query", map[string]any{"database": "notes", "sql": "SELECT v FROM t"})
			if raw, _ := json.Marshal(out); msg != "" || !bytes.Contains(raw, []byte(s.secret)) {
				t.Fatalf("MCP in %s: %s %s", ref, raw, msg)
			}
			// A presigned URL made there opens there.
			p := w.ok(w.do(inst.Token, "POST", "", base+"/buckets/files/presign", map[string]any{"key": "doc.txt"}), 200).json(t)
			if u := p["url"].(string); !strings.Contains(u, "/v1/workspaces/"+s.id+"/s3/files/doc.txt") {
				t.Fatalf("presigned URL %s doesn't name workspace %s", u, s.id)
			} else if r := w.fetch("GET", u, nil); r.status != 200 || string(r.body) != s.secret {
				t.Fatalf("presigned URL in %s: %d %s", ref, r.status, r.body)
			}
		}
	}
	// On short URLs it is in the default workspace, where notes isn't.
	w.fails(w.sql(inst.Token, "notes", "SELECT 1"), 404, "database_not_found")
	w.s3Fails(w.s3(inst, "GET", "/v1/s3/files/doc.txt", nil), 404, "NoSuchBucket")
	// A workspace token's presigned URL stays short and opens in its workspace.
	p := w.ok(w.do(a.admin.Token, "POST", "/v1/buckets/{bucket}/presign", "/v1/buckets/files/presign", map[string]any{"key": "doc.txt"}), 200).json(t)
	if u := p["url"].(string); strings.Contains(u, "/v1/workspaces/") {
		t.Fatalf("a workspace token's presigned URL is qualified: %s", u)
	} else if r := w.fetch("GET", u, nil); r.status != 200 || string(r.body) != a.secret {
		t.Fatalf("presigned URL: %d %s", r.status, r.body)
	}

	// The same Idempotency-Key and body in two workspaces is two creates;
	// in one workspace it replays.
	body := map[string]any{"name": "idem"}
	first := w.ok(w.do(inst.Token, "POST", "", "/v1/workspaces/acme/databases", body, "Idempotency-Key", "k1"), 201).json(t)
	again := w.ok(w.do(inst.Token, "POST", "", "/v1/workspaces/acme/databases", body, "Idempotency-Key", "k1"), 201).json(t)
	other := w.ok(w.do(inst.Token, "POST", "", "/v1/workspaces/globex/databases", body, "Idempotency-Key", "k1"), 201).json(t)
	if first["id"] != again["id"] || first["workspace_id"] != a.id {
		t.Fatalf("replay in one workspace: %v then %v", first, again)
	}
	if other["id"] == first["id"] || other["workspace_id"] != b.id {
		t.Fatalf("the key replayed workspace A's response in B: %v", other)
	}
}

// Creating and updating workspaces, their limits on counts, and the token
// cap per workspace.
func TestWorkspaceManagement(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.MaxTokensPerWorkspace = 3; c.MaxWorkspaces = 4 })
	const pWS, pOne = "/v1/workspaces", "/v1/workspaces/{ws}"
	def := e.ok(e.do(e.admin, "GET", "/v1/workspace", "/v1/workspace", nil), 200).json(t)
	if def["name"] != "default" || def["default"] != true || def["id"] != e.defaultWS() || def["limits"].(map[string]any)["max_databases"] != 0.0 {
		t.Fatalf("default workspace: %v", def)
	}
	ws := e.ok(e.do(e.admin, "POST", pWS, pWS, map[string]any{"name": "small", "max_databases": 1, "max_buckets": 1}), 201).json(t)
	if l := ws["limits"].(map[string]any); l["max_databases"] != 1.0 || l["max_buckets"] != 1.0 || l["max_database_bytes"] != float64(2<<30) || l["max_object_bytes"] != float64(5<<30) {
		t.Fatalf("limits: %v", l)
	}
	e.fails(e.do(e.admin, "POST", pWS, pWS, map[string]any{"name": "small"}), 409, "name_taken")
	e.fails(e.do(e.admin, "POST", pWS, pWS, map[string]any{"name": "default"}), 409, "name_taken")
	if r := e.do(e.admin, "POST", pWS, pWS, map[string]any{"name": "Bad_Name"}); r.status != 422 {
		t.Fatalf("bad name: %d %s", r.status, r.body)
	}
	adm := e.ok(e.do(e.admin, "POST", "", "/v1/workspaces/small/tokens", map[string]any{"name": "admin", "access": "admin"}), 201).json(t)["token"].(string)

	// Count limits, in the trash too, and only for this workspace.
	e.ok(e.do(adm, "POST", pDBs, "/v1/databases", map[string]any{"name": "one"}), 201)
	e.fails(e.do(adm, "POST", pDBs, "/v1/databases", map[string]any{"name": "two"}), 409, "limit_reached")
	e.ok(e.do(adm, "DELETE", pDB, "/v1/databases/one", nil), 200)
	e.fails(e.do(adm, "POST", pDBs, "/v1/databases", map[string]any{"name": "two"}), 409, "limit_reached")
	e.ok(e.do(adm, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "one"}), 201)
	e.fails(e.do(adm, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "two"}), 409, "limit_reached")
	for _, db := range []string{"a", "b", "c"} {
		e.createDB(db) // the default workspace has no limit of its own
	}
	usage := e.ok(e.do(adm, "GET", "/v1/workspace", "/v1/workspace", nil), 200).json(t)["usage"].(map[string]any)
	if usage["databases"] != 1.0 || usage["buckets"] != 1.0 || usage["tokens"] != 1.0 {
		t.Fatalf("usage: %v", usage)
	}
	// The instance admin raises the limit; the workspace's own admin can't.
	e.fails(e.do(adm, "PATCH", pOne, "/v1/workspaces/small", map[string]any{"max_databases": 5}), 403, "forbidden")
	up := e.ok(e.do(e.admin, "PATCH", pOne, "/v1/workspaces/small", map[string]any{"max_databases": 5, "name": "bigger"}), 200).json(t)
	if up["name"] != "bigger" || up["limits"].(map[string]any)["max_databases"] != 5.0 || up["limits"].(map[string]any)["max_buckets"] != 1.0 {
		t.Fatalf("updated: %v", up)
	}
	e.ok(e.do(adm, "POST", pDBs, "/v1/databases", map[string]any{"name": "two"}), 201)
	// A rename doesn't break the workspace's tokens or its id.
	if cur := e.ok(e.do(adm, "GET", "/v1/workspace", "/v1/workspace", nil), 200).json(t); cur["name"] != "bigger" || cur["id"] != ws["id"] {
		t.Fatalf("after the rename: %v", cur)
	}
	e.fails(e.do(e.admin, "GET", pOne, "/v1/workspaces/small", nil), 404, "workspace_not_found")
	e.fails(e.do(e.admin, "PATCH", pOne, "/v1/workspaces/bigger", map[string]any{"name": "default"}), 409, "name_taken")

	// The token cap: three live tokens, then no more until one is revoked.
	e.ok(e.do(adm, "POST", pTokens, "/v1/tokens", map[string]any{"name": "t2", "access": "read"}), 201)
	third := e.ok(e.do(adm, "POST", pTokens, "/v1/tokens", map[string]any{"name": "t3", "access": "read"}), 201).json(t)
	e.fails(e.do(adm, "POST", pTokens, "/v1/tokens", map[string]any{"name": "t4", "access": "read"}), 409, "limit_reached")
	e.ok(e.do(adm, "DELETE", pToken, "/v1/tokens/"+third["id"].(string), nil), 204)
	e.ok(e.do(adm, "POST", pTokens, "/v1/tokens", map[string]any{"name": "t4", "access": "read"}), 201)
	// Instance tokens and other workspaces aren't counted against it.
	e.ok(e.do(e.admin, "POST", "/v1/instance/tokens", "/v1/instance/tokens", map[string]any{"name": "op", "access": "read"}), 201)

	// The workspace cap.
	e.ok(e.do(e.admin, "POST", pWS, pWS, map[string]any{"name": "third"}), 201)
	e.ok(e.do(e.admin, "POST", pWS, pWS, map[string]any{"name": "fourth"}), 201)
	e.fails(e.do(e.admin, "POST", pWS, pWS, map[string]any{"name": "fifth"}), 409, "limit_reached")
	// An instance token below admin sees workspaces and can't make one.
	ro := e.ok(e.do(e.admin, "POST", "/v1/instance/tokens", "/v1/instance/tokens", map[string]any{"name": "ro", "access": "read"}), 201).json(t)["token"].(string)
	if got := len(e.ok(e.do(ro, "GET", pWS, pWS, nil), 200).json(t)["data"].([]any)); got != 4 {
		t.Fatalf("instance read token lists %d workspaces", got)
	}
	e.fails(e.do(ro, "POST", pWS, pWS, map[string]any{"name": "x"}), 403, "forbidden")
	e.fails(e.do(ro, "POST", "", "/v1/workspaces/bigger/databases", map[string]any{"name": "x"}), 403, "forbidden")
	e.ok(e.do(ro, "GET", "", "/v1/workspaces/bigger/databases", nil), 200)
}

// noFollow sends a request without following redirects.
func (e *env) noFollow(token, method, path string, body []byte) resp {
	e.t.Helper()
	req, err := http.NewRequest(method, e.http.URL+path, bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	return resp{status: res.StatusCode, header: res.Header, body: buf.Bytes()}
}

// A qualified URL never redirects to its short form. The router cleans a
// path with empty, "." or ".." segments by redirecting, and the short path
// would send an instance token to the default workspace.
func TestWorkspaceQualifiedURLNeverRedirects(t *testing.T) {
	w := newWSWorld(t, nil)
	// A notes database in the default workspace too, holding something else.
	w.createDB("notes")
	w.ok(w.sql(w.admin, "notes", "CREATE TABLE t(v)"), 200)
	w.ok(w.sql(w.admin, "notes", "INSERT INTO t VALUES ('secret of the default workspace')"), 200)
	query, _ := json.Marshal(map[string]any{"sql": "SELECT v FROM t"})
	write, _ := json.Marshal(map[string]any{"sql": "INSERT INTO t VALUES ('landed in the wrong workspace')"})
	pipeline, _ := json.Marshal(map[string]any{"baton": nil, "requests": []any{exec("SELECT v FROM t"), closeReq}})
	for _, path := range []string{
		"/v1/workspaces/acme/databases//notes/query",
		"/v1/workspaces/acme/databases/./notes/query",
		"/v1/workspaces/acme/databases/x/../notes/query",
		"/v1/workspaces/acme/databases/notes/query/",
		"/v1/workspaces/acme/databases/notes//query",
		"/v1/workspaces/acme/databases/%2e/notes/query",
		"/v1/workspaces/acme/libsql//notes/main/v2/pipeline",
		"/v1/workspaces/acme/libsql/notes/main/./v2/pipeline",
		"/v1/workspaces/acme/mcp/",
		"/v1/workspaces/acme/mcp/../databases",
		"/v1/workspaces/acme/tokens/",
		"/v1/workspaces/acme/buckets//files",
	} {
		for _, tok := range []string{w.admin, w.a.admin.Token, w.b.admin.Token} {
			for _, body := range [][]byte{query, write, pipeline} {
				for _, method := range []string{"POST", "GET", "DELETE"} {
					r := w.noFollow(tok, method, path, body)
					if r.status >= 300 && r.status < 400 || r.header.Get("Location") != "" {
						t.Fatalf("%s %s redirects to %q (%d)", method, path, r.header.Get("Location"), r.status)
					}
					if r.status < 400 || bytes.Contains(r.body, []byte("secret of")) {
						t.Fatalf("%s %s: %d %s", method, path, r.status, r.body)
					}
					// And a client that does follow redirects ends up nowhere.
					if f := w.do(tok, method, "", path, body); f.status < 400 || bytes.Contains(f.body, []byte("secret of")) {
						t.Fatalf("%s %s, following redirects: %d %s", method, path, f.status, f.body)
					}
				}
			}
		}
	}
	// A path the router cleans before the workspace's part ends is still a
	// qualified URL after cleaning: the redirect keeps the workspace.
	if r := w.noFollow(w.admin, "POST", "/v1/workspaces/acme//databases/notes/query", query); r.status != 307 || r.header.Get("Location") != "/v1/workspaces/acme/databases/notes/query" {
		t.Fatalf("a slash after the workspace: %d to %q", r.status, r.header.Get("Location"))
	}
	// Nothing was written anywhere.
	for name, tok := range map[string]string{"default": w.admin, "acme": w.a.admin.Token, "globex": w.b.admin.Token} {
		if r := w.ok(w.sql(tok, "notes", "SELECT count(*) AS n FROM t"), 200); !bytes.Contains(r.body, []byte(`"value":"1"`)) {
			t.Fatalf("%s/notes changed: %s", name, r.body)
		}
	}
	// The canonical qualified URL still works.
	if r := w.ok(w.do(w.admin, "POST", "", "/v1/workspaces/acme/databases/notes/query", query), 200); !bytes.Contains(r.body, []byte(w.a.secret)) {
		t.Fatalf("canonical qualified URL: %s", r.body)
	}
}

// 17.9 row 9: a workspace's own limits on bytes. A write past them is
// refused with nothing partial kept, and other workspaces aren't affected.
func TestWorkspaceIsolationQuotas(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.RateLimit, c.RateBurst = 100000, 100000 })
	mk := func(name string, limits map[string]any) key {
		limits["name"] = name
		e.ok(e.do(e.admin, "POST", "/v1/workspaces", "/v1/workspaces", limits), 201)
		var k key
		r := e.ok(e.do(e.admin, "POST", "", "/v1/workspaces/"+name+"/tokens", map[string]any{"name": "admin", "access": "admin", "access_key": true}), 201)
		if err := json.Unmarshal(r.body, &k); err != nil {
			t.Fatal(err)
		}
		e.ok(e.do(k.Token, "POST", pDBs, "/v1/databases", map[string]any{"name": "app"}), 201)
		e.ok(e.sql(k.Token, "app", "CREATE TABLE t(v)"), 200)
		e.ok(e.do(k.Token, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "files"}), 201)
		return k
	}
	tight := mk("tight", map[string]any{"max_database_bytes": 400_000, "max_object_bytes": 100_000})
	roomy := mk("roomy", map[string]any{})
	big := "INSERT INTO t VALUES (zeroblob(300000))"
	count := func(k key) string {
		r := e.ok(e.sql(k.Token, "app", "SELECT count(*) FROM t"), 200).json(t)
		return r["rows"].([]any)[0].([]any)[0].(map[string]any)["value"].(string)
	}

	// Databases: a small write fits, one past the limit doesn't, and the
	// refused one leaves no rows behind.
	e.ok(e.sql(tight.Token, "app", "INSERT INTO t VALUES (zeroblob(1000))"), 200)
	e.fails(e.sql(tight.Token, "app", big), 507, "workspace_quota_exceeded")
	e.fails(e.do(tight.Token, "POST", pBatch, "/v1/databases/app/batch", map[string]any{"statements": []any{
		map[string]any{"sql": "INSERT INTO t VALUES (1)"}, map[string]any{"sql": big}}}), 507, "workspace_quota_exceeded")
	wantErr(t, e.one(tight.Token, "app", "main", big), "workspace_quota_exceeded", "libSQL past the workspace limit")
	if _, msg := call(t, e.mcp(tight.Token), "execute", map[string]any{"database": "app", "sql": big}); !strings.Contains(msg, "workspace_quota_exceeded") {
		t.Fatalf("MCP past the workspace limit: %q", msg)
	}
	if n := count(tight); n != "1" {
		t.Fatalf("a refused write left rows: %s", n)
	}
	// Copies count too: a second database's worth of branches doesn't fit.
	e.ok(e.sql(tight.Token, "app", "INSERT INTO t VALUES (zeroblob(150000))"), 200)
	e.fails(e.do(tight.Token, "POST", pBranches, "/v1/databases/app/branches", map[string]any{"name": "copy"}), 507, "workspace_quota_exceeded")
	if got := len(e.ok(e.do(tight.Token, "GET", pBranches, "/v1/databases/app/branches", nil), 200).json(t)["data"].([]any)); got != 1 {
		t.Fatalf("a refused branch was kept: %d branches", got)
	}
	// The other workspace isn't affected by any of it.
	e.ok(e.sql(roomy.Token, "app", big), 200)
	e.ok(e.do(roomy.Token, "POST", pBranches, "/v1/databases/app/branches", map[string]any{"name": "copy"}), 201)

	// Objects, through REST, the S3 API and MCP.
	blob := bytes.Repeat([]byte("x"), 80_000)
	e.ok(e.do(tight.Token, "PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/files/object?key=a", blob), 200)
	e.fails(e.do(tight.Token, "PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/files/object?key=b", blob), 507, "workspace_quota_exceeded")
	e.s3Fails(e.s3(tight, "PUT", "/v1/s3/files/c", blob), 403, "QuotaExceeded")
	if _, msg := call(t, e.mcp(tight.Token), "put_object", map[string]any{"bucket": "files", "key": "d", "text": string(blob)}); !strings.Contains(msg, "workspace_quota_exceeded") {
		t.Fatalf("MCP put past the workspace limit: %q", msg)
	}
	// The limit is on the workspace, not the bucket: a second bucket shares it.
	e.ok(e.do(tight.Token, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "more"}), 201)
	e.fails(e.do(tight.Token, "PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/more/object?key=e", blob), 507, "workspace_quota_exceeded")
	// Replacing an object counts the difference only.
	e.ok(e.do(tight.Token, "PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/files/object?key=a", blob), 200)
	list := e.ok(e.do(tight.Token, "GET", "/v1/buckets/{bucket}/objects", "/v1/buckets/files/objects", nil), 200).json(t)["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("refused uploads were kept: %v", list)
	}
	for _, k := range []string{"b", "c", "d"} {
		e.fails(e.do(tight.Token, "GET", "", "/v1/buckets/files/object?key="+k, nil), 404, "object_not_found")
	}
	e.ok(e.do(roomy.Token, "PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/files/object?key=a", blob), 200)
	e.ok(e.do(roomy.Token, "PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/files/object?key=b", blob), 200)

	// What the workspace sees matches what was kept, and its own admin
	// can't lift the limit; the instance admin can.
	usage := e.ok(e.do(tight.Token, "GET", "/v1/workspace", "/v1/workspace", nil), 200).json(t)["usage"].(map[string]any)
	if usage["object_bytes"] != 80_000.0 || usage["database_bytes"].(float64) <= 150_000 || usage["database_bytes"].(float64) > 400_000 {
		t.Fatalf("usage: %v", usage)
	}
	e.fails(e.do(tight.Token, "PATCH", "/v1/workspaces/{ws}", "/v1/workspaces/tight", map[string]any{"max_database_bytes": 0}), 403, "forbidden")
	e.ok(e.do(e.admin, "PATCH", "/v1/workspaces/{ws}", "/v1/workspaces/tight", map[string]any{"max_database_bytes": 0, "max_object_bytes": 0}), 200)
	e.ok(e.sql(tight.Token, "app", big), 200)
	e.ok(e.do(tight.Token, "PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/files/object?key=b", blob), 200)
}

// A workspace token that tries an instance action is refused, and the
// refusal is logged in its own workspace. It can't push the instance's
// history out of the instance log.
func TestWorkspaceCannotFloodInstanceAudit(t *testing.T) {
	w := newWSWorld(t, func(c *config.Config) { c.AuditMaxRows = 5 })
	instance := func() []any {
		return w.ok(w.do(w.admin, "GET", "", "/v1/instance/audit?limit=200", nil), 200).json(t)["data"].([]any)
	}
	creates := func() int {
		n := 0
		for _, raw := range instance() {
			if e := raw.(map[string]any); e["action"] == "workspace.create" && e["workspace_id"] == "" {
				n++
			}
		}
		return n
	}
	if creates() != 2 {
		t.Fatalf("instance log at the start: %v", instance())
	}
	for i := 0; i < 20; i++ {
		w.fails(w.do(w.a.read.Token, "POST", "/v1/workspaces", "/v1/workspaces", map[string]any{"name": "x"}), 403, "forbidden")
		w.fails(w.do(w.a.admin.Token, "POST", "/v1/instance/tokens", "/v1/instance/tokens", map[string]any{"name": "x", "access": "admin"}), 403, "forbidden")
		w.fails(w.do(w.a.dbAdmin, "PATCH", "/v1/workspaces/{ws}", "/v1/workspaces/globex", map[string]any{"name": "x"}), 403, "forbidden")
	}
	if creates() != 2 {
		t.Fatalf("a workspace token pushed the instance's history out: %v", instance())
	}
	// The refusals are in workspace A's own log, capped there.
	own := w.ok(w.do(w.a.admin.Token, "GET", "", "/v1/audit?limit=200", nil), 200).json(t)["data"].([]any)
	if len(own) != 5 {
		t.Fatalf("workspace A's log holds %d entries, want its cap of 5", len(own))
	}
	for _, raw := range own {
		if e := raw.(map[string]any); e["result"] != "error" || e["code"] != "forbidden" || e["workspace_id"] != w.a.id {
			t.Fatalf("workspace A's log: %v", e)
		}
	}
	// B's log wasn't touched by A's noise.
	if got := len(w.ok(w.do(w.b.admin.Token, "GET", "", "/v1/audit?limit=200", nil), 200).json(t)["data"].([]any)); got != 5 {
		t.Fatalf("workspace B's log holds %d entries", got)
	}
}

// Minting and revoking tokens in a loop can't grow meta.db without bound:
// revoked tokens count against a hard cap until they are cleared, and an
// instance cap doesn't tell a workspace how full the instance is.
func TestWorkspaceBoundedTokensAndCapMessages(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.RateLimit, c.RateBurst = 100000, 100000
		c.MaxTokensPerWorkspace, c.MaxDatabases, c.MaxBuckets = 2, 2, 2
	})
	e.ok(e.do(e.admin, "POST", "/v1/workspaces", "/v1/workspaces", map[string]any{"name": "loop"}), 201)
	adm := e.ok(e.do(e.admin, "POST", "", "/v1/workspaces/loop/tokens", map[string]any{"name": "admin", "access": "admin"}), 201).json(t)["token"].(string)
	minted := 1
	for {
		r := e.do(adm, "POST", pTokens, "/v1/tokens", map[string]any{"name": "x", "access": "read"})
		if r.status != 201 {
			e.fails(r, 409, "limit_reached")
			break
		}
		minted++
		e.ok(e.do(adm, "DELETE", pToken, "/v1/tokens/"+r.json(t)["id"].(string), nil), 204)
		if minted > 100 {
			t.Fatal("minting and revoking never hit a cap")
		}
	}
	if minted != 20 {
		t.Fatalf("the workspace held %d token rows before the cap, want 20 (10 times its 2 live tokens)", minted)
	}
	// Once the revoked ones are cleared, it mints again.
	if n, err := e.srv.Engine.Meta().PurgeRevokedTokens(context.Background(), time.Now().Add(time.Hour)); err != nil || n != 19 {
		t.Fatalf("purged %d revoked tokens, %v", n, err)
	}
	e.ok(e.do(adm, "POST", pTokens, "/v1/tokens", map[string]any{"name": "x", "access": "read"}), 201)
	// The service token survives the purge even when revoked rows go.
	e.ok(e.do(e.admin, "GET", "/v1/workspaces", "/v1/workspaces", nil), 200)

	// The instance's own caps: the default workspace fills them, and the
	// other workspace is told only that there is no room, not how many
	// exist or how many the instance allows.
	e.createDB("one")
	e.createDB("two")
	e.ok(e.do(e.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "one"}), 201)
	e.ok(e.do(e.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "two"}), 201)
	for _, r := range []resp{
		e.do(adm, "POST", pDBs, "/v1/databases", map[string]any{"name": "mine"}),
		e.do(adm, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "mine"}),
	} {
		e.fails(r, 409, "limit_reached")
		if title := r.json(t)["title"].(string); strings.ContainsAny(title, "0123456789") {
			t.Fatalf("the instance cap's message carries a number: %q", title)
		}
	}
}
