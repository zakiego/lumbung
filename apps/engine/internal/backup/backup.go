// Package backup copies a data directory to a backup folder and back
// (PLAN 12). It works while lumbungd is running: every file is copied with
// SQLite's online backup API, inside one read transaction per file, so each
// copy is consistent.
//
// A backup is a folder named by its UTC time, with a -meta suffix when it
// holds only meta.db:
//
//	<to>/20261114T020000Z/
//	  manifest.json                 what's in it, with SHA-256 sums
//	  meta.db
//	  dbs/<database id>/<branch id>.db
//	  secrets/service-token         when present
//	<to>/20261114T013000Z-meta/     meta.db only, taken before an upgrade
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"

	"github.com/zakiego/lumbung/apps/engine/internal/ids"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/sqlitex"
	"github.com/zakiego/lumbung/apps/engine/internal/storage"
)

const (
	stampLayout = "20060102T150405Z"
	metaSuffix  = "-meta"
)

// Options say what to back up.
type Options struct {
	DataDir     string
	To          string
	AllBranches bool // every branch, not only each database's default
	MetaOnly    bool // only meta.db (before an upgrade)
	Keep        int  // keep the newest N backups of this kind in To; 0 keeps all
	Limits      sqlitex.Limits
	Now         func() time.Time
}

// Manifest describes a backup.
type Manifest struct {
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	MetaVersion int       `json:"meta_version"`
	AllBranches bool      `json:"all_branches"`
	MetaOnly    bool      `json:"meta_only"`
	Files       []File    `json:"files"`
	// Skipped lists branch files that disappeared while the backup ran
	// (the branch was deleted, or its database moved to the trash).
	Skipped []string `json:"skipped,omitempty"`
}

// File is one copied file.
type File struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Bytes is the total size of the files.
func (m Manifest) Bytes() int64 {
	var n int64
	for _, f := range m.Files {
		n += f.Bytes
	}
	return n
}

// Run makes one backup and applies retention. It returns the new folder and
// its manifest. On a retention error the backup itself is complete.
func Run(ctx context.Context, o Options) (string, Manifest, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	// SQLite resolves file names itself, and a relative one doesn't resolve
	// against the working directory, so every path here is made absolute.
	to, err := filepath.Abs(o.To)
	if err != nil {
		return "", Manifest{}, err
	}
	o.To = to
	layout, err := storage.New(o.DataDir)
	if err != nil {
		return "", Manifest{}, err
	}
	if _, err := os.Stat(layout.Meta()); err != nil {
		return "", Manifest{}, fmt.Errorf("backup: no meta.db in %s: %w", layout.Root, err)
	}
	name := o.Now().UTC().Format(stampLayout)
	if o.MetaOnly {
		name += metaSuffix
	}
	final := filepath.Join(o.To, name)
	if _, err := os.Stat(final); err == nil {
		return "", Manifest{}, fmt.Errorf("backup: %s already exists; wait a second and run again", final)
	}
	if err := os.MkdirAll(o.To, 0o700); err != nil {
		return "", Manifest{}, err
	}
	// A unique work folder, so two runs never share one.
	work, err := os.MkdirTemp(o.To, "."+name+".partial-")
	if err != nil {
		return "", Manifest{}, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(work)
		}
	}()

	m := Manifest{Version: 1, CreatedAt: o.Now().UTC(), AllBranches: o.AllBranches, MetaOnly: o.MetaOnly}
	if err := copySQLite(ctx, layout.Meta(), filepath.Join(work, "meta.db"), o.Limits); err != nil {
		return "", m, fmt.Errorf("backup: meta.db: %w", err)
	}
	if err := m.add(work, "meta.db"); err != nil {
		return "", m, err
	}

	version, branches, err := readCopy(ctx, filepath.Join(work, "meta.db"), o.AllBranches)
	if err != nil {
		return "", m, fmt.Errorf("backup: read the copied meta.db: %w", err)
	}
	m.MetaVersion = version
	if o.MetaOnly {
		branches = nil
	}
	for _, b := range branches {
		rel := filepath.Join("dbs", b.dbID, b.id+".db")
		src := layout.BranchFile(b.dbID, b.id)
		if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
			m.Skipped = append(m.Skipped, filepath.ToSlash(rel))
			continue
		}
		if err := copySQLite(ctx, src, filepath.Join(work, rel), o.Limits); err != nil {
			if _, serr := os.Stat(src); errors.Is(serr, fs.ErrNotExist) {
				_ = os.Remove(filepath.Join(work, rel))
				m.Skipped = append(m.Skipped, filepath.ToSlash(rel))
				continue
			}
			return "", m, fmt.Errorf("backup: branch %s: %w", b.id, err)
		}
		if err := m.add(work, rel); err != nil {
			return "", m, err
		}
	}
	if b, err := os.ReadFile(filepath.Join(layout.SecretsDir(), "service-token")); err == nil {
		rel := filepath.Join("secrets", "service-token")
		if err := os.MkdirAll(filepath.Join(work, "secrets"), 0o700); err != nil {
			return "", m, err
		}
		if err := os.WriteFile(filepath.Join(work, rel), b, 0o600); err != nil {
			return "", m, err
		}
		if err := m.add(work, rel); err != nil {
			return "", m, err
		}
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(work, "manifest.json"), append(mb, '\n'), 0o600); err != nil {
		return "", m, err
	}
	if err := os.Rename(work, final); err != nil {
		return "", m, err
	}
	ok = true
	if err := prune(o.To, o.Keep, o.MetaOnly, o.Now()); err != nil {
		return final, m, fmt.Errorf("backup: %s is complete, but retention failed: %w", final, err)
	}
	return final, m, nil
}

