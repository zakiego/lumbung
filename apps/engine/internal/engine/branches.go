package engine

import (
	"context"
	"errors"
	"os"
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
	if parentTarget != nil {
		err = e.copyBranch(ctx, *parentTarget, dst)
	} else if _, cerr := copyDB(ctx, srcPath, dst); cerr != nil {
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
	if tg.branch.Protected || tg.branch.ID == tg.db.DefaultBranchID {
		return apierr.Newf(apierr.BranchProtected, "branch %q is protected", tg.branch.Name)
	}
	return e.withExclusive(ctx, tg, func() error {
		e.dropPool(tg.branch.ID)
		if err := e.meta.DeleteSchedule(ctx, tg.branch.ID); err != nil {
			return err
		}
		if err := e.meta.DeleteBranch(ctx, tg.branch.ID); err != nil {
			return err
		}
		e.forgetSize(tg.branch.ID)
		return removeBranchFiles(tg.path)
	})
}

// ResetBranch replaces a branch's content with a fresh copy of what it was
// made from: its source snapshot, or else its parent branch.
func (e *Engine) ResetBranch(ctx context.Context, dbRef, branchRef string) (meta.Branch, error) {
	tg, err := e.resolve(ctx, dbRef, branchRef)
	if err != nil {
		return meta.Branch{}, err
	}
	if tg.branch.Protected || tg.branch.ID == tg.db.DefaultBranchID {
		return meta.Branch{}, apierr.Newf(apierr.BranchProtected, "branch %q is protected", tg.branch.Name)
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
	if parentTarget != nil {
		if err := e.copyBranch(ctx, *parentTarget, staged); err != nil {
			return meta.Branch{}, err
		}
	} else if _, err := copyDB(ctx, srcPath, staged); err != nil {
		return meta.Branch{}, apierr.FromSQLite(err)
	}
	if err := e.swapBranchFile(ctx, tg, staged); err != nil {
		return meta.Branch{}, err
	}
	if err := e.meta.TouchBranch(ctx, tg.branch.ID); err != nil {
		return meta.Branch{}, err
	}
	return e.meta.GetBranch(ctx, tg.db.ID, tg.branch.ID)
}

// swapBranchFile replaces a branch's file with a prepared one, holding the
// branch alone so no request sees the swap half done.
func (e *Engine) swapBranchFile(ctx context.Context, tg target, staged string) error {
	return e.withExclusive(ctx, tg, func() error {
		e.dropPool(tg.branch.ID)
		if err := removeBranchFiles(tg.path); err != nil {
			return err
		}
		if err := os.Rename(staged, tg.path); err != nil {
			return err
		}
		e.recordSize(tg.branch.ID, fileBytes(tg.path))
		return nil
	})
}

// withExclusive runs f holding the branch's gate alone. It waits for
// requests in flight, at most maintenanceTimeout.
func (e *Engine) withExclusive(ctx context.Context, tg target, f func() error) error {
	ctx, cancel := context.WithTimeout(ctx, maintenanceTimeout)
	defer cancel()
	g := e.gates.get(tg.branch.ID)
	if err := g.exclusive(ctx); err != nil {
		return apierr.New(apierr.Busy, "timed out waiting for requests on the branch to finish")
	}
	defer g.release()
	return f()
}

// PromoteBranch makes a branch the database's default. The old default
// stays as an ordinary branch and keeps its protection.
func (e *Engine) PromoteBranch(ctx context.Context, dbRef, branchRef string) (meta.Branch, error) {
	tg, err := e.resolve(ctx, dbRef, branchRef)
	if err != nil {
		return meta.Branch{}, err
	}
	if err := e.meta.SetDefaultBranch(ctx, tg.db.ID, tg.branch.ID); err != nil {
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
