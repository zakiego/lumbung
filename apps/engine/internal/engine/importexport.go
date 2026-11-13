package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ncruces/go-sqlite3"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/sqlitex"
)

// ImportFormat is what an import uploads.
type ImportFormat int

const (
	ImportDB  ImportFormat = iota + 1 // a SQLite database file
	ImportSQL                         // a SQL dump
)

// ImportMode says what may happen to existing content.
type ImportMode int

const (
	// ImportIntoEmpty refuses a branch that already has tables.
	ImportIntoEmpty ImportMode = iota
	// ImportReplace throws the branch's content away. Not allowed on a
	// protected branch.
	ImportReplace
)

var sqliteHeader = []byte("SQLite format 3\x00")

// Import replaces a branch's content with an uploaded database file or SQL
// dump (PLAN 6.7). The upload is built into a new file first and checked
// under the same isolation rules as user SQL; the branch only changes when
// everything passed.
func (e *Engine) Import(ctx context.Context, t Target, format ImportFormat, mode ImportMode, r io.Reader) error {
	tg, err := e.resolve(ctx, t.Database, t.Branch)
	if err != nil {
		return err
	}
	if mode == ImportReplace && (tg.branch.Protected || tg.branch.ID == tg.db.DefaultBranchID) {
		return apierr.Newf(apierr.BranchProtected, "branch %q is protected; import into a new branch and promote it", tg.branch.Name)
	}
	if mode == ImportIntoEmpty {
		empty, err := e.branchEmpty(ctx, t)
		if err != nil {
			return err
		}
		if !empty {
			return apierr.Newf(apierr.BranchNotEmpty, "branch %q already has tables; use mode=replace", tg.branch.Name)
		}
	}
	if err := e.checkDisk(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, e.cfg.ImportTimeout)
	defer cancel()

	staged := tempName(tg.path)
	defer func() { _ = removeBranchFiles(staged) }()
	limit := min(e.cfg.MaxImportBytes, e.cfg.MaxBranchBytes)
	switch format {
	case ImportDB:
		err = e.stageDB(ctx, r, staged, limit)
	case ImportSQL:
		err = e.stageSQL(ctx, r, staged, limit)
	default:
		err = apierr.New(apierr.BadRequest, "unknown import format")
	}
	if err != nil {
		return err
	}
	if err := checkIntegrity(ctx, staged); err != nil {
		return apierr.New(apierr.InvalidImport, "the imported database fails its integrity check").WithDetail(err.Error())
	}
	if err := e.swapBranchFile(ctx, tg, staged); err != nil {
		return err
	}
	return e.meta.TouchBranch(ctx, tg.branch.ID)
}

func (e *Engine) branchEmpty(ctx context.Context, t Target) (bool, error) {
	empty := true
	err := e.session(ctx, t, Read, e.cfg.StatementTimeout, func(conn *sqlitex.Conn, _ target) error {
		return eachRow(conn, "SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite\\_%' ESCAPE '\\'", func(s *sqlite3.Stmt) {
			empty = s.ColumnInt(0) == 0
		})
	})
	return empty, err
}

// copyLimited writes r to path, failing once more than limit bytes arrive.
func copyLimited(r io.Reader, path string, limit int64) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, apierr.Newf(apierr.ImportTooLarge, "the upload is larger than %d bytes", limit)
	}
	return n, nil
}

// stageDB checks an uploaded database file and opens it as a user
// connection: confined to its own file, defensive, trusted schema off.
func (e *Engine) stageDB(ctx context.Context, r io.Reader, staged string, limit int64) error {
	n, err := copyLimited(r, staged, limit)
	if err != nil {
		return err
	}
	head := make([]byte, len(sqliteHeader))
	f, err := os.Open(staged)
	if err != nil {
		return err
	}
	_, rerr := io.ReadFull(f, head)
	f.Close()
	if rerr != nil || !bytes.Equal(head, sqliteHeader) || n < 512 {
		return apierr.New(apierr.InvalidImport, "the file isn't a SQLite database")
	}
	conn, err := sqlitex.Open(ctx, sqlitex.Options{Path: staged, Limits: e.limits()})
	if err != nil {
		return apierr.New(apierr.InvalidImport, "the file can't be opened as a SQLite database").WithDetail(err.Error())
	}
	defer conn.Close()
	conn.SetInterrupt(ctx)
	var pages, size int64
	if err := eachRow(conn, "SELECT page_count, page_size FROM pragma_page_count, pragma_page_size", func(s *sqlite3.Stmt) {
		pages, size = s.ColumnInt64(0), s.ColumnInt64(1)
	}); err != nil {
		return apierr.New(apierr.InvalidImport, "the file can't be read as a SQLite database").WithDetail(err.Error())
	}
	if pages*size > e.cfg.MaxBranchBytes {
		return apierr.Newf(apierr.ImportTooLarge, "the database is larger than the %d byte branch limit", e.cfg.MaxBranchBytes)
	}
	return nil
}

