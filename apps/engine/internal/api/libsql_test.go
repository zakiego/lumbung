package api_test

// The libSQL protocol endpoint (ADR 0005), spoken as raw JSON the way
// @libsql/client sends it. examples/libsql runs the real client.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

// hrana is one stream: it sends pipelines and keeps the baton.
type hrana struct {
	e      *env
	token  string
	path   string // /libsql/<db>/<branch>/v2
	baton  any    // nil or string
	status int
}

func (e *env) libsql(token, db, branch string) *hrana {
	return &hrana{e: e, token: token, path: "/libsql/" + db + "/" + branch + "/v2"}
}

func (h *hrana) post(path string, body any) (int, http.Header, []byte) {
	h.e.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", h.e.http.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.e.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, out
}

// pipeline sends requests on the stream. It returns the results, or for an
// HTTP error the error body, and updates the baton.
func (h *hrana) pipeline(reqs ...map[string]any) []map[string]any {
	h.e.t.Helper()
	status, hdr, body := h.post(h.path+"/pipeline", map[string]any{"baton": h.baton, "requests": reqs})
	h.status = status
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		h.e.t.Fatalf("not JSON (%d): %s", status, body)
	}
	if status != 200 {
		if ct := hdr.Get("Content-Type"); ct != "application/json" {
			h.e.t.Fatalf("error content type %q, the client only reads application/json", ct)
		}
		return []map[string]any{{"type": "http", "error": m}}
	}
	h.baton = m["baton"]
	var out []map[string]any
	for _, r := range m["results"].([]any) {
		out = append(out, r.(map[string]any))
	}
	if len(out) != len(reqs) {
		h.e.t.Fatalf("%d results for %d requests", len(out), len(reqs))
	}
	return out
}

func exec(sql string, args ...any) map[string]any {
	return map[string]any{"type": "execute", "stmt": stmtOf(sql, args...)}
}

func stmtOf(sql string, args ...any) map[string]any {
	st := map[string]any{"sql": sql}
	if len(args) > 0 {
		st["args"] = args
	}
	return st
}

var closeReq = map[string]any{"type": "close"}

// ok returns a result's response, failing on an error.
func (h *hrana) ok(r map[string]any) map[string]any {
	h.e.t.Helper()
	if r["type"] != "ok" {
		h.e.t.Fatalf("want ok, got %v", r)
	}
	return r["response"].(map[string]any)
}

// errCode returns the code of an error result or an HTTP error.
func errCode(t *testing.T, r map[string]any) string {
	t.Helper()
	e, ok := r["error"].(map[string]any)
	if !ok {
		t.Fatalf("want an error, got %v", r)
	}
	return e["code"].(string)
}

func wantErr(t *testing.T, r map[string]any, code, what string) {
	t.Helper()
	if got := errCode(t, r); got != code {
		t.Fatalf("%s: got %s (%v), want %s", what, got, r["error"], code)
	}
}

// run executes one statement on the stream and returns its result.
func (h *hrana) run(sql string, args ...any) map[string]any {
	h.e.t.Helper()
	return h.ok(h.pipeline(exec(sql, args...))[0])["result"].(map[string]any)
}

// one runs a statement on a fresh stream, as client.execute does.
func (e *env) one(token, db, branch, sql string, args ...any) map[string]any {
	e.t.Helper()
	return e.libsql(token, db, branch).pipeline(exec(sql, args...), closeReq)[0]
}

func rows(t *testing.T, res map[string]any) [][]any {
	t.Helper()
	var out [][]any
	for _, r := range res["rows"].([]any) {
		out = append(out, r.([]any))
	}
	return out
}

func restCount(e *env, db, table string) string {
	e.t.Helper()
	// read_only: a write token's query would otherwise wait for the writer
	// an open transaction holds.
	r := e.ok(e.do(e.admin, "POST", pQuery, "/v1/databases/"+db+"/query", map[string]any{"sql": "SELECT count(*) FROM " + table, "read_only": true}), 200).json(e.t)
	return r["rows"].([]any)[0].([]any)[0].(map[string]any)["value"].(string)
}

