package engine

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/sqlitex"
	"github.com/zakiego/lumbung/apps/engine/internal/value"
)

// Access is what the caller may do on a branch.
type Access int

const (
	// Read runs on a read-only connection: SQLite itself refuses writes.
	Read Access = iota + 1
	// Write runs on the branch's writer connection.
	Write
)

// Statement is one SQL statement with its parameters. Args binds positional
// parameters (?, ?NNN) in order; Named binds :name, @name and $name, keyed
// with or without the prefix. A statement uses one style or the other.
type Statement struct {
	SQL   string                 `json:"sql"`
	Args  []value.Value          `json:"args,omitempty"`
	Named map[string]value.Value `json:"named,omitempty"`
}

// Column describes a result column.
type Column struct {
	Name     string `json:"name"`
	DeclType string `json:"decltype"` // declared type, empty for expressions
}

// Result is the outcome of one statement.
type Result struct {
	Columns         []Column        `json:"columns"`
	Rows            [][]value.Value `json:"rows"`
	RowsAffected    int64           `json:"rows_affected"`
	LastInsertRowID int64           `json:"last_insert_rowid"`
}

// Target names a branch. An empty Branch means the database's default.
type Target struct {
	Database string
	Branch   string
}

// Query runs one statement in its own transaction.
func (e *Engine) Query(ctx context.Context, t Target, access Access, stmt Statement) (*Result, error) {
	res, err := e.run(ctx, t, access, []Statement{stmt})
	if err != nil {
		return nil, err
	}
	return res[0], nil
}

// Batch runs statements in order in one transaction: all of them take
// effect or none do.
func (e *Engine) Batch(ctx context.Context, t Target, access Access, stmts []Statement) ([]*Result, error) {
	return e.BatchWith(ctx, t, access, stmts, BatchOptions{})
}

// BatchOptions change how a batch runs.
type BatchOptions struct {
	// Migration turns foreign key enforcement off for the batch and checks
	// every foreign key once before committing. Rebuilding a table (create
	// a new one, copy, drop the old, rename), which schema tools generate
	// for column changes, would otherwise fire ON DELETE actions on the
	// drop and delete child rows. Needs write access.
	Migration bool
}

// BatchWith is Batch with options.
func (e *Engine) BatchWith(ctx context.Context, t Target, access Access, stmts []Statement, opts BatchOptions) ([]*Result, error) {
	if len(stmts) == 0 {
		return nil, apierr.New(apierr.EmptyStatement, "the batch has no statements")
	}
	if !opts.Migration {
		return e.run(ctx, t, access, stmts)
	}
	if access != Write {
		return nil, apierr.New(apierr.ReadOnly, "a migration needs write access")
	}
	var results []*Result
	err := e.sessionWith(ctx, t, access, e.cfg.StatementTimeout, sessionHooks{
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
	}, func(conn *sqlitex.Conn, _ target) error {
		var err error
		results, err = e.runAll(conn, stmts)
		return err
	})
	return results, err
}

// sessionHooks run engine SQL around a session's transaction.
type sessionHooks struct {
	beforeBegin  func(*sqlitex.Conn) error
	beforeCommit func(*sqlitex.Conn) error
}

func (e *Engine) run(ctx context.Context, t Target, access Access, stmts []Statement) ([]*Result, error) {
	var results []*Result
	err := e.session(ctx, t, access, e.cfg.StatementTimeout, func(conn *sqlitex.Conn, _ target) error {
		var err error
		results, err = e.runAll(conn, stmts)
		return err
	})
	return results, err
}

// session runs f on a connection to the target inside one transaction:
// a read-only connection and BEGIN for Read, the writer and BEGIN IMMEDIATE
// with the size caps applied for Write. It commits when f succeeds and rolls
// back otherwise, and maps errors to API errors.
func (e *Engine) session(ctx context.Context, t Target, access Access, timeout time.Duration, f func(*sqlitex.Conn, target) error) error {
	return e.sessionWith(ctx, t, access, timeout, sessionHooks{}, f)
}

