package meta

// Workspaces (PLAN 17, ADR 0008): a workspace owns databases, buckets and
// tokens. Lookups of those by name or id take the workspace, so a record of
// another workspace is never found.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
)

// WorkspaceLimits are a workspace's own caps. 0 means no cap of its own; the
// instance limits always apply on top.
type WorkspaceLimits struct {
	MaxDatabases     int // counting the trash
	MaxBuckets       int
	MaxDatabaseBytes int64 // branch files of live databases
	MaxObjectBytes   int64 // objects plus multipart parts in progress
}

// Workspace is one workspace record.
type Workspace struct {
	ID         string
	Name       string
	Default    bool
	Limits     WorkspaceLimits
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
	PurgeAfter *time.Time
}

const workspaceCols = `id, name, is_default, max_databases, max_buckets, max_database_bytes, max_object_bytes, created_at, updated_at, deleted_at, purge_after`

func scanWorkspace(r scanner) (Workspace, error) {
	var w Workspace
	var created, updated string
	var deleted, purge sql.NullString
	if err := r.Scan(&w.ID, &w.Name, &w.Default, &w.Limits.MaxDatabases, &w.Limits.MaxBuckets, &w.Limits.MaxDatabaseBytes, &w.Limits.MaxObjectBytes,
		&created, &updated, &deleted, &purge); err != nil {
		return Workspace{}, err
	}
	w.CreatedAt = *parseTime(sql.NullString{String: created, Valid: true})
	w.UpdatedAt = *parseTime(sql.NullString{String: updated, Valid: true})
	w.DeletedAt = parseTime(deleted)
	w.PurgeAfter = parseTime(purge)
	return w, nil
}

func noWorkspace(ref string) error {
	return apierr.Newf(apierr.WorkspaceNotFound, "no workspace %q", ref)
}

func (l WorkspaceLimits) valid() bool {
	return l.MaxDatabases >= 0 && l.MaxBuckets >= 0 && l.MaxDatabaseBytes >= 0 && l.MaxObjectBytes >= 0
}

