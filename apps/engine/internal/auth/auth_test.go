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

func TestGrantsScope(t *testing.T) {
	stored := meta.Token{ID: "tok_a", Scope: ScopeGrants, Access: AccessAdmin, Grants: []Grant{
		{DatabaseID: "db_a", Access: AccessWrite},
		{DatabaseID: "db_b", BranchID: "br_dev", Access: AccessAdmin},
		{BucketID: "bk_a", Access: AccessRead},
	}}
	p, err := FromToken(stored, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// The stored level is the highest among the grants; a principal never
	// carries it, so nothing can use it in place of a grant's own level.
	if p.Access != "" || p.InstanceAdmin() {
		t.Fatalf("grants principal carries a level: %+v", p)
	}
	stored.Grants[0].Access = AccessAdmin
	if p.Grants[0].Access != AccessWrite {
		t.Fatal("the principal shares the stored grants")
	}

	type dbCase struct {
		access, db, branch string
		want               bool
	}
	for i, c := range []dbCase{
		{AccessWrite, "db_a", "", true},
		{AccessWrite, "db_a", "br_any", true},
		{AccessAdmin, "db_a", "", false},
		{AccessAdmin, "db_b", "br_dev", true},
		{AccessRead, "db_b", "br_main", false},
		{AccessRead, "db_b", "", false},
		{AccessRead, "db_c", "", false},
		{AccessRead, "", "", false},
		{AccessRead, "bk_a", "", false},
	} {
		if got := p.AtLeast(c.access, c.db, c.branch); got != c.want {
			t.Errorf("AtLeast case %d: got %v", i, got)
		}
	}
	if !p.SeesDatabase("db_a") || !p.SeesDatabase("db_b") || p.SeesDatabase("db_c") || p.SeesDatabase("") || p.SeesDatabase("bk_a") {
		t.Fatal("SeesDatabase")
	}
	if !p.WholeDatabase("db_a") || p.WholeDatabase("db_b") || p.WholeDatabase("db_c") || p.WholeDatabase("") {
		t.Fatal("WholeDatabase")
	}
	if !p.SeesBranch("db_a", "br_any") || !p.SeesBranch("db_b", "br_dev") || p.SeesBranch("db_b", "br_main") || p.SeesBranch("db_b", "") || p.SeesBranch("db_c", "br_dev") {
		t.Fatal("SeesBranch")
	}
	if !p.SeesBucket("bk_a") || p.SeesBucket("bk_b") || p.SeesBucket("") || p.SeesBucket("db_a") {
		t.Fatal("SeesBucket")
	}
	// The bucket grant is read: neither the write nor the admin grant on a
	// database lifts it.
	if !p.BucketAtLeast(AccessRead, "bk_a") || p.BucketAtLeast(AccessWrite, "bk_a") || p.BucketAtLeast(AccessRead, "bk_b") || p.BucketAtLeast(AccessRead, "") {
		t.Fatal("BucketAtLeast")
	}
	if !p.HasAdmin() || (Principal{Scope: ScopeGrants, Grants: stored.Grants[2:]}).HasAdmin() {
		t.Fatal("HasAdmin")
	}

	// It is an admin of db_b's dev branch only, so that is all it may hand out.
	if !p.CoversToken(ScopeDatabase, []Grant{{DatabaseID: "db_b", BranchID: "br_dev", Access: AccessWrite}}) {
		t.Fatal("doesn't cover its admin grant")
	}
	for i, g := range [][]Grant{
		{{DatabaseID: "db_b", Access: AccessRead}},
		{{DatabaseID: "db_b", BranchID: "br_main", Access: AccessRead}},
		{{DatabaseID: "db_a", Access: AccessRead}},
		{{BucketID: "bk_a", Access: AccessRead}},
		{{DatabaseID: "db_b", BranchID: "br_dev", Access: AccessRead}, {BucketID: "bk_a", Access: AccessRead}},
		{{DatabaseID: "db_b", BranchID: "br_dev", Access: "owner"}},
		{{DatabaseID: "db_b", BranchID: "br_dev", BucketID: "bk_a", Access: AccessRead}},
		{{Access: AccessRead}},
		{},
	} {
		if p.CoversToken(ScopeGrants, g) {
			t.Errorf("covers case %d: %+v", i, g)
		}
	}
	if p.CoversToken(ScopeInstance, nil) || p.Covers(ScopeInstance, "", "", "", AccessRead) {
		t.Fatal("covers an instance token")
	}
	// A grants principal with no grants reaches nothing.
	empty := Principal{Scope: ScopeGrants, Access: AccessAdmin}
	if empty.AtLeast(AccessRead, "db_a", "") || empty.SeesDatabase("db_a") || empty.SeesBucket("bk_a") || empty.BucketAtLeast(AccessRead, "bk_a") ||
		empty.HasAdmin() || empty.WholeDatabase("db_a") || empty.CoversToken(ScopeDatabase, []Grant{{DatabaseID: "db_a", Access: AccessRead}}) {
		t.Fatal("a grants principal without grants reaches something")
	}

	// TokenGrants gives every scope the same shape.
	if g := TokenGrants(meta.Token{Scope: ScopeDatabase, DatabaseID: "db_a", BranchID: "br_x", Access: AccessRead}); len(g) != 1 || g[0] != (Grant{DatabaseID: "db_a", BranchID: "br_x", Access: AccessRead}) {
		t.Fatalf("database token grants %+v", g)
	}
	if g := TokenGrants(meta.Token{Scope: ScopeBucket, BucketID: "bk_a", Access: AccessWrite}); len(g) != 1 || g[0] != (Grant{BucketID: "bk_a", Access: AccessWrite}) {
		t.Fatalf("bucket token grants %+v", g)
	}
	if len(TokenGrants(meta.Token{Scope: ScopeInstance, Access: AccessAdmin})) != 0 || len(TokenGrants(stored)) != 3 {
		t.Fatal("instance or grants token grants")
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
