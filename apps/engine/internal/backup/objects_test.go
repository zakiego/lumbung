package backup

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

func putObj(t *testing.T, e *engine.Engine, bucketID, key, body string) meta.Object {
	t.Helper()
	o, err := e.PutObject(bg, engine.PutObjectInput{BucketID: bucketID, Key: key, Size: int64(len(body)), Body: strings.NewReader(body)})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func readObj(t *testing.T, e *engine.Engine, bucketID, key string) string {
	t.Helper()
	_, f, err := e.OpenObject(bg, bucketID, key)
	if err != nil {
		t.Fatalf("open %s: %v", key, err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b)
}

func TestBackupIncludesObjects(t *testing.T) {
	src := t.TempDir()
	e := open(t, src)
	b, err := e.CreateBucket(bg, "photos")
	if err != nil {
		t.Fatal(err)
	}
	putObj(t, e, b.ID, "a.txt", "alpha")
	putObj(t, e, b.ID, "dir/b.txt", "bravo")
	if _, err := e.CreateUpload(bg, b.ID, "in-progress", engine.ObjectAttrs{}, ""); err != nil {
		t.Fatal(err)
	}
	// A token whose access key secret is sealed with this instance's key.
	sealed := e.Sealer().Seal("tok_aaaaaaaaaaaaaaaaaaaa", "the-secret")
	if _, err := e.Meta().CreateToken(bg, meta.Token{ID: "tok_aaaaaaaaaaaaaaaaaaaa", Name: "s3", SecretHash: []byte{1}, Scope: "bucket",
		BucketID: b.ID, Access: "read", AccessSecret: sealed}); err != nil {
		t.Fatal(err)
	}

	to := t.TempDir()
	now := time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC)
	dir1, m1, err := Run(bg, Options{DataDir: src, To: to, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if !m1.Objects || m1.ObjectFiles() != 2 || m1.Linked != 0 {
		t.Fatalf("first backup: objects=%v files=%d linked=%d", m1.Objects, m1.ObjectFiles(), m1.Linked)
	}
	if _, err := os.Stat(filepath.Join(dir1, "secrets", "access-key-encryption-key")); err != nil {
		t.Fatal("the access key encryption key isn't in the backup")
	}

	// The next backup links unchanged files and copies the new one.
	putObj(t, e, b.ID, "c.txt", "charlie")
	dir2, m2, err := Run(bg, Options{DataDir: src, To: to, Now: func() time.Time { return now.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if m2.ObjectFiles() != 3 || m2.Linked != 2 {
		t.Fatalf("second backup: files=%d linked=%d", m2.ObjectFiles(), m2.Linked)
	}
	for _, f := range m1.Files {
		if !strings.HasPrefix(f.Path, "objects/") {
			continue
		}
		a, _ := os.Stat(filepath.Join(dir1, f.Path))
		c, _ := os.Stat(filepath.Join(dir2, f.Path))
		if a.Sys().(*syscall.Stat_t).Ino != c.Sys().(*syscall.Stat_t).Ino {
			t.Fatalf("%s was copied, not linked", f.Path)
		}
	}
	if _, err := Verify(dir2); err != nil {
		t.Fatal(err)
	}

	// --skip-objects leaves them out.
	_, m3, err := Run(bg, Options{DataDir: src, To: t.TempDir(), SkipObjects: true})
	if err != nil {
		t.Fatal(err)
	}
	if m3.Objects || m3.ObjectFiles() != 0 {
		t.Fatalf("skip-objects backup has %d object files", m3.ObjectFiles())
	}

	// Restore: objects come back with their content, the upload in
	// progress doesn't, and the access key secret still opens.
	dst := t.TempDir()
	if _, err := Restore(bg, dir2, dst); err != nil {
		t.Fatal(err)
	}
	r := open(t, dst)
	for k, want := range map[string]string{"a.txt": "alpha", "dir/b.txt": "bravo", "c.txt": "charlie"} {
		if got := readObj(t, r, b.ID, k); got != want {
			t.Fatalf("restored %s = %q", k, got)
		}
	}
	rb, _ := r.GetBucket(bg, "photos")
	if rb.ObjectCount != 3 || rb.SizeBytes != int64(len("alphabravocharlie")) {
		t.Fatalf("restored bucket totals: %d objects %d bytes", rb.ObjectCount, rb.SizeBytes)
	}
	if ups, _, _ := r.ListUploads(bg, b.ID, "", "", "", 10); len(ups) != 0 {
		t.Fatalf("an upload in progress survived the restore: %v", ups)
	}
	tok, err := r.Meta().GetToken(bg, "tok_aaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Sealer().Open(tok.ID, tok.AccessSecret); err != nil || got != "the-secret" {
		t.Fatalf("access key secret after restore: %q %v", got, err)
	}
}

// A backup made without object files restores without the objects, and
// the bucket totals match what's there.
func TestRestoreDropsObjectsWithoutFiles(t *testing.T) {
	src := t.TempDir()
	e := open(t, src)
	b, _ := e.CreateBucket(bg, "photos")
	putObj(t, e, b.ID, "keep.txt", "keep")
	gone := putObj(t, e, b.ID, "gone.txt", "gone!")
	dir, m, err := Run(bg, Options{DataDir: src, To: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// Take one object file out of the backup, as if it was skipped.
	rel := "objects/" + b.ID + "/" + gone.BlobID[3:5] + "/" + gone.BlobID
	if err := os.Remove(filepath.Join(dir, rel)); err != nil {
		t.Fatal(err)
	}
	var files []File
	for _, f := range m.Files {
		if f.Path != rel {
			files = append(files, f)
		}
	}
	m.Files = files
	mb, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if _, err := Restore(bg, dir, dst); err != nil {
		t.Fatal(err)
	}
	r := open(t, dst)
	if readObj(t, r, b.ID, "keep.txt") != "keep" {
		t.Fatal("kept object lost")
	}
	if _, err := r.HeadObject(bg, b.ID, "gone.txt"); err == nil {
		t.Fatal("an object without its file survived the restore")
	}
	rb, _ := r.GetBucket(bg, "photos")
	if rb.ObjectCount != 1 || rb.SizeBytes != 4 {
		t.Fatalf("bucket totals after reconcile: %d objects %d bytes", rb.ObjectCount, rb.SizeBytes)
	}
}

func TestVerifyObjectPaths(t *testing.T) {
	for p, want := range map[string]bool{
		"objects/bk_aaaaaaaaaaaaaaaaaaaa/bb/ob_bbbbbbbbbbbbbbbbbbbb": true,
		"objects/bk_aaaaaaaaaaaaaaaaaaaa/zz/ob_bbbbbbbbbbbbbbbbbbbb": false,
		"objects/bk_aaaaaaaaaaaaaaaaaaaa/bb/../../meta.db":           false,
		"objects/../secrets/x":              false,
		"secrets/access-key-encryption-key": true,
		"secrets/other":                     false,
	} {
		if validPath(p) != want {
			t.Errorf("validPath(%q) = %v", p, !want)
		}
	}
}
