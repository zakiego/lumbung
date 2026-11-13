package meta

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// AuditEntry is one management action.
type AuditEntry struct {
	ID        int64
	At        time.Time
	TokenID   string // the token that made the request
	Actor     string // the dashboard user acting through the service token, if any
	Action    string // e.g. database.create
	Target    string // ids the action touched
	Result    string // ok or error
	Code      string // error code when Result is error
	RequestID string
}

// AddAudit appends an entry and trims the log to keep at most maxRows.
func (s *Store) AddAudit(ctx context.Context, e AuditEntry, maxRows int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit (at, token_id, actor, action, target, result, code, request_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now(), e.TokenID, e.Actor, e.Action, e.Target, e.Result, e.Code, e.RequestID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM audit WHERE id <= (SELECT max(id) FROM audit) - ?`, maxRows)
	return err
}

// ListAudit returns entries newest first, with ids below before (0: from
// the newest).
func (s *Store) ListAudit(ctx context.Context, before int64, limit int) ([]AuditEntry, error) {
	q := `SELECT id, at, token_id, actor, action, target, result, code, request_id FROM audit`
	args := []any{}
	if before > 0 {
		q += ` WHERE id < ?`
		args = append(args, before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var at string
		if err := rows.Scan(&e.ID, &at, &e.TokenID, &e.Actor, &e.Action, &e.Target, &e.Result, &e.Code, &e.RequestID); err != nil {
			return nil, err
		}
		e.At = *parseTime(sql.NullString{String: at, Valid: true})
		out = append(out, e)
	}
	return out, rows.Err()
}

// StatsBucket is one branch's counters for one minute.
type StatsBucket struct {
	BranchID    string
	Minute      time.Time
	Queries     int64
	RowsRead    int64
	RowsWritten int64
	Errors      int64
	Durations   []int64 // counts per duration bucket, see stats.Bounds
}

// AddStats merges buckets into stats_minute and drops buckets older than
// keepSince.
func (s *Store) AddStats(ctx context.Context, buckets []StatsBucket, keepSince time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, b := range buckets {
		minute := b.Minute.UTC().Format(time.RFC3339)
		var old string
		err := tx.QueryRowContext(ctx, `SELECT duration_hist FROM stats_minute WHERE branch_id = ? AND minute = ?`, b.BranchID, minute).Scan(&old)
		hist := append([]int64(nil), b.Durations...) // don't change the caller's batch
		if err == nil {
			var prev []int64
			if json.Unmarshal([]byte(old), &prev) == nil {
				for i := range prev {
					if i < len(hist) {
						hist[i] += prev[i]
					}
				}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		h, _ := json.Marshal(hist)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO stats_minute (branch_id, minute, queries, rows_read, rows_written, errors, duration_hist) VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (branch_id, minute) DO UPDATE SET queries = queries + excluded.queries, rows_read = rows_read + excluded.rows_read,
			   rows_written = rows_written + excluded.rows_written, errors = errors + excluded.errors, duration_hist = excluded.duration_hist`,
			b.BranchID, minute, b.Queries, b.RowsRead, b.RowsWritten, b.Errors, string(h)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stats_minute WHERE minute < ?`, keepSince.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// ListStats returns a branch's buckets since a time, oldest first.
func (s *Store) ListStats(ctx context.Context, branchID string, since time.Time) ([]StatsBucket, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT minute, queries, rows_read, rows_written, errors, duration_hist FROM stats_minute WHERE branch_id = ? AND minute >= ? ORDER BY minute`,
		branchID, since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StatsBucket{}
	for rows.Next() {
		b := StatsBucket{BranchID: branchID}
		var minute, hist string
		if err := rows.Scan(&minute, &b.Queries, &b.RowsRead, &b.RowsWritten, &b.Errors, &hist); err != nil {
			return nil, err
		}
		b.Minute, _ = time.Parse(time.RFC3339, minute)
		_ = json.Unmarshal([]byte(hist), &b.Durations)
		out = append(out, b)
	}
	return out, rows.Err()
}

// SlowQuery is one entry of the slow-query log.
type SlowQuery struct {
	At         time.Time
	DurationMS int64
	SQL        string
	ErrorCode  string
}

// AddSlowQuery appends to a branch's slow-query log, keeping the newest
// maxPerBranch entries.
func (s *Store) AddSlowQuery(ctx context.Context, branchID string, q SlowQuery, maxPerBranch int) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO slow_queries (branch_id, at, duration_ms, sql, error_code) VALUES (?, ?, ?, ?, ?)`,
		branchID, q.At.UTC().Format(TimeLayout), q.DurationMS, q.SQL, q.ErrorCode); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM slow_queries WHERE branch_id = ? AND id NOT IN (SELECT id FROM slow_queries WHERE branch_id = ? ORDER BY id DESC LIMIT ?)`,
		branchID, branchID, maxPerBranch)
	return err
}

