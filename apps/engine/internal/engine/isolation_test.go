package engine

// The isolation table, PLAN 6.2. One test per row, each running user SQL
// through the real query path (Engine.Query / Engine.Batch) and asserting it
// fails cleanly. These are never skipped, weakened or marked flaky.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// wantNoLoadExtension asserts load_extension failed because the function
// doesn't exist in this build: it isn't compiled into the driver.
func wantNoLoadExtension(t *testing.T, err error, what string) {
	t.Helper()
	wantCode(t, err, apierr.SQLError, what)
	if d := apierr.As(err).Detail; !strings.Contains(d, "no such function: load_extension") {
		t.Fatalf("%s: got %q, want no such function", what, d)
	}
}

func bothAccess(f func(a Access, name string)) {
	f(Read, "read")
	f(Write, "write")
}

// Row 1: ATTACH DATABASE '<other branch file>' and DETACH are denied.
func TestIsolationAttachDetach(t *testing.T) {
	e := newEngine(t, nil)
	victim := mustCreate(t, e, "victim")
	mustQuery(t, e, "victim", Write, "CREATE TABLE secret(x)")
	mustQuery(t, e, "victim", Write, "INSERT INTO secret VALUES ('s')")
	mustCreate(t, e, "attacker")
	victimFile := e.Layout().BranchFile(victim.ID, victim.DefaultBranchID)
	before := fileHash(t, victimFile)

	targets := []string{
		victimFile,
		"file:" + victimFile + "?vfs=os",
		"file:" + victimFile + "?mode=ro",
		e.Layout().Meta(),
		":memory:",
		"",
		filepath.Join(e.Layout().Root, "new.db"),
	}
	bothAccess(func(a Access, name string) {
		for _, target := range targets {
			_, err := query(e, "attacker", a, "ATTACH DATABASE ? AS o", target)
			wantCode(t, err, apierr.StatementDenied, name+" attach "+target)
			_, err = query(e, "attacker", a, "ATTACH '"+strings.ReplaceAll(target, "'", "''")+"' AS o")
			wantCode(t, err, apierr.StatementDenied, name+" attach literal "+target)
		}
		_, err := query(e, "attacker", a, "DETACH DATABASE main")
		wantCode(t, err, apierr.StatementDenied, name+" detach main")
		_, err = query(e, "attacker", a, "DETACH o")
		wantCode(t, err, apierr.StatementDenied, name+" detach o")
	})
	_, err := e.Batch(context.Background(), Target{Database: "attacker"}, Write, []Statement{
		stmt("CREATE TABLE t(x)"), stmt("ATTACH ? AS o", victimFile),
	})
	wantCode(t, err, apierr.StatementDenied, "attach in batch")

	if fileHash(t, victimFile) != before {
		t.Fatal("victim file changed")
	}
	if _, err := os.Stat(filepath.Join(e.Layout().Root, "new.db")); !os.IsNotExist(err) {
		t.Fatal("attach created a file")
	}
	// The batch rolled back as a whole.
	if _, err := query(e, "attacker", Read, "SELECT * FROM t"); err == nil {
		t.Fatal("table from the failed batch exists")
	}
}

