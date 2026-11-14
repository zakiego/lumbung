package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/sqlitex"
)

// BranchSource says what a new branch copies. Set at most one field; with
// neither, the branch copies the database's default branch.
type BranchSource struct {
	Branch   string // name or id
	Snapshot string // id
}

// NewBranchOptions are optional settings for CreateBranch.
type NewBranchOptions struct {
	Protected bool
	ExpiresIn time.Duration // 0: never expires
}

// ListBranches returns a database's branches, oldest first.
func (e *Engine) ListBranches(ctx context.Context, dbRef string) ([]meta.Branch, error) {
	d, err := e.database(ctx, dbRef)
	if err != nil {
		return nil, err
	}
	return e.meta.ListBranches(ctx, d.ID)
}

// GetBranch returns one branch.
func (e *Engine) GetBranch(ctx context.Context, dbRef, branchRef string) (meta.Branch, error) {
	tg, err := e.resolve(ctx, dbRef, branchRef)
	return tg.branch, err
}

func (e *Engine) database(ctx context.Context, dbRef string) (meta.Database, error) {
	if e.refused != nil {
		return meta.Database{}, e.refused
	}
	return e.meta.GetDatabase(ctx, dbRef)
}

// CreateBranch creates a branch as a full copy of a branch or a snapshot.
func (e *Engine) CreateBranch(ctx context.Context, dbRef, name string, src BranchSource, opts NewBranchOptions) (meta.Branch, error) {
	d, err := e.database(ctx, dbRef)
	if err != nil {
		return meta.Branch{}, err
	}
	if !ids.ValidName(name) {
		return meta.Branch{}, apierr.Newf(apierr.InvalidName, "invalid branch name %q", name)
	}
	if src.Branch != "" && src.Snapshot != "" {
		return meta.Branch{}, apierr.New(apierr.BadRequest, "copy from a branch or a snapshot, not both")
	}
	if _, err := e.meta.GetBranch(ctx, d.ID, name); err == nil {
		return meta.Branch{}, apierr.Newf(apierr.NameTaken, "a branch named %q already exists", name)
	}
	n, err := e.meta.CountBranches(ctx, d.ID)
	if err != nil {
		return meta.Branch{}, err
	}
	if n >= e.cfg.MaxBranches {
		return meta.Branch{}, apierr.Newf(apierr.LimitReached, "the database has the most branches allowed (%d)", e.cfg.MaxBranches)
	}
	if err := e.checkDisk(); err != nil {
		return meta.Branch{}, err
	}

	nb := meta.NewBranch{ID: ids.New(ids.Branch), DatabaseID: d.ID, Name: name, Protected: opts.Protected}
	if opts.ExpiresIn > 0 {
		at := time.Now().Add(opts.ExpiresIn)
		nb.ExpiresAt = &at
	}
	var srcPath string
	var parentTarget *target
	if src.Snapshot != "" {
		sn, err := e.meta.GetSnapshot(ctx, d.ID, src.Snapshot)
		if err != nil {
			return meta.Branch{}, err
		}
		nb.SourceSnapshotID = sn.ID
		if _, err := e.meta.GetBranch(ctx, d.ID, sn.BranchID); err == nil {
			nb.ParentID = sn.BranchID
		}
		srcPath = e.layout.SnapshotFile(d.ID, sn.ID)
	} else {
		ref := src.Branch
		if ref == "" {
			ref = d.DefaultBranchID
		}
		parent, err := e.meta.GetBranch(ctx, d.ID, ref)
		if err != nil {
			return meta.Branch{}, err
		}
		nb.ParentID = parent.ID
		parentTarget = &target{db: d, branch: parent, path: e.layout.BranchFile(d.ID, parent.ID)}
	}

	dst := e.layout.BranchFile(d.ID, nb.ID)
	var srcSize int64
	if parentTarget != nil {
		srcSize = fileBytes(parentTarget.path)
	} else {
		srcSize = fileBytes(srcPath)
	}
	if err := e.reserveBytes(srcSize); err != nil {
		return meta.Branch{}, err
	}
	defer e.releaseBytes(srcSize)
	if parentTarget != nil {
		err = e.copyBranch(ctx, *parentTarget, dst)
	} else if _, cerr := e.copyDB(ctx, srcPath, dst); cerr != nil {
		err = apierr.FromSQLite(cerr)
	}
	if err != nil {
		return meta.Branch{}, err
	}
	size := fileBytes(dst)
	b, err := e.meta.CreateBranch(ctx, nb)
	if err != nil {
		_ = removeBranchFiles(dst)
		return meta.Branch{}, err
	}
	e.recordSize(b.ID, size)
	return b, nil
}

