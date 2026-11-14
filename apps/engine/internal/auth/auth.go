// Package auth makes, parses and checks API tokens (PLAN 6.8).
//
// A token looks like lmb_<20 char id>_<40 char secret>. The id is public and
// indexes the record; only the SHA-256 of the whole token is stored, and it
// is compared in constant time. The full token is shown once, at creation.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

const (
	prefix    = "lmb_"
	idLen     = 20
	secretLen = 40
	alphabet  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	idChars   = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// Scopes and access levels.
const (
	// ScopeInstance is a token that belongs to no workspace: it operates the
	// instance and may act inside any workspace (ADR 0008).
	ScopeInstance = meta.ScopeInstance
	// ScopeWorkspace is a token for everything in one workspace, at one
	// level, including what is created later.
	ScopeWorkspace = "workspace"
	ScopeDatabase  = "database"
	ScopeBucket    = "bucket"
	// ScopeGrants is a token that reaches several databases and buckets,
	// each at its own level (ADR 0007).
	ScopeGrants = meta.ScopeGrants

	AccessRead  = "read"
	AccessWrite = "write"
	AccessAdmin = "admin"
)

// Grant is one thing a principal reaches: a database (or one branch of it)
// or a bucket, at one access level.
type Grant = meta.Grant

func randomString(n int, chars string) string {
	out := make([]byte, n)
	buf := make([]byte, n*2)
	limit := 256 - 256%len(chars)
	for i := 0; i < n; {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out[i] = chars[int(b)%len(chars)]
			i++
			if i == n {
				break
			}
		}
	}
	return string(out)
}

// New makes a token. It returns the record id (tok_<id>) and the full token.
func New() (id, token string) {
	short := randomString(idLen, idChars)
	return "tok_" + short, prefix + short + "_" + randomString(secretLen, alphabet)
}

// Hash is what's stored for a token.
func Hash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// ErrMalformed means the string isn't shaped like a token.
var ErrMalformed = errors.New("auth: malformed token")

// Parse returns the record id of a token-shaped string.
func Parse(token string) (string, error) {
	if len(token) != len(prefix)+idLen+1+secretLen || !strings.HasPrefix(token, prefix) || token[len(prefix)+idLen] != '_' {
		return "", ErrMalformed
	}
	short := token[len(prefix) : len(prefix)+idLen]
	for _, c := range short {
		if !strings.ContainsRune(idChars, c) {
			return "", ErrMalformed
		}
	}
	for _, c := range token[len(prefix)+idLen+1:] {
		if !strings.ContainsRune(alphabet, c) {
			return "", ErrMalformed
		}
	}
	return "tok_" + short, nil
}

// Matches compares a presented token with a stored hash in constant time.
func Matches(token string, hash []byte) bool {
	return subtle.ConstantTimeCompare(Hash(token), hash) == 1
}

// Principal is who a request acts as.
//
// Nothing outside this file decides access from the fields: every check goes
// through a method below. Two rules live here and nowhere else:
//
//  1. A principal acts in exactly one workspace per request, WorkspaceID.
//     Every check takes the record it is about and first compares the
//     record's workspace with it, for every scope, the instance included.
//     So a lookup a handler forgot to scope still reaches nothing outside
//     the request's workspace.
//  2. Below a whole workspace, a principal only reaches what one of its
//     grants names.
type Principal struct {
	TokenID string
	Name    string
	Scope   string
	// WorkspaceID is the workspace this principal acts in. A token below the
	// instance has its own, always. An instance principal has none until In
	// gives it the workspace of the request.
	WorkspaceID string
	DatabaseID  string
	BranchID    string
	BucketID    string
	// Access is the level of an instance, workspace, database or bucket
	// principal. It is empty for the scope grants, where each grant has its
	// own.
	Access string
	// Grants is what a principal with the scope grants reaches.
	Grants    []Grant
	Service   bool
	ExpiresAt *time.Time // nil: never
	Actor     string     // dashboard user, only honored for the service token
}

// FromToken builds a principal from a live token record. It fails for a
// revoked or expired token, and for a record that breaks the rule that a
// token belongs to a workspace exactly when it isn't an instance token.
func FromToken(t meta.Token, now time.Time) (Principal, error) {
	if t.RevokedAt != nil {
		return Principal{}, errors.New("auth: token revoked")
	}
	if t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) {
		return Principal{}, errors.New("auth: token expired")
	}
	if (t.Scope == ScopeInstance) != (t.WorkspaceID == "") {
		return Principal{}, errors.New("auth: token has no valid workspace")
	}
	p := Principal{
		TokenID: t.ID, Name: t.Name, Scope: t.Scope, WorkspaceID: t.WorkspaceID, DatabaseID: t.DatabaseID,
		BranchID: t.BranchID, BucketID: t.BucketID, Access: t.Access, Service: t.Service, ExpiresAt: t.ExpiresAt,
	}
	if t.Scope == ScopeGrants {
		// The stored level is the highest among the grants, for display. It
		// must never stand in for a grant's own level.
		p.Access = ""
		p.Grants = append([]Grant(nil), t.Grants...)
	}
	return p, nil
}