// Row 2: VACUUM INTO '<path>' is denied.
func TestIsolationVacuumInto(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(x)")
	mustQuery(t, e, "a", Write, "INSERT INTO t VALUES (1)")
	for _, p := range []string{
		filepath.Join(e.Layout().Root, "copy.db"),
		filepath.Join(t.TempDir(), "out.db"),
		"relative.db",
		"file:" + filepath.Join(e.Layout().Root, "uri.db") + "?vfs=os",
	} {
		bothAccess(func(a Access, name string) {
			_, err := query(e, "a", a, "VACUUM INTO ?", p)
			wantCode(t, err, apierr.StatementDenied, name+" vacuum into "+p)
			_, err = query(e, "a", a, "VACUUM main INTO '"+p+"'")
			wantCode(t, err, apierr.StatementDenied, name+" vacuum main into "+p)
		})
		if !strings.HasPrefix(p, "file:") {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("%s was created", p)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(e.Layout().Root, "uri.db")); !os.IsNotExist(err) {
		t.Fatal("uri.db was created")
	}
	bothAccess(func(a Access, name string) {
		_, err := query(e, "a", a, "VACUUM")
		wantCode(t, err, apierr.StatementDenied, name+" plain vacuum")
	})
}

// Row 3: load_extension(...) as SQL function and as API is denied.
func TestIsolationLoadExtension(t *testing.T) {
	e := newEngine(t, nil)
	d := mustCreate(t, e, "a")
	bothAccess(func(a Access, name string) {
		for _, sql := range []string{
			"SELECT load_extension('/tmp/x.so')",
			"SELECT load_extension('/tmp/x.so', 'sqlite3_extension_init')",
		} {
			_, err := query(e, "a", a, sql)
			wantNoLoadExtension(t, err, name+" "+sql)
		}
	})
	// As API: the engine never exposes extension loading, and every user
	// connection has it switched off.
	p := e.acquirePool(d.DefaultBranchID, e.Layout().BranchFile(d.ID, d.DefaultBranchID))
	defer e.releasePool(p)
	w, err := p.acquireWriter(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	on, err := w.Config(sqlite3.DBCONFIG_ENABLE_LOAD_EXTENSION)
	p.releaseWriter(w)
	if err != nil || on {
		t.Fatalf("writer load_extension enabled=%v err=%v", on, err)
	}
	r, err := p.acquireReader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	on, err = r.Config(sqlite3.DBCONFIG_ENABLE_LOAD_EXTENSION)
	p.releaseReader(r)
	if err != nil || on {
		t.Fatalf("reader load_extension enabled=%v err=%v", on, err)
	}
}

// Row 4: writable_schema, journal_mode, temp_store_directory, locking_mode
// and any pragma not on the allowlist are denied or ignored without effect.
func TestIsolationPragmas(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE parent(id INTEGER PRIMARY KEY)")
	mustQuery(t, e, "a", Write, "CREATE TABLE child(pid INTEGER REFERENCES parent(id))")

	denied := []string{
		"PRAGMA writable_schema = ON", "PRAGMA writable_schema = RESET", "PRAGMA writable_schema",
		"PRAGMA journal_mode = DELETE", "PRAGMA journal_mode = OFF", "PRAGMA main.journal_mode = MEMORY",
		"PRAGMA temp_store_directory = '/tmp'", "PRAGMA temp_store_directory", "PRAGMA data_store_directory = '/tmp'",
		"PRAGMA locking_mode = EXCLUSIVE", "PRAGMA locking_mode",
		"PRAGMA temp_store = FILE", "PRAGMA mmap_size = 268435456", "PRAGMA cache_size = -100000",
		"PRAGMA synchronous = OFF", "PRAGMA query_only = OFF", "PRAGMA trusted_schema = ON",
		"PRAGMA recursive_triggers = OFF", "PRAGMA ignore_check_constraints = ON", "PRAGMA cell_size_check = OFF",
		"PRAGMA legacy_alter_table = ON", "PRAGMA database_list", "PRAGMA wal_checkpoint(TRUNCATE)",
		"PRAGMA wal_autocheckpoint = 0", "PRAGMA journal_size_limit = -1", "PRAGMA max_page_count = 2147483646",
		"PRAGMA busy_timeout = 0", "PRAGMA soft_heap_limit = 0", "PRAGMA hard_heap_limit = 0",
		"PRAGMA threads = 8", "PRAGMA automatic_index = OFF", "PRAGMA case_sensitive_like = ON",
		"PRAGMA shrink_memory", "PRAGMA optimize", "PRAGMA schema_version = 1", "PRAGMA page_size = 512",
		"PRAGMA auto_vacuum = FULL", "PRAGMA incremental_vacuum", "PRAGMA secure_delete = OFF",
		"PRAGMA reverse_unordered_selects = ON", "PRAGMA count_changes = ON", "PRAGMA no_such_pragma = 1",
		"SELECT * FROM pragma_database_list", "SELECT name FROM pragma_database_list WHERE seq = 0",
	}
	bothAccess(func(a Access, name string) {
		for _, sql := range denied {
			_, err := query(e, "a", a, sql)
			wantCode(t, err, apierr.StatementDenied, name+" "+sql)
		}
	})

	// Still WAL, writable schema off.
	if v := scalar(t, mustQuery(t, e, "a", Read, "PRAGMA journal_mode")); v.Text != "wal" {
		t.Fatalf("journal_mode = %q", v.Text)
	}
	// foreign_keys = OFF is accepted (migrations send it) but has no effect:
	// it runs inside the engine's transaction, and the connection is reset.
	mustQuery(t, e, "a", Write, "PRAGMA foreign_keys = OFF")
	_, err := query(e, "a", Write, "INSERT INTO child VALUES (42)")
	wantCode(t, err, apierr.ConstraintViolation, "foreign key after foreign_keys=OFF")
	_, err = e.Batch(context.Background(), Target{Database: "a"}, Write, []Statement{
		stmt("PRAGMA foreign_keys = OFF"), stmt("INSERT INTO child VALUES (43)"),
	})
	wantCode(t, err, apierr.ConstraintViolation, "foreign key inside a batch with foreign_keys=OFF")
	if v := scalar(t, mustQuery(t, e, "a", Read, "PRAGMA foreign_keys")); v.Int != 1 {
		t.Fatalf("foreign_keys = %d", v.Int)
	}
}

// Row 5: writes with a read-only token (INSERT, DDL, PRAGMA user_version = 1)
// are denied by the read-only connection.
func TestIsolationReadOnlyWrites(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(x); ")
	mustQuery(t, e, "a", Write, "CREATE INDEX t_x ON t(x)") // so REINDEX has work to write
	mustQuery(t, e, "a", Write, "INSERT INTO t VALUES (1)")
	for _, sql := range []string{
		"INSERT INTO t VALUES (2)", "UPDATE t SET x = 5", "DELETE FROM t", "REPLACE INTO t VALUES (3)",
		"CREATE TABLE u(x)", "DROP TABLE t", "ALTER TABLE t ADD COLUMN y", "CREATE INDEX i ON t(x)",
		"CREATE VIEW v AS SELECT 1", "CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT 1; END",
		"PRAGMA user_version = 1", "PRAGMA application_id = 7", "REINDEX", "ANALYZE",
		"INSERT INTO t SELECT x FROM t RETURNING x", "CREATE VIRTUAL TABLE f USING fts5(b)",
	} {
		_, err := query(e, "a", Read, sql)
		wantCode(t, err, apierr.ReadOnly, sql)
	}
	_, err := e.Batch(context.Background(), Target{Database: "a"}, Read, []Statement{stmt("SELECT 1"), stmt("INSERT INTO t VALUES (9)")})
	wantCode(t, err, apierr.ReadOnly, "write in a read batch")
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM t")); v.Int != 1 {
		t.Fatalf("rows = %d", v.Int)
	}
	if v := scalar(t, mustQuery(t, e, "a", Read, "PRAGMA user_version")); v.Int != 0 {
		t.Fatalf("user_version = %d", v.Int)
	}
}

// Row 6: an infinite recursive CTE is cancelled at the statement timeout and
// the connection is still usable after.
func TestIsolationRecursiveCTE(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.StatementTimeout = 300 * time.Millisecond })
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(x)")
	loop := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM r) SELECT count(*) FROM r"
	bothAccess(func(a Access, name string) {
		start := time.Now()
		_, err := query(e, "a", a, loop)
		wantCode(t, err, apierr.StatementTimeout, name+" infinite CTE")
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("%s: cancel took %v", name, d)
		}
		if v := scalar(t, mustQuery(t, e, "a", a, "SELECT 41 + 1")); v.Int != 42 {
			t.Fatalf("%s: connection unusable after cancel", name)
		}
	})
	// A write cut off by the timeout leaves nothing behind.
	_, err := query(e, "a", Write, "INSERT INTO t "+strings.Replace(loop, "count(*)", "n", 1))
	wantCode(t, err, apierr.StatementTimeout, "infinite insert")
	if v := scalar(t, mustQuery(t, e, "a", Write, "SELECT count(*) FROM t")); v.Int != 0 {
		t.Fatalf("cancelled insert kept %d rows", v.Int)
	}
}