func (e *Engine) sessionWith(ctx context.Context, t Target, access Access, timeout time.Duration, hooks sessionHooks, f func(*sqlitex.Conn, target) error) error {
	if access != Read && access != Write {
		return apierr.New(apierr.BadRequest, "unknown access")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tg, err := e.resolve(ctx, t.Database, t.Branch)
	if err != nil {
		return err
	}
	// Share the branch's gate so a reset, delete or import can't swap the
	// file mid-request, then look the branch up again: it may have been
	// deleted, or its database trashed, while this request waited.
	g := e.gates.get(tg.branch.ID)
	if err := g.share(ctx); err != nil {
		return apierr.New(apierr.Busy, "timed out waiting for maintenance on the branch")
	}
	defer g.unshare()
	if tg, err = e.resolve(ctx, tg.db.ID, tg.branch.ID); err != nil {
		return err
	}

	p := e.acquirePool(tg.branch.ID, tg.path)
	defer e.releasePool(p)
	var conn *sqlitex.Conn
	if access == Write {
		conn, err = p.acquireWriter(ctx, false)
	} else {
		conn, err = p.acquireReader(ctx)
	}
	if err != nil {
		return mapAcquire(err)
	}
	defer func() {
		if access == Write {
			p.releaseWriter(conn)
		} else {
			p.releaseReader(conn)
		}
	}()

	var grant sizeGrant
	if access == Write {
		grant, err = e.applySizeLimit(conn)
		if err != nil {
			return err
		}
		defer func() {
			e.truncateWAL(conn, tg.path)
			e.finishWrite(tg.branch.ID, tg.path, grant)
		}()
	}

	conn.SetInterrupt(ctx)
	begin := "BEGIN"
	if access == Write {
		begin = "BEGIN IMMEDIATE"
	}
	if hooks.beforeBegin != nil {
		if err := hooks.beforeBegin(conn); err != nil {
			return e.sqlErr(err, grant.quotaLimited)
		}
	}
	if err := conn.ExecTrusted(begin); err != nil {
		return e.sqlErr(err, grant.quotaLimited)
	}
	if err := f(conn, tg); err != nil {
		_ = conn.ExecTrusted("ROLLBACK")
		return e.sqlErr(err, grant.quotaLimited)
	}
	if hooks.beforeCommit != nil {
		if err := hooks.beforeCommit(conn); err != nil {
			_ = conn.ExecTrusted("ROLLBACK")
			return e.sqlErr(err, grant.quotaLimited)
		}
	}
	if err := conn.ExecTrusted("COMMIT"); err != nil {
		_ = conn.ExecTrusted("ROLLBACK")
		return e.sqlErr(err, grant.quotaLimited)
	}
	return nil
}

func mapAcquire(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return apierr.New(apierr.Busy, "timed out waiting for a connection")
	}
	if errors.Is(err, errPoolClosed) {
		return apierr.New(apierr.Busy, "branch is closing, retry")
	}
	// Opening a connection isn't user SQL: a failure here (a missing or
	// unreadable file) is the engine's problem, not a denied statement.
	return apierr.New(apierr.Internal, "can't open the branch").WithDetail(err.Error())
}

// sqlErr maps an error from running statements. A full database is the
// branch limit unless the instance quota set the tighter cap.
func (e *Engine) sqlErr(err error, quotaLimited bool) error {
	ae := apierr.FromSQLite(err)
	if ae.Code == apierr.BranchSizeLimit && quotaLimited {
		return apierr.New(apierr.InstanceQuotaExceeded, "instance size quota reached").WithDetail(ae.Detail)
	}
	return ae
}

// sizeGrant is the disk room a write transaction reserved.
type sizeGrant struct {
	bytes        int64
	quotaLimited bool // the instance quota, not the branch limit, set the cap
}

