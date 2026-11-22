package engine

import (
	"context"
	"strconv"
	"time"
)

// ChangeWait is what WaitChange returns.
type ChangeWait struct {
	// Cursor names the branch's state now. Pass it to the next WaitChange.
	Cursor string
	// Changed is true when the branch changed since the cursor passed in.
	Changed bool
}

// WaitChange blocks until the branch changes after cursor, the timeout
// passes or ctx ends, so a client can wait for writes instead of polling
// (PLAN 7.2). A change is a commit that changed rows or the schema, or a
// reset, import, promotion or delete.
//
// An empty cursor, or a zero timeout, returns at once with the current
// cursor. A cursor from another branch, or from before a restart, differs
// from the current one and returns at once as changed. Changed can be a
// false alarm (a savepoint rolled back, a failed reset), never a miss.
//
// The wait holds nothing: not the gate, a pool or a connection. A branch
// deleted while it waits returns branch_not_found.
func (e *Engine) WaitChange(ctx context.Context, t Target, cursor string, timeout time.Duration) (ChangeWait, error) {
	tg, err := e.resolve(ctx, t.Workspace, t.Database, t.Branch)
	if err != nil {
		return ChangeWait{}, err
	}
	g := e.gates.get(tg.branch.ID)
	seq, moved := g.position()
	cur := e.cursor(tg.branch.ID, seq)
	if cursor == "" {
		return ChangeWait{Cursor: cur}, nil
	}
	if cursor != cur {
		return ChangeWait{Cursor: cur, Changed: true}, nil
	}
	if timeout <= 0 {
		return ChangeWait{Cursor: cur}, nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-moved:
	case <-timer.C:
		return ChangeWait{Cursor: cur}, nil
	case <-ctx.Done():
		return ChangeWait{Cursor: cur}, nil
	}
	// Look again, past ctx: the waiter is owed an answer, and a cancel that
	// raced the change shouldn't turn it into an error.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := e.resolve(rctx, tg.db.WorkspaceID, tg.db.ID, tg.branch.ID); err != nil {
		return ChangeWait{}, err
	}
	seq, _ = g.position()
	return ChangeWait{Cursor: e.cursor(tg.branch.ID, seq), Changed: true}, nil
}

func (e *Engine) cursor(branchID string, seq uint64) string {
	return e.boot + "." + branchID + "." + strconv.FormatUint(seq, 36)
}
