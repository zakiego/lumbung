//go:build unix

package storage

import "golang.org/x/sys/unix"

// FreeBytes returns the bytes available to this process on the file system
// that holds path.
func FreeBytes(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
