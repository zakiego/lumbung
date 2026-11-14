package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

func TestTokenRoundTrip(t *testing.T) {
	id, tok := New()
	if !strings.HasPrefix(tok, "lmb_") || !strings.HasPrefix(id, "tok_") {
		t.Fatalf("token %q id %q", tok, id)
	}
	got, err := Parse(tok)
	if err != nil || got != id {
		t.Fatalf("parse: %q %v", got, err)
	}
	h := Hash(tok)
	if !Matches(tok, h) || Matches(tok+"x", h) || Matches(strings.Replace(tok, tok[len(tok)-1:], "!", 1), h) {
		t.Fatal("match")
	}
	id2, tok2 := New()
	if id2 == id || tok2 == tok {
		t.Fatal("not random")
	}
	for _, bad := range []string{"", "lmb_", tok + "a", tok[:len(tok)-1], strings.Replace(tok, "lmb_", "xyz_", 1), "lmb_ABCDEFGHIJKLMNOPQRST_" + strings.Repeat("a", 40)} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("parsed %q", bad)
		}
	}
}

func TestPrincipal(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	if _, err := FromToken(meta.Token{ExpiresAt: &past}, time.Now()); err == nil {
		t.Fatal("expired token accepted")
	}
	if _, err := FromToken(meta.Token{RevokedAt: &past}, time.Now()); err == nil {
		t.Fatal("revoked token accepted")
	}
	admin := Principal{Scope: ScopeInstance, Access: AccessAdmin}
	dbRead := Principal{Scope: ScopeDatabase, DatabaseID: "db_a", Access: AccessRead}
	brWrite := Principal{Scope: ScopeDatabase, DatabaseID: "db_a", BranchID: "br_x", Access: AccessWrite}
	cases := []struct {
		p                  Principal
		access, db, branch string
		want               bool
	}{
		{admin, AccessAdmin, "db_z", "", true},
		{dbRead, AccessRead, "db_a", "br_any", true},
		{dbRead, AccessWrite, "db_a", "br_any", false},
		{dbRead, AccessRead, "db_b", "", false},
		{brWrite, AccessWrite, "db_a", "br_x", true},
		{brWrite, AccessRead, "db_a", "br_y", false},
		{brWrite, AccessRead, "db_a", "", false},
	}
	for i, c := range cases {
		if got := c.p.AtLeast(c.access, c.db, c.branch); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
	dbAdmin := Principal{Scope: ScopeDatabase, DatabaseID: "db_a", Access: AccessAdmin}
	if !dbAdmin.Covers(ScopeDatabase, "db_a", "", "", AccessWrite) || dbAdmin.Covers(ScopeInstance, "", "", "", AccessRead) || dbAdmin.Covers(ScopeDatabase, "db_b", "", "", AccessRead) {
		t.Fatal("database admin covers")
	}
	if brWrite.Covers(ScopeDatabase, "db_a", "br_x", "", AccessRead) {
		t.Fatal("a write token minted tokens")
	}
}

func TestBucketScope(t *testing.T) {
	bucketRead := Principal{Scope: ScopeBucket, BucketID: "bk_a", Access: AccessRead}
	bucketAdmin := Principal{Scope: ScopeBucket, BucketID: "bk_a", Access: AccessAdmin}
	dbAdmin := Principal{Scope: ScopeDatabase, DatabaseID: "db_a", Access: AccessAdmin}
	instRead := Principal{Scope: ScopeInstance, Access: AccessRead}

	if !bucketRead.BucketAtLeast(AccessRead, "bk_a") || bucketRead.BucketAtLeast(AccessWrite, "bk_a") || bucketRead.BucketAtLeast(AccessRead, "bk_b") {
		t.Fatal("bucket read token")
	}
	if bucketRead.SeesBucket("") || bucketRead.SeesDatabase("") || bucketRead.SeesDatabase("db_a") || bucketRead.AtLeast(AccessRead, "", "") {
		t.Fatal("a bucket token reaches databases")
	}
	if dbAdmin.SeesBucket("bk_a") || dbAdmin.BucketAtLeast(AccessRead, "bk_a") || dbAdmin.SeesBucket("") {
		t.Fatal("a database token reaches buckets")
	}
	if !instRead.BucketAtLeast(AccessRead, "bk_z") || instRead.BucketAtLeast(AccessWrite, "bk_z") {
		t.Fatal("instance read token")
	}
	if !bucketAdmin.Covers(ScopeBucket, "", "", "bk_a", AccessWrite) || bucketAdmin.Covers(ScopeBucket, "", "", "bk_b", AccessRead) ||
		bucketAdmin.Covers(ScopeDatabase, "db_a", "", "", AccessRead) || bucketAdmin.Covers(ScopeInstance, "", "", "", AccessRead) {
		t.Fatal("bucket admin covers")
	}
	if dbAdmin.Covers(ScopeBucket, "", "", "bk_a", AccessRead) || bucketRead.Covers(ScopeBucket, "", "", "bk_a", AccessRead) {
		t.Fatal("covers across scopes or without admin")
	}
}

func TestAccessKeyIDs(t *testing.T) {
	id, _ := New()
	akid := AccessKeyID(id)
	if !strings.HasPrefix(akid, "LMB") || len(akid) != 23 || strings.ToUpper(akid) != akid {
		t.Fatalf("access key id %q", akid)
	}
	if got, ok := TokenIDOfAccessKey(akid); !ok || got != id {
		t.Fatalf("round trip %q %v", got, ok)
	}
	for _, bad := range []string{"", "LMB", strings.ToLower(akid), "XYZ" + akid[3:], akid + "A", "LMB" + strings.Repeat("!", 20)} {
		if _, ok := TokenIDOfAccessKey(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	if s := NewAccessSecret(); len(s) != 40 || s == NewAccessSecret() {
		t.Fatalf("secret %q", s)
	}
}
