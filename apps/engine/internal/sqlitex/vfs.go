package sqlitex

import (
	"fmt"
	"path/filepath"
	"sync/atomic"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/vfs"
)

// confinedVFS wraps the default VFS for one branch connection. It opens,
// deletes and reports on only that branch's own files: the database file and
// its -journal and -wal companions. The -shm file is mapped by the opened
// database file itself, not opened through the VFS. Anonymous temp files are
// refused too, since user connections keep temp storage in memory.
//
// This is the last line of defense under the authorizer and the attach limit:
// even if a statement got past both, it couldn't touch another file.
type confinedVFS struct {
	base    vfs.VFSFilename
	allowed map[string]bool
}

var vfsSeq atomic.Uint64

// registerConfinedVFS registers a confined VFS for the database file at path
// and returns its name. The caller unregisters it after closing the
// connection. path must be absolute.
func registerConfinedVFS(path string) (string, error) {
	base, ok := vfs.Find("").(vfs.VFSFilename)
	if !ok {
		return "", fmt.Errorf("sqlitex: default VFS doesn't open by filename")
	}
	// The default VFS resolves symlinks in the directory part (the file
	// itself is opened with O_NOFOLLOW), so resolve the same way here.
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("sqlitex: resolve %s: %w", path, err)
	}
	full := filepath.Join(dir, filepath.Base(path))
	v := &confinedVFS{base: base, allowed: map[string]bool{}}
	// Accept both the path as given and as the VFS resolves it (symlinked
	// temp dirs on macOS resolve differently).
	for _, p := range []string{filepath.Clean(path), full} {
		for _, suffix := range []string{"", "-journal", "-wal"} {
			v.allowed[p+suffix] = true
		}
	}
	name := fmt.Sprintf("lumbung-%d", vfsSeq.Add(1))
	vfs.Register(name, v)
	return name, nil
}

func (v *confinedVFS) permit(name string) bool {
	return v.allowed[name] || v.allowed[filepath.Clean(name)]
}

var errConfined = sqlite3.CANTOPEN

func (v *confinedVFS) Open(name string, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	if name == "" || !v.permit(name) {
		return nil, flags, errConfined
	}
	return v.base.Open(name, flags)
}

func (v *confinedVFS) OpenFilename(name *vfs.Filename, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	if name == nil || !v.permit(name.String()) {
		return nil, flags, errConfined
	}
	return v.base.OpenFilename(name, flags)
}

func (v *confinedVFS) Delete(name string, syncDir bool) error {
	if !v.permit(name) {
		return sqlite3.IOERR_DELETE
	}
	return v.base.Delete(name, syncDir)
}

// Access answers "doesn't exist" for anything outside the branch, so a
// statement can't probe the file system either.
func (v *confinedVFS) Access(name string, flags vfs.AccessFlag) (bool, error) {
	if !v.permit(name) {
		return false, nil
	}
	return v.base.Access(name, flags)
}

func (v *confinedVFS) FullPathname(name string) (string, error) {
	return v.base.FullPathname(name)
}
