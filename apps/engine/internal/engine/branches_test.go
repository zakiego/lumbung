package engine

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

var bg = context.Background()

func onBranch(e *Engine, db, branch string, access Access, sql string, args ...any) (*Result, error) {
	return e.Query(bg, Target{Database: db, Branch: branch}, access, stmt(sql, args...))
}

func mustOnBranch(t *testing.T, e *Engine, db, branch string, access Access, sql string, args ...any) *Result {
	t.Helper()
	r, err := onBranch(e, db, branch, access, sql, args...)
	if err != nil {
		t.Fatalf("%s on %s: %v", sql, branch, err)
	}
	return r
}

func TestBranchLifecycle(t *testing.T) {
	e := newEngine(t, nil)
	d := mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	mustQuery(t, e, "app", Write, "INSERT INTO t VALUES (1)")

	dev, err := e.CreateBranch(bg, "app", "dev", BranchSource{}, NewBranchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if dev.ParentID != d.DefaultBranchID || dev.Protected {
		t.Fatalf("dev: %+v", dev)
	}
	// A full copy, independent afterwards.
	mustOnBranch(t, e, "app", "dev", Write, "INSERT INTO t VALUES (2)")
	if v := scalar(t, mustOnBranch(t, e, "app", "dev", Read, "SELECT count(*) FROM t")); v.Int != 2 {
		t.Fatalf("dev rows %d", v.Int)
	}
	if v := scalar(t, mustOnBranch(t, e, "app", "main", Read, "SELECT count(*) FROM t")); v.Int != 1 {
		t.Fatalf("main rows %d", v.Int)
	}

	_, err = e.CreateBranch(bg, "app", "dev", BranchSource{}, NewBranchOptions{})
	wantCode(t, err, apierr.NameTaken, "duplicate branch")
	_, err = e.CreateBranch(bg, "app", "Bad_Name", BranchSource{}, NewBranchOptions{})
	wantCode(t, err, apierr.InvalidName, "bad name")
	_, err = e.CreateBranch(bg, "app", "x", BranchSource{Branch: "nope"}, NewBranchOptions{})
	wantCode(t, err, apierr.BranchNotFound, "missing source")

	// Branch of a branch.
	feat, err := e.CreateBranch(bg, "app", "feat", BranchSource{Branch: "dev"}, NewBranchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if feat.ParentID != dev.ID {
		t.Fatalf("feat parent %s", feat.ParentID)
	}

	// Rename keeps the id and data.
	name := "develop"
	renamed, err := e.UpdateBranch(bg, "app", "dev", BranchChanges{Name: &name})
	if err != nil || renamed.ID != dev.ID || renamed.Name != "develop" {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	mustOnBranch(t, e, "app", "develop", Read, "SELECT count(*) FROM t")
	_, err = onBranch(e, "app", "dev", Read, "SELECT 1")
	wantCode(t, err, apierr.BranchNotFound, "old name")

	// Reset copies the parent again.
	if _, err := e.ResetBranch(bg, "app", "develop"); err != nil {
		t.Fatal(err)
	}
	if v := scalar(t, mustOnBranch(t, e, "app", "develop", Read, "SELECT count(*) FROM t")); v.Int != 1 {
		t.Fatalf("after reset %d rows", v.Int)
	}

	// Delete removes the file; main can't be deleted or reset.
	path := e.Layout().BranchFile(d.ID, feat.ID)
	if err := e.DeleteBranch(bg, "app", "feat"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("branch file left behind")
	}
	wantCode(t, e.DeleteBranch(bg, "app", "main"), apierr.BranchProtected, "delete main")
	_, err = e.ResetBranch(bg, "app", "main")
	wantCode(t, err, apierr.BranchProtected, "reset main")
	f := false
	_, err = e.UpdateBranch(bg, "app", "main", BranchChanges{Protected: &f})
	wantCode(t, err, apierr.BranchProtected, "unprotect main")

	// A protected branch can't be deleted or reset until unprotected.
	on := true
	if _, err := e.UpdateBranch(bg, "app", "develop", BranchChanges{Protected: &on}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.DeleteBranch(bg, "app", "develop"), apierr.BranchProtected, "delete protected")
	if _, err := e.UpdateBranch(bg, "app", "develop", BranchChanges{Protected: &f}); err != nil {
		t.Fatal(err)
	}

	// Reset of a branch whose parent is gone.
	child, err := e.CreateBranch(bg, "app", "child", BranchSource{Branch: "develop"}, NewBranchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteBranch(bg, "app", "develop"); err != nil {
		t.Fatal(err)
	}
	_, err = e.ResetBranch(bg, "app", child.ID)
	wantCode(t, err, apierr.ParentGone, "reset with parent gone")
}

func TestPromote(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	if _, err := e.CreateBranch(bg, "app", "next", BranchSource{}, NewBranchOptions{ExpiresIn: time.Hour}); err != nil {
		t.Fatal(err)
	}
	mustOnBranch(t, e, "app", "next", Write, "ALTER TABLE t ADD COLUMN y")
	b, err := e.PromoteBranch(bg, "app", "next")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Protected || b.ExpiresAt != nil {
		t.Fatalf("promoted branch: %+v", b)
	}
	// The default now answers unnamed queries.
	r := mustQuery(t, e, "app", Read, "SELECT * FROM t")
	if len(r.Columns) != 2 {
		t.Fatalf("default has %d columns", len(r.Columns))
	}
	old, err := e.GetBranch(bg, "app", "main")
	if err != nil || !old.Protected {
		t.Fatalf("old default: %+v %v", old, err)
	}
}

func TestBranchCap(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxBranches = 2 })
	mustCreate(t, e, "app")
	if _, err := e.CreateBranch(bg, "app", "one", BranchSource{}, NewBranchOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := e.CreateBranch(bg, "app", "two", BranchSource{}, NewBranchOptions{})
	wantCode(t, err, apierr.LimitReached, "branch cap")
	e.freeBytes = func(string) (int64, error) { return 1, nil }
	if err := e.DeleteBranch(bg, "app", "one"); err != nil {
		t.Fatal(err)
	}
	_, err = e.CreateBranch(bg, "app", "two", BranchSource{}, NewBranchOptions{})
	wantCode(t, err, apierr.DiskFull, "branch with a full disk")
}

func TestExpiredBranches(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	if _, err := e.CreateBranch(bg, "app", "pr-1", BranchSource{}, NewBranchOptions{ExpiresIn: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CreateBranch(bg, "app", "pr-2", BranchSource{}, NewBranchOptions{ExpiresIn: time.Millisecond, Protected: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	e.tick(time.Now())
	if _, err := e.GetBranch(bg, "app", "pr-1"); err == nil {
		t.Fatal("expired branch kept")
	}
	if _, err := e.GetBranch(bg, "app", "pr-2"); err != nil {
		t.Fatal("protected expired branch deleted")
	}
}

func TestSchemaAndDiff(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	for _, sql := range []string{
		"CREATE TABLE users(id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE, name TEXT DEFAULT 'x')",
		"CREATE INDEX users_name ON users(name)",
		"CREATE VIEW names AS SELECT name FROM users",
		"CREATE TABLE gone(a)",
		"CREATE VIRTUAL TABLE docs USING fts5(body)",
	} {
		mustQuery(t, e, "app", Write, sql)
	}
	sc, err := e.Schema(bg, Target{Database: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Tables) != 3 || len(sc.Views) != 1 {
		t.Fatalf("schema: %d tables (shadow tables must be hidden), %d views", len(sc.Tables), len(sc.Views))
	}
	var users Table
	for _, tb := range sc.Tables {
		if tb.Name == "users" {
			users = tb
		}
	}
	if len(users.Columns) != 3 || !users.Columns[1].NotNull || users.Columns[0].PK != 1 || *users.Columns[2].Default != "'x'" {
		t.Fatalf("users columns: %+v", users.Columns)
	}

	if _, err := e.CreateBranch(bg, "app", "mig", BranchSource{}, NewBranchOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"ALTER TABLE users ADD COLUMN age INTEGER",
		"DROP TABLE gone",
		"CREATE TABLE posts(id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id))",
		"DROP INDEX users_name",
		"CREATE TRIGGER t AFTER INSERT ON posts BEGIN SELECT 1; END",
	} {
		mustOnBranch(t, e, "app", "mig", Write, sql)
	}
	diff, err := e.DiffBranches(bg, "app", "mig", "main")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range diff.Changes {
		got[c.Kind+":"+c.Name] = c.Change
	}
	want := map[string]string{"table:users": "changed", "table:gone": "removed", "table:posts": "added", "index:users_name": "removed", "trigger:t": "added"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected changes: %v", got)
	}
	for _, c := range diff.Changes {
		if c.Name == "users" && (c.Columns == nil || len(c.Columns.Added) != 1 || c.Columns.Added[0].Name != "age") {
			t.Errorf("users column changes: %+v", c.Columns)
		}
	}
	if len(diff.BranchDDL) == 0 || len(diff.BaseDDL) == 0 {
		t.Error("diff is missing DDL")
	}
	same, err := e.DiffBranches(bg, "app", "main", "main")
	if err != nil || len(same.Changes) != 0 {
		t.Fatalf("self diff: %+v %v", same, err)
	}
}