// Row 7: a result with more rows or bytes than the cap is an error with a
// stable code, and no partial success is reported as full.
func TestIsolationResultCap(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxResultRows = 10; c.MaxResultBytes = 4096 })
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(x)")

	r := mustQuery(t, e, "a", Read, "SELECT value FROM generate_series(1, 10)")
	if len(r.Rows) != 10 {
		t.Fatalf("10 rows: got %d", len(r.Rows))
	}
	bothAccess(func(a Access, name string) {
		_, err := query(e, "a", a, "SELECT value FROM generate_series(1, 11)")
		wantCode(t, err, apierr.ResultTooLarge, name+" 11 rows")
		_, err = query(e, "a", a, "SELECT randomblob(3000) UNION ALL SELECT randomblob(3000)")
		wantCode(t, err, apierr.ResultTooLarge, name+" bytes")
		// NULLs aren't free: each value costs at least the memory it takes.
		_, err = query(e, "a", a, "SELECT NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL FROM generate_series(1, 10)")
		wantCode(t, err, apierr.ResultTooLarge, name+" wide NULL rows")
	})
	// The byte cap counts across a batch.
	_, err := e.Batch(context.Background(), Target{Database: "a"}, Read, []Statement{
		stmt("SELECT randomblob(3000)"), stmt("SELECT randomblob(3000)"),
	})
	wantCode(t, err, apierr.ResultTooLarge, "bytes across a batch")
	// A write whose RETURNING passes the cap is rolled back, not half-reported.
	_, err = query(e, "a", Write, "INSERT INTO t SELECT value FROM generate_series(1, 50) RETURNING x")
	wantCode(t, err, apierr.ResultTooLarge, "insert returning")
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM t")); v.Int != 0 {
		t.Fatalf("rolled back insert kept %d rows", v.Int)
	}
}

