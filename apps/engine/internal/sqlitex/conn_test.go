package sqlitex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3"
)

func openUser(t *testing.T, path string, readOnly bool) *Conn {
	t.Helper()
	c, err := Open(context.Background(), Options{Path: path, ReadOnly: readOnly, Create: !readOnly})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func userText(c *Conn, sql string) (string, error) {
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

func TestOpenSettings(t *testing.T) {
	c := openUser(t, filepath.Join(t.TempDir(), "a.db"), false)
	for sql, want := range map[string]string{
		"PRAGMA journal_mode": "wal",
		"PRAGMA foreign_keys": "1",
	} {
		if got, err := userText(c, sql); err != nil || got != want {
			t.Errorf("%s: got %q %v, want %q", sql, got, err, want)
		}
	}
	for op, want := range map[sqlite3.DBConfig]bool{
		sqlite3.DBCONFIG_DEFENSIVE:      true,
		sqlite3.DBCONFIG_TRUSTED_SCHEMA: false,
	} {
		if got, _ := c.Config(op); got != want {
			t.Errorf("config %d: got %v, want %v", op, got, want)
		}
	}
}

// The VFS refuses other files even when the authorizer and the attach limit
// are out of the way, which only engine code can arrange.
func TestConfinedVFS(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other.db")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c := openUser(t, filepath.Join(dir, "a.db"), false)
	c.Limit(sqlite3.LIMIT_ATTACHED, 2)
	if err := c.ExecTrusted("ATTACH '" + other + "' AS o"); !errors.Is(err, sqlite3.CANTOPEN) {
		t.Fatalf("attach other: got %v, want CANTOPEN", err)
	}
	if err := c.ExecTrusted("VACUUM INTO '" + filepath.Join(dir, "v.db") + "'"); !errors.Is(err, sqlite3.CANTOPEN) {
		t.Fatalf("vacuum into: got %v, want CANTOPEN", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "v.db")); !os.IsNotExist(err) {
		t.Fatalf("vacuum target exists: %v", err)
	}
	// The branch's own file still works.
	if err := c.Exec("CREATE TABLE t(x); INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizerPragmas(t *testing.T) {
	c := openUser(t, filepath.Join(t.TempDir(), "a.db"), false)
	if err := c.Exec("CREATE TABLE t(x)"); err != nil {
		t.Fatal(err)
	}
	allowed := []string{
		"PRAGMA table_info(t)", "SELECT * FROM pragma_table_info('t')", "PRAGMA user_version",
		"PRAGMA user_version = 3", "PRAGMA journal_mode", "PRAGMA integrity_check",
		"PRAGMA foreign_keys = OFF", "PRAGMA main.page_count",
	}
	denied := []string{
		"PRAGMA journal_mode = DELETE", "PRAGMA writable_schema = ON", "PRAGMA temp_store_directory = '/tmp'",
		"PRAGMA locking_mode = EXCLUSIVE", "PRAGMA database_list", "PRAGMA temp_store = FILE",
		"PRAGMA max_page_count = 1", "PRAGMA mmap_size = 1000000", "PRAGMA cache_size = 1",
		"SELECT * FROM pragma_database_list",
	}
	for _, sql := range allowed {
		if _, err := userText(c, sql); err != nil {
			t.Errorf("%s: denied: %v", sql, err)
		}
	}
	for _, sql := range denied {
		if _, err := userText(c, sql); !errors.Is(err, sqlite3.AUTH) {
			t.Errorf("%s: got %v, want AUTH", sql, err)
		}
	}
	if err := c.Reset(); err != nil {
		t.Fatal(err)
	}
	if got, _ := userText(c, "PRAGMA foreign_keys"); got != "1" {
		t.Fatalf("reset left foreign_keys = %s", got)
	}
}

func TestAuthorizerStatements(t *testing.T) {
	c := openUser(t, filepath.Join(t.TempDir(), "a.db"), false)
	if err := c.Exec("CREATE TABLE users(name)"); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"BEGIN", "COMMIT", "SAVEPOINT s", "CREATE TEMP TABLE x(a)", "CREATE TEMP VIEW v AS SELECT 1",
		"ATTACH ':memory:' AS m", "DETACH main",
		// Temp objects made by naming the schema instead of the TEMP keyword.
		"CREATE TABLE temp.users(name)", "CREATE TABLE TEMP.x(a)", "CREATE VIEW temp.v AS SELECT 1",
		"CREATE TRIGGER temp.tr AFTER INSERT ON main.users BEGIN SELECT 1; END",
		"CREATE VIRTUAL TABLE temp.f USING fts5(b)",
		"INSERT INTO temp.sqlite_master VALUES ('table', 'x', 'x', 0, 'CREATE TABLE x(a)')",
		"CREATE TABLE temp.t2 AS SELECT * FROM users",
	} {
		if _, err := userText(c, sql); !errors.Is(err, sqlite3.AUTH) {
			t.Errorf("%s: got %v, want AUTH", sql, err)
		}
	}
	// SQLite itself refuses a temp index on a main table.
	if _, err := userText(c, "CREATE INDEX temp.i ON users(name)"); err == nil {
		t.Error("temp index on a main table allowed")
	}
	if _, err := userText(c, "SELECT count(*) FROM temp.sqlite_schema"); err != nil {
		t.Errorf("reading temp schema: %v", err)
	}
	if err := c.Exec("CREATE VIRTUAL TABLE f USING fts5(body)"); err != nil {
		t.Fatalf("fts5: %v", err)
	}
	// Every ALTER TABLE form works, including renames, which touch the
	// (empty) temp schema internally.
	for _, sql := range []string{
		"CREATE TABLE r(x)", "ALTER TABLE r RENAME TO r2", "ALTER TABLE r2 RENAME COLUMN x TO y",
		"ALTER TABLE r2 ADD COLUMN z", "ALTER TABLE r2 DROP COLUMN z",
	} {
		if err := c.Exec(sql); err != nil {
			t.Errorf("%s: %v", sql, err)
		}
	}
	if !c.GetAutocommit() {
		t.Fatal("a denied BEGIN left a transaction open")
	}
}

// Reset refuses to hand back a connection that somehow holds a temp object.
func TestResetRejectsTempObjects(t *testing.T) {
	c := openUser(t, filepath.Join(t.TempDir(), "a.db"), false)
	if err := c.ExecTrusted("CREATE TEMP TABLE leftover(a)"); err != nil {
		t.Fatal(err)
	}
	if err := c.Reset(); !errors.Is(err, ErrDirty) {
		t.Fatalf("reset: got %v, want ErrDirty", err)
	}
}

func TestReadOnlyConn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	w := openUser(t, path, false)
	if err := w.Exec("CREATE TABLE t(x)"); err != nil {
		t.Fatal(err)
	}
	r := openUser(t, path, true)
	if err := r.Exec("INSERT INTO t VALUES (1)"); !errors.Is(err, sqlite3.READONLY) {
		t.Fatalf("insert on read-only: got %v", err)
	}
	if _, err := Open(context.Background(), Options{Path: path, ReadOnly: true, Create: true}); !errors.Is(err, ErrReadOnlyCreate) {
		t.Fatalf("read-only create: got %v", err)
	}
}

// Savepoints are allowed only while the engine says so, BEGIN and COMMIT
// stay denied even then, and Reset turns the allowance off.
func TestAllowSavepoints(t *testing.T) {
	c := openUser(t, filepath.Join(t.TempDir(), "a.db"), false)
	if err := c.Exec("CREATE TABLE t(x)"); err != nil {
		t.Fatal(err)
	}
	if err := c.ExecTrusted("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	c.AllowSavepoints(true)
	for _, sql := range []string{"SAVEPOINT a", "INSERT INTO t VALUES (1)", "ROLLBACK TO a", "RELEASE a", "SAVEPOINT b", "INSERT INTO t VALUES (2)", "RELEASE SAVEPOINT b"} {
		if err := c.Exec(sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	for _, sql := range []string{"COMMIT", "ROLLBACK", "END", "BEGIN"} {
		if _, err := userText(c, sql); !errors.Is(err, sqlite3.AUTH) {
			t.Errorf("%s with savepoints allowed: got %v, want AUTH", sql, err)
		}
	}
	if c.GetAutocommit() {
		t.Fatal("a savepoint ended the engine's transaction")
	}
	if err := c.ExecTrusted("COMMIT"); err != nil {
		t.Fatal(err)
	}
	if got, _ := userText(c, "SELECT group_concat(x) FROM t"); got != "2" {
		t.Fatalf("rows after savepoints: %q", got)
	}
	if err := c.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, err := userText(c, "SAVEPOINT s"); !errors.Is(err, sqlite3.AUTH) {
		t.Fatalf("savepoint after reset: got %v, want AUTH", err)
	}
}
