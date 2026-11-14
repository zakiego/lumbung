package engine

import (
	"context"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

func beginTx(t *testing.T, e *Engine, db string, access Access) *Tx {
	t.Helper()
	tx, err := e.BeginTx(context.Background(), Target{Database: db}, access, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tx.Rollback)
	return tx
}

func txExec(t *testing.T, e *Engine, tx *Tx, sql string, args ...any) *Result {
	t.Helper()
	r, err := tx.Exec(context.Background(), stmt(sql, args...), e.NewBudget())
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return r
}

func count(t *testing.T, e *Engine, db, table string) int64 {
	t.Helper()
	return scalar(t, mustQuery(t, e, db, Read, "SELECT count(*) FROM "+table)).Int
}

func TestTxCommitAcrossCalls(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")

	tx := beginTx(t, e, "app", Write)
	if !tx.Writable() {
		t.Fatal("a write transaction isn't on the writer")
	}
	txExec(t, e, tx, "INSERT INTO t VALUES (?)", 1)
	r := txExec(t, e, tx, "INSERT INTO t VALUES (?)", 2)
	if r.RowsAffected != 1 || r.LastInsertRowID != 2 {
		t.Fatalf("insert result: %+v", r)
	}
	if got := scalar(t, txExec(t, e, tx, "SELECT count(*) FROM t")).Int; got != 2 {
		t.Fatalf("the transaction sees %d rows", got)
	}
	if got := count(t, e, "app", "t"); got != 0 {
		t.Fatalf("a reader sees %d uncommitted rows", got)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !tx.Done() {
		t.Fatal("not done after commit")
	}
	if got := count(t, e, "app", "t"); got != 2 {
		t.Fatalf("after commit: %d rows", got)
	}
	_, err := tx.Exec(context.Background(), stmt("SELECT 1"), e.NewBudget())
	wantCode(t, err, apierr.TransactionAborted, "exec after commit")
	wantCode(t, tx.Commit(context.Background()), apierr.TransactionAborted, "second commit")
}

func TestTxRollback(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	tx := beginTx(t, e, "app", Write)
	txExec(t, e, tx, "INSERT INTO t VALUES (1)")
	tx.Rollback()
	tx.Rollback() // a second rollback does nothing
	if got := count(t, e, "app", "t"); got != 0 {
		t.Fatalf("after rollback: %d rows", got)
	}
	// The writer is free again.
	mustQuery(t, e, "app", Write, "INSERT INTO t VALUES (2)")
}

// An open write transaction holds the branch's only writer, so other writes
// wait for it; reads go on.
func TestTxHoldsTheWriter(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	tx := beginTx(t, e, "app", Write)
	txExec(t, e, tx, "INSERT INTO t VALUES (1)")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := e.Query(ctx, Target{Database: "app"}, Write, stmt("INSERT INTO t VALUES (2)"))
	wantCode(t, err, apierr.Busy, "write while a transaction holds the writer")
	mustQuery(t, e, "app", Read, "SELECT count(*) FROM t")

	done := make(chan error, 1)
	go func() {
		_, err := e.Query(context.Background(), Target{Database: "app"}, Write, stmt("INSERT INTO t VALUES (3)"))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("the waiting write: %v", err)
	}
	if got := count(t, e, "app", "t"); got != 2 {
		t.Fatalf("rows: %d", got)
	}
}

func TestTxSavepointsAndDeniedControl(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	tx := beginTx(t, e, "app", Write)
	for _, sql := range []string{"SAVEPOINT a", "INSERT INTO t VALUES (1)", "ROLLBACK TO a", "RELEASE a", "SAVEPOINT b", "INSERT INTO t VALUES (2)", "RELEASE b"} {
		txExec(t, e, tx, sql)
	}
	for _, sql := range []string{"COMMIT", "ROLLBACK", "BEGIN", "END", "ATTACH ':memory:' AS m"} {
		_, err := tx.Exec(context.Background(), stmt(sql), e.NewBudget())
		wantCode(t, err, apierr.StatementDenied, sql)
	}
	if tx.Done() {
		t.Fatal("a denied statement ended the transaction")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, mustQuery(t, e, "app", Read, "SELECT group_concat(x) FROM t")).Text; got != "2" {
		t.Fatalf("rows: %q", got)
	}
	// Outside an interactive transaction savepoints stay denied.
	_, err := query(e, "app", Write, "SAVEPOINT s")
	wantCode(t, err, apierr.StatementDenied, "savepoint on a plain query")
}

// A constraint failure leaves the transaction open, as in SQLite.
func TestTxStatementErrorKeepsTransaction(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x UNIQUE)")
	tx := beginTx(t, e, "app", Write)
	txExec(t, e, tx, "INSERT INTO t VALUES (1)")
	_, err := tx.Exec(context.Background(), stmt("INSERT INTO t VALUES (1)"), e.NewBudget())
	wantCode(t, err, apierr.ConstraintViolation, "duplicate")
	if tx.Done() {
		t.Fatal("a constraint failure ended the transaction")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := count(t, e, "app", "t"); got != 1 {
		t.Fatalf("rows: %d", got)
	}
}

// A write cancelled at the statement timeout, or a result past the caps,
// ends the transaction: nothing of it is kept and the writer is free.
func TestTxEndsOnTimeoutAndCaps(t *testing.T) {
	e := newEngine(t, func(c *config.Config) {
		c.StatementTimeout = 300 * time.Millisecond
		c.MaxResultRows = 5
	})
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")

	tx := beginTx(t, e, "app", Write)
	txExec(t, e, tx, "INSERT INTO t VALUES (1)")
	_, err := tx.Exec(context.Background(), stmt("INSERT INTO t WITH RECURSIVE c(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM c) SELECT n FROM c"), e.NewBudget())
	wantCode(t, err, apierr.StatementTimeout, "endless insert")
	if !tx.Done() {
		t.Fatal("the timed-out write left the transaction open")
	}
	if got := count(t, e, "app", "t"); got != 0 {
		t.Fatalf("rows kept after the timeout: %d", got)
	}

	tx = beginTx(t, e, "app", Write)
	txExec(t, e, tx, "INSERT INTO t VALUES (1)")
	_, err = tx.Exec(context.Background(), stmt("INSERT INTO t SELECT value FROM json_each('[1,2,3,4,5,6,7]') RETURNING x"), e.NewBudget())
	wantCode(t, err, apierr.ResultTooLarge, "returning past the cap")
	if !tx.Done() {
		t.Fatal("result_too_large left the transaction open")
	}
	if got := count(t, e, "app", "t"); got != 0 {
		t.Fatalf("rows kept after result_too_large: %d", got)
	}
	mustQuery(t, e, "app", Write, "INSERT INTO t VALUES (9)")
}

func TestTxReadOnly(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	tx := beginTx(t, e, "app", Read)
	if tx.Writable() {
		t.Fatal("a read transaction is on the writer")
	}
	txExec(t, e, tx, "SELECT * FROM t")
	for _, sql := range []string{"INSERT INTO t VALUES (1)", "CREATE TABLE u(x)", "PRAGMA user_version = 1"} {
		_, err := tx.Exec(context.Background(), stmt(sql), e.NewBudget())
		wantCode(t, err, apierr.ReadOnly, sql)
	}
	// A read transaction doesn't block writers.
	mustQuery(t, e, "app", Write, "INSERT INTO t VALUES (1)")
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTxMigrationChecksForeignKeys(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	if _, err := e.Batch(context.Background(), Target{Database: "app"}, Write, []Statement{
		stmt("CREATE TABLE p(id INTEGER PRIMARY KEY)"),
		stmt("CREATE TABLE c(id INTEGER PRIMARY KEY, p INTEGER REFERENCES p(id) ON DELETE CASCADE)"),
		stmt("INSERT INTO p VALUES (1)"), stmt("INSERT INTO c VALUES (1, 1)"),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := e.BeginTx(context.Background(), Target{Database: "app"}, Read, TxOptions{Migration: true})
	wantCode(t, err, apierr.ReadOnly, "read migration")

	// Rebuilding the parent doesn't cascade.
	tx, err := e.BeginTx(context.Background(), Target{Database: "app"}, Write, TxOptions{Migration: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"CREATE TABLE p2(id INTEGER PRIMARY KEY)", "INSERT INTO p2 SELECT id FROM p", "DROP TABLE p", "ALTER TABLE p2 RENAME TO p"} {
		txExec(t, e, tx, sql)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := count(t, e, "app", "c"); got != 1 {
		t.Fatalf("children after the rebuild: %d", got)
	}

	// A migration that breaks a foreign key doesn't commit.
	tx, err = e.BeginTx(context.Background(), Target{Database: "app"}, Write, TxOptions{Migration: true})
	if err != nil {
		t.Fatal(err)
	}
	txExec(t, e, tx, "DELETE FROM p")
	wantCode(t, tx.Commit(context.Background()), apierr.ConstraintViolation, "broken foreign key")
	if got := count(t, e, "app", "p"); got != 1 {
		t.Fatalf("parents after a refused migration: %d", got)
	}
	// Foreign keys are back on for the next request.
	_, err = query(e, "app", Write, "DELETE FROM p")
	if err != nil {
		t.Fatal(err)
	}
	if got := count(t, e, "app", "c"); got != 0 {
		t.Fatalf("cascade after the migration: %d children", got)
	}
}

// A reset waiting on the branch rolls back the open transaction instead of
// timing out, and closing the engine gives every connection back.
func TestTxAbortedByMaintenanceAndClose(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	b, err := e.CreateBranch(context.Background(), "app", "dev", BranchSource{}, NewBranchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.BeginTx(context.Background(), Target{Database: "app", Branch: "dev"}, Write, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	txExec(t, e, tx, "INSERT INTO t VALUES (1)")
	if _, err := e.ResetBranch(context.Background(), "app", b.ID); err != nil {
		t.Fatalf("reset with an open transaction: %v", err)
	}
	if !tx.Done() {
		t.Fatal("the reset left the transaction open")
	}

	main := beginTx(t, e, "app", Write)
	txExec(t, e, main, "INSERT INTO t VALUES (1)")
	if err := e.Close(); err != nil {
		t.Fatalf("close with an open transaction: %v", err)
	}
	if !main.Done() {
		t.Fatal("close left the transaction open")
	}
	_, err = e.BeginTx(context.Background(), Target{Database: "app"}, Write, TxOptions{})
	if err == nil {
		t.Fatal("began a transaction on a closed engine")
	}
}

func TestDescribe(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(id INTEGER PRIMARY KEY, name TEXT)")
	d, err := e.Describe(context.Background(), Target{Database: "app"}, "SELECT id, name AS n, 1 FROM t WHERE id = ? AND name = :name")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Params) != 2 || d.Params[0] != "" || d.Params[1] != ":name" {
		t.Fatalf("params: %q", d.Params)
	}
	if len(d.Columns) != 3 || d.Columns[0].DeclType != "INTEGER" || d.Columns[1].Name != "n" || d.Columns[2].DeclType != "" {
		t.Fatalf("columns: %+v", d.Columns)
	}
	if !d.ReadOnly || d.Explain {
		t.Fatalf("flags: %+v", d)
	}
	d, err = e.Describe(context.Background(), Target{Database: "app"}, "/* x */ EXPLAIN QUERY PLAN DELETE FROM t")
	if err != nil || !d.Explain {
		t.Fatalf("explain: %+v %v", d, err)
	}
	d, err = e.Describe(context.Background(), Target{Database: "app"}, "DELETE FROM t")
	if err != nil || d.ReadOnly {
		t.Fatalf("delete: %+v %v", d, err)
	}
	_, err = e.Describe(context.Background(), Target{Database: "app"}, "SELECT 1; SELECT 2")
	wantCode(t, err, apierr.MultipleStatements, "two statements")
	_, err = e.Describe(context.Background(), Target{Database: "app"}, "ATTACH 'x' AS y")
	wantCode(t, err, apierr.StatementDenied, "describe attach")
}

// QueryWith runs a write token's reads on a reader, so they don't wait for
// an interactive transaction holding the writer; writes still do.
func TestQueryWithReadsBesideATransaction(t *testing.T) {
	e := newEngine(t, nil)
	mustCreate(t, e, "app")
	mustQuery(t, e, "app", Write, "CREATE TABLE t(x)")
	tx := beginTx(t, e, "app", Write)
	txExec(t, e, tx, "INSERT INTO t VALUES (1)")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := e.QueryWith(ctx, Target{Database: "app"}, Write, stmt("SELECT count(*) FROM t"), e.NewBudget())
	if err != nil {
		t.Fatalf("read beside a transaction: %v", err)
	}
	if scalar(t, r).Int != 0 {
		t.Fatal("the read saw uncommitted rows")
	}
	short, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	_, err = e.QueryWith(short, Target{Database: "app"}, Write, stmt("INSERT INTO t VALUES (2)"), e.NewBudget())
	wantCode(t, err, apierr.Busy, "write beside a transaction")
	_, err = e.QueryWith(context.Background(), Target{Database: "app"}, Write, stmt("SELECT nope FROM t"), e.NewBudget())
	wantCode(t, err, apierr.SQLError, "bad SQL")
	tx.Rollback()
	r, err = e.QueryWith(context.Background(), Target{Database: "app"}, Write, stmt("INSERT INTO t VALUES (3) RETURNING x"), e.NewBudget())
	if err != nil || scalar(t, r).Int != 3 {
		t.Fatalf("write: %v", err)
	}
}
