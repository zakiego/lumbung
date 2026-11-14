package api_test

import (
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
	"time"
)

// grantsWorld is two databases and two buckets, and one token that reaches
// database "notes" (write) and bucket "files" (read) and nothing else.
type grantsWorld struct {
	*env
	agent key
}

func newGrantsWorld(t *testing.T) *grantsWorld {
	e := newEnv(t, nil)
	w := &grantsWorld{env: e}
	for _, db := range []string{"notes", "victim"} {
		e.createDB(db)
		e.ok(e.sql(e.admin, db, "CREATE TABLE t(x)"), 200)
		e.ok(e.sql(e.admin, db, "INSERT INTO t VALUES ('row of "+db+"')"), 200)
	}
	for _, b := range []string{"files", "vault"} {
		e.ok(e.do(e.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": b}), 201)
		e.ok(e.putObject(e.admin, b, "doc.txt", "secret of "+b), 200)
	}
	w.agent = e.accessKey(map[string]any{"name": "agent", "grants": []map[string]any{
		{"database": "notes", "access": "write"},
		{"bucket": "files", "access": "read"},
	}})
	return w
}

// PLAN 16.5, the grants row, through REST: a token with grants reaches what
// its grants name, at each grant's own level, and nothing else.
func TestGrantsIsolationREST(t *testing.T) {
	w := newGrantsWorld(t)
	tok := w.agent.Token

	// Its own database, at write.
	w.ok(w.sql(tok, "notes", "INSERT INTO t VALUES ('from the agent')"), 200)
	w.ok(w.do(tok, "GET", pDB, "/v1/databases/notes", nil), 200)
	// Admin work on it is past its level.
	w.fails(w.do(tok, "DELETE", pDB, "/v1/databases/notes", nil), 403, "forbidden")
	w.fails(w.do(tok, "POST", pDBs, "/v1/databases", map[string]any{"name": "more"}), 403, "forbidden")

	// Its own bucket, at read: the database grant's write level doesn't carry over.
	r := w.ok(w.do(tok, "GET", "", "/v1/buckets/files/object?key=doc.txt", nil), 200)
	if string(r.body) != "secret of files" {
		t.Fatalf("read its bucket: %q", r.body)
	}
	w.fails(w.putObject(tok, "files", "new.txt", "x"), 403, "forbidden")
	w.fails(w.do(tok, "DELETE", "/v1/buckets/{bucket}/object", "/v1/buckets/files/object?key=doc.txt", nil), 403, "forbidden")
	w.fails(w.do(tok, "PATCH", "/v1/buckets/{bucket}", "/v1/buckets/files", map[string]any{"public_read": true}), 403, "forbidden")
	w.fails(w.do(tok, "POST", "/v1/buckets/{bucket}/presign", "/v1/buckets/files/presign", map[string]any{"key": "x", "method": "PUT"}), 403, "forbidden")
	w.fails(w.do(tok, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "more-files"}), 403, "forbidden")

	// The database and the bucket it has no grant for: the same answer as
	// ones that don't exist.
	w.fails(w.sql(tok, "victim", "SELECT * FROM t"), 404, "database_not_found")
	w.fails(w.do(tok, "GET", pDB, "/v1/databases/victim", nil), 404, "database_not_found")
	w.fails(w.do(tok, "GET", pBranches, "/v1/databases/victim/branches", nil), 404, "database_not_found")
	for _, o := range []struct {
		method, template, path string
		body                   any
	}{
		{"GET", "/v1/buckets/{bucket}", "/v1/buckets/vault", nil},
		{"GET", "/v1/buckets/{bucket}/objects", "/v1/buckets/vault/objects", nil},
		{"GET", "/v1/buckets/{bucket}/object", "/v1/buckets/vault/object?key=doc.txt", nil},
		{"PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/vault/object?key=new.txt", []byte("x")},
		{"DELETE", "/v1/buckets/{bucket}/object", "/v1/buckets/vault/object?key=doc.txt", nil},
		{"POST", "/v1/buckets/{bucket}/presign", "/v1/buckets/vault/presign", map[string]any{"key": "doc.txt"}},
		{"POST", "/v1/buckets/{bucket}/copy", "/v1/buckets/vault/copy", map[string]any{"from_bucket": "files", "from_key": "doc.txt", "key": "c.txt"}},
	} {
		got := w.do(tok, o.method, o.template, o.path, o.body)
		missing := w.do(tok, o.method, o.template, strings.Replace(o.path, "vault", "nowhere", 1), o.body)
		if got.status != 404 || got.code(t) != "bucket_not_found" || missing.status != got.status || missing.code(t) != got.code(t) {
			t.Fatalf("%s %s: %d %s, missing bucket: %d %s", o.method, o.path, got.status, got.body, missing.status, missing.body)
		}
	}
	// Copying out of a bucket it can't see into one it can only read.
	w.fails(w.do(tok, "POST", "/v1/buckets/{bucket}/copy", "/v1/buckets/files/copy", map[string]any{"from_bucket": "vault", "from_key": "doc.txt", "key": "c.txt"}), 403, "forbidden")

	// Lists show only what the grants name.
	dbs := w.ok(w.do(tok, "GET", pDBs, "/v1/databases", nil), 200).json(t)["data"].([]any)
	if len(dbs) != 1 || dbs[0].(map[string]any)["name"] != "notes" {
		t.Fatalf("lists databases %v", dbs)
	}
	buckets := w.ok(w.do(tok, "GET", "/v1/buckets", "/v1/buckets", nil), 200).json(t)["data"].([]any)
	if len(buckets) != 1 || buckets[0].(map[string]any)["name"] != "files" {
		t.Fatalf("lists buckets %v", buckets)
	}
	// It has no admin grant, so it can't see or make tokens.
	w.fails(w.do(tok, "GET", pTokens, "/v1/tokens", nil), 403, "forbidden")
	w.fails(w.do(tok, "POST", pTokens, "/v1/tokens", map[string]any{"name": "child", "database": "notes", "access": "read"}), 403, "forbidden")

	// The other database and bucket are untouched.
	if got := w.ok(w.sql(w.admin, "victim", "SELECT count(*) FROM t"), 200).json(t)["rows"].([]any)[0].([]any)[0].(map[string]any)["value"]; fmt.Sprint(got) != "1" {
		t.Fatalf("victim has %v rows", got)
	}
	if r := w.ok(w.do(w.admin, "GET", "", "/v1/buckets/vault/object?key=doc.txt", nil), 200); string(r.body) != "secret of vault" {
		t.Fatalf("vault changed: %q", r.body)
	}
	w.fails(w.do(w.admin, "GET", "/v1/buckets/{bucket}/object", "/v1/buckets/vault/object?key=new.txt", nil), 404, "object_not_found")
}

// The same row through the S3 API, signed with the token's access key.
func TestGrantsIsolationS3(t *testing.T) {
	w := newGrantsWorld(t)
	k := w.agent
	r := w.s3OK(w.s3(k, "GET", "/v1/s3/files/doc.txt", nil), 200)
	if string(r.body) != "secret of files" {
		t.Fatalf("read its bucket: %q", r.body)
	}
	// Read only: no write, delete or presigned write.
	w.s3Fails(w.s3(k, "PUT", "/v1/s3/files/new.txt", []byte("x")), 403, "AccessDenied")
	w.s3Fails(w.s3(k, "DELETE", "/v1/s3/files/doc.txt", nil), 403, "AccessDenied")
	w.s3Fails(w.fetch("PUT", w.presign(k, "PUT", "/v1/s3/files/new.txt", time.Minute, time.Now()), []byte("x")), 403, "AccessDenied")
	// The bucket it has no grant for looks like one that doesn't exist.
	for _, op := range bucketOps("vault") {
		got := w.s3(k, op.method, op.path, op.body, op.hdr...)
		missing := w.s3(k, op.method, strings.Replace(op.path, "vault", "nowhere", 1), op.body, op.hdr...)
		if got.status != 404 || got.status != missing.status || s3Code(got) != s3Code(missing) {
			t.Fatalf("%s on another bucket: %d %s, missing bucket: %d %s", op.name, got.status, s3Code(got), missing.status, s3Code(missing))
		}
	}
	w.s3Fails(w.fetch("GET", w.presign(k, "GET", "/v1/s3/vault/doc.txt", time.Minute, time.Now()), nil), 404, "NoSuchBucket")
	w.s3Fails(w.s3(k, "PUT", "/v1/s3/files/stolen.txt", nil, "x-amz-copy-source", "vault/doc.txt"), 403, "AccessDenied")
	var list struct {
		Names []string `xml:"Buckets>Bucket>Name"`
	}
	_ = xml.Unmarshal(w.s3OK(w.s3(k, "GET", "/v1/s3", nil), 200).body, &list)
	if fmt.Sprint(list.Names) != "[files]" {
		t.Fatalf("lists %v", list.Names)
	}
}

// The same row through the libSQL endpoint and the MCP tools.
func TestGrantsIsolationLibSQLAndMCP(t *testing.T) {
	w := newGrantsWorld(t)
	tok := w.agent.Token

	if r := w.one(tok, "notes", "main", "INSERT INTO t VALUES ('over libsql')"); r["type"] != "ok" {
		t.Fatalf("write its database: %v", r)
	}
	wantErr(t, w.one(tok, "victim", "main", "SELECT * FROM t"), "database_not_found", "libsql on another database")

	s := w.mcp(tok)
	out, msg := call(t, s, "list_databases", nil)
	if msg != "" || len(out["databases"].([]any)) != 1 {
		t.Fatalf("list_databases: %v %s", out, msg)
	}
	out, msg = call(t, s, "list_buckets", nil)
	if msg != "" || len(out["buckets"].([]any)) != 1 {
		t.Fatalf("list_buckets: %v %s", out, msg)
	}
	if _, msg := call(t, s, "execute", map[string]any{"database": "notes", "sql": "INSERT INTO t VALUES ('over mcp')"}); msg != "" {
		t.Fatalf("execute on its database: %s", msg)
	}
	if _, msg := call(t, s, "get_object", map[string]any{"bucket": "files", "key": "doc.txt"}); msg != "" {
		t.Fatalf("get_object on its bucket: %s", msg)
	}
	for tool, c := range map[string]struct {
		args map[string]any
		want string
	}{
		"query":           {map[string]any{"database": "victim", "sql": "SELECT * FROM t"}, "database_not_found"},
		"execute":         {map[string]any{"database": "victim", "sql": "DELETE FROM t"}, "database_not_found"},
		"get_object":      {map[string]any{"bucket": "vault", "key": "doc.txt"}, "bucket_not_found"},
		"list_objects":    {map[string]any{"bucket": "vault"}, "bucket_not_found"},
		"put_object":      {map[string]any{"bucket": "files", "key": "new.txt", "text": "x"}, "forbidden"},
		"delete_object":   {map[string]any{"bucket": "files", "key": "doc.txt"}, "forbidden"},
		"create_database": {map[string]any{"name": "more"}, "forbidden"},
		"create_bucket":   {map[string]any{"name": "more-files"}, "forbidden"},
		"delete_branch":   {map[string]any{"database": "notes", "branch": "main"}, "forbidden"},
	} {
		if _, msg := call(t, s, tool, c.args); !strings.Contains(msg, c.want) {
			t.Errorf("%s: %q, want %s", tool, msg, c.want)
		}
	}
}

// A grant limited to one branch reaches only that branch, next to a grant
// for another database as a whole.
func TestGrantsBranchLimit(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	e.createDB("logs")
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)
	e.ok(e.sql(e.admin, "logs", "CREATE TABLE t(x)"), 200)
	e.ok(e.do(e.admin, "POST", pBranches, "/v1/databases/app/branches", map[string]any{"name": "dev"}), 201)
	tok, _ := e.token(map[string]any{"name": "dev", "grants": []map[string]any{
		{"database": "app", "branch": "dev", "access": "write"},
		{"database": "logs", "access": "read"},
	}})
	e.ok(e.do(tok, "POST", pBQuery, "/v1/databases/app/branches/dev/query", map[string]any{"sql": "INSERT INTO t VALUES (1)"}), 200)
	e.fails(e.sql(tok, "app", "SELECT * FROM t"), 403, "forbidden")
	e.fails(e.do(tok, "GET", pBranch, "/v1/databases/app/branches/nope", nil), 403, "forbidden")
	e.fails(e.do(tok, "GET", pDB, "/v1/databases/app", nil), 403, "forbidden")
	// Two branches of one database, each at its own level.
	e.ok(e.do(e.admin, "POST", pBranches, "/v1/databases/app/branches", map[string]any{"name": "staging"}), 201)
	two, _ := e.token(map[string]any{"name": "two", "grants": []map[string]any{
		{"database": "app", "branch": "dev", "access": "write"},
		{"database": "app", "branch": "staging", "access": "read"},
	}})
	e.ok(e.do(two, "POST", pBQuery, "/v1/databases/app/branches/dev/query", map[string]any{"sql": "INSERT INTO t VALUES (2)"}), 200)
	e.ok(e.do(two, "POST", pBQuery, "/v1/databases/app/branches/staging/query", map[string]any{"sql": "SELECT * FROM t"}), 200)
	e.fails(e.do(two, "POST", pBQuery, "/v1/databases/app/branches/staging/query", map[string]any{"sql": "INSERT INTO t VALUES (2)"}), 403, "read_only")
	e.fails(e.sql(two, "app", "SELECT * FROM t"), 403, "forbidden")
	// A branch admin minting tokens can't use the answer to learn branch
	// names: a missing branch and someone else's look the same.
	devAdmin, _ := e.token(map[string]any{"name": "dev-admin", "database": "app", "branch": "dev", "access": "admin"})
	for _, branch := range []string{"staging", "nope"} {
		e.fails(e.do(devAdmin, "POST", pTokens, "/v1/tokens", map[string]any{"name": "probe", "database": "app", "branch": branch, "access": "read"}), 403, "forbidden")
		e.fails(e.do(devAdmin, "POST", pTokens, "/v1/tokens", map[string]any{"name": "probe", "grants": []map[string]any{
			{"database": "app", "branch": "dev", "access": "read"}, {"database": "app", "branch": branch, "access": "read"},
		}}), 403, "forbidden")
	}
	e.ok(e.do(devAdmin, "POST", pTokens, "/v1/tokens", map[string]any{"name": "child", "database": "app", "branch": "dev", "access": "write"}), 201)
	// The whole-database grant still works, at its own level.
	e.ok(e.sql(tok, "logs", "SELECT * FROM t"), 200)
	e.ok(e.do(tok, "GET", pDB, "/v1/databases/logs", nil), 200)
	e.fails(e.sql(tok, "logs", "INSERT INTO t VALUES (1)"), 403, "read_only")
}