// stageSQL runs a SQL dump into a new file under the user authorizer, with
// transaction statements turned into no-ops (the dump runs in one
// transaction the engine opens).
func (e *Engine) stageSQL(ctx context.Context, r io.Reader, staged string, limit int64) error {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return apierr.Newf(apierr.ImportTooLarge, "the upload is larger than %d bytes", limit)
	}
	conn, err := sqlitex.Open(ctx, sqlitex.Options{Path: staged, Create: true, Limits: e.limits()})
	if err != nil {
		return err
	}
	defer conn.Close()
	pageSize, err := trustedInt(conn, "PRAGMA page_size")
	if err != nil {
		return err
	}
	if _, err := trustedInt(conn, fmt.Sprintf("PRAGMA max_page_count = %d", e.cfg.MaxBranchBytes/pageSize)); err != nil {
		return err
	}
	conn.SetInterrupt(ctx)
	conn.IgnoreTransactions(true)
	if err := conn.ExecTrusted("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	sql := string(body)
	for strings.TrimSpace(sql) != "" {
		stmt, tail, err := conn.Prepare(sql)
		if err != nil {
			_ = conn.ExecTrusted("ROLLBACK")
			return importErr(err)
		}
		if stmt == nil {
			break // only comments and whitespace left
		}
		for stmt.Step() {
		}
		err = stmt.Err()
		stmt.Close()
		if err != nil {
			_ = conn.ExecTrusted("ROLLBACK")
			return importErr(err)
		}
		sql = tail
	}
	conn.IgnoreTransactions(false)
	if err := conn.ExecTrusted("COMMIT"); err != nil {
		_ = conn.ExecTrusted("ROLLBACK")
		return importErr(err)
	}
	return nil
}

func importErr(err error) error {
	ae := apierr.FromSQLite(err)
	switch ae.Code {
	case apierr.SQLError, apierr.ConstraintViolation:
		return apierr.New(apierr.InvalidImport, "the SQL dump failed to run").WithDetail(ae.Detail)
	case apierr.BranchSizeLimit:
		return apierr.New(apierr.ImportTooLarge, "the dump grows past the branch size limit").WithDetail(ae.Detail)
	}
	return ae
}

// ExportDB writes a consistent copy of a branch as a SQLite file.
func (e *Engine) ExportDB(ctx context.Context, t Target, w io.Writer) error {
	tg, err := e.resolve(ctx, t.Database, t.Branch)
	if err != nil {
		return err
	}
	if err := e.checkDisk(); err != nil {
		return err
	}
	tmp := tempName(e.layout.TmpDir() + "/export")
	defer func() { _ = removeBranchFiles(tmp) }()
	if err := e.copyBranch(ctx, tg, tmp); err != nil {
		return err
	}
	f, err := os.Open(tmp)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// ExportSQL writes a SQL dump of a branch, read in one transaction.
func (e *Engine) ExportSQL(ctx context.Context, t Target, w io.Writer) error {
	return e.session(ctx, t, Read, e.cfg.ImportTimeout, func(conn *sqlitex.Conn, _ target) error {
		return writeDump(conn, w)
	})
}

// copyBranch copies a branch's file while holding its gate shared, so a
// reset or import can't swap the file out from under the copy.
func (e *Engine) copyBranch(ctx context.Context, tg target, dst string) error {
	g := e.gates.get(tg.branch.ID)
	if err := g.share(ctx); err != nil {
		return apierr.New(apierr.Busy, "timed out waiting for the branch")
	}
	defer g.unshare()
	if _, err := copyDB(ctx, tg.path, dst); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return apierr.New(apierr.StatementTimeout, "the copy ran too long")
		}
		return apierr.FromSQLite(err)
	}
	return nil
}
