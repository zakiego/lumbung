package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/sqlitex"
	"github.com/zakiego/lumbung/apps/engine/internal/sqlsplit"
)

// Tx is a transaction on one branch connection. Every query and batch runs
// in one that is opened and finished within the call (session). BeginTx opens
// one that stays open across calls, for the libSQL protocol's interactive
// transactions: it holds the branch's gate, its pool and its connection (the
// writer, for write access) until Commit, Rollback or Abort.
type Tx struct {
	e      *Engine
	tg     target
	access Access
	hooks  sessionHooks
	g      *gate
	p      *branchPool
	conn   *sqlitex.Conn
	grant  sizeGrant
	// granted is set once applySizeLimit reserved room, which release gives
	// back.
	granted bool

	// ctx ends when the transaction is aborted from outside (Abort), which
	// interrupts a statement in flight.
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	finished bool
}

// TxOptions change how an interactive transaction runs.
type TxOptions struct {
	// Migration is BatchOptions.Migration for the whole transaction:
	// foreign keys off, and every foreign key checked once before COMMIT.
	Migration bool
	// Deadline, when set, ends the transaction: a statement running then is
	// interrupted, and the transaction is rolled back and released.
	Deadline time.Time
}

// Budget caps what one request may return: rows per statement and bytes
// across every statement it runs.
type Budget struct{ b resultBudget }

// NewBudget returns a budget with the configured caps.
func (e *Engine) NewBudget() *Budget {
	return &Budget{resultBudget{rows: e.cfg.MaxResultRows, bytes: e.cfg.MaxResultBytes}}
}

// begin resolves the target, takes the branch's gate, a connection and, for
// writes, the size cap, and runs BEGIN. On error everything taken is given
// back.
func (e *Engine) begin(ctx context.Context, t Target, access Access, hooks sessionHooks) (*Tx, error) {
	if access != Read && access != Write {
		return nil, apierr.New(apierr.BadRequest, "unknown access")
	}
	tg, err := e.resolve(ctx, t.Database, t.Branch)
	if err != nil {
		return nil, err
	}
	// Share the branch's gate so a reset, delete or import can't swap the
	// file mid-request, then look the branch up again: it may have been
	// deleted, or its database trashed, while this request waited.
	g := e.gates.get(tg.branch.ID)
	if err := g.share(ctx); err != nil {
		return nil, apierr.New(apierr.Busy, "timed out waiting for maintenance on the branch")
	}
	tx := &Tx{e: e, access: access, hooks: hooks, g: g}
	ok := false
	defer func() {
		if !ok {
			tx.release()
		}
	}()
	if tg, err = e.resolve(ctx, tg.db.ID, tg.branch.ID); err != nil {
		return nil, err
	}
	tx.tg = tg
	tx.p = e.acquirePool(tg.branch.ID, tg.path)
	var conn *sqlitex.Conn
	if access == Write {
		conn, err = tx.p.acquireWriter(ctx, false)
	} else {
		conn, err = tx.p.acquireReader(ctx)
	}
	if err != nil {
		return nil, mapAcquire(err)
	}
	tx.conn = conn
	// Start each session from a clean last-insert rowid, so nothing left on
	// the pooled connection by an earlier request shows through.
	conn.SetLastInsertRowID(0)
	if access == Write {
		if tx.grant, err = e.applySizeLimit(conn); err != nil {
			return nil, err
		}
		tx.granted = true
	}

	conn.SetInterrupt(ctx)
	begin := "BEGIN"
	if access == Write {
		begin = "BEGIN IMMEDIATE"
	}
	if hooks.beforeBegin != nil {
		if err := hooks.beforeBegin(conn); err != nil {
			return nil, e.sqlErr(err, tx.grant.quotaLimited)
		}
	}
	if err := conn.ExecTrusted(begin); err != nil {
		return nil, e.sqlErr(err, tx.grant.quotaLimited)
	}
	ok = true
	return tx, nil
}

