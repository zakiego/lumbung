package meta

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"
)

// Grant is one thing a token reaches: a database (or one branch of it) or a
// bucket, at one access level. Exactly one of DatabaseID and BucketID is set.
type Grant struct {
	DatabaseID string
	BranchID   string
	BucketID   string
	Access     string // read, write or admin
}

// Token is a stored token. Only the SHA-256 of the full token is kept.
type Token struct {
	ID         string
	Name       string
	SecretHash []byte
	Scope      string // instance, database, bucket or grants
	DatabaseID string
	BranchID   string
	BucketID   string
	// Access is the level of an instance, database or bucket token. For a
	// grants token it is the highest level among its grants, kept for
	// display only: what the token may do is decided by Grants.
	Access string
	// Grants is what a token with the scope grants reaches. Empty for the
	// other scopes.
	Grants  []Grant
	Service bool
	// AccessSecret is the S3 access key secret, encrypted (ADR 0006). Empty
	// when the token has no access key.
	AccessSecret []byte
	CreatedAt    time.Time
	ExpiresAt    *time.Time
	LastUsedAt   *time.Time
	RevokedAt    *time.Time
}

// ErrNoToken means no token has that id.
var ErrNoToken = errors.New("meta: no such token")

const tokenCols = `id, name, secret_hash, scope, coalesce(database_id, ''), coalesce(branch_id, ''), coalesce(bucket_id, ''), access, service, access_secret, created_at, expires_at, last_used_at, revoked_at`

func scanToken(r scanner) (Token, error) {
	var t Token
	var created string
	var expires, used, revoked sql.NullString
	if err := r.Scan(&t.ID, &t.Name, &t.SecretHash, &t.Scope, &t.DatabaseID, &t.BranchID, &t.BucketID, &t.Access, &t.Service, &t.AccessSecret, &created, &expires, &used, &revoked); err != nil {
		return Token{}, err
	}
	t.CreatedAt = *parseTime(sql.NullString{String: created, Valid: true})
	t.ExpiresAt = parseTime(expires)
	t.LastUsedAt = parseTime(used)
	t.RevokedAt = parseTime(revoked)
	return t, nil
}

// ScopeGrants is the scope of a token that reaches several databases and
// buckets, listed in its Grants.
const ScopeGrants = "grants"

// CreateToken stores a token record, with its grants when it has the scope
// grants.
func (s *Store) CreateToken(ctx context.Context, t Token) (Token, error) {
	if (t.Scope == ScopeGrants) != (len(t.Grants) > 0) {
		return Token{}, errors.New("meta: only a grants token has grants, and it needs at least one")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Token{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO tokens (id, name, secret_hash, scope, database_id, branch_id, bucket_id, access, service, access_secret, created_at, expires_at)
		 VALUES (?, ?, ?, ?, nullif(?, ''), nullif(?, ''), nullif(?, ''), ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.SecretHash, t.Scope, t.DatabaseID, t.BranchID, t.BucketID, t.Access, t.Service, nilIfEmpty(t.AccessSecret), now(), fmtTime(t.ExpiresAt))
	if err != nil {
		return Token{}, err
	}
	for i, g := range t.Grants {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO token_grants (token_id, position, database_id, branch_id, bucket_id, access) VALUES (?, ?, nullif(?, ''), nullif(?, ''), nullif(?, ''), ?)`,
			t.ID, i, g.DatabaseID, g.BranchID, g.BucketID, g.Access); err != nil {
			return Token{}, err
		}
	}
	if err := tx.Commit(); err != nil {
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
	if err != nil || t.Scope != ScopeGrants {
		return t, err
	}
	grants, err := s.tokenGrants(ctx, `WHERE token_id = ?`, id)
	if err != nil {
		return Token{}, err
	}
	t.Grants = grants[id]
	return t, nil
}

// tokenGrants reads grants by token id, each token's in the order they were
// given.
func (s *Store) tokenGrants(ctx context.Context, where string, args ...any) (map[string][]Grant, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT token_id, coalesce(database_id, ''), coalesce(branch_id, ''), coalesce(bucket_id, ''), access FROM token_grants `+where+` ORDER BY token_id, position`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Grant{}
	for rows.Next() {
		var id string
		var g Grant
		if err := rows.Scan(&id, &g.DatabaseID, &g.BranchID, &g.BucketID, &g.Access); err != nil {
			return nil, err
		}
		out[id] = append(out[id], g)
	}
	return out, rows.Err()
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
	withGrants := false
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		withGrants = withGrants || t.Scope == ScopeGrants
		out = append(out, t)
	}
	if err := rows.Err(); err != nil || !withGrants {
		return out, err
	}
	grants, err := s.tokenGrants(ctx, `WHERE token_id IN (SELECT id FROM tokens WHERE revoked_at IS NULL)`)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Grants = grants[out[i].ID]
	}
	return out, nil
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
	ts := at.UTC().Format(TimeLayout)
	cutoff := at.Add(-time.Minute).UTC().Format(TimeLayout)
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

// ErrTokenIDTaken means a service token's id belongs to another token.
var ErrTokenIDTaken = errors.New("meta: token id already used by another token")

// SetServiceToken makes the token with this id and hash the one live service
// token, in one transaction: an earlier service token with the same id and
// secret comes back, any other live service token is revoked. It refuses,
// changing nothing, when the id belongs to a token that isn't a service
// token or has a different secret.
func (s *Store) SetServiceToken(ctx context.Context, id string, hash []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var service bool
	var existing []byte
	err = tx.QueryRowContext(ctx, `SELECT service, secret_hash FROM tokens WHERE id = ?`, id).Scan(&service, &existing)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if found && (!service || !bytes.Equal(existing, hash)) {
		return ErrTokenIDTaken
	}
	ts := now()
	if _, err := tx.ExecContext(ctx, `UPDATE tokens SET revoked_at = ? WHERE service = 1 AND revoked_at IS NULL AND id != ?`, ts, id); err != nil {
		return err
	}
	if found {
		_, err = tx.ExecContext(ctx, `UPDATE tokens SET revoked_at = NULL WHERE id = ?`, id)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO tokens (id, name, secret_hash, scope, access, service, created_at) VALUES (?, 'service', ?, 'instance', 'admin', 1, ?)`,
			id, hash, ts)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