func TestGrantsTokenRules(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("notes")
	e.createDB("other")
	e.ok(e.do(e.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "files"}), 201)
	create := func(token string, body map[string]any) resp {
		return e.do(token, "POST", pTokens, "/v1/tokens", body)
	}
	grants := func(g ...map[string]any) map[string]any { return map[string]any{"name": "t", "grants": g} }

	// What comes back names every grant, and the level shown is the highest.
	m := e.ok(create(e.admin, grants(map[string]any{"database": "notes", "access": "read"}, map[string]any{"bucket": "files", "access": "write"})), 201).json(t)
	if m["scope"] != "grants" || m["access"] != "write" || m["database_id"] != "" || m["bucket_id"] != "" || len(m["grants"].([]any)) != 2 {
		t.Fatalf("grants token: %v", m)
	}
	first := m["grants"].([]any)[0].(map[string]any)
	if !strings.HasPrefix(first["database_id"].(string), "db_") || first["access"] != "read" || first["bucket_id"] != "" {
		t.Fatalf("first grant: %v", first)
	}
	// One grant is a plain database token.
	m = e.ok(create(e.admin, grants(map[string]any{"database": "notes", "access": "read"})), 201).json(t)
	if m["scope"] != "database" || m["access"] != "read" || len(m["grants"].([]any)) != 1 {
		t.Fatalf("one grant: %v", m)
	}
	// The old shape still works and lists its one grant. Without a target a
	// token reaches the whole workspace (ADR 0008; it used to be an instance
	// token) and lists no grants, and neither does an instance token.
	m = e.ok(create(e.admin, map[string]any{"name": "b", "bucket": "files", "access": "admin"}), 201).json(t)
	if m["scope"] != "bucket" || len(m["grants"].([]any)) != 1 {
		t.Fatalf("bucket token: %v", m)
	}
	m = e.ok(create(e.admin, map[string]any{"name": "w", "access": "read"}), 201).json(t)
	if m["scope"] != "workspace" || m["workspace_id"] != e.defaultWS() || len(m["grants"].([]any)) != 0 {
		t.Fatalf("workspace token: %v", m)
	}
	m = e.ok(e.do(e.admin, "POST", "/v1/instance/tokens", "/v1/instance/tokens", map[string]any{"name": "i", "access": "read"}), 201).json(t)
	if m["scope"] != "instance" || m["workspace_id"] != "" || len(m["grants"].([]any)) != 0 {
		t.Fatalf("instance token: %v", m)
	}

	// Bad shapes.
	for name, body := range map[string]map[string]any{
		"grants with access":   {"name": "t", "access": "read", "grants": []map[string]any{{"database": "notes", "access": "read"}}},
		"grants with database": {"name": "t", "database": "notes", "grants": []map[string]any{{"bucket": "files", "access": "read"}}},
		"database and bucket":  {"name": "t", "database": "notes", "bucket": "files", "access": "read"},
		"no access":            {"name": "t", "database": "notes"},
		"grant with both":      grants(map[string]any{"database": "notes", "bucket": "files", "access": "read"}),
		"grant with neither":   grants(map[string]any{"access": "read"}, map[string]any{"bucket": "files", "access": "read"}),
		"branch alone":         grants(map[string]any{"branch": "main", "access": "read"}, map[string]any{"bucket": "files", "access": "read"}),
		"same thing twice":     grants(map[string]any{"database": "notes", "access": "read"}, map[string]any{"database": "notes", "access": "write"}),
	} {
		if r := create(e.admin, body); r.status != 400 || r.code(t) != "bad_request" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	// A whole-database grant already covers its branches, in either order.
	for _, body := range []map[string]any{
		grants(map[string]any{"database": "notes", "access": "admin"}, map[string]any{"database": "notes", "branch": "main", "access": "read"}),
		grants(map[string]any{"database": "notes", "branch": "main", "access": "read"}, map[string]any{"database": "notes", "access": "admin"}),
	} {
		if r := create(e.admin, body); r.status != 400 || r.code(t) != "bad_request" {
			t.Errorf("a branch grant next to its whole database: %d %s", r.status, r.body)
		}
	}
	// More than 20 grants is refused before anything is looked up.
	many := []map[string]any{}
	for i := 0; i < 21; i++ {
		many = append(many, map[string]any{"database": fmt.Sprintf("db-%d", i), "access": "read"})
	}
	e.fails(create(e.admin, grants(many...)), 422, "invalid_request")
	e.fails(create(e.admin, grants(map[string]any{"database": "notes", "access": "read"}, map[string]any{"bucket": "nowhere", "access": "read"})), 404, "bucket_not_found")
	e.fails(create(e.admin, grants(map[string]any{"database": "nowhere", "access": "read"}, map[string]any{"bucket": "files", "access": "read"})), 404, "database_not_found")

	// Minting: an admin of both may hand out both; an admin of one may not
	// hand out the other, or a second database it doesn't reach.
	both, bothID := e.token(grants(map[string]any{"database": "notes", "access": "admin"}, map[string]any{"bucket": "files", "access": "admin"}))
	half, _ := e.token(grants(map[string]any{"database": "notes", "access": "admin"}, map[string]any{"bucket": "files", "access": "read"}))
	child := grants(map[string]any{"database": "notes", "access": "write"}, map[string]any{"bucket": "files", "access": "read"})
	childID := e.ok(create(both, child), 201).json(t)["id"].(string)
	e.fails(create(half, child), 403, "forbidden")
	e.ok(create(half, map[string]any{"name": "t", "database": "notes", "access": "write"}), 201)
	e.fails(create(both, grants(map[string]any{"database": "notes", "access": "read"}, map[string]any{"database": "other", "access": "read"})), 404, "database_not_found")
	e.fails(create(both, map[string]any{"name": "t", "access": "read"}), 403, "forbidden")

	// Managing: half is an admin of the database only, so tokens that also
	// reach the bucket are not its to see or revoke.
	ids := func(token string) map[string]bool {
		out := map[string]bool{}
		for _, v := range e.ok(e.do(token, "GET", pTokens, "/v1/tokens", nil), 200).json(t)["data"].([]any) {
			out[v.(map[string]any)["id"].(string)] = true
		}
		return out
	}
	if seen := ids(half); seen[childID] || seen[bothID] {
		t.Fatalf("an admin of one grant sees tokens that reach more: %v", seen)
	}
	if seen := ids(both); !seen[childID] || !seen[bothID] {
		t.Fatalf("an admin of both grants doesn't see its tokens: %v", seen)
	}
	e.fails(e.do(half, "DELETE", pToken, "/v1/tokens/"+childID, nil), 404, "token_not_found")
	e.ok(e.do(both, "DELETE", pToken, "/v1/tokens/"+childID, nil), 204)
}

