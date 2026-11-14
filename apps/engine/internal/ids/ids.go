// Package ids makes opaque ids and checks user-chosen names.
//
// Ids look like "db_k3x9q2m7v4p8w1z6c5n0" and name files on disk, so they
// only ever contain [a-z0-9_]. Names match [a-z0-9][a-z0-9-]{0,62} and never
// contain an underscore, so a path segment that holds either one is never
// ambiguous.
package ids

import (
	"crypto/rand"
	"regexp"
)

// Prefixes for each kind of id.
const (
	Database = "db"
	Branch   = "br"
	Snapshot = "sn"
	Token    = "tok"
	Bucket   = "bk"
	Blob     = "ob"
	Upload   = "up"
)

const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// New returns prefix_ followed by 20 random characters (about 103 bits).
func New(prefix string) string {
	const n = 20
	buf := make([]byte, n)
	out := make([]byte, n)
	for i := 0; i < n; {
		if _, err := rand.Read(buf); err != nil {
			panic(err) // crypto/rand never fails on supported platforms
		}
		for _, b := range buf {
			// Reject bytes past the largest multiple of 36 to stay uniform.
			if b >= 252 {
				continue
			}
			out[i] = alphabet[int(b)%len(alphabet)]
			i++
			if i == n {
				break
			}
		}
	}
	return prefix + "_" + string(out)
}

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	idRe   = regexp.MustCompile(`^[a-z]{2,3}_[a-z0-9]{20}$`)
)

// ValidName reports whether s is a valid database or branch name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

var bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// ValidBucketName reports whether s is a valid bucket name: 3 to 63
// lowercase letters, digits and hyphens, starting and ending with a letter
// or digit, so every S3 client accepts it.
func ValidBucketName(s string) bool { return bucketRe.MatchString(s) }

// IsID reports whether s has the shape of an id with the given prefix.
func IsID(s, prefix string) bool {
	return idRe.MatchString(s) && len(s) > len(prefix) && s[:len(prefix)+1] == prefix+"_"
}
