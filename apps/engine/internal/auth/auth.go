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
	ScopeInstance = "instance"
	ScopeDatabase = "database"
	ScopeBucket   = "bucket"
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
// through a method below, so one place holds the rule that a principal only
// reaches what one of its grants names.
type Principal struct {
	TokenID    string
	Name       string
	Scope      string
	DatabaseID string
	BranchID   string
	BucketID   string
	// Access is the level of an instance, database or bucket principal. It
	// is empty for the scope grants, where each grant has its own.
	Access string
	// Grants is what a principal with the scope grants reaches.
	Grants    []Grant
	Service   bool
	ExpiresAt *time.Time // nil: never
	Actor     string     // dashboard user, only honored for the service token
}

// FromToken builds a principal from a live token record. It fails for a
// revoked or expired token.
func FromToken(t meta.Token, now time.Time) (Principal, error) {
	if t.RevokedAt != nil {
		return Principal{}, errors.New("auth: token revoked")
	}
	if t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) {
		return Principal{}, errors.New("auth: token expired")
	}
	p := Principal{
		TokenID: t.ID, Name: t.Name, Scope: t.Scope, DatabaseID: t.DatabaseID,
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

// grants lists what a principal below the instance reaches. A database or
// bucket principal has exactly one grant.
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

// InstanceAdmin reports whether the principal may do anything.
func (p Principal) InstanceAdmin() bool {
	return p.Scope == ScopeInstance && p.Access == AccessAdmin
}

// HasAdmin reports whether the principal is an admin of anything, which is
// what managing tokens takes.
func (p Principal) HasAdmin() bool {
	if p.Scope == ScopeInstance {
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
func (p Principal) AtLeast(access, dbID, branchID string) bool {
	if p.Scope == ScopeInstance {
		return rank(p.Access) >= rank(access)
	}
	if dbID == "" {
		return false
	}
	for _, g := range p.grants() {
		if g.DatabaseID != dbID || rank(g.Access) < rank(access) {
			continue
		}
		if g.BranchID == "" || (branchID != "" && g.BranchID == branchID) {
			return true
		}
	}
	return false
}

// SeesDatabase reports whether the principal may know a database exists.
func (p Principal) SeesDatabase(dbID string) bool {
	if p.Scope == ScopeInstance {
		return true
	}
	if dbID == "" {
		return false
	}
	for _, g := range p.grants() {
		if g.DatabaseID == dbID {
			return true
		}
	}
	return false
}

// WholeDatabase reports whether the principal reaches a database as a
// whole, not only some of its branches.
func (p Principal) WholeDatabase(dbID string) bool {
	if p.Scope == ScopeInstance {
		return true
	}
	if dbID == "" {
		return false
	}
	for _, g := range p.grants() {
		if g.DatabaseID == dbID && g.BranchID == "" {
			return true
		}
	}
	return false
}

// SeesBranch reports whether the principal may know a branch of a database
// exists.
func (p Principal) SeesBranch(dbID, branchID string) bool {
	if p.Scope == ScopeInstance {
		return true
	}
	if dbID == "" || branchID == "" {
		return false
	}
	for _, g := range p.grants() {
		if g.DatabaseID == dbID && (g.BranchID == "" || g.BranchID == branchID) {
			return true
		}
	}
	return false
}

// SeesBucket reports whether the principal may know a bucket exists.
// Instance tokens see every bucket; a token sees a bucket below that only
// when one of its grants names it.
func (p Principal) SeesBucket(bucketID string) bool {
	if p.Scope == ScopeInstance {
		return true
	}
	if bucketID == "" {
		return false
	}
	for _, g := range p.grants() {
		if g.BucketID == bucketID {
			return true
		}
	}
	return false
}

// BucketAtLeast reports whether the principal has at least the given access
// to a bucket.
func (p Principal) BucketAtLeast(access, bucketID string) bool {
	if p.Scope == ScopeInstance {
		return rank(p.Access) >= rank(access)
	}
	if bucketID == "" {
		return false
	}
	for _, g := range p.grants() {
		if g.BucketID == bucketID && rank(g.Access) >= rank(access) {
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

// coversGrant reports whether p is an admin of what g names, so handing g
// out isn't an escalation. An admin of a database covers its branches; an
// admin of one branch covers only that branch.
func (p Principal) coversGrant(g Grant) bool {
	if p.Scope == ScopeInstance {
		return p.Access == AccessAdmin
	}
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

// CoversToken reports whether p's grants include everything a token of the
// given scope and grants has, so p may mint or manage such a token without
// escalating. Only an instance admin covers an instance token. Below that,
// p must be an admin of every single grant: being an admin of one database
// a token reaches isn't enough to manage a token that reaches more.
func (p Principal) CoversToken(scope string, grants []Grant) bool {
	if scope == ScopeInstance {
		return p.InstanceAdmin()
	}
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

// Covers is CoversToken for a token with one scope: an instance token, a
// database token (optionally limited to a branch) or a bucket token.
func (p Principal) Covers(scope, dbID, branchID, bucketID, access string) bool {
	switch scope {
	case ScopeInstance:
		return p.InstanceAdmin()
	case ScopeDatabase:
		return p.CoversToken(scope, []Grant{{DatabaseID: dbID, BranchID: branchID, Access: access}})
	case ScopeBucket:
		return p.CoversToken(scope, []Grant{{BucketID: bucketID, Access: access}})
	}
	return false
}

// TokenGrants is what a stored token reaches below the instance, in the
// shape CoversToken takes: one grant for a database or bucket token, the
// stored list for a grants token.
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
