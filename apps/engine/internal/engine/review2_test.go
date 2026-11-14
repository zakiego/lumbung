package engine

// Regression tests for the milestone 2 review.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// Dumps write tables in name order; a child that sorts before its parent,
// a self-reference and a row near the value limit must all round-trip.
func TestDumpForeignKeysAndLargeRows(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "src")
	for _, sql := range []string{
		"CREATE TABLE z_parent(id INTEGER PRIMARY KEY)",
		"CREATE TABLE a_child(id INTEGER PRIMARY KEY, pid INTEGER REFERENCES z_parent(id))",
		"CREATE TABLE tree(id INTEGER PRIMARY KEY, up INTEGER REFERENCES tree(id))",
		"INSERT INTO z_parent VALUES (1)",
		"INSERT INTO a_child VALUES (1, 1)",
		"INSERT INTO tree VALUES (1, NULL)",
		"INSERT INTO tree VALUES (3, 1)",
		"INSERT INTO tree VALUES (2, 3)", // row 2 points forward at row 3
		"CREATE TABLE big(b BLOB)",
		"INSERT INTO big VALUES (randomblob(800000))",
		"CREATE TRIGGER t1 AFTER INSERT ON z_parent BEGIN INSERT INTO tree VALUES (NULL, NULL); SELECT 1; END",
	} {
		mustQuery(t, e, "src", Write, sql)
	}
	var dump bytes.Buffer
	if err := e.ExportSQL(bg, Target{Workspace: ws(e), Database: "src"}, &dump); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, e, "dst")
	if err := e.Import(bg, Target{Workspace: ws(e), Database: "dst"}, ImportSQL, ImportIntoEmpty, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, q := range []string{"SELECT * FROM a_child", "SELECT * FROM tree ORDER BY id", "SELECT hex(b) FROM big"} {
		if a, b := rowsJSON(t, e, "src", "", q), rowsJSON(t, e, "dst", "", q); a != b {
			t.Errorf("%s differs", q)
		}
	}
	// A dump that leaves a dangling reference is refused.
	mustCreate(t, e, "bad")
	err := e.Import(bg, Target{Workspace: ws(e), Database: "bad"}, ImportSQL, ImportIntoEmpty, strings.NewReader(
		"CREATE TABLE p(id INTEGER PRIMARY KEY); CREATE TABLE c(pid INTEGER REFERENCES p(id)); INSERT INTO c VALUES (9);"))
	wantCode(t, err, apierr.InvalidImport, "dangling foreign key")
}

func TestContentlessFTSRefusesSQLDump(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE VIRTUAL TABLE f USING fts5(body, content='')")
	err := e.ExportSQL(bg, Target{Workspace: ws(e), Database: "a"}, io.Discard)
	wantCode(t, err, apierr.BadRequest, "contentless fts5 dump")
	if err := e.ExportDB(bg, Target{Workspace: ws(e), Database: "a"}, io.Discard); err != nil {
		t.Fatalf("a .db download still works: %v", err)
	}
}

// Import time must grow linearly with the dump, not quadratically.
func TestLargeDumpImport(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	var b strings.Builder
	b.WriteString("CREATE TABLE t(id INTEGER PRIMARY KEY, s TEXT);\n")
	for i := 0; i < 60000; i++ {
		fmt.Fprintf(&b, "INSERT INTO t VALUES (%d, 'row %d with some padding to make it longer');\n", i, i)
	}
	start := time.Now()
	if err := e.Import(bg, Target{Workspace: ws(e), Database: "a"}, ImportSQL, ImportIntoEmpty, strings.NewReader(b.String())); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 60*time.Second {
		t.Fatalf("a %d byte dump took %v", b.Len(), d)
	}
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM t")); v.Int != 60000 {
		t.Fatalf("rows %d", v.Int)
	}
}

// An into-empty import rechecks at the swap: tables created while the
// upload streamed are kept, and the import fails.
func TestImportIntoEmptyRechecksAtSwap(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "a")
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- e.Import(bg, Target{Workspace: ws(e), Database: "a"}, ImportSQL, ImportIntoEmpty, pr)
	}()
	_, _ = pw.Write([]byte("CREATE TABLE fresh(x);\n"))
	mustQuery(t, e, "a", Write, "CREATE TABLE written_meanwhile(x)")
	mustQuery(t, e, "a", Write, "INSERT INTO written_meanwhile VALUES (1)")
	_, _ = pw.Write([]byte("INSERT INTO fresh VALUES (1);\n"))
	pw.Close()
	wantCode(t, <-done, apierr.BranchNotEmpty, "import after a concurrent write")
	if v := scalar(t, mustQuery(t, e, "a", Read, "SELECT count(*) FROM written_meanwhile")); v.Int != 1 {
		t.Fatal("concurrent write lost")
	}
}

