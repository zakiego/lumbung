//go:build unix

package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLocked means another process (a running lumbungd, or a restore) holds
// the data directory.
var ErrLocked = errors.New("storage: the data folder is in use by another lumbungd or restore")

// Lock takes an exclusive lock on the data directory, so two engines never
// share one, and a restore can tell the engine is stopped. The lock lives on
// an open file and goes away with the process, even after a crash.
func (l Layout) Lock() (release func() error, err error) {
	if err := os.MkdirAll(l.Root, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(l.Root, "lumbungd.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("storage: open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("storage: lock %s: %w", l.Root, err)
	}
	return f.Close, nil
}
