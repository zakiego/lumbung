package engine

// Multipart uploads (PLAN 16.2). Parts are files under uploads/<upload id>/,
// recorded in meta.db. Completing concatenates the listed parts into one
// new object file; parts not listed are discarded.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// CreateUpload starts a multipart upload.
func (e *Engine) CreateUpload(ctx context.Context, bucketID, key string, attrs ObjectAttrs, tokenID string) (meta.Upload, error) {
	if e.refused != nil {
		return meta.Upload{}, e.refused
	}
	if err := checkKey(key); err != nil {
		return meta.Upload{}, err
	}
	if err := CheckAttrs(&attrs); err != nil {
		return meta.Upload{}, err
	}
	if err := e.checkDisk(); err != nil {
		return meta.Upload{}, err
	}
	return e.meta.CreateUpload(ctx, meta.Upload{ID: ids.New(ids.Upload), BucketID: bucketID, Key: key,
		ContentType: attrs.ContentType, Headers: attrs.Headers, Metadata: attrs.Metadata, TokenID: tokenID}, maxUploadsPerBucket)
}

// GetUpload finds an upload in progress for this bucket and key.
func (e *Engine) GetUpload(ctx context.Context, uploadID, bucketID, key string) (meta.Upload, error) {
	if e.refused != nil {
		return meta.Upload{}, e.refused
	}
	if !ids.IsID(uploadID, ids.Upload) || !ValidKey(key) {
		return meta.Upload{}, apierr.New(apierr.UploadNotFound, "no such multipart upload")
	}
	return e.meta.GetUpload(ctx, uploadID, bucketID, key)
}

func partRel(uploadID, file string) string { return path.Join("uploads", uploadID, file) }

// UploadPartInput is one part.
type UploadPartInput struct {
	UploadID, BucketID, Key string
	Number                  int
	Size                    int64
	Body                    io.Reader
	ContentMD5              []byte
}

