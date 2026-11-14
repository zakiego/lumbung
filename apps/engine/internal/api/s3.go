package api

// The S3-compatible API (PLAN 16.2, ADR 0006): path-style at /v1/s3/,
// AWS Signature Version 4 or a bearer token, public reads per bucket, and
// S3 XML errors. Requests are matched on the raw path before the router, so
// keys with //, . or .. segments arrive unchanged.

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/s3proto"
)

// S3Prefix is where the S3-compatible API lives.
const S3Prefix = "/v1/s3"

func isS3Path(p string) bool { return p == S3Prefix || strings.HasPrefix(p, S3Prefix+"/") }

// Caps on XML request bodies.
const (
	s3SmallBody    = 64 << 10 // CreateBucket configuration
	s3DeleteBody   = 2 << 20  // DeleteObjects, up to 1000 keys
	s3CompleteBody = 4 << 20  // CompleteMultipartUpload, up to 10000 parts
	s3MaxKeys      = 1000
)

// s3Req is one S3 request.
type s3Req struct {
	w      http.ResponseWriter
	r      *http.Request
	id     string
	path   string // decoded path, for the signature
	bucket string // bucket name from the path, "" for the service
	key    string // "" for a bucket request
	q      url.Values

	p auth.Principal
	// ws is the id of the workspace the request acts in (PLAN 17.3): the
	// token's, or the default workspace for a request with no workspace of
	// its own. Every bucket lookup takes it.
	ws string
	// sel is the workspace a qualified URL names, "" for a short URL.
	sel string
	// base is the path the S3 API is served under for this request:
	// /v1/s3, or /v1/workspaces/<ws>/s3 for a qualified URL.
	base   string
	anon   bool
	bearer bool
	sig    *s3proto.Auth
	sigKey []byte

	// bodyErr is the protocol error a verifying reader hit, reported in
	// place of the engine's generic one.
	bodyErr *s3proto.Error
}

// serveS3 handles every request under /v1/s3, and under
// /v1/workspaces/<sel>/s3 when sel names a workspace. rest is the escaped
// path in its short form, /v1/s3/...; the signature is still checked
// against the path the client really sent.
func (s *Server) serveS3(w http.ResponseWriter, r *http.Request, sel, rest string) {
	id := r.Header.Get("X-Request-Id")
	if !requestIDRe.MatchString(id) {
		id = newRequestID()
	}
	w.Header().Set("X-Request-Id", id)
	w.Header().Set("x-amz-request-id", id)
	req := &s3Req{w: w, r: r, id: id, q: r.URL.Query(), sel: sel, base: S3Prefix}
	if sel != "" {
		req.base = workspacesPrefix + sel + "/s3"
	}
	path, err := url.PathUnescape(r.URL.EscapedPath())
	short, err2 := url.PathUnescape(rest)
	if err != nil || err2 != nil {
		s.s3Fail(req, s3proto.Errf("InvalidURI", http.StatusBadRequest, "couldn't parse the specified URI"))
		return
	}
	req.path = path
	short = strings.TrimPrefix(short, S3Prefix)
	short = strings.TrimPrefix(short, "/")
	req.bucket, req.key, _ = strings.Cut(short, "/")

	if r.Method == http.MethodOptions {
		s.s3Preflight(req)
		return
	}
	if e := s.s3Authenticate(req); e != nil {
		s.s3Fail(req, e)
		return
	}
	if e := s.s3Workspace(req); e != nil {
		s.s3Fail(req, e)
		return
	}
	s.s3Dispatch(req)
}

// ---- authentication

// s3Workspace resolves the workspace the request acts in, after
// authentication. A token naming a workspace that isn't its own, or one
// that doesn't exist, gets the same answer for both: NoSuchBucket when the
// request names a bucket, AccessDenied when it doesn't. An anonymous
// request to a missing workspace gets AccessDenied, as for a missing or
// private bucket.
func (s *Server) s3Workspace(req *s3Req) *s3proto.Error {
	ctx := req.r.Context()
	fail := func(err error) *s3proto.Error {
		switch apierr.As(err).Code {
		case apierr.WorkspaceNotFound:
			if req.anon || req.bucket == "" {
				return accessDenied()
			}
			return noSuchBucket(req.bucket)
		case apierr.Unauthorized:
			return invalidKeyID("the access key id you provided does not exist")
		}
		s.log.Error("s3 workspace", "err", err, "request_id", req.id)
		return s3proto.Errf("InternalError", http.StatusInternalServerError, "internal error")
	}
	if req.anon {
		ws, err := s.requestWorkspace(ctx, false, "", req.sel)
		if err != nil {
			return fail(err)
		}
		req.ws = ws.ID
		return nil
	}
	p, err := s.act(ctx, req.p, req.sel)
	if err != nil {
		return fail(err)
	}
	if req.bearer {
		// The dashboard reaches this API with the service token as a bearer
		// token, for a user whose level it names.
		if p, err = narrowed(p, req.r.Header.Get(actorAccessHeader), req.sel != ""); err != nil {
			return s3proto.Errf("InvalidRequest", http.StatusBadRequest, "%s", apierr.As(err).Message)
		}
	}
	req.p, req.ws = p, p.WorkspaceID
	return nil
}

func (s *Server) s3AuthFailed(req *s3Req, e *s3proto.Error) *s3proto.Error {
	if ok, wait := s.failures.Allow(s.clientIPFromRequest(req.r)); !ok {
		req.w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		return s3proto.Errf("SlowDown", http.StatusServiceUnavailable, "too many failed authentications from this address")
	}
	return e
}

// s3Authenticate resolves who the request acts as: a SigV4 signature (header
// or presigned URL), a bearer token, or nobody (public reads only).
func (s *Server) s3Authenticate(req *s3Req) *s3proto.Error {
	r := req.r
	h := r.Header.Get("Authorization")
	switch {
	case strings.HasPrefix(h, s3proto.Algorithm+" "):
		a, e := s3proto.ParseHeader(r)
		if e != nil {
			return s.s3AuthFailed(req, e)
		}
		return s.s3Verify(req, a)
	case h != "":
		if tok, ok := bearer(h); ok && strings.HasPrefix(tok, "lmb_") {
			p, err := s.authenticate(r.Context(), h)
			if err != nil {
				ae := apierr.As(err)
				if ae.Code != apierr.Unauthorized {
					s.log.Error("authenticate", "err", err, "request_id", req.id)
					return s3proto.Errf("InternalError", http.StatusInternalServerError, "internal error")
				}
				return s.s3AuthFailed(req, s3proto.Errf("InvalidToken", http.StatusForbidden, "%s", ae.Message))
			}
			if a := r.Header.Get("X-Lumbung-Actor"); a != "" && p.Service && actorRe.MatchString(a) {
				p.Actor = a
			}
			req.p, req.bearer = p, true
			return s.s3RateLimit(req)
		}
		return s.s3AuthFailed(req, s3proto.Errf("InvalidRequest", http.StatusBadRequest, "use AWS Signature Version 4 (%s) or a Lumbung bearer token", s3proto.Algorithm))
	case req.q.Has("X-Amz-Algorithm") || req.q.Has("X-Amz-Credential") || req.q.Has("X-Amz-Signature"):
		a, e := s3proto.ParsePresigned(req.q)
		if e != nil {
			return s.s3AuthFailed(req, e)
		}
		return s.s3Verify(req, a)
	}
	req.anon = true
	if ok, wait := s.publicLimit.Allow(s.clientIPFromRequest(r)); !ok {
		req.w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		return s3proto.Errf("SlowDown", http.StatusServiceUnavailable, "too many requests without a token from this address")
	}
	return nil
}

