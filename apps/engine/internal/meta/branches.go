package meta

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
)

func fmtTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(TimeLayout)
}

// ListBranches returns a database's branches, oldest first.
func (s *Store) ListBranches(ctx context.Context, dbID string) ([]Branch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+branchCols+` FROM branches WHERE database_id = ? ORDER BY created_at, id`, dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Branch{}
	for rows.Next() {
		b, err := scanBranch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CountBranches counts a database's branches.
func (s *Store) CountBranches(ctx context.Context, dbID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM branches WHERE database_id = ?`, dbID).Scan(&n)
	return n, err
}

// NewBranch is what CreateBranch records.
type NewBranch struct {
	ID               string
	DatabaseID       string
	Name             string
	ParentID         string // empty when made from a snapshot of a deleted branch
	SourceSnapshotID string
	Protected        bool
	ExpiresAt        *time.Time
}

// CreateBranch records a branch whose file already exists.
func (s *Store) CreateBranch(ctx context.Context, nb NewBranch) (Branch, error) {
	if !ids.ValidName(nb.Name) {
		return Branch{}, apierr.Newf(apierr.InvalidName, "invalid branch name %q", nb.Name)
	}
	ts := now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO branches (id, database_id, name, parent_id, source_snapshot_id, protected, expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, nullif(?, ''), nullif(?, ''), ?, ?, ?, ?)`,
		nb.ID, nb.DatabaseID, nb.Name, nb.ParentID, nb.SourceSnapshotID, nb.Protected, fmtTime(nb.ExpiresAt), ts, ts)
	if isUnique(err) {
		return Branch{}, apierr.Newf(apierr.NameTaken, "a branch named %q already exists", nb.Name)
	}
	if err != nil {
		return Branch{}, fmt.Errorf("meta: insert branch: %w", err)
	}
	return s.GetBranch(ctx, nb.DatabaseID, nb.ID)
}

// BranchUpdate changes branch fields. Nil fields stay as they are; a
// non-nil ExpiresAt pointing at nil clears the expiry.
type BranchUpdate struct {
	Name      *string
	Protected *bool
	ExpiresAt **time.Time
}

// UpdateBranch applies an update. The default branch can't lose protection.
func (s *Store) UpdateBranch(ctx context.Context, dbID, branchID string, u BranchUpdate) (Branch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Branch{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var defaultID string
	if err := tx.QueryRowContext(ctx, `SELECT default_branch_id FROM databases WHERE id = ?`, dbID).Scan(&defaultID); err != nil {
		return Branch{}, err
	}
	ts := now()
	if u.Name != nil {
		if !ids.ValidName(*u.Name) {
			return Branch{}, apierr.Newf(apierr.InvalidName, "invalid branch name %q", *u.Name)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE branches SET name = ?, updated_at = ? WHERE id = ?`, *u.Name, ts, branchID); err != nil {
			if isUnique(err) {
				return Branch{}, apierr.Newf(apierr.NameTaken, "a branch named %q already exists", *u.Name)
			}
			return Branch{}, err
		}
	}
	if u.Protected != nil {
		if !*u.Protected && branchID == defaultID {
			return Branch{}, apierr.New(apierr.BranchProtected, "the default branch is always protected")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE branches SET protected = ?, updated_at = ? WHERE id = ?`, *u.Protected, ts, branchID); err != nil {
			return Branch{}, err
		}
	}
	if u.ExpiresAt != nil {
		if *u.ExpiresAt != nil && branchID == defaultID {
			return Branch{}, apierr.New(apierr.BranchProtected, "the default branch can't expire")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE branches SET expires_at = ?, updated_at = ? WHERE id = ?`, fmtTime(*u.ExpiresAt), ts, branchID); err != nil {
			return Branch{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Branch{}, err
	}
	return s.GetBranch(ctx, dbID, branchID)
}

// TouchBranch records that a branch's content was replaced (reset, import).
func (s *Store) TouchBranch(ctx context.Context, branchID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE branches SET updated_at = ? WHERE id = ?`, now(), branchID)
	return err
}

// DeleteBranch removes a branch record, revokes tokens limited to it and
// drops its stats. Its snapshots stay.
func (s *Store) DeleteBranch(ctx context.Context, branchID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`UPDATE tokens SET revoked_at = '` + now() + `' WHERE branch_id = ? AND revoked_at IS NULL`,
		`DELETE FROM stats_minute WHERE branch_id = ?`,
		`DELETE FROM slow_queries WHERE branch_id = ?`,
		`DELETE FROM branches WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, branchID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetDefaultBranch makes a branch the database's default and protects it.
// The old default keeps its protection.
func (s *Store) SetDefaultBranch(ctx context.Context, dbID, branchID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ts := now()
	if _, err := tx.ExecContext(ctx, `UPDATE branches SET protected = 1, expires_at = NULL, updated_at = ? WHERE id = ? AND database_id = ?`, ts, branchID, dbID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE databases SET default_branch_id = ?, updated_at = ? WHERE id = ?`, branchID, ts, dbID); err != nil {
		return err
	}
	return tx.Commit()
}

// ExpiredBranches lists unprotected branches of live databases whose expiry
// has passed.
func (s *Store) ExpiredBranches(ctx context.Context, at time.Time) ([]Branch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+branchColsB+` FROM branches b JOIN databases d ON d.id = b.database_id
		WHERE d.deleted_at IS NULL AND b.protected = 0 AND b.expires_at IS NOT NULL AND b.expires_at <= ? AND b.id != d.default_branch_id`,
		at.UTC().Format(TimeLayout))
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

// ListDatabases returns live databases ordered by name.
func (s *Store) ListDatabases(ctx context.Context) ([]Database, error) {
	return s.listDatabases(ctx, `deleted_at IS NULL ORDER BY name`)
}

// ListTrash returns databases in the trash, most recently deleted first.
func (s *Store) ListTrash(ctx context.Context) ([]Database, error) {
	return s.listDatabases(ctx, `deleted_at IS NOT NULL ORDER BY deleted_at DESC`)
}

func (s *Store) listDatabases(ctx context.Context, where string) ([]Database, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dbCols+` FROM databases WHERE `+where)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RenameDatabase changes a live database's name.
func (s *Store) RenameDatabase(ctx context.Context, dbID, name string) (Database, error) {
	if !ids.ValidName(name) {
		return Database{}, apierr.Newf(apierr.InvalidName, "invalid database name %q", name)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE databases SET name = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`, name, now(), dbID)
	if isUnique(err) {
		return Database{}, apierr.Newf(apierr.NameTaken, "a database named %q already exists", name)
	}
	if err != nil {
		return Database{}, err
	}
	return s.GetDatabase(ctx, dbID)
}

// SoftDeleteDatabase moves a database to the trash.
func (s *Store) SoftDeleteDatabase(ctx context.Context, dbID string, purgeAfter time.Time) error {
	ts := now()
	_, err := s.db.ExecContext(ctx, `UPDATE databases SET deleted_at = ?, purge_after = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		ts, purgeAfter.UTC().Format(TimeLayout), ts, dbID)
	return err
}

// GetDeletedDatabase finds a database in the trash by id, or by name (the
// most recently deleted one with that name).
func (s *Store) GetDeletedDatabase(ctx context.Context, ref string) (Database, error) {
	var row *sql.Row
	switch {
	case ids.IsID(ref, ids.Database):
		row = s.db.QueryRowContext(ctx, `SELECT `+dbCols+` FROM databases WHERE id = ? AND deleted_at IS NOT NULL`, ref)
	case ids.ValidName(ref):
		row = s.db.QueryRowContext(ctx, `SELECT `+dbCols+` FROM databases WHERE name = ? AND deleted_at IS NOT NULL ORDER BY deleted_at DESC LIMIT 1`, ref)
	default:
		return Database{}, apierr.Newf(apierr.DatabaseNotFound, "no database %q in the trash", ref)
	}
	d, err := scanDatabase(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Database{}, apierr.Newf(apierr.DatabaseNotFound, "no database %q in the trash", ref)
	}
	return d, err
}

// RestoreDatabase takes a database out of the trash under name.
func (s *Store) RestoreDatabase(ctx context.Context, dbID, name string) (Database, error) {
	if !ids.ValidName(name) {
		return Database{}, apierr.Newf(apierr.InvalidName, "invalid database name %q", name)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE databases SET name = ?, deleted_at = NULL, purge_after = NULL, updated_at = ? WHERE id = ? AND deleted_at IS NOT NULL`,
		name, now(), dbID)
	if isUnique(err) {
		return Database{}, apierr.Newf(apierr.NameTaken, "a database named %q already exists; restore it under another name", name)
	}
	if err != nil {
		return Database{}, err
	}
	return s.GetDatabase(ctx, dbID)
}

// PurgeDue lists trashed databases whose retention has passed.
func (s *Store) PurgeDue(ctx context.Context, at time.Time) ([]Database, error) {
	return s.listDatabases(ctx, `deleted_at IS NOT NULL AND purge_after <= '`+at.UTC().Format(TimeLayout)+`'`)
}

// PurgeDatabase deletes every record of a trashed database. It reports
// false, changing nothing, if the database isn't in the trash (restored
// meanwhile).
func (s *Store) PurgeDatabase(ctx context.Context, dbID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM databases WHERE id = ? AND deleted_at IS NOT NULL`, dbID).Scan(&n); err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	for _, q := range []string{
		`DELETE FROM tokens WHERE database_id = ?`,
		`DELETE FROM stats_minute WHERE branch_id IN (SELECT id FROM branches WHERE database_id = ?)`,
		`DELETE FROM slow_queries WHERE branch_id IN (SELECT id FROM branches WHERE database_id = ?)`,
		`DELETE FROM snapshot_schedules WHERE database_id = ?`,
		`DELETE FROM snapshots WHERE database_id = ?`,
		`DELETE FROM branches WHERE database_id = ?`,
		`DELETE FROM databases WHERE id = ? AND deleted_at IS NOT NULL`,
	} {
		if _, err := tx.ExecContext(ctx, q, dbID); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// DropMissing removes records whose files a restore didn't bring back:
// databases in the trash, branches not in have (keyed by branch id), every
// snapshot, and what hangs off them. It runs on a freshly restored meta.db.
func (s *Store) DropMissing(ctx context.Context, have map[string]bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT b.id FROM branches b JOIN databases d ON d.id = b.database_id WHERE d.deleted_at IS NULL`)
	if err != nil {
		return err
	}
	var missing []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !have[id] {
			missing = append(missing, id)
		}
	}
	rows.Close()
	for _, id := range missing {
		for _, q := range []string{
			`DELETE FROM snapshot_schedules WHERE branch_id = ?`,
			`DELETE FROM tokens WHERE branch_id = ?`,
			`DELETE FROM stats_minute WHERE branch_id = ?`,
			`DELETE FROM slow_queries WHERE branch_id = ?`,
			`DELETE FROM branches WHERE id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
	}
	for _, q := range []string{
		`DELETE FROM snapshots`,
		`DELETE FROM tokens WHERE database_id IN (SELECT id FROM databases WHERE deleted_at IS NOT NULL)`,
		`DELETE FROM snapshot_schedules WHERE database_id IN (SELECT id FROM databases WHERE deleted_at IS NOT NULL)`,
		`DELETE FROM stats_minute WHERE branch_id IN (SELECT b.id FROM branches b JOIN databases d ON d.id = b.database_id WHERE d.deleted_at IS NOT NULL)`,
		`DELETE FROM slow_queries WHERE branch_id IN (SELECT b.id FROM branches b JOIN databases d ON d.id = b.database_id WHERE d.deleted_at IS NOT NULL)`,
		`DELETE FROM branches WHERE database_id IN (SELECT id FROM databases WHERE deleted_at IS NOT NULL)`,
		`DELETE FROM databases WHERE deleted_at IS NOT NULL`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}
