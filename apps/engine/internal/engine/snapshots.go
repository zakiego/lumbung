package engine

import (
	"context"
	"os"
	"sort"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// CreateSnapshot copies a branch to a new snapshot (PLAN 6.6).
func (e *Engine) CreateSnapshot(ctx context.Context, t Target, label string) (meta.Snapshot, error) {
	tg, err := e.resolve(ctx, t.Workspace, t.Database, t.Branch)
	if err != nil {
		return meta.Snapshot{}, err
	}
	if len(label) > 200 {
		return meta.Snapshot{}, apierr.New(apierr.BadRequest, "a snapshot label is at most 200 bytes")
	}
	return e.snapshot(ctx, tg, meta.SnapshotManual, label)
}

func (e *Engine) snapshot(ctx context.Context, tg target, kind, label string) (meta.Snapshot, error) {
	if err := e.checkDisk(); err != nil {
		return meta.Snapshot{}, err
	}
	id := ids.New(ids.Snapshot)
	path := e.layout.SnapshotFile(tg.db.ID, id)
	if err := e.copyBranch(ctx, tg, path); err != nil {
		return meta.Snapshot{}, err
	}
	size := fileBytes(path)
	sn, err := e.meta.CreateSnapshot(ctx, meta.Snapshot{
		ID: id, DatabaseID: tg.db.ID, BranchID: tg.branch.ID, BranchName: tg.branch.Name,
		Kind: kind, Label: label, SizeBytes: size,
	})
	if err != nil {
		_ = os.Remove(path)
		return meta.Snapshot{}, err
	}
	return sn, nil
}

// ListSnapshots returns a branch's snapshots, newest first.
func (e *Engine) ListSnapshots(ctx context.Context, t Target) ([]meta.Snapshot, error) {
	tg, err := e.resolve(ctx, t.Workspace, t.Database, t.Branch)
	if err != nil {
		return nil, err
	}
	return e.meta.ListSnapshots(ctx, tg.db.ID, tg.branch.ID)
}

// GetSnapshot returns one snapshot of a database.
func (e *Engine) GetSnapshot(ctx context.Context, ws, dbRef, snapshotID string) (meta.Snapshot, error) {
	d, err := e.database(ctx, ws, dbRef)
	if err != nil {
		return meta.Snapshot{}, err
	}
	return e.meta.GetSnapshot(ctx, d.ID, snapshotID)
}

// DeleteSnapshot removes a snapshot and its file.
func (e *Engine) DeleteSnapshot(ctx context.Context, ws, dbRef, snapshotID string) error {
	sn, err := e.GetSnapshot(ctx, ws, dbRef, snapshotID)
	if err != nil {
		return err
	}
	return e.removeSnapshot(ctx, sn)
}

func (e *Engine) removeSnapshot(ctx context.Context, sn meta.Snapshot) error {
	if err := e.meta.DeleteSnapshot(ctx, sn.ID); err != nil {
		return err
	}
	if err := os.Remove(e.layout.SnapshotFile(sn.DatabaseID, sn.ID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RestoreSnapshot creates a new branch from a snapshot. It never overwrites
// an existing branch; promoting the new branch is a separate step.
func (e *Engine) RestoreSnapshot(ctx context.Context, ws, dbRef, snapshotID, branchName string) (meta.Branch, error) {
	return e.CreateBranch(ctx, ws, dbRef, branchName, BranchSource{Snapshot: snapshotID}, NewBranchOptions{})
}

// ScheduleOptions configure a branch's snapshot schedule.
type ScheduleOptions struct {
	Every     time.Duration // at least a minute
	KeepCount int           // keep the newest N scheduled snapshots; 0 keeps any number
	KeepFor   time.Duration // drop scheduled snapshots older than this; 0 keeps any age
}

// SetSchedule creates or replaces a branch's snapshot schedule.
func (e *Engine) SetSchedule(ctx context.Context, t Target, o ScheduleOptions) (meta.Schedule, error) {
	tg, err := e.resolve(ctx, t.Workspace, t.Database, t.Branch)
	if err != nil {
		return meta.Schedule{}, err
	}
	if o.Every < time.Minute {
		return meta.Schedule{}, apierr.New(apierr.BadRequest, "a schedule runs at most once a minute")
	}
	if o.KeepCount < 0 || o.KeepFor < 0 {
		return meta.Schedule{}, apierr.New(apierr.BadRequest, "retention can't be negative")
	}
	return e.meta.SetSchedule(ctx, meta.Schedule{
		BranchID: tg.branch.ID, DatabaseID: tg.db.ID, Every: o.Every.Truncate(time.Second),
		KeepCount: o.KeepCount, KeepFor: o.KeepFor.Truncate(time.Second),
	})
}

// GetSchedule returns a branch's schedule, if any.
func (e *Engine) GetSchedule(ctx context.Context, t Target) (meta.Schedule, bool, error) {
	tg, err := e.resolve(ctx, t.Workspace, t.Database, t.Branch)
	if err != nil {
		return meta.Schedule{}, false, err
	}
	return e.meta.GetSchedule(ctx, tg.branch.ID)
}

// DeleteSchedule removes a branch's schedule. Its snapshots stay.
func (e *Engine) DeleteSchedule(ctx context.Context, t Target) error {
	tg, err := e.resolve(ctx, t.Workspace, t.Database, t.Branch)
	if err != nil {
		return err
	}
	return e.meta.DeleteSchedule(ctx, tg.branch.ID)
}

// runSchedules takes due scheduled snapshots and applies retention.
func (e *Engine) runSchedules(ctx context.Context, now time.Time) {
	scheds, err := e.meta.AllSchedules(ctx)
	if err != nil {
		e.log.Error("list schedules", "err", err)
		return
	}
	for _, sc := range scheds {
		if sc.LastRunAt == nil || !now.Before(sc.LastRunAt.Add(sc.Every)) {
			tg, err := e.resolveByID(ctx, sc.DatabaseID, sc.BranchID)
			if err != nil {
				continue
			}
			if _, err := e.snapshot(ctx, tg, meta.SnapshotScheduled, "scheduled"); err != nil {
				e.log.Error("scheduled snapshot", "branch", sc.BranchID, "err", err)
				continue
			}
			if err := e.meta.MarkScheduleRun(ctx, sc.BranchID, now); err != nil {
				e.log.Error("mark schedule", "branch", sc.BranchID, "err", err)
			}
		}
		if err := e.applyRetention(ctx, sc, now); err != nil {
			e.log.Error("snapshot retention", "branch", sc.BranchID, "err", err)
		}
	}
}

// applyRetention drops scheduled snapshots past the count or age limit.
// Manual snapshots are never removed by retention.
func (e *Engine) applyRetention(ctx context.Context, sc meta.Schedule, now time.Time) error {
	all, err := e.meta.ListSnapshots(ctx, sc.DatabaseID, sc.BranchID)
	if err != nil {
		return err
	}
	var scheduled []meta.Snapshot
	for _, sn := range all {
		if sn.Kind == meta.SnapshotScheduled {
			scheduled = append(scheduled, sn)
		}
	}
	sort.Slice(scheduled, func(i, j int) bool { return scheduled[i].CreatedAt.After(scheduled[j].CreatedAt) })
	for i, sn := range scheduled {
		tooMany := sc.KeepCount > 0 && i >= sc.KeepCount
		tooOld := sc.KeepFor > 0 && now.Sub(sn.CreatedAt) > sc.KeepFor
		if tooMany || tooOld {
			if err := e.removeSnapshot(ctx, sn); err != nil {
				return err
			}
		}
	}
	return nil
}
