// Package sqlitex opens SQLite connections that are safe to hand user SQL.
//
// A user connection is locked down in layers (PLAN 6.2):
//
//   - an authorizer denies ATTACH, DETACH, VACUUM INTO, transaction control,
//     temp objects, pragmas off the allowlist and denied functions;
//   - the attached-database limit is zero;
//   - defensive mode is on and trusted schema is off, so views and triggers
//     can't call functions that aren't marked innocuous;
//   - temp storage is in memory, so no temp files are created;
//   - the connection's VFS only opens the branch's own files;
//   - read-only access uses a connection opened read-only, never SQL checks;
//   - each connection has a memory ceiling and runtime limits on value size,
//     statement length and bound parameters.
package sqlitex

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/ext/fts5"
	"github.com/ncruces/go-sqlite3/vfs"
)

// Limits bound what one connection may do.
type Limits struct {
	MaxValueBytes  int           // largest string or blob (SQLITE_LIMIT_LENGTH)
	MaxSQLBytes    int           // longest statement text (SQLITE_LIMIT_SQL_LENGTH)
	MaxParams      int           // most bound parameters (SQLITE_LIMIT_VARIABLE_NUMBER)
	MaxMemoryBytes int64         // memory ceiling for the connection
	BusyTimeout    time.Duration // how long to wait for a lock
}

// Options describe a user connection to one branch file.
type Options struct {
	Path     string // absolute path of the branch file
	ReadOnly bool   // open read-only: every write fails inside SQLite
	Create   bool   // create the file if missing (writer only)
	Limits   Limits
}

// Conn is a connection for user SQL. It isn't safe for concurrent use.
type Conn struct {
	*sqlite3.Conn
	vfsName  string
	readOnly bool
	trusted  bool
}

// ErrReadOnlyCreate is returned when a read-only connection is asked to
// create its file.
var ErrReadOnlyCreate = errors.New("sqlitex: a read-only connection can't create its file")

