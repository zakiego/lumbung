package engine

import (
	"context"
	"errors"
	"strconv"
	"strings"

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
	if len(stmts) == 0 {
		return nil, apierr.New(apierr.EmptyStatement, "the batch has no statements")
	}
	return e.run(ctx, t, access, stmts)
}

func (e *Engine) run(ctx context.Context, t Target, access Access, stmts []Statement) ([]*Result, error) {
	if access != Read && access != Write {
		return nil, apierr.New(apierr.BadRequest, "unknown access")
	}
	tg, err := e.resolve(ctx, t.Database, t.Branch)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, e.cfg.StatementTimeout)
	defer cancel()

	p := e.pool(tg.branch.ID, tg.path)
	var conn *sqlitex.Conn
	if access == Write {
		conn, err = p.acquireWriter(ctx, false)
	} else {
		conn, err = p.acquireReader(ctx)
	}
	if err != nil {
		return nil, mapAcquire(err)
	}
	defer func() {
		if access == Write {
			p.releaseWriter(conn)
		} else {
			p.releaseReader(conn)
		}
	}()

	var quotaLimited bool
	if access == Write {
		quotaLimited, err = e.applySizeLimit(conn)
		if err != nil {
			return nil, err
		}
	}

	conn.SetInterrupt(ctx)
	begin := "BEGIN"
	if access == Write {
		begin = "BEGIN IMMEDIATE"
	}
	if err := conn.ExecTrusted(begin); err != nil {
		return nil, e.sqlErr(err, quotaLimited)
	}
	results, err := e.runAll(conn, stmts)
	if err != nil {
		_ = conn.ExecTrusted("ROLLBACK")
		return nil, e.sqlErr(err, quotaLimited)
	}
	if err := conn.ExecTrusted("COMMIT"); err != nil {
		_ = conn.ExecTrusted("ROLLBACK")
		return nil, e.sqlErr(err, quotaLimited)
	}
	if access == Write {
		e.recordSize(tg.branch.ID, fileBytes(tg.path))
	}
	return results, nil
}

func mapAcquire(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return apierr.New(apierr.Busy, "timed out waiting for a connection")
	}
	if errors.Is(err, errPoolClosed) {
		return apierr.New(apierr.Busy, "branch is closing, retry")
	}
	return apierr.FromSQLite(err)
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

// applySizeLimit caps how far the writer's file may grow with
// max_page_count: the branch limit, or less when the instance quota leaves
// less room. It reports whether the quota was the tighter cap.
func (e *Engine) applySizeLimit(conn *sqlitex.Conn) (bool, error) {
	total := e.TotalBytes()
	if total >= e.cfg.MaxInstanceBytes {
		return true, apierr.Newf(apierr.InstanceQuotaExceeded, "the instance uses %d bytes of its %d byte quota", total, e.cfg.MaxInstanceBytes)
	}
	pageSize, err := trustedInt(conn, "PRAGMA page_size")
	if err != nil {
		return false, apierr.FromSQLite(err)
	}
	pages, err := trustedInt(conn, "PRAGMA page_count")
	if err != nil {
		return false, apierr.FromSQLite(err)
	}
	limit := e.cfg.MaxBranchBytes / pageSize
	quotaLimited := false
	if room := pages + (e.cfg.MaxInstanceBytes-total)/pageSize; room < limit {
		limit, quotaLimited = room, true
	}
	if limit < 1 {
		limit = 1
	}
	if _, err := trustedInt(conn, "PRAGMA max_page_count = "+strconv.FormatInt(limit, 10)); err != nil {
		return false, apierr.FromSQLite(err)
	}
	return quotaLimited, nil
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

// column reads one value and the bytes it counts toward the result cap.
func column(stmt *sqlite3.Stmt, i int) (value.Value, int64) {
	switch stmt.ColumnType(i) {
	case sqlite3.INTEGER:
		return value.IntValue(stmt.ColumnInt64(i)), 8
	case sqlite3.FLOAT:
		return value.FloatValue(stmt.ColumnFloat(i)), 8
	case sqlite3.TEXT:
		s := stmt.ColumnText(i)
		return value.TextValue(s), int64(len(s))
	case sqlite3.BLOB:
		b := stmt.ColumnBlob(i, nil)
		return value.BlobValue(b), int64(len(b))
	}
	return value.NullValue(), 1
}