type branchRef struct{ dbID, id string }

// readCopy lists the branches to copy from the copied meta.db, read-only so
// the file stays exactly as its checksum says.
func readCopy(ctx context.Context, path string, all bool) (int, []branchRef, error) {
	c, err := sqlite3.OpenContext(ctx, uri(path, "ro"))
	if err != nil {
		return 0, nil, err
	}
	defer c.Close()
	stmt, _, err := c.Prepare("PRAGMA user_version")
	if err != nil {
		return 0, nil, err
	}
	stmt.Step()
	version := stmt.ColumnInt(0)
	stmt.Close()
	q := `SELECT b.database_id, b.id FROM branches b JOIN databases d ON d.id = b.database_id WHERE d.deleted_at IS NULL`
	if !all {
		q += ` AND b.id = d.default_branch_id`
	}
	stmt, _, err = c.Prepare(q + ` ORDER BY b.database_id, b.id`)
	if err != nil {
		return 0, nil, err
	}
	defer stmt.Close()
	var out []branchRef
	for stmt.Step() {
		dbID, id := stmt.ColumnText(0), stmt.ColumnText(1)
		if !ids.IsID(dbID, ids.Database) || !ids.IsID(id, ids.Branch) {
			return 0, nil, fmt.Errorf("meta.db lists a malformed branch id %q/%q", dbID, id)
		}
		out = append(out, branchRef{dbID, id})
	}
	return version, out, stmt.Err()
}

func (m *Manifest) add(root, rel string) error {
	f, err := os.Open(filepath.Join(root, rel))
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	m.Files = append(m.Files, File{Path: filepath.ToSlash(rel), Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))})
	return nil
}

func uri(path, mode string) string {
	return (&url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {mode}}.Encode()}).String()
}

// copySQLite copies a live SQLite file with the online backup API and checks
// the copy.
func copySQLite(ctx context.Context, src, dst string, limits sqlitex.Limits) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	conn, err := sqlite3.OpenContext(ctx, uri(src, "rw"))
	if err != nil {
		return err
	}
	conn.SetInterrupt(ctx)
	err = conn.Backup("main", dst)
	conn.Close()
	if err != nil {
		return err
	}
	return check(ctx, dst, limits)
}

// check switches the copy to rollback-journal mode, so a backup is one file,
// then runs an integrity check on a hardened connection: the check evaluates
// the user's schema (expression indexes, CHECK constraints), so it gets the
// same memory and size limits as user SQL, and Ctrl-C stops it.
func check(ctx context.Context, path string, limits sqlitex.Limits) error {
	c, err := sqlite3.OpenContext(ctx, uri(path, "rw"))
	if err != nil {
		return err
	}
	err = c.Exec("PRAGMA journal_mode = DELETE")
	c.Close()
	if err != nil {
		return err
	}
	h, err := sqlitex.Open(ctx, sqlitex.Options{Path: path, ReadOnly: true, Limits: limits})
	if err != nil {
		return err
	}
	defer h.Close()
	h.SetInterrupt(ctx)
	stmt, _, err := h.Prepare("PRAGMA integrity_check")
	if err != nil {
		return err
	}
	defer stmt.Close()
	var msgs []string
	for stmt.Step() {
		msgs = append(msgs, stmt.ColumnText(0))
	}
	if err := stmt.Err(); err != nil {
		return err
	}
	if len(msgs) != 1 || msgs[0] != "ok" {
		return fmt.Errorf("integrity check of the copy: %s", strings.Join(msgs, "; "))
	}
	return nil
}