// A purge that races a restore leaves the restored database alone.
func TestPurgeSkipsRestored(t *testing.T) {
	e := newEngine(t, nil)
	d := mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(x)")
	if _, err := e.DeleteDatabase(bg, ws(e), "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RestoreDatabase(bg, ws(e), d.ID, ""); err != nil {
		t.Fatal(err)
	}
	e.purgeOne(bg, d.ID)
	mustQuery(t, e, "a", Read, "SELECT count(*) FROM t")
}

// Maintenance waiting on a branch rechecks it once it gets the gate: a
// branch promoted to default while a delete waited is not deleted.
func TestDeleteRechecksAfterWaiting(t *testing.T) {
	e := newEngine(t, nil)
	d := mustCreate(t, e, "a")
	b, err := e.CreateBranch(bg, ws(e), "a", "next", BranchSource{}, NewBranchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g := e.gates.get(b.ID)
	if err := g.share(bg); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.DeleteBranch(bg, ws(e), "a", "next") }()
	time.Sleep(50 * time.Millisecond)
	// Promote behind the gate's back, as a race would.
	if err := e.meta.SetDefaultBranch(bg, d.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	g.unshare()
	wantCode(t, <-done, apierr.BranchProtected, "delete of a branch promoted meanwhile")
	if _, err := e.GetBranch(bg, ws(e), "a", "next"); err != nil {
		t.Fatal("promoted branch was deleted")
	}
}

// Whole-file copies count against the instance quota.
func TestCopiesRespectInstanceQuota(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxInstanceBytes = 600 << 10; c.MaxBranches = 100 })
	mustCreate(t, e, "a")
	mustQuery(t, e, "a", Write, "CREATE TABLE t(b)")
	mustQuery(t, e, "a", Write, "INSERT INTO t VALUES (randomblob(150000))")
	var err error
	for i := 0; i < 10 && err == nil; i++ {
		_, err = e.CreateBranch(bg, ws(e), "a", fmt.Sprintf("b%d", i), BranchSource{}, NewBranchOptions{})
	}
	wantCode(t, err, apierr.InstanceQuotaExceeded, "branching past the quota")
	if total := e.TotalBytes(); total > 600<<10 {
		t.Fatalf("instance at %d bytes", total)
	}
}

// Files a crash left without a meta record are removed at start.
func TestOrphansRemovedAtStart(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	e, err := Open(bg, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := mustCreate(t, e, "a")
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	orphanBranch := e.Layout().BranchFile(d.ID, ids.New(ids.Branch))
	orphanSnap := e.Layout().SnapshotFile(d.ID, ids.New(ids.Snapshot))
	orphanDB := e.Layout().DatabaseDir(ids.New(ids.Database))
	notOurs := filepath.Join(e.Layout().DatabaseDir(d.ID), "readme.txt")
	for _, p := range []string{orphanBranch, orphanSnap, notOurs} {
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(orphanDB, 0o700)
	e2, err := Open(bg, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	for _, p := range []string{orphanBranch, orphanSnap, orphanDB} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s kept", p)
		}
	}
	if _, err := os.Stat(notOurs); err != nil {
		t.Error("a file not named like an id was removed")
	}
	mustQuery(t, e2, "a", Read, "SELECT 1")
}

// Purging a database also removes its tokens and stats, which reference it.
func TestPurgeRemovesTokensAndStats(t *testing.T) {
	e := newEngine(t, nil)
	d := mustCreate(t, e, "a")
	if _, err := e.meta.CreateToken(bg, meta.Token{ID: "tok_aaaaaaaaaaaaaaaaaaaa", Name: "t", SecretHash: []byte{1},
		Scope: "database", WorkspaceID: d.WorkspaceID, DatabaseID: d.ID, Access: "read"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.meta.AddStats(bg, []meta.StatsBucket{{BranchID: d.DefaultBranchID, Minute: time.Now(), Queries: 1, Durations: []int64{1}}}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DeleteDatabase(bg, ws(e), "a"); err != nil {
		t.Fatal(err)
	}
	e.purgeTrash(bg, time.Now().Add(e.cfg.TrashRetention+time.Hour))
	if _, err := e.meta.GetToken(bg, "tok_aaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("token of a purged database kept")
	}
	if rows, _ := e.meta.ListStats(bg, d.DefaultBranchID, time.Now().Add(-time.Hour)); len(rows) != 0 {
		t.Fatal("stats of a purged database kept")
	}
	_, err := e.RestoreDatabase(bg, ws(e), d.ID, "")
	wantCode(t, err, apierr.DatabaseNotFound, "restore after purge")
}
