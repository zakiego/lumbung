package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

func (e *env) mcp(token string) *mcp.ClientSession {
	e.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             e.http.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearer{token, http.DefaultTransport}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	s, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		e.t.Fatalf("connect: %v", err)
	}
	e.t.Cleanup(func() { _ = s.Close() })
	return s
}

// call runs a tool and returns its structured output and whether it failed.
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v", name, err)
	}
	if res.IsError {
		text := ""
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		return nil, text
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out, ""
}

func TestMCPTools(t *testing.T) {
	e := newEnv(t, nil)
	s := e.mcp(e.admin)

	tools, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	annotations := map[string]*mcp.ToolAnnotations{}
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
		annotations[tl.Name] = tl.Annotations
		if tl.Description == "" || tl.InputSchema == nil {
			t.Errorf("%s needs a description and an input schema", tl.Name)
		}
	}
	sort.Strings(names)
	want := []string{"create_branch", "create_database", "create_snapshot", "delete_branch", "diff_branches", "execute",
		"get_database", "get_schema", "list_branches", "list_databases", "list_snapshots", "query", "reset_branch"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools: %v", names)
	}
	for _, n := range []string{"list_databases", "get_database", "list_branches", "diff_branches", "get_schema", "query", "list_snapshots"} {
		if a := annotations[n]; a == nil || !a.ReadOnlyHint {
			t.Errorf("%s should be read-only", n)
		}
	}
	for _, n := range []string{"delete_branch", "reset_branch", "execute"} {
		if a := annotations[n]; a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
			t.Errorf("%s should be destructive", n)
		}
	}
	for _, n := range []string{"create_database", "create_branch", "create_snapshot"} {
		if a := annotations[n]; a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%s should be additive", n)
		}
	}

	if _, msg := call(t, s, "create_database", map[string]any{"name": "shop"}); msg != "" {
		t.Fatal(msg)
	}
	if _, msg := call(t, s, "execute", map[string]any{"database": "shop", "sql": "CREATE TABLE items(id INTEGER PRIMARY KEY, name TEXT, price REAL, data BLOB)"}); msg != "" {
		t.Fatal(msg)
	}
	out, msg := call(t, s, "execute", map[string]any{"database": "shop", "sql": "INSERT INTO items (name, price) VALUES (?, ?), (?, ?)", "args": []any{"tea", 1.5, "rice", 2}})
	if msg != "" || out["rows_affected"].(float64) != 2 {
		t.Fatalf("insert: %v %s", out, msg)
	}
	out, _ = call(t, s, "query", map[string]any{"database": "shop", "sql": "SELECT name, price FROM items ORDER BY id"})
	if out["row_count"].(float64) != 2 || out["truncated"] != false {
		t.Fatalf("query: %v", out)
	}
	if row := out["rows"].([]any)[1].([]any); row[0] != "rice" || row[1].(float64) != 2 {
		t.Fatalf("row: %v", row)
	}

	// query is always read-only, even for an admin token.
	_, msg = call(t, s, "query", map[string]any{"database": "shop", "sql": "DELETE FROM items"})
	if !strings.Contains(msg, "read_only") {
		t.Fatalf("query wrote: %q", msg)
	}
	// The isolation rules hold through MCP too.
	_, msg = call(t, s, "execute", map[string]any{"database": "shop", "sql": "ATTACH 'x.db' AS x"})
	if !strings.Contains(msg, "statement_denied") {
		t.Fatalf("attach: %q", msg)
	}

	// Big results are cut to max_rows with a note, not an error.
	call(t, s, "execute", map[string]any{"database": "shop", "sql": "INSERT INTO items (name) SELECT 'n' || value FROM generate_series(1, 300)"})
	out, msg = call(t, s, "query", map[string]any{"database": "shop", "sql": "SELECT name FROM items", "max_rows": 10})
	if msg != "" || out["row_count"].(float64) != 10 || out["truncated"] != true || !strings.Contains(out["note"].(string), "first 10 rows") {
		t.Fatalf("truncation: %v %s", out, msg)
	}

	// Branch workflow.
	if _, msg := call(t, s, "create_branch", map[string]any{"database": "shop", "name": "try"}); msg != "" {
		t.Fatal(msg)
	}
	call(t, s, "execute", map[string]any{"database": "shop", "branch": "try", "sql": "ALTER TABLE items ADD COLUMN stock INTEGER"})
	out, _ = call(t, s, "diff_branches", map[string]any{"database": "shop", "branch": "try"})
	if changes := out["changes"].([]any); len(changes) != 1 {
		t.Fatalf("diff: %v", out)
	}
	out, _ = call(t, s, "get_schema", map[string]any{"database": "shop", "branch": "try"})
	if len(out["tables"].([]any)) != 1 {
		t.Fatalf("schema: %v", out)
	}
	if _, msg := call(t, s, "create_snapshot", map[string]any{"database": "shop", "branch": "try", "label": "x"}); msg != "" {
		t.Fatal(msg)
	}
	out, _ = call(t, s, "list_snapshots", map[string]any{"database": "shop", "branch": "try"})
	if len(out["snapshots"].([]any)) != 1 {
		t.Fatalf("snapshots: %v", out)
	}
	if _, msg := call(t, s, "reset_branch", map[string]any{"database": "shop", "branch": "try"}); msg != "" {
		t.Fatal(msg)
	}
	_, msg = call(t, s, "delete_branch", map[string]any{"database": "shop", "branch": "main"})
	if !strings.Contains(msg, "branch_protected") {
		t.Fatalf("delete main: %q", msg)
	}
	if _, msg := call(t, s, "delete_branch", map[string]any{"database": "shop", "branch": "try"}); msg != "" {
		t.Fatal(msg)
	}
	out, _ = call(t, s, "list_databases", nil)
	if len(out["databases"].([]any)) != 1 {
		t.Fatalf("list_databases: %v", out)
	}

	// Management through MCP is audited.
	// Only successful entries count: the failed delete above is audited too.
	audit := e.ok(e.do(e.admin, "GET", "/v1/audit", "/v1/audit", nil), 200).json(t)
	actions := map[string]bool{}
	for _, raw := range audit["data"].([]any) {
		entry := raw.(map[string]any)
		if entry["result"] == "ok" {
			actions[entry["action"].(string)] = true
		}
		if id, _ := entry["request_id"].(string); id == "" || id == "mcp" {
			t.Errorf("audit entry without its own request id: %v", entry)
		}
	}
	for _, a := range []string{"database.create", "branch.create", "branch.reset", "branch.delete", "snapshot.create"} {
		if !actions[a] {
			t.Errorf("audit is missing %s", a)
		}
	}
}

