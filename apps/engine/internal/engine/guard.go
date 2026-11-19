package engine

import (
	"errors"

	"github.com/zakiego/lumbung/apps/engine/internal/sqlitex"
)

// logPanic records a panic Tx.guard turned into an error. Running out of
// memory is a statement asking too much and is a warning; anything else is
// a bug and is logged with its stack.
func (e *Engine) logPanic(err error, branchID string) {
	var pe *sqlitex.PanicError
	if errors.As(err, &pe) {
		e.log.Error("panic running SQL, closing the connection", "branch", branchID, "panic", pe.Value, "stack", string(pe.Stack))
		return
	}
	e.log.Warn("statement ran out of memory, closing the connection", "branch", branchID, "err", err)
}
