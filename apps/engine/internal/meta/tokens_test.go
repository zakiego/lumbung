package meta

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A grants token goes with whatever it reaches, whether it is live or
// already revoked, and its grant rows never hold a delete back through the
// foreign keys.
func TestGrantsTokensGoWithWhatTheyReach(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, err := s.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range [][3]string{{"purged", "db_purged00000000000000", "br_purged00000000000000"}, {"missing", "db_missing0000000000000", "br_missing0000000000000"}, {"kept", "db_kept0000000000000000", "br_kept0000000000000000"}} {
		if _, _, err := s.CreateDatabase(ctx, w.ID, d[0], d[1], d[2], 100); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateBucket(ctx, w.ID, "bk_files000000000000000", "files", 100); err != nil {
		t.Fatal(err)
	}
	token := func(id, dbID string, revoked bool) {
		t.Helper()
		_, err := s.CreateToken(ctx, Token{ID: id, Name: id, SecretHash: []byte(id), WorkspaceID: w.ID, Scope: ScopeGrants, Access: "write", Grants: []Grant{
			{DatabaseID: dbID, Access: "write"}, {BucketID: "bk_files000000000000000", Access: "read"},
		}}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if revoked {
			if ok, err := s.RevokeToken(ctx, id); err != nil || !ok {
				t.Fatalf("revoke %s: %v %v", id, ok, err)
			}
		}
	}
	token("tok_purged_live", "db_purged00000000000000", false)
	token("tok_purged_revoked", "db_purged00000000000000", true)
	token("tok_missing_live", "db_missing0000000000000", false)
	token("tok_missing_revoked", "db_missing0000000000000", true)
	token("tok_kept", "db_kept0000000000000000", false)
	gone := func(ids ...string) {
		t.Helper()
		for _, id := range ids {
			if _, err := s.GetToken(ctx, id); err != ErrNoToken {
				t.Errorf("%s is still there: %v", id, err)
			}
		}
	}

	// In the trash the token stays; purging removes it, revoked or not.
	if err := s.SoftDeleteDatabase(ctx, "db_purged00000000000000", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if tok, err := s.GetToken(ctx, "tok_purged_live"); err != nil || tok.RevokedAt != nil || len(tok.Grants) != 2 {
		t.Fatalf("token of a trashed database: %+v %v", tok, err)
	}
	if ok, err := s.PurgeDatabase(ctx, "db_purged00000000000000"); err != nil || !ok {
		t.Fatalf("purge: %v %v", ok, err)
	}
	gone("tok_purged_live", "tok_purged_revoked")

	// A restore that lost a database's files drops it and its tokens.
	if err := s.DropMissing(ctx, Present{Branches: map[string]bool{"br_kept0000000000000000": true}, Snapshots: map[string]bool{}, Trash: map[string]bool{}}); err != nil {
		t.Fatalf("drop missing: %v", err)
	}
	gone("tok_missing_live", "tok_missing_revoked")

	// Deleting the bucket revokes what is left, and the grants stay readable.
	if _, err := s.DeleteBucket(ctx, "bk_files000000000000000"); err != nil {
		t.Fatalf("delete bucket: %v", err)
	}
	if tok, err := s.GetToken(ctx, "tok_kept"); err != nil || tok.RevokedAt == nil || len(tok.Grants) != 2 {
		t.Fatalf("token after its bucket went: %+v %v", tok, err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM token_grants WHERE token_id != 'tok_kept'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d grant rows left behind, %v", n, err)
	}
}