func TestMCPScopes(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("a")
	e.createDB("b")
	e.ok(e.sql(e.admin, "a", "CREATE TABLE t(x)"), 200)
	read, _ := e.token(map[string]any{"name": "r", "database": "a", "access": "read"})
	s := e.mcp(read)

	out, _ := call(t, s, "list_databases", nil)
	if n := len(out["databases"].([]any)); n != 1 {
		t.Fatalf("read token sees %d databases", n)
	}
	if _, msg := call(t, s, "query", map[string]any{"database": "a", "sql": "SELECT * FROM t"}); msg != "" {
		t.Fatal(msg)
	}
	for tool, args := range map[string]map[string]any{
		"execute":         {"database": "a", "sql": "INSERT INTO t VALUES (1)"},
		"create_branch":   {"database": "a", "name": "x"},
		"create_database": {"name": "c"},
		"create_snapshot": {"database": "a"},
	} {
		if _, msg := call(t, s, tool, args); !strings.Contains(msg, "forbidden") {
			t.Errorf("%s with a read token: %q", tool, msg)
		}
	}
	if _, msg := call(t, s, "query", map[string]any{"database": "b", "sql": "SELECT 1"}); !strings.Contains(msg, "database_not_found") {
		t.Fatalf("other database: %q", msg)
	}
}

func TestMCPNeedsAToken(t *testing.T) {
	e := newEnv(t, nil)
	req, _ := http.NewRequest("POST", e.http.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", res.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer lmb_aaaaaaaaaaaaaaaaaaaa_"+strings.Repeat("b", 40))
	req.Body = http.NoBody
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", res.StatusCode)
	}
}

func TestMCPLargeIntegerArgs(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("a")
	e.ok(e.sql(e.admin, "a", "CREATE TABLE t(id INTEGER PRIMARY KEY)"), 200)
	e.ok(e.sql(e.admin, "a", "INSERT INTO t VALUES (9007199254740992), (9007199254740993)"), 200)
	s := e.mcp(e.admin)

	// As a JSON number it would round to the other row, so it's refused.
	_, msg := call(t, s, "execute", map[string]any{"database": "a", "sql": "DELETE FROM t WHERE id = ?", "args": []any{9007199254740993}})
	if !strings.Contains(msg, "invalid_params") {
		t.Fatalf("large integer arg: %q", msg)
	}
	// As a string it matches exactly.
	out, msg := call(t, s, "execute", map[string]any{"database": "a", "sql": "DELETE FROM t WHERE id = ?", "args": []any{"9007199254740993"}})
	if msg != "" || out["rows_affected"].(float64) != 1 {
		t.Fatalf("string arg: %v %q", out, msg)
	}
	out, _ = call(t, s, "query", map[string]any{"database": "a", "sql": "SELECT id FROM t"})
	if rows := out["rows"].([]any); len(rows) != 1 || rows[0].([]any)[0].(float64) != 9007199254740992 {
		t.Fatalf("left: %v", out)
	}
}

