package engine

// Object storage (PLAN 16, ADR 0006). Buckets and objects are indexed in
// meta.db; each object's content is one immutable file named by a random
// id under objects/<bucket id>/. A write streams to tmp/, is checked, is
// renamed into place and then recorded, so readers only ever see whole
// objects. Replaced and deleted files are removed after the GC delay.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/storage"
)

// Protocol limits that aren't settings.
const (
	MaxKeyBytes      = 1024
	MaxMetadataBytes = 2048
	MaxPartBytes     = 5 << 30
	MaxParts         = 10000
	MaxCORSOrigins   = 20
	// maxUploadsPerBucket bounds multipart uploads in progress per bucket.
	maxUploadsPerBucket = 1000
)

// MinPartBytes is the smallest part allowed before the last one. A variable
// so tests can use small parts.
var MinPartBytes int64 = 5 << 20

// ObjectHeaders are the standard headers stored with an object, besides
// Content-Type.
var ObjectHeaders = []string{"cache-control", "content-disposition", "content-encoding", "content-language", "expires"}

// ValidKey reports whether s can be an object key: 1 to 1024 bytes of
// UTF-8 without control characters or the two non-characters XML forbids.
func ValidKey(s string) bool {
	if len(s) == 0 || len(s) > MaxKeyBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0xfffe || r == 0xffff {
			return false
		}
	}
	return true
}

func checkKey(key string) error {
	if !ValidKey(key) {
		return apierr.New(apierr.InvalidKey, "an object key must be 1 to 1024 bytes of UTF-8 without control characters")
	}
	return nil
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func isTokenChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!#$&'*+^`|~", c) >= 0
}

// ObjectAttrs are what a client sets on an object besides its content.
type ObjectAttrs struct {
	ContentType string
	Headers     map[string]string // keys from ObjectHeaders
	Metadata    map[string]string // lowercase names without x-amz-meta-
}

// CheckAttrs validates attributes before any byte is read, and fills in the
// default content type.
func CheckAttrs(a *ObjectAttrs) error {
	if a.ContentType == "" {
		a.ContentType = "application/octet-stream"
	}
	if len(a.ContentType) > 256 || !printableASCII(a.ContentType) {
		return apierr.New(apierr.BadRequest, "the content type must be printable ASCII, at most 256 characters")
	}
	allowed := map[string]bool{}
	for _, h := range ObjectHeaders {
		allowed[h] = true
	}
	for k, v := range a.Headers {
		if !allowed[k] {
			return apierr.Newf(apierr.BadRequest, "header %q can't be stored with an object", k)
		}
		if len(v) > 1024 || !printableASCII(v) {
			return apierr.Newf(apierr.BadRequest, "the %s header must be printable ASCII, at most 1024 characters", k)
		}
	}
	n := 0
	for k, v := range a.Metadata {
		if k == "" || k != strings.ToLower(k) {
			return apierr.New(apierr.BadRequest, "metadata names must be lowercase and not empty")
		}
		for i := 0; i < len(k); i++ {
			if !isTokenChar(k[i]) {
				return apierr.Newf(apierr.BadRequest, "metadata name %q has a character headers can't carry", k)
			}
		}
		if !printableASCII(v) {
			return apierr.Newf(apierr.BadRequest, "metadata %q must be printable ASCII", k)
		}
		n += len(k) + len(v)
	}
	if n > MaxMetadataBytes {
		return apierr.Newf(apierr.BadRequest, "metadata is %d bytes; at most %d are allowed", n, MaxMetadataBytes)
	}
	return nil
}

// ---- buckets

// CheckCORSOrigins validates a bucket's CORS origins: "*" or
// scheme://host[:port], at most MaxCORSOrigins.
func CheckCORSOrigins(origins []string) error {
	if len(origins) > MaxCORSOrigins {
		return apierr.Newf(apierr.BadRequest, "at most %d CORS origins", MaxCORSOrigins)
	}
	for _, o := range origins {
		if o == "*" {
			continue
		}
		scheme, host, ok := strings.Cut(o, "://")
		if !ok || (scheme != "http" && scheme != "https") || host == "" || len(o) > 200 ||
			strings.ContainsAny(host, "/?#@ \t") || !printableASCII(o) {
			return apierr.Newf(apierr.BadRequest, "CORS origin %q must be * or scheme://host[:port]", o)
		}
	}
	return nil
}