// BranchChanges are the editable fields of a branch.
type BranchChanges = meta.BranchUpdate

// UpdateBranch renames a branch or changes its protection or expiry.
func (e *Engine) UpdateBranch(ctx context.Context, dbRef, branchRef string, u BranchChanges) (meta.Branch, error) {
	tg, err := e.resolve(ctx, dbRef, branchRef)
	if err != nil {
		return meta.Branch{}, err
	}
	return e.meta.UpdateBranch(ctx, tg.db.ID, tg.branch.ID, u)
}

// DeleteBranch removes a branch and its file. Protected branches, including
// the default branch, can't be deleted. Its snapshots are kept.
func (e *Engine) DeleteBranch(ctx context.Context, dbRef, branchRef string) error {
	tg, err := e.resolve(ctx, dbRef, branchRef)
	if err != nil {
		return err
	}
	if err := unprotected(tg); err != nil {
		return err
	}
	return e.withExclusive(ctx, tg, unprotected, func(cur target) error {
		e.dropPool(cur.branch.ID)
		if err := e.meta.DeleteSchedule(ctx, cur.branch.ID); err != nil {
			return err
		}
		if err := e.meta.DeleteBranch(ctx, cur.branch.ID); err != nil {
			return err
		}
		e.forgetSize(cur.branch.ID)
		return removeBranchFiles(cur.path)
	})
}

// unprotected refuses a protected branch or the default branch.
func unprotected(tg target) error {
	if tg.branch.Protected || tg.branch.ID == tg.db.DefaultBranchID {
		return apierr.Newf(apierr.BranchProtected, "branch %q is protected", tg.branch.Name)
	}
	return nil
}

// ResetBranch replaces a branch's content with a fresh copy of what it was
// made from: its source snapshot, or else its parent branch.
func (e *Engine) ResetBranch(ctx context.Context, dbRef, branchRef string) (meta.Branch, error) {
	tg, err := e.resolve(ctx, dbRef, branchRef)
	if err != nil {
		return meta.Branch{}, err
	}
	if err := unprotected(tg); err != nil {
		return meta.Branch{}, err
	}
	var srcPath string
	var parentTarget *target
	switch {
	case tg.branch.SourceSnapshotID != "":
		if _, err := e.meta.GetSnapshot(ctx, tg.db.ID, tg.branch.SourceSnapshotID); err != nil {
			return meta.Branch{}, apierr.New(apierr.ParentGone, "the snapshot this branch was made from was deleted")
		}
		srcPath = e.layout.SnapshotFile(tg.db.ID, tg.branch.SourceSnapshotID)
	case tg.branch.ParentID != "":
		parent, err := e.meta.GetBranch(ctx, tg.db.ID, tg.branch.ParentID)
		if err != nil {
			return meta.Branch{}, apierr.New(apierr.ParentGone, "the branch this branch was made from was deleted")
		}
		parentTarget = &target{db: tg.db, branch: parent, path: e.layout.BranchFile(tg.db.ID, parent.ID)}
	default:
		return meta.Branch{}, apierr.New(apierr.ParentGone, "this branch has no parent to reset from")
	}
	if err := e.checkDisk(); err != nil {
		return meta.Branch{}, err
	}
	// Copy first, outside the gate, so requests on the branch only wait for
	// the file swap.
	staged := tempName(tg.path)
	defer func() { _ = removeBranchFiles(staged) }()
	srcSize := fileBytes(srcPath)
	if parentTarget != nil {
		srcSize = fileBytes(parentTarget.path)
	}
	if err := e.reserveBytes(srcSize); err != nil {
		return meta.Branch{}, err
	}
	defer e.releaseBytes(srcSize)
	if parentTarget != nil {
		if err := e.copyBranch(ctx, *parentTarget, staged); err != nil {
			return meta.Branch{}, err
		}
	} else if _, err := e.copyDB(ctx, srcPath, staged); err != nil {
		return meta.Branch{}, apierr.FromSQLite(err)
	}
	if err := e.swapBranchFile(ctx, tg, staged, unprotected); err != nil {
		return meta.Branch{}, err
	}
	if err := e.meta.TouchBranch(ctx, tg.branch.ID); err != nil {
		return meta.Branch{}, err
	}
	return e.meta.GetBranch(ctx, tg.db.ID, tg.branch.ID)
}