// Row 8: randomblob(2000000000) and oversized strings are denied by the
// length limit.
func TestIsolationOversizedValues(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxValueBytes = 1 << 20; c.MaxSQLBytes = 64 << 10 })
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(x)")
	bothAccess(func(a Access, name string) {
		for _, sql := range []string{
			"SELECT randomblob(2000000000)",
			"SELECT length(randomblob(2000000000))",
			"SELECT zeroblob(2000000000)",
			"SELECT printf('%.*c', 2000000000, 'x')",
			"SELECT replace(hex(zeroblob(600000)), '0', 'ab')",
			"SELECT group_concat(hex(randomblob(1000)), '') FROM generate_series(1, 2000)",
		} {
			_, err := query(e, "a", a, sql)
			wantCode(t, err, apierr.ValueTooLarge, name+" "+sql)
		}
		_, err := query(e, "a", a, "SELECT length(?)", strings.Repeat("x", 2<<20))
		wantCode(t, err, apierr.ValueTooLarge, name+" oversized parameter")
		_, err = query(e, "a", a, "SELECT '"+strings.Repeat("y", 70<<10)+"'")
		wantCode(t, err, apierr.ValueTooLarge, name+" oversized statement")
	})
	_, err := query(e, "a", Write, "INSERT INTO t VALUES (?)", bytes.Repeat([]byte{1}, 2<<20))
	wantCode(t, err, apierr.ValueTooLarge, "oversized blob insert")
	// Values under the limit still work.
	mustQuery(t, e, "a", Write, "INSERT INTO t VALUES (randomblob(500000))")
}