// CreateBucket creates an empty bucket.
func (e *Engine) CreateBucket(ctx context.Context, name string) (meta.Bucket, error) {
	if e.refused != nil {
		return meta.Bucket{}, e.refused
	}
	if !ids.ValidBucketName(name) {
		return meta.Bucket{}, apierr.Newf(apierr.InvalidName, "invalid bucket name %q: use 3 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit", name)
	}
	if err := e.checkDisk(); err != nil {
		return meta.Bucket{}, err
	}
	return e.meta.CreateBucket(ctx, ids.New(ids.Bucket), name, e.cfg.MaxBuckets)
}

// GetBucket finds a bucket by name or id.
func (e *Engine) GetBucket(ctx context.Context, ref string) (meta.Bucket, error) {
	if e.refused != nil {
		return meta.Bucket{}, e.refused
	}
	return e.meta.GetBucket(ctx, ref)
}

// ListBuckets returns every bucket by name.
func (e *Engine) ListBuckets(ctx context.Context) ([]meta.Bucket, error) {
	if e.refused != nil {
		return nil, e.refused
	}
	return e.meta.ListBuckets(ctx)
}

// UpdateBucket changes a bucket's name or settings.
func (e *Engine) UpdateBucket(ctx context.Context, id string, u meta.BucketUpdate) (meta.Bucket, error) {
	if e.refused != nil {
		return meta.Bucket{}, e.refused
	}
	if u.CORSOrigins != nil {
		if err := CheckCORSOrigins(*u.CORSOrigins); err != nil {
			return meta.Bucket{}, err
		}
	}
	return e.meta.UpdateBucket(ctx, id, u)
}

// DeleteBucket removes an empty bucket: its uploads in progress are
// aborted and its tokens revoked. Files of objects deleted earlier are
// still removed on the GC delay, so a running backup can copy them.
func (e *Engine) DeleteBucket(ctx context.Context, id string) error {
	if e.refused != nil {
		return e.refused
	}
	uploads, err := e.meta.DeleteBucket(ctx, id)
	if err != nil {
		return err
	}
	for _, u := range uploads {
		_ = os.RemoveAll(e.layout.UploadDir(u))
	}
	return nil
}

// ---- writing

// reservation is room claimed for a write in flight.
type reservation struct {
	e        *Engine
	bucketID string
	n        int64
	done     bool
}

// reserveObjectBytes claims room for n new bytes in a bucket and in the
// instance's object storage. credit is space the write frees (the object it
// replaces). Release the claim when the write fails, or record the write
// with settle, which drops the claim in the same step so the bytes are
// never counted twice.
func (e *Engine) reserveObjectBytes(ctx context.Context, bucketID string, n, credit int64) (*reservation, error) {
	e.objMu.Lock()
	defer e.objMu.Unlock()
	used, total, err := e.meta.ObjectUsage(ctx, bucketID)
	if err != nil {
		return nil, err
	}
	if used-credit+e.objReserved[bucketID]+n > e.cfg.MaxBucketBytes {
		return nil, apierr.Newf(apierr.BucketSizeLimit, "%d more bytes would pass the %d byte bucket size limit", n, e.cfg.MaxBucketBytes)
	}
	if total-credit+e.objReservedTotal+n > e.cfg.MaxObjectStorageBytes {
		return nil, apierr.Newf(apierr.ObjectQuotaExceeded, "%d more bytes would pass the %d byte object storage quota", n, e.cfg.MaxObjectStorageBytes)
	}
	e.objReserved[bucketID] += n
	e.objReservedTotal += n
	return &reservation{e: e, bucketID: bucketID, n: n}, nil
}

func (r *reservation) releaseLocked() {
	if r == nil || r.done {
		return
	}
	r.done = true
	e := r.e
	e.objReserved[r.bucketID] -= r.n
	if e.objReserved[r.bucketID] == 0 {
		delete(e.objReserved, r.bucketID)
	}
	e.objReservedTotal -= r.n
}

// release drops the claim; it does nothing after settle.
func (r *reservation) release() {
	if r == nil {
		return
	}
	r.e.objMu.Lock()
	defer r.e.objMu.Unlock()
	r.releaseLocked()
}

// settle runs record (which commits the write to meta.db) and drops the
// claim while no other write can check the quota in between.
func (r *reservation) settle(record func() error) error {
	if r == nil {
		return record()
	}
	r.e.objMu.Lock()
	defer r.e.objMu.Unlock()
	err := record()
	r.releaseLocked()
	return err
}

// checkDiskFor refuses a write of n bytes that would leave less free space
// than the configured minimum.
func (e *Engine) checkDiskFor(n int64) error {
	free, err := e.freeBytes(e.layout.Root)
	if err != nil {
		return fmt.Errorf("engine: free disk: %w", err)
	}
	if free-n < e.cfg.MinFreeDiskBytes {
		return apierr.Newf(apierr.DiskFull, "writing %d bytes would leave less than %d bytes free on the data disk", n, e.cfg.MinFreeDiskBytes)
	}
	return nil
}