// swapBranchFile replaces a branch's file with a prepared one, holding the
// branch alone so no request sees the swap half done. recheck runs on the
// branch as it is once the gate is held.
//
// The staged file is synced, then renamed over the old file, which is atomic:
// after a crash the branch has either its old content or the new one. The old
// file's -wal and -shm go first, once its connections are closed (closing
// checkpoints them), so the new file never meets a stale log.
func (e *Engine) swapBranchFile(ctx context.Context, tg target, staged string, recheck func(target) error) error {
	if err := syncFile(staged); err != nil {
		return err
	}
	return e.withExclusive(ctx, tg, recheck, func(cur target) error {
		e.dropPool(cur.branch.ID)
		for _, p := range []string{cur.path + "-wal", cur.path + "-shm", cur.path + "-journal"} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := os.Rename(staged, cur.path); err != nil {
			return err
		}
		if err := syncDir(filepath.Dir(cur.path)); err != nil {
			return err
		}
		e.recordSize(cur.branch.ID, fileBytes(cur.path))
		return nil
	})
}

// withExclusive runs f holding the branch's gate alone. It waits for
// requests in flight, at most maintenanceTimeout. Once the gate is held it
// looks the branch up again, since it may have been deleted, renamed,
// promoted or protected while this waited, and runs recheck on that fresh
// state before f.
func (e *Engine) withExclusive(ctx context.Context, tg target, recheck func(target) error, f func(target) error) error {
	ctx, cancel := context.WithTimeout(ctx, maintenanceTimeout)
	defer cancel()
	// The file is about to be replaced or removed, so an interactive
	// transaction left open on it would only hold the gate up. Roll them back
	// before every attempt, since one can begin during a priority break.
	g := e.gates.get(tg.branch.ID)
	if err := g.exclusiveWith(ctx, func() { e.abortTxs(tg.branch.ID) }); err != nil {
		return apierr.New(apierr.Busy, "timed out waiting for requests on the branch to finish")
	}
	defer g.release()
	cur, err := e.resolve(ctx, tg.db.ID, tg.branch.ID)
	if err != nil {
		return err
	}
	if recheck != nil {
		if err := recheck(cur); err != nil {
			return err
		}
	}
	return f(cur)
}

// PromoteBranch makes a branch the database's default. The old default
// stays as an ordinary branch and keeps its protection.
func (e *Engine) PromoteBranch(ctx context.Context, dbRef, branchRef string) (meta.Branch, error) {
	tg, err := e.resolve(ctx, dbRef, branchRef)
	if err != nil {
		return meta.Branch{}, err
	}
	// Hold the branch so a delete or reset waiting on it sees the new default
	// when it rechecks.
	err = e.withExclusive(ctx, tg, nil, func(cur target) error {
		return e.meta.SetDefaultBranch(ctx, cur.db.ID, cur.branch.ID)
	})
	if err != nil {
		return meta.Branch{}, err
	}
	return e.meta.GetBranch(ctx, tg.db.ID, tg.branch.ID)
}

// Schema returns a branch's schema.
func (e *Engine) Schema(ctx context.Context, t Target) (*Schema, error) {
	var sc *Schema
	err := e.session(ctx, t, Read, e.cfg.StatementTimeout, func(conn *sqlitex.Conn, _ target) error {
		var err error
		sc, err = readSchema(conn)
		return err
	})
	return sc, err
}

// DiffBranches reports how branch's schema differs from against's.
func (e *Engine) DiffBranches(ctx context.Context, dbRef, branchRef, againstRef string) (*Diff, error) {
	a, err := e.resolve(ctx, dbRef, againstRef)
	if err != nil {
		return nil, err
	}
	b, err := e.resolve(ctx, dbRef, branchRef)
	if err != nil {
		return nil, err
	}
	base, err := e.Schema(ctx, Target{Database: a.db.ID, Branch: a.branch.ID})
	if err != nil {
		return nil, err
	}
	head, err := e.Schema(ctx, Target{Database: b.db.ID, Branch: b.branch.ID})
	if err != nil {
		return nil, err
	}
	return &Diff{
		Branch:    b.branch.Name,
		Against:   a.branch.Name,
		Changes:   diffSchemas(base, head),
		BranchDDL: head.DDL(),
		BaseDDL:   base.DDL(),
	}, nil
}

// deleteExpiredBranches removes unprotected branches past their expiry.
func (e *Engine) deleteExpiredBranches(ctx context.Context) {
	expired, err := e.meta.ExpiredBranches(ctx, time.Now())
	if err != nil {
		e.log.Error("list expired branches", "err", err)
		return
	}
	for _, b := range expired {
		if err := e.DeleteBranch(ctx, b.DatabaseID, b.ID); err != nil && !errors.Is(err, context.Canceled) {
			e.log.Error("delete expired branch", "branch", b.ID, "err", err)
		}
	}
}
