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
	// The new CHECKs hold: a bucket token needs a bucket, a database token no
	// bucket. Since migration 6 a token below the instance names its
	// workspace, so these do, and fail only for the reason they test.
	const wsOf = `(SELECT id FROM workspaces WHERE is_default = 1)`
	for i, q := range []string{
		`INSERT INTO tokens (id, name, secret_hash, scope, workspace_id, access, created_at) VALUES ('tok_x1', 'x', x'00', 'bucket', ` + wsOf + `, 'read', 'now')`,
		`INSERT INTO tokens (id, name, secret_hash, scope, workspace_id, database_id, bucket_id, access, created_at) VALUES ('tok_x2', 'x', x'00', 'database', ` + wsOf + `, 'db_aaaaaaaaaaaaaaaaaaaa', 'bk_a', 'read', 'now')`,
		`INSERT INTO tokens (id, name, secret_hash, scope, bucket_id, access, created_at) VALUES ('tok_x3', 'x', x'00', 'instance', 'bk_a', 'read', 'now')`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err == nil {
			t.Errorf("insert %d passed the checks", i)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO tokens (id, name, secret_hash, scope, workspace_id, bucket_id, access, created_at) VALUES ('tok_ok', 'x', x'00', 'bucket', `+wsOf+`, 'bk_a', 'read', ?)`, fmt.Sprint(now())); err != nil {
		t.Fatalf("bucket token: %v", err)
	}
}

// Migration 5 rebuilds the tokens table again and adds token_grants; tokens
// made before it must survive with every column, access key included.
func TestTokensSurviveMigration5(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "meta.db")
	raw, err := sql.Open("sqlite3", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := raw.ExecContext(ctx, migrations[i]); err != nil {
			t.Fatal(err)
		}
	}
	const at = "'2026-01-01T00:00:00.000000000Z'"
	for _, q := range []string{
		`INSERT INTO databases (id, name, default_branch_id, created_at, updated_at) VALUES ('db_a', 'app', 'br_a', ` + at + `, ` + at + `)`,
		`INSERT INTO buckets (id, name, created_at, updated_at) VALUES ('bk_a', 'files', ` + at + `, ` + at + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, access, service, created_at) VALUES ('tok_service', 'service', x'01', 'instance', 'admin', 1, ` + at + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, database_id, branch_id, access, created_at, last_used_at) VALUES ('tok_db', 'db', x'02', 'database', 'db_a', 'br_a', 'write', ` + at + `, ` + at + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, bucket_id, access, access_secret, created_at, expires_at, revoked_at) VALUES ('tok_bk', 'bk', x'03', 'bucket', 'bk_a', 'read', x'aabb', ` + at + `, ` + at + `, ` + at + `)`,
		`PRAGMA user_version = 4`,
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
	if svc, err := s.GetToken(ctx, "tok_service"); err != nil || !svc.Service || svc.Scope != "instance" || svc.Access != "admin" {
		t.Fatalf("service token %+v %v", svc, err)
	}
	if db, err := s.GetToken(ctx, "tok_db"); err != nil || db.DatabaseID != "db_a" || db.BranchID != "br_a" || db.Access != "write" || db.LastUsedAt == nil || len(db.Grants) != 0 {
		t.Fatalf("database token %+v %v", db, err)
	}
	if bk, err := s.GetToken(ctx, "tok_bk"); err != nil || bk.BucketID != "bk_a" || string(bk.AccessSecret) != "\xaa\xbb" || bk.ExpiresAt == nil || bk.RevokedAt == nil {
		t.Fatalf("bucket token %+v %v", bk, err)
	}

	// A grants token round-trips with its grants in order.
	w, err := s.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Grant{{DatabaseID: "db_a", BranchID: "br_a", Access: "write"}, {BucketID: "bk_a", Access: "read"}}
	got, err := s.CreateToken(ctx, Token{ID: "tok_g", Name: "agent", SecretHash: []byte{4}, WorkspaceID: w.ID, Scope: ScopeGrants, Access: "write", Grants: want}, 0)
	if err != nil || len(got.Grants) != 2 || got.Grants[0] != want[0] || got.Grants[1] != want[1] || got.DatabaseID != "" || got.BucketID != "" {
		t.Fatalf("grants token %+v %v", got, err)
	}
	list, err := s.ListTokens(ctx, w.ID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range list {
		if (tok.ID == "tok_g") != (len(tok.Grants) == 2) {
			t.Fatalf("listed %s with grants %+v", tok.ID, tok.Grants)
		}
	}
	// Grants belong to the scope grants, and it needs at least one.
	if _, err := s.CreateToken(ctx, Token{ID: "tok_x1", Name: "x", SecretHash: []byte{5}, WorkspaceID: w.ID, Scope: ScopeGrants, Access: "read"}, 0); err == nil {
		t.Error("a grants token without grants was stored")
	}
	if _, err := s.CreateToken(ctx, Token{ID: "tok_x2", Name: "x", SecretHash: []byte{6}, Scope: "instance", Access: "read", Grants: want}, 0); err == nil {
		t.Error("an instance token with grants was stored")
	}
	// The CHECKs hold: a grant names a database or a bucket, never both or
	// neither, and a branch needs a database.
	for i, q := range []string{
		`INSERT INTO token_grants (token_id, position, access) VALUES ('tok_g', 5, 'read')`,
		`INSERT INTO token_grants (token_id, position, database_id, bucket_id, access) VALUES ('tok_g', 6, 'db_a', 'bk_a', 'read')`,
		`INSERT INTO token_grants (token_id, position, branch_id, bucket_id, access) VALUES ('tok_g', 7, 'br_a', 'bk_a', 'read')`,
		`INSERT INTO token_grants (token_id, position, bucket_id, access) VALUES ('tok_g', 8, 'bk_a', 'owner')`,
		`INSERT INTO token_grants (token_id, position, bucket_id, access) VALUES ('tok_none', 0, 'bk_a', 'read')`,
		`INSERT INTO tokens (id, name, secret_hash, scope, workspace_id, database_id, access, created_at) VALUES ('tok_x3', 'x', x'00', 'grants', (SELECT id FROM workspaces WHERE is_default = 1), 'db_a', 'read', 'now')`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err == nil {
			t.Errorf("insert %d passed the checks", i)
		}
	}
	// Deleting a token takes its grants with it.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tokens WHERE id = 'tok_g'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM token_grants`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d grants left behind, %v", n, err)
	}
}
