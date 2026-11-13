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
	if !dbAdmin.Covers(ScopeDatabase, "db_a", "", AccessWrite) || dbAdmin.Covers(ScopeInstance, "", "", AccessRead) || dbAdmin.Covers(ScopeDatabase, "db_b", "", AccessRead) {
		t.Fatal("database admin covers")
	}
	if brWrite.Covers(ScopeDatabase, "db_a", "br_x", AccessRead) {
		t.Fatal("a write token minted tokens")
	}
}
