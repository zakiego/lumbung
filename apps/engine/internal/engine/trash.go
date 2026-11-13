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

// ListDatabases returns live databases ordered by name.
func (e *Engine) ListDatabases(ctx context.Context) ([]meta.Database, error) {
	if e.refused != nil {
		return nil, e.refused
	}
	return e.meta.ListDatabases(ctx)
}

// ListTrash returns databases in the trash.
func (e *Engine) ListTrash(ctx context.Context) ([]meta.Database, error) {
	if e.refused != nil {
		return nil, e.refused
	}
	return e.meta.ListTrash(ctx)
}

// GetDatabase returns a live database.
func (e *Engine) GetDatabase(ctx context.Context, dbRef string) (meta.Database, error) {
	return e.database(ctx, dbRef)
}

// RenameDatabase changes a database's name.
func (e *Engine) RenameDatabase(ctx context.Context, dbRef, name string) (meta.Database, error) {
	d, err := e.database(ctx, dbRef)
	if err != nil {
		return meta.Database{}, err
	}
	return e.meta.RenameDatabase(ctx, d.ID, name)
}

// DeleteDatabase moves a database to the trash (PLAN 6.6). Its branches stop
// answering at once; it can be restored until the trash retention passes.
func (e *Engine) DeleteDatabase(ctx context.Context, dbRef string) (meta.Database, error) {
	d, err := e.database(ctx, dbRef)
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
		if err := g.exclusive(ctx2); err != nil {
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
	return e.meta.GetDeletedDatabase(ctx, d.ID)
}

// RestoreDatabase takes a database out of the trash. An empty name keeps the
// old one, which fails if another database took it meanwhile.
func (e *Engine) RestoreDatabase(ctx context.Context, dbRef, name string) (meta.Database, error) {
	if e.refused != nil {
		return meta.Database{}, e.refused
	}
	d, err := e.meta.GetDeletedDatabase(ctx, dbRef)
	if err != nil {
		return meta.Database{}, err
	}
	if name == "" {
		name = d.Name
	}
	if _, err := e.meta.GetDatabase(ctx, name); err == nil {
		return meta.Database{}, apierr.Newf(apierr.NameTaken, "a database named %q already exists; restore it under another name", name)
	}
	entry := e.layout.TrashEntry(d.ID)
	if err := moveIfExists(filepath.Join(entry, "dbs"), e.layout.DatabaseDir(d.ID)); err != nil {
		return meta.Database{}, err
	}
	if err := moveIfExists(filepath.Join(entry, "snapshots"), e.layout.SnapshotDir(d.ID)); err != nil {
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
		e.recordSize(b.ID, fileBytes(e.layout.BranchFile(d.ID, b.ID)))
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
		if err := os.RemoveAll(e.layout.TrashEntry(d.ID)); err != nil {
			e.log.Error("purge database files", "db", d.ID, "err", err)
			continue
		}
		if err := e.meta.PurgeDatabase(ctx, d.ID); err != nil {
			e.log.Error("purge database records", "db", d.ID, "err", err)
		}
	}
}

func moveIfExists(from, to string) error {
	if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
