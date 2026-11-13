package sqlitex

// The driver spike (PLAN 6.1, ADR 0001). Each test proves one capability the
// engine needs from github.com/ncruces/go-sqlite3, on the raw driver, so a
// driver upgrade that drops one fails here first.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

func rawOpen(t *testing.T, path string) *sqlite3.Conn {
	t.Helper()
	c, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func queryText(t *testing.T, c *sqlite3.Conn, sql string) (string, error) {
	t.Helper()
	stmt, _, err := c.Prepare(sql)
	if err != nil {
		return "", err
	}
	defer stmt.Close()
	var out string
	if stmt.Step() {
		out = stmt.ColumnText(0)
	}
	return out, stmt.Err()
}

// 1. A per-connection authorizer that can deny statements.
func TestDriverAuthorizer(t *testing.T) {
	c := rawOpen(t, filepath.Join(t.TempDir(), "a.db"))
	var seen []sqlite3.AuthorizerActionCode
	err := c.SetAuthorizer(func(a sqlite3.AuthorizerActionCode, _, _, _, _ string) sqlite3.AuthorizerReturnCode {
		seen = append(seen, a)
		if a == sqlite3.AUTH_ATTACH {
			return sqlite3.AUTH_DENY
		}
		return sqlite3.AUTH_OK
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = queryText(t, c, "ATTACH 'x.db' AS x")
	if !errors.Is(err, sqlite3.AUTH) {
		t.Fatalf("attach: got %v, want AUTH", err)
	}
	// VACUUM INTO attaches its target, so the same rule catches it.
	_, err = queryText(t, c, "VACUUM INTO 'y.db'")
	if !errors.Is(err, sqlite3.AUTH) {
		t.Fatalf("vacuum into: got %v, want AUTH", err)
	}
	if len(seen) == 0 {
		t.Fatal("authorizer never ran")
	}
}

// 2. Runtime limits.
func TestDriverLimits(t *testing.T) {
	c := rawOpen(t, filepath.Join(t.TempDir(), "a.db"))
	c.Limit(sqlite3.LIMIT_ATTACHED, 0)
	c.Limit(sqlite3.LIMIT_LENGTH, 1000)
	c.Limit(sqlite3.LIMIT_SQL_LENGTH, 200)
	c.Limit(sqlite3.LIMIT_VARIABLE_NUMBER, 2)
	for _, tc := range []struct {
		sql  string
		want sqlite3.ErrorCode
	}{
		{"ATTACH 'x.db' AS x", sqlite3.ERROR},
		{"SELECT length(randomblob(2000))", sqlite3.TOOBIG},
		{"SELECT '" + strings.Repeat("a", 300) + "'", sqlite3.TOOBIG},
		{"SELECT ?1, ?2, ?3", sqlite3.ERROR},
	} {
		_, err := queryText(t, c, tc.sql)
		if !errors.Is(err, tc.want) {
			t.Errorf("%.40q: got %v, want %v", tc.sql, err, tc.want)
		}
	}
	if got := c.Limit(sqlite3.LIMIT_ATTACHED, -1); got != 0 {
		t.Fatalf("attached limit reads %d", got)
	}
}

// 3. Cancelling a running statement from a context deadline.
func TestDriverInterrupt(t *testing.T) {
	c := rawOpen(t, filepath.Join(t.TempDir(), "a.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c.SetInterrupt(ctx)
	start := time.Now()
	_, err := queryText(t, c, "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r) SELECT count(*) FROM r")
	if !errors.Is(err, sqlite3.INTERRUPT) {
		t.Fatalf("got %v, want INTERRUPT", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("interrupt took %v", d)
	}
	c.SetInterrupt(context.Background())
	if got, err := queryText(t, c, "SELECT 1"); err != nil || got != "1" {
		t.Fatalf("connection unusable after interrupt: %q %v", got, err)
	}
}

// 4. An online copy of a live database.
func TestDriverBackup(t *testing.T) {
	dir := t.TempDir()
	c := rawOpen(t, filepath.Join(dir, "a.db"))
	if err := c.Exec("PRAGMA journal_mode=WAL; CREATE TABLE t(x); INSERT INTO t VALUES (1),(2)"); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.db")
	if err := c.Backup("main", dst); err != nil {
		t.Fatal(err)
	}
	cp := rawOpen(t, dst)
	if got, err := queryText(t, cp, "SELECT count(*) FROM t"); err != nil || got != "2" {
		t.Fatalf("copy: %q %v", got, err)
	}
	if got, err := queryText(t, cp, "PRAGMA integrity_check"); err != nil || got != "ok" {
		t.Fatalf("integrity: %q %v", got, err)
	}
}

// 5. Read-only connections.
func TestDriverReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	c := rawOpen(t, path)
	if err := c.Exec("CREATE TABLE t(x)"); err != nil {
		t.Fatal(err)
	}
	ro, err := sqlite3.OpenFlags(path, sqlite3.OPEN_READONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	for _, sql := range []string{"INSERT INTO t VALUES (1)", "CREATE TABLE u(x)", "PRAGMA user_version = 1"} {
		if err := ro.Exec(sql); !errors.Is(err, sqlite3.READONLY) {
			t.Errorf("%s: got %v, want READONLY", sql, err)
		}
	}
}

// 6. JSON1 and FTS5.
func TestDriverJSONAndFTS5(t *testing.T) {
	c := rawOpen(t, filepath.Join(t.TempDir(), "a.db"))
	if got, err := queryText(t, c, `SELECT json_extract('{"a":[1,2]}', '$.a[1]')`); err != nil || got != "2" {
		t.Fatalf("json: %q %v", got, err)
	}
	if err := fts5.Register(c); err != nil {
		t.Fatal(err)
	}
	if err := c.Exec("CREATE VIRTUAL TABLE f USING fts5(body); INSERT INTO f VALUES ('lumbung stores rice'), ('other')"); err != nil {
		t.Fatal(err)
	}
	if got, err := queryText(t, c, "SELECT body FROM f WHERE f MATCH 'rice'"); err != nil || got != "lumbung stores rice" {
		t.Fatalf("fts5: %q %v", got, err)
	}
}

// load_extension isn't compiled into the driver at all.
func TestDriverHasNoLoadExtension(t *testing.T) {
	c := rawOpen(t, filepath.Join(t.TempDir(), "a.db"))
	if _, err := queryText(t, c, "SELECT load_extension('x')"); err == nil {
		t.Fatal("load_extension exists")
	}
}