// commit runs the commit hook and COMMIT, rolling back if either fails. It
// doesn't release.
func (tx *Tx) commit() error {
	if tx.hooks.beforeCommit != nil {
		if err := tx.hooks.beforeCommit(tx.conn); err != nil {
			_ = tx.conn.ExecTrusted("ROLLBACK")
			return tx.e.sqlErr(err, tx.grant.quotaLimited)
		}
	}
	if err := tx.conn.ExecTrusted("COMMIT"); err != nil {
		_ = tx.conn.ExecTrusted("ROLLBACK")
		return tx.e.sqlErr(err, tx.grant.quotaLimited)
	}
	return nil
}

// release gives back what begin took, in reverse order. The connection's
// Reset rolls back a transaction still open.
func (tx *Tx) release() {
	e := tx.e
	if tx.conn != nil && !tx.conn.GetAutocommit() {
		_ = tx.conn.ExecTrusted("ROLLBACK")
	}
	if tx.granted {
		e.truncateWAL(tx.conn, tx.tg.path)
		e.finishWrite(tx.tg.branch.ID, tx.tg.path, tx.grant)
	}
	if tx.conn != nil {
		if tx.access == Write {
			tx.p.releaseWriter(tx.conn)
		} else {
			tx.p.releaseReader(tx.conn)
		}
	}
	if tx.p != nil {
		e.releasePool(tx.p)
	}
	tx.g.unshare()
	if tx.cancel != nil {
		tx.cancel()
	}
}

// migrationHooks turn foreign keys off for a transaction and check them all
// before it commits (BatchOptions.Migration).
func migrationHooks() sessionHooks {
	return sessionHooks{
		beforeBegin: func(conn *sqlitex.Conn) error { return conn.ExecTrusted("PRAGMA foreign_keys = OFF") },
		beforeCommit: func(conn *sqlitex.Conn) error {
			n, err := trustedInt(conn, "SELECT count(*) FROM pragma_foreign_key_check")
			if err != nil {
				return err
			}
			if n > 0 {
				return apierr.Newf(apierr.ConstraintViolation, "the migration leaves %d rows that break foreign keys; nothing was applied", n)
			}
			return nil
		},
	}
}

// BeginTx opens an interactive transaction: a read-only connection and BEGIN
// for Read, the branch's writer and BEGIN IMMEDIATE for Write. It stays open,
// holding that connection, until Commit, Rollback or Abort. Waiting for the
// connection is bounded by the statement timeout. Inside it, user SQL may
// use savepoints; BEGIN, COMMIT and ROLLBACK stay denied.
func (e *Engine) BeginTx(ctx context.Context, t Target, access Access, opts TxOptions) (*Tx, error) {
	var hooks sessionHooks
	if opts.Migration {
		if access != Write {
			return nil, apierr.New(apierr.ReadOnly, "a migration needs write access")
		}
		hooks = migrationHooks()
	}
	ctx, cancel := context.WithTimeout(ctx, e.cfg.StatementTimeout)
	defer cancel()
	tx, err := e.begin(ctx, t, access, hooks)
	if err != nil {
		return nil, err
	}
	tx.conn.SetInterrupt(context.Background())
	tx.conn.AllowSavepoints(true)
	if opts.Deadline.IsZero() {
		tx.ctx, tx.cancel = context.WithCancel(context.Background())
	} else {
		tx.ctx, tx.cancel = context.WithDeadline(context.Background(), opts.Deadline)
		context.AfterFunc(tx.ctx, func() {
			if errors.Is(tx.ctx.Err(), context.DeadlineExceeded) {
				tx.Rollback()
			}
		})
	}

	e.txMu.Lock()
	if e.txClosed {
		e.txMu.Unlock()
		tx.release()
		return nil, apierr.New(apierr.Unavailable, "the engine is shutting down")
	}
	e.txs[tx] = struct{}{}
	e.txMu.Unlock()
	return tx, nil
}

// BranchID is the id of the branch the transaction runs on.
func (tx *Tx) BranchID() string { return tx.tg.branch.ID }