func (s *Server) s3RateLimit(req *s3Req) *s3proto.Error {
	if ok, wait := s.perToken.Allow(rateKey(req.p)); !ok {
		req.w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		return s3proto.Errf("SlowDown", http.StatusServiceUnavailable, "too many requests for this token")
	}
	return nil
}

func invalidKeyID(msg string) *s3proto.Error {
	return s3proto.Errf("InvalidAccessKeyId", http.StatusForbidden, "%s", msg)
}

// s3Verify checks a SigV4 signature against the token the access key
// belongs to. The token is read on every request, so revoking it ends the
// key and every URL it signed at once.
func (s *Server) s3Verify(req *s3Req, a *s3proto.Auth) *s3proto.Error {
	ctx := req.r.Context()
	tokID, ok := auth.TokenIDOfAccessKey(a.AccessKeyID)
	if !ok {
		return s.s3AuthFailed(req, invalidKeyID("the access key id you provided does not exist"))
	}
	rec, err := s.store.GetToken(ctx, tokID)
	if errors.Is(err, meta.ErrNoToken) || (err == nil && len(rec.AccessSecret) == 0) {
		return s.s3AuthFailed(req, invalidKeyID("the access key id you provided does not exist"))
	}
	if err != nil {
		s.log.Error("s3 token", "err", err, "request_id", req.id)
		return s3proto.Errf("InternalError", http.StatusInternalServerError, "internal error")
	}
	p, err := s.principalOf(ctx, rec)
	if err != nil {
		if apierr.As(err).Code != apierr.Unauthorized {
			s.log.Error("s3 token", "err", err, "request_id", req.id)
			return s3proto.Errf("InternalError", http.StatusInternalServerError, "internal error")
		}
		// The same answer as an unknown key: the id alone mustn't tell
		// whether its token, or its workspace, was ever live.
		return s.s3AuthFailed(req, invalidKeyID("the access key id you provided does not exist"))
	}
	secret, err := s.eng.Sealer().Open(rec.ID, rec.AccessSecret)
	if err != nil {
		s.log.Error("s3 access key secret", "err", err, "token", rec.ID, "request_id", req.id)
		return s3proto.Errf("InternalError", http.StatusInternalServerError, "internal error")
	}
	key, e := a.Verify(req.r, req.path, secret)
	if e != nil {
		return s.s3AuthFailed(req, e)
	}
	if e := a.CheckTime(s.now(), s.eng.Config().PresignMaxExpiry); e != nil {
		return s.s3AuthFailed(req, e)
	}
	if extra := a.UnsignedAmzHeaders(req.r.Header); len(extra) > 0 {
		return s.s3AuthFailed(req, s3proto.Errf("AccessDenied", http.StatusForbidden, "there were headers present in the request which were not signed: %s", strings.Join(extra, ", ")))
	}
	s.touch(ctx, rec.ID)
	req.p, req.sig, req.sigKey = p, a, key
	return s.s3RateLimit(req)
}

// ---- errors

// s3FromAPI maps an engine error to an S3 error.
func (s *Server) s3FromAPI(req *s3Req, err error) *s3proto.Error {
	var pe *s3proto.Error
	if errors.As(err, &pe) {
		return pe
	}
	if req.bodyErr != nil {
		return req.bodyErr
	}
	ae := apierr.As(err)
	m := func(code string, status int) *s3proto.Error {
		return &s3proto.Error{Code: code, Status: status, Message: ae.Message}
	}
	switch ae.Code {
	case apierr.BucketNotFound:
		return m("NoSuchBucket", http.StatusNotFound)
	case apierr.ObjectNotFound:
		return m("NoSuchKey", http.StatusNotFound)
	case apierr.BucketNotEmpty:
		return m("BucketNotEmpty", http.StatusConflict)
	case apierr.NameTaken:
		return m("BucketAlreadyOwnedByYou", http.StatusConflict)
	case apierr.InvalidName:
		return m("InvalidBucketName", http.StatusBadRequest)
	case apierr.InvalidKey:
		return m("KeyTooLongError", http.StatusBadRequest)
	case apierr.ObjectTooLarge:
		return m("EntityTooLarge", http.StatusBadRequest)
	case apierr.BucketSizeLimit, apierr.ObjectQuotaExceeded, apierr.WorkspaceQuotaExceeded:
		return m("QuotaExceeded", http.StatusForbidden)
	case apierr.DiskFull:
		return m("InsufficientStorage", http.StatusInsufficientStorage)
	case apierr.UploadNotFound:
		return m("NoSuchUpload", http.StatusNotFound)
	case apierr.InvalidPart:
		if strings.Contains(ae.Message, "ascending") {
			return m("InvalidPartOrder", http.StatusBadRequest)
		}
		return m("InvalidPart", http.StatusBadRequest)
	case apierr.PartTooSmall:
		return m("EntityTooSmall", http.StatusBadRequest)
	case apierr.BadDigest:
		return m("BadDigest", http.StatusBadRequest)
	case apierr.IncompleteBody:
		return m("IncompleteBody", http.StatusBadRequest)
	case apierr.LengthRequired:
		return m("MissingContentLength", http.StatusLengthRequired)
	case apierr.PreconditionFailed:
		return m("PreconditionFailed", http.StatusPreconditionFailed)
	case apierr.InvalidRange:
		return m("InvalidRange", http.StatusRequestedRangeNotSatisfiable)
	case apierr.LimitReached:
		return m("TooManyBuckets", http.StatusBadRequest)
	case apierr.Forbidden:
		return m("AccessDenied", http.StatusForbidden)
	case apierr.RateLimited:
		return m("SlowDown", http.StatusServiceUnavailable)
	case apierr.BadRequest:
		return m("InvalidArgument", http.StatusBadRequest)
	case apierr.Unavailable, apierr.Busy:
		return m("ServiceUnavailable", http.StatusServiceUnavailable)
	}
	s.log.Error("s3", "err", err, "method", req.r.Method, "request_id", req.id)
	return &s3proto.Error{Code: "InternalError", Status: http.StatusInternalServerError, Message: "we encountered an internal error; please try again"}
}

func (s *Server) s3Fail(req *s3Req, e *s3proto.Error) {
	h := req.w.Header()
	h.Set("Content-Type", "application/xml")
	h.Del("Content-Length")
	h.Del("ETag")
	h.Del("Last-Modified")
	req.w.WriteHeader(e.Status)
	if req.r.Method == http.MethodHead {
		return
	}
	doc := s3proto.ErrorDoc{Code: e.Code, Message: e.Message, RequestID: req.id, BucketName: req.bucket, Key: req.key,
		Resource: req.path}
	s3WriteXMLBody(req.w, doc)
}

