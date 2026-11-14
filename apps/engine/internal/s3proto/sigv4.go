// Package s3proto holds the parts of the S3 REST protocol that don't touch
// the engine: AWS Signature Version 4 (header, presigned URL and chunked
// bodies), checksums, URI encoding and the XML documents (PLAN 16.2,
// ADR 0006).
package s3proto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Algorithm is the only signing algorithm accepted.
const Algorithm = "AWS4-HMAC-SHA256"

// Payload hash values with a special meaning.
const (
	UnsignedPayload        = "UNSIGNED-PAYLOAD"
	StreamingSigned        = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	StreamingSignedTrailer = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER"
	StreamingUnsignedTrail = "STREAMING-UNSIGNED-PAYLOAD-TRAILER"
	EmptySHA256            = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	TimeFormat             = "20060102T150405Z"
	dateFormat             = "20060102"
	// MaxSkew is how far a request's time may be from the server's.
	MaxSkew = 15 * time.Minute
	// MaxPresignExpiry is the protocol's cap on X-Amz-Expires.
	MaxPresignExpiry = 7 * 24 * time.Hour
)

// Error is a protocol error with its S3 code and HTTP status.
type Error struct {
	Code    string
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errf makes a protocol error.
func Errf(code string, status int, format string, args ...any) *Error {
	return &Error{Code: code, Status: status, Message: fmt.Sprintf(format, args...)}
}

// Scope is a credential scope: date/region/service/aws4_request.
type Scope struct {
	Date    string // YYYYMMDD
	Region  string
	Service string
}

func (s Scope) String() string { return s.Date + "/" + s.Region + "/" + s.Service + "/aws4_request" }

// Auth is a parsed signature from a header or a presigned URL.
type Auth struct {
	AccessKeyID   string
	Scope         Scope
	SignedHeaders []string // lowercase, sorted as sent
	Signature     string   // hex
	Time          time.Time
	Presigned     bool
	Expires       time.Duration // presigned only
	PayloadHash   string        // x-amz-content-sha256, or UNSIGNED-PAYLOAD for presigned URLs
}

func malformed(format string, args ...any) *Error {
	return Errf("AuthorizationHeaderMalformed", http.StatusBadRequest, format, args...)
}

func parseCredential(v string) (string, Scope, *Error) {
	parts := strings.Split(v, "/")
	if len(parts) != 5 || parts[0] == "" || parts[4] != "aws4_request" {
		return "", Scope{}, malformed("the Credential must be <access key id>/<date>/<region>/s3/aws4_request")
	}
	if parts[3] != "s3" {
		return "", Scope{}, malformed("the credential scope's service must be s3, not %q", parts[3])
	}
	if _, err := time.Parse(dateFormat, parts[1]); err != nil {
		return "", Scope{}, malformed("the credential scope's date %q isn't YYYYMMDD", parts[1])
	}
	return parts[0], Scope{Date: parts[1], Region: parts[2], Service: parts[3]}, nil
}

func parseSignedHeaders(v string) ([]string, *Error) {
	if v == "" {
		return nil, malformed("SignedHeaders is empty")
	}
	hs := strings.Split(v, ";")
	hasHost := false
	for _, h := range hs {
		if h == "" || h != strings.ToLower(h) {
			return nil, malformed("SignedHeaders must be lowercase names separated by ;")
		}
		hasHost = hasHost || h == "host"
	}
	if !hasHost {
		return nil, malformed("the host header must be signed")
	}
	return hs, nil
}

// ParseHeader reads an `Authorization: AWS4-HMAC-SHA256 ...` request.
func ParseHeader(r *http.Request) (*Auth, *Error) {
	h := r.Header.Get("Authorization")
	rest, ok := strings.CutPrefix(h, Algorithm+" ")
	if !ok {
		return nil, Errf("InvalidRequest", http.StatusBadRequest, "use AWS Signature Version 4 (%s)", Algorithm)
	}
	a := &Auth{}
	var cred, signed string
	for _, part := range strings.Split(rest, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, malformed("can't read %q", part)
		}
		switch k {
		case "Credential":
			cred = v
		case "SignedHeaders":
			signed = v
		case "Signature":
			a.Signature = v
		}
	}
	var err *Error
	if a.AccessKeyID, a.Scope, err = parseCredential(cred); err != nil {
		return nil, err
	}
	if a.SignedHeaders, err = parseSignedHeaders(signed); err != nil {
		return nil, err
	}
	if len(a.Signature) != 64 {
		return nil, malformed("the Signature must be 64 hex characters")
	}
	date := r.Header.Get("X-Amz-Date")
	if date == "" {
		return nil, Errf("AccessDenied", http.StatusForbidden, "AWS authentication requires a valid X-Amz-Date header")
	}
	t, perr := time.Parse(TimeFormat, date)
	if perr != nil {
		return nil, Errf("AccessDenied", http.StatusForbidden, "X-Amz-Date %q isn't YYYYMMDDTHHMMSSZ", date)
	}
	a.Time = t
	if t.Format(dateFormat) != a.Scope.Date {
		return nil, malformed("the credential scope's date doesn't match X-Amz-Date")
	}
	a.PayloadHash = r.Header.Get("X-Amz-Content-Sha256")
	if a.PayloadHash == "" {
		return nil, Errf("InvalidRequest", http.StatusBadRequest, "missing required header for this request: x-amz-content-sha256")
	}
	if !validPayloadHash(a.PayloadHash) {
		return nil, Errf("InvalidArgument", http.StatusBadRequest, "x-amz-content-sha256 must be a SHA-256 hex digest or a known streaming mode")
	}
	return a, nil
}

