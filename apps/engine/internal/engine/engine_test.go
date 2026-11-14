package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/value"
)

func TestCreateDatabase(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxDatabases = 2 })
	d := mustCreate(t, e, "app")
	if d.Name != "app" || d.DefaultBranchID == "" {
		t.Fatalf("database: %+v", d)
	}
	_, err := e.CreateDatabase(context.Background(), ws(e), "app")
	wantCode(t, err, apierr.NameTaken, "duplicate name")
	for _, bad := range []string{"", "App", "-x", "a_b", "a/b", "../x", string(make([]byte, 64))} {
		_, err := e.CreateDatabase(context.Background(), ws(e), bad)
		wantCode(t, err, apierr.InvalidName, fmt.Sprintf("name %q", bad))
	}
	mustCreate(t, e, "two")
	_, err = e.CreateDatabase(context.Background(), ws(e), "three")
	wantCode(t, err, apierr.LimitReached, "database cap")
	// Reachable by name and by id; the default branch by name too.
	mustQuery(t, e, d.ID, Read, "SELECT 1")
	if _, err := e.Query(context.Background(), Target{Workspace: ws(e), Database: "app", Branch: "main"}, Read, stmt("SELECT 1")); err != nil {
		t.Fatal(err)
	}
	_, err = e.Query(context.Background(), Target{Workspace: ws(e), Database: "nope"}, Read, stmt("SELECT 1"))
	wantCode(t, err, apierr.DatabaseNotFound, "missing database")
	_, err = e.Query(context.Background(), Target{Workspace: ws(e), Database: "app", Branch: "dev"}, Read, stmt("SELECT 1"))
	wantCode(t, err, apierr.BranchNotFound, "missing branch")
}

func TestQueryShape(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(id INTEGER PRIMARY KEY, n INTEGER, r REAL, s TEXT, b BLOB)")
	r := mustQuery(t, e, "a", Write, "INSERT INTO t (n, r, s, b) VALUES (?, ?, ?, ?), (NULL, 1.0, '', x'')",
		int64(9007199254740993), 2.5, "héllo", []byte{0, 1})
	if r.RowsAffected != 2 || r.LastInsertRowID != 2 {
		t.Fatalf("insert: %+v", r)
	}
	r = mustQuery(t, e, "a", Write, "CREATE INDEX i ON t(n)")
	if r.RowsAffected != 0 || r.LastInsertRowID != 0 {
		t.Fatalf("DDL reported changes: %+v", r)
	}
	r = mustQuery(t, e, "a", Read, "SELECT n, r, s, b, n AS n FROM t ORDER BY id")
	got, _ := json.Marshal(r)
	want := `{"columns":[{"name":"n","decltype":"INTEGER"},{"name":"r","decltype":"REAL"},{"name":"s","decltype":"TEXT"},{"name":"b","decltype":"BLOB"},{"name":"n","decltype":"INTEGER"}],` +
		`"rows":[[{"type":"integer","value":"9007199254740993"},{"type":"float","value":2.5},{"type":"text","value":"héllo"},{"type":"blob","base64":"AAE="},{"type":"integer","value":"9007199254740993"}],` +
		`[{"type":"null"},{"type":"float","value":1},{"type":"text","value":""},{"type":"blob","base64":""},{"type":"null"}]],"rows_affected":0,"last_insert_rowid":0}`
	if string(got) != want {
		t.Fatalf("result:\n got %s\nwant %s", got, want)
	}
	// An empty result still has its columns.
	r = mustQuery(t, e, "a", Read, "SELECT id FROM t WHERE id < 0")
	if len(r.Columns) != 1 || r.Rows == nil || len(r.Rows) != 0 {
		t.Fatalf("empty result: %+v", r)
	}
}

func TestStatementRules(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	for sql, code := range map[string]string{
		"":                      apierr.EmptyStatement,
		"  -- just a comment\n": apierr.EmptyStatement,
		"SELECT 1; SELECT 2":    apierr.MultipleStatements,
		"SELECT 1; garbage(":    apierr.MultipleStatements,
		"SELEC 1":               apierr.SQLError,
		"SELECT * FROM missing": apierr.SQLError,
	} {
		_, err := query(e, "a", Read, sql)
		wantCode(t, err, code, fmt.Sprintf("%q", sql))
	}
	mustQuery(t, e, "a", Read, "SELECT 1; -- trailing comment")

	_, err := query(e, "a", Read, "SELECT ?, ?", 1)
	wantCode(t, err, apierr.InvalidParams, "too few args")
	_, err = query(e, "a", Read, "SELECT ?", 1, 2)
	wantCode(t, err, apierr.InvalidParams, "too many args")

	named := func(sql string, m map[string]value.Value) error {
		_, err := e.Query(context.Background(), Target{Workspace: ws(e), Database: "a"}, Read, Statement{SQL: sql, Named: m})
		return err
	}
	if err := named("SELECT :a, @b, $c", map[string]value.Value{"a": value.IntValue(1), ":b": value.IntValue(2), "$c": value.IntValue(3)}); err == nil {
		t.Fatal("prefix mismatch @b vs :b accepted")
	}
	if err := named("SELECT :a, @b, $c", map[string]value.Value{"a": value.IntValue(1), "@b": value.IntValue(2), "c": value.IntValue(3)}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, named("SELECT :a", map[string]value.Value{"a": value.IntValue(1), "x": value.IntValue(2)}), apierr.InvalidParams, "extra named")
	wantCode(t, named("SELECT :a, :b", map[string]value.Value{"a": value.IntValue(1)}), apierr.InvalidParams, "missing named")
	wantCode(t, named("SELECT ?", map[string]value.Value{"a": value.IntValue(1)}), apierr.InvalidParams, "named for positional")
}