func TestLibSQLExecuteAndValues(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	h := e.libsql(e.admin, "app", "main")
	res := h.pipeline(
		exec("CREATE TABLE t(id INTEGER PRIMARY KEY, i INTEGER, f REAL, s TEXT, b BLOB, n)"),
		exec("INSERT INTO t (i, f, s, b, n) VALUES (?, ?, ?, ?, ?)",
			map[string]any{"type": "integer", "value": "9007199254740993"},
			map[string]any{"type": "float", "value": 1.5},
			map[string]any{"type": "text", "value": "héllo"},
			map[string]any{"type": "blob", "base64": "AAEC"},
			map[string]any{"type": "null"}),
		map[string]any{"type": "execute", "stmt": map[string]any{
			"sql":        "SELECT i, f, s, b, n, 1 + 1 AS two FROM t WHERE s = :s",
			"named_args": []any{map[string]any{"name": "s", "value": map[string]any{"type": "text", "value": "héllo"}}},
		}},
		map[string]any{"type": "execute", "stmt": map[string]any{"sql": "SELECT * FROM t", "want_rows": false}},
		closeReq,
	)
	ins := h.ok(res[1])["result"].(map[string]any)
	if ins["affected_row_count"].(float64) != 1 || ins["last_insert_rowid"] != "1" {
		t.Fatalf("insert: %v", ins)
	}
	sel := h.ok(res[2])["result"].(map[string]any)
	got, _ := json.Marshal(sel["rows"])
	want := `[[{"type":"integer","value":"9007199254740993"},{"type":"float","value":1.5},{"type":"text","value":"héllo"},{"base64":"AAEC","type":"blob"},{"type":"null"},{"type":"integer","value":"2"}]]`
	if string(got) != want {
		t.Fatalf("rows:\n got %s\nwant %s", got, want)
	}
	cols := sel["cols"].([]any)
	if c := cols[0].(map[string]any); c["name"] != "i" || c["decltype"] != "INTEGER" {
		t.Fatalf("col 0: %v", c)
	}
	if c := cols[5].(map[string]any); c["name"] != "two" || c["decltype"] != nil {
		t.Fatalf("expression column: %v", c)
	}
	if n := len(h.ok(res[3])["result"].(map[string]any)["rows"].([]any)); n != 0 {
		t.Fatalf("want_rows false returned %d rows", n)
	}
	if h.ok(res[4])["type"] != "close" || h.baton != nil {
		t.Fatalf("after close: baton %v", h.baton)
	}
}

func TestLibSQLInteractiveTransaction(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)

	// As transaction(): BEGIN with the first statements, then more requests
	// on the same stream, each with the baton from the last response.
	h := e.libsql(e.admin, "app", "main")
	res := h.pipeline(
		map[string]any{"type": "store_sql", "sql_id": 1, "sql": "INSERT INTO t VALUES (?)"},
		map[string]any{"type": "batch", "batch": map[string]any{"steps": []any{
			map[string]any{"stmt": stmtOf("BEGIN IMMEDIATE")},
			map[string]any{"stmt": map[string]any{"sql_id": 1, "args": []any{1}}, "condition": map[string]any{"type": "ok", "step": 0}},
		}}},
	)
	b := h.ok(res[1])["result"].(map[string]any)
	if b["step_errors"].([]any)[1] != nil || b["step_results"].([]any)[1] == nil {
		t.Fatalf("batch: %v", b)
	}
	first := h.baton
	if first == nil {
		t.Fatal("no baton for an open transaction")
	}
	h.ok(h.pipeline(map[string]any{"type": "execute", "stmt": map[string]any{"sql_id": 1, "args": []any{2}}})[0])
	if h.baton == first {
		t.Fatal("the baton didn't change")
	}
	if got := h.run("SELECT count(*) FROM t"); rows(t, got)[0][0].(map[string]any)["value"] != "2" {
		t.Fatalf("the transaction sees %v", got)
	}
	if got := restCount(e, "app", "t"); got != "0" {
		t.Fatalf("REST sees %s uncommitted rows", got)
	}
	// Another stream's read doesn't wait for the writer this one holds.
	if got := rows(t, h.ok(e.one(e.admin, "app", "main", "SELECT count(*) FROM t"))["result"].(map[string]any))[0][0].(map[string]any)["value"]; got != "0" {
		t.Fatalf("another stream sees %v uncommitted rows", got)
	}
	auto := h.ok(h.pipeline(map[string]any{"type": "get_autocommit"})[0])
	if auto["is_autocommit"] != false {
		t.Fatalf("get_autocommit in a transaction: %v", auto)
	}
	res = h.pipeline(exec("COMMIT"), closeReq)
	h.ok(res[0])
	if got := restCount(e, "app", "t"); got != "2" {
		t.Fatalf("after commit REST sees %s rows", got)
	}

	// The same with ROLLBACK.
	h = e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("BEGIN"))[0])
	h.run("INSERT INTO t VALUES (3)")
	h.ok(h.pipeline(exec("ROLLBACK"), closeReq)[0])
	if got := restCount(e, "app", "t"); got != "2" {
		t.Fatalf("after rollback REST sees %s rows", got)
	}

	// Nested transactions use savepoints.
	h = e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("BEGIN"))[0])
	for _, sql := range []string{"SAVEPOINT sp1", "INSERT INTO t VALUES (4)", "ROLLBACK TO SAVEPOINT sp1", "RELEASE SAVEPOINT sp1", "INSERT INTO t VALUES (5)"} {
		h.run(sql)
	}
	h.ok(h.pipeline(exec("COMMIT"), closeReq)[0])
	if got := restCount(e, "app", "t"); got != "3" {
		t.Fatalf("after savepoints REST sees %s rows", got)
	}

	// Transaction control in the wrong state is a SQL error, as in SQLite.
	h = e.libsql(e.admin, "app", "main")
	wantErr(t, h.pipeline(exec("COMMIT"))[0], "sql_error", "commit outside a transaction")
	h.ok(h.pipeline(exec("BEGIN"))[0])
	wantErr(t, h.pipeline(exec("BEGIN"))[0], "sql_error", "begin inside a transaction")
	h.pipeline(exec("ROLLBACK"), closeReq)
}

