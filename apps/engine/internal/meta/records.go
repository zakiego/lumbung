package meta

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ncruces/go-sqlite3"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
)

// Database is one database record.
type Database struct {
	ID              string
	Name            string
	DefaultBranchID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
	PurgeAfter      *time.Time
}

// Branch is one branch record.
type Branch struct {
	ID               string
	DatabaseID       string
	Name             string
	ParentID         string
	SourceSnapshotID string
	Protected        bool
	ExpiresAt        *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const dbCols = `id, name, default_branch_id, created_at, updated_at, deleted_at, purge_after`
const branchCols = `id, database_id, name, coalesce(parent_id, ''), coalesce(source_snapshot_id, ''), protected, expires_at, created_at, updated_at`

// branchColsB is branchCols for a query that aliases branches as b.
const branchColsB = `b.id, b.database_id, b.name, coalesce(b.parent_id, ''), coalesce(b.source_snapshot_id, ''), b.protected, b.expires_at, b.created_at, b.updated_at`

type scanner interface{ Scan(...any) error }

func scanDatabase(r scanner) (Database, error) {
	var d Database
	var created, updated string
	var deleted, purge sql.NullString
	if err := r.Scan(&d.ID, &d.Name, &d.DefaultBranchID, &created, &updated, &deleted, &purge); err != nil {
		return Database{}, err
	}
	d.CreatedAt = *parseTime(sql.NullString{String: created, Valid: true})
	d.UpdatedAt = *parseTime(sql.NullString{String: updated, Valid: true})
	d.DeletedAt = parseTime(deleted)
	d.PurgeAfter = parseTime(purge)
	return d, nil
}

func scanBranch(r scanner) (Branch, error) {
	var b Branch
	var created, updated string
	var expires sql.NullString
	if err := r.Scan(&b.ID, &b.DatabaseID, &b.Name, &b.ParentID, &b.SourceSnapshotID, &b.Protected, &expires, &created, &updated); err != nil {
		return Branch{}, err
	}
	b.CreatedAt = *parseTime(sql.NullString{String: created, Valid: true})
	b.UpdatedAt = *parseTime(sql.NullString{String: updated, Valid: true})
	b.ExpiresAt = parseTime(expires)
	return b, nil
}

func isUnique(err error) bool {
	return errors.Is(err, sqlite3.CONSTRAINT_UNIQUE)
}

// CreateDatabase records a new database and its default branch, named main.
// It fails with limit_reached when maxDatabases already exist, counted in
// the same transaction so concurrent creates can't pass the cap.
func (s *Store) CreateDatabase(ctx context.Context, name, dbID, branchID string, maxDatabases int) (Database, Branch, error) {
	if !ids.ValidName(name) {
		return Database{}, Branch{}, apierr.Newf(apierr.InvalidName, "invalid database name %q", name)
	}
	ts := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Database{}, Branch{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM databases`).Scan(&n); err != nil {
		return Database{}, Branch{}, err
	}
	if n >= maxDatabases {
		return Database{}, Branch{}, apierr.Newf(apierr.LimitReached, "the instance has the most databases allowed (%d)", maxDatabases)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO databases (id, name, default_branch_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		dbID, name, branchID, ts, ts); err != nil {
		if isUnique(err) {
			return Database{}, Branch{}, apierr.Newf(apierr.NameTaken, "a database named %q already exists", name)
		}
		return Database{}, Branch{}, fmt.Errorf("meta: insert database: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO branches (id, database_id, name, protected, created_at, updated_at) VALUES (?, ?, 'main', 1, ?, ?)`,
		branchID, dbID, ts, ts); err != nil {
		return Database{}, Branch{}, fmt.Errorf("meta: insert branch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Database{}, Branch{}, err
	}
	d, err := s.GetDatabase(ctx, dbID)
	if err != nil {
		return Database{}, Branch{}, err
	}
	b, err := s.GetBranch(ctx, dbID, branchID)
	return d, b, err
}

// GetDatabase finds a live database by name or id.
func (s *Store) GetDatabase(ctx context.Context, ref string) (Database, error) {
	col := "name"
	if ids.IsID(ref, ids.Database) {
		col = "id"
	} else if !ids.ValidName(ref) {
		return Database{}, apierr.Newf(apierr.DatabaseNotFound, "no database %q", ref)
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+dbCols+` FROM databases WHERE `+col+` = ? AND deleted_at IS NULL`, ref)
	d, err := scanDatabase(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Database{}, apierr.Newf(apierr.DatabaseNotFound, "no database %q", ref)
	}
	return d, err
}

// CountDatabases counts databases, including ones in the trash.
func (s *Store) CountDatabases(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM databases`).Scan(&n)
	return n, err
}

// ListLiveBranches returns every branch of every database not in the trash.
func (s *Store) ListLiveBranches(ctx context.Context) ([]Branch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+branchColsB+` FROM branches b JOIN databases d ON d.id = b.database_id WHERE d.deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Branch
	for rows.Next() {
		b, err := scanBranch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBranch finds a branch of a database by name or id.
func (s *Store) GetBranch(ctx context.Context, dbID, ref string) (Branch, error) {
	col := "name"
	if ids.IsID(ref, ids.Branch) {
		col = "id"
	} else if !ids.ValidName(ref) {
		return Branch{}, apierr.Newf(apierr.BranchNotFound, "no branch %q", ref)
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+branchCols+` FROM branches WHERE database_id = ? AND `+col+` = ?`, dbID, ref)
	b, err := scanBranch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Branch{}, apierr.Newf(apierr.BranchNotFound, "no branch %q", ref)
	}
	return b, err
}