func rank(access string) int {
	switch access {
	case AccessRead:
		return 1
	case AccessWrite:
		return 2
	case AccessAdmin:
		return 3
	}
	return 0
}

// Bound reports whether the principal belongs to one workspace, which every
// token but an instance token does.
func (p Principal) Bound() bool { return p.Scope != ScopeInstance }

// In returns the principal acting in a workspace. An instance principal may
// act in any workspace, one per request. A bound principal acts only in its
// own: for any other workspace In reports false, and the caller answers as
// if the workspace didn't exist.
func (p Principal) In(wsID string) (Principal, bool) {
	if wsID == "" {
		return Principal{}, false
	}
	if p.Scope == ScopeInstance {
		p.WorkspaceID = wsID
		return p, true
	}
	if p.WorkspaceID != wsID {
		return Principal{}, false
	}
	return p, true
}

// in reports whether a record of the given workspace is in the workspace
// this principal acts in. Every check on a record starts here.
func (p Principal) in(wsID string) bool {
	return wsID != "" && p.WorkspaceID == wsID
}

// wide reports whether the principal has one level for everything in its
// workspace: an instance or a workspace principal.
func (p Principal) wide() bool {
	return p.Scope == ScopeInstance || p.Scope == ScopeWorkspace
}

// grants lists what a principal below a whole workspace reaches. A database
// or bucket principal has exactly one grant.
func (p Principal) grants() []Grant {
	switch p.Scope {
	case ScopeDatabase:
		return []Grant{{DatabaseID: p.DatabaseID, BranchID: p.BranchID, Access: p.Access}}
	case ScopeBucket:
		return []Grant{{BucketID: p.BucketID, Access: p.Access}}
	case ScopeGrants:
		return p.Grants
	}
	return nil
}

// InstanceAdmin reports whether the principal may operate the instance:
// workspaces and their limits, instance tokens, the instance audit log,
// moves between workspaces.
func (p Principal) InstanceAdmin() bool {
	return p.Scope == ScopeInstance && p.Access == AccessAdmin
}

// SeesWorkspace reports whether the principal may know a workspace exists:
// an instance principal sees every one, a bound one only its own.
func (p Principal) SeesWorkspace(wsID string) bool {
	if wsID == "" {
		return false
	}
	return p.Scope == ScopeInstance || p.WorkspaceID == wsID
}

// WorkspaceAtLeast reports whether the principal has at least the given
// access to the whole workspace it acts in. Creating, renaming, deleting and
// restoring databases and buckets, the trash and the audit log take admin.
// A database, bucket or grants principal never has it, at any level.
func (p Principal) WorkspaceAtLeast(access string) bool {
	return p.WorkspaceID != "" && p.wide() && rank(access) > 0 && rank(p.Access) >= rank(access)
}

// HasAdmin reports whether the principal is an admin of anything, which is
// what managing tokens takes.
func (p Principal) HasAdmin() bool {
	if p.wide() {
		return p.Access == AccessAdmin
	}
	for _, g := range p.grants() {
		if g.Access == AccessAdmin {
			return true
		}
	}
	return false
}

// AtLeast reports whether the principal has at least the given access to a
// database, and to a branch of it when branchID isn't empty. An empty
// branchID asks about the database as a whole, which a grant limited to one
// branch never gives.
func (p Principal) AtLeast(access string, d meta.Database, branchID string) bool {
	if !p.in(d.WorkspaceID) || d.ID == "" || rank(access) == 0 {
		return false
	}
	if p.wide() {
		return rank(p.Access) >= rank(access)
	}
	for _, g := range p.grants() {
		if g.DatabaseID != d.ID || rank(g.Access) < rank(access) {
			continue
		}
		if g.BranchID == "" || (branchID != "" && g.BranchID == branchID) {
			return true
		}
	}
	return false
}

// SeesDatabase reports whether the principal may know a database exists.
func (p Principal) SeesDatabase(d meta.Database) bool {
	if !p.in(d.WorkspaceID) || d.ID == "" {
		return false
	}
	if p.wide() {
		return rank(p.Access) > 0
	}
	for _, g := range p.grants() {
		if g.DatabaseID == d.ID {
			return true
		}
	}
	return false
}