// Closing the stream, letting it go idle, or passing the transaction
// timeout rolls back and frees the writer.
func TestLibSQLRollbackOnCloseAndTimeout(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.LibSQLStreamIdle = 300 * time.Millisecond
		c.LibSQLTxTimeout = 2 * time.Second
	})
	e.createDB("app")
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)

	h := e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("BEGIN"))[0])
	h.run("INSERT INTO t VALUES (1)")
	h.ok(h.pipeline(closeReq)[0])
	if got := restCount(e, "app", "t"); got != "0" {
		t.Fatalf("close kept %s rows", got)
	}

	h = e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("BEGIN"))[0])
	h.run("INSERT INTO t VALUES (1)")
	time.Sleep(700 * time.Millisecond)
	wantErr(t, h.pipeline(exec("COMMIT"))[0], "invalid_baton", "commit after the idle timeout")
	if h.status != 400 {
		t.Fatalf("status %d", h.status)
	}
	e.ok(e.sql(e.admin, "app", "INSERT INTO t VALUES (2)"), 200)
	if got := restCount(e, "app", "t"); got != "1" {
		t.Fatalf("after the idle timeout: %s rows", got)
	}

	// A busy transaction still ends at the transaction timeout.
	h = e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("BEGIN"))[0])
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := h.pipeline(exec("INSERT INTO t VALUES (3)"))[0]
		if r["type"] == "http" {
			wantErr(t, r, "invalid_baton", "insert past the transaction timeout")
			break
		}
		h.ok(r)
		if time.Now().After(deadline) {
			t.Fatal("the transaction outlived its timeout")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := restCount(e, "app", "t"); got != "1" {
		t.Fatalf("after the transaction timeout: %s rows", got)
	}
}

