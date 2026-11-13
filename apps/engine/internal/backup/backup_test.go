package backup

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
)

var bg = context.Background()

func open(t *testing.T, dir string) *engine.Engine {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = dir
	cfg.MinFreeDiskBytes = 1
	e, err := engine.Open(bg, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func q(t *testing.T, e *engine.Engine, db, branch string, access engine.Access, sql string) *engine.Result {
	t.Helper()
	r, err := e.Query(bg, engine.Target{Database: db, Branch: branch}, access, engine.Statement{SQL: sql})
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return r
}

func TestBackupAndRestore(t *testing.T) {
	src := t.TempDir()
	e := open(t, src)
	for _, name := range []string{"app", "other", "gone"} {
		if _, err := e.CreateDatabase(bg, name); err != nil {
			t.Fatal(err)
		}
		q(t, e, name, "", engine.Write, "CREATE TABLE t(x)")
		q(t, e, name, "", engine.Write, "INSERT INTO t VALUES (1)")
	}
	if _, err := e.CreateBranch(bg, "app", "dev", engine.BranchSource{}, engine.NewBranchOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CreateSnapshot(bg, engine.Target{Database: "app"}, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DeleteDatabase(bg, "gone"); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(src, "secrets"), 0o700)
	_ = os.WriteFile(filepath.Join(src, "secrets", "service-token"), []byte("lmb_x\n"), 0o600)

	// Back up while writers run: the copy holds whole transactions only.
	ctx, cancel := context.WithCancel(bg)
	var wg sync.WaitGroup
	var writes atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			if _, err := e.Batch(bg, engine.Target{Database: "app"}, engine.Write, []engine.Statement{
				{SQL: "INSERT INTO t VALUES (2)"}, {SQL: "INSERT INTO t VALUES (2)"},
			}); err != nil {
				t.Errorf("writer: %v", err)
				return
			}
			writes.Add(1)
		}
	}()
	time.Sleep(20 * time.Millisecond)
	out := t.TempDir()
	dir, err := Run(bg, Options{DataDir: src, To: out})
	cancel()
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	m, err := Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 4 { // meta.db, app/main, other/main, the service token
		t.Fatalf("manifest: %+v", m.Files)
	}

	dst := filepath.Join(t.TempDir(), "restored")
	if _, err := Restore(bg, dir, dst); err != nil {
		t.Fatal(err)
	}
	r := open(t, dst)
	if err := r.Ready(bg); err != nil {
		t.Fatal(err)
	}
	// Only whole transactions: 1 plus an even number of 2s.
	if n := q(t, r, "app", "", engine.Read, "SELECT count(*) FROM t").Rows[0][0].Int; n%2 != 1 {
		t.Fatalf("restored copy has %d rows: a transaction was split", n)
	}
	q(t, r, "other", "", engine.Read, "SELECT * FROM t")
	if _, err := r.GetBranch(bg, "app", "dev"); err == nil {
		t.Fatal("a branch whose file wasn't backed up was restored")
	}
	if snaps, _ := r.ListSnapshots(bg, engine.Target{Database: "app"}); len(snaps) != 0 {
		t.Fatal("snapshot records restored without their files")
	}
	if trash, _ := r.ListTrash(bg); len(trash) != 0 {
		t.Fatal("trashed database restored without its files")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "secrets", "service-token")); string(b) != "lmb_x\n" {
		t.Fatal("service token not restored")
	}
	if _, err := Restore(bg, dir, dst); err == nil {
		t.Fatal("restore into a non-empty folder")
	}

	// All branches, retention, meta-only.
	clock := time.Now().UTC().Add(24 * time.Hour) // after the backup above
	for i := 0; i < 3; i++ {
		clock = clock.Add(time.Hour)
		now := clock
		if _, err := Run(bg, Options{DataDir: src, To: out, AllBranches: true, Keep: 2, Now: func() time.Time { return now }}); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := List(out)
	if len(all) != 2 {
		t.Fatalf("retention kept %d backups", len(all))
	}
	latest, _ := Latest(out)
	lm, err := Verify(latest)
	if err != nil {
		t.Fatal(err)
	}
	if len(lm.Files) != 5 || !lm.AllBranches {
		t.Fatalf("all-branches backup: %+v", lm.Files)
	}
	metaDir := filepath.Join(t.TempDir(), "meta")
	mo, err := Run(bg, Options{DataDir: src, To: metaDir, MetaOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if mm, _ := Verify(mo); len(mm.Files) != 2 {
		t.Fatalf("meta-only backup: %+v", mm.Files)
	}

	// Tampering is caught.
	if err := os.WriteFile(filepath.Join(latest, "meta.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(latest); err == nil {
		t.Fatal("a changed file passed verification")
	}
}

func TestRelativePaths(t *testing.T) {
	src := t.TempDir()
	e := open(t, src)
	if _, err := e.CreateDatabase(bg, "app"); err != nil {
		t.Fatal(err)
	}
	q(t, e, "app", "", engine.Write, "CREATE TABLE t(x)")
	t.Chdir(t.TempDir())
	dir, err := Run(bg, Options{DataDir: src, To: "backups"})
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("backup folder %q isn't absolute", dir)
	}
	rel, _ := filepath.Rel(mustGetwd(t), dir)
	if _, err := Restore(bg, rel, "restored"); err != nil {
		t.Fatal(err)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}