// ListSlowQueries returns a branch's slow queries, newest first.
func (s *Store) ListSlowQueries(ctx context.Context, branchID string) ([]SlowQuery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, duration_ms, sql, error_code FROM slow_queries WHERE branch_id = ? ORDER BY id DESC`, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SlowQuery{}
	for rows.Next() {
		var q SlowQuery
		var at string
		if err := rows.Scan(&at, &q.DurationMS, &q.SQL, &q.ErrorCode); err != nil {
			return nil, err
		}
		q.At = *parseTime(sql.NullString{String: at, Valid: true})
		out = append(out, q)
	}
	return out, rows.Err()
}

// Idempotent is a stored response for an Idempotency-Key.
type Idempotent struct {
	Method      string
	Path        string
	RequestHash []byte
	Status      int
	Body        []byte
}

// GetIdempotent returns a stored response younger than maxAge.
func (s *Store) GetIdempotent(ctx context.Context, tokenID, key string, maxAge time.Duration) (Idempotent, bool, error) {
	var r Idempotent
	err := s.db.QueryRowContext(ctx,
		`SELECT method, path, request_hash, status, body FROM idempotency WHERE token_id = ? AND key = ? AND created_at >= ?`,
		tokenID, key, time.Now().Add(-maxAge).UTC().Format(TimeLayout)).Scan(&r.Method, &r.Path, &r.RequestHash, &r.Status, &r.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return Idempotent{}, false, nil
	}
	return r, err == nil, err
}

// PutIdempotent stores a response and drops entries older than maxAge.
func (s *Store) PutIdempotent(ctx context.Context, tokenID, key string, r Idempotent, maxAge time.Duration) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO idempotency (token_id, key, method, path, request_hash, status, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (token_id, key) DO UPDATE SET method = excluded.method, path = excluded.path, request_hash = excluded.request_hash,
		   status = excluded.status, body = excluded.body, created_at = excluded.created_at`,
		tokenID, key, r.Method, r.Path, r.RequestHash, r.Status, r.Body, now()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM idempotency WHERE created_at < ?`, time.Now().Add(-maxAge).UTC().Format(TimeLayout))
	return err
}

// LastActivity returns the latest stats minute among branches, or nil.
func (s *Store) LastActivity(ctx context.Context, branchIDs []string) (*time.Time, error) {
	if len(branchIDs) == 0 {
		return nil, nil
	}
	args := make([]any, len(branchIDs))
	marks := make([]byte, 0, len(branchIDs)*2)
	for i, id := range branchIDs {
		args[i] = id
		if i > 0 {
			marks = append(marks, ',')
		}
		marks = append(marks, '?')
	}
	var minute sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT max(minute) FROM stats_minute WHERE branch_id IN (`+string(marks)+`)`, args...).Scan(&minute); err != nil {
		return nil, err
	}
	if !minute.Valid {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, minute.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