func TestLibSQLBatons(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	e.ok(e.do(e.admin, "POST", pBranches, "/v1/databases/app/branches", map[string]any{"name": "dev"}), 201)
	other, _ := e.token(map[string]any{"name": "other", "database": "app", "access": "write"})

	h := e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("SELECT 1"))[0])
	stale := h.baton
	h.ok(h.pipeline(exec("SELECT 2"))[0])
	current := h.baton

	replay := e.libsql(e.admin, "app", "main")
	replay.baton = stale
	wantErr(t, replay.pipeline(exec("SELECT 3"))[0], "invalid_baton", "stale baton")
	for _, b := range []string{"garbage", "a.b", strings.Repeat("x", 300)} {
		bad := e.libsql(e.admin, "app", "main")
		bad.baton = b
		wantErr(t, bad.pipeline(exec("SELECT 3"))[0], "invalid_baton", "made-up baton "+b[:1])
	}
	thief := e.libsql(other, "app", "main")
	thief.baton = current
	wantErr(t, thief.pipeline(exec("SELECT 3"))[0], "invalid_baton", "another token's baton")
	elsewhere := e.libsql(e.admin, "app", "dev")
	elsewhere.baton = current
	wantErr(t, elsewhere.pipeline(exec("SELECT 3"))[0], "invalid_baton", "baton on another branch")
	// The rightful owner still holds the stream.
	h.ok(h.pipeline(exec("SELECT 4"), closeReq)[0])

	// Two requests racing with the same baton: exactly one gets through.
	h = e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("SELECT 1"))[0])
	shared := h.baton
	var wg sync.WaitGroup
	var mu sync.Mutex
	okCount := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := e.libsql(e.admin, "app", "main")
			c.baton = shared
			b, _ := json.Marshal(map[string]any{"baton": shared, "requests": []any{exec("SELECT 1")}})
			req, _ := http.NewRequest("POST", e.http.URL+c.path+"/pipeline", bytes.NewReader(b))
			req.Header.Set("Authorization", "Bearer "+e.admin)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			res.Body.Close()
			if res.StatusCode == 200 {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("%d of 4 requests with one baton got through", okCount)
	}
}

func TestLibSQLStreamCaps(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.LibSQLStreamsPerToken = 2; c.LibSQLMaxStreams = 3 })
	e.createDB("app")
	a, _ := e.token(map[string]any{"name": "a", "database": "app", "access": "read"})
	b, _ := e.token(map[string]any{"name": "b", "database": "app", "access": "read"})

	open := func(token string) *hrana {
		h := e.libsql(token, "app", "main")
		h.pipeline(exec("SELECT 1"))
		return h
	}
	a1, a2 := open(a), open(a)
	if a1.status != 200 || a2.status != 200 {
		t.Fatal("couldn't open two streams")
	}
	a3 := open(a)
	if a3.status != 429 {
		t.Fatalf("third stream for one token: %d", a3.status)
	}
	wantErr(t, a3.pipeline(exec("SELECT 1"))[0], "too_many_streams", "per token")
	// A one-shot request needs a stream too.
	wantErr(t, e.one(a, "app", "main", "SELECT 1"), "too_many_streams", "one-shot at the token cap")
	b1 := open(b)
	if b1.status != 200 {
		t.Fatal("another token's first stream refused")
	}
	if open(b).status != 429 {
		t.Fatal("instance cap not applied")
	}
	// Closing a stream makes room.
	a1.ok(a1.pipeline(closeReq)[0])
	if open(b).status != 200 {
		t.Fatal("no room after a close")
	}
}

func TestLibSQLScopes(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	e.createDB("other")
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)
	e.ok(e.do(e.admin, "POST", pBranches, "/v1/databases/app/branches", map[string]any{"name": "dev"}), 201)

	// No token, or a bad one: the protocol's error shape, as application/json.
	wantErr(t, e.one("", "app", "main", "SELECT 1"), "unauthorized", "no token")
	wantErr(t, e.one("lmb_aaaaaaaaaaaaaaaaaaaa_"+strings.Repeat("b", 40), "app", "main", "SELECT 1"), "unauthorized", "bad token")

	read, _ := e.token(map[string]any{"name": "r", "database": "app", "access": "read"})
	e.libsql(read, "app", "main").ok(e.one(read, "app", "main", "SELECT * FROM t"))
	for _, sql := range []string{"INSERT INTO t VALUES (1)", "CREATE TABLE u(x)", "PRAGMA user_version = 1"} {
		wantErr(t, e.one(read, "app", "main", sql), "read_only", "read token: "+sql)
	}
	// A write transaction asked for by a read token runs read-only.
	h := e.libsql(read, "app", "main")
	h.ok(h.pipeline(exec("BEGIN IMMEDIATE"))[0])
	wantErr(t, h.pipeline(exec("INSERT INTO t VALUES (1)"))[0], "read_only", "read token in BEGIN IMMEDIATE")
	h.pipeline(exec("ROLLBACK"), closeReq)
	wantErr(t, e.one(read, "other", "main", "SELECT 1"), "database_not_found", "read token, other database")

	// A write token's READONLY transaction can't write either.
	h = e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("BEGIN TRANSACTION READONLY"))[0])
	wantErr(t, h.pipeline(exec("INSERT INTO t VALUES (1)"))[0], "read_only", "READONLY transaction")
	h.pipeline(exec("ROLLBACK"), closeReq)

	dev, _ := e.token(map[string]any{"name": "dev", "database": "app", "branch": "dev", "access": "write"})
	e.libsql(dev, "app", "dev").ok(e.one(dev, "app", "dev", "INSERT INTO t VALUES (1)"))
	wantErr(t, e.one(dev, "app", "main", "SELECT * FROM t"), "forbidden", "branch token on main")
	wantErr(t, e.one(dev, "app", "nope", "SELECT 1"), "forbidden", "branch token on a missing branch")
	if got := restCount(e, "app", "t"); got != "0" {
		t.Fatalf("main got %s rows from the dev token", got)
	}
}

