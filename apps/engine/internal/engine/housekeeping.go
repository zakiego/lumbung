package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
)

// reserveBytes claims room in the instance quota for a whole-file copy
// (branch, reset, restore from the trash). Release it once the copy's size
// is recorded.
func (e *Engine) reserveBytes(n int64) error {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	if e.total+e.reserved+n > e.cfg.MaxInstanceBytes {
		return apierr.Newf(apierr.InstanceQuotaExceeded, "a copy of %d bytes would pass the %d byte instance quota", n, e.cfg.MaxInstanceBytes)
	}
	e.reserved += n
	return nil
}

func (e *Engine) releaseBytes(n int64) {
	e.usageMu.Lock()
	e.reserved -= n
	e.usageMu.Unlock()
}

// removeOrphans deletes branch and snapshot files meta.db doesn't know: a
// crash between copying a file and recording it leaves one behind. Only
// files and folders named like engine ids are touched.
func (e *Engine) removeOrphans(ctx context.Context) error {
	branches, err := e.meta.ListLiveBranches(ctx)
	if err != nil {
		return err
	}
	liveDB := map[string]bool{}
	liveBranch := map[string]bool{}
	for _, b := range branches {
		liveDB[b.DatabaseID] = true
		liveBranch[b.ID] = true
	}
	dbDirs, _ := os.ReadDir(e.layout.DBsDir())
	for _, d := range dbDirs {
		if !d.IsDir() || !ids.IsID(d.Name(), ids.Database) {
			continue
		}
		dir := filepath.Join(e.layout.DBsDir(), d.Name())
		if !liveDB[d.Name()] {
			// No live database owns this folder (trashed ones live in trash/).
			if _, err := e.meta.DeletedDatabaseByID(ctx, d.Name()); err != nil {
				e.log.Warn("removing orphan database folder", "path", dir)
				_ = os.RemoveAll(dir)
			}
			continue
		}
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			id := branchIDOf(f.Name())
			if id != "" && !liveBranch[id] {
				e.log.Warn("removing orphan branch file", "path", filepath.Join(dir, f.Name()))
				_ = os.Remove(filepath.Join(dir, f.Name()))
			}
		}
	}

	snapDirs, _ := os.ReadDir(e.layout.SnapshotsDir())
	for _, d := range snapDirs {
		if !d.IsDir() || !ids.IsID(d.Name(), ids.Database) {
			continue
		}
		known := map[string]bool{}
		snaps, err := e.meta.ListSnapshots(ctx, d.Name(), "")
		if err != nil {
			return err
		}
		for _, sn := range snaps {
			known[sn.ID] = true
		}
		dir := filepath.Join(e.layout.SnapshotsDir(), d.Name())
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			id := strings.TrimSuffix(f.Name(), ".db")
			if ids.IsID(id, ids.Snapshot) && strings.HasSuffix(f.Name(), ".db") && !known[id] {
				e.log.Warn("removing orphan snapshot file", "path", filepath.Join(dir, f.Name()))
				_ = os.Remove(filepath.Join(dir, f.Name()))
			}
		}
	}
	return nil
}

// branchIDOf returns the branch id a branch file or companion is named for,
// or "" for anything else.
func branchIDOf(name string) string {
	for _, suffix := range []string{".db", ".db-wal", ".db-shm", ".db-journal"} {
		if id := strings.TrimSuffix(name, suffix); id != name && ids.IsID(id, ids.Branch) {
			return id
		}
	}
	return ""
}
