// Package storage knows where files live under the data directory
// (PLAN 5, Storage). Every path is built from opaque ids, never from names a
// user chose, so renames are free and path traversal can't happen.
package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zakiego/lumbung/apps/engine/internal/ids"
)

// Layout is the data directory.
type Layout struct {
	Root string // absolute
}

// New returns the layout for root, made absolute.
func New(root string) (Layout, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Layout{}, err
	}
	return Layout{Root: abs}, nil
}

// Ensure creates the directory tree with owner-only permissions.
func (l Layout) Ensure() error {
	for _, d := range []string{l.Root, l.DBsDir(), l.SnapshotsDir(), l.TrashDir(), l.SecretsDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("storage: create %s: %w", d, err)
		}
	}
	return nil
}

func (l Layout) Meta() string         { return filepath.Join(l.Root, "meta.db") }
func (l Layout) DBsDir() string       { return filepath.Join(l.Root, "dbs") }
func (l Layout) SnapshotsDir() string { return filepath.Join(l.Root, "snapshots") }
func (l Layout) TrashDir() string     { return filepath.Join(l.Root, "trash") }
func (l Layout) SecretsDir() string   { return filepath.Join(l.Root, "secrets") }

// DatabaseDir is the folder holding a database's branch files.
func (l Layout) DatabaseDir(dbID string) string {
	mustID(dbID, ids.Database)
	return filepath.Join(l.DBsDir(), dbID)
}

// BranchFile is the SQLite file of one branch.
func (l Layout) BranchFile(dbID, branchID string) string {
	mustID(branchID, ids.Branch)
	return filepath.Join(l.DatabaseDir(dbID), branchID+".db")
}

// SnapshotFile is the SQLite file of one snapshot.
func (l Layout) SnapshotFile(dbID, snapshotID string) string {
	mustID(dbID, ids.Database)
	mustID(snapshotID, ids.Snapshot)
	return filepath.Join(l.SnapshotsDir(), dbID, snapshotID+".db")
}

// BranchFiles lists a branch file and the companions SQLite may create.
func BranchFiles(path string) []string {
	return []string{path, path + "-wal", path + "-shm", path + "-journal"}
}

// mustID panics on anything but a well-formed id. Paths are only ever built
// from ids the engine made, so a bad one is a bug, not user input.
func mustID(id, prefix string) {
	if !ids.IsID(id, prefix) {
		panic(fmt.Sprintf("storage: %q isn't a %s id", id, prefix))
	}
}
