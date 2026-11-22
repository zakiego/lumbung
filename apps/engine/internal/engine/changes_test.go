package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
)

func waitOn(t testing.TB, e *Engine, db, branch, cursor string, timeout time.Duration) ChangeWait {
	t.Helper()
	w, err := e.WaitChange(context.Background(), Target{Workspace: ws(e), Database: db, Branch: branch}, cursor, timeout)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	return w
}

// cursorOf is the branch's cursor now.
func cursorOf(t testing.TB, e *Engine, db, branch string) string {
	t.Helper()
	w := waitOn(t, e, db, branch, "", 0)
	if w.Changed {
		t.Fatal("an empty cursor reported a change")
	}
	return w.Cursor
}

func TestWaitChangeCursor(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	c := cursorOf(t, e, "app", "")
	if w := waitOn(t, e, "app", "", c, 0); w.Changed || w.Cursor != c {
		t.Fatalf("no change, zero timeout: %+v, want %q unchanged", w, c)
	}
	mustQuery(t, e, "app", Write, "INSERT INTO t VALUES (1)")
	w := waitOn(t, e, "app", "", c, time.Minute)
	if !w.Changed || w.Cursor == c {
		t.Fatalf("after an insert: %+v, want a new cursor", w)
	}
	// A cursor from another process, or garbage, reads as changed at once.
	for _, foreign := range []string{"x", strings.Replace(w.Cursor, e.boot, "0123456789", 1)} {
		if got := waitOn(t, e, "app", "", foreign, time.Minute); !got.Changed || got.Cursor != w.Cursor {
			t.Fatalf("foreign cursor %q: %+v", foreign, got)
		}
	}
}

func TestWaitChangeIgnoresWritesThatChangeNothing(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	tg := Target{Workspace: ws(e), Database: "app"}
	c := cursorOf(t, e, "app", "")

	mustQuery(t, e, "app", Write, "SELECT * FROM t")
	mustQuery(t, e, "app", Write, "UPDATE t SET x = 2 WHERE x = 1")
	if _, err := e.Batch(context.Background(), tg, Write, []Statement{stmt("SELECT 1"), stmt("SELECT count(*) FROM t")}); err != nil {
		t.Fatal(err)
	}
	// A batch that fails rolls back its first insert.
	if _, err := e.Batch(context.Background(), tg, Write, []Statement{stmt("INSERT INTO t VALUES (1)"), stmt("INSERT INTO missing VALUES (1)")}); err == nil {
		t.Fatal("the batch should fail")
	}
	if got := cursorOf(t, e, "app", ""); got != c {
		t.Fatalf("cursor moved from %q to %q with nothing changed", c, got)
	}
}

func TestWaitChangeSchema(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	c := cursorOf(t, e, "app", "")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	c2 := cursorOf(t, e, "app", "")
	if c2 == c {
		t.Fatal("CREATE TABLE didn't move the cursor")
	}
	mustQuery(t, e, "app", Write, "CREATE INDEX t_x ON t(x)")
	if cursorOf(t, e, "app", "") == c2 {
		t.Fatal("CREATE INDEX didn't move the cursor")
	}
}

