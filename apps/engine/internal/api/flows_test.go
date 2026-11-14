package api_test

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

const (
	pDBs      = "/v1/databases"
	pDB       = "/v1/databases/{db}"
	pQuery    = "/v1/databases/{db}/query"
	pBatch    = "/v1/databases/{db}/batch"
	pBQuery   = "/v1/databases/{db}/branches/{branch}/query"
	pBranches = "/v1/databases/{db}/branches"
	pBranch   = "/v1/databases/{db}/branches/{branch}"
	pTokens   = "/v1/tokens"
	pToken    = "/v1/tokens/{token_id}"
)

func (e *env) createDB(name string) map[string]any {
	e.t.Helper()
	return e.ok(e.do(e.admin, "POST", pDBs, "/v1/databases", map[string]any{"name": name}), 201).json(e.t)
}

func (e *env) sql(token, db, sql string, args ...any) resp {
	e.t.Helper()
	body := map[string]any{"sql": sql}
	if len(args) > 0 {
		body["args"] = args
	}
	return e.do(token, "POST", pQuery, "/v1/databases/"+db+"/query", body)
}

func (e *env) token(body map[string]any) (string, string) {
	e.t.Helper()
	m := e.ok(e.do(e.admin, "POST", pTokens, "/v1/tokens", body), 201).json(e.t)
	return m["token"].(string), m["id"].(string)
}

func TestHealthAndDocument(t *testing.T) {
	e := newEnv(t, nil)
	r := e.do("", "GET", "", "/health", nil)
	if r.status != 200 || r.json(t)["status"] != "ok" {
		t.Fatalf("health: %d %s", r.status, r.body)
	}
	r = e.do("", "GET", "", "/v1/openapi.json", nil)
	if r.status != 200 || !bytes.Contains(r.body, []byte(`"openapi":"3.1.0"`)) {
		t.Fatalf("openapi: %d %.200s", r.status, r.body)
	}
}

func TestAuthentication(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AuthFailuresPerMin = 3 })
	r := e.fails(e.do("", "GET", pDBs, "/v1/databases", nil), 401, "unauthorized")
	if r.header.Get("WWW-Authenticate") == "" {
		t.Fatal("no WWW-Authenticate")
	}
	golden(t, "unauthorized-missing", r)
	golden(t, "unauthorized-malformed", e.fails(e.do("nope", "GET", pDBs, "/v1/databases", nil), 401, "unauthorized"))
	fake := "lmb_aaaaaaaaaaaaaaaaaaaa_" + strings.Repeat("b", 40)
	e.fails(e.do(fake, "GET", pDBs, "/v1/databases", nil), 401, "unauthorized")
	// Three failures from this address: more bad tokens are refused with
	// rate_limited, but a valid token still gets in.
	r = e.fails(e.do(fake, "GET", pDBs, "/v1/databases", nil), 429, "rate_limited")
	if r.header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	e.ok(e.do(e.admin, "GET", pDBs, "/v1/databases", nil), 200)
}

func TestUnknownRoutesAreProblems(t *testing.T) {
	e := newEnv(t, nil)
	e.fails(e.do(e.admin, "GET", "", "/v1/nope", nil), 404, "not_found")
	e.fails(e.do(e.admin, "PUT", "", "/v1/databases", nil), 405, "method_not_allowed")
}