// List returns the full backups in a folder, newest first.
func List(dir string) ([]string, error) { return list(dir, false) }

func list(dir string, metaOnly bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if metaOnly {
			if !strings.HasSuffix(name, metaSuffix) || !isStamp(strings.TrimSuffix(name, metaSuffix)) {
				continue
			}
		} else if !isStamp(name) {
			continue
		}
		out = append(out, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

func isStamp(name string) bool {
	_, err := time.Parse(stampLayout, name)
	return err == nil
}

// prune keeps the newest keep backups of one kind, so meta-only backups
// never push full ones out, and removes work folders a killed run left
// behind more than a day ago.
func prune(dir string, keep int, metaOnly bool, now time.Time) error {
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), ".") || !strings.Contains(e.Name(), ".partial-") {
				continue
			}
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > 24*time.Hour {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	if keep <= 0 {
		return nil
	}
	all, err := list(dir, metaOnly)
	if err != nil {
		return err
	}
	for i, name := range all {
		if i >= keep {
			if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// validPath reports whether a manifest path is one a backup can hold, so a
// tampered manifest can't make restore write outside the data folder.
func validPath(p string) bool {
	switch p {
	case "meta.db", "secrets/service-token":
		return true
	}
	parts := strings.Split(p, "/")
	return len(parts) == 3 && parts[0] == "dbs" && path.Clean(p) == p &&
		ids.IsID(parts[1], ids.Database) && strings.HasSuffix(parts[2], ".db") &&
		ids.IsID(strings.TrimSuffix(parts[2], ".db"), ids.Branch)
}

// Verify checks every file in a backup against its manifest.
func Verify(dir string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	hasMeta := false
	for _, f := range m.Files {
		if !validPath(f.Path) {
			return m, fmt.Errorf("backup: the manifest lists an unexpected file %q", f.Path)
		}
		hasMeta = hasMeta || f.Path == "meta.db"
		var got Manifest
		if err := got.add(dir, filepath.FromSlash(f.Path)); err != nil {
			return m, err
		}
		if got.Files[0].SHA256 != f.SHA256 {
			return m, fmt.Errorf("backup: %s doesn't match its manifest", f.Path)
		}
	}
	if !hasMeta {
		return m, errors.New("backup: the manifest lists no meta.db")
	}
	return m, nil
}

// Restore copies a full backup into an empty data directory. Records whose
// files aren't in the backup (other branches, snapshots, the trash) are
// removed from the restored meta.db, so it never points at a missing file.
// The files are staged in a subfolder and moved into place only once the
// whole restore worked, so a failure leaves the folder empty again.
func Restore(ctx context.Context, from, dataDir string) (Manifest, error) {
	from, err := filepath.Abs(from)
	if err != nil {
		return Manifest{}, err
	}
	m, err := Verify(from)
	if err != nil {
		return m, err
	}
	if m.MetaOnly {
		return m, errors.New("restore: this backup holds only meta.db; roll back an upgrade with restore --meta-only")
	}
	layout, err := storage.New(dataDir)
	if err != nil {
		return m, err
	}
	unlock, err := layout.Lock()
	if err != nil {
		return m, err
	}
	defer unlock()
	entries, err := os.ReadDir(layout.Root)
	if err != nil {
		return m, err
	}
	for _, e := range entries {
		if e.Name() != "lumbungd.lock" {
			return m, fmt.Errorf("restore: %s isn't empty; restore into a new folder", layout.Root)
		}
	}
	stage := filepath.Join(layout.Root, ".restoring")
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(stage)
		}
	}()
	staged := storage.Layout{Root: stage}
	if err := staged.Ensure(); err != nil {
		return m, err
	}
	have := map[string]bool{}
	for _, f := range m.Files {
		if err := copyFile(filepath.Join(from, filepath.FromSlash(f.Path)), filepath.Join(stage, filepath.FromSlash(f.Path))); err != nil {
			return m, err
		}
		if strings.HasPrefix(f.Path, "dbs/") {
			have[strings.TrimSuffix(path.Base(f.Path), ".db")] = true
		}
	}
	if err := reconcile(ctx, staged.Meta(), meta.Present{Branches: have}); err != nil {
		return m, err
	}
	names, err := os.ReadDir(stage)
	if err != nil {
		return m, err
	}
	for _, e := range names {
		if err := os.Rename(filepath.Join(stage, e.Name()), filepath.Join(layout.Root, e.Name())); err != nil {
			return m, err
		}
	}
	done = true
	return m, os.Remove(stage)
}

// RestoreMeta rolls meta.db in an existing data directory back to a backup,
// for undoing an upgrade. lumbungd must be stopped: the data directory's
// lock proves it. The current meta.db (with its WAL files) is kept next to
// it as meta.db.before-restore-<time>. Records whose files are gone since
// the backup are removed; files with no record are removed by lumbungd on
// its next start.
func RestoreMeta(ctx context.Context, from, dataDir string, now time.Time) (Manifest, string, error) {
	from, err := filepath.Abs(from)
	if err != nil {
		return Manifest{}, "", err
	}
	m, err := Verify(from)
	if err != nil {
		return m, "", err
	}
	layout, err := storage.New(dataDir)
	if err != nil {
		return m, "", err
	}
	unlock, err := layout.Lock()
	if errors.Is(err, storage.ErrLocked) {
		return m, "", fmt.Errorf("restore: lumbungd is running on %s; stop it first", layout.Root)
	} else if err != nil {
		return m, "", err
	}
	defer unlock()
	if _, err := os.Stat(layout.Meta()); err != nil {
		return m, "", fmt.Errorf("restore: no meta.db in %s; use restore without --meta-only for an empty folder", layout.Root)
	}
	incoming := layout.Meta() + ".restoring"
	_ = os.Remove(incoming)
	if err := copyFile(filepath.Join(from, "meta.db"), incoming); err != nil {
		return m, "", err
	}
	aside := layout.Meta() + ".before-restore-" + now.UTC().Format(stampLayout)
	// Move the WAL files aside with the file they belong to: left in place,
	// SQLite would replay the newer WAL onto the restored meta.db.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Rename(layout.Meta()+suffix, aside+suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(incoming)
			return m, "", err
		}
	}
	if err := os.Rename(incoming, layout.Meta()); err != nil {
		return m, aside, err
	}
	p, err := present(layout)
	if err != nil {
		return m, aside, err
	}
	return m, aside, reconcile(ctx, layout.Meta(), p)
}