func TestWaitChangeInteractiveTx(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	c := cursorOf(t, e, "app", "")

	tx := beginTx(t, e, "app", Write)
	txExec(t, e, tx, "INSERT INTO t VALUES (1)")
	txExec(t, e, tx, "INSERT INTO t VALUES (2)")
	if cursorOf(t, e, "app", "") != c {
		t.Fatal("the cursor moved before COMMIT")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	c2 := cursorOf(t, e, "app", "")
	if c2 == c {
		t.Fatal("COMMIT didn't move the cursor")
	}

	tx = beginTx(t, e, "app", Write)
	txExec(t, e, tx, "INSERT INTO t VALUES (3)")
	tx.Rollback()
	if cursorOf(t, e, "app", "") != c2 {
		t.Fatal("a rolled back transaction moved the cursor")
	}
}

func TestWaitChangeBlocks(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	c := cursorOf(t, e, "app", "")

	start := time.Now()
	if w := waitOn(t, e, "app", "", c, 150*time.Millisecond); w.Changed || w.Cursor != c {
		t.Fatalf("timeout: %+v", w)
	}
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("returned after %v, before the timeout", d)
	}

	// Many waiters, one write: every one wakes.
	const n = 8
	var wg sync.WaitGroup
	got := make([]ChangeWait, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = waitOn(t, e, "app", "", c, 30*time.Second)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	start = time.Now()
	mustQuery(t, e, "app", Write, "INSERT INTO t VALUES (1)")
	wg.Wait()
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("waiters woke after %v", d)
	}
	for i, w := range got {
		if !w.Changed || w.Cursor == c {
			t.Fatalf("waiter %d: %+v", i, w)
		}
	}

	// A canceled wait returns without an error and without a change.
	ctx, cancel := context.WithCancel(context.Background())
	c = cursorOf(t, e, "app", "")
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	w, err := e.WaitChange(ctx, Target{Workspace: ws(e), Database: "app"}, c, 30*time.Second)
	if err != nil || w.Changed || w.Cursor != c {
		t.Fatalf("canceled: %+v, %v", w, err)
	}
}

func TestWaitChangeMaintenance(t *testing.T) {
	e := newEngine(t, nil)
	ctx := context.Background()
	d := mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	if _, err := e.CreateBranch(ctx, ws(e), "app", "dev", BranchSource{Branch: "main"}, NewBranchOptions{}); err != nil {
		t.Fatal(err)
	}

	// The cursor lives on past the branch's pool closing.
	c := cursorOf(t, e, "app", "dev")
	dev, err := e.GetBranch(ctx, ws(e), "app", "dev")
	if err != nil {
		t.Fatal(err)
	}
	e.dropPool(dev.ID)
	if cursorOf(t, e, "app", "dev") != c {
		t.Fatal("closing the pool moved the cursor")
	}

	if _, err := e.ResetBranch(ctx, ws(e), "app", "dev"); err != nil {
		t.Fatal(err)
	}
	if w := waitOn(t, e, "app", "dev", c, time.Minute); !w.Changed {
		t.Fatal("a reset didn't move the cursor")
	}

	// Promoting dev wakes a wait on the default branch, which was main.
	c = cursorOf(t, e, "app", "")
	done := make(chan ChangeWait, 1)
	go func() { done <- waitOn(t, e, "app", "", c, 30*time.Second) }()
	time.Sleep(50 * time.Millisecond)
	if _, err := e.PromoteBranch(ctx, ws(e), "app", "dev"); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if !w.Changed {
			t.Fatalf("promote: %+v", w)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("promote didn't wake the wait on the old default")
	}
	// The next wait with that cursor resolves the new default, a different
	// branch, so it reports a change at once.
	if w := waitOn(t, e, "app", "", c, time.Minute); !w.Changed || !strings.Contains(w.Cursor, dev.ID) {
		t.Fatalf("after promote: %+v, want a cursor on %s", w, dev.ID)
	}

	// Deleting a branch ends its waits with branch_not_found.
	if _, err := e.CreateBranch(ctx, ws(e), "app", "tmp", BranchSource{Branch: "dev"}, NewBranchOptions{}); err != nil {
		t.Fatal(err)
	}
	c = cursorOf(t, e, "app", "tmp")
	errc := make(chan error, 1)
	go func() {
		_, err := e.WaitChange(ctx, Target{Workspace: ws(e), Database: d.ID, Branch: "tmp"}, c, 30*time.Second)
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := e.DeleteBranch(ctx, ws(e), "app", "tmp"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		wantCode(t, err, apierr.BranchNotFound, "wait on a deleted branch")
	case <-time.After(5 * time.Second):
		t.Fatal("deleting the branch didn't end the wait")
	}
}
