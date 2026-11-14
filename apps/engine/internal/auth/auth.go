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

	AccessRead  = "read"
	AccessWrite = "write"
	AccessAdmin = "admin"
)

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
type Principal struct {
	TokenID    string
	Name       string
	Scope      string
	DatabaseID string
	BranchID   string
	BucketID   string
	Access     string
	Service    bool
	ExpiresAt  *time.Time // nil: never
	Actor      string     // dashboard user, only honored for the service token
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
	return Principal{
		TokenID: t.ID, Name: t.Name, Scope: t.Scope, DatabaseID: t.DatabaseID,
		BranchID: t.BranchID, BucketID: t.BucketID, Access: t.Access, Service: t.Service, ExpiresAt: t.ExpiresAt,
	}, nil
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

// InstanceAdmin reports whether the principal may do anything.
func (p Principal) InstanceAdmin() bool {
	return p.Scope == ScopeInstance && p.Access == AccessAdmin
}

// AtLeast reports whether the principal has at least the given access to a
// database, and to a branch of it when branchID isn't empty. An empty
// branchID asks about the database as a whole, which a branch-limited token
// never has.
func (p Principal) AtLeast(access, dbID, branchID string) bool {
	if rank(p.Access) < rank(access) {
		return false
	}
	if p.Scope == ScopeInstance {
		return true
	}
	if p.Scope != ScopeDatabase || dbID == "" || p.DatabaseID != dbID {
		return false
	}
	if p.BranchID == "" {
		return true
	}
	return branchID != "" && p.BranchID == branchID
}

// SeesDatabase reports whether the principal may know a database exists.
func (p Principal) SeesDatabase(dbID string) bool {
	return p.Scope == ScopeInstance || (p.Scope == ScopeDatabase && dbID != "" && p.DatabaseID == dbID)
}

// SeesBucket reports whether the principal may know a bucket exists.
// Instance tokens see every bucket; database tokens see none.
func (p Principal) SeesBucket(bucketID string) bool {
	return p.Scope == ScopeInstance || (p.Scope == ScopeBucket && bucketID != "" && p.BucketID == bucketID)
}

// BucketAtLeast reports whether the principal has at least the given access
// to a bucket.
func (p Principal) BucketAtLeast(access, bucketID string) bool {
	return rank(p.Access) >= rank(access) && p.SeesBucket(bucketID)
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

// Covers reports whether p's grant includes everything a grant of the given
// scope has, so p may mint or manage such a token without escalating. Only
// admins cover anything; a database admin covers tokens of its database (and
// its branch, when limited), a bucket admin tokens of its bucket.
func (p Principal) Covers(scope, dbID, branchID, bucketID, access string) bool {
	if rank(p.Access) < rank(AccessAdmin) || rank(p.Access) < rank(access) {
		return false
	}
	switch p.Scope {
	case ScopeInstance:
		return true
	case ScopeDatabase:
		if scope != ScopeDatabase || dbID == "" || dbID != p.DatabaseID {
			return false
		}
		return p.BranchID == "" || p.BranchID == branchID
	case ScopeBucket:
		return scope == ScopeBucket && bucketID != "" && bucketID == p.BucketID
	}
	return false
}