// UploadPart stores one part and returns its ETag (MD5 hex). Uploading the
// same number again replaces the part.
func (e *Engine) UploadPart(ctx context.Context, in UploadPartInput) (string, error) {
	u, err := e.GetUpload(ctx, in.UploadID, in.BucketID, in.Key)
	if err != nil {
		return "", err
	}
	if in.Number < 1 || in.Number > MaxParts {
		return "", apierr.Newf(apierr.InvalidPart, "part numbers run from 1 to %d", MaxParts)
	}
	if in.Size < 0 {
		return "", apierr.New(apierr.LengthRequired, "the part needs a length")
	}
	if in.Size > MaxPartBytes || in.Size > e.cfg.MaxObjectBytes {
		return "", apierr.Newf(apierr.ObjectTooLarge, "the part is %d bytes; the limit is %d", in.Size, min(int64(MaxPartBytes), e.cfg.MaxObjectBytes))
	}
	res, err := e.reserveObjectBytes(ctx, u.BucketID, in.Size, 0)
	if err != nil {
		return "", err
	}
	defer res.release()
	if err := e.checkDiskFor(in.Size); err != nil {
		return "", err
	}
	tmp, sum, err := e.writeTemp(in.Body, in.Size)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	if in.ContentMD5 != nil && !equalBytes(in.ContentMD5, sum) {
		return "", apierr.New(apierr.BadDigest, "the body doesn't match Content-MD5")
	}
	dir := e.layout.UploadDir(u.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file := strconv.Itoa(in.Number) + "-" + strings.TrimPrefix(ids.New(ids.Blob), "ob_")
	final := filepath.Join(dir, file)
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	etag := hex.EncodeToString(sum)
	err = res.settle(func() error {
		return e.meta.PutPart(ctx, meta.Part{UploadID: u.ID, Number: in.Number, File: file, Size: in.Size, ETag: etag},
			func(old string) string { return partRel(u.ID, old) }, e.gcAfter())
	})
	if err != nil {
		_ = os.Remove(final)
		return "", err
	}
	return etag, nil
}

// CompletedPart is a part named in a completion request.
type CompletedPart struct {
	Number int
	ETag   string
}

func normETag(s string) string { return strings.ToLower(strings.Trim(strings.TrimSpace(s), `"`)) }

// CompleteUpload assembles the listed parts, in order, into the object.
// The ETag is the MD5 of the parts' MD5s with a -<count> suffix.
func (e *Engine) CompleteUpload(ctx context.Context, uploadID, bucketID, key string, list []CompletedPart) (meta.Object, error) {
	u, err := e.GetUpload(ctx, uploadID, bucketID, key)
	if err != nil {
		return meta.Object{}, err
	}
	if len(list) == 0 {
		return meta.Object{}, apierr.New(apierr.InvalidPart, "list at least one part")
	}
	if len(list) > MaxParts {
		return meta.Object{}, apierr.Newf(apierr.InvalidPart, "at most %d parts", MaxParts)
	}
	stored, err := e.meta.ListParts(ctx, u.ID, 0, MaxParts)
	if err != nil {
		return meta.Object{}, err
	}
	byNum := map[int]meta.Part{}
	for _, p := range stored {
		byNum[p.Number] = p
	}
	parts := make([]meta.Part, 0, len(list))
	var total int64
	digests := md5.New()
	for i, c := range list {
		if i > 0 && c.Number <= list[i-1].Number {
			return meta.Object{}, apierr.New(apierr.InvalidPart, "list the parts in ascending order, each once")
		}
		p, ok := byNum[c.Number]
		if !ok || normETag(c.ETag) != p.ETag {
			return meta.Object{}, apierr.Newf(apierr.InvalidPart, "part %d wasn't uploaded or its ETag doesn't match", c.Number)
		}
		if i < len(list)-1 && p.Size < MinPartBytes {
			return meta.Object{}, apierr.Newf(apierr.PartTooSmall, "part %d is %d bytes; every part but the last must be at least %d", c.Number, p.Size, MinPartBytes)
		}
		raw, _ := hex.DecodeString(p.ETag)
		digests.Write(raw)
		total += p.Size
		parts = append(parts, p)
	}
	if total > e.cfg.MaxObjectBytes {
		return meta.Object{}, apierr.Newf(apierr.ObjectTooLarge, "the object would be %d bytes; the limit is %d", total, e.cfg.MaxObjectBytes)
	}
	if err := e.checkDiskFor(total); err != nil {
		return meta.Object{}, err
	}
	tmp, err := e.concatParts(ctx, u.ID, parts, total)
	if err != nil {
		return meta.Object{}, err
	}
	defer os.Remove(tmp)
	o := meta.Object{BucketID: u.BucketID, Key: u.Key, Size: total,
		ETag:        fmt.Sprintf("%s-%d", hex.EncodeToString(digests.Sum(nil)), len(parts)),
		ContentType: u.ContentType, Headers: u.Headers, Metadata: u.Metadata, Parts: len(parts)}
	out, err := e.commitBlob(ctx, tmp, o, nil, func(o meta.Object) (meta.Object, error) {
		return e.meta.CompleteUpload(ctx, u.ID, o, blobRel, e.gcAfter())
	})
	if err != nil {
		return meta.Object{}, err
	}
	_ = os.RemoveAll(e.layout.UploadDir(u.ID))
	return out, nil
}

// concatParts writes the parts one after another into a new temp file.
func (e *Engine) concatParts(ctx context.Context, uploadID string, parts []meta.Part, total int64) (string, error) {
	readers := make([]io.Reader, 0, len(parts))
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	for _, p := range parts {
		f, err := os.Open(filepath.Join(e.layout.UploadDir(uploadID), filepath.Base(p.File)))
		if err != nil {
			return "", fmt.Errorf("engine: part %d of %s: %w", p.Number, uploadID, err)
		}
		files = append(files, f)
		readers = append(readers, io.LimitReader(f, p.Size))
	}
	tmp, _, err := e.writeTemp(stopReader{ctx, io.MultiReader(readers...)}, total)
	return tmp, err
}

// stopReader stops a long copy when the request ends.
type stopReader struct {
	ctx context.Context
	r   io.Reader
}

func (c stopReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// AbortUpload ends an upload and removes its parts.
func (e *Engine) AbortUpload(ctx context.Context, uploadID, bucketID, key string) error {
	u, err := e.GetUpload(ctx, uploadID, bucketID, key)
	if err != nil {
		return err
	}
	if _, err := e.meta.AbortUpload(ctx, u.ID); err != nil {
		return err
	}
	return os.RemoveAll(e.layout.UploadDir(u.ID))
}

// ListParts returns an upload's parts after a part number.
func (e *Engine) ListParts(ctx context.Context, uploadID, bucketID, key string, after, max int) ([]meta.Part, bool, error) {
	u, err := e.GetUpload(ctx, uploadID, bucketID, key)
	if err != nil {
		return nil, false, err
	}
	parts, err := e.meta.ListParts(ctx, u.ID, after, max+1)
	if err != nil {
		return nil, false, err
	}
	if len(parts) > max {
		return parts[:max], true, nil
	}
	return parts, false, nil
}

// ListUploads returns a bucket's uploads in progress.
func (e *Engine) ListUploads(ctx context.Context, bucketID, prefix, keyMarker, idMarker string, max int) ([]meta.Upload, bool, error) {
	if e.refused != nil {
		return nil, false, e.refused
	}
	ups, err := e.meta.ListUploads(ctx, bucketID, prefix, keyMarker, idMarker, max+1)
	if err != nil {
		return nil, false, err
	}
	if len(ups) > max {
		return ups[:max], true, nil
	}
	return ups, false, nil
}

// ---- housekeeping

// objectHousekeeping removes files whose GC delay passed, aborts expired
// uploads and, hourly, sweeps files no record knows.
func (e *Engine) objectHousekeeping(ctx context.Context, now time.Time) {
	e.runRemovals(ctx, now)
	ups, err := e.meta.ExpiredUploads(ctx, now.Add(-e.cfg.MultipartExpiry))
	if err != nil {
		e.log.Error("list expired uploads", "err", err)
	}
	for _, u := range ups {
		if _, err := e.meta.AbortUpload(ctx, u.ID); err != nil {
			e.log.Error("abort expired upload", "upload", u.ID, "err", err)
			continue
		}
		_ = os.RemoveAll(e.layout.UploadDir(u.ID))
	}
	if now.Sub(e.lastSweep) >= time.Hour {
		e.lastSweep = now
		if err := e.sweepObjects(ctx, now, e.cfg.ObjectGCDelay); err != nil {
			e.log.Error("sweep object files", "err", err)
		}
	}
}

// validRemoval reports whether a queued path is one the engine queues: an
// object file or a part file, so a tampered meta.db can't make the
// housekeeping remove anything else.
func validRemoval(rel string) bool {
	parts := strings.Split(rel, "/")
	if path.Clean(rel) != rel {
		return false
	}
	switch {
	case len(parts) == 4 && parts[0] == "objects":
		return ids.IsID(parts[1], ids.Bucket) && ids.IsID(parts[3], ids.Blob) && parts[2] == parts[3][3:5]
	case len(parts) == 3 && parts[0] == "uploads":
		num, rest, ok := strings.Cut(parts[2], "-")
		n, err := strconv.Atoi(num)
		return ids.IsID(parts[1], ids.Upload) && ok && err == nil && n >= 1 && n <= MaxParts && ids.IsID("ob_"+rest, ids.Blob)
	}
	return false
}

// runRemovals deletes files whose removal time has come.
func (e *Engine) runRemovals(ctx context.Context, now time.Time) {
	for {
		due, err := e.meta.DueRemovals(ctx, now, 500)
		if err != nil {
			e.log.Error("list due removals", "err", err)
			return
		}
		for _, rel := range due {
			if validRemoval(rel) {
				if err := os.Remove(filepath.Join(e.layout.Root, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
					e.log.Error("remove object file", "path", rel, "err", err)
					continue
				}
			} else {
				e.log.Warn("ignoring an unexpected path queued for removal", "path", rel)
			}
			if err := e.meta.ForgetRemoval(ctx, rel); err != nil {
				e.log.Error("forget removal", "path", rel, "err", err)
				return
			}
		}
		if len(due) < 500 || ctx.Err() != nil {
			return
		}
	}
}

// sweepObjects removes object files and upload folders no record knows and
// nothing queues, once they are older than minAge (a crash between writing
// a file and recording it leaves one behind). Folders of buckets that no
// longer exist are removed once empty.
func (e *Engine) sweepObjects(ctx context.Context, now time.Time, minAge time.Duration) error {
	e.objCommit.Lock()
	defer e.objCommit.Unlock()
	live, err := e.meta.LiveBlobs(ctx)
	if err != nil {
		return err
	}
	queued, err := e.meta.QueuedRemovals(ctx)
	if err != nil {
		return err
	}
	uploads, err := e.meta.LiveUploads(ctx)
	if err != nil {
		return err
	}
	old := func(fi os.FileInfo) bool { return now.Sub(fi.ModTime()) >= minAge }
	buckets, _ := os.ReadDir(e.layout.ObjectsDir())
	for _, b := range buckets {
		if !b.IsDir() || !ids.IsID(b.Name(), ids.Bucket) {
			continue
		}
		bdir := filepath.Join(e.layout.ObjectsDir(), b.Name())
		shards, _ := os.ReadDir(bdir)
		for _, s := range shards {
			if !s.IsDir() || len(s.Name()) != 2 {
				continue
			}
			sdir := filepath.Join(bdir, s.Name())
			files, _ := os.ReadDir(sdir)
			for _, f := range files {
				rel := blobRelOrEmpty(b.Name(), f.Name())
				if rel == "" || live[f.Name()] || queued[rel] {
					continue
				}
				if fi, err := f.Info(); err == nil && old(fi) {
					e.log.Warn("removing orphan object file", "path", rel)
					_ = os.Remove(filepath.Join(sdir, f.Name()))
				}
			}
			_ = os.Remove(sdir) // only succeeds when empty
		}
		if _, err := e.meta.GetBucket(ctx, b.Name()); err != nil {
			_ = os.Remove(bdir) // a deleted bucket's folder, once empty
		}
	}
	dirs, _ := os.ReadDir(e.layout.UploadsDir())
	for _, d := range dirs {
		if !d.IsDir() || !ids.IsID(d.Name(), ids.Upload) || uploads[d.Name()] {
			continue
		}
		if fi, err := d.Info(); err == nil && old(fi) {
			_ = os.RemoveAll(filepath.Join(e.layout.UploadsDir(), d.Name()))
		}
	}
	return nil
}

func blobRelOrEmpty(bucketID, name string) string {
	if !ids.IsID(name, ids.Blob) {
		return ""
	}
	return blobRel(bucketID, name)
}