func s3WriteXMLBody(w io.Writer, v any) {
	_, _ = io.WriteString(w, xml.Header)
	_ = xml.NewEncoder(w).Encode(v)
}

func (s *Server) s3XML(req *s3Req, status int, v any) {
	req.w.Header().Set("Content-Type", "application/xml")
	req.w.WriteHeader(status)
	s3WriteXMLBody(req.w, v)
}

func (s *Server) s3Err(req *s3Req, err error) { s.s3Fail(req, s.s3FromAPI(req, err)) }

// ---- dispatch

// unsupported query parameters that select a sub-resource this server
// doesn't implement.
var s3Unsupported = []string{"acl", "tagging", "policy", "policyStatus", "cors", "lifecycle", "website", "logging",
	"notification", "replication", "encryption", "object-lock", "retention", "legal-hold", "versions", "torrent",
	"restore", "select", "accelerate", "analytics", "inventory", "metrics", "ownershipControls",
	"intelligent-tiering", "publicAccessBlock", "requestPayment", "attributes"}

func notImplemented(what string) *s3proto.Error {
	return s3proto.Errf("NotImplemented", http.StatusNotImplemented, "%s isn't supported by this server", what)
}

func (s *Server) s3Dispatch(req *s3Req) {
	for _, k := range s3Unsupported {
		if req.q.Has(k) {
			s.s3Fail(req, notImplemented("the ?"+k+" sub-resource"))
			return
		}
	}
	m := req.r.Method
	q := req.q
	switch {
	case req.bucket == "":
		if m == http.MethodGet {
			s.s3ListBuckets(req)
			return
		}
	case req.key == "":
		switch {
		case m == http.MethodPut && hasSubresource(q):
			// PutBucketVersioning, PutBucketTagging and friends must never
			// fall through to CreateBucket.
			s.s3Fail(req, notImplemented("changing bucket settings over the S3 API"))
		case m == http.MethodPut:
			s.s3CreateBucket(req)
		case m == http.MethodDelete:
			s.s3DeleteBucket(req)
		case m == http.MethodHead:
			s.s3HeadBucket(req)
		case m == http.MethodGet && q.Has("location"):
			s.s3Location(req)
		case m == http.MethodGet && q.Has("versioning"):
			s.s3Versioning(req)
		case m == http.MethodGet && q.Has("uploads"):
			s.s3ListUploads(req)
		case m == http.MethodGet && q.Get("list-type") == "2":
			s.s3ListObjects(req, true)
		case m == http.MethodGet:
			s.s3ListObjects(req, false)
		case m == http.MethodPost && q.Has("delete"):
			s.s3DeleteObjects(req)
		default:
			s.s3Fail(req, s3proto.Errf("MethodNotAllowed", http.StatusMethodNotAllowed, "the specified method is not allowed against this resource"))
		}
		return
	default:
		switch {
		case m == http.MethodPut && q.Has("uploadId"):
			if req.r.Header.Get("X-Amz-Copy-Source") != "" {
				s.s3Fail(req, notImplemented("UploadPartCopy"))
				return
			}
			s.s3UploadPart(req)
		case m == http.MethodPut && req.r.Header.Get("X-Amz-Copy-Source") != "":
			s.s3CopyObject(req)
		case m == http.MethodPut:
			s.s3PutObject(req)
		case (m == http.MethodGet || m == http.MethodHead) && q.Has("uploadId"):
			s.s3ListParts(req)
		case (m == http.MethodGet || m == http.MethodHead) && q.Has("partNumber"):
			s.s3Fail(req, notImplemented("reading one part (partNumber)"))
		case m == http.MethodGet || m == http.MethodHead:
			s.s3GetObject(req)
		case m == http.MethodDelete && q.Has("uploadId"):
			s.s3AbortUpload(req)
		case m == http.MethodDelete:
			s.s3DeleteObject(req)
		case m == http.MethodPost && q.Has("uploads"):
			s.s3CreateUpload(req)
		case m == http.MethodPost && q.Has("uploadId"):
			s.s3CompleteUpload(req)
		default:
			s.s3Fail(req, s3proto.Errf("MethodNotAllowed", http.StatusMethodNotAllowed, "the specified method is not allowed against this resource"))
		}
		return
	}
	s.s3Fail(req, s3proto.Errf("MethodNotAllowed", http.StatusMethodNotAllowed, "the specified method is not allowed against this resource"))
}

// hasSubresource reports whether a query names something besides the
// signature, the SDKs' x-id hint and response overrides.
func hasSubresource(q url.Values) bool {
	for k := range q {
		if strings.HasPrefix(k, "X-Amz-") || strings.HasPrefix(k, "x-amz-") || k == "x-id" || strings.HasPrefix(k, "response-") {
			continue
		}
		return true
	}
	return false
}

// ---- buckets and access

func noSuchBucket(name string) *s3proto.Error {
	return s3proto.Errf("NoSuchBucket", http.StatusNotFound, "the specified bucket does not exist")
}

func accessDenied() *s3proto.Error {
	return s3proto.Errf("AccessDenied", http.StatusForbidden, "access denied")
}

// s3Bucket resolves the request's bucket in the request's workspace and
// checks access. A bucket of another workspace is never found. A bucket the
// token can't see answers like a missing one; without a token, missing and
// private buckets both answer AccessDenied. publicOK marks the object reads
// a public bucket allows without a token.
func (s *Server) s3Bucket(req *s3Req, need string, publicOK bool) (meta.Bucket, *s3proto.Error) {
	b, err := s.eng.GetBucket(req.r.Context(), req.ws, req.bucket)
	if req.anon {
		if err != nil || !publicOK || !b.PublicRead {
			return meta.Bucket{}, accessDenied()
		}
		s.s3CORS(req, b)
		return b, nil
	}
	if err != nil {
		if apierr.As(err).Code == apierr.BucketNotFound {
			return meta.Bucket{}, noSuchBucket(req.bucket)
		}
		return meta.Bucket{}, s.s3FromAPI(req, err)
	}
	if !req.p.SeesBucket(b) {
		return meta.Bucket{}, noSuchBucket(req.bucket)
	}
	s.s3CORS(req, b)
	if !req.p.BucketAtLeast(need, b) {
		return meta.Bucket{}, accessDenied()
	}
	return b, nil
}