// applySizeLimit caps how far the writer's file may grow with
// max_page_count: the branch limit, or less when the instance quota leaves
// less room. Room is reserved under the usage lock until finishWrite, so
// concurrent writers on different branches can't each spend the same free
// space. New pages land in the WAL before the database file, so a
// transaction may add at most half the free room in pages.
//
// The cap is never below the current page count: a full branch, or one on a
// full instance, still runs deletes and writes that reuse free pages.
func (e *Engine) applySizeLimit(conn *sqlitex.Conn) (sizeGrant, error) {
	pageSize, err := trustedInt(conn, "PRAGMA page_size")
	if err != nil {
		return sizeGrant{}, apierr.FromSQLite(err)
	}
	pages, err := trustedInt(conn, "PRAGMA page_count")
	if err != nil {
		return sizeGrant{}, apierr.FromSQLite(err)
	}
	branchPages := e.cfg.MaxBranchBytes / pageSize

	e.usageMu.Lock()
	free := e.cfg.MaxInstanceBytes - e.total - e.reserved
	roomPages := int64(0)
	if free > 0 {
		roomPages = free / (2 * pageSize)
	}
	limit := min(branchPages, pages+roomPages)
	g := sizeGrant{quotaLimited: pages+roomPages < branchPages}
	if limit < pages {
		limit = pages
	}
	g.bytes = (limit - pages) * pageSize * 2
	e.reserved += g.bytes
	e.usageMu.Unlock()

	if _, err := trustedInt(conn, "PRAGMA max_page_count = "+strconv.FormatInt(max(limit, 1), 10)); err != nil {
		e.finishWrite("", "", g)
		return sizeGrant{}, apierr.FromSQLite(err)
	}
	return g, nil
}

// finishWrite releases a write's reservation and records the branch's new
// size. An empty branchID only releases.
func (e *Engine) finishWrite(branchID, path string, g sizeGrant) {
	e.usageMu.Lock()
	e.reserved -= g.bytes
	e.usageMu.Unlock()
	if branchID != "" {
		e.recordSize(branchID, fileBytes(path))
	}
}

// truncateWAL checkpoints and truncates the WAL once it passes a quarter of
// the branch limit (at most 1 MiB), so the log can't sit on disk at the size
// of the largest transaction. A checkpoint blocked by readers just waits for
// the next write.
func (e *Engine) truncateWAL(conn *sqlitex.Conn, path string) {
	threshold := min(e.cfg.MaxBranchBytes/4, 1<<20)
	fi, err := os.Stat(path + "-wal")
	if err != nil || fi.Size() <= threshold {
		return
	}
	_ = conn.ExecTrusted("PRAGMA wal_checkpoint(TRUNCATE)")
}

func trustedInt(conn *sqlitex.Conn, sql string) (int64, error) {
	stmt, err := conn.PrepareTrusted(sql)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var n int64
	if stmt.Step() {
		n = stmt.ColumnInt64(0)
	}
	return n, stmt.Err()
}