// CreateWorkspace records a new workspace. It fails with limit_reached when
// max workspaces already exist (counting the trash), checked in the same
// transaction.
func (s *Store) CreateWorkspace(ctx context.Context, id, name string, limits WorkspaceLimits, max int) (Workspace, error) {
	if !ids.ValidName(name) {
		return Workspace{}, apierr.Newf(apierr.InvalidName, "invalid workspace name %q", name)
	}
	if !limits.valid() {
		return Workspace{}, apierr.New(apierr.BadRequest, "a limit can't be negative")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Workspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workspaces`).Scan(&n); err != nil {
		return Workspace{}, err
	}
	if n >= max {
		return Workspace{}, apierr.Newf(apierr.LimitReached, "the instance has the most workspaces allowed (%d)", max)
	}
	ts := now()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspaces (id, name, max_databases, max_buckets, max_database_bytes, max_object_bytes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, limits.MaxDatabases, limits.MaxBuckets, limits.MaxDatabaseBytes, limits.MaxObjectBytes, ts, ts); err != nil {
		if isUnique(err) {
			return Workspace{}, apierr.Newf(apierr.NameTaken, "a workspace named %q already exists", name)
		}
		return Workspace{}, err
	}
	if err := tx.Commit(); err != nil {
		return Workspace{}, err
	}
	return s.GetWorkspace(ctx, id)
}

// GetWorkspace finds a live workspace by name or id.
func (s *Store) GetWorkspace(ctx context.Context, ref string) (Workspace, error) {
	col := "name"
	if ids.IsID(ref, ids.Workspace) {
		col = "id"
	} else if !ids.ValidName(ref) {
		return Workspace{}, noWorkspace(ref)
	}
	w, err := scanWorkspace(s.db.QueryRowContext(ctx, `SELECT `+workspaceCols+` FROM workspaces WHERE `+col+` = ? AND deleted_at IS NULL`, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, noWorkspace(ref)
	}
	return w, err
}

// DefaultWorkspace returns the instance's default workspace. Migration 6
// makes it, and it can't be deleted, so it always exists.
func (s *Store) DefaultWorkspace(ctx context.Context) (Workspace, error) {
	return scanWorkspace(s.db.QueryRowContext(ctx, `SELECT `+workspaceCols+` FROM workspaces WHERE is_default = 1`))
}

// ListWorkspaces returns live workspaces by name.
func (s *Store) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+workspaceCols+` FROM workspaces WHERE deleted_at IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WorkspaceUpdate holds what to change; nil leaves a field as it is.
type WorkspaceUpdate struct {
	Name             *string
	MaxDatabases     *int
	MaxBuckets       *int
	MaxDatabaseBytes *int64
	MaxObjectBytes   *int64
}

// UpdateWorkspace renames a live workspace or changes its limits.
func (s *Store) UpdateWorkspace(ctx context.Context, id string, u WorkspaceUpdate) (Workspace, error) {
	w, err := s.GetWorkspace(ctx, id)
	if err != nil {
		return Workspace{}, err
	}
	if u.Name != nil {
		if !ids.ValidName(*u.Name) {
			return Workspace{}, apierr.Newf(apierr.InvalidName, "invalid workspace name %q", *u.Name)
		}
		w.Name = *u.Name
	}
	if u.MaxDatabases != nil {
		w.Limits.MaxDatabases = *u.MaxDatabases
	}
	if u.MaxBuckets != nil {
		w.Limits.MaxBuckets = *u.MaxBuckets
	}
	if u.MaxDatabaseBytes != nil {
		w.Limits.MaxDatabaseBytes = *u.MaxDatabaseBytes
	}
	if u.MaxObjectBytes != nil {
		w.Limits.MaxObjectBytes = *u.MaxObjectBytes
	}
	if !w.Limits.valid() {
		return Workspace{}, apierr.New(apierr.BadRequest, "a limit can't be negative")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE workspaces SET name = ?, max_databases = ?, max_buckets = ?, max_database_bytes = ?, max_object_bytes = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		w.Name, w.Limits.MaxDatabases, w.Limits.MaxBuckets, w.Limits.MaxDatabaseBytes, w.Limits.MaxObjectBytes, now(), w.ID)
	if isUnique(err) {
		return Workspace{}, apierr.Newf(apierr.NameTaken, "a workspace named %q already exists", w.Name)
	}
	if err != nil {
		return Workspace{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Workspace{}, noWorkspace(id)
	}
	return s.GetWorkspace(ctx, w.ID)
}

// LimitsOf returns a live workspace's own limits, for a quota check.
func (s *Store) LimitsOf(ctx context.Context, wsID string) (WorkspaceLimits, error) {
	var l WorkspaceLimits
	err := s.db.QueryRowContext(ctx,
		`SELECT max_databases, max_buckets, max_database_bytes, max_object_bytes FROM workspaces WHERE id = ? AND deleted_at IS NULL`, wsID).
		Scan(&l.MaxDatabases, &l.MaxBuckets, &l.MaxDatabaseBytes, &l.MaxObjectBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceLimits{}, noWorkspace(wsID)
	}
	return l, err
}

// WorkspaceCounts is what a workspace holds, from meta.db. Branch file sizes
// are measured by the engine, not recorded here.
type WorkspaceCounts struct {
	Databases   int // counting the trash
	Buckets     int
	Tokens      int   // live
	ObjectBytes int64 // objects plus multipart parts in progress
}

// CountWorkspace counts what a workspace holds.
func (s *Store) CountWorkspace(ctx context.Context, wsID string) (WorkspaceCounts, error) {
	var c WorkspaceCounts
	err := s.db.QueryRowContext(ctx, `
SELECT
  (SELECT count(*) FROM databases WHERE workspace_id = ?1),
  (SELECT count(*) FROM buckets WHERE workspace_id = ?1),
  (SELECT count(*) FROM tokens WHERE workspace_id = ?1 AND revoked_at IS NULL),
  coalesce((SELECT sum(size_bytes) FROM buckets WHERE workspace_id = ?1), 0) +
  coalesce((SELECT sum(p.size) FROM upload_parts p JOIN uploads u ON u.id = p.upload_id JOIN buckets b ON b.id = u.bucket_id WHERE b.workspace_id = ?1), 0)`,
		wsID).Scan(&c.Databases, &c.Buckets, &c.Tokens, &c.ObjectBytes)
	return c, err
}

// ---- trash, restore, purge (PLAN 17.6)

// ErrDefaultWorkspace means the default workspace was asked to go away.
var ErrDefaultWorkspace = apierr.New(apierr.Forbidden, "the default workspace can't be deleted")

// SoftDeleteWorkspace moves a live workspace to the trash. Its records stay
// as they are, tokens included: nothing resolves in a trashed workspace, so
// they stop working at once, and a restore brings them all back.
func (s *Store) SoftDeleteWorkspace(ctx context.Context, id string, purgeAfter time.Time) error {
	w, err := s.GetWorkspace(ctx, id)
	if err != nil {
		return err
	}
	if w.Default {
		return ErrDefaultWorkspace
	}
	ts := now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE workspaces SET deleted_at = ?, purge_after = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL AND is_default = 0`,
		ts, purgeAfter.UTC().Format(TimeLayout), ts, w.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return noWorkspace(id)
	}
	return nil
}

// GetDeletedWorkspace finds a workspace in the trash by id, or by name (the
// most recently deleted one with that name).
func (s *Store) GetDeletedWorkspace(ctx context.Context, ref string) (Workspace, error) {
	var row *sql.Row
	switch {
	case ids.IsID(ref, ids.Workspace):
		row = s.db.QueryRowContext(ctx, `SELECT `+workspaceCols+` FROM workspaces WHERE id = ? AND deleted_at IS NOT NULL`, ref)
	case ids.ValidName(ref):
		row = s.db.QueryRowContext(ctx, `SELECT `+workspaceCols+` FROM workspaces WHERE name = ? AND deleted_at IS NOT NULL ORDER BY deleted_at DESC LIMIT 1`, ref)
	default:
		return Workspace{}, apierr.Newf(apierr.WorkspaceNotFound, "no workspace %q in the trash", ref)
	}
	w, err := scanWorkspace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, apierr.Newf(apierr.WorkspaceNotFound, "no workspace %q in the trash", ref)
	}
	return w, err
}

// ListDeletedWorkspaces returns workspaces in the trash, most recently
// deleted first.
func (s *Store) ListDeletedWorkspaces(ctx context.Context) ([]Workspace, error) {
	return s.listWorkspaces(ctx, `deleted_at IS NOT NULL ORDER BY deleted_at DESC`)
}

// WorkspacesToPurge lists trashed workspaces whose retention has passed.
func (s *Store) WorkspacesToPurge(ctx context.Context, at time.Time) ([]Workspace, error) {
	return s.listWorkspaces(ctx, `deleted_at IS NOT NULL AND purge_after <= ?`, at.UTC().Format(TimeLayout))
}

func (s *Store) listWorkspaces(ctx context.Context, where string, args ...any) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+workspaceCols+` FROM workspaces WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// RestoreWorkspace takes a workspace out of the trash under name.
func (s *Store) RestoreWorkspace(ctx context.Context, id, name string) (Workspace, error) {
	if !ids.ValidName(name) {
		return Workspace{}, apierr.Newf(apierr.InvalidName, "invalid workspace name %q", name)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE workspaces SET name = ?, deleted_at = NULL, purge_after = NULL, updated_at = ? WHERE id = ? AND deleted_at IS NOT NULL`, name, now(), id)
	if isUnique(err) {
		return Workspace{}, apierr.Newf(apierr.NameTaken, "a workspace named %q already exists; restore it under another name", name)
	}
	if err != nil {
		return Workspace{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Workspace{}, apierr.Newf(apierr.WorkspaceNotFound, "no workspace %q in the trash", id)
	}
	return s.GetWorkspace(ctx, id)
}

// PurgedWorkspace is what a purged workspace held, by id, so the engine
// can remove its files.
type PurgedWorkspace struct {
	Databases []string // live and trashed
	Uploads   []string
}

// PurgeWorkspace deletes every record of a trashed workspace: its
// databases with their branches, snapshots, schedules and stats, its
// buckets with their objects and uploads, its tokens, and the workspace
// itself. Audit entries stay. It reports false, changing nothing, if the
// workspace isn't in the trash (restored meanwhile).
//
// Object files aren't listed: once their rows are gone the object sweep
// removes them after the GC delay, like any file meta.db doesn't know, so a
// backup running meanwhile can still copy them.
func (s *Store) PurgeWorkspace(ctx context.Context, id string) (PurgedWorkspace, bool, error) {
	var out PurgedWorkspace
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workspaces WHERE id = ? AND deleted_at IS NOT NULL AND is_default = 0`, id).Scan(&n); err != nil {
		return out, false, err
	}
	if n == 0 {
		return out, false, nil
	}
	if out.Databases, err = queryStrings(ctx, tx, `SELECT id FROM databases WHERE workspace_id = ?`, id); err != nil {
		return out, false, err
	}
	if out.Uploads, err = queryStrings(ctx, tx, `SELECT u.id FROM uploads u JOIN buckets b ON b.id = u.bucket_id WHERE b.workspace_id = ?`, id); err != nil {
		return out, false, err
	}
	const dbs = `(SELECT id FROM databases WHERE workspace_id = ?)`
	const bks = `(SELECT id FROM buckets WHERE workspace_id = ?)`
	for _, q := range []string{
		// Tokens first: they reference databases, and their grants go with them.
		`DELETE FROM tokens WHERE workspace_id = ?`,
		`DELETE FROM stats_minute WHERE branch_id IN (SELECT id FROM branches WHERE database_id IN ` + dbs + `)`,
		`DELETE FROM slow_queries WHERE branch_id IN (SELECT id FROM branches WHERE database_id IN ` + dbs + `)`,
		`DELETE FROM snapshot_schedules WHERE database_id IN ` + dbs,
		`DELETE FROM snapshots WHERE database_id IN ` + dbs,
		`DELETE FROM branches WHERE database_id IN ` + dbs,
		`DELETE FROM databases WHERE workspace_id = ?`,
		`DELETE FROM upload_parts WHERE upload_id IN (SELECT id FROM uploads WHERE bucket_id IN ` + bks + `)`,
		`DELETE FROM uploads WHERE bucket_id IN ` + bks,
		`DELETE FROM objects WHERE bucket_id IN ` + bks,
		`DELETE FROM buckets WHERE workspace_id = ?`,
		`DELETE FROM workspaces WHERE id = ? AND deleted_at IS NOT NULL AND is_default = 0`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return out, false, err
		}
	}
	return out, true, tx.Commit()
}

// ---- moving between workspaces

// MoveDatabase puts a live database into another live workspace. It fails
// if a live database there has the name, or the workspace has its most
// databases already. Every token that reached the database is revoked,
// whole: a token of the old workspace can't hold a grant across the
// boundary. Both happen in one transaction.
func (s *Store) MoveDatabase(ctx context.Context, dbID, fromWS, toWS string) (Database, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Database{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var own, limit int
	if err := tx.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM databases WHERE workspace_id = w.id), w.max_databases FROM workspaces w WHERE w.id = ? AND w.deleted_at IS NULL`,
		toWS).Scan(&own, &limit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Database{}, noWorkspace(toWS)
		}
		return Database{}, err
	}
	if limit > 0 && own >= limit {
		return Database{}, apierr.Newf(apierr.LimitReached, "the workspace has the most databases allowed (%d)", limit)
	}
	ts := now()
	res, err := tx.ExecContext(ctx, `UPDATE databases SET workspace_id = ?, updated_at = ? WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`, toWS, ts, dbID, fromWS)
	if isUnique(err) {
		return Database{}, apierr.New(apierr.NameTaken, "a database with that name already exists in the workspace")
	}
	if err != nil {
		return Database{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Database{}, apierr.Newf(apierr.DatabaseNotFound, "no database %q", dbID)
	}
	for _, q := range []string{
		`UPDATE tokens SET revoked_at = ? WHERE revoked_at IS NULL AND database_id = ?`,
		`UPDATE tokens SET revoked_at = ? WHERE revoked_at IS NULL AND id IN (SELECT token_id FROM token_grants WHERE database_id = ?)`,
	} {
		if _, err := tx.ExecContext(ctx, q, ts, dbID); err != nil {
			return Database{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Database{}, err
	}
	return s.DatabaseByID(ctx, dbID)
}

// MoveBucket puts a bucket into another live workspace, under the same
// rules as MoveDatabase. The workspace's own cap on object bytes is checked
// in the same transaction.
func (s *Store) MoveBucket(ctx context.Context, bucketID, fromWS, toWS string) (Bucket, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Bucket{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var own, limit int
	var used, byteLimit, size int64
	if err := tx.QueryRowContext(ctx, `
SELECT (SELECT count(*) FROM buckets WHERE workspace_id = w.id), w.max_buckets,
       coalesce((SELECT sum(size_bytes) FROM buckets WHERE workspace_id = w.id), 0) +
       coalesce((SELECT sum(p.size) FROM upload_parts p JOIN uploads u ON u.id = p.upload_id JOIN buckets b ON b.id = u.bucket_id WHERE b.workspace_id = w.id), 0),
       w.max_object_bytes,
       coalesce((SELECT size_bytes FROM buckets WHERE id = ?1), 0) +
       coalesce((SELECT sum(p.size) FROM upload_parts p JOIN uploads u ON u.id = p.upload_id WHERE u.bucket_id = ?1), 0)
FROM workspaces w WHERE w.id = ?2 AND w.deleted_at IS NULL`, bucketID, toWS).Scan(&own, &limit, &used, &byteLimit, &size); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Bucket{}, noWorkspace(toWS)
		}
		return Bucket{}, err
	}
	if limit > 0 && own >= limit {
		return Bucket{}, apierr.Newf(apierr.LimitReached, "the workspace has the most buckets allowed (%d)", limit)
	}
	if byteLimit > 0 && used+size > byteLimit {
		return Bucket{}, apierr.Newf(apierr.WorkspaceQuotaExceeded, "the bucket's %d bytes would pass the workspace's %d byte limit on its buckets", size, byteLimit)
	}
	ts := now()
	res, err := tx.ExecContext(ctx, `UPDATE buckets SET workspace_id = ?, updated_at = ? WHERE id = ? AND workspace_id = ?`, toWS, ts, bucketID, fromWS)
	if isUnique(err) {
		return Bucket{}, apierr.New(apierr.NameTaken, "a bucket with that name already exists in the workspace")
	}
	if err != nil {
		return Bucket{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Bucket{}, apierr.Newf(apierr.BucketNotFound, "no bucket %q", bucketID)
	}
	for _, q := range []string{
		`UPDATE tokens SET revoked_at = ? WHERE revoked_at IS NULL AND bucket_id = ?`,
		`UPDATE tokens SET revoked_at = ? WHERE revoked_at IS NULL AND id IN (SELECT token_id FROM token_grants WHERE bucket_id = ?)`,
	} {
		if _, err := tx.ExecContext(ctx, q, ts, bucketID); err != nil {
			return Bucket{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Bucket{}, err
	}
	return s.BucketByID(ctx, bucketID)
}