// Deleting anything a token reaches revokes the whole token.
func TestGrantsRevokedWithWhatTheyReach(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("notes")
	e.ok(e.sql(e.admin, "notes", "CREATE TABLE t(x)"), 200)
	e.ok(e.do(e.admin, "POST", pBranches, "/v1/databases/notes/branches", map[string]any{"name": "dev"}), 201)
	for _, b := range []string{"files", "scratch"} {
		e.ok(e.do(e.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": b}), 201)
	}
	withBucket, _ := e.token(map[string]any{"name": "a", "grants": []map[string]any{
		{"database": "notes", "access": "read"}, {"bucket": "scratch", "access": "read"},
	}})
	withBranch, _ := e.token(map[string]any{"name": "b", "grants": []map[string]any{
		{"database": "notes", "branch": "dev", "access": "read"}, {"bucket": "files", "access": "read"},
	}})
	e.ok(e.sql(withBucket, "notes", "SELECT * FROM t"), 200)
	e.ok(e.do(withBranch, "GET", "/v1/buckets/{bucket}", "/v1/buckets/files", nil), 200)

	e.ok(e.do(e.admin, "DELETE", "/v1/buckets/{bucket}", "/v1/buckets/scratch", nil), 204)
	e.fails(e.sql(withBucket, "notes", "SELECT * FROM t"), 401, "unauthorized")

	e.ok(e.do(e.admin, "DELETE", pBranch, "/v1/databases/notes/branches/dev", nil), 204)
	e.fails(e.do(withBranch, "GET", "/v1/buckets/{bucket}", "/v1/buckets/files", nil), 401, "unauthorized")
}