func TestReviewFixes(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("a")
	e.createDB("b")
	e.ok(e.sql(e.admin, "a", "CREATE TABLE t(x)"), 200)
	e.ok(e.do(e.admin, "POST", pBranches, "/v1/databases/a/branches", map[string]any{"name": "dev"}), 201)

	// create-token takes no Idempotency-Key: the secret is never stored.
	a := e.ok(e.do(e.admin, "POST", pTokens, "/v1/tokens", map[string]any{"name": "x", "access": "read"}, "Idempotency-Key", "same"), 201).json(t)
	b := e.ok(e.do(e.admin, "POST", pTokens, "/v1/tokens", map[string]any{"name": "x", "access": "read"}, "Idempotency-Key", "same"), 201).json(t)
	if a["id"] == b["id"] {
		t.Fatal("create-token replayed a stored response")
	}

	// The key covers the path: reusing it on another database is a different
	// request, refused instead of replaying the first database's branch.
	e.ok(e.do(e.admin, "POST", pBranches, "/v1/databases/a/branches", map[string]any{"name": "pr"}, "Idempotency-Key", "k"), 201)
	e.fails(e.do(e.admin, "POST", pBranches, "/v1/databases/b/branches", map[string]any{"name": "pr"}, "Idempotency-Key", "k"), 422, "idempotency_mismatch")
	e.fails(e.do(e.admin, "GET", pBranch, "/v1/databases/b/branches/pr", nil), 404, "branch_not_found")

	// A branch token can't reset (that would copy the parent in), can't see
	// the database's details, and can't tell missing branches from others.
	devOnly, _ := e.token(map[string]any{"name": "dev", "database": "a", "branch": "dev", "access": "write"})
	e.fails(e.do(devOnly, "POST", pBranch+"/reset", "/v1/databases/a/branches/dev/reset", nil), 403, "forbidden")
	e.fails(e.do(devOnly, "GET", pDB, "/v1/databases/a", nil), 403, "forbidden")
	e.fails(e.do(devOnly, "GET", pBranch, "/v1/databases/a/branches/nope", nil), 403, "forbidden")
	e.fails(e.do(devOnly, "GET", pBranch, "/v1/databases/a/branches/main", nil), 403, "forbidden")

	// A token that expires can't mint one that outlives it.
	short, _ := e.token(map[string]any{"name": "short", "database": "a", "access": "admin", "expires_in_days": 1})
	child := e.ok(e.do(short, "POST", pTokens, "/v1/tokens", map[string]any{"name": "child", "database": "a", "access": "read"}), 201).json(t)
	if child["expires_at"] == nil {
		t.Fatal("child of an expiring token never expires")
	}
}

func TestPerTokenRateLimit(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.RateLimit = 1; c.RateBurst = 3 })
	for i := 0; i < 3; i++ {
		e.ok(e.do(e.admin, "GET", pDBs, "/v1/databases", nil), 200)
	}
	golden(t, "rate-limited", e.fails(e.do(e.admin, "GET", pDBs, "/v1/databases", nil), 429, "rate_limited"))
}