func TestMCPCapsWideResults(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("a")
	e.ok(e.sql(e.admin, "a", "CREATE TABLE docs(id INTEGER PRIMARY KEY, body TEXT)"), 200)
	e.ok(e.sql(e.admin, "a", "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 50) INSERT INTO docs SELECT i, printf('%.*c', 40000, 'x') FROM n"), 200)
	s := e.mcp(e.admin)

	// Each cell shows 2000 characters, so many 40 KB rows still fit.
	out, msg := call(t, s, "query", map[string]any{"database": "a", "sql": "SELECT * FROM docs"})
	if msg != "" {
		t.Fatal(msg)
	}
	if n := len(out["rows"].([]any)); n < 20 || out["truncated"] != true {
		t.Fatalf("wide rows: %d rows, truncated %v", n, out["truncated"])
	}
	cell := out["rows"].([]any)[0].([]any)[1].(string)
	if !strings.HasSuffix(cell, "(40000 characters)") {
		t.Fatalf("cell not shortened: %q", cell[len(cell)-30:])
	}

	// RETURNING rows from a write are capped too, and the note says the
	// write applied in full.
	out, msg = call(t, s, "execute", map[string]any{"database": "a", "sql": "UPDATE docs SET body = body || 'y' RETURNING *"})
	if msg != "" {
		t.Fatal(msg)
	}
	if out["truncated"] != true || out["rows_affected"].(float64) != 50 || !strings.Contains(out["note"].(string), "applied to all 50 rows") {
		t.Fatalf("execute: rows %d, %v %v %v", len(out["rows"].([]any)), out["truncated"], out["rows_affected"], out["note"])
	}
	if b, _ := json.Marshal(out["rows"]); len(b) > 64<<10 {
		t.Fatalf("execute rows are %d bytes", len(b))
	}
}

func TestMCPSchemaTable(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("a")
	e.ok(e.sql(e.admin, "a", "CREATE TABLE t(x)"), 200)
	e.ok(e.sql(e.admin, "a", "CREATE TABLE u(y)"), 200)
	e.ok(e.sql(e.admin, "a", "CREATE INDEX u_y ON u(y)"), 200)
	s := e.mcp(e.admin)
	out, msg := call(t, s, "get_schema", map[string]any{"database": "a", "table": "u"})
	if msg != "" {
		t.Fatal(msg)
	}
	if len(out["tables"].([]any)) != 1 || len(out["indexes"].([]any)) != 1 {
		t.Fatalf("one table: %v", out)
	}
	if _, msg := call(t, s, "get_schema", map[string]any{"database": "a", "table": "nope"}); !strings.Contains(msg, "not_found") {
		t.Fatalf("missing table: %q", msg)
	}

	// A schema too large for the cap comes back as names.
	var ddl strings.Builder
	for i := range 300 {
		ddl.WriteString("CREATE TABLE wide_" + strconv.Itoa(i) + "(a TEXT, b TEXT, c TEXT, d TEXT, e TEXT, f TEXT, g TEXT, h TEXT);")
	}
	e.ok(e.do(e.admin, "POST", pBatch, "/v1/databases/a/batch", map[string]any{"statements": splitDDL(ddl.String())}), 200)
	out, msg = call(t, s, "get_schema", map[string]any{"database": "a"})
	if msg != "" {
		t.Fatal(msg)
	}
	if out["names_only"] != true || out["truncated"] != true || len(out["tables"].([]any)) != 200 {
		t.Fatalf("large schema: names_only %v truncated %v tables %d", out["names_only"], out["truncated"], len(out["tables"].([]any)))
	}
}

func splitDDL(sql string) []map[string]any {
	var out []map[string]any
	for _, s := range strings.Split(sql, ";") {
		if strings.TrimSpace(s) != "" {
			out = append(out, map[string]any{"sql": s})
		}
	}
	return out
}

func TestMCPManagementScopes(t *testing.T) {
	e := newEnv(t, nil)
	e.createDB("a")
	admin := e.mcp(e.admin)
	if _, msg := call(t, admin, "create_branch", map[string]any{"database": "a", "name": "dev"}); msg != "" {
		t.Fatal(msg)
	}
	write, _ := e.token(map[string]any{"name": "w", "database": "a", "access": "write"})
	if _, msg := call(t, e.mcp(write), "delete_branch", map[string]any{"database": "a", "branch": "dev"}); !strings.Contains(msg, "forbidden") {
		t.Fatalf("delete_branch with a write token: %q", msg)
	}
	branchWrite, _ := e.token(map[string]any{"name": "bw", "database": "a", "branch": "dev", "access": "write"})
	if _, msg := call(t, e.mcp(branchWrite), "reset_branch", map[string]any{"database": "a", "branch": "dev"}); !strings.Contains(msg, "forbidden") {
		t.Fatalf("reset_branch with a branch write token: %q", msg)
	}
}

func TestMCPFailedAuthLockout(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AuthFailuresPerMin = 2 })
	bad := "Bearer lmb_aaaaaaaaaaaaaaaaaaaa_" + strings.Repeat("b", 40)
	var last *http.Response
	for range 4 {
		req, _ := http.NewRequest("POST", e.http.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", bad)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		last = res
	}
	if last.StatusCode != http.StatusTooManyRequests || last.Header.Get("Retry-After") == "" {
		t.Fatalf("lockout: %d, Retry-After %q", last.StatusCode, last.Header.Get("Retry-After"))
	}
	if !strings.HasPrefix(last.Header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("lockout content type %q", last.Header.Get("Content-Type"))
	}
}
