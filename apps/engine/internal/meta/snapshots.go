package meta

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
)

// Snapshot kinds.
const (
	SnapshotManual    = "manual"
	SnapshotScheduled = "scheduled"
)

// Snapshot is one snapshot record.
type Snapshot struct {
	ID         string
	DatabaseID string
	BranchID   string
	BranchName string
	Kind       string
	Label      string
	SizeBytes  int64
	CreatedAt  time.Time
}

// Schedule is a branch's snapshot schedule.
type Schedule struct {
	BranchID   string
	DatabaseID string
	Every      time.Duration
	KeepCount  int           // 0: no count limit
	KeepFor    time.Duration // 0: no age limit
	LastRunAt  *time.Time
	CreatedAt  time.Time
}

const snapCols = `id, database_id, branch_id, branch_name, kind, label, size_bytes, created_at`

func scanSnapshot(r scanner) (Snapshot, error) {
	var sn Snapshot
	var created string
	if err := r.Scan(&sn.ID, &sn.DatabaseID, &sn.BranchID, &sn.BranchName, &sn.Kind, &sn.Label, &sn.SizeBytes, &created); err != nil {
		return Snapshot{}, err
	}
	sn.CreatedAt = *parseTime(sql.NullString{String: created, Valid: true})
	return sn, nil
}

// CreateSnapshot records a snapshot whose file already exists. wsID is the
// workspace the caller resolved the database in: the snapshot is only
// recorded if the database is still live there (see databaseLiveIn).
func (s *Store) CreateSnapshot(ctx context.Context, sn Snapshot, wsID string) (Snapshot, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO snapshots (id, database_id, branch_id, branch_name, kind, label, size_bytes, created_at)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ? WHERE EXISTS (`+databaseLiveIn+`)`,
		sn.ID, sn.DatabaseID, sn.BranchID, sn.BranchName, sn.Kind, sn.Label, sn.SizeBytes, now(), sn.DatabaseID, wsID)
	if err != nil {
		return Snapshot{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Snapshot{}, apierr.Newf(apierr.DatabaseNotFound, "no database %q", sn.DatabaseID)
	}
	return s.GetSnapshot(ctx, sn.DatabaseID, sn.ID)
}

// GetSnapshot finds a snapshot of a database by id.
func (s *Store) GetSnapshot(ctx context.Context, dbID, id string) (Snapshot, error) {
	if !ids.IsID(id, ids.Snapshot) {
		return Snapshot{}, apierr.Newf(apierr.SnapshotNotFound, "no snapshot %q", id)
	}
	sn, err := scanSnapshot(s.db.QueryRowContext(ctx, `SELECT `+snapCols+` FROM snapshots WHERE database_id = ? AND id = ?`, dbID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, apierr.Newf(apierr.SnapshotNotFound, "no snapshot %q", id)
	}
	return sn, err
}

// ListSnapshots returns a branch's snapshots, newest first. An empty
// branchID lists every snapshot of the database.
func (s *Store) ListSnapshots(ctx context.Context, dbID, branchID string) ([]Snapshot, error) {
	q := `SELECT ` + snapCols + ` FROM snapshots WHERE database_id = ?`
	args := []any{dbID}
	if branchID != "" {
		q += ` AND branch_id = ?`
		args = append(args, branchID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

// DeleteSnapshot removes a snapshot record.
func (s *Store) DeleteSnapshot(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM snapshots WHERE id = ?`, id)
	return err
}

// SetSchedule creates or replaces a branch's snapshot schedule.
func (s *Store) SetSchedule(ctx context.Context, sc Schedule) (Schedule, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO snapshot_schedules (branch_id, database_id, every_seconds, keep_count, keep_seconds, created_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (branch_id) DO UPDATE SET every_seconds = excluded.every_seconds, keep_count = excluded.keep_count, keep_seconds = excluded.keep_seconds`,
		sc.BranchID, sc.DatabaseID, int64(sc.Every/time.Second), sc.KeepCount, int64(sc.KeepFor/time.Second), now())
	if err != nil {
		return Schedule{}, err
	}
	got, ok, err := s.GetSchedule(ctx, sc.BranchID)
	if err == nil && !ok {
		err = errors.New("meta: schedule vanished")
	}
	return got, err
}

// GetSchedule returns a branch's schedule, if it has one.
func (s *Store) GetSchedule(ctx context.Context, branchID string) (Schedule, bool, error) {
	rows, err := s.listSchedules(ctx, `branch_id = ?`, branchID)
	if err != nil || len(rows) == 0 {
		return Schedule{}, false, err
	}
	return rows[0], true, nil
}

// DeleteSchedule removes a branch's schedule.
func (s *Store) DeleteSchedule(ctx context.Context, branchID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM snapshot_schedules WHERE branch_id = ?`, branchID)
	return err
}

// AllSchedules returns the schedules of live databases.
func (s *Store) AllSchedules(ctx context.Context) ([]Schedule, error) {
	return s.listSchedules(ctx, `database_id IN (SELECT id FROM databases WHERE deleted_at IS NULL)`)
}

// MarkScheduleRun records when a schedule last took a snapshot.
func (s *Store) MarkScheduleRun(ctx context.Context, branchID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE snapshot_schedules SET last_run_at = ? WHERE branch_id = ?`, at.UTC().Format(TimeLayout), branchID)
	return err
}

func (s *Store) listSchedules(ctx context.Context, where string, args ...any) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT branch_id, database_id, every_seconds, keep_count, keep_seconds, last_run_at, created_at FROM snapshot_schedules WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var sc Schedule
		var every, keepSecs int64
		var last sql.NullString
		var created string
		if err := rows.Scan(&sc.BranchID, &sc.DatabaseID, &every, &sc.KeepCount, &keepSecs, &last, &created); err != nil {
			return nil, err
		}
		sc.Every = time.Duration(every) * time.Second
		sc.KeepFor = time.Duration(keepSecs) * time.Second
		sc.LastRunAt = parseTime(last)
		sc.CreatedAt = *parseTime(sql.NullString{String: created, Valid: true})
		out = append(out, sc)
	}
	return out, rows.Err()
}