// WholeDatabase reports whether the principal reaches a database as a
// whole, not only some of its branches.
func (p Principal) WholeDatabase(d meta.Database) bool {
	if !p.in(d.WorkspaceID) || d.ID == "" {
		return false
	}
	if p.wide() {
		return rank(p.Access) > 0
	}
	for _, g := range p.grants() {
		if g.DatabaseID == d.ID && g.BranchID == "" {
			return true
		}
	}
	return false
}

// SeesBranch reports whether the principal may know a branch of a database
// exists.
func (p Principal) SeesBranch(d meta.Database, branchID string) bool {
	if !p.in(d.WorkspaceID) || d.ID == "" || branchID == "" {
		return false
	}
	if p.wide() {
		return rank(p.Access) > 0
	}
	for _, g := range p.grants() {
		if g.DatabaseID == d.ID && (g.BranchID == "" || g.BranchID == branchID) {
			return true
		}
	}
	return false
}

// SeesBucket reports whether the principal may know a bucket exists. A
// workspace-wide principal sees every bucket of its workspace; below that a
// principal sees a bucket only when one of its grants names it.
func (p Principal) SeesBucket(b meta.Bucket) bool {
	if !p.in(b.WorkspaceID) || b.ID == "" {
		return false
	}
	if p.wide() {
		return rank(p.Access) > 0
	}
	for _, g := range p.grants() {
		if g.BucketID == b.ID {
			return true
		}
	}
	return false
}

// BucketAtLeast reports whether the principal has at least the given access
// to a bucket.
func (p Principal) BucketAtLeast(access string, b meta.Bucket) bool {
	if !p.in(b.WorkspaceID) || b.ID == "" || rank(access) == 0 {
		return false
	}
	if p.wide() {
		return rank(p.Access) >= rank(access)
	}
	for _, g := range p.grants() {
		if g.BucketID == b.ID && rank(g.Access) >= rank(access) {
			return true
		}
	}
	return false
}

// ChildExpiry caps a requested expiry (nil: never) at the principal's own,
// so a token can't outlive the token that created it.
func (p Principal) ChildExpiry(requested *time.Time) *time.Time {
	if p.ExpiresAt == nil {
		return requested
	}
	if requested == nil || requested.After(*p.ExpiresAt) {
		t := *p.ExpiresAt
		return &t
	}
	return requested
}

// coversGrant reports whether p, a principal below a whole workspace, is an
// admin of what g names, so handing g out isn't an escalation. An admin of a
// database covers its branches; an admin of one branch covers only that
// branch. The caller has already checked that g's token is in p's workspace.
func (p Principal) coversGrant(g Grant) bool {
	if (g.DatabaseID == "") == (g.BucketID == "") || rank(g.Access) == 0 {
		return false
	}
	for _, own := range p.grants() {
		if own.Access != AccessAdmin {
			continue
		}
		if g.BucketID != "" && own.BucketID == g.BucketID {
			return true
		}
		if g.DatabaseID != "" && own.DatabaseID == g.DatabaseID && (own.BranchID == "" || own.BranchID == g.BranchID) {
			return true
		}
	}
	return false
}

// CoversToken reports whether p's reach includes everything a token has, so
// p may mint, list or revoke such a token without escalating.
//
// Only an instance admin covers an instance token. Any other token must be
// in the workspace p acts in. There, an admin of the whole workspace covers
// every token. Below that, p must be an admin of every single grant, and
// never covers a workspace token: being an admin of one database a token
// reaches isn't enough to manage a token that reaches more.
func (p Principal) CoversToken(t meta.Token) bool {
	if t.Scope == ScopeInstance {
		return t.WorkspaceID == "" && p.InstanceAdmin()
	}
	if !p.in(t.WorkspaceID) {
		return false
	}
	if p.wide() {
		return p.Access == AccessAdmin
	}
	if t.Scope == ScopeWorkspace {
		return false
	}
	grants := TokenGrants(t)
	if len(grants) == 0 {
		return false
	}
	for _, g := range grants {
		if !p.coversGrant(g) {
			return false
		}
	}
	return true
}

// TokenGrants is what a stored token reaches below a whole workspace: one
// grant for a database or bucket token, the stored list for a grants token,
// nothing for a workspace or instance token.
func TokenGrants(t meta.Token) []Grant {
	switch t.Scope {
	case ScopeDatabase:
		return []Grant{{DatabaseID: t.DatabaseID, BranchID: t.BranchID, Access: t.Access}}
	case ScopeBucket:
		return []Grant{{BucketID: t.BucketID, Access: t.Access}}
	case ScopeGrants:
		return t.Grants
	}
	return nil
}
