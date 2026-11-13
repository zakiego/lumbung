// Package backup copies a data directory to a backup folder and back
// (PLAN 12). It works while lumbungd is running: every file is copied with
// SQLite's online backup API, inside one read transaction per file, so each
// copy is consistent.
//
// A backup is a folder named by its UTC time:
//
//	<to>/20261114T020000Z/
//	  manifest.json                 what's in it, with SHA-256 sums
//	  meta.db
//	  dbs/<database id>/<branch id>.db
//	  secrets/service-token         when present
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ncruces/go-sqlite3"

	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/storage"
)

// Options say what to back up.
type Options struct {
	DataDir     string
	To          string
	AllBranches bool // every branch, not only each database's default
	MetaOnly    bool // only meta.db (before an upgrade)
	Keep        int  // keep the newest N backups in To; 0 keeps all
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
}

// File is one copied file.
type File struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Run makes one backup and applies retention. It returns the new folder.
func Run(ctx context.Context, o Options) (string, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	// SQLite resolves file names itself, and a relative one doesn't resolve
	// against the working directory, so every path here is made absolute.
	to, err := filepath.Abs(o.To)
	if err != nil {
		return "", err
	}
	o.To = to
	layout, err := storage.New(o.DataDir)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(layout.Meta()); err != nil {
		return "", fmt.Errorf("backup: no meta.db in %s: %w", layout.Root, err)
	}
	stamp := o.Now().UTC().Format("20060102T150405Z")
	final := filepath.Join(o.To, stamp)
	work := final + ".partial"
	if err := os.MkdirAll(work, 0o700); err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(work)
		}
	}()

	m := Manifest{Version: 1, CreatedAt: o.Now().UTC(), AllBranches: o.AllBranches, MetaOnly: o.MetaOnly}
	if err := copySQLite(ctx, layout.Meta(), filepath.Join(work, "meta.db")); err != nil {
		return "", fmt.Errorf("backup: meta.db: %w", err)
	}
	if err := m.add(work, "meta.db"); err != nil {
		return "", err
	}

	version, branches, err := readCopy(ctx, filepath.Join(work, "meta.db"), o.AllBranches)
	if err != nil {
		return "", fmt.Errorf("backup: read the copied meta.db: %w", err)
	}
	m.MetaVersion = version
	if o.MetaOnly {
		branches = nil
	}
	for _, b := range branches {
		rel := filepath.Join("dbs", b.dbID, b.id+".db")
		if err := copySQLite(ctx, layout.BranchFile(b.dbID, b.id), filepath.Join(work, rel)); err != nil {
			return "", fmt.Errorf("backup: branch %s: %w", b.id, err)
		}
		if err := m.add(work, rel); err != nil {
			return "", err
		}
	}
	if b, err := os.ReadFile(filepath.Join(layout.SecretsDir(), "service-token")); err == nil {
		rel := filepath.Join("secrets", "service-token")
		if err := os.MkdirAll(filepath.Join(work, "secrets"), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(work, rel), b, 0o600); err != nil {
			return "", err
		}
		if err := m.add(work, rel); err != nil {
			return "", err
		}
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(work, "manifest.json"), append(mb, '\n'), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(work, final); err != nil {
		return "", err
	}
	ok = true
	if o.Keep > 0 {
		if err := prune(o.To, o.Keep); err != nil {
			return final, fmt.Errorf("backup: retention: %w", err)
		}
	}
	return final, nil
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
		out = append(out, branchRef{stmt.ColumnText(0), stmt.ColumnText(1)})
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
func copySQLite(ctx context.Context, src, dst string) error {
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
	return check(ctx, dst)
}

func check(ctx context.Context, path string) error {
	c, err := sqlite3.OpenContext(ctx, uri(path, "rw"))
	if err != nil {
		return err
	}
	defer c.Close()
	// Store copies in rollback-journal mode, so a backup is one file.
	if err := c.Exec("PRAGMA journal_mode = DELETE"); err != nil {
		return err
	}
	stmt, _, err := c.Prepare("PRAGMA integrity_check")
	if err != nil {
		return err
	}
	defer stmt.Close()
	stmt.Step()
	if res := stmt.ColumnText(0); res != "ok" {
		return fmt.Errorf("integrity check of the copy: %s", res)
	}
	return stmt.Err()
}

// List returns the backups in a folder, newest first.
func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && isStamp(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

func isStamp(name string) bool {
	_, err := time.Parse("20060102T150405Z", name)
	return err == nil
}

func prune(dir string, keep int) error {
	all, err := List(dir)
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
	for _, f := range m.Files {
		var got Manifest
		if err := got.add(dir, filepath.FromSlash(f.Path)); err != nil {
			return m, err
		}
		if got.Files[0].SHA256 != f.SHA256 {
			return m, fmt.Errorf("backup: %s doesn't match its manifest", f.Path)
		}
	}
	return m, nil
}

// Restore copies a backup into an empty data directory. Branches and
// snapshots whose files aren't in the backup are removed from the restored
// meta.db, so it never points at a missing file. Run it with lumbungd
// stopped, then start lumbungd on the restored folder.
func Restore(ctx context.Context, from, dataDir string) (Manifest, error) {
	from, err := filepath.Abs(from)
	if err != nil {
		return Manifest{}, err
	}
	m, err := Verify(from)
	if err != nil {
		return m, err
	}
	layout, err := storage.New(dataDir)
	if err != nil {
		return m, err
	}
	if entries, err := os.ReadDir(layout.Root); err == nil && len(entries) > 0 {
		return m, fmt.Errorf("restore: %s isn't empty; restore into a new folder", layout.Root)
	}
	if err := layout.Ensure(); err != nil {
		return m, err
	}
	have := map[string]bool{}
	for _, f := range m.Files {
		src := filepath.Join(from, filepath.FromSlash(f.Path))
		dst := filepath.Join(layout.Root, filepath.FromSlash(f.Path))
		if err := copyFile(src, dst); err != nil {
			return m, err
		}
		if strings.HasPrefix(f.Path, "dbs/") {
			have[strings.TrimSuffix(filepath.Base(f.Path), ".db")] = true
		}
	}
	store, err := meta.Open(ctx, layout.Meta())
	if err != nil {
		return m, err
	}
	defer store.Close()
	if err := store.DropMissing(ctx, have); err != nil {
		return m, err
	}
	return m, nil
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

// ErrNoBackups means a backup folder holds none.
var ErrNoBackups = errors.New("backup: no backups found")

// Latest returns the newest backup in a folder.
func Latest(dir string) (string, error) {
	all, err := List(dir)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "", ErrNoBackups
	}
	return filepath.Join(dir, all[0]), nil
}
