package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/ncruces/go-sqlite3"
)

// tempName returns a path next to dst for a file being prepared. Only the
// engine names these; they never come from user input.
func tempName(dst string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return dst + ".tmp-" + hex.EncodeToString(b[:])
}

// trustedURI builds a URI for an engine-owned connection on the default VFS.
func trustedURI(path, mode string) string {
	return (&url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {mode}}.Encode()}).String()
}

// copyDB makes a consistent copy of the SQLite file src at dst with the
// online backup API (PLAN 6.5). The backup runs in one step, inside one read
// transaction on the source, so the copy holds exactly the transactions
// committed when it started, and writers in WAL mode keep going meanwhile.
// The copy is written to a temp file, checked with integrity_check, synced,
// then renamed into place, so dst either doesn't exist or is complete.
// It returns the size of the copy.
func copyDB(ctx context.Context, src, dst string) (int64, error) {
	if _, err := os.Stat(dst); err == nil {
		return 0, fmt.Errorf("engine: copy target %s exists", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return 0, err
	}
	tmp := tempName(dst)
	cleanup := func() {
		for _, p := range []string{tmp, tmp + "-journal", tmp + "-wal", tmp + "-shm"} {
			_ = os.Remove(p)
		}
	}

	// Read-write without create: a read-only connection can't create the
	// -shm a WAL source may need. Only the backup reads through it.
	conn, err := sqlite3.OpenContext(ctx, trustedURI(src, "rw"))
	if err != nil {
		return 0, fmt.Errorf("engine: open copy source: %w", err)
	}
	conn.SetInterrupt(ctx)
	err = conn.Backup("main", tmp)
	conn.Close()
	if err != nil {
		cleanup()
		return 0, fmt.Errorf("engine: backup: %w", err)
	}
	if err := checkIntegrity(ctx, tmp); err != nil {
		cleanup()
		return 0, err
	}
	if err := syncFile(tmp); err != nil {
		cleanup()
		return 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		cleanup()
		return 0, err
	}
	cleanup()
	if err := syncDir(filepath.Dir(dst)); err != nil {
		return 0, err
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// checkIntegrity runs PRAGMA integrity_check on an engine-owned file.
func checkIntegrity(ctx context.Context, path string) error {
	c, err := sqlite3.OpenContext(ctx, trustedURI(path, "rw"))
	if err != nil {
		return fmt.Errorf("engine: open for integrity check: %w", err)
	}
	defer c.Close()
	c.SetInterrupt(ctx)
	stmt, _, err := c.Prepare("PRAGMA integrity_check")
	if err != nil {
		return err
	}
	defer stmt.Close()
	var msgs []string
	for stmt.Step() {
		msgs = append(msgs, stmt.ColumnText(0))
	}
	if err := stmt.Err(); err != nil {
		return fmt.Errorf("engine: integrity check: %w", err)
	}
	if len(msgs) != 1 || msgs[0] != "ok" {
		return fmt.Errorf("engine: integrity check failed: %v", msgs)
	}
	return nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// removeBranchFiles deletes a branch file and its companions.
func removeBranchFiles(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