// Writable reports whether the transaction runs on the branch's writer.
func (tx *Tx) Writable() bool { return tx.access == Write }

// Done reports whether the transaction has ended: committed, rolled back,
// aborted, or rolled back by SQLite after an error.
func (tx *Tx) Done() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.finished
}

func errTxDone() error {
	return apierr.New(apierr.TransactionAborted, "the transaction has ended; send ROLLBACK and start again")
}

func errTxDeadline() error {
	return apierr.New(apierr.TransactionAborted, "the transaction passed its time limit and was rolled back")
}

// Exec runs one statement in the transaction, within the statement timeout,
// the transaction's deadline and the budget. The transaction is rolled back
// and released, and Done reports true, when SQLite ended it because of the
// error (an interrupt, a full branch), when the deadline passed, or when a
// statement that changed rows failed with result_too_large, since part of it
// ran. A read past the caps fails alone.
func (tx *Tx) Exec(ctx context.Context, stmt Statement, b *Budget) (*Result, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.finished {
		return nil, errTxDone()
	}
	if tx.ctx.Err() != nil {
		tx.finishLocked()
		return nil, errTxDeadline()
	}
	ctx, cancel := context.WithTimeout(ctx, tx.e.cfg.StatementTimeout)
	defer cancel()
	stop := context.AfterFunc(tx.ctx, cancel)
	defer stop()
	changesBefore := tx.conn.TotalChanges()
	tx.conn.SetInterrupt(ctx)
	res, err := runOne(tx.conn, stmt, &b.b)
	tx.conn.SetInterrupt(context.Background())
	if err == nil {
		return res, nil
	}
	mapped := tx.e.sqlErr(err, tx.grant.quotaLimited)
	tooLarge := apierr.As(mapped).Code == apierr.ResultTooLarge && tx.conn.TotalChanges() > changesBefore
	if tooLarge || tx.conn.GetAutocommit() || tx.ctx.Err() != nil {
		tx.finishLocked()
	}
	if errors.Is(tx.ctx.Err(), context.DeadlineExceeded) {
		return nil, errTxDeadline()
	}
	return nil, mapped
}

// Describe prepares a statement in the transaction without running it.
func (tx *Tx) Describe(sql string) (*Description, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.finished {
		return nil, errTxDone()
	}
	d, err := describe(tx.conn, sql)
	if err != nil {
		return nil, tx.e.sqlErr(err, false)
	}
	return d, nil
}

// Commit commits and releases the transaction. A failed commit rolls back.
func (tx *Tx) Commit(ctx context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.finished {
		return errTxDone()
	}
	ctx, cancel := context.WithTimeout(ctx, tx.e.cfg.StatementTimeout)
	defer cancel()
	stop := context.AfterFunc(tx.ctx, cancel)
	defer stop()
	tx.conn.SetInterrupt(ctx)
	err := tx.commit()
	tx.finishLocked()
	return err
}

// Rollback rolls back and releases the transaction. It does nothing once
// the transaction has ended.
func (tx *Tx) Rollback() {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if !tx.finished {
		tx.finishLocked()
	}
}

// Abort interrupts a statement in flight, then rolls back.
func (tx *Tx) Abort() {
	if tx.cancel != nil {
		tx.cancel()
	}
	tx.Rollback()
}

// finishLocked rolls back anything still open and releases. Call with mu.
func (tx *Tx) finishLocked() {
	tx.finished = true
	tx.release()
	tx.e.txMu.Lock()
	delete(tx.e.txs, tx)
	tx.e.txMu.Unlock()
}

// abortTxs rolls back the open interactive transactions on a branch, or on
// every branch when branchID is empty: the engine is closing, or a reset,
// delete or import is about to replace the file.
func (e *Engine) abortTxs(branchID string) {
	e.txMu.Lock()
	var hit []*Tx
	for tx := range e.txs {
		if branchID == "" || tx.tg.branch.ID == branchID {
			hit = append(hit, tx)
		}
	}
	if branchID == "" {
		e.txClosed = true
	}
	e.txMu.Unlock()
	for _, tx := range hit {
		tx.Abort()
	}
}

