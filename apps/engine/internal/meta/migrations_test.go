package meta

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// Migration 4 rebuilds the tokens table; tokens made before it must survive
// with every column.
func TestTokensSurviveMigration4(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "meta.db")
	raw, err := sql.Open("sqlite3", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := raw.ExecContext(ctx, migrations[i]); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO databases (id, name, default_branch_id, created_at, updated_at) VALUES ('db_aaaaaaaaaaaaaaaaaaaa', 'app', 'br_aaaaaaaaaaaaaaaaaaaa', '2026-01-01T00:00:00.000000000Z', '2026-01-01T00:00:00.000000000Z')`,
		`INSERT INTO tokens (id, name, secret_hash, scope, access, service, created_at) VALUES ('tok_service', 'service', x'01', 'instance', 'admin', 1, '2026-01-01T00:00:00.000000000Z')`,
		`INSERT INTO tokens (id, name, secret_hash, scope, database_id, branch_id, access, created_at, expires_at, revoked_at) VALUES ('tok_db', 'db', x'02', 'database', 'db_aaaaaaaaaaaaaaaaaaaa', 'br_aaaaaaaaaaaaaaaaaaaa', 'write', '2026-01-02T00:00:00.000000000Z', '2027-01-01T00:00:00.000000000Z', '2026-06-01T00:00:00.000000000Z')`,
		`PRAGMA user_version = 3`,
	} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != Known() {
		t.Fatalf("version %d", s.Version())
	}
	svc, err := s.GetToken(ctx, "tok_service")
	if err != nil || !svc.Service || svc.Scope != "instance" || svc.Access != "admin" {
		t.Fatalf("service token %+v %v", svc, err)
	}
	db, err := s.GetToken(ctx, "tok_db")
	if err != nil || db.DatabaseID != "db_aaaaaaaaaaaaaaaaaaaa" || db.BranchID != "br_aaaaaaaaaaaaaaaaaaaa" || db.RevokedAt == nil || db.ExpiresAt == nil || db.BucketID != "" {
		t.Fatalf("database token %+v %v", db, err)
	}
	// The new CHECKs hold: a bucket token needs a bucket, a database token no bucket.
	for i, q := range []string{
		`INSERT INTO tokens (id, name, secret_hash, scope, access, created_at) VALUES ('tok_x1', 'x', x'00', 'bucket', 'read', 'now')`,
		`INSERT INTO tokens (id, name, secret_hash, scope, database_id, bucket_id, access, created_at) VALUES ('tok_x2', 'x', x'00', 'database', 'db_aaaaaaaaaaaaaaaaaaaa', 'bk_a', 'read', 'now')`,
		`INSERT INTO tokens (id, name, secret_hash, scope, bucket_id, access, created_at) VALUES ('tok_x3', 'x', x'00', 'instance', 'bk_a', 'read', 'now')`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err == nil {
			t.Errorf("insert %d passed the checks", i)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO tokens (id, name, secret_hash, scope, bucket_id, access, created_at) VALUES ('tok_ok', 'x', x'00', 'bucket', 'bk_a', 'read', ?)`, fmt.Sprint(now())); err != nil {
		t.Fatalf("bucket token: %v", err)
	}
}