// writeTemp copies exactly size bytes of body into a new file in tmp/,
// synced, and returns its path and MD5. Fewer or more bytes fail with
// incomplete_body. A read error from body (a failed signature or checksum,
// which the S3 layer reports at the end of the body) fails the write.
func (e *Engine) writeTemp(body io.Reader, size int64) (string, []byte, error) {
	f, err := os.CreateTemp(e.layout.TmpDir(), "obj-")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			_ = os.Remove(path)
		}
	}()
	h := md5.New()
	n, err := io.CopyN(io.MultiWriter(f, h), body, size)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return "", nil, apierr.Newf(apierr.IncompleteBody, "the body ended after %d of %d bytes", n, size)
		}
		return "", nil, readErr(err)
	}
	// One more read: it must report the end, which is also where a
	// verifying reader reports a bad signature or checksum.
	var one [1]byte
	m, err := io.ReadFull(body, one[:])
	if m > 0 {
		return "", nil, apierr.Newf(apierr.IncompleteBody, "the body is longer than the declared %d bytes", size)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", nil, readErr(err)
	}
	if err := f.Sync(); err != nil {
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		return "", nil, err
	}
	ok = true
	return path, h.Sum(nil), nil
}

// readErr keeps a documented error from a verifying reader and turns any
// other read failure (the client went away) into incomplete_body.
func readErr(err error) error {
	var ae *apierr.Error
	if errors.As(err, &ae) {
		return ae
	}
	return apierr.New(apierr.IncompleteBody, "the body couldn't be read to the end").WithDetail(err.Error())
}

// place moves a finished temp file to its blob path, creating the folder.
func (e *Engine) place(tmp, bucketID, blobID string) (string, error) {
	final := e.layout.BlobFile(bucketID, blobID)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return "", err
	}
	return final, os.Rename(tmp, final)
}

func (e *Engine) gcAfter() time.Time { return time.Now().Add(e.cfg.ObjectGCDelay) }

func blobRel(bucketID, blobID string) string {
	return filepath.ToSlash(storage.BlobRel(bucketID, blobID))
}

// PutObjectInput is one object write.
type PutObjectInput struct {
	BucketID   string
	Key        string
	Size       int64     // declared length; required
	Body       io.Reader // exactly Size bytes
	Attrs      ObjectAttrs
	ContentMD5 []byte // optional; checked against the body
}

// PutObject stores an object, replacing any with the same key.
func (e *Engine) PutObject(ctx context.Context, in PutObjectInput) (meta.Object, error) {
	if e.refused != nil {
		return meta.Object{}, e.refused
	}
	if err := checkKey(in.Key); err != nil {
		return meta.Object{}, err
	}
	if err := CheckAttrs(&in.Attrs); err != nil {
		return meta.Object{}, err
	}
	if in.Size < 0 {
		return meta.Object{}, apierr.New(apierr.LengthRequired, "the upload needs a length")
	}
	if in.Size > e.cfg.MaxObjectBytes {
		return meta.Object{}, apierr.Newf(apierr.ObjectTooLarge, "the object is %d bytes; the limit is %d", in.Size, e.cfg.MaxObjectBytes)
	}
	var credit int64
	if old, err := e.meta.GetObject(ctx, in.BucketID, in.Key); err == nil {
		credit = old.Size
	}
	res, err := e.reserveObjectBytes(ctx, in.BucketID, in.Size, credit)
	if err != nil {
		return meta.Object{}, err
	}
	defer res.release()
	if err := e.checkDiskFor(in.Size); err != nil {
		return meta.Object{}, err
	}
	tmp, sum, err := e.writeTemp(in.Body, in.Size)
	if err != nil {
		return meta.Object{}, err
	}
	defer os.Remove(tmp) // a no-op once renamed
	if in.ContentMD5 != nil && !equalBytes(in.ContentMD5, sum) {
		return meta.Object{}, apierr.New(apierr.BadDigest, "the body doesn't match Content-MD5")
	}
	o := meta.Object{BucketID: in.BucketID, Key: in.Key, Size: in.Size, ETag: hex.EncodeToString(sum),
		ContentType: in.Attrs.ContentType, Headers: in.Attrs.Headers, Metadata: in.Attrs.Metadata}
	return e.commitBlob(ctx, tmp, o, res, func(o meta.Object) (meta.Object, error) {
		return e.meta.PutObject(ctx, o, blobRel, e.gcAfter())
	})
}