// PLAN 6.2 through the libSQL endpoint, inside and outside an interactive
// transaction.
func TestLibSQLIsolation(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.StatementTimeout = 500 * time.Millisecond
		c.MaxResultRows = 100
		c.MaxValueBytes = 1 << 20
		c.MaxBranchBytes = 512 << 10
	})
	e.createDB("app")
	e.createDB("victim")
	e.ok(e.sql(e.admin, "victim", "CREATE TABLE secret(x)"), 200)
	e.ok(e.sql(e.admin, "victim", "INSERT INTO secret VALUES ('s')"), 200)
	e.ok(e.sql(e.admin, "app", "CREATE TABLE t(x)"), 200)
	e.ok(e.sql(e.admin, "app", "CREATE TABLE log(x)"), 200)
	e.ok(e.sql(e.admin, "app", "CREATE VIEW bad_view AS SELECT load_extension('/tmp/x.so') AS y"), 200)
	e.ok(e.sql(e.admin, "app", "CREATE TRIGGER bad_trigger AFTER INSERT ON log BEGIN SELECT load_extension('/tmp/x.so'); END"), 200)
	outside := e.srv.Engine.Layout().Meta() // meta.db, outside every branch

	denied := []string{
		"ATTACH DATABASE '" + outside + "' AS v",
		"ATTACH DATABASE ':memory:' AS m",
		"DETACH main",
		"VACUUM INTO '/tmp/lumbung-libsql-vacuum.db'",
		"PRAGMA writable_schema = ON",
		"PRAGMA journal_mode = DELETE",
		"PRAGMA temp_store_directory = '/tmp'",
		"PRAGMA locking_mode = EXCLUSIVE",
		"PRAGMA database_list",
		"CREATE TEMP TABLE x(a)",
		"SAVEPOINT s",
		"BEGIN; ATTACH ':memory:' AS m",
	}
	check := func(h *hrana, where string) {
		for _, sql := range denied {
			r := h.pipeline(exec(sql))[0]
			if code := errCode(t, r); code != "statement_denied" && code != "multiple_statements" {
				t.Errorf("%s: %s: got %s", where, sql, code)
			}
		}
		wantErr(t, h.pipeline(exec("SELECT load_extension('/tmp/x.so')"))[0], "sql_error", where+": load_extension")
		wantErr(t, h.pipeline(exec("SELECT * FROM bad_view"))[0], "sql_error", where+": view calling load_extension")
		wantErr(t, h.pipeline(exec("INSERT INTO log VALUES (1)"))[0], "sql_error", where+": trigger calling load_extension")
		if code := errCode(t, h.pipeline(exec("SELECT readfile('/etc/passwd')"))[0]); code != "sql_error" && code != "statement_denied" {
			t.Errorf("%s: readfile: got %s", where, code)
		}
		wantErr(t, h.pipeline(exec("SELECT * FROM victim.secret"))[0], "sql_error", where+": another database's table")
		wantErr(t, h.pipeline(exec("SELECT randomblob(2000000000)"))[0], "value_too_large", where+": randomblob")
		wantErr(t, h.pipeline(exec("WITH RECURSIVE c(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM c LIMIT 1000) SELECT n FROM c"))[0], "result_too_large", where+": rows past the cap")
	}
	h := e.libsql(e.admin, "app", "main")
	check(h, "autocommit")
	// The infinite CTE is cancelled and the stream still works.
	wantErr(t, h.pipeline(exec("WITH RECURSIVE c(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM c) SELECT count(*) FROM c"))[0], "statement_timeout", "endless CTE")
	h.run("SELECT 1")
	h.pipeline(closeReq)

	h = e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("BEGIN"))[0])
	// result_too_large ends the transaction, so run that check last in its
	// own transaction.
	for _, sql := range denied {
		if sql == "SAVEPOINT s" {
			continue // allowed inside a transaction
		}
		r := h.pipeline(exec(sql))[0]
		if code := errCode(t, r); code != "statement_denied" && code != "multiple_statements" {
			t.Errorf("in a transaction: %s: got %s", sql, code)
		}
	}
	wantErr(t, h.pipeline(exec("WITH RECURSIVE c(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM c LIMIT 1000) SELECT n FROM c"))[0], "result_too_large", "rows past the cap in a transaction")
	wantErr(t, h.pipeline(exec("SELECT 1"))[0], "transaction_aborted", "after the transaction ended")
	h.ok(h.pipeline(exec("ROLLBACK"))[0])
	h.run("SELECT 1")
	h.pipeline(closeReq)

	// A write cancelled at the statement timeout inside a transaction.
	h = e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("BEGIN"))[0])
	h.run("INSERT INTO t VALUES (1)")
	wantErr(t, h.pipeline(exec("INSERT INTO t SELECT count(*) FROM (WITH RECURSIVE c(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM c) SELECT n FROM c)"))[0], "statement_timeout", "endless insert")
	wantErr(t, h.pipeline(exec("INSERT INTO t VALUES (2)"))[0], "transaction_aborted", "insert after the rollback")
	wantErr(t, h.pipeline(exec("COMMIT"))[0], "transaction_aborted", "commit after the rollback")
	h.run("SELECT 1")
	h.pipeline(closeReq)
	if got := restCount(e, "app", "t"); got != "0" {
		t.Fatalf("rows kept from an aborted transaction: %s", got)
	}

	// Filling the branch stops at its size limit, in a transaction too.
	h = e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(exec("BEGIN"))[0])
	var last map[string]any
	for i := 0; i < 100; i++ {
		last = h.pipeline(exec("INSERT INTO t VALUES (randomblob(32768))"))[0]
		if last["type"] != "ok" {
			break
		}
	}
	wantErr(t, last, "branch_size_limit", "filling the branch")
	h.pipeline(exec("ROLLBACK"), closeReq)

	// The victim is untouched.
	if got := restCount(e, "victim", "secret"); got != "1" {
		t.Fatalf("victim rows: %s", got)
	}
}

