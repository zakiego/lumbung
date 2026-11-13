package meta

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Token is a stored token. Only the SHA-256 of the full token is kept.
type Token struct {
	ID         string
	Name       string
	SecretHash []byte
	Scope      string // instance or database
	DatabaseID string
	BranchID   string
	Access     string // read, write or admin
	Service    bool
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// ErrNoToken means no token has that id.
var ErrNoToken = errors.New("meta: no such token")

const tokenCols = `id, name, secret_hash, scope, coalesce(database_id, ''), coalesce(branch_id, ''), access, service, created_at, expires_at, last_used_at, revoked_at`

func scanToken(r scanner) (Token, error) {
	var t Token
	var created string
	var expires, used, revoked sql.NullString
	if err := r.Scan(&t.ID, &t.Name, &t.SecretHash, &t.Scope, &t.DatabaseID, &t.BranchID, &t.Access, &t.Service, &created, &expires, &used, &revoked); err != nil {
		return Token{}, err
	}
	t.CreatedAt = *parseTime(sql.NullString{String: created, Valid: true})
	t.ExpiresAt = parseTime(expires)
	t.LastUsedAt = parseTime(used)
	t.RevokedAt = parseTime(revoked)
	return t, nil
}

// CreateToken stores a token record.
func (s *Store) CreateToken(ctx context.Context, t Token) (Token, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (id, name, secret_hash, scope, database_id, branch_id, access, service, created_at, expires_at)
		 VALUES (?, ?, ?, ?, nullif(?, ''), nullif(?, ''), ?, ?, ?, ?)`,
		t.ID, t.Name, t.SecretHash, t.Scope, t.DatabaseID, t.BranchID, t.Access, t.Service, now(), fmtTime(t.ExpiresAt))
	if err != nil {
		return Token{}, err
	}
	return s.GetToken(ctx, t.ID)
}

// GetToken finds a token by id, revoked or not.
func (s *Store) GetToken(ctx context.Context, id string) (Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM tokens WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, ErrNoToken
	}
	return t, err
}

// ListTokens returns tokens that aren't revoked, newest first, after the
// cursor id (exclusive) when given.
func (s *Store) ListTokens(ctx context.Context, afterCreated, afterID string, limit int) ([]Token, error) {
	q := `SELECT ` + tokenCols + ` FROM tokens WHERE revoked_at IS NULL`
	args := []any{}
	if afterID != "" {
		q += ` AND (created_at, id) < (?, ?)`
		args = append(args, afterCreated, afterID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken marks a token revoked. It reports whether a live token was
// revoked.
func (s *Store) RevokeToken(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now(), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// TouchToken records a token's last use, at most once a minute per token.
func (s *Store) TouchToken(ctx context.Context, id string, at time.Time) error {
	ts := at.UTC().Format(time.RFC3339Nano)
	cutoff := at.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE tokens SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`, ts, id, cutoff)
	return err
}

// ServiceToken returns the live service token, if one exists.
func (s *Store) ServiceToken(ctx context.Context) (Token, bool, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM tokens WHERE service = 1 AND revoked_at IS NULL ORDER BY created_at DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, false, nil
	}
	return t, err == nil, err
}