func validPayloadHash(v string) bool {
	switch v {
	case UnsignedPayload, StreamingSigned, StreamingSignedTrailer, StreamingUnsignedTrail:
		return true
	}
	if len(v) != 64 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil && strings.ToLower(v) == v
}

// ParsePresigned reads the X-Amz-* query parameters of a presigned URL.
func ParsePresigned(q url.Values) (*Auth, *Error) {
	if q.Get("X-Amz-Algorithm") != Algorithm {
		return nil, Errf("AuthorizationQueryParametersError", http.StatusBadRequest, "X-Amz-Algorithm must be %s", Algorithm)
	}
	a := &Auth{Presigned: true, Signature: q.Get("X-Amz-Signature")}
	var err *Error
	if a.AccessKeyID, a.Scope, err = parseCredential(q.Get("X-Amz-Credential")); err != nil {
		return nil, Errf("AuthorizationQueryParametersError", http.StatusBadRequest, "%s", err.Message)
	}
	if a.SignedHeaders, err = parseSignedHeaders(q.Get("X-Amz-SignedHeaders")); err != nil {
		return nil, Errf("AuthorizationQueryParametersError", http.StatusBadRequest, "%s", err.Message)
	}
	if len(a.Signature) != 64 {
		return nil, Errf("AuthorizationQueryParametersError", http.StatusBadRequest, "X-Amz-Signature must be 64 hex characters")
	}
	t, perr := time.Parse(TimeFormat, q.Get("X-Amz-Date"))
	if perr != nil {
		return nil, Errf("AuthorizationQueryParametersError", http.StatusBadRequest, "X-Amz-Date must be YYYYMMDDTHHMMSSZ")
	}
	if t.Format(dateFormat) != a.Scope.Date {
		return nil, Errf("AuthorizationQueryParametersError", http.StatusBadRequest, "the credential scope's date doesn't match X-Amz-Date")
	}
	a.Time = t
	secs, perr := strconv.Atoi(q.Get("X-Amz-Expires"))
	if perr != nil || secs < 1 || time.Duration(secs)*time.Second > MaxPresignExpiry {
		return nil, Errf("AuthorizationQueryParametersError", http.StatusBadRequest, "X-Amz-Expires must be 1 to 604800 seconds")
	}
	a.Expires = time.Duration(secs) * time.Second
	a.PayloadHash = UnsignedPayload
	if v := q.Get("X-Amz-Content-Sha256"); v != "" {
		if !validPayloadHash(v) || strings.HasPrefix(v, "STREAMING-") {
			return nil, Errf("AuthorizationQueryParametersError", http.StatusBadRequest, "X-Amz-Content-Sha256 isn't valid")
		}
		a.PayloadHash = v
	}
	return a, nil
}

// CheckTime refuses a request outside the allowed clock skew, or a
// presigned URL that has expired or isn't valid yet, or that lives longer
// than maxExpiry.
func (a *Auth) CheckTime(now time.Time, maxExpiry time.Duration) *Error {
	if a.Presigned {
		if a.Expires > maxExpiry {
			return Errf("AuthorizationQueryParametersError", http.StatusBadRequest, "X-Amz-Expires can be at most %d seconds on this server", int(maxExpiry.Seconds()))
		}
		if a.Time.After(now.Add(MaxSkew)) {
			return Errf("AccessDenied", http.StatusForbidden, "the request's time is in the future")
		}
		if !now.Before(a.Time.Add(a.Expires)) {
			return Errf("AccessDenied", http.StatusForbidden, "request has expired")
		}
		return nil
	}
	if d := now.Sub(a.Time); d > MaxSkew || d < -MaxSkew {
		return Errf("RequestTimeTooSkewed", http.StatusForbidden, "the difference between the request time and the server's time is too large")
	}
	return nil
}

// URIEncode encodes s the way SigV4 wants: every byte but A-Z a-z 0-9 - . _ ~
// as %XX with capital hex, and "/" too unless keepSlash.
func URIEncode(s string, keepSlash bool) string {
	const hexd = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexd[c>>4])
			b.WriteByte(hexd[c&15])
		}
	}
	return b.String()
}

