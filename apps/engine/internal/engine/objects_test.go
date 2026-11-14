package engine

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

func mustBucket(t testing.TB, e *Engine, name string) meta.Bucket {
	t.Helper()
	b, err := e.CreateBucket(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func put(e *Engine, bucketID, key, body string) (meta.Object, error) {
	return e.PutObject(context.Background(), PutObjectInput{BucketID: bucketID, Key: key, Size: int64(len(body)), Body: strings.NewReader(body)})
}

func mustPut(t testing.TB, e *Engine, bucketID, key, body string) meta.Object {
	t.Helper()
	o, err := put(e, bucketID, key, body)
	if err != nil {
		t.Fatalf("put %q: %v", key, err)
	}
	return o
}

func read(t testing.TB, e *Engine, bucketID, key string) string {
	t.Helper()
	_, f, err := e.OpenObject(context.Background(), bucketID, key)
	if err != nil {
		t.Fatalf("open %q: %v", key, err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// tree lists every file under dir, relative, for checking what a write
// touched.
func tree(t testing.TB, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out[rel] = true
		}
		return nil
	})
	return out
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestBucketNamesAndLimits(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxBuckets = 2 })
	ctx := context.Background()
	for _, bad := range []string{"ab", "-abc", "abc-", "ABC", "a_b", strings.Repeat("a", 64), "a.b"} {
		_, err := e.CreateBucket(ctx, bad)
		wantCode(t, err, apierr.InvalidName, bad)
	}
	mustBucket(t, e, "abc")
	_, err := e.CreateBucket(ctx, "abc")
	wantCode(t, err, apierr.NameTaken, "duplicate")
	mustBucket(t, e, "two")
	_, err = e.CreateBucket(ctx, "three")
	wantCode(t, err, apierr.LimitReached, "max buckets")
}

func TestPutGetOverwriteDelete(t *testing.T) {
	e := newEngine(t, nil)
	ctx := context.Background()
	b := mustBucket(t, e, "files")
	o := mustPut(t, e, b.ID, "a/b.txt", "hello")
	if o.ETag != md5hex("hello") || o.Size != 5 || o.ContentType != "application/octet-stream" {
		t.Fatalf("object %+v", o)
	}
	if got := read(t, e, b.ID, "a/b.txt"); got != "hello" {
		t.Fatalf("read %q", got)
	}
	// A reader holding the old file keeps reading it after an overwrite.
	_, old, err := e.OpenObject(ctx, b.ID, "a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	mustPut(t, e, b.ID, "a/b.txt", "goodbye!")
	if got, _ := io.ReadAll(old); string(got) != "hello" {
		t.Fatalf("old reader got %q", got)
	}
	if got := read(t, e, b.ID, "a/b.txt"); got != "goodbye!" {
		t.Fatalf("read %q", got)
	}
	bk, _ := e.GetBucket(ctx, b.ID)
	if bk.SizeBytes != 8 || bk.ObjectCount != 1 {
		t.Fatalf("bucket totals %d bytes %d objects", bk.SizeBytes, bk.ObjectCount)
	}
	if ok, err := e.DeleteObject(ctx, b.ID, "a/b.txt"); err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if ok, err := e.DeleteObject(ctx, b.ID, "a/b.txt"); err != nil || ok {
		t.Fatalf("second delete: %v %v", ok, err)
	}
	_, err = e.HeadObject(ctx, b.ID, "a/b.txt")
	wantCode(t, err, apierr.ObjectNotFound, "deleted")
	bk, _ = e.GetBucket(ctx, b.ID)
	if bk.SizeBytes != 0 || bk.ObjectCount != 0 {
		t.Fatalf("bucket totals after delete %d bytes %d objects", bk.SizeBytes, bk.ObjectCount)
	}
}

func TestPutChecksBody(t *testing.T) {
	e := newEngine(t, nil)
	ctx := context.Background()
	b := mustBucket(t, e, "files")
	before := tree(t, e.layout.Root)
	_, err := e.PutObject(ctx, PutObjectInput{BucketID: b.ID, Key: "short", Size: 10, Body: strings.NewReader("abc")})
	wantCode(t, err, apierr.IncompleteBody, "short body")
	_, err = e.PutObject(ctx, PutObjectInput{BucketID: b.ID, Key: "long", Size: 2, Body: strings.NewReader("abc")})
	wantCode(t, err, apierr.IncompleteBody, "long body")
	sum := md5.Sum([]byte("other"))
	_, err = e.PutObject(ctx, PutObjectInput{BucketID: b.ID, Key: "md5", Size: 3, Body: strings.NewReader("abc"), ContentMD5: sum[:]})
	wantCode(t, err, apierr.BadDigest, "content-md5")
	// A verifying reader failing at the end leaves nothing behind.
	bad := io.MultiReader(strings.NewReader("abc"), errReader{apierr.New(apierr.BadDigest, "signature")})
	_, err = e.PutObject(ctx, PutObjectInput{BucketID: b.ID, Key: "sig", Size: 3, Body: bad})
	wantCode(t, err, apierr.BadDigest, "verifier")
	if after := tree(t, e.layout.Root); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("failed puts left files: before %v after %v", before, after)
	}
	_, err = e.PutObject(ctx, PutObjectInput{BucketID: b.ID, Key: "x", Size: 1, Body: strings.NewReader("a"),
		Attrs: ObjectAttrs{Metadata: map[string]string{"big": strings.Repeat("v", 3000)}}})
	wantCode(t, err, apierr.BadRequest, "metadata too big")
	_, err = e.PutObject(ctx, PutObjectInput{BucketID: b.ID, Key: "x", Size: 1, Body: strings.NewReader("a"),
		Attrs: ObjectAttrs{Headers: map[string]string{"x-evil": "1"}}})
	wantCode(t, err, apierr.BadRequest, "unknown header")
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestKeysAreOpaque(t *testing.T) {
	e := newEngine(t, nil)
	b := mustBucket(t, e, "files")
	keys := []string{"../../meta.db", "/etc/passwd", "a//b", "./x", "..", "%2e%2e/x", "dbs/x.db", strings.Repeat("k", 1024), "emoji 🍚/ok"}
	for _, k := range keys {
		mustPut(t, e, b.ID, k, "v:"+k)
	}
	for _, k := range keys {
		if got := read(t, e, b.ID, k); got != "v:"+k {
			t.Fatalf("%q read %q", k, got)
		}
	}
	for rel := range tree(t, e.layout.Root) {
		if strings.HasPrefix(rel, "objects/") {
			if !strings.HasPrefix(rel, "objects/"+b.ID+"/") {
				t.Fatalf("object file outside its bucket folder: %s", rel)
			}
			continue
		}
		if rel != "meta.db" && rel != "meta.db-wal" && rel != "meta.db-shm" && rel != "lumbungd.lock" && rel != "secrets/access-key-encryption-key" {
			t.Fatalf("unexpected file %s", rel)
		}
	}
	for _, bad := range []string{"", strings.Repeat("k", 1025), "a\x00b", "a\nb", "\x7f", string([]byte{0xff}), "a￿b"} {
		_, err := put(e, b.ID, bad, "x")
		wantCode(t, err, apierr.InvalidKey, fmt.Sprintf("%q", bad))
	}
}

func TestQuotas(t *testing.T) {
	e := newEngine(t, func(c *config.Config) {
		c.MaxObjectBytes = 10
		c.MaxBucketBytes = 15
		c.MaxObjectStorageBytes = 24
	})
	a := mustBucket(t, e, "aaa")
	b := mustBucket(t, e, "bbb")
	_, err := put(e, a.ID, "big", strings.Repeat("x", 11))
	wantCode(t, err, apierr.ObjectTooLarge, "object limit")
	mustPut(t, e, a.ID, "one", strings.Repeat("x", 10))
	_, err = put(e, a.ID, "two", strings.Repeat("x", 6))
	wantCode(t, err, apierr.BucketSizeLimit, "bucket limit")
	// Replacing an object counts only the difference.
	mustPut(t, e, a.ID, "one", strings.Repeat("y", 10))
	mustPut(t, e, b.ID, "one", strings.Repeat("x", 10))
	_, err = put(e, b.ID, "two", strings.Repeat("x", 5))
	wantCode(t, err, apierr.ObjectQuotaExceeded, "instance quota")
	e.freeBytes = func(string) (int64, error) { return e.cfg.MinFreeDiskBytes + 3, nil }
	_, err = put(e, b.ID, "small", "abcd")
	wantCode(t, err, apierr.DiskFull, "free disk")
}

func TestConcurrentPutsRespectQuota(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxBucketBytes = 100 })
	b := mustBucket(t, e, "files")
	var wg sync.WaitGroup
	var mu sync.Mutex
	okCount := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := put(e, b.ID, fmt.Sprintf("k%d", i), strings.Repeat("x", 10)); err == nil {
				mu.Lock()
				okCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	bk, _ := e.GetBucket(context.Background(), b.ID)
	if bk.SizeBytes > 100 || okCount != 10 {
		t.Fatalf("%d puts succeeded, bucket holds %d bytes", okCount, bk.SizeBytes)
	}
}

func TestListPrefixDelimiterPaging(t *testing.T) {
	e := newEngine(t, nil)
	ctx := context.Background()
	b := mustBucket(t, e, "files")
	for _, k := range []string{"a.txt", "dir/1", "dir/2", "dir/sub/3", "dir2/x", "z", "é"} {
		mustPut(t, e, b.ID, k, "x")
	}
	keys := func(r ListObjectsResult) string {
		var out []string
		for _, o := range r.Objects {
			out = append(out, o.Key)
		}
		return strings.Join(out, ",") + "|" + strings.Join(r.Prefixes, ",")
	}
	r, err := e.ListObjects(ctx, ListObjectsInput{BucketID: b.ID, Delimiter: "/", Max: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(r); got != "a.txt,z,é|dir/,dir2/" || r.Truncated {
		t.Fatalf("root listing %s", got)
	}
	r, _ = e.ListObjects(ctx, ListObjectsInput{BucketID: b.ID, Prefix: "dir/", Delimiter: "/", Max: 1000})
	if got := keys(r); got != "dir/1,dir/2|dir/sub/" {
		t.Fatalf("dir listing %s", got)
	}
	// Page one item at a time, prefixes counting as items, and never repeat.
	var all []string
	after := ""
	for i := 0; i < 10; i++ {
		r, err := e.ListObjects(ctx, ListObjectsInput{BucketID: b.ID, Delimiter: "/", After: after, Max: 1})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, keys(r))
		if !r.Truncated {
			break
		}
		after = r.Next
	}
	if got := strings.Join(all, " "); got != "a.txt| |dir/ |dir2/ z| é|" {
		t.Fatalf("paged %q", got)
	}
	r, _ = e.ListObjects(ctx, ListObjectsInput{BucketID: b.ID, Prefix: "dir", Max: 2})
	if got := keys(r); got != "dir/1,dir/2|" || !r.Truncated || r.Next != "dir/2" {
		t.Fatalf("prefix page %s %v %q", got, r.Truncated, r.Next)
	}
	r, _ = e.ListObjects(ctx, ListObjectsInput{BucketID: b.ID, Prefix: "dir", After: r.Next, Max: 2})
	if got := keys(r); got != "dir/sub/3,dir2/x|" || r.Truncated {
		t.Fatalf("prefix page 2 %s", got)
	}
}

func TestCopyObject(t *testing.T) {
	e := newEngine(t, nil)
	ctx := context.Background()
	a := mustBucket(t, e, "aaa")
	b := mustBucket(t, e, "bbb")
	src, err := e.PutObject(ctx, PutObjectInput{BucketID: a.ID, Key: "src", Size: 3, Body: strings.NewReader("abc"),
		Attrs: ObjectAttrs{ContentType: "text/plain", Metadata: map[string]string{"m": "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	cp, err := e.CopyObject(ctx, CopyObjectInput{SrcBucketID: a.ID, SrcKey: "src", BucketID: b.ID, Key: "dst"})
	if err != nil {
		t.Fatal(err)
	}
	if cp.ETag != src.ETag || cp.ContentType != "text/plain" || cp.Metadata["m"] != "1" || cp.BlobID == src.BlobID {
		t.Fatalf("copy %+v", cp)
	}
	// The source can go away; the copy keeps its content.
	if _, err := e.DeleteObject(ctx, a.ID, "src"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, e, b.ID, "dst"); got != "abc" {
		t.Fatalf("copy read %q", got)
	}
	_, err = e.CopyObject(ctx, CopyObjectInput{SrcBucketID: b.ID, SrcKey: "dst", BucketID: b.ID, Key: "dst"})
	wantCode(t, err, apierr.BadRequest, "copy onto itself without replace")
	cp, err = e.CopyObject(ctx, CopyObjectInput{SrcBucketID: b.ID, SrcKey: "dst", BucketID: b.ID, Key: "dst", Replace: true,
		Attrs: ObjectAttrs{ContentType: "text/csv"}})
	if err != nil || cp.ContentType != "text/csv" || len(cp.Metadata) != 0 {
		t.Fatalf("replace: %+v %v", cp, err)
	}
	stop := errors.New("nope")
	_, err = e.CopyObject(ctx, CopyObjectInput{SrcBucketID: b.ID, SrcKey: "dst", BucketID: a.ID, Key: "x", Check: func(meta.Object) error { return stop }})
	if !errors.Is(err, stop) {
		t.Fatalf("check: %v", err)
	}
}

func TestMultipart(t *testing.T) {
	MinPartBytes = 4
	t.Cleanup(func() { MinPartBytes = 5 << 20 })
	e := newEngine(t, nil)
	ctx := context.Background()
	b := mustBucket(t, e, "files")
	u, err := e.CreateUpload(ctx, b.ID, "big", ObjectAttrs{ContentType: "text/plain"}, "tok_x")
	if err != nil {
		t.Fatal(err)
	}
	part := func(n int, body string) string {
		t.Helper()
		etag, err := e.UploadPart(ctx, UploadPartInput{UploadID: u.ID, BucketID: b.ID, Key: "big", Number: n, Size: int64(len(body)), Body: strings.NewReader(body)})
		if err != nil {
			t.Fatalf("part %d: %v", n, err)
		}
		return etag
	}
	e1 := part(1, "aaaa")
	part(2, "XXXX")
	e2 := part(2, "bbbb") // replaces
	e3 := part(3, "c")
	_, err = e.UploadPart(ctx, UploadPartInput{UploadID: u.ID, BucketID: b.ID, Key: "other", Number: 1, Size: 1, Body: strings.NewReader("x")})
	wantCode(t, err, apierr.UploadNotFound, "other key")
	other := mustBucket(t, e, "other")
	_, err = e.UploadPart(ctx, UploadPartInput{UploadID: u.ID, BucketID: other.ID, Key: "big", Number: 1, Size: 1, Body: strings.NewReader("x")})
	wantCode(t, err, apierr.UploadNotFound, "other bucket")

	_, err = e.CompleteUpload(ctx, u.ID, b.ID, "big", []CompletedPart{{2, e2}, {1, e1}})
	wantCode(t, err, apierr.InvalidPart, "order")
	_, err = e.CompleteUpload(ctx, u.ID, b.ID, "big", []CompletedPart{{1, e1}, {2, e1}})
	wantCode(t, err, apierr.InvalidPart, "etag")
	_, err = e.CompleteUpload(ctx, u.ID, b.ID, "big", []CompletedPart{{1, e1}, {4, e3}})
	wantCode(t, err, apierr.InvalidPart, "missing part")

	o, err := e.CompleteUpload(ctx, u.ID, b.ID, "big", []CompletedPart{{1, `"` + e1 + `"`}, {2, e2}, {3, e3}})
	if err != nil {
		t.Fatal(err)
	}
	raw := func(h string) []byte { b, _ := hex.DecodeString(h); return b }
	sum := md5.Sum(bytes.Join([][]byte{raw(e1), raw(e2), raw(e3)}, nil))
	if o.ETag != hex.EncodeToString(sum[:])+"-3" || o.Size != 9 || o.ContentType != "text/plain" || o.Parts != 3 {
		t.Fatalf("object %+v", o)
	}
	if got := read(t, e, b.ID, "big"); got != "aaaabbbbc" {
		t.Fatalf("read %q", got)
	}
	if _, err := os.Stat(e.layout.UploadDir(u.ID)); !os.IsNotExist(err) {
		t.Fatalf("upload folder left: %v", err)
	}
	_, err = e.CompleteUpload(ctx, u.ID, b.ID, "big", []CompletedPart{{1, e1}})
	wantCode(t, err, apierr.UploadNotFound, "complete twice")

	// Small parts before the last are refused; abort removes everything.
	u2, _ := e.CreateUpload(ctx, b.ID, "small", ObjectAttrs{}, "")
	s1, _ := e.UploadPart(ctx, UploadPartInput{UploadID: u2.ID, BucketID: b.ID, Key: "small", Number: 1, Size: 1, Body: strings.NewReader("a")})
	s2, _ := e.UploadPart(ctx, UploadPartInput{UploadID: u2.ID, BucketID: b.ID, Key: "small", Number: 2, Size: 1, Body: strings.NewReader("b")})
	_, err = e.CompleteUpload(ctx, u2.ID, b.ID, "small", []CompletedPart{{1, s1}, {2, s2}})
	wantCode(t, err, apierr.PartTooSmall, "small part")
	if err := e.AbortUpload(ctx, u2.ID, b.ID, "small"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.layout.UploadDir(u2.ID)); !os.IsNotExist(err) {
		t.Fatalf("aborted upload folder left: %v", err)
	}
	_, err = e.UploadPart(ctx, UploadPartInput{UploadID: u2.ID, BucketID: b.ID, Key: "small", Number: 3, Size: 1, Body: strings.NewReader("c")})
	wantCode(t, err, apierr.UploadNotFound, "after abort")
}

func TestPartsCountTowardQuota(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MaxBucketBytes = 10 })
	ctx := context.Background()
	b := mustBucket(t, e, "files")
	u, _ := e.CreateUpload(ctx, b.ID, "k", ObjectAttrs{}, "")
	for n := 1; n <= 2; n++ {
		if _, err := e.UploadPart(ctx, UploadPartInput{UploadID: u.ID, BucketID: b.ID, Key: "k", Number: n, Size: 5, Body: strings.NewReader("12345")}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := put(e, b.ID, "x", "1")
	wantCode(t, err, apierr.BucketSizeLimit, "parts fill the bucket")
}

func TestDeleteBucket(t *testing.T) {
	e := newEngine(t, nil)
	ctx := context.Background()
	b := mustBucket(t, e, "files")
	mustPut(t, e, b.ID, "k", "v")
	wantCode(t, e.DeleteBucket(ctx, b.ID), apierr.BucketNotEmpty, "not empty")
	u, _ := e.CreateUpload(ctx, b.ID, "up", ObjectAttrs{}, "")
	if _, err := e.UploadPart(ctx, UploadPartInput{UploadID: u.ID, BucketID: b.ID, Key: "up", Number: 1, Size: 1, Body: strings.NewReader("x")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DeleteObject(ctx, b.ID, "k"); err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteBucket(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.layout.UploadDir(u.ID)); !os.IsNotExist(err) {
		t.Fatal("upload folder left")
	}
	_, err := e.GetBucket(ctx, "files")
	wantCode(t, err, apierr.BucketNotFound, "deleted bucket")
	mustBucket(t, e, "files") // the name is free again
}

func TestGCAndSweep(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.ObjectGCDelay = time.Hour })
	ctx := context.Background()
	b := mustBucket(t, e, "files")
	first := mustPut(t, e, b.ID, "k", "one")
	mustPut(t, e, b.ID, "k", "two")
	oldFile := e.layout.BlobFile(b.ID, first.BlobID)
	e.runRemovals(ctx, time.Now())
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatal("replaced file removed before the delay")
	}
	e.runRemovals(ctx, time.Now().Add(2*time.Hour))
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatal("replaced file kept after the delay")
	}
	// An orphan (written, never recorded) goes once old enough.
	orphan := e.layout.BlobFile(b.ID, "ob_aaaaaaaaaaaaaaaaaaaa")
	_ = os.MkdirAll(filepath.Dir(orphan), 0o700)
	_ = os.WriteFile(orphan, []byte("x"), 0o600)
	stray := filepath.Join(e.layout.ObjectsDir(), b.ID, "notes.txt")
	_ = os.WriteFile(stray, []byte("x"), 0o600)
	if err := e.sweepObjects(ctx, time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("young orphan removed")
	}
	if err := e.sweepObjects(ctx, time.Now().Add(2*time.Hour), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("old orphan kept")
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatal("a file not named like an id was touched")
	}
	if got := read(t, e, b.ID, "k"); got != "two" {
		t.Fatalf("live object hurt by the sweep: %q", got)
	}
	for _, bad := range []string{"meta.db", "objects/../meta.db", "dbs/db_aaaaaaaaaaaaaaaaaaaa/br_aaaaaaaaaaaaaaaaaaaa.db", "objects/" + b.ID + "/zz/ob_aaaaaaaaaaaaaaaaaaaa", "uploads/up_aaaaaaaaaaaaaaaaaaaa/../../meta.db"} {
		if validRemoval(bad) {
			t.Fatalf("validRemoval(%q) = true", bad)
		}
	}
}

func TestMultipartExpiry(t *testing.T) {
	e := newEngine(t, func(c *config.Config) { c.MultipartExpiry = time.Hour })
	ctx := context.Background()
	b := mustBucket(t, e, "files")
	u, _ := e.CreateUpload(ctx, b.ID, "k", ObjectAttrs{}, "")
	if _, err := e.UploadPart(ctx, UploadPartInput{UploadID: u.ID, BucketID: b.ID, Key: "k", Number: 1, Size: 1, Body: strings.NewReader("x")}); err != nil {
		t.Fatal(err)
	}
	e.objectHousekeeping(ctx, time.Now())
	if _, err := e.GetUpload(ctx, u.ID, b.ID, "k"); err != nil {
		t.Fatal("fresh upload expired")
	}
	e.objectHousekeeping(ctx, time.Now().Add(2*time.Hour))
	_, err := e.GetUpload(ctx, u.ID, b.ID, "k")
	wantCode(t, err, apierr.UploadNotFound, "expired upload")
	if _, err := os.Stat(e.layout.UploadDir(u.ID)); !os.IsNotExist(err) {
		t.Fatal("expired upload folder left")
	}
}

func TestCrashLeftoversRemovedAtStart(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.DataDir = dir
	e, err := Open(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := mustBucket(t, e, "files")
	o := mustPut(t, e, b.ID, "k", "v")
	orphan := e.layout.BlobFile(b.ID, "ob_bbbbbbbbbbbbbbbbbbbb")
	young := e.layout.BlobFile(b.ID, "ob_cccccccccccccccccccc")
	_ = os.MkdirAll(filepath.Dir(orphan), 0o700)
	_ = os.MkdirAll(filepath.Dir(young), 0o700)
	_ = os.WriteFile(orphan, []byte("x"), 0o600)
	_ = os.WriteFile(young, []byte("x"), 0o600)
	old := time.Now().Add(-2 * cfg.ObjectGCDelay)
	_ = os.Chtimes(orphan, old, old)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e, err = Open(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("old orphan kept across restart")
	}
	if _, err := os.Stat(young); err != nil {
		t.Fatal("an orphan younger than the GC delay was removed at start")
	}
	if _, err := os.Stat(e.layout.BlobFile(b.ID, o.BlobID)); err != nil {
		t.Fatal("live file removed at start")
	}
}

func TestAccessKeySecretsSealed(t *testing.T) {
	e := newEngine(t, nil)
	sealed := e.Sealer().Seal("tok_aaaaaaaaaaaaaaaaaaaa", "the-secret")
	if bytes.Contains(sealed, []byte("the-secret")) {
		t.Fatal("secret in plain text")
	}
	if got, err := e.Sealer().Open("tok_aaaaaaaaaaaaaaaaaaaa", sealed); err != nil || got != "the-secret" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := e.Sealer().Open("tok_bbbbbbbbbbbbbbbbbbbb", sealed); err == nil {
		t.Fatal("a sealed secret opened under another token")
	}
	fi, err := os.Stat(e.layout.AccessKeyFile())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi, err)
	}
}