func reconcile(ctx context.Context, metaPath string, p meta.Present) error {
	store, err := meta.Open(ctx, metaPath)
	if store != nil {
		defer store.Close()
	}
	if err != nil {
		return err
	}
	return store.DropMissing(ctx, p)
}

// present lists the branch, snapshot and trash files in a data directory.
func present(l storage.Layout) (meta.Present, error) {
	p := meta.Present{Branches: map[string]bool{}, Snapshots: map[string]bool{}, Trash: map[string]bool{}}
	files := func(dir string, prefix string, into map[string]bool) error {
		dbs, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		for _, db := range dbs {
			if !db.IsDir() {
				continue
			}
			entries, err := os.ReadDir(filepath.Join(dir, db.Name()))
			if err != nil {
				return err
			}
			for _, e := range entries {
				id := strings.TrimSuffix(e.Name(), ".db")
				if strings.HasSuffix(e.Name(), ".db") && ids.IsID(id, prefix) {
					into[id] = true
				}
			}
		}
		return nil
	}
	if err := files(l.DBsDir(), ids.Branch, p.Branches); err != nil {
		return p, err
	}
	if err := files(l.SnapshotsDir(), ids.Snapshot, p.Snapshots); err != nil {
		return p, err
	}
	trash, err := os.ReadDir(l.TrashDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return p, err
	}
	for _, e := range trash {
		if e.IsDir() && ids.IsID(e.Name(), ids.Database) {
			p.Trash[e.Name()] = true
		}
	}
	return p, nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ErrNoBackups means a backup folder holds none of the asked kind.
var ErrNoBackups = errors.New("backup: no backups found")

// Latest returns the newest full backup in a folder, or with metaOnly the
// newest meta-only one.
func Latest(dir string, metaOnly bool) (string, error) {
	all, err := list(dir, metaOnly)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "", ErrNoBackups
	}
	return filepath.Join(dir, all[0]), nil
}