// CanonicalQuery sorts and encodes query parameters, leaving out
// X-Amz-Signature (for presigned URLs).
func CanonicalQuery(q url.Values) string {
	type kv struct{ k, v string }
	var pairs []kv
	for k, vs := range q {
		if k == "X-Amz-Signature" {
			continue
		}
		for _, v := range vs {
			pairs = append(pairs, kv{URIEncode(k, false), URIEncode(v, false)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, "&")
}

func canonicalHeaderValue(vs []string) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = strings.Join(strings.Fields(v), " ")
	}
	return strings.Join(out, ",")
}

// CanonicalRequest builds the canonical request. path is the decoded path
// (it is encoded here); host is the request's Host.
func CanonicalRequest(method, path string, query url.Values, header http.Header, host string, signed []string, payloadHash string) string {
	var b strings.Builder
	b.WriteString(method)
	b.WriteByte('\n')
	b.WriteString(URIEncode(path, true))
	b.WriteByte('\n')
	b.WriteString(CanonicalQuery(query))
	b.WriteByte('\n')
	for _, h := range signed {
		b.WriteString(h)
		b.WriteByte(':')
		if h == "host" {
			b.WriteString(strings.TrimSpace(host))
		} else {
			b.WriteString(canonicalHeaderValue(header.Values(h)))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(strings.Join(signed, ";"))
	b.WriteByte('\n')
	b.WriteString(payloadHash)
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// SigningKey derives the key for a secret and scope.
func SigningKey(secret string, s Scope) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), s.Date)
	k = hmacSHA256(k, s.Region)
	k = hmacSHA256(k, s.Service)
	return hmacSHA256(k, "aws4_request")
}

// StringToSign wraps a canonical request.
func StringToSign(t time.Time, s Scope, canonical string) string {
	return Algorithm + "\n" + t.UTC().Format(TimeFormat) + "\n" + s.String() + "\n" + sha256Hex(canonical)
}

// Sign returns the hex signature of a string to sign.
func Sign(key []byte, stringToSign string) string {
	return hex.EncodeToString(hmacSHA256(key, stringToSign))
}

// SignatureMismatch is the error for a wrong signature.
var SignatureMismatch = &Error{Code: "SignatureDoesNotMatch", Status: http.StatusForbidden,
	Message: "the request signature we calculated does not match the signature you provided; check your key and signing method"}

// Verify checks a request's signature with the secret. path is the decoded
// request path. It returns the signing key and seed signature, which a
// chunked body continues from.
func (a *Auth) Verify(r *http.Request, path, secret string) ([]byte, *Error) {
	q := r.URL.Query()
	canonical := CanonicalRequest(r.Method, path, q, r.Header, r.Host, a.SignedHeaders, a.PayloadHash)
	key := SigningKey(secret, a.Scope)
	want := Sign(key, StringToSign(a.Time, a.Scope, canonical))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(a.Signature))) {
		return nil, SignatureMismatch
	}
	return key, nil
}

// UnsignedAmzHeaders lists x-amz-* headers in the request that aren't
// signed. Header auth must sign them all; a presigned URL may leave out
// only those that add checks.
func (a *Auth) UnsignedAmzHeaders(h http.Header) []string {
	signed := map[string]bool{}
	for _, s := range a.SignedHeaders {
		signed[s] = true
	}
	var out []string
	for name := range h {
		l := strings.ToLower(name)
		if !strings.HasPrefix(l, "x-amz-") || signed[l] {
			continue
		}
		if a.Presigned && (l == "x-amz-content-sha256" || l == "x-amz-date" || strings.HasPrefix(l, "x-amz-checksum-") ||
			l == "x-amz-sdk-checksum-algorithm" || l == "x-amz-trailer" || l == "x-amz-decoded-content-length") {
			continue
		}
		if !a.Presigned && (l == "x-amz-trailer" || l == "x-amz-decoded-content-length") {
			// Sent by streaming uploads and covered by the chunk
			// signatures or checksums, as the SDKs do it.
			continue
		}
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// PresignInput describes a URL to presign.
type PresignInput struct {
	Method      string
	Scheme      string // http or https
	Host        string
	Path        string // decoded, e.g. /v1/s3/bucket/key
	AccessKeyID string
	Secret      string
	Region      string
	Time        time.Time
	Expires     time.Duration
}

// Presign returns a presigned URL that signs only the host header.
func Presign(in PresignInput) string {
	scope := Scope{Date: in.Time.UTC().Format(dateFormat), Region: in.Region, Service: "s3"}
	q := url.Values{}
	q.Set("X-Amz-Algorithm", Algorithm)
	q.Set("X-Amz-Credential", in.AccessKeyID+"/"+scope.String())
	q.Set("X-Amz-Date", in.Time.UTC().Format(TimeFormat))
	q.Set("X-Amz-Expires", strconv.Itoa(int(in.Expires/time.Second)))
	q.Set("X-Amz-SignedHeaders", "host")
	canonical := CanonicalRequest(in.Method, in.Path, q, http.Header{}, in.Host, []string{"host"}, UnsignedPayload)
	sig := Sign(SigningKey(in.Secret, scope), StringToSign(in.Time, scope, canonical))
	return in.Scheme + "://" + in.Host + URIEncode(in.Path, true) + "?" + CanonicalQuery(q) + "&X-Amz-Signature=" + sig
}

// ErrNoBody is returned by readers that expect more bytes.
var ErrNoBody = errors.New("s3proto: body ended early")