func TestDatabasesAndQueries(t *testing.T) {
	e := newEnv(t, nil)
	d := e.createDB("app")
	if d["default_branch"] != "main" || d["branch_count"].(float64) != 1 {
		t.Fatalf("database: %v", d)
	}
	golden(t, "name-taken", e.fails(e.do(e.admin, "POST", pDBs, "/v1/databases", map[string]any{"name": "app"}), 409, "name_taken"))
	golden(t, "invalid-request", e.fails(e.do(e.admin, "POST", pDBs, "/v1/databases", map[string]any{"name": "Bad Name"}), 422, "invalid_request"))
	golden(t, "database-not-found", e.fails(e.do(e.admin, "GET", pDB, "/v1/databases/nope", nil), 404, "database_not_found"))

	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(id INTEGER PRIMARY KEY, n INTEGER, r REAL, s TEXT, b BLOB)"), 200)
	r := e.ok(e.sql(e.admin, "app", "INSERT INTO t (n, r, s, b) VALUES (?, ?, ?, ?)",
		map[string]any{"type": "integer", "value": "9007199254740993"}, 1.5, "hi", map[string]any{"type": "blob", "base64": "AAE="}), 200).json(t)
	if r["rows_affected"].(float64) != 1 || r["last_insert_rowid"] != "1" {
		t.Fatalf("insert: %v", r)
	}
	r = e.ok(e.sql(e.admin, "app", "SELECT n, r, s, b FROM t"), 200).json(t)
	row := r["rows"].([]any)[0].([]any)
	if row[0].(map[string]any)["value"] != "9007199254740993" || row[3].(map[string]any)["base64"] != "AAE=" {
		t.Fatalf("row: %v", row)
	}
	golden(t, "multiple-statements", e.fails(e.sql(e.admin, "app", "SELECT 1; SELECT 2"), 400, "multiple_statements"))
	golden(t, "statement-denied", e.fails(e.sql(e.admin, "app", "ATTACH 'x.db' AS x"), 403, "statement_denied"))
	golden(t, "sql-error", e.fails(e.sql(e.admin, "app", "SELECT * FROM missing"), 400, "sql_error"))
	golden(t, "invalid-params", e.fails(e.sql(e.admin, "app", "SELECT ?, ?", 1), 400, "invalid_params"))
	// An integer past 64 bits is refused while the body is parsed, never rounded.
	e.fails(e.sql(e.admin, "app", "SELECT ?", 9223372036854775808.0), 422, "invalid_request")

	b := e.ok(e.do(e.admin, "POST", pBatch, "/v1/databases/app/batch", map[string]any{"statements": []any{
		map[string]any{"sql": "INSERT INTO t (n) VALUES (2)"},
		map[string]any{"sql": "SELECT count(*) FROM t"},
	}}), 200).json(t)
	if res := b["results"].([]any); len(res) != 2 {
		t.Fatalf("batch: %v", b)
	}
	e.fails(e.do(e.admin, "POST", pBatch, "/v1/databases/app/batch", map[string]any{"statements": []any{
		map[string]any{"sql": "INSERT INTO t (n) VALUES (3)"},
		map[string]any{"sql": "INSERT INTO nope VALUES (1)"},
	}}), 400, "sql_error")
	cnt := e.ok(e.sql(e.admin, "app", "SELECT count(*) AS c FROM t"), 200).json(t)
	if cnt["rows"].([]any)[0].([]any)[0].(map[string]any)["value"] != "2" {
		t.Fatalf("failed batch kept a row: %v", cnt)
	}

	// Lists page with a cursor.
	e.createDB("app2")
	e.createDB("app3")
	p1 := e.ok(e.do(e.admin, "GET", pDBs, "/v1/databases?limit=2", nil), 200).json(t)
	if len(p1["data"].([]any)) != 2 || p1["next_cursor"] == "" {
		t.Fatalf("page 1: %v", p1)
	}
	p2 := e.ok(e.do(e.admin, "GET", pDBs, "/v1/databases?limit=2&cursor="+p1["next_cursor"].(string), nil), 200).json(t)
	if len(p2["data"].([]any)) != 1 || p2["next_cursor"] != "" {
		t.Fatalf("page 2: %v", p2)
	}

	// Idempotency-Key: the retry returns the first response; a different
	// request with the same key is refused.
	a := e.ok(e.do(e.admin, "POST", pDBs, "/v1/databases", map[string]any{"name": "idem"}, "Idempotency-Key", "k1"), 201).json(t)
	b2 := e.ok(e.do(e.admin, "POST", pDBs, "/v1/databases", map[string]any{"name": "idem"}, "Idempotency-Key", "k1"), 201).json(t)
	if a["id"] != b2["id"] {
		t.Fatal("idempotent retry made a second database")
	}
	golden(t, "idempotency-mismatch", e.fails(e.do(e.admin, "POST", pDBs, "/v1/databases", map[string]any{"name": "other"}, "Idempotency-Key", "k1"), 422, "idempotency_mismatch"))

	// Rename, trash, restore.
	e.ok(e.do(e.admin, "PATCH", pDB, "/v1/databases/app3", map[string]any{"name": "app4"}), 200)
	e.ok(e.do(e.admin, "DELETE", pDB, "/v1/databases/app4", nil), 200)
	trash := e.ok(e.do(e.admin, "GET", "/v1/trash", "/v1/trash", nil), 200).json(t)
	if len(trash["data"].([]any)) != 1 {
		t.Fatalf("trash: %v", trash)
	}
	e.ok(e.do(e.admin, "POST", "/v1/databases/{db}/restore", "/v1/databases/app4/restore", map[string]any{}), 200)
	e.ok(e.do(e.admin, "GET", pDB, "/v1/databases/app4", nil), 200)
}