// migrate() sends PRAGMA foreign_keys=off before its transaction. Like
// REST's migration batch, a table rebuild then doesn't cascade, and a
// migration that breaks a foreign key doesn't commit.
func TestLibSQLMigrateForeignKeys(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	h := e.libsql(e.admin, "app", "main")
	for _, sql := range []string{
		"CREATE TABLE p(id INTEGER PRIMARY KEY)",
		"CREATE TABLE c(id INTEGER PRIMARY KEY, p INTEGER REFERENCES p(id) ON DELETE CASCADE)",
		"INSERT INTO p VALUES (1)", "INSERT INTO c VALUES (1, 1)",
	} {
		h.run(sql)
	}
	steps := func(sqls ...string) map[string]any {
		var out []any
		out = append(out, map[string]any{"stmt": stmtOf("PRAGMA foreign_keys=off")})
		out = append(out, map[string]any{"stmt": stmtOf("BEGIN DEFERRED")})
		for _, s := range sqls {
			out = append(out, map[string]any{"stmt": stmtOf(s), "condition": map[string]any{"type": "ok", "step": len(out) - 1}})
		}
		commit := len(out)
		out = append(out, map[string]any{"stmt": stmtOf("COMMIT"), "condition": map[string]any{"type": "ok", "step": commit - 1}})
		out = append(out, map[string]any{"stmt": stmtOf("ROLLBACK"), "condition": map[string]any{"type": "not", "cond": map[string]any{"type": "ok", "step": commit}}})
		out = append(out, map[string]any{"stmt": stmtOf("PRAGMA foreign_keys=on")})
		return map[string]any{"type": "batch", "batch": map[string]any{"steps": out}}
	}
	res := h.ok(h.pipeline(steps("CREATE TABLE p2(id INTEGER PRIMARY KEY)", "INSERT INTO p2 SELECT id FROM p", "DROP TABLE p", "ALTER TABLE p2 RENAME TO p"))[0])["result"].(map[string]any)
	for i, e := range res["step_errors"].([]any) {
		if e != nil {
			t.Fatalf("step %d: %v", i, e)
		}
	}
	if got := restCount(e, "app", "c"); got != "1" {
		t.Fatalf("children after the rebuild: %s", got)
	}
	res = h.ok(h.pipeline(steps("DELETE FROM p"))[0])["result"].(map[string]any)
	if e := res["step_errors"].([]any)[3].(map[string]any); e["code"] != "constraint_violation" {
		t.Fatalf("commit of a broken migration: %v", e)
	}
	if got := restCount(e, "app", "p"); got != "1" {
		t.Fatalf("parents after the refused migration: %s", got)
	}
	// Foreign keys are on again for the next transaction.
	h.run("DELETE FROM p")
	if got := restCount(e, "app", "c"); got != "0" {
		t.Fatalf("cascade after the migration: %s children", got)
	}
}