func (s *Server) s3Audit(req *s3Req, action, target string, err error) {
	e := meta.AuditEntry{WorkspaceID: req.ws, TokenID: req.p.TokenID, Actor: req.p.Actor, Action: action, Target: target, Result: "ok", RequestID: req.id}
	if err != nil {
		e.Result, e.Code = "error", apierr.As(err).Code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if aerr := s.store.AddAudit(ctx, e, s.eng.Config().AuditMaxRows); aerr != nil {
		s.log.Error("audit", "err", aerr, "action", action)
	}
}

func (s *Server) s3ListBuckets(req *s3Req) {
	if req.anon {
		s.s3Fail(req, accessDenied())
		return
	}
	all, err := s.eng.ListBuckets(req.r.Context(), req.ws)
	if err != nil {
		s.s3Err(req, err)
		return
	}
	out := s3proto.ListAllMyBucketsResult{Xmlns: s3proto.Namespace, Owner: s3proto.TheOwner, Buckets: []s3proto.BucketEntry{}}
	for _, b := range all {
		if req.p.SeesBucket(b) {
			out.Buckets = append(out.Buckets, s3proto.BucketEntry{Name: b.Name, CreationDate: s3proto.XMLTime(b.CreatedAt)})
		}
	}
	s.s3XML(req, http.StatusOK, out)
}

func (s *Server) s3CreateBucket(req *s3Req) {
	if req.anon || !req.p.WorkspaceAtLeast(auth.AccessAdmin) {
		s.s3Fail(req, accessDenied())
		return
	}
	if _, e := s.s3ReadSmall(req, s3SmallBody); e != nil {
		s.s3Fail(req, e)
		return
	}
	b, err := s.eng.CreateBucket(req.r.Context(), req.ws, req.bucket)
	target := req.bucket
	if err == nil {
		target = b.ID
	}
	s.s3Audit(req, "bucket.create", target, err)
	if err != nil {
		s.s3Err(req, err)
		return
	}
	req.w.Header().Set("Location", "/"+b.Name)
	req.w.WriteHeader(http.StatusOK)
}

func (s *Server) s3DeleteBucket(req *s3Req) {
	if req.anon {
		s.s3Fail(req, accessDenied())
		return
	}
	b, e := s.s3Bucket(req, auth.AccessAdmin, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	if !req.p.WorkspaceAtLeast(auth.AccessAdmin) {
		s.s3Fail(req, accessDenied())
		return
	}
	err := s.eng.DeleteBucket(req.r.Context(), b.ID)
	s.s3Audit(req, "bucket.delete", b.ID, err)
	if err != nil {
		s.s3Err(req, err)
		return
	}
	req.w.WriteHeader(http.StatusNoContent)
}

func (s *Server) s3HeadBucket(req *s3Req) {
	if _, e := s.s3Bucket(req, auth.AccessRead, false); e != nil {
		s.s3Fail(req, e)
		return
	}
	req.w.WriteHeader(http.StatusOK)
}

func (s *Server) s3Location(req *s3Req) {
	if _, e := s.s3Bucket(req, auth.AccessRead, false); e != nil {
		s.s3Fail(req, e)
		return
	}
	s.s3XML(req, http.StatusOK, s3proto.LocationConstraint{Xmlns: s3proto.Namespace})
}

func (s *Server) s3Versioning(req *s3Req) {
	if _, e := s.s3Bucket(req, auth.AccessRead, false); e != nil {
		s.s3Fail(req, e)
		return
	}
	s.s3XML(req, http.StatusOK, s3proto.VersioningConfiguration{Xmlns: s3proto.Namespace})
}

// ---- CORS

func originAllowed(b meta.Bucket, origin string) (string, bool) {
	for _, o := range b.CORSOrigins {
		if o == "*" {
			return "*", true
		}
		if o == origin {
			return origin, true
		}
	}
	return "", false
}

const corsExpose = "ETag, Content-Length, Content-Range, Content-Type, Last-Modified, Accept-Ranges, x-amz-request-id, x-amz-checksum-crc32, x-amz-checksum-crc32c, x-amz-checksum-sha1, x-amz-checksum-sha256"

// s3CORS adds CORS headers for an allowed Origin on a bucket request.
func (s *Server) s3CORS(req *s3Req, b meta.Bucket) {
	origin := req.r.Header.Get("Origin")
	if origin == "" {
		return
	}
	h := req.w.Header()
	h.Add("Vary", "Origin")
	if allow, ok := originAllowed(b, origin); ok {
		h.Set("Access-Control-Allow-Origin", allow)
		h.Set("Access-Control-Expose-Headers", corsExpose)
	}
}

// s3PreflightWorkspace finds the workspace of a CORS preflight, which
// carries no credentials of its own. A presigned URL names its access key
// in the query, so its token's workspace is used; anything else is the
// workspace the URL names, or the default one. The answer only says whether
// an origin may call a bucket, so the key isn't verified here: the request
// that follows is.
func (s *Server) s3PreflightWorkspace(req *s3Req) string {
	ctx := req.r.Context()
	if req.sel == "" {
		if cred := req.q.Get("X-Amz-Credential"); cred != "" {
			keyID, _, _ := strings.Cut(cred, "/")
			if tokID, ok := auth.TokenIDOfAccessKey(keyID); ok {
				if rec, err := s.store.GetToken(ctx, tokID); err == nil && len(rec.AccessSecret) > 0 {
					if p, err := s.principalOf(ctx, rec); err == nil && p.Bound() {
						return p.WorkspaceID
					}
				}
			}
		}
	}
	ws, err := s.requestWorkspace(ctx, false, "", req.sel)
	if err != nil {
		return ""
	}
	return ws.ID
}

// s3Preflight answers a CORS preflight from the bucket's origins.
func (s *Server) s3Preflight(req *s3Req) {
	origin := req.r.Header.Get("Origin")
	method := req.r.Header.Get("Access-Control-Request-Method")
	h := req.w.Header()
	h.Add("Vary", "Origin")
	h.Add("Vary", "Access-Control-Request-Method")
	h.Add("Vary", "Access-Control-Request-Headers")
	forbidden := s3proto.Errf("AccessForbidden", http.StatusForbidden, "CORSResponse: this CORS request is not allowed")
	if req.bucket == "" || origin == "" || method == "" {
		s.s3Fail(req, forbidden)
		return
	}
	if ok, wait := s.publicLimit.Allow(s.clientIPFromRequest(req.r)); !ok {
		h.Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		s.s3Fail(req, s3proto.Errf("SlowDown", http.StatusServiceUnavailable, "too many requests without a token from this address"))
		return
	}
	b, err := s.eng.GetBucket(req.r.Context(), s.s3PreflightWorkspace(req), req.bucket)
	allow, ok := originAllowed(b, origin)
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPost, http.MethodDelete:
	default:
		ok = false
	}
	if err != nil || !ok {
		s.s3Fail(req, forbidden)
		return
	}
	h.Set("Access-Control-Allow-Origin", allow)
	h.Set("Access-Control-Allow-Methods", "GET, HEAD, PUT, POST, DELETE")
	if rh := req.r.Header.Get("Access-Control-Request-Headers"); rh != "" {
		h.Set("Access-Control-Allow-Headers", rh)
	}
	h.Set("Access-Control-Expose-Headers", corsExpose)
	h.Set("Access-Control-Max-Age", "3600")
	req.w.WriteHeader(http.StatusOK)
}

// ---- bodies

// captureReader turns a protocol error from a verifying reader into a
// documented engine error, and remembers the precise one for the response.
type captureReader struct {
	req *s3Req
	r   io.Reader
}

func (c captureReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	var pe *s3proto.Error
	if errors.As(err, &pe) {
		c.req.bodyErr = pe
		return n, apierr.New(apierr.BadDigest, pe.Message)
	}
	return n, err
}

// s3Body prepares an upload body: aws-chunked decoding with chunk
// signatures, the signed payload hash, declared checksums, and an idle
// deadline. It returns the decoded length.
func (s *Server) s3Body(req *s3Req) (io.Reader, int64, *s3proto.Error) {
	r := req.r
	var body io.Reader = &idleReader{r: r.Body, set: http.NewResponseController(req.w).SetReadDeadline, idle: uploadIdle}
	hash := s3proto.UnsignedPayload
	if req.sig != nil {
		hash = req.sig.PayloadHash
	} else if v := r.Header.Get("X-Amz-Content-Sha256"); v != "" {
		// A bearer-token client may still send a payload hash or an
		// unsigned chunked body; signed chunks need a signature.
		switch {
		case v == s3proto.StreamingSigned || v == s3proto.StreamingSignedTrailer:
			return nil, 0, s3proto.Errf("InvalidRequest", http.StatusBadRequest, "signed chunks need AWS Signature Version 4, not a bearer token")
		case v == s3proto.UnsignedPayload || v == s3proto.StreamingUnsignedTrail:
			hash = v
		case len(v) == 64:
			hash = strings.ToLower(v)
		default:
			return nil, 0, s3proto.Errf("InvalidArgument", http.StatusBadRequest, "x-amz-content-sha256 must be a SHA-256 hex digest or a known streaming mode")
		}
	}
	size := r.ContentLength
	sums, e := s3proto.HeaderChecksums(r.Header)
	if e != nil {
		return nil, 0, e
	}
	var chunked *s3proto.ChunkedReader
	switch hash {
	case s3proto.StreamingSigned, s3proto.StreamingSignedTrailer, s3proto.StreamingUnsignedTrail:
		dl := r.Header.Get("X-Amz-Decoded-Content-Length")
		n, err := strconv.ParseInt(dl, 10, 64)
		if err != nil || n < 0 {
			return nil, 0, s3proto.Errf("MissingContentLength", http.StatusLengthRequired, "a streaming upload needs x-amz-decoded-content-length")
		}
		size = n
		var signer *s3proto.ChunkSigner
		if hash != s3proto.StreamingUnsignedTrail {
			signer = &s3proto.ChunkSigner{Key: req.sigKey, Time: req.sig.Time, Scope: req.sig.Scope, Prev: strings.ToLower(req.sig.Signature)}
		}
		trailer := r.Header.Get("X-Amz-Trailer")
		if hash == s3proto.StreamingSigned && trailer != "" {
			return nil, 0, s3proto.Errf("InvalidRequest", http.StatusBadRequest, "x-amz-trailer needs a -TRAILER streaming mode")
		}
		chunked = s3proto.NewChunkedReader(body, signer, trailer)
		body = chunked
		for _, t := range strings.Split(trailer, ",") {
			t = strings.ToLower(strings.TrimSpace(t))
			if t == "" {
				continue
			}
			alg, ok := strings.CutPrefix(t, "x-amz-checksum-")
			if !ok || s3proto.NewChecksum(alg) == nil {
				return nil, 0, s3proto.Errf("InvalidRequest", http.StatusBadRequest, "unsupported trailer %s", t)
			}
			sums = append(sums, s3proto.Checksum{Alg: alg})
		}
	case s3proto.UnsignedPayload:
	default:
		body = s3proto.VerifySHA256(body, hash)
	}
	if size < 0 {
		return nil, 0, s3proto.Errf("MissingContentLength", http.StatusLengthRequired, "you must provide the Content-Length HTTP header")
	}
	body = s3proto.VerifyChecksums(body, sums, func(alg string) string {
		for _, c := range sums {
			if c.Alg == alg && c.Value != "" {
				return c.Value
			}
		}
		if chunked != nil {
			return chunked.Trailer("x-amz-checksum-" + alg)
		}
		return ""
	})
	return captureReader{req: req, r: body}, size, nil
}

func (s *Server) s3ContentMD5(req *s3Req) ([]byte, *s3proto.Error) {
	v := req.r.Header.Get("Content-MD5")
	if v == "" {
		return nil, nil
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) != 16 {
		return nil, s3proto.Errf("InvalidDigest", http.StatusBadRequest, "the Content-MD5 you specified was invalid")
	}
	return b, nil
}

// s3ReadSmall reads a small request body (XML), verified like an upload.
func (s *Server) s3ReadSmall(req *s3Req, max int64) ([]byte, *s3proto.Error) {
	body, size, e := s.s3Body(req)
	if e != nil {
		if e.Code == "MissingContentLength" && req.r.ContentLength <= 0 && req.r.Header.Get("X-Amz-Decoded-Content-Length") == "" {
			return nil, nil // no body at all
		}
		return nil, e
	}
	if size > max {
		return nil, s3proto.Errf("MaxMessageLengthExceeded", http.StatusBadRequest, "your request was too big")
	}
	b, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, s.s3FromAPI(req, err)
	}
	if int64(len(b)) > max {
		return nil, s3proto.Errf("MaxMessageLengthExceeded", http.StatusBadRequest, "your request was too big")
	}
	if md5, e := s.s3ContentMD5(req); e != nil {
		return nil, e
	} else if md5 != nil && !bytes.Equal(md5, md5Sum(b)) {
		return nil, s3proto.Errf("BadDigest", http.StatusBadRequest, "the Content-MD5 you specified did not match what we received")
	}
	return b, nil
}