func TestScopes(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("a")
	e.createDB("b")
	e.ok(e.sql(e.admin, "a", "CREATE TABLE t(x)"), 200)
	e.ok(e.do(e.admin, "POST", pBranches, "/v1/databases/a/branches", map[string]any{"name": "dev"}), 201)

	read, _ := e.token(map[string]any{"name": "reader", "database": "a", "access": "read"})
	write, _ := e.token(map[string]any{"name": "writer", "database": "a", "access": "write"})
	devOnly, _ := e.token(map[string]any{"name": "dev", "database": "a", "branch": "dev", "access": "write"})

	e.ok(e.sql(read, "a", "SELECT * FROM t"), 200)
	golden(t, "read-only", e.fails(e.sql(read, "a", "INSERT INTO t VALUES (1)"), 403, "read_only"))
	e.ok(e.sql(write, "a", "INSERT INTO t VALUES (1)"), 200)
	// read_only on a write token still uses a read-only connection.
	e.fails(e.do(write, "POST", pQuery, "/v1/databases/a/query", map[string]any{"sql": "INSERT INTO t VALUES (2)", "read_only": true}), 403, "read_only")

	// Other databases don't exist for a database token.
	e.fails(e.sql(read, "b", "SELECT 1"), 404, "database_not_found")
	list := e.ok(e.do(read, "GET", pDBs, "/v1/databases", nil), 200).json(t)
	if n := len(list["data"].([]any)); n != 1 {
		t.Fatalf("database token sees %d databases", n)
	}
	// Management needs more than read or write.
	golden(t, "forbidden", e.fails(e.do(write, "POST", pDBs, "/v1/databases", map[string]any{"name": "c"}), 403, "forbidden"))
	e.fails(e.do(write, "DELETE", pBranch, "/v1/databases/a/branches/dev", nil), 403, "forbidden")
	e.fails(e.do(write, "POST", pTokens, "/v1/tokens", map[string]any{"name": "x", "access": "read"}), 403, "forbidden")

	// A branch token reaches only its branch.
	e.ok(e.do(devOnly, "POST", pBQuery, "/v1/databases/a/branches/dev/query", map[string]any{"sql": "INSERT INTO t VALUES (3)"}), 200)
	e.fails(e.sql(devOnly, "a", "SELECT 1"), 403, "forbidden")
	e.fails(e.do(devOnly, "GET", pBranches, "/v1/databases/a/branches", nil), 403, "forbidden")

	// A database admin mints tokens only inside its database.
	dbAdmin, _ := e.token(map[string]any{"name": "dba", "database": "a", "access": "admin"})
	e.ok(e.do(dbAdmin, "POST", pTokens, "/v1/tokens", map[string]any{"name": "ci", "database": "a", "access": "write"}), 201)
	e.fails(e.do(dbAdmin, "POST", pTokens, "/v1/tokens", map[string]any{"name": "x", "access": "read"}), 403, "forbidden")
	e.fails(e.do(dbAdmin, "POST", pTokens, "/v1/tokens", map[string]any{"name": "x", "database": "b", "access": "read"}), 404, "database_not_found")
}

