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

// The older tests all run in one workspace. db, bk and tok build records
// in it, so every assertion below is about scopes and grants, as it was
// before workspaces; TestWorkspaceBoundary covers the boundary itself.
const wsA, wsB = "ws_a", "ws_b"

func db(id string) meta.Database { return meta.Database{ID: id, WorkspaceID: wsA} }
func bk(id string) meta.Bucket   { return meta.Bucket{ID: id, WorkspaceID: wsA} }

// in makes a principal act in workspace A.
func in(p Principal) Principal {
	p.WorkspaceID = wsA
	return p
}

// tok is a stored token of workspace A with the given scope and grants, in
// the one shape each scope is stored in.
func tok(scope string, grants []Grant) meta.Token {
	t := meta.Token{Scope: scope, WorkspaceID: wsA}
	switch {
	case scope == ScopeInstance:
		t.WorkspaceID = ""
	case scope == ScopeDatabase && len(grants) == 1:
		t.DatabaseID, t.BranchID, t.Access = grants[0].DatabaseID, grants[0].BranchID, grants[0].Access
	case scope == ScopeBucket && len(grants) == 1:
		t.BucketID, t.Access = grants[0].BucketID, grants[0].Access
	default:
		t.Grants = grants
	}
	return t
}

// covers is CoversToken for a token with one scope.
func covers(p Principal, scope, dbID, branchID, bucketID, access string) bool {
	switch scope {
	case ScopeDatabase:
		return p.CoversToken(tok(scope, []Grant{{DatabaseID: dbID, BranchID: branchID, Access: access}}))
	case ScopeBucket:
		return p.CoversToken(tok(scope, []Grant{{BucketID: bucketID, Access: access}}))
	}
	return p.CoversToken(meta.Token{Scope: scope, Access: access})
}

func TestPrincipal(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	if _, err := FromToken(meta.Token{Scope: ScopeInstance, ExpiresAt: &past}, time.Now()); err == nil {
		t.Fatal("expired token accepted")
	}
	if _, err := FromToken(meta.Token{Scope: ScopeInstance, RevokedAt: &past}, time.Now()); err == nil {
		t.Fatal("revoked token accepted")
	}
	admin := in(Principal{Scope: ScopeInstance, Access: AccessAdmin})
	dbRead := in(Principal{Scope: ScopeDatabase, DatabaseID: "db_a", Access: AccessRead})
	brWrite := in(Principal{Scope: ScopeDatabase, DatabaseID: "db_a", BranchID: "br_x", Access: AccessWrite})
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
		if got := c.p.AtLeast(c.access, db(c.db), c.branch); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
	dbAdmin := in(Principal{Scope: ScopeDatabase, DatabaseID: "db_a", Access: AccessAdmin})
	if !covers(dbAdmin, ScopeDatabase, "db_a", "", "", AccessWrite) || covers(dbAdmin, ScopeInstance, "", "", "", AccessRead) || covers(dbAdmin, ScopeDatabase, "db_b", "", "", AccessRead) {
		t.Fatal("database admin covers")
	}
	if covers(brWrite, ScopeDatabase, "db_a", "br_x", "", AccessRead) {
		t.Fatal("a write token minted tokens")
	}
}

func TestBucketScope(t *testing.T) {
	bucketRead := in(Principal{Scope: ScopeBucket, BucketID: "bk_a", Access: AccessRead})
	bucketAdmin := in(Principal{Scope: ScopeBucket, BucketID: "bk_a", Access: AccessAdmin})
	dbAdmin := in(Principal{Scope: ScopeDatabase, DatabaseID: "db_a", Access: AccessAdmin})
	instRead := in(Principal{Scope: ScopeInstance, Access: AccessRead})

	if !bucketRead.BucketAtLeast(AccessRead, bk("bk_a")) || bucketRead.BucketAtLeast(AccessWrite, bk("bk_a")) || bucketRead.BucketAtLeast(AccessRead, bk("bk_b")) {
		t.Fatal("bucket read token")
	}
	if bucketRead.SeesBucket(bk("")) || bucketRead.SeesDatabase(db("")) || bucketRead.SeesDatabase(db("db_a")) || bucketRead.AtLeast(AccessRead, db(""), "") {
		t.Fatal("a bucket token reaches databases")
	}
	if dbAdmin.SeesBucket(bk("bk_a")) || dbAdmin.BucketAtLeast(AccessRead, bk("bk_a")) || dbAdmin.SeesBucket(bk("")) {
		t.Fatal("a database token reaches buckets")
	}
	if !instRead.BucketAtLeast(AccessRead, bk("bk_z")) || instRead.BucketAtLeast(AccessWrite, bk("bk_z")) {
		t.Fatal("instance read token")
	}
	if !covers(bucketAdmin, ScopeBucket, "", "", "bk_a", AccessWrite) || covers(bucketAdmin, ScopeBucket, "", "", "bk_b", AccessRead) ||
		covers(bucketAdmin, ScopeDatabase, "db_a", "", "", AccessRead) || covers(bucketAdmin, ScopeInstance, "", "", "", AccessRead) {
		t.Fatal("bucket admin covers")
	}
	if covers(dbAdmin, ScopeBucket, "", "", "bk_a", AccessRead) || covers(bucketRead, ScopeBucket, "", "", "bk_a", AccessRead) {
		t.Fatal("covers across scopes or without admin")
	}
}