// s3Attrs reads an object's content type, standard headers and metadata
// from the request.
func s3Attrs(h http.Header) engine.ObjectAttrs {
	a := engine.ObjectAttrs{ContentType: h.Get("Content-Type"), Headers: map[string]string{}, Metadata: map[string]string{}}
	for _, name := range engine.ObjectHeaders {
		v := strings.Join(h.Values(name), ",")
		if name == "content-encoding" {
			var keep []string
			for _, enc := range strings.Split(v, ",") {
				if enc = strings.TrimSpace(enc); enc != "" && !strings.EqualFold(enc, "aws-chunked") {
					keep = append(keep, enc)
				}
			}
			v = strings.Join(keep, ",")
		}
		if v != "" {
			a.Headers[name] = v
		}
	}
	for name, vs := range h {
		if l := strings.ToLower(name); strings.HasPrefix(l, "x-amz-meta-") {
			a.Metadata[strings.TrimPrefix(l, "x-amz-meta-")] = strings.Join(vs, ",")
		}
	}
	return a
}

// ---- objects

func quoteETag(e string) string { return `"` + e + `"` }

func md5Sum(b []byte) []byte {
	h := md5.Sum(b)
	return h[:]
}

// setObjectHeaders writes an object's stored headers, with response-*
// overrides for signed requests.
func setObjectHeaders(req *s3Req, o meta.Object) {
	h := req.w.Header()
	h.Set("ETag", quoteETag(o.ETag))
	h.Set("Last-Modified", o.ModifiedAt.UTC().Format(http.TimeFormat))
	h.Set("Content-Type", o.ContentType)
	h.Set("Accept-Ranges", "bytes")
	for k, v := range o.Headers {
		h.Set(k, v)
	}
	for k, v := range o.Metadata {
		h.Set("X-Amz-Meta-"+k, v)
	}
	if o.Parts > 0 {
		h.Set("x-amz-mp-parts-count", strconv.Itoa(o.Parts))
	}
	if !req.anon {
		for _, name := range []string{"content-type", "content-language", "expires", "cache-control", "content-disposition", "content-encoding"} {
			if v := req.q.Get("response-" + name); v != "" {
				h.Set(name, v)
			}
		}
	}
	setContentSafety(h)
}

