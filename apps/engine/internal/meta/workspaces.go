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