// commitBlob gives a finished file a blob id, moves it into place and runs
// record. Placing and recording hold the commit lock shared, so the orphan
// sweep never sees a placed file before its row. A failed record removes
// the file.
func (e *Engine) commitBlob(ctx context.Context, tmp string, o meta.Object, res *reservation, record func(meta.Object) (meta.Object, error)) (meta.Object, error) {
	o.BlobID = ids.New(ids.Blob)
	e.objCommit.RLock()
	defer e.objCommit.RUnlock()
	final, err := e.place(tmp, o.BucketID, o.BlobID)
	if err != nil {
		return meta.Object{}, err
	}
	var out meta.Object
	err = res.settle(func() error {
		var err error
		out, err = record(o)
		return err
	})
	if err != nil {
		_ = os.Remove(final)
		return meta.Object{}, err
	}
	return out, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- reading

// HeadObject returns an object's record.
func (e *Engine) HeadObject(ctx context.Context, bucketID, key string) (meta.Object, error) {
	if e.refused != nil {
		return meta.Object{}, e.refused
	}
	if err := checkKey(key); err != nil {
		return meta.Object{}, apierr.New(apierr.ObjectNotFound, "no object with that key")
	}
	return e.meta.GetObject(ctx, bucketID, key)
}

// OpenObject returns an object's record and its open file. The file stays
// readable even if the object is replaced meanwhile. Close it.
func (e *Engine) OpenObject(ctx context.Context, bucketID, key string) (meta.Object, *os.File, error) {
	o, err := e.HeadObject(ctx, bucketID, key)
	if err != nil {
		return meta.Object{}, nil, err
	}
	f, err := os.Open(e.layout.BlobFile(o.BucketID, o.BlobID))
	if err != nil {
		return meta.Object{}, nil, fmt.Errorf("engine: object %s/%s has no file: %w", bucketID, o.BlobID, err)
	}
	return o, f, nil
}

// ---- deleting and copying

// DeleteObject removes an object. Deleting a key that doesn't exist
// succeeds, as in the S3 protocol. It reports whether one was there.
func (e *Engine) DeleteObject(ctx context.Context, bucketID, key string) (bool, error) {
	if e.refused != nil {
		return false, e.refused
	}
	if !ValidKey(key) {
		return false, nil
	}
	return e.meta.DeleteObject(ctx, bucketID, key, blobRel, e.gcAfter())
}

// CopyObjectInput copies an object, within a bucket or across buckets.
type CopyObjectInput struct {
	SrcBucketID, SrcKey string
	BucketID, Key       string
	// Replace sets Attrs on the copy instead of the source's.
	Replace bool
	Attrs   ObjectAttrs
	// Check runs on the source record before copying (conditional copy).
	Check func(src meta.Object) error
}

// CopyObject copies an object. Files are immutable, so the copy is a hard
// link when the file system allows, else a full copy.
func (e *Engine) CopyObject(ctx context.Context, in CopyObjectInput) (meta.Object, error) {
	if e.refused != nil {
		return meta.Object{}, e.refused
	}
	if err := checkKey(in.Key); err != nil {
		return meta.Object{}, err
	}
	src, err := e.HeadObject(ctx, in.SrcBucketID, in.SrcKey)
	if err != nil {
		return meta.Object{}, err
	}
	if in.Check != nil {
		if err := in.Check(src); err != nil {
			return meta.Object{}, err
		}
	}
	attrs := ObjectAttrs{ContentType: src.ContentType, Headers: src.Headers, Metadata: src.Metadata}
	if in.Replace {
		attrs = in.Attrs
		if err := CheckAttrs(&attrs); err != nil {
			return meta.Object{}, err
		}
	} else if in.SrcBucketID == in.BucketID && in.SrcKey == in.Key {
		return meta.Object{}, apierr.New(apierr.BadRequest, "copying an object onto itself needs new metadata (REPLACE)")
	}
	if src.Size > e.cfg.MaxObjectBytes {
		return meta.Object{}, apierr.Newf(apierr.ObjectTooLarge, "the object is %d bytes; the limit is %d", src.Size, e.cfg.MaxObjectBytes)
	}
	var credit int64
	if old, err := e.meta.GetObject(ctx, in.BucketID, in.Key); err == nil {
		credit = old.Size
	}
	res, err := e.reserveObjectBytes(ctx, in.BucketID, src.Size, credit)
	if err != nil {
		return meta.Object{}, err
	}
	defer res.release()
	tmp, err := e.cloneTemp(e.layout.BlobFile(src.BucketID, src.BlobID), src.Size)
	if err != nil {
		return meta.Object{}, err
	}
	defer os.Remove(tmp)
	o := meta.Object{BucketID: in.BucketID, Key: in.Key, Size: src.Size, ETag: src.ETag, Parts: src.Parts,
		ContentType: attrs.ContentType, Headers: attrs.Headers, Metadata: attrs.Metadata}
	return e.commitBlob(ctx, tmp, o, res, func(o meta.Object) (meta.Object, error) {
		return e.meta.PutObject(ctx, o, blobRel, e.gcAfter())
	})
}

// cloneTemp makes a temp file with the same content as src: a hard link
// when possible, otherwise a copy (which needs the disk space).
func (e *Engine) cloneTemp(src string, size int64) (string, error) {
	tmp := filepath.Join(e.layout.TmpDir(), "obj-"+ids.New(ids.Blob))
	if err := os.Link(src, tmp); err == nil {
		return tmp, nil
	}
	if err := e.checkDiskFor(size); err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	path, _, err := e.writeTemp(in, size)
	return path, err
}

// ---- listing

// ListObjectsInput is one page of a listing.
type ListObjectsInput struct {
	BucketID  string
	Prefix    string
	Delimiter string
	// After lists keys after this position (exclusive). When it falls inside
	// a common prefix already returned, the whole prefix is skipped.
	After string
	Max   int
}

// ListObjectsResult is one page. Next is the last key or common prefix
// returned, to pass as After for the next page.
type ListObjectsResult struct {
	Objects   []meta.Object
	Prefixes  []string
	Truncated bool
	Next      string
}

// prefixEnd returns the smallest string greater than every string starting
// with p, or "" for no bound. Valid UTF-8 never holds 0xff, so incrementing
// the last byte is enough.
func prefixEnd(p string) string {
	if p == "" {
		return ""
	}
	return p[:len(p)-1] + string([]byte{p[len(p)-1] + 1})
}

// skipPast is a position after every key that starts with p.
func skipPast(p string) string { return p + "\xff" }

// ListObjects lists keys in byte order, rolling keys that contain the
// delimiter after the prefix into common prefixes.
func (e *Engine) ListObjects(ctx context.Context, in ListObjectsInput) (ListObjectsResult, error) {
	if e.refused != nil {
		return ListObjectsResult{}, e.refused
	}
	out := ListObjectsResult{Objects: []meta.Object{}, Prefixes: []string{}}
	if in.Max <= 0 {
		return out, nil
	}
	upper := prefixEnd(in.Prefix)
	from, inclusive := in.Prefix, true
	if in.After != "" && in.After >= in.Prefix {
		from, inclusive = in.After, false
		if in.Delimiter != "" && strings.HasPrefix(in.After, in.Prefix) {
			if i := strings.Index(in.After[len(in.Prefix):], in.Delimiter); i >= 0 {
				from = skipPast(in.Prefix + in.After[len(in.Prefix):len(in.Prefix)+i+len(in.Delimiter)])
			}
		}
	}
	want := in.Max + 1 // one more than asked, to know whether it's truncated
	type item struct {
		key    string
		obj    *meta.Object
		prefix bool
	}
	var items []item
	for len(items) < want {
		batch := min(1000, want-len(items))
		rows, err := e.meta.ListObjectsFrom(ctx, in.BucketID, from, inclusive, upper, batch)
		if err != nil {
			return ListObjectsResult{}, err
		}
		jumped := false
		for i := range rows {
			o := rows[i]
			if in.Delimiter != "" {
				if j := strings.Index(o.Key[len(in.Prefix):], in.Delimiter); j >= 0 {
					cp := o.Key[:len(in.Prefix)+j+len(in.Delimiter)]
					items = append(items, item{key: cp, prefix: true})
					from, inclusive, jumped = skipPast(cp), false, true
					break
				}
			}
			items = append(items, item{key: o.Key, obj: &rows[i]})
			from, inclusive = o.Key, false
			if len(items) == want {
				break
			}
		}
		if !jumped && len(rows) < batch {
			break
		}
	}
	if len(items) > in.Max {
		out.Truncated = true
		items = items[:in.Max]
	}
	for _, it := range items {
		if it.prefix {
			out.Prefixes = append(out.Prefixes, it.key)
		} else {
			out.Objects = append(out.Objects, *it.obj)
		}
	}
	if out.Truncated && len(items) > 0 {
		out.Next = items[len(items)-1].key
	}
	return out, nil
}

// ---- access key secrets

// Sealer encrypts and decrypts S3 access key secrets.
func (e *Engine) Sealer() *auth.Sealer { return e.sealer }