// runAll runs statements, counting result rows and bytes against the caps
// across the whole request.
func (e *Engine) runAll(conn *sqlitex.Conn, stmts []Statement) ([]*Result, error) {
	budget := &resultBudget{rows: e.cfg.MaxResultRows, bytes: e.cfg.MaxResultBytes}
	out := make([]*Result, 0, len(stmts))
	for i := range stmts {
		res, err := runOne(conn, stmts[i], budget)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

type resultBudget struct {
	rows  int   // per statement
	bytes int64 // per request
	used  int64
}

func runOne(conn *sqlitex.Conn, st Statement, budget *resultBudget) (*Result, error) {
	stmt, tail, err := conn.Prepare(st.SQL)
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
	if err := bind(stmt, st); err != nil {
		return nil, err
	}

	// Start from a clean last-insert rowid so a statement never reports one
	// left on the pooled connection by an earlier request.
	conn.SetLastInsertRowID(0)
	before := conn.TotalChanges()

	n := stmt.ColumnCount()
	res := &Result{Columns: make([]Column, n), Rows: [][]value.Value{}}
	for i := 0; i < n; i++ {
		res.Columns[i] = Column{Name: stmt.ColumnName(i), DeclType: stmt.ColumnDeclType(i)}
	}
	for stmt.Step() {
		if len(res.Rows) >= budget.rows {
			return nil, apierr.Newf(apierr.ResultTooLarge, "the statement returns more than %d rows", budget.rows)
		}
		row := make([]value.Value, n)
		for i := 0; i < n; i++ {
			v, size := column(stmt, i)
			row[i] = v
			budget.used += size
		}
		if budget.used > budget.bytes {
			return nil, apierr.Newf(apierr.ResultTooLarge, "the result is larger than %d bytes", budget.bytes)
		}
		res.Rows = append(res.Rows, row)
	}
	if err := stmt.Err(); err != nil {
		return nil, err
	}
	// changes() keeps the count of the last INSERT, UPDATE or DELETE, so
	// only read it when this statement changed rows (total_changes moved).
	// It excludes rows changed by triggers, which is the documented meaning.
	if conn.TotalChanges() > before {
		res.RowsAffected = conn.Changes()
	}
	res.LastInsertRowID = conn.LastInsertRowID()
	return res, nil
}

// checkTail refuses SQL text with a second statement after the first.
// Whitespace and comments after it are fine.
func checkTail(conn *sqlitex.Conn, tail string) error {
	if strings.TrimSpace(tail) == "" {
		return nil
	}
	next, _, err := conn.Prepare(tail)
	if next != nil {
		next.Close()
		return apierr.New(apierr.MultipleStatements, "send one statement per query, or use batch")
	}
	if err != nil {
		// The rest doesn't parse; still more than one statement was sent.
		return apierr.New(apierr.MultipleStatements, "send one statement per query, or use batch")
	}
	return nil
}

func bind(stmt *sqlite3.Stmt, st Statement) error {
	count := stmt.BindCount()
	if len(st.Args) > 0 && len(st.Named) > 0 {
		return apierr.New(apierr.InvalidParams, "use args or named, not both")
	}
	if len(st.Named) == 0 {
		if len(st.Args) != count {
			return apierr.Newf(apierr.InvalidParams, "the statement has %d parameters, got %d args", count, len(st.Args))
		}
		for i, v := range st.Args {
			if err := bindValue(stmt, i+1, v); err != nil {
				return err
			}
		}
		return nil
	}
	used := 0
	for i := 1; i <= count; i++ {
		name := stmt.BindName(i)
		if name == "" {
			return apierr.Newf(apierr.InvalidParams, "parameter %d is positional; use args", i)
		}
		v, ok := st.Named[name]
		if !ok {
			v, ok = st.Named[name[1:]]
		}
		if !ok {
			return apierr.Newf(apierr.InvalidParams, "missing a value for %s", name)
		}
		used++
		if err := bindValue(stmt, i, v); err != nil {
			return err
		}
	}
	if used != len(st.Named) {
		return apierr.Newf(apierr.InvalidParams, "got %d named values, the statement uses %d", len(st.Named), used)
	}
	return nil
}

func bindValue(stmt *sqlite3.Stmt, i int, v value.Value) error {
	var err error
	switch v.Kind {
	case value.Null:
		err = stmt.BindNull(i)
	case value.Integer:
		err = stmt.BindInt64(i, v.Int)
	case value.Float:
		err = stmt.BindFloat(i, v.Float)
	case value.Text:
		err = stmt.BindText(i, v.Text)
	case value.Blob:
		err = stmt.BindBlob(i, v.Blob)
	default:
		return apierr.Newf(apierr.InvalidParams, "parameter %d has no type", i)
	}
	return err
}

// column reads one value and the bytes it counts toward the result cap: an
// estimate of its JSON size, and never less than the memory a Value takes,
// so a wide result of NULLs can't build a huge response cheaply.
func column(stmt *sqlite3.Stmt, i int) (value.Value, int64) {
	const minCost = 48
	switch stmt.ColumnType(i) {
	case sqlite3.INTEGER:
		return value.IntValue(stmt.ColumnInt64(i)), minCost
	case sqlite3.FLOAT:
		return value.FloatValue(stmt.ColumnFloat(i)), minCost
	case sqlite3.TEXT:
		s := stmt.ColumnText(i)
		return value.TextValue(s), minCost + int64(len(s))*2 // escaping can double it
	case sqlite3.BLOB:
		b := stmt.ColumnBlob(i, nil)
		return value.BlobValue(b), minCost + int64(len(b))*2 // base64, plus the raw bytes held
	}
	return value.NullValue(), minCost
}