func TestLibSQLSequenceDescribeConditions(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	h := e.libsql(e.admin, "app", "main")
	h.ok(h.pipeline(map[string]any{"type": "sequence", "sql": "CREATE TABLE t(id INTEGER PRIMARY KEY, name TEXT); INSERT INTO t (name) VALUES ('a'); -- done\n"})[0])
	wantErr(t, h.pipeline(map[string]any{"type": "sequence", "sql": "INSERT INTO t (name) VALUES ('b'); INSERT INTO nope VALUES (1); INSERT INTO t (name) VALUES ('c')"})[0], "sql_error", "sequence with a failing statement")
	if got := restCount(e, "app", "t"); got != "2" {
		t.Fatalf("after the sequence: %s rows", got)
	}
	// executeMultiple in a transaction: BEGIN and COMMIT inside a sequence.
	h.ok(h.pipeline(map[string]any{"type": "sequence", "sql": "BEGIN; INSERT INTO t (name) VALUES ('d'); ROLLBACK;"})[0])
	if got := restCount(e, "app", "t"); got != "2" {
		t.Fatalf("after a rolled back sequence: %s rows", got)
	}

	d := h.ok(h.pipeline(map[string]any{"type": "describe", "sql": "SELECT id, name FROM t WHERE id = ? AND name = :n"})[0])["result"].(map[string]any)
	params, _ := json.Marshal(d["params"])
	if string(params) != `[{"name":null},{"name":":n"}]` || d["is_readonly"] != true || d["is_explain"] != false {
		t.Fatalf("describe: %v", d)
	}
	if c := d["cols"].([]any)[1].(map[string]any); c["name"] != "name" || c["decltype"] != "TEXT" {
		t.Fatalf("describe cols: %v", d["cols"])
	}
	wantErr(t, h.pipeline(map[string]any{"type": "describe", "sql": "ATTACH 'x' AS y"})[0], "statement_denied", "describe attach")

	// v3-style conditions: is_autocommit, and, or, error.
	steps := []any{
		map[string]any{"stmt": stmtOf("BEGIN")},
		map[string]any{"stmt": stmtOf("INSERT INTO nope VALUES (1)")},
		map[string]any{"stmt": stmtOf("SELECT 'ran-after-error'"), "condition": map[string]any{"type": "and", "conds": []any{
			map[string]any{"type": "error", "step": 1},
			map[string]any{"type": "not", "cond": map[string]any{"type": "is_autocommit"}},
		}}},
		map[string]any{"stmt": stmtOf("SELECT 'skipped'"), "condition": map[string]any{"type": "or", "conds": []any{
			map[string]any{"type": "ok", "step": 1},
			map[string]any{"type": "is_autocommit"},
		}}},
		map[string]any{"stmt": stmtOf("ROLLBACK")},
		map[string]any{"stmt": stmtOf("SELECT 'auto'"), "condition": map[string]any{"type": "is_autocommit"}},
	}
	b := h.ok(h.pipeline(map[string]any{"type": "batch", "batch": map[string]any{"steps": steps}})[0])["result"].(map[string]any)
	results := b["step_results"].([]any)
	if results[2] == nil || results[3] != nil || results[5] == nil || b["step_errors"].([]any)[1] == nil {
		t.Fatalf("conditions: %v", b)
	}
	bad := []any{map[string]any{"stmt": stmtOf("SELECT 1"), "condition": map[string]any{"type": "ok", "step": 3}}}
	wantErr(t, h.pipeline(map[string]any{"type": "batch", "batch": map[string]any{"steps": bad}})[0], "bad_request", "condition on a later step")

	// Stored SQL: unknown ids, reuse and close.
	wantErr(t, h.pipeline(map[string]any{"type": "execute", "stmt": map[string]any{"sql_id": 7}})[0], "bad_request", "unknown sql_id")
	res := h.pipeline(
		map[string]any{"type": "store_sql", "sql_id": 7, "sql": "SELECT 7"},
		map[string]any{"type": "store_sql", "sql_id": 7, "sql": "SELECT 8"},
		map[string]any{"type": "execute", "stmt": map[string]any{"sql_id": 7}},
		map[string]any{"type": "close_sql", "sql_id": 7},
		map[string]any{"type": "execute", "stmt": map[string]any{"sql_id": 7}},
		map[string]any{"type": "nope"},
		closeReq,
		exec("SELECT 1"),
	)
	h.ok(res[0])
	wantErr(t, res[1], "bad_request", "sql_id reused")
	h.ok(res[2])
	h.ok(res[3])
	wantErr(t, res[4], "bad_request", "closed sql_id")
	wantErr(t, res[5], "bad_request", "unknown request type")
	wantErr(t, res[7], "bad_request", "request after close")
}

