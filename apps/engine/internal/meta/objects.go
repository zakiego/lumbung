package meta

// Object storage records (PLAN 16, ADR 0006): buckets, objects, multipart
// uploads and their parts, and files waiting for delayed removal. Object
// content lives in files named by ids; these rows are the index.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
)

// Bucket is one bucket record.
type Bucket struct {
	ID          string
	WorkspaceID string
	Name        string
	PublicRead  bool
	CORSOrigins []string
	SizeBytes   int64 // objects only; uploads in progress are counted apart
	ObjectCount int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Object is one object record.
type Object struct {
	BucketID    string
	Key         string
	BlobID      string
	Size        int64
	ETag        string // without quotes
	ContentType string
	Headers     map[string]string // cache-control, content-disposition, ...
	Metadata    map[string]string // x-amz-meta-* without the prefix, lowercase
	Parts       int               // 0 for a single upload
	ModifiedAt  time.Time
}

// Upload is a multipart upload in progress.
type Upload struct {
	ID          string
	BucketID    string
	Key         string
	ContentType string
	Headers     map[string]string
	Metadata    map[string]string
	TokenID     string
	CreatedAt   time.Time
}

// Part is one uploaded part.
type Part struct {
	UploadID  string
	Number    int
	File      string // file name inside the upload's folder
	Size      int64
	ETag      string // MD5 hex, without quotes
	CreatedAt time.Time
}

const bucketCols = `id, workspace_id, name, public_read, cors_origins, size_bytes, object_count, created_at, updated_at`

func scanBucket(r scanner) (Bucket, error) {
	var b Bucket
	var cors, created, updated string
	if err := r.Scan(&b.ID, &b.WorkspaceID, &b.Name, &b.PublicRead, &cors, &b.SizeBytes, &b.ObjectCount, &created, &updated); err != nil {
		return Bucket{}, err
	}
	if err := json.Unmarshal([]byte(cors), &b.CORSOrigins); err != nil {
		return Bucket{}, fmt.Errorf("meta: bucket %s cors_origins: %w", b.ID, err)
	}
	if b.CORSOrigins == nil {
		b.CORSOrigins = []string{}
	}
	b.CreatedAt = *parseTime(sql.NullString{String: created, Valid: true})
	b.UpdatedAt = *parseTime(sql.NullString{String: updated, Valid: true})
	return b, nil
}

func jsonText(v any) string {
	if m, ok := v.(map[string]string); ok && m == nil {
		return "{}"
	}
	if l, ok := v.([]string); ok && l == nil {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // maps and lists of strings always marshal
	}
	return string(b)
}

func jsonMap(s string) (map[string]string, error) {
	m := map[string]string{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// CreateBucket records a new bucket in a workspace. It fails with
// limit_reached when the instance already has max buckets or the workspace
// its own cap, counted in the same transaction.
func (s *Store) CreateBucket(ctx context.Context, wsID, id, name string, max int) (Bucket, error) {
	if !ids.ValidBucketName(name) {
		return Bucket{}, apierr.Newf(apierr.InvalidName, "invalid bucket name %q", name)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Bucket{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM buckets`).Scan(&n); err != nil {
		return Bucket{}, err
	}
	if n >= max {
		return Bucket{}, apierr.Newf(apierr.LimitReached, "the instance has the most buckets allowed (%d)", max)
	}
	var own, limit int
	if err := tx.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM buckets WHERE workspace_id = w.id), w.max_buckets FROM workspaces w WHERE w.id = ? AND w.deleted_at IS NULL`,
		wsID).Scan(&own, &limit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Bucket{}, noWorkspace(wsID)
		}
		return Bucket{}, err
	}
	if limit > 0 && own >= limit {
		return Bucket{}, apierr.Newf(apierr.LimitReached, "the workspace has the most buckets allowed (%d)", limit)
	}
	ts := now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO buckets (id, workspace_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, id, wsID, name, ts, ts); err != nil {
		if isUnique(err) {
			return Bucket{}, apierr.Newf(apierr.NameTaken, "a bucket named %q already exists", name)
		}
		return Bucket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Bucket{}, err
	}
	return s.BucketByID(ctx, id)
}

// GetBucket finds a bucket of a workspace by name or id. A bucket of
// another workspace is not found, by id as much as by name.
func (s *Store) GetBucket(ctx context.Context, wsID, ref string) (Bucket, error) {
	col := "name"
	if ids.IsID(ref, ids.Bucket) {
		col = "id"
	} else if !ids.ValidBucketName(ref) {
		return Bucket{}, apierr.Newf(apierr.BucketNotFound, "no bucket %q", ref)
	}
	b, err := scanBucket(s.db.QueryRowContext(ctx, `SELECT `+bucketCols+` FROM buckets WHERE workspace_id = ? AND `+col+` = ?`, wsID, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return Bucket{}, apierr.Newf(apierr.BucketNotFound, "no bucket %q", ref)
	}
	return b, err
}

// BucketByID finds a bucket by id alone, in any workspace. It is for
// re-reading a bucket the caller already resolved, and for the engine's
// own jobs. A request never resolves a bucket this way.
func (s *Store) BucketByID(ctx context.Context, id string) (Bucket, error) {
	b, err := scanBucket(s.db.QueryRowContext(ctx, `SELECT `+bucketCols+` FROM buckets WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Bucket{}, apierr.Newf(apierr.BucketNotFound, "no bucket %q", id)
	}
	return b, err
}

// ListBuckets returns a workspace's buckets by name.
func (s *Store) ListBuckets(ctx context.Context, wsID string) ([]Bucket, error) {
	return s.listBuckets(ctx, `WHERE workspace_id = ? ORDER BY name`, wsID)
}

// ListAllBuckets returns the buckets of every workspace, for the engine's
// own jobs and backups.
func (s *Store) ListAllBuckets(ctx context.Context) ([]Bucket, error) {
	return s.listBuckets(ctx, `ORDER BY name`)
}

func (s *Store) listBuckets(ctx context.Context, where string, args ...any) ([]Bucket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+bucketCols+` FROM buckets `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bucket{}
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BucketUpdate holds the settings to change; nil leaves one as it is.
type BucketUpdate struct {
	Name        *string
	PublicRead  *bool
	CORSOrigins *[]string
}

// UpdateBucket changes a bucket's settings.
func (s *Store) UpdateBucket(ctx context.Context, id string, u BucketUpdate) (Bucket, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Bucket{}, err
	}
	defer func() { _ = tx.Rollback() }()
	ts := now()
	exec := func(q string, args ...any) error {
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return apierr.Newf(apierr.BucketNotFound, "no bucket %q", id)
		}
		return nil
	}
	if u.Name != nil {
		if !ids.ValidBucketName(*u.Name) {
			return Bucket{}, apierr.Newf(apierr.InvalidName, "invalid bucket name %q", *u.Name)
		}
		if err := exec(`UPDATE buckets SET name = ?, updated_at = ? WHERE id = ?`, *u.Name, ts, id); err != nil {
			if isUnique(err) {
				return Bucket{}, apierr.Newf(apierr.NameTaken, "a bucket named %q already exists", *u.Name)
			}
			return Bucket{}, err
		}
	}
	if u.PublicRead != nil {
		if err := exec(`UPDATE buckets SET public_read = ?, updated_at = ? WHERE id = ?`, *u.PublicRead, ts, id); err != nil {
			return Bucket{}, err
		}
	}
	if u.CORSOrigins != nil {
		if err := exec(`UPDATE buckets SET cors_origins = ?, updated_at = ? WHERE id = ?`, jsonText(*u.CORSOrigins), ts, id); err != nil {
			return Bucket{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Bucket{}, err
	}
	return s.BucketByID(ctx, id)
}

// DeleteBucket removes an empty bucket, aborts its uploads and revokes its
// tokens. It returns the upload ids whose folders the caller removes.
func (s *Store) DeleteBucket(ctx context.Context, id string) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM objects WHERE bucket_id = ? LIMIT 1`, id).Scan(&one)
	if err == nil {
		return nil, apierr.New(apierr.BucketNotEmpty, "the bucket still holds objects; delete them first")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	uploads, err := queryStrings(ctx, tx, `SELECT id FROM uploads WHERE bucket_id = ?`, id)
	if err != nil {
		return nil, err
	}
	ts := now()
	for _, q := range []string{
		`DELETE FROM upload_parts WHERE upload_id IN (SELECT id FROM uploads WHERE bucket_id = ?)`,
		`DELETE FROM uploads WHERE bucket_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tokens SET revoked_at = ? WHERE bucket_id = ? AND revoked_at IS NULL`, ts, id); err != nil {
		return nil, err
	}
	// A token that reaches this bucket among other things goes with it.
	if _, err := tx.ExecContext(ctx, `UPDATE tokens SET revoked_at = ? WHERE revoked_at IS NULL AND id IN (SELECT token_id FROM token_grants WHERE bucket_id = ?)`, ts, id); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM buckets WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, apierr.Newf(apierr.BucketNotFound, "no bucket %q", id)
	}
	return uploads, tx.Commit()
}

type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

func queryStrings(ctx context.Context, db querier, q string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ObjectUsage is how many bytes buckets hold: objects plus parts of uploads
// in progress, for one bucket and for the instance.
func (s *Store) ObjectUsage(ctx context.Context, bucketID string) (bucket, total int64, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT
  coalesce((SELECT size_bytes FROM buckets WHERE id = ?1), 0) +
  coalesce((SELECT sum(p.size) FROM upload_parts p JOIN uploads u ON u.id = p.upload_id WHERE u.bucket_id = ?1), 0),
  coalesce((SELECT sum(size_bytes) FROM buckets), 0) +
  coalesce((SELECT sum(size) FROM upload_parts), 0)`, bucketID).Scan(&bucket, &total)
	return bucket, total, err
}

const objectCols = `bucket_id, key, blob_id, size, etag, content_type, headers, metadata, parts, modified_at`

func scanObject(r scanner) (Object, error) {
	var o Object
	var headers, md, modified string
	if err := r.Scan(&o.BucketID, &o.Key, &o.BlobID, &o.Size, &o.ETag, &o.ContentType, &headers, &md, &o.Parts, &modified); err != nil {
		return Object{}, err
	}
	var err error
	if o.Headers, err = jsonMap(headers); err != nil {
		return Object{}, err
	}
	if o.Metadata, err = jsonMap(md); err != nil {
		return Object{}, err
	}
	o.ModifiedAt = *parseTime(sql.NullString{String: modified, Valid: true})
	return o, nil
}

// GetObject finds an object by bucket id and key.
func (s *Store) GetObject(ctx context.Context, bucketID, key string) (Object, error) {
	o, err := scanObject(s.db.QueryRowContext(ctx, `SELECT `+objectCols+` FROM objects WHERE bucket_id = ? AND key = ?`, bucketID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Object{}, apierr.New(apierr.ObjectNotFound, "no object with that key")
	}
	return o, err
}

// queueRemoval schedules a file or folder (relative to the data folder) for
// removal after delay.
func queueRemoval(ctx context.Context, tx *sql.Tx, rel string, after time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO blob_gc (path, delete_after) VALUES (?, ?) ON CONFLICT (path) DO UPDATE SET delete_after = excluded.delete_after`,
		rel, after.UTC().Format(TimeLayout))
	return err
}

// BlobRel names an object file relative to the data folder. It matches
// storage.BlobRel; meta can't import storage (storage imports nothing
// engine-side, but keeping meta free of path building keeps it testable).
type BlobRel func(bucketID, blobID string) string

// WriteCond is a condition on the object a write replaces, checked in the
// write's transaction.
type WriteCond struct {
	IfNoneMatch bool     // no object may exist (If-None-Match: *)
	IfMatch     []string // the existing object's ETag must be one of these (* for any)
}

func (c *WriteCond) check(found bool, etag string) error {
	if c == nil {
		return nil
	}
	failed := apierr.New(apierr.PreconditionFailed, "at least one of the preconditions you specified did not hold")
	if c.IfNoneMatch && found {
		return failed
	}
	if len(c.IfMatch) > 0 {
		if !found {
			return failed
		}
		for _, e := range c.IfMatch {
			if e == "*" || e == etag {
				return nil
			}
		}
		return failed
	}
	return nil
}

// PutObject records an object, replacing any object with the same key. The
// replaced object's file is queued for removal after gcAfter. It fails with
// bucket_not_found when the bucket was deleted meanwhile; the caller then
// removes the new file. It returns the record as written; an error means
// nothing was committed.
func (s *Store) PutObject(ctx context.Context, o Object, rel BlobRel, gcAfter time.Time, cond *WriteCond) (Object, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Object{}, err
	}
	defer func() { _ = tx.Rollback() }()
	out, err := s.putObjectTx(ctx, tx, o, rel, gcAfter, cond)
	if err != nil {
		return Object{}, err
	}
	return out, tx.Commit()
}

// putObjectTx writes an object's rows and returns the record as written.
func (s *Store) putObjectTx(ctx context.Context, tx *sql.Tx, o Object, rel BlobRel, gcAfter time.Time, cond *WriteCond) (Object, error) {
	ts := now()
	o.ModifiedAt = *parseTime(sql.NullString{String: ts, Valid: true})
	if o.Headers == nil {
		o.Headers = map[string]string{}
	}
	if o.Metadata == nil {
		o.Metadata = map[string]string{}
	}
	return o, s.writeObjectRows(ctx, tx, o, ts, rel, gcAfter, cond)
}

func (s *Store) writeObjectRows(ctx context.Context, tx *sql.Tx, o Object, ts string, rel BlobRel, gcAfter time.Time, cond *WriteCond) error {
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM buckets WHERE id = ?`, o.BucketID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return apierr.New(apierr.BucketNotFound, "the bucket was deleted")
	} else if err != nil {
		return err
	}
	var oldBlob, oldETag string
	var oldSize int64
	err := tx.QueryRowContext(ctx, `SELECT blob_id, size, etag FROM objects WHERE bucket_id = ? AND key = ?`, o.BucketID, o.Key).Scan(&oldBlob, &oldSize, &oldETag)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := cond.check(found, oldETag); err != nil {
		return err
	}
	if found {
		if err := queueRemoval(ctx, tx, rel(o.BucketID, oldBlob), gcAfter); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO objects (`+objectCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (bucket_id, key) DO UPDATE SET blob_id = excluded.blob_id, size = excluded.size, etag = excluded.etag,
  content_type = excluded.content_type, headers = excluded.headers, metadata = excluded.metadata,
  parts = excluded.parts, modified_at = excluded.modified_at`,
		o.BucketID, o.Key, o.BlobID, o.Size, o.ETag, o.ContentType, jsonText(o.Headers), jsonText(o.Metadata), o.Parts, ts); err != nil {
		return err
	}
	count := int64(1)
	if found {
		count = 0
	}
	_, err = tx.ExecContext(ctx, `UPDATE buckets SET size_bytes = size_bytes + ?, object_count = object_count + ? WHERE id = ?`,
		o.Size-oldSize, count, o.BucketID)
	return err
}

// DeleteObject removes an object and queues its file for removal. It
// reports whether an object was there.
func (s *Store) DeleteObject(ctx context.Context, bucketID, key string, rel BlobRel, gcAfter time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var blob string
	var size int64
	err = tx.QueryRowContext(ctx, `DELETE FROM objects WHERE bucket_id = ? AND key = ? RETURNING blob_id, size`, bucketID, key).Scan(&blob, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := queueRemoval(ctx, tx, rel(bucketID, blob), gcAfter); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE buckets SET size_bytes = size_bytes - ?, object_count = object_count - 1 WHERE id = ?`, size, bucketID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ListObjectsFrom returns up to limit objects of a bucket with keys from
// `from` (inclusive or not) and, when upper isn't empty, before upper
// (exclusive), in byte order.
func (s *Store) ListObjectsFrom(ctx context.Context, bucketID, from string, inclusive bool, upper string, limit int) ([]Object, error) {
	op := ">"
	if inclusive {
		op = ">="
	}
	q := `SELECT ` + objectCols + ` FROM objects WHERE bucket_id = ? AND key ` + op + ` ?`
	args := []any{bucketID, from}
	if upper != "" {
		q += ` AND key < ?`
		args = append(args, upper)
	}
	q += ` ORDER BY key LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Object{}
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// LiveBlobs returns every object file's blob id, by bucket id.
func (s *Store) LiveBlobs(ctx context.Context) (map[string]bool, error) {
	ids, err := queryStrings(ctx, s.db, `SELECT blob_id FROM objects`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// QueuedRemovals returns every path waiting in blob_gc, due or not.
func (s *Store) QueuedRemovals(ctx context.Context) (map[string]bool, error) {
	paths, err := queryStrings(ctx, s.db, `SELECT path FROM blob_gc`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[p] = true
	}
	return out, nil
}

// DueRemovals returns paths whose removal time has come.
func (s *Store) DueRemovals(ctx context.Context, at time.Time, limit int) ([]string, error) {
	return queryStrings(ctx, s.db, `SELECT path FROM blob_gc WHERE delete_after <= ? ORDER BY delete_after LIMIT ?`,
		at.UTC().Format(TimeLayout), limit)
}

// ForgetRemoval drops a path from blob_gc once it is gone.
func (s *Store) ForgetRemoval(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM blob_gc WHERE path = ?`, path)
	return err
}

// ---- multipart uploads

const uploadCols = `id, bucket_id, key, content_type, headers, metadata, token_id, created_at`

func scanUpload(r scanner) (Upload, error) {
	var u Upload
	var headers, md, created string
	if err := r.Scan(&u.ID, &u.BucketID, &u.Key, &u.ContentType, &headers, &md, &u.TokenID, &created); err != nil {
		return Upload{}, err
	}
	var err error
	if u.Headers, err = jsonMap(headers); err != nil {
		return Upload{}, err
	}
	if u.Metadata, err = jsonMap(md); err != nil {
		return Upload{}, err
	}
	u.CreatedAt = *parseTime(sql.NullString{String: created, Valid: true})
	return u, nil
}

// CreateUpload records a multipart upload, unless the bucket already has
// max uploads in progress.
func (s *Store) CreateUpload(ctx context.Context, u Upload, max int) (Upload, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Upload{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM buckets WHERE id = ?`, u.BucketID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return Upload{}, apierr.New(apierr.BucketNotFound, "the bucket was deleted")
	} else if err != nil {
		return Upload{}, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM uploads WHERE bucket_id = ?`, u.BucketID).Scan(&n); err != nil {
		return Upload{}, err
	}
	if n >= max {
		return Upload{}, apierr.Newf(apierr.LimitReached, "the bucket has the most multipart uploads in progress allowed (%d)", max)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO uploads (`+uploadCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.BucketID, u.Key, u.ContentType, jsonText(u.Headers), jsonText(u.Metadata), u.TokenID, now()); err != nil {
		return Upload{}, err
	}
	if err := tx.Commit(); err != nil {
		return Upload{}, err
	}
	return s.GetUpload(ctx, u.ID, u.BucketID, u.Key)
}

// GetUpload finds an upload in progress. The bucket and key must match, so
// an upload id never works under another bucket or key.
func (s *Store) GetUpload(ctx context.Context, id, bucketID, key string) (Upload, error) {
	u, err := scanUpload(s.db.QueryRowContext(ctx, `SELECT `+uploadCols+` FROM uploads WHERE id = ? AND bucket_id = ? AND key = ?`, id, bucketID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, apierr.New(apierr.UploadNotFound, "no such multipart upload")
	}
	return u, err
}

// ListUploads returns a bucket's uploads in progress with keys starting with
// prefix, ordered by key then start time, after the (key, id) marker.
func (s *Store) ListUploads(ctx context.Context, bucketID, prefix, keyMarker, idMarker string, limit int) ([]Upload, error) {
	q := `SELECT ` + uploadCols + ` FROM uploads WHERE bucket_id = ? AND substr(key, 1, length(?)) = ?`
	args := []any{bucketID, prefix, prefix}
	if keyMarker != "" {
		if idMarker != "" {
			q += ` AND (key > ? OR (key = ? AND id > ?))`
			args = append(args, keyMarker, keyMarker, idMarker)
		} else {
			q += ` AND key > ?`
			args = append(args, keyMarker)
		}
	}
	q += ` ORDER BY key, id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Upload{}
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ExpiredUploads returns uploads started before cutoff.
func (s *Store) ExpiredUploads(ctx context.Context, cutoff time.Time) ([]Upload, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+uploadCols+` FROM uploads WHERE created_at < ?`, cutoff.UTC().Format(TimeLayout))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// LiveUploads returns the ids of every upload in progress.
func (s *Store) LiveUploads(ctx context.Context) (map[string]bool, error) {
	ids, err := queryStrings(ctx, s.db, `SELECT id FROM uploads`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// PutPart records a part, replacing one with the same number, whose file is
// queued for removal (relative path oldRel(file)). It fails with
// upload_not_found when the upload ended meanwhile.
func (s *Store) PutPart(ctx context.Context, p Part, oldRel func(file string) string, gcAfter time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM uploads WHERE id = ?`, p.UploadID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return apierr.New(apierr.UploadNotFound, "the multipart upload ended")
	} else if err != nil {
		return err
	}
	var old string
	err = tx.QueryRowContext(ctx, `SELECT file FROM upload_parts WHERE upload_id = ? AND part_number = ?`, p.UploadID, p.Number).Scan(&old)
	if err == nil {
		if err := queueRemoval(ctx, tx, oldRel(old), gcAfter); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO upload_parts (upload_id, part_number, file, size, etag, created_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (upload_id, part_number) DO UPDATE SET file = excluded.file, size = excluded.size, etag = excluded.etag, created_at = excluded.created_at`,
		p.UploadID, p.Number, p.File, p.Size, p.ETag, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// ListParts returns an upload's parts with numbers above after, in order.
func (s *Store) ListParts(ctx context.Context, uploadID string, after, limit int) ([]Part, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT upload_id, part_number, file, size, etag, created_at FROM upload_parts WHERE upload_id = ? AND part_number > ? ORDER BY part_number LIMIT ?`,
		uploadID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Part{}
	for rows.Next() {
		var p Part
		var created string
		if err := rows.Scan(&p.UploadID, &p.Number, &p.File, &p.Size, &p.ETag, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = *parseTime(sql.NullString{String: created, Valid: true})
		out = append(out, p)
	}
	return out, rows.Err()
}

// CompleteUpload records the assembled object and ends the upload in one
// transaction. It fails with upload_not_found when the upload already
// ended, so two completions can't both win.
func (s *Store) CompleteUpload(ctx context.Context, uploadID string, o Object, rel BlobRel, gcAfter time.Time, cond *WriteCond) (Object, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Object{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE id = ? AND bucket_id = ? AND key = ?`, uploadID, o.BucketID, o.Key)
	if err != nil {
		return Object{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Object{}, apierr.New(apierr.UploadNotFound, "no such multipart upload")
	}
	out, err := s.putObjectTx(ctx, tx, o, rel, gcAfter, cond)
	if err != nil {
		return Object{}, err
	}
	return out, tx.Commit()
}

// AbortUpload ends an upload and drops its parts. It reports whether the
// upload existed.
func (s *Store) AbortUpload(ctx context.Context, uploadID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, uploadID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