func TestTokensAndAudit(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("a")
	tok, id := e.token(map[string]any{"name": "ci", "database": "a", "access": "write", "expires_in_days": 7})
	if !strings.HasPrefix(tok, "lmb_") {
		t.Fatalf("token %q", tok)
	}
	list := e.ok(e.do(e.admin, "GET", pTokens, "/v1/tokens", nil), 200)
	if bytes.Contains(list.body, []byte(tok)) || bytes.Contains(list.body, []byte("secret")) {
		t.Fatal("token list leaks the secret")
	}
	e.ok(e.sql(tok, "a", "SELECT 1"), 200)
	e.ok(e.do(e.admin, "DELETE", pToken, "/v1/tokens/"+id, nil), 204)
	e.fails(e.sql(tok, "a", "SELECT 1"), 401, "unauthorized")
	golden(t, "token-not-found", e.fails(e.do(e.admin, "DELETE", pToken, "/v1/tokens/"+id, nil), 404, "token_not_found"))

	// The service token is an instance token (ADR 0008): it is listed with
	// the instance tokens, not with a workspace's, and can't be revoked
	// through either door.
	var serviceID string
	for _, raw := range e.ok(e.do(e.admin, "GET", pTokens, "/v1/tokens", nil), 200).json(t)["data"].([]any) {
		if m := raw.(map[string]any); m["service"] == true || m["scope"] == "instance" {
			t.Fatalf("the workspace's token list holds an instance token: %v", m)
		}
	}
	all := e.ok(e.do(e.admin, "GET", "/v1/instance/tokens", "/v1/instance/tokens", nil), 200).json(t)
	for _, raw := range all["data"].([]any) {
		if m := raw.(map[string]any); m["service"] == true {
			serviceID = m["id"].(string)
		}
	}
	if serviceID == "" {
		t.Fatal("the service token isn't among the instance tokens")
	}
	e.fails(e.do(e.admin, "DELETE", "/v1/instance/tokens/{token_id}", "/v1/instance/tokens/"+serviceID, nil), 403, "forbidden")
	e.fails(e.do(e.admin, "DELETE", pToken, "/v1/tokens/"+serviceID, nil), 404, "token_not_found")

	// The actor header is honored for the service token only.
	other, _ := e.token(map[string]any{"name": "admin2", "access": "admin"})
	e.ok(e.do(e.admin, "POST", pDBs, "/v1/databases", map[string]any{"name": "b"}, "X-Lumbung-Actor", "zaki@example.com"), 201)
	e.ok(e.do(other, "POST", pDBs, "/v1/databases", map[string]any{"name": "c"}, "X-Lumbung-Actor", "spoofed"), 201)
	e.fails(e.do(e.admin, "POST", pDBs, "/v1/databases", map[string]any{"name": "a"}), 409, "name_taken")

	audit := e.ok(e.do(e.admin, "GET", "/v1/audit", "/v1/audit?limit=3", nil), 200).json(t)
	entries := audit["data"].([]any)
	if len(entries) != 3 || audit["next_cursor"] == "" {
		t.Fatalf("audit page: %v", audit)
	}
	first := entries[0].(map[string]any)
	if first["action"] != "database.create" || first["result"] != "error" || first["code"] != "name_taken" {
		t.Fatalf("newest entry: %v", first)
	}
	if entries[1].(map[string]any)["actor"] != "" {
		t.Fatal("actor header honored for a non-service token")
	}
	if entries[2].(map[string]any)["actor"] != "zaki@example.com" {
		t.Fatalf("service actor missing: %v", entries[2])
	}
	e.ok(e.do(e.admin, "GET", "/v1/audit", "/v1/audit?cursor="+url.QueryEscape(audit["next_cursor"].(string)), nil), 200)
}

