package engine

// PLAN 6.2: feed statements through the query path and fail if any file
// outside the attacking branch's own files is created or changed.
//
// CI runs it for a bounded time:
//
//	go test -run '^$' -fuzz '^FuzzQueryPath$' -fuzztime 60s ./internal/engine
//
// Without -fuzz, go test runs the seed corpus below as a normal test.

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/storage"
)

type fileState struct {
	size  int64
	mtime time.Time
	sum   [32]byte
}

// snapshotTree records every file under root. Content is hashed except for
// -shm files: the shared-memory index changes when the engine itself reads
// meta.db, and holds no data. Its creation still counts.
func snapshotTree(t testing.TB, root string) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		st := fileState{size: info.Size(), mtime: info.ModTime()}
		if !strings.HasSuffix(p, "-shm") {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			st.sum = sha256.Sum256(b)
		} else {
			st.size, st.mtime = 0, time.Time{}
		}
		out[p] = st
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var fuzzSeeds = []string{
	"SELECT 1",
	"CREATE TABLE t(x)",
	"INSERT INTO t VALUES (randomblob(100))",
	"ATTACH '{VICTIM}' AS v",
	"ATTACH 'file:{VICTIM}?vfs=os' AS v",
	"ATTACH '{META}' AS m",
	"ATTACH '{ROOT}/x.db' AS x",
	"VACUUM INTO '{ROOT}/copy.db'",
	"VACUUM INTO 'relative.db'",
	"VACUUM",
	"SELECT load_extension('{ROOT}/x.so')",
	"PRAGMA writable_schema = ON",
	"PRAGMA journal_mode = DELETE",
	"PRAGMA temp_store_directory = '{ROOT}'",
	"PRAGMA locking_mode = EXCLUSIVE",
	"PRAGMA temp_store = FILE",
	"CREATE TEMP TABLE x(a)",
	"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r) SELECT count(*) FROM r",
	"SELECT randomblob(2000000000)",
	"CREATE VIEW v1 AS SELECT load_extension('x')",
	"CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT 1; END",
	"CREATE VIRTUAL TABLE f USING fts5(body)",
	"INSERT INTO f VALUES ('hello world')",
	"SELECT * FROM pragma_database_list",
	"BEGIN; ATTACH '{VICTIM}' AS v; COMMIT",
	"CREATE TABLE big AS SELECT randomblob(10000) FROM generate_series(1, 2000)",
	"SELECT * FROM generate_series(1, 100000) ORDER BY random()",
	"CREATE INDEX i ON t(x)",
	"REINDEX",
	"ANALYZE",
	"SELECT json_extract('{\"a\":1}', '$.a')",
}

func FuzzQueryPath(f *testing.F) {
	e := newEngine(f, func(c *config.Config) {
		c.StatementTimeout = 250 * time.Millisecond
		c.MaxBranchBytes = 4 << 20
		c.MaxResultRows = 1000
		c.MaxResultBytes = 1 << 20
	})
	victim := mustCreate(f, e, "victim")
	mustQuery(f, e, "victim", Write, "CREATE TABLE secret(x)")
	mustQuery(f, e, "victim", Write, "INSERT INTO secret VALUES ('do not touch')")
	attacker := mustCreate(f, e, "attacker")
	// Close the victim's connections so its files only change if an attack
	// reaches them.
	e.dropPool(victim.DefaultBranchID)

	victimFile := e.Layout().BranchFile(victim.ID, victim.DefaultBranchID)
	own := map[string]bool{}
	for _, p := range storage.BranchFiles(e.Layout().BranchFile(attacker.ID, attacker.DefaultBranchID)) {
		own[p] = true
	}
	root := e.Layout().Root
	cwd, err := os.Getwd()
	if err != nil {
		f.Fatal(err)
	}
	cwdBefore := snapshotTree(f, cwd)

	expand := func(sql string) string {
		r := strings.NewReplacer("{VICTIM}", victimFile, "{META}", e.Layout().Meta(), "{ROOT}", root)
		return r.Replace(sql)
	}

	for _, s := range fuzzSeeds {
		f.Add(s, true)
		f.Add(s, false)
	}

	f.Fuzz(func(t *testing.T, sql string, write bool) {
		before := snapshotTree(t, root)
		access := Read
		if write {
			access = Write
		}
		_, err := e.Query(context.Background(), Target{Workspace: ws(e), Database: "attacker"}, access, Statement{SQL: expand(sql)})
		if err != nil {
			if ae := apierr.As(err); ae.Code == apierr.Internal {
				// Internal errors are fine for garbage input as long as the
				// files hold; note them so a real bug is visible in -v.
				t.Logf("internal error for %q: %v", sql, ae)
			}
		}
		after := snapshotTree(t, root)
		for p, st := range after {
			if own[p] {
				continue
			}
			old, ok := before[p]
			if !ok {
				t.Fatalf("%q created %s", sql, p)
			}
			if old != st {
				t.Fatalf("%q changed %s", sql, p)
			}
		}
		for p := range before {
			if _, ok := after[p]; !ok && !own[p] {
				t.Fatalf("%q removed %s", sql, p)
			}
		}
		if now := snapshotTree(t, cwd); len(now) != len(cwdBefore) {
			t.Fatalf("%q created a file in the working directory", sql)
		}
	})
}