// Open opens a hardened user connection.
func Open(ctx context.Context, opts Options) (*Conn, error) {
	if !filepath.IsAbs(opts.Path) {
		return nil, fmt.Errorf("sqlitex: path must be absolute: %q", opts.Path)
	}
	if opts.ReadOnly && opts.Create {
		return nil, ErrReadOnlyCreate
	}
	vfsName, err := registerConfinedVFS(opts.Path)
	if err != nil {
		return nil, err
	}

	// The driver opens with read-write, create and URI flags; the URI's mode
	// narrows that, which SQLite allows (a mode may only be stricter than the
	// flags). OpenContext is the entry point that takes the memory ceiling.
	mode := "rw"
	switch {
	case opts.ReadOnly:
		mode = "ro"
	case opts.Create:
		mode = "rwc"
	}
	q := url.Values{"vfs": {vfsName}, "mode": {mode}}
	uri := (&url.URL{Scheme: "file", Path: opts.Path, RawQuery: q.Encode()}).String()

	if opts.Limits.MaxMemoryBytes > 0 {
		ctx = sqlite3.WithMaxMemory(ctx, opts.Limits.MaxMemoryBytes)
	}
	raw, err := sqlite3.OpenContext(ctx, uri)
	if err != nil {
		vfs.Unregister(vfsName)
		return nil, fmt.Errorf("sqlitex: open %s: %w", opts.Path, err)
	}
	c := &Conn{Conn: raw, vfsName: vfsName, readOnly: opts.ReadOnly}
	if err := c.harden(opts); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// harden applies the configuration and limits, then installs the authorizer.
// Engine pragmas run first, before user SQL can be denied anything.
func (c *Conn) harden(opts Options) error {
	// ENABLE_ATTACH_CREATE and ENABLE_ATTACH_WRITE aren't set: the driver
	// rejects them, and attach is already refused by the authorizer, the zero
	// attach limit and the confined VFS.
	for op, on := range map[sqlite3.DBConfig]bool{
		sqlite3.DBCONFIG_DEFENSIVE:             true,
		sqlite3.DBCONFIG_TRUSTED_SCHEMA:        false,
		sqlite3.DBCONFIG_ENABLE_LOAD_EXTENSION: false,
		sqlite3.DBCONFIG_WRITABLE_SCHEMA:       false,
		sqlite3.DBCONFIG_ENABLE_FTS3_TOKENIZER: false,
		sqlite3.DBCONFIG_DQS_DML:               false,
		sqlite3.DBCONFIG_DQS_DDL:               false,
		sqlite3.DBCONFIG_ENABLE_FKEY:           true,
	} {
		got, err := c.Config(op, on)
		if err != nil {
			return fmt.Errorf("sqlitex: config %d: %w", op, err)
		}
		if got != on {
			return fmt.Errorf("sqlitex: config %d stayed %v", op, got)
		}
	}

	c.Limit(sqlite3.LIMIT_ATTACHED, 0)
	if l := opts.Limits; l.MaxValueBytes > 0 {
		c.Limit(sqlite3.LIMIT_LENGTH, l.MaxValueBytes)
	}
	if l := opts.Limits; l.MaxSQLBytes > 0 {
		c.Limit(sqlite3.LIMIT_SQL_LENGTH, l.MaxSQLBytes)
	}
	if l := opts.Limits; l.MaxParams > 0 {
		c.Limit(sqlite3.LIMIT_VARIABLE_NUMBER, l.MaxParams)
	}
	if got := c.Limit(sqlite3.LIMIT_ATTACHED, -1); got != 0 {
		return fmt.Errorf("sqlitex: attached limit stayed %d", got)
	}
	if opts.Limits.BusyTimeout > 0 {
		if err := c.BusyTimeout(opts.Limits.BusyTimeout); err != nil {
			return fmt.Errorf("sqlitex: busy timeout: %w", err)
		}
	}

	setup := "PRAGMA foreign_keys = ON; PRAGMA temp_store = MEMORY;"
	if !opts.ReadOnly {
		setup += " PRAGMA synchronous = NORMAL;"
	}
	if err := c.Exec(setup); err != nil {
		return fmt.Errorf("sqlitex: setup pragmas: %w", err)
	}
	if !opts.ReadOnly {
		mode, err := c.pragmaText("PRAGMA journal_mode = WAL")
		if err != nil {
			return fmt.Errorf("sqlitex: journal mode: %w", err)
		}
		if mode != "wal" {
			return fmt.Errorf("sqlitex: journal mode is %q, want wal", mode)
		}
	}
	if err := fts5.Register(c.Conn); err != nil {
		return fmt.Errorf("sqlitex: fts5: %w", err)
	}
	return c.SetAuthorizer(func(action sqlite3.AuthorizerActionCode, arg3, arg4, schema, inner string) sqlite3.AuthorizerReturnCode {
		if c.trusted {
			return sqlite3.AUTH_OK
		}
		return authorize(action, arg3, arg4, schema, inner)
	})
}

func (c *Conn) pragmaText(sql string) (string, error) {
	stmt, _, err := c.Prepare(sql)
	if err != nil {
		return "", err
	}
	defer stmt.Close()
	var out string
	if stmt.Step() {
		out = stmt.ColumnText(0)
	}
	return out, stmt.Err()
}

// ReadOnly reports whether the connection was opened read-only.
func (c *Conn) ReadOnly() bool { return c.readOnly }

// ExecTrusted runs engine SQL that the user authorizer would refuse, such as
// BEGIN and COMMIT. Never pass it user input.
func (c *Conn) ExecTrusted(sql string) error {
	c.trusted = true
	defer func() { c.trusted = false }()
	return c.Exec(sql)
}

// PrepareTrusted prepares engine SQL past the user authorizer. Never pass it
// user input. The authorizer runs at prepare time, so the statement can then
// be stepped normally.
func (c *Conn) PrepareTrusted(sql string) (*sqlite3.Stmt, error) {
	c.trusted = true
	defer func() { c.trusted = false }()
	stmt, _, err := c.Prepare(sql)
	return stmt, err
}

// ErrDirty means a connection holds state that must not reach the next
// request, so the pool closes it instead of reusing it.
var ErrDirty = errors.New("sqlitex: connection holds temp objects")

// Reset returns the connection to its pooled state after a request: no open
// transaction, no interrupt, and the engine's per-connection settings back.
// It fails with ErrDirty if any temp object exists, as a backstop to the
// authorizer's temp rules.
func (c *Conn) Reset() error {
	c.SetInterrupt(context.Background())
	if !c.GetAutocommit() {
		if err := c.ExecTrusted("ROLLBACK"); err != nil {
			return fmt.Errorf("sqlitex: rollback on reset: %w", err)
		}
	}
	stmt, err := c.PrepareTrusted("SELECT count(*) FROM temp.sqlite_schema")
	if err != nil {
		return err
	}
	stmt.Step()
	n := stmt.ColumnInt(0)
	err = stmt.Err()
	stmt.Close()
	if err != nil {
		return err
	}
	if n != 0 {
		return ErrDirty
	}
	return c.ExecTrusted("PRAGMA foreign_keys = ON")
}

// Close closes the connection and removes its VFS.
func (c *Conn) Close() error {
	err := c.Conn.Close()
	vfs.Unregister(c.vfsName)
	return err
}