func TestLibSQLRoutes(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	for _, p := range []string{"/libsql/app/main/v2", "/libsql/app/main/v3"} {
		r := e.do("", "GET", "", p, nil)
		if r.status != 200 {
			t.Fatalf("GET %s: %d", p, r.status)
		}
	}
	if r := e.do("", "GET", "", "/libsql/app/main/v3-protobuf", nil); r.status != 404 {
		t.Fatalf("v3-protobuf: %d", r.status)
	}
	// A client URL without the branch or the trailing slash.
	r := e.do(e.admin, "POST", "", "/libsql/app/v2/pipeline", map[string]any{"baton": nil, "requests": []any{}})
	if r.status != 404 || !strings.Contains(string(r.body), "trailing slash") {
		t.Fatalf("no branch: %d %s", r.status, r.body)
	}
	// v3 works the same as v2.
	h := e.libsql(e.admin, "app", "main")
	h.path = "/libsql/app/main/v3"
	if got := rows(t, h.run("SELECT 40 + 2"))[0][0].(map[string]any)["value"]; got != "42" {
		t.Fatalf("v3: %v", got)
	}
	// Malformed bodies.
	status, _, body := h.post(h.path+"/pipeline", "not json")
	if status != 400 || !strings.Contains(string(body), "bad_request") {
		t.Fatalf("bad body: %d %s", status, body)
	}
}

func TestLibSQLCursor(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("app")
	h := e.libsql(e.admin, "app", "main")
	batch := map[string]any{"steps": []any{
		map[string]any{"stmt": stmtOf("SELECT 1 AS a UNION ALL SELECT 2")},
		map[string]any{"stmt": stmtOf("SELECT * FROM nope")},
		map[string]any{"stmt": stmtOf("SELECT 3"), "condition": map[string]any{"type": "ok", "step": 1}},
	}}
	status, _, body := h.post("/libsql/app/main/v3/cursor", map[string]any{"baton": nil, "batch": batch})
	if status != 200 {
		t.Fatalf("cursor: %d %s", status, body)
	}
	var types []string
	sc := bufio.NewScanner(bytes.NewReader(body))
	first := true
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if first {
			if _, ok := m["baton"].(string); !ok {
				t.Fatalf("first line: %v", m)
			}
			first = false
			continue
		}
		types = append(types, m["type"].(string))
	}
	if got := strings.Join(types, ","); got != "step_begin,row,row,step_end,step_error" {
		t.Fatalf("entries: %s", got)
	}
}
