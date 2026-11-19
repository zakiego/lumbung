package sqlitex

import (
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/ncruces/go-sqlite3"
)

// The driver panics instead of returning an error when SQLite runs out of
// memory: every call that reads a result code (Step, Exec, Close, ...) panics
// with "sqlite3: out of memory" on SQLITE_NOMEM. A connection runs SQLite in
// its own WebAssembly memory capped at Limits.MaxMemoryBytes, so one
// statement that needs more than the cap (a GROUP BY or ORDER BY that holds
// large values in its sorter, say) reaches it. Guard turns that panic back
// into an error and marks the connection broken, so the pool closes it
// instead of reusing it. Closing also gives its memory back: WebAssembly
// memory grows but never shrinks while the connection is open.

// oomPanic is the message the driver panics with on SQLITE_NOMEM
// (internal/errutil.OOMErr in github.com/ncruces/go-sqlite3).
const oomPanic = "sqlite3: out of memory"

// OutOfMemoryError is what Guard returns when SQLite ran out of memory. It
// matches sqlite3.NOMEM with errors.Is.
type OutOfMemoryError struct {
	Limit int64 // the connection's memory ceiling in bytes, 0 if unknown
}

func (e *OutOfMemoryError) Error() string {
	if e.Limit > 0 {
		return fmt.Sprintf("the statement needed more than the %d MiB of memory one connection may use", e.Limit>>20)
	}
	return "the statement needed more memory than one connection may use"
}

// Is reports a match with sqlite3.NOMEM.
func (e *OutOfMemoryError) Is(target error) bool { return target == sqlite3.NOMEM }

// PanicError is what Guard returns for any other panic while running SQL.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("sqlitex: panic: %v", e.Value) }

// ErrBroken is returned by Reset for a connection that panicked: it must be
// closed, not reused.
var ErrBroken = errors.New("sqlitex: connection broken by a panic")

// Guard runs f and turns a panic in it into an error: an
// *OutOfMemoryError for SQLite running out of memory, a *PanicError for
// anything else. Either way the connection is marked broken.
func (c *Conn) Guard(f func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			c.broken = true
			err = fromPanic(v, c.maxMemory)
		}
	}()
	return f()
}

// Broken reports whether a panic went through the connection. A broken
// connection must be closed.
func (c *Conn) Broken() bool { return c.broken }

// Recover turns a panic into an error in *err, as Guard does, for code that
// holds a connection only for the call and closes it after. Defer it
// directly: defer sqlitex.Recover(&err).
func Recover(err *error) {
	if v := recover(); v != nil {
		*err = fromPanic(v, 0)
	}
}

// IsOutOfMemory reports whether a recovered panic value is the driver's
// out-of-memory panic.
func IsOutOfMemory(v any) bool {
	e, ok := v.(error)
	return ok && e.Error() == oomPanic
}

func fromPanic(v any, limit int64) error {
	if IsOutOfMemory(v) {
		return &OutOfMemoryError{Limit: limit}
	}
	return &PanicError{Value: v, Stack: debug.Stack()}
}