// Row 9: filling the disk is stopped by the per-branch size limit, then by
// the instance quota. Creating files is refused when free disk is low.
func TestIsolationDiskFill(t *testing.T) {
	t.Run("branch limit", func(t *testing.T) {
		e := newEngine(t, func(c *config.Config) { c.MaxBranchBytes = 512 << 10 })
		d := mustCreate(t, e, "a")
		mustQuery(t, e, "a", Write, "CREATE TABLE t(x)")
		var err error
		for i := 0; i < 100 && err == nil; i++ {
			_, err = query(e, "a", Write, "INSERT INTO t VALUES (randomblob(32768))")
		}
		wantCode(t, err, apierr.BranchSizeLimit, "filling one branch")
		pages := scalar(t, mustQuery(t, e, "a", Read, "PRAGMA page_count")).Int
		size := scalar(t, mustQuery(t, e, "a", Read, "PRAGMA page_size")).Int
		if pages*size > 512<<10 {
			t.Fatalf("branch grew to %d bytes past its 512KiB limit", pages*size)
		}
		// On disk, with the WAL: the log is truncated once it passes a
		// quarter of the limit, so it adds at most that plus one write.
		if got := fileBytes(e.Layout().BranchFile(d.ID, d.DefaultBranchID)); got > 512<<10+128<<10+64<<10 {
			t.Fatalf("branch takes %d bytes on disk with its WAL", got)
		}
		// Reads and deletes still work on a full branch.
		mustQuery(t, e, "a", Read, "SELECT count(*) FROM t")
		mustQuery(t, e, "a", Write, "DELETE FROM t")
	})
	t.Run("instance quota", func(t *testing.T) {
		e := newEngine(t, func(c *config.Config) { c.MaxBranchBytes = 64 << 20; c.MaxInstanceBytes = 1 << 20 })
		mustCreate(t, e, "a")
		mustCreate(t, e, "b")
		mustQuery(t, e, "a", Write, "CREATE TABLE t(x)")
		mustQuery(t, e, "b", Write, "CREATE TABLE t(x)")
		var err error
		for i := 0; i < 200 && err == nil; i++ {
			_, err = query(e, "a", Write, "INSERT INTO t VALUES (randomblob(32768))")
		}
		wantCode(t, err, apierr.InstanceQuotaExceeded, "filling the instance")
		_, err = query(e, "b", Write, "INSERT INTO t VALUES (randomblob(32768))")
		wantCode(t, err, apierr.InstanceQuotaExceeded, "writing another database at quota")
		mustQuery(t, e, "b", Read, "SELECT count(*) FROM t")
		if total := e.TotalBytes(); total > 1<<20+256<<10 {
			t.Fatalf("instance grew to %d bytes past its 1MiB quota", total)
		}
		// At the quota, deletes still run, and so do writes that reuse the
		// pages they freed.
		mustQuery(t, e, "a", Write, "DELETE FROM t WHERE rowid IN (SELECT rowid FROM t LIMIT 2)")
		mustQuery(t, e, "a", Write, "INSERT INTO t VALUES (randomblob(32768))")
	})
	t.Run("concurrent writers share the quota", func(t *testing.T) {
		e := newEngine(t, func(c *config.Config) { c.MaxBranchBytes = 64 << 20; c.MaxInstanceBytes = 2 << 20 })
		names := []string{"a", "b", "c", "d"}
		for _, n := range names {
			mustCreate(t, e, n)
			mustQuery(t, e, n, Write, "CREATE TABLE t(x)")
		}
		var wg sync.WaitGroup
		for _, n := range names {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 100; i++ {
					if _, err := query(e, n, Write, "INSERT INTO t SELECT randomblob(16384) FROM generate_series(1, 8)"); err != nil {
						if apierr.As(err).Code != apierr.InstanceQuotaExceeded {
							t.Errorf("%s: %v", n, err)
						}
						return
					}
				}
			}()
		}
		wg.Wait()
		if total := e.TotalBytes(); total > 2<<20+512<<10 {
			t.Fatalf("concurrent writers took the instance to %d bytes past its 2MiB quota", total)
		}
	})
	t.Run("free disk", func(t *testing.T) {
		e := newEngine(t, nil)
		e.freeBytes = func(string) (int64, error) { return 1 << 20, nil }
		_, err := e.CreateDatabase(context.Background(), "a")
		wantCode(t, err, apierr.DiskFull, "create with a full disk")
	})
}

