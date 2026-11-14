package meta

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zakiego/lumbung/apps/engine/internal/ids"
)

// v5Fixture is a version 5 meta.db shaped like a live instance: databases
// (one in the trash), branches, snapshots, a schedule, buckets with objects
// and an upload in progress, audit and stats rows, and every kind of token
// that version could hold, live, revoked and expired, a grants token
// included.
func v5Fixture(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	raw, err := sql.Open("sqlite3", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for i := 0; i < 5; i++ {
		if _, err := raw.ExecContext(ctx, migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	const at = "'2026-01-01T00:00:00.000000000Z'"
	const later = "'2026-06-01T00:00:00.000000000Z'"
	for _, q := range []string{
		`INSERT INTO databases (id, name, default_branch_id, created_at, updated_at) VALUES ('db_testing', 'testing', 'br_testing_main', ` + at + `, ` + later + `)`,
		`INSERT INTO databases (id, name, default_branch_id, created_at, updated_at) VALUES ('db_palui', 'palui', 'br_palui_main', ` + at + `, ` + at + `)`,
		`INSERT INTO databases (id, name, default_branch_id, created_at, updated_at, deleted_at, purge_after) VALUES ('db_gone', 'gone', 'br_gone_main', ` + at + `, ` + at + `, ` + later + `, ` + later + `)`,
		// A trashed database may share its name with a live one.
		`INSERT INTO databases (id, name, default_branch_id, created_at, updated_at, deleted_at, purge_after) VALUES ('db_gone2', 'testing', 'br_gone2_main', ` + at + `, ` + at + `, ` + later + `, ` + later + `)`,
		`INSERT INTO branches (id, database_id, name, protected, created_at, updated_at) VALUES ('br_testing_main', 'db_testing', 'main', 1, ` + at + `, ` + at + `)`,
		`INSERT INTO branches (id, database_id, name, parent_id, expires_at, created_at, updated_at) VALUES ('br_testing_dev', 'db_testing', 'dev', 'br_testing_main', ` + later + `, ` + at + `, ` + at + `)`,
		`INSERT INTO branches (id, database_id, name, protected, created_at, updated_at) VALUES ('br_palui_main', 'db_palui', 'main', 1, ` + at + `, ` + at + `)`,
		`INSERT INTO branches (id, database_id, name, protected, created_at, updated_at) VALUES ('br_gone_main', 'db_gone', 'main', 1, ` + at + `, ` + at + `)`,
		`INSERT INTO branches (id, database_id, name, protected, created_at, updated_at) VALUES ('br_gone2_main', 'db_gone2', 'main', 1, ` + at + `, ` + at + `)`,
		`INSERT INTO snapshots (id, database_id, branch_id, branch_name, kind, label, size_bytes, created_at) VALUES ('sn_a', 'db_testing', 'br_testing_main', 'main', 'manual', 'before', 4096, ` + at + `)`,
		`INSERT INTO snapshot_schedules (branch_id, database_id, every_seconds, keep_count, created_at) VALUES ('br_palui_main', 'db_palui', 3600, 24, ` + at + `)`,

		`INSERT INTO buckets (id, name, public_read, cors_origins, size_bytes, object_count, created_at, updated_at) VALUES ('bk_photos', 'photos', 1, '["https://example.com"]', 11, 2, ` + at + `, ` + later + `)`,
		`INSERT INTO buckets (id, name, created_at, updated_at) VALUES ('bk_palui_files', 'palui-files', ` + at + `, ` + at + `)`,
		`INSERT INTO objects (bucket_id, key, blob_id, size, etag, content_type, modified_at) VALUES ('bk_photos', 'a.jpg', 'ob_a', 5, 'e1', 'image/jpeg', ` + at + `)`,
		`INSERT INTO objects (bucket_id, key, blob_id, size, etag, content_type, headers, metadata, parts, modified_at) VALUES ('bk_photos', 'dir/b.jpg', 'ob_b', 6, 'e2-2', 'image/jpeg', '{"cache-control":"no-store"}', '{"k":"v"}', 2, ` + at + `)`,
		`INSERT INTO uploads (id, bucket_id, key, content_type, token_id, created_at) VALUES ('up_a', 'bk_palui_files', 'big.bin', 'application/octet-stream', 'tok_grants', ` + at + `)`,
		`INSERT INTO upload_parts (upload_id, part_number, file, size, etag, created_at) VALUES ('up_a', 1, '1-x', 7, 'p1', ` + at + `)`,
		`INSERT INTO blob_gc (path, delete_after) VALUES ('objects/bk_photos/ob/ob_old', ` + later + `)`,

		// Every kind of token version 5 could hold.
		`INSERT INTO tokens (id, name, secret_hash, scope, access, service, created_at, last_used_at) VALUES ('tok_service', 'service', x'01', 'instance', 'admin', 1, ` + at + `, ` + later + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, access, created_at, last_used_at) VALUES ('tok_admin', 'mcporter', x'02', 'instance', 'admin', ` + at + `, ` + later + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, access, access_secret, created_at, expires_at) VALUES ('tok_inst_read', 'viewer', x'03', 'instance', 'read', x'cc01', ` + at + `, ` + later + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, database_id, access, created_at) VALUES ('tok_db', 'app', x'04', 'database', 'db_testing', 'write', ` + at + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, database_id, branch_id, access, created_at) VALUES ('tok_branch', 'preview', x'05', 'database', 'db_testing', 'br_testing_dev', 'admin', ` + at + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, bucket_id, access, access_secret, created_at) VALUES ('tok_bucket', 'uploader', x'06', 'bucket', 'bk_photos', 'write', x'aabb', ` + at + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, access, access_secret, created_at, last_used_at) VALUES ('tok_grants', 'palui', x'07', 'grants', 'write', x'ddee', ` + at + `, ` + later + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, access, created_at, revoked_at) VALUES ('tok_grants_revoked', 'old agent', x'08', 'grants', 'admin', ` + at + `, ` + later + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, database_id, access, created_at, revoked_at) VALUES ('tok_db_revoked', 'old', x'09', 'database', 'db_palui', 'read', ` + at + `, ` + later + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, database_id, access, created_at, expires_at) VALUES ('tok_db_trashed', 'trashed', x'0a', 'database', 'db_gone', 'read', ` + at + `, ` + at + `)`,
		`INSERT INTO tokens (id, name, secret_hash, scope, access, created_at, revoked_at) VALUES ('tok_inst_revoked', 'old admin', x'0b', 'instance', 'admin', ` + at + `, ` + later + `)`,
		`INSERT INTO token_grants (token_id, position, database_id, access) VALUES ('tok_grants', 0, 'db_palui', 'write')`,
		`INSERT INTO token_grants (token_id, position, bucket_id, access) VALUES ('tok_grants', 1, 'bk_palui_files', 'read')`,
		`INSERT INTO token_grants (token_id, position, database_id, branch_id, access) VALUES ('tok_grants', 2, 'db_testing', 'br_testing_dev', 'admin')`,
		`INSERT INTO token_grants (token_id, position, bucket_id, access) VALUES ('tok_grants_revoked', 0, 'bk_photos', 'admin')`,
		`INSERT INTO token_grants (token_id, position, database_id, access) VALUES ('tok_grants_revoked', 1, 'db_testing', 'read')`,

		`INSERT INTO audit (at, token_id, actor, action, target, result, request_id) VALUES (` + at + `, 'tok_service', 'dashboard:zaki', 'database.create', 'db_testing', 'ok', 'req-a')`,
		`INSERT INTO audit (at, token_id, action, target, result, code, request_id) VALUES (` + later + `, 'tok_admin', 'bucket.create', 'photos', 'error', 'name_taken', 'req-b')`,
		`INSERT INTO stats_minute (branch_id, minute, queries, rows_read, rows_written, errors, duration_hist) VALUES ('br_testing_main', ` + at + `, 3, 2, 1, 0, '[1]')`,
		`INSERT INTO slow_queries (branch_id, at, duration_ms, sql) VALUES ('br_testing_main', ` + at + `, 900, 'SELECT 1')`,
		`INSERT INTO idempotency (token_id, key, method, path, request_hash, status, body, created_at) VALUES ('tok_admin', 'k1', 'POST', '/v1/databases', x'00', 201, x'7b7d', ` + at + `)`,
		`PRAGMA user_version = 5`,
	} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// rows reads a whole query as text, for comparing tables across a migration.
func rows(t *testing.T, db *sql.DB, q string) [][]string {
	t.Helper()
	rs, err := db.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	var out [][]string
	for rs.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			row[i] = "NULL"
			if v.Valid {
				row[i] = fmt.Sprintf("%q", v.String)
			}
		}
		out = append(out, row)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Migration 6 moves a live instance into the default workspace. It can't be
// undone on live data, so every table it rebuilds is compared row for row
// with what was there before, and every kind of token is checked afterwards.
func TestMigration6KeepsALiveInstance(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "meta.db")
	v5Fixture(t, path)

	// What must come through unchanged, read before the migration. hex()
	// makes the blobs comparable as text.
	const (
		tokensQ = `SELECT id, name, hex(secret_hash), scope, database_id, branch_id, bucket_id, access, service, hex(access_secret), created_at, expires_at, last_used_at, revoked_at FROM tokens ORDER BY id`
		grantsQ = `SELECT token_id, position, database_id, branch_id, bucket_id, access FROM token_grants ORDER BY token_id, position`
		dbsQ    = `SELECT id, name, default_branch_id, created_at, updated_at, deleted_at, purge_after FROM databases ORDER BY id`
		bktsQ   = `SELECT id, name, public_read, cors_origins, size_bytes, object_count, created_at, updated_at FROM buckets ORDER BY id`
		auditQ  = `SELECT id, at, token_id, actor, action, target, result, code, request_id FROM audit ORDER BY id`
	)
	untouched := []string{
		`SELECT * FROM branches ORDER BY id`, `SELECT * FROM snapshots ORDER BY id`, `SELECT * FROM snapshot_schedules ORDER BY branch_id`,
		`SELECT bucket_id, key, blob_id, size, etag, content_type, headers, metadata, parts, modified_at FROM objects ORDER BY bucket_id, key`,
		`SELECT * FROM uploads ORDER BY id`, `SELECT * FROM upload_parts ORDER BY upload_id, part_number`, `SELECT * FROM blob_gc ORDER BY path`,
		`SELECT * FROM stats_minute ORDER BY branch_id, minute`, `SELECT id, branch_id, at, duration_ms, sql, error_code FROM slow_queries ORDER BY id`,
		`SELECT token_id, key, method, path, hex(request_hash), status, hex(body), created_at FROM idempotency ORDER BY token_id, key`,
	}
	before, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][][]string{}
	for _, q := range append([]string{tokensQ, grantsQ, dbsQ, bktsQ, auditQ}, untouched...) {
		want[q] = rows(t, before, q)
		if len(want[q]) == 0 {
			t.Fatalf("the fixture has no rows for %s", q)
		}
	}
	before.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()
	if s.Version() != Known() || Known() < 6 {
		t.Fatalf("version %d of %d", s.Version(), Known())
	}

	// Nothing rebuilt lost or changed a row, and nothing else was touched.
	for q, w := range want {
		if got := rows(t, s.db, q); !reflect.DeepEqual(got, w) {
			t.Errorf("%s changed:\n got %v\nwant %v", q, got, w)
		}
	}
	// No reference dangles and the file is sound.
	if bad := rows(t, s.db, `PRAGMA foreign_key_check`); len(bad) != 0 {
		t.Fatalf("foreign key violations after the migration: %v", bad)
	}
	if ok := rows(t, s.db, `PRAGMA integrity_check`); len(ok) != 1 || ok[0][0] != `"ok"` {
		t.Fatalf("integrity check: %v", ok)
	}
	if left := rows(t, s.db, `SELECT name FROM sqlite_schema WHERE name LIKE '%\_keep' ESCAPE '\' OR name LIKE '%\_new' ESCAPE '\'`); len(left) != 0 {
		t.Fatalf("the migration left tables behind: %v", left)
	}

	// One workspace, the default one, and everything is in it.
	def, err := s.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "default" || !def.Default || !ids.IsID(def.ID, ids.Workspace) || def.DeletedAt != nil || def.Limits != (WorkspaceLimits{}) || def.CreatedAt.IsZero() {
		t.Fatalf("default workspace %+v", def)
	}
	if all, err := s.ListWorkspaces(ctx); err != nil || len(all) != 1 || all[0].ID != def.ID {
		t.Fatalf("workspaces %+v %v", all, err)
	}
	if byName, err := s.GetWorkspace(ctx, "default"); err != nil || byName.ID != def.ID {
		t.Fatalf("default by name: %v", err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM databases WHERE workspace_id IS NOT ?`,
		`SELECT count(*) FROM buckets WHERE workspace_id IS NOT ?`,
		`SELECT count(*) FROM tokens WHERE scope != 'instance' AND workspace_id IS NOT ?`,
	} {
		var n int
		if err := s.db.QueryRowContext(ctx, q, def.ID).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows outside the default workspace, %v", q, n, err)
		}
	}
	var instWithWS int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM tokens WHERE scope = 'instance' AND workspace_id IS NOT NULL`).Scan(&instWithWS); err != nil || instWithWS != 0 {
		t.Fatalf("%d instance tokens were put in a workspace, %v", instWithWS, err)
	}

	// Each kind of token, through the store the engine reads it with.
	type wantTok struct {
		scope, ws, db, branch, bucket, access string
		service, key, revoked, expires        bool
		grants                                []Grant
	}
	for id, w := range map[string]wantTok{
		"tok_service":      {scope: "instance", access: "admin", service: true},
		"tok_admin":        {scope: "instance", access: "admin"},
		"tok_inst_read":    {scope: "instance", access: "read", key: true, expires: true},
		"tok_inst_revoked": {scope: "instance", access: "admin", revoked: true},
		"tok_db":           {scope: "database", ws: def.ID, db: "db_testing", access: "write"},
		"tok_branch":       {scope: "database", ws: def.ID, db: "db_testing", branch: "br_testing_dev", access: "admin"},
		"tok_bucket":       {scope: "bucket", ws: def.ID, bucket: "bk_photos", access: "write", key: true},
		"tok_db_revoked":   {scope: "database", ws: def.ID, db: "db_palui", access: "read", revoked: true},
		"tok_db_trashed":   {scope: "database", ws: def.ID, db: "db_gone", access: "read", expires: true},
		"tok_grants": {scope: "grants", ws: def.ID, access: "write", key: true, grants: []Grant{
			{DatabaseID: "db_palui", Access: "write"}, {BucketID: "bk_palui_files", Access: "read"},
			{DatabaseID: "db_testing", BranchID: "br_testing_dev", Access: "admin"},
		}},
		"tok_grants_revoked": {scope: "grants", ws: def.ID, access: "admin", revoked: true, grants: []Grant{
			{BucketID: "bk_photos", Access: "admin"}, {DatabaseID: "db_testing", Access: "read"},
		}},
	} {
		got, err := s.GetToken(ctx, id)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		if got.Scope != w.scope || got.WorkspaceID != w.ws || got.DatabaseID != w.db || got.BranchID != w.branch || got.BucketID != w.bucket ||
			got.Access != w.access || got.Service != w.service || (len(got.AccessSecret) > 0) != w.key ||
			(got.RevokedAt != nil) != w.revoked || (got.ExpiresAt != nil) != w.expires || !reflect.DeepEqual(got.Grants, w.grants) {
			t.Errorf("%s came through as %+v", id, got)
		}
	}
	if svc, ok, err := s.ServiceToken(ctx); err != nil || !ok || svc.ID != "tok_service" {
		t.Fatalf("service token %+v %v %v", svc, ok, err)
	}
	// Lists split by workspace: the default one holds the bound tokens, the
	// instance list the rest.
	bound, err := s.ListTokens(ctx, def.ID, "", "", 100)
	if err != nil || len(bound) != 5 { // the expired one is listed until it is revoked
		t.Fatalf("live tokens in the default workspace: %d %v", len(bound), err)
	}
	for _, tok := range bound {
		if tok.WorkspaceID != def.ID || tok.Scope == "instance" || (tok.ID == "tok_grants") != (len(tok.Grants) == 3) {
			t.Errorf("listed %+v in the default workspace", tok)
		}
	}
	if inst, err := s.ListTokens(ctx, "", "", "", 100); err != nil || len(inst) != 3 {
		t.Fatalf("live instance tokens: %d %v", len(inst), err)
	}

	// Lookups are by workspace now, by name and by id.
	if d, err := s.GetDatabase(ctx, def.ID, "testing"); err != nil || d.ID != "db_testing" || d.WorkspaceID != def.ID {
		t.Fatalf("database by name %+v %v", d, err)
	}
	if d, err := s.GetDeletedDatabase(ctx, def.ID, "gone"); err != nil || d.ID != "db_gone" || d.DeletedAt == nil {
		t.Fatalf("trashed database %+v %v", d, err)
	}
	if b, err := s.GetBucket(ctx, def.ID, "photos"); err != nil || b.ID != "bk_photos" || !b.PublicRead || len(b.CORSOrigins) != 1 || b.ObjectCount != 2 {
		t.Fatalf("bucket %+v %v", b, err)
	}
	// Audit entries from before workspaces stay with the instance: only an
	// instance admin could read them, and they name instance tokens and
	// dashboard users. The default workspace's log starts empty.
	if entries, err := s.ListAllAudit(ctx, "", 0, 10); err != nil || len(entries) != 2 || entries[1].Actor != "dashboard:zaki" || entries[0].Code != "name_taken" ||
		entries[0].WorkspaceID != "" || entries[1].WorkspaceID != "" {
		t.Fatalf("audit %+v %v", entries, err)
	}
	if entries, err := s.ListAudit(ctx, def.ID, 0, 10); err != nil || len(entries) != 0 {
		t.Fatalf("the default workspace's log holds old entries: %+v %v", entries, err)
	}

	// The schema holds the new rules.
	other, err := s.CreateWorkspace(ctx, "ws_otherotherotherother", "other", WorkspaceLimits{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	at := now()
	for i, q := range []string{
		// A token belongs to a workspace exactly when it isn't an instance token.
		`INSERT INTO tokens (id, name, secret_hash, scope, access, created_at) VALUES ('tok_x1', 'x', x'00', 'workspace', 'read', '` + at + `')`,
		`INSERT INTO tokens (id, name, secret_hash, scope, workspace_id, access, created_at) VALUES ('tok_x2', 'x', x'00', 'instance', '` + def.ID + `', 'read', '` + at + `')`,
		`INSERT INTO tokens (id, name, secret_hash, scope, database_id, access, created_at) VALUES ('tok_x3', 'x', x'00', 'database', 'db_testing', 'read', '` + at + `')`,
		// The service token is an instance token.
		`INSERT INTO tokens (id, name, secret_hash, scope, workspace_id, access, service, created_at) VALUES ('tok_x4', 'x', x'00', 'workspace', '` + def.ID + `', 'admin', 1, '` + at + `')`,
		// A workspace must exist.
		`INSERT INTO tokens (id, name, secret_hash, scope, workspace_id, access, created_at) VALUES ('tok_x5', 'x', x'00', 'workspace', 'ws_nope', 'read', '` + at + `')`,
		`INSERT INTO databases (id, workspace_id, name, default_branch_id, created_at, updated_at) VALUES ('db_x1', 'ws_nope', 'x', 'br_x', '` + at + `', '` + at + `')`,
		`INSERT INTO databases (id, name, default_branch_id, created_at, updated_at) VALUES ('db_x2', 'x', 'br_x', '` + at + `', '` + at + `')`,
		`INSERT INTO buckets (id, name, created_at, updated_at) VALUES ('bk_x1', 'xxx', '` + at + `', '` + at + `')`,
		// Names are unique inside a workspace.
		`INSERT INTO databases (id, workspace_id, name, default_branch_id, created_at, updated_at) VALUES ('db_x3', '` + def.ID + `', 'testing', 'br_x', '` + at + `', '` + at + `')`,
		`INSERT INTO buckets (id, workspace_id, name, created_at, updated_at) VALUES ('bk_x2', '` + def.ID + `', 'photos', '` + at + `', '` + at + `')`,
		// There is one default workspace, and it is never in the trash.
		`INSERT INTO workspaces (id, name, is_default, created_at, updated_at) VALUES ('ws_x1', 'second-default', 1, '` + at + `', '` + at + `')`,
		`UPDATE workspaces SET deleted_at = '` + at + `' WHERE is_default = 1`,
		`INSERT INTO workspaces (id, name, created_at, updated_at) VALUES ('ws_x2', 'other', '` + at + `', '` + at + `')`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err == nil {
			t.Errorf("statement %d passed the schema: %s", i, q)
		}
	}
	// The same names are free in another workspace.
	for _, q := range []string{
		`INSERT INTO databases (id, workspace_id, name, default_branch_id, created_at, updated_at) VALUES ('db_o1', '` + other.ID + `', 'testing', 'br_o1', '` + at + `', '` + at + `')`,
		`INSERT INTO buckets (id, workspace_id, name, created_at, updated_at) VALUES ('bk_o1', '` + other.ID + `', 'photos', '` + at + `', '` + at + `')`,
		`INSERT INTO tokens (id, name, secret_hash, scope, workspace_id, access, created_at) VALUES ('tok_o1', 'x', x'00', 'workspace', '` + other.ID + `', 'admin', '` + at + `')`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}

	// token_grants still hangs off the rebuilt tokens table: deleting a
	// token takes its grants, and a grant can't name a token that isn't there.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tokens WHERE id = 'tok_grants_revoked'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM token_grants WHERE token_id = 'tok_grants_revoked'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d grants outlived their token, %v", n, err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO token_grants (token_id, position, bucket_id, access) VALUES ('tok_nope', 0, 'bk_photos', 'read')`); err == nil {
		t.Fatal("a grant was stored for a token that doesn't exist")
	}
	// And the tables that reference databases and buckets still do.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM databases WHERE id = 'db_testing'`); err == nil {
		t.Fatal("a database with branches was deleted")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM buckets WHERE id = 'bk_photos'`); err == nil {
		t.Fatal("a bucket with objects was deleted")
	}

	// Opening again changes nothing.
	s.Close()
	again, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if d, err := again.DefaultWorkspace(ctx); err != nil || d.ID != def.ID {
		t.Fatalf("the default workspace changed on reopen: %+v %v", d, err)
	}
}

// A fresh meta.db gets the default workspace too, so a new instance works
// before anyone makes a workspace.
func TestFreshMetaHasDefaultWorkspace(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	def, err := s.DefaultWorkspace(ctx)
	if err != nil || def.Name != "default" || !def.Default || !ids.IsID(def.ID, ids.Workspace) {
		t.Fatalf("default workspace %+v %v", def, err)
	}
	if _, _, err := s.CreateDatabase(ctx, def.ID, "app", "db_aaaaaaaaaaaaaaaaaaaa", "br_aaaaaaaaaaaaaaaaaaaa", 10); err != nil {
		t.Fatal(err)
	}
}
