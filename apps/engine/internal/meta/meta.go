// Package meta stores the engine's own records in meta.db: databases,
// branches, and later snapshots, tokens, audit and stats.
//
// Migrations are embedded, forward-only, and run at start (PLAN 6.10).
// PRAGMA user_version holds how many have been applied. A meta.db with more
// than this binary knows about means a rollback to an older binary: the
// store opens in a refused state and /health fails, instead of guessing.
package meta

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver" // registers "sqlite3"
)

// ErrNewerSchema means meta.db was written by a newer binary.
var ErrNewerSchema = errors.New("meta: meta.db is newer than this binary")

// Store is meta.db.
type Store struct {
	db      *sql.DB
	version int // applied migrations
}

// Open opens or creates meta.db at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(wal)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "synchronous(normal)")
	q.Set("_txlock", "immediate")
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("meta: open: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		if errors.Is(err, ErrNewerSchema) {
			return s, err
		}
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes meta.db.
func (s *Store) Close() error { return s.db.Close() }

// Version is the number of applied migrations.
func (s *Store) Version() int { return s.version }

// Known is the number of migrations this binary has.
func Known() int { return len(migrations) }

func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("meta: read version: %w", err)
	}
	s.version = v
	if v > len(migrations) {
		return fmt.Errorf("%w: it has %d migrations, this binary knows %d", ErrNewerSchema, v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("meta: migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("meta: migration %d: %w", i+1, err)
		}
		s.version = i + 1
	}
	return nil
}

// TimeLayout is how meta.db stores times: UTC RFC 3339 with a fixed nine
// fraction digits. Fixed width makes text order match time order (RFC3339Nano
// drops trailing zeros, so ".5Z" would sort after ".52Z").
const TimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func now() string { return time.Now().UTC().Format(TimeLayout) }

func parseTime(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s.String)
	if err != nil {
		return nil
	}
	return &t
}