// setContentSafety keeps object content inert in a browser: objects are
// served from the engine's origin, which may be the dashboard's too, so
// uploaded HTML or SVG must never run as that origin. The sandbox makes a
// document opened directly a unique origin without scripts; nosniff stops
// a browser from guessing a more active type.
func setContentSafety(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
}

// checkConditions applies If-Match, If-Unmodified-Since, If-None-Match and
// If-Modified-Since, in that order of precedence. It returns the status to
// answer with instead of the object, or 0.
func checkConditions(h http.Header, o meta.Object, write bool) int {
	mod := o.ModifiedAt.Truncate(time.Second)
	etagMatches := func(list string) bool {
		for _, e := range strings.Split(list, ",") {
			e = strings.TrimSpace(e)
			if e == "*" || strings.Trim(strings.TrimPrefix(e, "W/"), `"`) == o.ETag {
				return true
			}
		}
		return false
	}
	if v := h.Get("If-Match"); v != "" {
		if !etagMatches(v) {
			return http.StatusPreconditionFailed
		}
	} else if v := h.Get("If-Unmodified-Since"); v != "" {
		if t, err := http.ParseTime(v); err == nil && mod.After(t) {
			return http.StatusPreconditionFailed
		}
	}
	notModified := http.StatusNotModified
	if write {
		notModified = http.StatusPreconditionFailed
	}
	if v := h.Get("If-None-Match"); v != "" {
		if etagMatches(v) {
			return notModified
		}
	} else if v := h.Get("If-Modified-Since"); v != "" && !write {
		if t, err := http.ParseTime(v); err == nil && !mod.After(t) {
			return notModified
		}
	}
	return 0
}

// parseRange reads a single "bytes=" range. ok is false when the header is
// absent or not one range, in which case the whole object is sent.
func parseRange(v string, size int64) (start, end int64, ok bool, unsatisfiable bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(v), "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	a, b, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false, false
	}
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" {
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n < 0 {
			return 0, 0, false, false
		}
		if n == 0 || size == 0 {
			return 0, 0, false, true
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true, false
	}
	start, err := strconv.ParseInt(a, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false, false
	}
	end = size - 1
	if b != "" {
		e, err := strconv.ParseInt(b, 10, 64)
		if err != nil || e < start {
			return 0, 0, false, false
		}
		end = min(e, size-1)
	}
	if start >= size {
		return 0, 0, false, true
	}
	return start, end, true, false
}

func (s *Server) s3GetObject(req *s3Req) {
	b, e := s.s3Bucket(req, auth.AccessRead, true)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	o, f, err := s.eng.OpenObject(req.r.Context(), b.ID, req.key)
	if err != nil {
		s.s3Err(req, err)
		return
	}
	defer f.Close()
	if status := checkConditions(req.r.Header, o, false); status != 0 {
		if status == http.StatusNotModified {
			h := req.w.Header()
			h.Set("ETag", quoteETag(o.ETag))
			h.Set("Last-Modified", o.ModifiedAt.UTC().Format(http.TimeFormat))
			req.w.WriteHeader(http.StatusNotModified)
			return
		}
		s.s3Fail(req, s3proto.Errf("PreconditionFailed", http.StatusPreconditionFailed, "at least one of the preconditions you specified did not hold"))
		return
	}
	start, end := int64(0), o.Size-1
	status := http.StatusOK
	if rv := req.r.Header.Get("Range"); rv != "" {
		st, en, ok, bad := parseRange(rv, o.Size)
		if bad {
			req.w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", o.Size))
			s.s3Fail(req, s3proto.Errf("InvalidRange", http.StatusRequestedRangeNotSatisfiable, "the requested range is not satisfiable"))
			return
		}
		if ok {
			start, end, status = st, en, http.StatusPartialContent
			req.w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, o.Size))
		}
	}
	setObjectHeaders(req, o)
	n := end - start + 1
	if o.Size == 0 {
		n = 0
	}
	req.w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
	req.w.WriteHeader(status)
	if req.r.Method == http.MethodHead || n == 0 {
		return
	}
	if _, err := io.Copy(req.w, io.NewSectionReader(f, start, n)); err != nil {
		panic(http.ErrAbortHandler) // headers are gone; cut the connection
	}
}

