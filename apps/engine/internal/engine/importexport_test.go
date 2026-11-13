package engine

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ncruces/go-sqlite3"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/value"
)

// seed fills a branch with values that are easy to get wrong in a dump.
func seed(t *testing.T, e *Engine, db string) {
	t.Helper()
	for _, sql := range []string{
		"CREATE TABLE t(id INTEGER PRIMARY KEY AUTOINCREMENT, i INTEGER, f, s TEXT, b BLOB, any_col)",
		"CREATE INDEX t_s ON t(s)",
		"CREATE VIEW v AS SELECT id, s FROM t",
		"CREATE TRIGGER tr AFTER DELETE ON t BEGIN SELECT 1; END",
		"CREATE VIRTUAL TABLE docs USING fts5(body)",
		"INSERT INTO docs VALUES ('lumbung keeps rice')",
		"CREATE TABLE g(a INTEGER, b INTEGER GENERATED ALWAYS AS (a * 2) STORED)",
		"INSERT INTO g(a) VALUES (21)",
	} {
		mustQuery(t, e, db, Write, sql)
	}
	_, err := e.Query(bg, Target{Database: db}, Write, Statement{
		SQL: "INSERT INTO t (i, f, s, b, any_col) VALUES (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)",
		Args: []value.Value{
			value.IntValue(math.MaxInt64), value.FloatValue(1), value.TextValue("it's \"quoted\"\nline"), value.BlobValue([]byte{0, 1, 255}), value.FloatValue(2),
			value.IntValue(math.MinInt64), value.FloatValue(math.Inf(1)), value.TextValue("nul\x00inside"), value.BlobValue([]byte{}), value.NullValue(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func rowsJSON(t *testing.T, e *Engine, db, branch, sql string) string {
	t.Helper()
	r := mustOnBranch(t, e, db, branch, Read, sql)
	var b strings.Builder
	for _, row := range r.Rows {
		for _, v := range row {
			j, _ := v.MarshalJSON()
			b.Write(j)
			b.WriteByte(' ')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestSQLDumpRoundTrip(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "src")
	seed(t, e, "src")
	var dump bytes.Buffer
	if err := e.ExportSQL(bg, Target{Database: "src"}, &dump); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, e, "dst")
	if err := e.Import(bg, Target{Database: "dst"}, ImportSQL, ImportIntoEmpty, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatalf("import dump: %v\n%s", err, dump.String())
	}
	for _, q := range []string{
		"SELECT id, i, typeof(f), f, s, b, typeof(any_col), any_col FROM t ORDER BY id",
		"SELECT * FROM v ORDER BY id",
		"SELECT body FROM docs WHERE docs MATCH 'rice'",
		"SELECT a, b FROM g",
		"SELECT name, seq FROM sqlite_sequence",
	} {
		if a, b := rowsJSON(t, e, "src", "", q), rowsJSON(t, e, "dst", "", q); a != b {
			t.Errorf("%s:\nsrc %s\ndst %s", q, a, b)
		}
	}
	a, _ := e.Schema(bg, Target{Database: "src"})
	b, _ := e.Schema(bg, Target{Database: "dst"})
	if d := diffSchemas(a, b); len(d) != 0 {
		t.Errorf("schema changed in the round trip: %+v", d)
	}
}

func TestDBExportImport(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "src")
	seed(t, e, "src")
	var file bytes.Buffer
	if err := e.ExportDB(bg, Target{Database: "src"}, &file); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(file.Bytes(), sqliteHeader) {
		t.Fatal("export isn't a SQLite file")
	}
	mustCreate(t, e, "dst")
	if err := e.Import(bg, Target{Database: "dst"}, ImportDB, ImportIntoEmpty, bytes.NewReader(file.Bytes())); err != nil {
		t.Fatal(err)
	}
	q := "SELECT id, i, f, s, b, any_col FROM t ORDER BY id"
	if a, b := rowsJSON(t, e, "src", "", q), rowsJSON(t, e, "dst", "", q); a != b {
		t.Fatalf("rows differ:\n%s\n%s", a, b)
	}
	// The tmp folder is clean afterwards.
	if entries, _ := os.ReadDir(e.Layout().TmpDir()); len(entries) != 0 {
		t.Fatalf("export left %d files in tmp", len(entries))
	}
}

func TestImportRules(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxImportBytes = 64 << 10 })
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE keep(x)")
	mustQuery(t, e, "app", Write, "INSERT INTO keep VALUES (1)")
	dump := "CREATE TABLE fresh(x);\nINSERT INTO fresh VALUES (7);\n"

	err := e.Import(bg, Target{Database: "app"}, ImportSQL, ImportIntoEmpty, strings.NewReader(dump))
	wantCode(t, err, apierr.BranchNotEmpty, "import into a branch with tables")
	err = e.Import(bg, Target{Database: "app"}, ImportSQL, ImportReplace, strings.NewReader(dump))
	wantCode(t, err, apierr.BranchProtected, "replace the default branch")

	if _, err := e.CreateBranch(bg, "app", "work", BranchSource{}, NewBranchOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.Import(bg, Target{Database: "app", Branch: "work"}, ImportSQL, ImportReplace, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	if v := scalar(t, mustOnBranch(t, e, "app", "work", Read, "SELECT x FROM fresh")); v.Int != 7 {
		t.Fatal("replace import")
	}
	if _, err := onBranch(e, "app", "work", Read, "SELECT * FROM keep"); err == nil {
		t.Fatal("replace kept the old tables")
	}

	mustCreate(t, e, "empty")
	tgt := Target{Database: "empty"}
	for name, body := range map[string]string{
		"not sqlite":       "hello world, not a database at all" + strings.Repeat(".", 600),
		"short header":     "SQLite format 3",
		"truncated header": string(sqliteHeader) + "garbage",
	} {
		err := e.Import(bg, tgt, ImportDB, ImportIntoEmpty, strings.NewReader(body))
		wantCode(t, err, apierr.InvalidImport, name)
	}
	err = e.Import(bg, tgt, ImportDB, ImportIntoEmpty, bytes.NewReader(make([]byte, 100<<10)))
	wantCode(t, err, apierr.ImportTooLarge, "oversized upload")
	// Dumps run under the same rules as user SQL.
	for name, c := range map[string]struct{ body, code string }{
		"attach":       {"ATTACH '/etc/passwd' AS p;", apierr.StatementDenied},
		"vacuum into":  {"CREATE TABLE a(x); VACUUM INTO '/tmp/x.db';", apierr.StatementDenied},
		"temp trigger": {"CREATE TABLE a(x); CREATE TEMP TRIGGER t AFTER INSERT ON a BEGIN SELECT 1; END;", apierr.StatementDenied},
		"temp schema":  {"CREATE TABLE temp.a(x);", apierr.StatementDenied},
		"savepoint":    {"SAVEPOINT s;", apierr.StatementDenied},
		"bad sql":      {"CREATE TABLE (;", apierr.InvalidImport},
	} {
		err := e.Import(bg, tgt, ImportSQL, ImportIntoEmpty, strings.NewReader(c.body))
		wantCode(t, err, c.code, name)
	}
	// BEGIN and COMMIT in a dump are harmless no-ops.
	if err := e.Import(bg, tgt, ImportSQL, ImportIntoEmpty, strings.NewReader("BEGIN TRANSACTION;\nCREATE TABLE z(a);\nCOMMIT;\n")); err != nil {
		t.Fatal(err)
	}
	// Nothing staged is left next to the branch files.
	d, _ := e.GetDatabase(bg, "empty")
	if m, _ := filepath.Glob(filepath.Join(e.Layout().DatabaseDir(d.ID), "*.tmp-*")); len(m) != 0 {
		t.Fatalf("staged files left: %v", m)
	}
}

// An uploaded file is opened under the same isolation rules: a view inside
// it that calls a non-innocuous function can't run.
func TestImportedFileIsConfined(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	if _, err := e.CreateBranch(bg, "app", "up", BranchSource{}, NewBranchOptions{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "evil.db")
	raw, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Exec("CREATE TABLE t(x); INSERT INTO t VALUES (1); CREATE VIEW v AS SELECT load_extension('/tmp/x.so') AS y"); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	body, _ := os.ReadFile(path)
	if err := e.Import(bg, Target{Database: "app", Branch: "up"}, ImportDB, ImportReplace, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if v := scalar(t, mustOnBranch(t, e, "app", "up", Read, "SELECT x FROM t")); v.Int != 1 {
		t.Fatal("imported data missing")
	}
	_, err = onBranch(e, "app", "up", Read, "SELECT * FROM v")
	wantNoLoadExtension(t, err, "view from an imported file")
}