// QueryWith runs one statement in its own transaction, like Query, counting
// its result against a budget shared with other statements of the request.
// With write access, a statement that only reads runs on a read-only
// connection, so it doesn't wait for an interactive transaction holding the
// writer. It sees the same committed data either way.
//
// A non-nil refuseWrites is returned, without running anything, for a
// statement that would write.
func (e *Engine) QueryWith(ctx context.Context, t Target, access Access, stmt Statement, b *Budget, refuseWrites error) (*Result, error) {
	var res *Result
	run := func(conn *sqlitex.Conn, _ target) error {
		var err error
		res, err = runOne(conn, stmt, &b.b)
		return err
	}
	if access == Write {
		needsWriter := false
		err := e.session(ctx, t, Read, e.cfg.StatementTimeout, func(conn *sqlitex.Conn, tg target) error {
			ro, err := readOnlyStatement(conn, stmt.SQL)
			if err != nil {
				return err
			}
			if !ro {
				needsWriter = true
				return errNeedsWriter
			}
			return run(conn, tg)
		})
		if !needsWriter {
			return res, err
		}
		if refuseWrites != nil {
			return nil, refuseWrites
		}
	}
	err := e.session(ctx, t, access, e.cfg.StatementTimeout, run)
	return res, err
}

// errNeedsWriter sends a statement from the read-only try to the writer.
var errNeedsWriter = errors.New("engine: statement writes")

// readOnlyStatement prepares SQL to see whether it only reads. A statement
// that fails to prepare here would fail the same way on the writer (same
// file, same authorizer), so its error is final. Empty SQL goes to the
// writer, which reports it.
func readOnlyStatement(conn *sqlitex.Conn, sql string) (bool, error) {
	stmt, _, err := conn.Prepare(sql)
	if err != nil || stmt == nil {
		return false, err
	}
	defer stmt.Close()
	return stmt.ReadOnly(), nil
}

// Description is what preparing a statement tells about it.
type Description struct {
	// Params are the parameter names in order (?NNN, :name, @name, $name
	// with their prefix), "" for a nameless ?.
	Params   []string
	Columns  []Column
	ReadOnly bool // the statement doesn't write the database
	Explain  bool // an EXPLAIN or EXPLAIN QUERY PLAN statement
}

// Describe prepares one statement on a read-only connection without running
// it.
func (e *Engine) Describe(ctx context.Context, t Target, sql string) (*Description, error) {
	var d *Description
	err := e.session(ctx, t, Read, e.cfg.StatementTimeout, func(conn *sqlitex.Conn, _ target) error {
		var err error
		d, err = describe(conn, sql)
		return err
	})
	return d, err
}

func describe(conn *sqlitex.Conn, sql string) (*Description, error) {
	stmt, tail, err := conn.Prepare(sql)
	if err != nil {
		return nil, err
	}
	if stmt == nil {
		return nil, apierr.New(apierr.EmptyStatement, "the SQL holds no statement")
	}
	defer stmt.Close()
	if err := checkTail(conn, tail); err != nil {
		return nil, err
	}
	d := &Description{Params: []string{}, Columns: []Column{}, ReadOnly: stmt.ReadOnly()}
	for i := 1; i <= stmt.BindCount(); i++ {
		d.Params = append(d.Params, stmt.BindName(i))
	}
	for i := 0; i < stmt.ColumnCount(); i++ {
		d.Columns = append(d.Columns, Column{Name: stmt.ColumnName(i), DeclType: stmt.ColumnDeclType(i)})
	}
	start := strings.ToUpper(sqlsplit.StripComments(sql))
	d.Explain = strings.HasPrefix(start, "EXPLAIN") && (len(start) == 7 || !sqlsplit.IsWordByte(start[7]))
	return d, nil
}