func (s *Server) s3PutObject(req *s3Req) {
	b, e := s.s3Bucket(req, auth.AccessWrite, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	body, size, e := s.s3Body(req)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	md5, e := s.s3ContentMD5(req)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	cond, e := writeCond(req.r.Header)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	o, err := s.eng.PutObject(req.r.Context(), engine.PutObjectInput{Workspace: req.ws, BucketID: b.ID, Key: req.key, Size: size, Body: body,
		Attrs: s3Attrs(req.r.Header), ContentMD5: md5, Cond: cond})
	if err != nil {
		s.s3Err(req, err)
		return
	}
	req.w.Header().Set("ETag", quoteETag(o.ETag))
	req.w.WriteHeader(http.StatusOK)
}

// writeCond reads If-None-Match: * and If-Match on a write. They are checked
// in the transaction that records the object, so two writers can't both
// pass.
func writeCond(h http.Header) (*meta.WriteCond, *s3proto.Error) {
	inm, im := h.Get("If-None-Match"), h.Get("If-Match")
	if inm == "" && im == "" {
		return nil, nil
	}
	if inm != "" && inm != "*" {
		return nil, notImplemented("If-None-Match on a write with a value other than *")
	}
	c := &meta.WriteCond{IfNoneMatch: inm == "*"}
	for _, e := range strings.Split(im, ",") {
		if e = strings.TrimSpace(e); e != "" {
			c.IfMatch = append(c.IfMatch, strings.Trim(strings.TrimPrefix(e, "W/"), `"`))
		}
	}
	return c, nil
}

func (s *Server) s3DeleteObject(req *s3Req) {
	b, e := s.s3Bucket(req, auth.AccessWrite, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	if _, err := s.eng.DeleteObject(req.r.Context(), req.ws, b.ID, req.key); err != nil {
		s.s3Err(req, err)
		return
	}
	req.w.WriteHeader(http.StatusNoContent)
}

func (s *Server) s3DeleteObjects(req *s3Req) {
	b, e := s.s3Bucket(req, auth.AccessWrite, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	raw, e := s.s3ReadSmall(req, s3DeleteBody)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	var in s3proto.Delete
	if err := xml.Unmarshal(raw, &in); err != nil || len(in.Objects) == 0 || len(in.Objects) > 1000 {
		s.s3Fail(req, s3proto.Errf("MalformedXML", http.StatusBadRequest, "the XML you provided was not well-formed or did not validate against our published schema"))
		return
	}
	out := s3proto.DeleteResult{Xmlns: s3proto.Namespace}
	for _, o := range in.Objects {
		if o.VersionID != "" && o.VersionID != "null" {
			out.Errors = append(out.Errors, s3proto.DeleteErrorEntry{Key: o.Key, Code: "NoSuchVersion", Message: "versioning isn't supported"})
			continue
		}
		if _, err := s.eng.DeleteObject(req.r.Context(), req.ws, b.ID, o.Key); err != nil {
			se := s.s3FromAPI(req, err)
			out.Errors = append(out.Errors, s3proto.DeleteErrorEntry{Key: o.Key, Code: se.Code, Message: se.Message})
			continue
		}
		if !in.Quiet {
			out.Deleted = append(out.Deleted, s3proto.DeletedEntry{Key: o.Key})
		}
	}
	s.s3XML(req, http.StatusOK, out)
}

// parseCopySource reads x-amz-copy-source: [/]bucket/key[?versionId=...].
func parseCopySource(v string) (bucket, key string, e *s3proto.Error) {
	bad := s3proto.Errf("InvalidArgument", http.StatusBadRequest, "copy source must be bucket/key, URL-encoded")
	src, version, _ := strings.Cut(v, "?")
	if version != "" {
		vq, err := url.ParseQuery(version)
		if err != nil || (vq.Get("versionId") != "" && vq.Get("versionId") != "null") {
			return "", "", s3proto.Errf("NoSuchVersion", http.StatusNotFound, "versioning isn't supported")
		}
	}
	dec, err := url.PathUnescape(strings.TrimPrefix(src, "/"))
	if err != nil {
		return "", "", bad
	}
	bucket, key, ok := strings.Cut(dec, "/")
	if !ok || bucket == "" || key == "" {
		return "", "", bad
	}
	return bucket, key, nil
}

func (s *Server) s3CopyObject(req *s3Req) {
	dst, e := s.s3Bucket(req, auth.AccessWrite, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	srcBucket, srcKey, e := parseCopySource(req.r.Header.Get("X-Amz-Copy-Source"))
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	ctx := req.r.Context()
	// The source is resolved in the same workspace as the destination, so
	// nothing is ever copied across workspaces.
	src, err := s.eng.GetBucket(ctx, req.ws, srcBucket)
	if err != nil || !req.p.SeesBucket(src) {
		s.s3Fail(req, noSuchBucket(srcBucket))
		return
	}
	if !req.p.BucketAtLeast(auth.AccessRead, src) {
		s.s3Fail(req, accessDenied())
		return
	}
	if _, e := s.s3ReadSmall(req, 0); e != nil {
		s.s3Fail(req, e)
		return
	}
	directive := strings.ToUpper(req.r.Header.Get("X-Amz-Metadata-Directive"))
	if directive != "" && directive != "COPY" && directive != "REPLACE" {
		s.s3Fail(req, s3proto.Errf("InvalidArgument", http.StatusBadRequest, "unknown metadata directive"))
		return
	}
	cond := http.Header{}
	for _, n := range []string{"If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		if v := req.r.Header.Get("X-Amz-Copy-Source-" + n); v != "" {
			cond.Set(n, v)
		}
	}
	o, err := s.eng.CopyObject(ctx, engine.CopyObjectInput{Workspace: req.ws, SrcBucketID: src.ID, SrcKey: srcKey, BucketID: dst.ID, Key: req.key,
		Replace: directive == "REPLACE", Attrs: s3Attrs(req.r.Header),
		Check: func(o meta.Object) error {
			// Every copy condition that doesn't hold, If-Modified-Since
			// and If-None-Match included, is a 412 on a copy.
			if checkConditions(cond, o, false) != 0 {
				return apierr.New(apierr.PreconditionFailed, "at least one of the preconditions you specified did not hold")
			}
			return nil
		}})
	if err != nil {
		s.s3Err(req, err)
		return
	}
	s.s3XML(req, http.StatusOK, s3proto.CopyObjectResult{Xmlns: s3proto.Namespace, ETag: quoteETag(o.ETag), LastModified: s3proto.XMLTime(o.ModifiedAt)})
}

// ---- listing

func maxParam(q url.Values, name string, def int) (int, *s3proto.Error) {
	v := q.Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, s3proto.Errf("InvalidArgument", http.StatusBadRequest, "%s must be a non-negative integer", name)
	}
	return min(n, def), nil
}

func (s *Server) s3ListObjects(req *s3Req, v2 bool) {
	b, e := s.s3Bucket(req, auth.AccessRead, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	q := req.q
	limit, e := maxParam(q, "max-keys", s3MaxKeys)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	enc := q.Get("encoding-type")
	if enc != "" && enc != "url" {
		s.s3Fail(req, s3proto.Errf("InvalidArgument", http.StatusBadRequest, "invalid encoding method specified in request"))
		return
	}
	in := engine.ListObjectsInput{BucketID: b.ID, Prefix: q.Get("prefix"), Delimiter: q.Get("delimiter"), Max: limit}
	token := q.Get("continuation-token")
	if v2 {
		in.After = q.Get("start-after")
		if token != "" {
			raw, err := base64.RawURLEncoding.DecodeString(token)
			if err != nil {
				s.s3Fail(req, s3proto.Errf("InvalidArgument", http.StatusBadRequest, "the continuation token provided is incorrect"))
				return
			}
			in.After = string(raw)
		}
	} else {
		in.After = q.Get("marker")
	}
	res, err := s.eng.ListObjects(req.r.Context(), in)
	if err != nil {
		s.s3Err(req, err)
		return
	}
	encode := func(v string) string {
		if enc == "url" {
			return s3proto.EncodeKey(v)
		}
		return v
	}
	owner := (*s3proto.Owner)(nil)
	if !v2 || q.Get("fetch-owner") == "true" {
		o := s3proto.TheOwner
		owner = &o
	}
	contents := make([]s3proto.Content, 0, len(res.Objects))
	for _, o := range res.Objects {
		contents = append(contents, s3proto.Content{Key: encode(o.Key), LastModified: s3proto.XMLTime(o.ModifiedAt),
			ETag: quoteETag(o.ETag), Size: o.Size, StorageClass: "STANDARD", Owner: owner})
	}
	prefixes := make([]s3proto.CommonPrefix, 0, len(res.Prefixes))
	for _, p := range res.Prefixes {
		prefixes = append(prefixes, s3proto.CommonPrefix{Prefix: encode(p)})
	}
	if v2 {
		out := s3proto.ListBucketResultV2{Xmlns: s3proto.Namespace, Name: b.Name, Prefix: encode(in.Prefix), Delimiter: encode(in.Delimiter),
			MaxKeys: limit, KeyCount: len(contents) + len(prefixes), IsTruncated: res.Truncated, EncodingType: enc,
			ContinuationToken: token, StartAfter: encode(q.Get("start-after")), Contents: contents, CommonPrefixes: prefixes}
		if res.Truncated {
			out.NextContinuationToken = base64.RawURLEncoding.EncodeToString([]byte(res.Next))
		}
		s.s3XML(req, http.StatusOK, out)
		return
	}
	out := s3proto.ListBucketResultV1{Xmlns: s3proto.Namespace, Name: b.Name, Prefix: encode(in.Prefix), Marker: encode(in.After),
		Delimiter: encode(in.Delimiter), MaxKeys: limit, IsTruncated: res.Truncated, EncodingType: enc, Contents: contents, CommonPrefixes: prefixes}
	if res.Truncated {
		out.NextMarker = encode(res.Next)
	}
	s.s3XML(req, http.StatusOK, out)
}

// ---- multipart

func (s *Server) s3CreateUpload(req *s3Req) {
	b, e := s.s3Bucket(req, auth.AccessWrite, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	if _, e := s.s3ReadSmall(req, s3SmallBody); e != nil {
		s.s3Fail(req, e)
		return
	}
	u, err := s.eng.CreateUpload(req.r.Context(), req.ws, b.ID, req.key, s3Attrs(req.r.Header), req.p.TokenID)
	if err != nil {
		s.s3Err(req, err)
		return
	}
	s.s3XML(req, http.StatusOK, s3proto.InitiateMultipartUploadResult{Xmlns: s3proto.Namespace, Bucket: b.Name, Key: req.key, UploadID: u.ID})
}

func (s *Server) s3UploadPart(req *s3Req) {
	b, e := s.s3Bucket(req, auth.AccessWrite, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	n, err := strconv.Atoi(req.q.Get("partNumber"))
	if err != nil || n < 1 || n > engine.MaxParts {
		s.s3Fail(req, s3proto.Errf("InvalidArgument", http.StatusBadRequest, "part number must be an integer between 1 and %d, inclusive", engine.MaxParts))
		return
	}
	body, size, e := s.s3Body(req)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	md5, e := s.s3ContentMD5(req)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	etag, err := s.eng.UploadPart(req.r.Context(), engine.UploadPartInput{Workspace: req.ws, UploadID: req.q.Get("uploadId"), BucketID: b.ID, Key: req.key,
		Number: n, Size: size, Body: body, ContentMD5: md5})
	if err != nil {
		s.s3Err(req, err)
		return
	}
	req.w.Header().Set("ETag", quoteETag(etag))
	req.w.WriteHeader(http.StatusOK)
}

func (s *Server) s3CompleteUpload(req *s3Req) {
	b, e := s.s3Bucket(req, auth.AccessWrite, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	raw, e := s.s3ReadSmall(req, s3CompleteBody)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	var in s3proto.CompleteMultipartUpload
	if err := xml.Unmarshal(raw, &in); err != nil {
		s.s3Fail(req, s3proto.Errf("MalformedXML", http.StatusBadRequest, "the XML you provided was not well-formed or did not validate against our published schema"))
		return
	}
	parts := make([]engine.CompletedPart, len(in.Parts))
	for i, p := range in.Parts {
		parts[i] = engine.CompletedPart{Number: p.PartNumber, ETag: p.ETag}
	}
	cond, e := writeCond(req.r.Header)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	o, err := s.eng.CompleteUpload(req.r.Context(), req.ws, req.q.Get("uploadId"), b.ID, req.key, parts, cond)
	if err != nil {
		s.s3Err(req, err)
		return
	}
	s.s3XML(req, http.StatusOK, s3proto.CompleteMultipartUploadResult{Xmlns: s3proto.Namespace,
		Location: req.base + "/" + b.Name + "/" + s3proto.URIEncode(o.Key, true), Bucket: b.Name, Key: o.Key, ETag: quoteETag(o.ETag)})
}

func (s *Server) s3AbortUpload(req *s3Req) {
	b, e := s.s3Bucket(req, auth.AccessWrite, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	if err := s.eng.AbortUpload(req.r.Context(), req.ws, req.q.Get("uploadId"), b.ID, req.key); err != nil {
		s.s3Err(req, err)
		return
	}
	req.w.WriteHeader(http.StatusNoContent)
}

func (s *Server) s3ListParts(req *s3Req) {
	b, e := s.s3Bucket(req, auth.AccessRead, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	limit, e := maxParam(req.q, "max-parts", 1000)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	after, _ := strconv.Atoi(req.q.Get("part-number-marker"))
	after = max(after, 0)
	uploadID := req.q.Get("uploadId")
	parts, truncated, err := s.eng.ListParts(req.r.Context(), uploadID, b.ID, req.key, after, limit)
	if err != nil {
		s.s3Err(req, err)
		return
	}
	out := s3proto.ListPartsResult{Xmlns: s3proto.Namespace, Bucket: b.Name, Key: req.key, UploadID: uploadID,
		Initiator: s3proto.TheOwner, Owner: s3proto.TheOwner, StorageClass: "STANDARD", PartNumberMarker: after,
		MaxParts: limit, IsTruncated: truncated, Parts: []s3proto.PartEntry{}}
	for _, p := range parts {
		out.Parts = append(out.Parts, s3proto.PartEntry{PartNumber: p.Number, LastModified: s3proto.XMLTime(p.CreatedAt), ETag: quoteETag(p.ETag), Size: p.Size})
		out.NextPartNumberMarker = p.Number
	}
	s.s3XML(req, http.StatusOK, out)
}

func (s *Server) s3ListUploads(req *s3Req) {
	b, e := s.s3Bucket(req, auth.AccessRead, false)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	limit, e := maxParam(req.q, "max-uploads", 1000)
	if e != nil {
		s.s3Fail(req, e)
		return
	}
	q := req.q
	ups, truncated, err := s.eng.ListUploads(req.r.Context(), b.ID, q.Get("prefix"), q.Get("key-marker"), q.Get("upload-id-marker"), limit)
	if err != nil {
		s.s3Err(req, err)
		return
	}
	out := s3proto.ListMultipartUploadsResult{Xmlns: s3proto.Namespace, Bucket: b.Name, KeyMarker: q.Get("key-marker"),
		UploadIDMarker: q.Get("upload-id-marker"), Prefix: q.Get("prefix"), MaxUploads: limit, IsTruncated: truncated, Uploads: []s3proto.UploadEntry{}}
	for _, u := range ups {
		out.Uploads = append(out.Uploads, s3proto.UploadEntry{Key: u.Key, UploadID: u.ID, Initiator: s3proto.TheOwner, Owner: s3proto.TheOwner,
			StorageClass: "STANDARD", Initiated: s3proto.XMLTime(u.CreatedAt)})
	}
	if truncated && len(ups) > 0 {
		out.NextKeyMarker, out.NextUploadIDMarker = ups[len(ups)-1].Key, ups[len(ups)-1].ID
	}
	s.s3XML(req, http.StatusOK, out)
}