func TestGrantsScope(t *testing.T) {
	stored := meta.Token{ID: "tok_a", WorkspaceID: wsA, Scope: ScopeGrants, Access: AccessAdmin, Grants: []Grant{
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
		if got := p.AtLeast(c.access, db(c.db), c.branch); got != c.want {
			t.Errorf("AtLeast case %d: got %v", i, got)
		}
	}
	if !p.SeesDatabase(db("db_a")) || !p.SeesDatabase(db("db_b")) || p.SeesDatabase(db("db_c")) || p.SeesDatabase(db("")) || p.SeesDatabase(db("bk_a")) {
		t.Fatal("SeesDatabase")
	}
	if !p.WholeDatabase(db("db_a")) || p.WholeDatabase(db("db_b")) || p.WholeDatabase(db("db_c")) || p.WholeDatabase(db("")) {
		t.Fatal("WholeDatabase")
	}
	if !p.SeesBranch(db("db_a"), "br_any") || !p.SeesBranch(db("db_b"), "br_dev") || p.SeesBranch(db("db_b"), "br_main") || p.SeesBranch(db("db_b"), "") || p.SeesBranch(db("db_c"), "br_dev") {
		t.Fatal("SeesBranch")
	}
	if !p.SeesBucket(bk("bk_a")) || p.SeesBucket(bk("bk_b")) || p.SeesBucket(bk("")) || p.SeesBucket(bk("db_a")) {
		t.Fatal("SeesBucket")
	}
	// The bucket grant is read: neither the write nor the admin grant on a
	// database lifts it.
	if !p.BucketAtLeast(AccessRead, bk("bk_a")) || p.BucketAtLeast(AccessWrite, bk("bk_a")) || p.BucketAtLeast(AccessRead, bk("bk_b")) || p.BucketAtLeast(AccessRead, bk("")) {
		t.Fatal("BucketAtLeast")
	}
	if !p.HasAdmin() || in(Principal{Scope: ScopeGrants, Grants: stored.Grants[2:]}).HasAdmin() {
		t.Fatal("HasAdmin")
	}

	// It is an admin of db_b's dev branch only, so that is all it may hand out.
	if !p.CoversToken(tok(ScopeDatabase, []Grant{{DatabaseID: "db_b", BranchID: "br_dev", Access: AccessWrite}})) {
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
		if p.CoversToken(tok(ScopeGrants, g)) {
			t.Errorf("covers case %d: %+v", i, g)
		}
	}
	if p.CoversToken(tok(ScopeInstance, nil)) || covers(p, ScopeInstance, "", "", "", AccessRead) {
		t.Fatal("covers an instance token")
	}
	// A grants principal with no grants reaches nothing.
	empty := in(Principal{Scope: ScopeGrants, Access: AccessAdmin})
	if empty.AtLeast(AccessRead, db("db_a"), "") || empty.SeesDatabase(db("db_a")) || empty.SeesBucket(bk("bk_a")) || empty.BucketAtLeast(AccessRead, bk("bk_a")) ||
		empty.HasAdmin() || empty.WholeDatabase(db("db_a")) || empty.CoversToken(tok(ScopeDatabase, []Grant{{DatabaseID: "db_a", Access: AccessRead}})) {
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

// The workspace boundary (PLAN 17.9, ADR 0008): every check starts from the
// record's workspace, for every scope, the instance included.
func TestWorkspaceBoundary(t *testing.T) {
	dbA, dbB := meta.Database{ID: "db_a", WorkspaceID: wsA}, meta.Database{ID: "db_b", WorkspaceID: wsB}
	bkA, bkB := meta.Bucket{ID: "bk_a", WorkspaceID: wsA}, meta.Bucket{ID: "bk_b", WorkspaceID: wsB}
	// A record with the same id in the other workspace: an id alone must
	// never be enough.
	dbAinB, bkAinB := meta.Database{ID: "db_a", WorkspaceID: wsB}, meta.Bucket{ID: "bk_a", WorkspaceID: wsB}

	reaches := func(p Principal, d meta.Database, b meta.Bucket) bool {
		return p.SeesDatabase(d) || p.WholeDatabase(d) || p.SeesBranch(d, "br_x") || p.AtLeast(AccessRead, d, "") ||
			p.AtLeast(AccessRead, d, "br_x") || p.SeesBucket(b) || p.BucketAtLeast(AccessRead, b)
	}
	principals := map[string]Principal{
		"instance admin acting in A": in(Principal{Scope: ScopeInstance, Access: AccessAdmin}),
		"workspace admin of A":       in(Principal{Scope: ScopeWorkspace, Access: AccessAdmin}),
		"workspace read of A":        in(Principal{Scope: ScopeWorkspace, Access: AccessRead}),
		"database admin in A":        in(Principal{Scope: ScopeDatabase, DatabaseID: "db_a", Access: AccessAdmin}),
		"branch token in A":          in(Principal{Scope: ScopeDatabase, DatabaseID: "db_a", BranchID: "br_x", Access: AccessWrite}),
		"bucket admin in A":          in(Principal{Scope: ScopeBucket, BucketID: "bk_a", Access: AccessAdmin}),
		"grants token in A": in(Principal{Scope: ScopeGrants, Grants: []Grant{
			{DatabaseID: "db_a", Access: AccessAdmin}, {BucketID: "bk_a", Access: AccessAdmin},
			// Grants that name the other workspace's ids must still reach nothing there.
			{DatabaseID: "db_b", Access: AccessAdmin}, {BucketID: "bk_b", Access: AccessAdmin},
		}}),
	}
	for name, p := range principals {
		if !reaches(p, dbA, bkA) {
			t.Errorf("%s reaches nothing in its own workspace", name)
		}
		if reaches(p, dbB, bkB) {
			t.Errorf("%s reaches workspace B", name)
		}
		if reaches(p, dbAinB, bkAinB) {
			t.Errorf("%s reaches a record of workspace B that shares an id with one of its own", name)
		}
		if reaches(p, meta.Database{ID: "db_a"}, meta.Bucket{ID: "bk_a"}) {
			t.Errorf("%s reaches a record with no workspace", name)
		}
	}

	// A principal that acts in no workspace reaches nothing, whatever its
	// scope and level: an instance token before In, and a zero principal.
	for name, p := range map[string]Principal{
		"instance admin before In": {Scope: ScopeInstance, Access: AccessAdmin},
		"zero principal":           {},
		"workspace admin, no id":   {Scope: ScopeWorkspace, Access: AccessAdmin},
	} {
		if reaches(p, dbA, bkA) || reaches(p, meta.Database{ID: "db_a"}, meta.Bucket{ID: "bk_a"}) || p.WorkspaceAtLeast(AccessRead) {
			t.Errorf("%s reaches something", name)
		}
	}

	// In: an instance principal acts in any workspace, one at a time. A
	// bound one acts only in its own.
	inst := Principal{Scope: ScopeInstance, Access: AccessAdmin}
	if p, ok := inst.In(wsB); !ok || p.WorkspaceID != wsB || !reaches(p, dbB, bkB) || reaches(p, dbA, bkA) {
		t.Fatal("instance principal In")
	}
	if inst.WorkspaceID != "" {
		t.Fatal("In changed the principal it was called on")
	}
	wsAdmin := principals["workspace admin of A"]
	if p, ok := wsAdmin.In(wsA); !ok || p.WorkspaceID != wsA {
		t.Fatal("a workspace principal can't act in its own workspace")
	}
	for _, other := range []string{wsB, "", "ws_"} {
		if p, ok := wsAdmin.In(other); ok || p.WorkspaceID != "" || reaches(p, dbB, bkB) {
			t.Errorf("a workspace principal acts in %q", other)
		}
	}
	if _, ok := inst.In(""); ok {
		t.Fatal("an instance principal acts in no workspace")
	}
	if inst.Bound() || !wsAdmin.Bound() || !(Principal{}).Bound() {
		t.Fatal("Bound")
	}

	// Seeing a workspace: the instance sees all, a bound principal its own.
	if !inst.SeesWorkspace(wsA) || !inst.SeesWorkspace(wsB) || inst.SeesWorkspace("") ||
		!wsAdmin.SeesWorkspace(wsA) || wsAdmin.SeesWorkspace(wsB) || wsAdmin.SeesWorkspace("") ||
		principals["database admin in A"].SeesWorkspace(wsB) {
		t.Fatal("SeesWorkspace")
	}

	// Workspace levels. Only an instance or workspace principal has a level
	// on the workspace as a whole; an admin of one database never does.
	read, write := principals["workspace read of A"], in(Principal{Scope: ScopeWorkspace, Access: AccessWrite})
	if !read.WorkspaceAtLeast(AccessRead) || read.WorkspaceAtLeast(AccessWrite) || read.AtLeast(AccessWrite, dbA, "") || read.BucketAtLeast(AccessWrite, bkA) ||
		!write.WorkspaceAtLeast(AccessWrite) || write.WorkspaceAtLeast(AccessAdmin) || !write.AtLeast(AccessWrite, dbA, "br_x") || write.AtLeast(AccessAdmin, dbA, "") ||
		!wsAdmin.WorkspaceAtLeast(AccessAdmin) || !wsAdmin.AtLeast(AccessAdmin, dbA, "") || !wsAdmin.BucketAtLeast(AccessAdmin, bkA) {
		t.Fatal("workspace levels")
	}
	for name, p := range principals {
		wide := p.Scope == ScopeInstance || p.Scope == ScopeWorkspace
		if got := p.WorkspaceAtLeast(AccessRead); got != wide {
			t.Errorf("%s: WorkspaceAtLeast(read) = %v", name, got)
		}
	}
	if wsAdmin.WorkspaceAtLeast("") || wsAdmin.WorkspaceAtLeast("owner") || wsAdmin.AtLeast("", dbA, "") || wsAdmin.BucketAtLeast("owner", bkA) {
		t.Fatal("an unknown level passes")
	}
	// Only an instance admin operates the instance.
	if wsAdmin.InstanceAdmin() || !inst.InstanceAdmin() || (Principal{Scope: ScopeInstance, Access: AccessWrite}).InstanceAdmin() {
		t.Fatal("InstanceAdmin")
	}

	// Minting and managing tokens stops at the workspace.
	wsTokA := meta.Token{Scope: ScopeWorkspace, WorkspaceID: wsA, Access: AccessAdmin}
	wsTokB := meta.Token{Scope: ScopeWorkspace, WorkspaceID: wsB, Access: AccessRead}
	dbTokA := meta.Token{Scope: ScopeDatabase, WorkspaceID: wsA, DatabaseID: "db_a", Access: AccessRead}
	dbTokB := meta.Token{Scope: ScopeDatabase, WorkspaceID: wsB, DatabaseID: "db_a", Access: AccessRead}
	instTok := meta.Token{Scope: ScopeInstance, Access: AccessRead}
	if !wsAdmin.CoversToken(wsTokA) || !wsAdmin.CoversToken(dbTokA) {
		t.Fatal("a workspace admin doesn't cover its workspace's tokens")
	}
	if wsAdmin.CoversToken(wsTokB) || wsAdmin.CoversToken(dbTokB) || wsAdmin.CoversToken(instTok) ||
		wsAdmin.CoversToken(meta.Token{Scope: ScopeWorkspace, Access: AccessRead}) ||
		wsAdmin.CoversToken(meta.Token{Scope: ScopeInstance, WorkspaceID: wsA, Access: AccessRead}) {
		t.Fatal("a workspace admin covers a token outside its workspace")
	}
	if write.CoversToken(dbTokA) || read.CoversToken(dbTokA) || write.CoversToken(wsTokA) {
		t.Fatal("a workspace token below admin mints tokens")
	}
	dbAdmin := principals["database admin in A"]
	if !dbAdmin.CoversToken(dbTokA) || dbAdmin.CoversToken(wsTokA) || dbAdmin.CoversToken(dbTokB) || dbAdmin.CoversToken(instTok) {
		t.Fatal("database admin covers")
	}
	actingA := principals["instance admin acting in A"]
	if !actingA.CoversToken(wsTokA) || !actingA.CoversToken(dbTokA) || !actingA.CoversToken(instTok) || actingA.CoversToken(wsTokB) || actingA.CoversToken(dbTokB) {
		t.Fatal("an instance admin covers the instance's tokens and those of the workspace it acts in, no others")
	}
	if in(Principal{Scope: ScopeInstance, Access: AccessWrite}).CoversToken(dbTokA) || in(Principal{Scope: ScopeInstance, Access: AccessWrite}).CoversToken(instTok) {
		t.Fatal("an instance token below admin mints tokens")
	}

	// A stored token belongs to a workspace exactly when it isn't an
	// instance token. Anything else never becomes a principal.
	now := time.Now()
	for i, bad := range []meta.Token{
		{Scope: ScopeWorkspace, Access: AccessAdmin},
		{Scope: ScopeDatabase, DatabaseID: "db_a", Access: AccessRead},
		{Scope: ScopeInstance, WorkspaceID: wsA, Access: AccessAdmin},
		{Access: AccessAdmin},
	} {
		if _, err := FromToken(bad, now); err == nil {
			t.Errorf("token %d became a principal", i)
		}
	}
	if p, err := FromToken(wsTokA, now); err != nil || p.WorkspaceID != wsA || p.Scope != ScopeWorkspace {
		t.Fatalf("workspace token %+v %v", p, err)
	}
	if p, err := FromToken(instTok, now); err != nil || p.WorkspaceID != "" || p.Bound() {
		t.Fatalf("instance token %+v %v", p, err)
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

// Narrow lowers the service principal to a workspace principal, and never
// does anything else (PLAN 17.8).
func TestNarrow(t *testing.T) {
	dbA, dbB := meta.Database{ID: "db_a", WorkspaceID: wsA}, meta.Database{ID: "db_b", WorkspaceID: wsB}
	service := in(Principal{TokenID: "tok_s", Scope: ScopeInstance, Access: AccessAdmin, Service: true, Actor: "dashboard:a@b.c"})
	for _, level := range []string{AccessRead, AccessWrite, AccessAdmin} {
		p, ok := service.Narrow(level)
		if !ok || p.Scope != ScopeWorkspace || p.WorkspaceID != wsA || p.Access != level || !p.Bound() || p.TokenID != "tok_s" || p.Actor != service.Actor {
			t.Fatalf("narrowed to %s: %+v %v", level, p, ok)
		}
		if p.InstanceAdmin() || p.SeesWorkspace(wsB) || p.SeesDatabase(dbB) || p.AtLeast(AccessRead, dbB, "") {
			t.Fatalf("narrowed to %s still reaches the instance or workspace B", level)
		}
		if p.CoversToken(meta.Token{Scope: ScopeInstance, Access: AccessRead}) || p.CoversToken(meta.Token{Scope: ScopeWorkspace, WorkspaceID: wsB, Access: AccessRead}) {
			t.Fatalf("narrowed to %s covers a token outside workspace A", level)
		}
		if got := p.AtLeast(AccessWrite, dbA, ""); got != (level != AccessRead) {
			t.Fatalf("narrowed to %s: write on its own workspace = %v", level, got)
		}
		if got := p.WorkspaceAtLeast(AccessAdmin); got != (level == AccessAdmin) {
			t.Fatalf("narrowed to %s: workspace admin = %v", level, got)
		}
		// It can't act in another workspace afterwards.
		if _, ok := p.In(wsB); ok {
			t.Fatalf("narrowed to %s acts in workspace B", level)
		}
	}
	// It only lowers, only for the service token, and only once a workspace
	// is chosen.
	for name, p := range map[string]Principal{
		"an unknown level":          service,
		"an instance token":         in(Principal{Scope: ScopeInstance, Access: AccessAdmin}),
		"a workspace token":         in(Principal{Scope: ScopeWorkspace, Access: AccessAdmin}),
		"a workspace service token": in(Principal{Scope: ScopeWorkspace, Access: AccessRead, Service: true}),
		"no workspace chosen":       {Scope: ScopeInstance, Access: AccessAdmin, Service: true},
	} {
		level := AccessRead
		if name == "an unknown level" {
			level = "owner"
		}
		if q, ok := p.Narrow(level); ok || q.Scope != "" || q.WorkspaceID != "" {
			t.Errorf("%s was narrowed: %+v", name, q)
		}
	}
	for _, level := range []string{"", "ADMIN", "instance", "*"} {
		if _, ok := service.Narrow(level); ok {
			t.Errorf("narrowed to %q", level)
		}
	}
	readService := in(Principal{Scope: ScopeInstance, Access: AccessRead, Service: true})
	if _, ok := readService.Narrow(AccessWrite); ok {
		t.Fatal("narrowing raised a level")
	}
	if p, ok := readService.Narrow(AccessRead); !ok || p.Access != AccessRead {
		t.Fatal("narrowing to its own level failed")
	}
}