func TestBatchAtomic(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(x UNIQUE)")
	res, err := e.Batch(context.Background(), Target{Workspace: ws(e), Database: "a"}, Write, []Statement{
		stmt("INSERT INTO t VALUES (1)"), stmt("INSERT INTO t VALUES (2)"), stmt("SELECT count(*) FROM t"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || res[2].Rows[0][0].Int != 2 {
		t.Fatalf("batch: %+v", res)
	}
	_, err = e.Batch(context.Background(), Target{Workspace: ws(e), Database: "a"}, Write, []Statement{
		stmt("INSERT INTO t VALUES (3)"), stmt("INSERT INTO t VALUES (1)"),
	})
	wantCode(t, err, apierr.ConstraintViolation, "duplicate in batch")
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM t")); v.Int != 2 {
		t.Fatalf("failed batch kept rows: %d", v.Int)
	}
	_, err = e.Batch(context.Background(), Target{Workspace: ws(e), Database: "a"}, Write, nil)
	wantCode(t, err, apierr.EmptyStatement, "empty batch")
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.ReadersPerBranch = 2 })
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(x)")
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				if _, err := query(e, "a", Write, "INSERT INTO t VALUES (?)", i); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	for r := 0; r < 6; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				if _, err := query(e, "a", Read, "SELECT count(*) FROM t"); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM t")); v.Int != 240 {
		t.Fatalf("rows = %d, want 240", v.Int)
	}
}

func TestOpenBranchCapAndIdleClose(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxOpenBranches = 2 })
	for _, n := range []string{"a", "b", "c", "d"} {
		mustCreate(t, e, n)
		mustQuery(t, e, n, Write, "CREATE TABLE t(x)")
	}
	for _, n := range []string{"a", "b", "c", "d", "a"} {
		mustQuery(t, e, n, Read, "SELECT count(*) FROM t")
	}
	e.poolsMu.Lock()
	open := len(e.pools)
	e.poolsMu.Unlock()
	if open > 2 {
		t.Fatalf("%d pools open, cap 2", open)
	}
	e.closeIdle(time.Now().Add(time.Second))
	e.poolsMu.Lock()
	open = len(e.pools)
	e.poolsMu.Unlock()
	if open != 0 {
		t.Fatalf("%d pools left after idle close", open)
	}
	// Closed pools reopen on demand with their data.
	mustQuery(t, e, "a", Read, "SELECT count(*) FROM t")
}

func TestReopenKeepsData(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	e, err := Open(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(x)")
	mustQuery(t, e, "a", Write, "INSERT INTO t VALUES (7)")
	total := e.TotalBytes()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e2, err := Open(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if v := scalar(t, mustQuery(t, e2, "a", Read, "SELECT x FROM t")); v.Int != 7 {
		t.Fatalf("x = %d", v.Int)
	}
	if e2.TotalBytes() == 0 || total == 0 {
		t.Fatalf("usage not measured: %d then %d", total, e2.TotalBytes())
	}
}

// PLAN 6.10: a meta.db newer than the binary (a bad rollback) makes the
// engine refuse to serve, so /health fails.
func TestNewerMetaRefuses(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	e, err := Open(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Ready(context.Background()); err != nil {
		t.Fatalf("fresh engine not ready: %v", err)
	}
	mustCreate(t, e, "a")
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sqlite3.Open(e.Layout().Meta())
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	e2, err := Open(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	wantCode(t, e2.Ready(context.Background()), apierr.Unavailable, "ready")
	_, err = e2.CreateDatabase(context.Background(), ws(e2), "b")
	wantCode(t, err, apierr.Unavailable, "create")
	_, err = query(e2, "a", Read, "SELECT 1")
	wantCode(t, err, apierr.Unavailable, "query")
}