func TestBranchesSnapshotsAndTransfer(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)
	e.ok(e.sql(e.admin, "app", "INSERT INTO t VALUES (1)"), 200)

	br := e.ok(e.do(e.admin, "POST", pBranches, "/v1/databases/app/branches", map[string]any{"name": "dev", "expires_in_days": 3}), 201).json(t)
	if br["expires_at"] == nil || br["default"] != false {
		t.Fatalf("branch: %v", br)
	}
	e.ok(e.do(e.admin, "POST", pBQuery, "/v1/databases/app/branches/dev/query", map[string]any{"sql": "ALTER TABLE t ADD COLUMN y"}), 200)
	diff := e.ok(e.do(e.admin, "GET", pBranch+"/diff", "/v1/databases/app/branches/dev/diff?against=main", nil), 200).json(t)
	if len(diff["changes"].([]any)) != 1 {
		t.Fatalf("diff: %v", diff)
	}
	e.ok(e.do(e.admin, "GET", pBranch+"/schema", "/v1/databases/app/branches/dev/schema", nil), 200)
	e.ok(e.do(e.admin, "PATCH", pBranch, "/v1/databases/app/branches/dev", map[string]any{"protected": true}), 200)
	golden(t, "branch-protected", e.fails(e.do(e.admin, "DELETE", pBranch, "/v1/databases/app/branches/dev", nil), 409, "branch_protected"))
	e.ok(e.do(e.admin, "PATCH", pBranch, "/v1/databases/app/branches/dev", map[string]any{"protected": false}), 200)
	e.ok(e.do(e.admin, "POST", pBranch+"/reset", "/v1/databases/app/branches/dev/reset", nil), 200)

	sn := e.ok(e.do(e.admin, "POST", pBranch+"/snapshots", "/v1/databases/app/branches/main/snapshots", map[string]any{"label": "before"}), 201).json(t)
	e.ok(e.do(e.admin, "GET", pBranch+"/snapshots", "/v1/databases/app/branches/main/snapshots", nil), 200)
	restored := e.ok(e.do(e.admin, "POST", "/v1/databases/{db}/snapshots/{snapshot}/restore", "/v1/databases/app/snapshots/"+sn["id"].(string)+"/restore", map[string]any{"name": "restored"}), 201).json(t)
	if restored["source_snapshot_id"] != sn["id"] {
		t.Fatalf("restore: %v", restored)
	}
	e.ok(e.do(e.admin, "PUT", pBranch+"/snapshot-schedule", "/v1/databases/app/branches/main/snapshot-schedule", map[string]any{"every_minutes": 360, "keep_count": 4}), 200)
	sched := e.ok(e.do(e.admin, "GET", pBranch+"/snapshot-schedule", "/v1/databases/app/branches/main/snapshot-schedule", nil), 200).json(t)
	if sched["every_minutes"].(float64) != 360 {
		t.Fatalf("schedule: %v", sched)
	}
	e.ok(e.do(e.admin, "DELETE", pBranch+"/snapshot-schedule", "/v1/databases/app/branches/main/snapshot-schedule", nil), 204)
	e.ok(e.do(e.admin, "DELETE", "/v1/databases/{db}/snapshots/{snapshot}", "/v1/databases/app/snapshots/"+sn["id"].(string), nil), 204)

	// Download as .db and as SQL, then import each into a new database.
	dbFile := e.ok(e.do(e.admin, "GET", pBranch+"/export", "/v1/databases/app/branches/main/export?format=db", nil), 200)
	if !bytes.HasPrefix(dbFile.body, []byte("SQLite format 3\x00")) || !strings.Contains(dbFile.header.Get("Content-Disposition"), ".db") {
		t.Fatal("db download")
	}
	dump := e.ok(e.do(e.admin, "GET", pBranch+"/export", "/v1/databases/app/branches/main/export?format=sql", nil), 200)
	if !bytes.Contains(dump.body, []byte("CREATE TABLE t(x)")) {
		t.Fatalf("dump: %s", dump.body)
	}
	e.createDB("copy1")
	e.ok(e.do(e.admin, "POST", pBranch+"/import", "/v1/databases/copy1/branches/main/import?format=db", dbFile.body, "Content-Type", "application/octet-stream"), 200)
	e.createDB("copy2")
	e.ok(e.do(e.admin, "POST", pBranch+"/import", "/v1/databases/copy2/branches/main/import?format=sql", dump.body, "Content-Type", "application/sql"), 200)
	for _, db := range []string{"copy1", "copy2"} {
		r := e.ok(e.sql(e.admin, db, "SELECT count(*) FROM t"), 200).json(t)
		if r["rows"].([]any)[0].([]any)[0].(map[string]any)["value"] != "1" {
			t.Fatalf("%s rows: %v", db, r)
		}
	}
	golden(t, "branch-not-empty", e.fails(e.do(e.admin, "POST", pBranch+"/import", "/v1/databases/copy2/branches/main/import?format=sql", "CREATE TABLE z(a);", "Content-Type", "application/sql"), 409, "branch_not_empty"))

	stats := e.ok(e.do(e.admin, "GET", pBranch+"/stats", "/v1/databases/app/branches/main/stats", nil), 200).json(t)
	if stats["queries"].(float64) < 2 {
		t.Fatalf("stats: %v", stats)
	}
	e.ok(e.do(e.admin, "POST", pBranch+"/promote", "/v1/databases/app/branches/restored/promote", nil), 200)
	d := e.ok(e.do(e.admin, "GET", pDB, "/v1/databases/app", nil), 200).json(t)
	if d["default_branch"] != "restored" {
		t.Fatalf("promote: %v", d)
	}
	e.ok(e.do(e.admin, "DELETE", pBranch, "/v1/databases/app/branches/dev", nil), 204)
}