// Row 10: a user-defined view or trigger that calls a denied function is
// denied at run time (trusted schema off).
func TestIsolationSchemaFunctions(t *testing.T) {
	e := newEngine(t, nil)
	d := mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(x)")
	mustQuery(t, e, "a", Write, "CREATE TABLE log(x)")

	// Views and triggers that call load_extension can be stored, but never run.
	mustQuery(t, e, "a", Write, "CREATE VIEW v AS SELECT load_extension('/tmp/x.so') AS y")
	mustQuery(t, e, "a", Write, "CREATE TRIGGER tr AFTER INSERT ON t BEGIN INSERT INTO log SELECT load_extension('/tmp/x.so'); END")
	bothAccess(func(a Access, name string) {
		_, err := query(e, "a", a, "SELECT * FROM v")
		wantNoLoadExtension(t, err, name+" view calling load_extension")
	})
	_, err := query(e, "a", Write, "INSERT INTO t VALUES (1)")
	wantNoLoadExtension(t, err, "trigger calling load_extension")
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM t")); v.Int != 0 {
		t.Fatal("insert that fired a failing trigger was kept")
	}

	// A function that isn't marked innocuous can't run from the schema. Only
	// engine code can register one, so do it on the pooled writer.
	p := e.acquirePool(d.DefaultBranchID, e.Layout().BranchFile(d.ID, d.DefaultBranchID))
	defer e.releasePool(p)
	w, err := p.acquireWriter(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.CreateFunction("side_effect", 0, 0, func(ctx sqlite3.Context, _ ...sqlite3.Value) { ctx.ResultInt(1) }); err != nil {
		t.Fatal(err)
	}
	if err := w.Exec("CREATE VIEW unsafe_v AS SELECT side_effect() AS y"); err != nil {
		t.Fatal(err)
	}
	if err := w.Exec("SELECT side_effect()"); err != nil {
		t.Fatalf("direct call should work: %v", err)
	}
	err = w.Exec("SELECT * FROM unsafe_v")
	if err == nil || !strings.Contains(err.Error(), "unsafe use") {
		t.Fatalf("non-innocuous function in a view: got %v, want unsafe use", err)
	}
	p.releaseWriter(w)
}

// Temp objects would live on a pooled connection and leak into later
// requests: shadowing a table for readers, or capturing writes with a temp
// trigger. Both the TEMP keyword and the temp. schema prefix are denied.
func TestIsolationTempObjects(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.ReadersPerBranch = 1 })
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE users(name)")
	mustQuery(t, e, "a", Write, "CREATE TABLE audit(name)")
	mustQuery(t, e, "a", Write, "INSERT INTO users VALUES ('real')")
	for _, sql := range []string{
		"CREATE TEMP TABLE users(name)",
		"CREATE TABLE temp.users(name)",
		"CREATE TABLE temp.users AS SELECT 'planted' AS name",
		"CREATE VIEW temp.users AS SELECT 'planted' AS name",
		"CREATE TRIGGER temp.spy AFTER INSERT ON main.users BEGIN INSERT INTO main.audit VALUES (new.name); END",
		"CREATE TEMP TRIGGER spy AFTER INSERT ON main.users BEGIN INSERT INTO main.audit VALUES (new.name); END",
	} {
		bothAccess(func(a Access, name string) {
			_, err := query(e, "a", a, sql)
			wantCode(t, err, apierr.StatementDenied, name+" "+sql)
		})
	}
	// With one reader and one writer, a leak would show up here.
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT name FROM users")); v.Text != "real" {
		t.Fatalf("reader sees %q", v.Text)
	}
	mustQuery(t, e, "a", Write, "INSERT INTO users VALUES ('second')")
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM audit")); v.Int != 0 {
		t.Fatalf("a trigger captured %d rows", v.Int)
	}
}
