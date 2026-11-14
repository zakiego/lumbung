package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// ListDatabases returns a workspace's live databases ordered by name.
func (e *Engine) ListDatabases(ctx context.Context, ws string) ([]meta.Database, error) {
	if e.refused != nil {
		return nil, e.refused
	}
	return e.meta.ListDatabases(ctx, ws)
}

// ListTrash returns a workspace's databases in the trash.
func (e *Engine) ListTrash(ctx context.Context, ws string) ([]meta.Database, error) {
	if e.refused != nil {
		return nil, e.refused
	}
	return e.meta.ListTrash(ctx, ws)
}

// GetDatabase returns a live database of a workspace.
func (e *Engine) GetDatabase(ctx context.Context, ws, dbRef string) (meta.Database, error) {
	return e.database(ctx, ws, dbRef)
}

// RenameDatabase changes a database's name.
func (e *Engine) RenameDatabase(ctx context.Context, ws, dbRef, name string) (meta.Database, error) {
	d, err := e.database(ctx, ws, dbRef)
	if err != nil {
		return meta.Database{}, err
	}
	return e.meta.RenameDatabase(ctx, d.ID, name)
}

// DeleteDatabase moves a database to the trash (PLAN 6.6). Its branches stop
// answering at once; it can be restored until the trash retention passes.
func (e *Engine) DeleteDatabase(ctx context.Context, ws, dbRef string) (meta.Database, error) {
	e.trashMu.Lock()
	defer e.trashMu.Unlock()
	d, err := e.database(ctx, ws, dbRef)
	if err != nil {
		return meta.Database{}, err
	}
	branches, err := e.meta.ListBranches(ctx, d.ID)
	if err != nil {
		return meta.Database{}, err
	}
	// Hold every branch alone so no request is mid-flight while the folder
	// moves.
	ctx2, cancel := context.WithTimeout(ctx, maintenanceTimeout)
	defer cancel()
	var held []*gate
	defer func() {
		for _, g := range held {
			g.release()
		}
	}()
	for _, b := range branches {
		g := e.gates.get(b.ID)
		if err := g.exclusiveWith(ctx2, func() { e.abortTxs(b.ID) }); err != nil {
			return meta.Database{}, apierr.New(apierr.Busy, "timed out waiting for requests on the database to finish")
		}
		held = append(held, g)
	}
	for _, b := range branches {
		e.dropPool(b.ID)
	}
	entry := e.layout.TrashEntry(d.ID)
	if err := os.MkdirAll(entry, 0o700); err != nil {
		return meta.Database{}, err
	}
	if err := moveIfExists(e.layout.DatabaseDir(d.ID), filepath.Join(entry, "dbs")); err != nil {
		return meta.Database{}, err
	}
	if err := moveIfExists(e.layout.SnapshotDir(d.ID), filepath.Join(entry, "snapshots")); err != nil {
		_ = moveIfExists(filepath.Join(entry, "dbs"), e.layout.DatabaseDir(d.ID))
		return meta.Database{}, err
	}
	if err := e.meta.SoftDeleteDatabase(ctx, d.ID, time.Now().Add(e.cfg.TrashRetention)); err != nil {
		// Put the folders back so the database keeps working.
		_ = moveIfExists(filepath.Join(entry, "dbs"), e.layout.DatabaseDir(d.ID))
		_ = moveIfExists(filepath.Join(entry, "snapshots"), e.layout.SnapshotDir(d.ID))
		return meta.Database{}, err
	}
	for _, b := range branches {
		e.forgetSize(b.ID)
	}
	return e.meta.GetDeletedDatabase(ctx, ws, d.ID)
}

// RestoreDatabase takes a database out of the trash. An empty name keeps the
// old one, which fails if another database took it meanwhile.
func (e *Engine) RestoreDatabase(ctx context.Context, ws, dbRef, name string) (meta.Database, error) {
	if e.refused != nil {
		return meta.Database{}, e.refused
	}
	e.trashMu.Lock()
	defer e.trashMu.Unlock()
	d, err := e.meta.GetDeletedDatabase(ctx, ws, dbRef)
	if err != nil {
		return meta.Database{}, err
	}
	if name == "" {
		name = d.Name
	}
	if _, err := e.meta.GetDatabase(ctx, ws, name); err == nil {
		return meta.Database{}, apierr.Newf(apierr.NameTaken, "a database named %q already exists; restore it under another name", name)
	}
	entry := e.layout.TrashEntry(d.ID)
	need := dirBytes(filepath.Join(entry, "dbs"))
	if err := e.reserveBytes(ctx, d.WorkspaceID, need); err != nil {
		return meta.Database{}, err
	}
	defer e.releaseBytes(d.WorkspaceID, need)
	if err := moveIfExists(filepath.Join(entry, "dbs"), e.layout.DatabaseDir(d.ID)); err != nil {
		return meta.Database{}, err
	}
	if err := moveIfExists(filepath.Join(entry, "snapshots"), e.layout.SnapshotDir(d.ID)); err != nil {
		_ = moveIfExists(e.layout.DatabaseDir(d.ID), filepath.Join(entry, "dbs"))
		return meta.Database{}, err
	}
	restored, err := e.meta.RestoreDatabase(ctx, d.ID, name)
	if err != nil {
		_ = moveIfExists(e.layout.DatabaseDir(d.ID), filepath.Join(entry, "dbs"))
		_ = moveIfExists(e.layout.SnapshotDir(d.ID), filepath.Join(entry, "snapshots"))
		return meta.Database{}, err
	}
	_ = os.Remove(entry)
	branches, err := e.meta.ListBranches(ctx, d.ID)
	if err != nil {
		return meta.Database{}, err
	}
	for _, b := range branches {
		e.recordSize(d.WorkspaceID, b.ID, fileBytes(e.layout.BranchFile(d.ID, b.ID)))
	}
	return restored, nil
}

// purgeTrash removes databases whose trash retention has passed.
func (e *Engine) purgeTrash(ctx context.Context, now time.Time) {
	due, err := e.meta.PurgeDue(ctx, now)
	if err != nil {
		e.log.Error("list trash to purge", "err", err)
		return
	}
	for _, d := range due {
		e.purgeOne(ctx, d.ID)
	}
}

// purgeOne removes a trashed database for good: records first, in a
// transaction that confirms it is still trashed, then its files.
func (e *Engine) purgeOne(ctx context.Context, dbID string) {
	e.trashMu.Lock()
	defer e.trashMu.Unlock()
	purged, err := e.meta.PurgeDatabase(ctx, dbID)
	if err != nil {
		e.log.Error("purge database records", "db", dbID, "err", err)
		return
	}
	if !purged {
		return // restored meanwhile
	}
	if err := os.RemoveAll(e.layout.TrashEntry(dbID)); err != nil {
		e.log.Error("purge database files", "db", dbID, "err", err)
	}
}

// dirBytes sums the sizes of files directly in dir.
func dirBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, en := range entries {
		if info, err := en.Info(); err == nil && !en.IsDir() {
			n += info.Size()
		}
	}
	return n
}

func moveIfExists(from, to string) error {
	if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
