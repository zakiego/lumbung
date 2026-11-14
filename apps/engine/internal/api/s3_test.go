package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/backup"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/s3proto"
)

// key is a token with an S3 access key.
type key struct {
	ID     string `json:"id"`
	Token  string `json:"token"`
	AKID   string `json:"access_key_id"`
	Secret string `json:"secret_access_key"`
}

func (e *env) accessKey(body map[string]any) key {
	e.t.Helper()
	body["access_key"] = true
	if body["name"] == nil {
		body["name"] = "s3"
	}
	r := e.ok(e.do(e.admin, "POST", "/v1/tokens", "/v1/tokens", body), 201)
	var k key
	if err := json.Unmarshal(r.body, &k); err != nil || k.AKID == "" || k.Secret == "" {
		e.t.Fatalf("token with access key: %s", r.body)
	}
	return k
}

// s3 sends one signed S3 request. rawPath is the escaped path under the
// server, e.g. /v1/s3/bucket/a%20b. hdr is name, value pairs.
func (e *env) s3(k key, method, rawPath string, body []byte, hdr ...string) resp {
	e.t.Helper()
	return e.s3At(k, time.Now(), method, rawPath, body, hdr...)
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (e *env) s3Request(k key, at time.Time, method, rawPath string, body []byte, hdr ...string) *http.Request {
	e.t.Helper()
	req, err := http.NewRequest(method, e.http.URL+rawPath, bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	payload := sha256hex(body)
	for i := 0; i+1 < len(hdr); i += 2 {
		if strings.EqualFold(hdr[i], "x-amz-content-sha256") {
			payload = hdr[i+1]
			continue
		}
		req.Header.Set(hdr[i], hdr[i+1])
	}
	if k.AKID == "" {
		return req
	}
	req.Header.Set("X-Amz-Date", at.UTC().Format(s3proto.TimeFormat))
	req.Header.Set("X-Amz-Content-Sha256", payload)
	signed := []string{"host"}
	for name := range req.Header {
		if l := strings.ToLower(name); strings.HasPrefix(l, "x-amz-") || l == "content-type" || l == "content-md5" {
			signed = append(signed, l)
		}
	}
	sortStrings(signed)
	scope := s3proto.Scope{Date: at.UTC().Format("20060102"), Region: "us-east-1", Service: "s3"}
	path, _ := url.PathUnescape(strings.SplitN(rawPath, "?", 2)[0])
	canonical := s3proto.CanonicalRequest(method, path, req.URL.Query(), req.Header, req.URL.Host, signed, payload)
	sig := s3proto.Sign(s3proto.SigningKey(k.Secret, scope), s3proto.StringToSign(at, scope, canonical))
	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		s3proto.Algorithm, k.AKID, scope.String(), strings.Join(signed, ";"), sig))
	return req
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (e *env) send(req *http.Request) resp {
	e.t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, header: res.Header, body: b}
}

func (e *env) s3At(k key, at time.Time, method, rawPath string, body []byte, hdr ...string) resp {
	e.t.Helper()
	return e.send(e.s3Request(k, at, method, rawPath, body, hdr...))
}

// s3Code reads the S3 error code from an XML error body.
func s3Code(r resp) string {
	var doc struct {
		Code string `xml:"Code"`
	}
	_ = xml.Unmarshal(r.body, &doc)
	return doc.Code
}

func (e *env) s3OK(r resp, want int) resp {
	e.t.Helper()
	if r.status != want {
		e.t.Fatalf("status %d, want %d: %s", r.status, want, r.body)
	}
	return r
}

func (e *env) s3Fails(r resp, status int, code string) {
	e.t.Helper()
	if r.status != status || (code != "" && s3Code(r) != code) {
		e.t.Fatalf("got %d %s, want %d %s: %s", r.status, s3Code(r), status, code, r.body)
	}
	if len(r.body) > 0 && !strings.HasPrefix(r.header.Get("Content-Type"), "application/xml") {
		e.t.Fatalf("error content type %q", r.header.Get("Content-Type"))
	}
}

func (e *env) presign(k key, method, rawPath string, expires time.Duration, at time.Time) string {
	e.t.Helper()
	u, _ := url.Parse(e.http.URL)
	path, _ := url.PathUnescape(rawPath)
	return s3proto.Presign(s3proto.PresignInput{Method: method, Scheme: "http", Host: u.Host, Path: path, AccessKeyID: k.AKID,
		Secret: k.Secret, Region: "us-east-1", Time: at, Expires: expires})
}

func (e *env) fetch(method, rawURL string, body []byte, hdr ...string) resp {
	e.t.Helper()
	req, _ := http.NewRequest(method, rawURL, bytes.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return e.send(req)
}

// s3World is two buckets, a and b, each with one object, and keys for
// them.
type s3World struct {
	*env
	admin                 key // instance admin
	aAdmin, aWrite, aRead key
	bRead                 key
	dbAdmin               key // a database token
}

func newS3World(t *testing.T, mod func(*config.Config)) *s3World {
	e := newEnv(t, mod)
	w := &s3World{env: e}
	w.admin = e.accessKey(map[string]any{"access": "admin"})
	for _, b := range []string{"bucket-a", "bucket-b"} {
		e.s3OK(e.s3(w.admin, "PUT", "/v1/s3/"+b, nil), 200)
		e.s3OK(e.s3(w.admin, "PUT", "/v1/s3/"+b+"/doc.txt", []byte("secret of "+b)), 200)
	}
	w.aAdmin = e.accessKey(map[string]any{"bucket": "bucket-a", "access": "admin"})
	w.aWrite = e.accessKey(map[string]any{"bucket": "bucket-a", "access": "write"})
	w.aRead = e.accessKey(map[string]any{"bucket": "bucket-a", "access": "read"})
	w.bRead = e.accessKey(map[string]any{"bucket": "bucket-b", "access": "read"})
	e.ok(e.do(e.admin, "POST", "/v1/databases", "/v1/databases", map[string]any{"name": "app"}), 201)
	w.dbAdmin = e.accessKey(map[string]any{"database": "app", "access": "admin"})
	return w
}

// bucketOps are requests on one bucket, as (method, path suffix, body,
// headers) for an isolation check.
type bucketOp struct {
	name, method, path string
	body               []byte
	hdr                []string
}

func bucketOps(bucket string) []bucketOp {
	b := "/v1/s3/" + bucket
	return []bucketOp{
		{"get", "GET", b + "/doc.txt", nil, nil},
		{"head", "HEAD", b + "/doc.txt", nil, nil},
		{"put", "PUT", b + "/new.txt", []byte("x"), nil},
		{"delete", "DELETE", b + "/doc.txt", nil, nil},
		{"list v2", "GET", b + "?list-type=2", nil, nil},
		{"list v1", "GET", b, nil, nil},
		{"head bucket", "HEAD", b, nil, nil},
		{"location", "GET", b + "?location", nil, nil},
		{"delete objects", "POST", b + "?delete", []byte(`<Delete><Object><Key>doc.txt</Key></Object></Delete>`), nil},
		{"copy in", "PUT", b + "/copy.txt", nil, []string{"x-amz-copy-source", "/" + bucket + "/doc.txt"}},
		{"create upload", "POST", b + "/big?uploads", nil, nil},
		{"list uploads", "GET", b + "?uploads", nil, nil},
		{"delete bucket", "DELETE", b, nil, nil},
	}
}

// Row 1: a token for bucket A reads, lists, heads, writes, deletes, copies
// or presigns in bucket B: denied, the same answer as a missing bucket.
func TestS3IsolationOtherBucket(t *testing.T) {
	w := newS3World(t, nil)
	for _, k := range []key{w.aAdmin, w.aWrite, w.aRead} {
		for _, op := range bucketOps("bucket-b") {
			r := w.s3(k, op.method, op.path, op.body, op.hdr...)
			missing := w.s3(k, op.method, strings.Replace(op.path, "bucket-b", "bucket-x", 1), op.body, op.hdr...)
			if r.status != http.StatusNotFound || r.status != missing.status || s3Code(r) != s3Code(missing) {
				t.Fatalf("%s on another bucket: %d %s, missing bucket: %d %s", op.name, r.status, s3Code(r), missing.status, s3Code(missing))
			}
		}
		// A URL it presigns for B opens nothing either.
		w.s3Fails(w.fetch("GET", w.presign(k, "GET", "/v1/s3/bucket-b/doc.txt", time.Minute, time.Now()), nil), 404, "NoSuchBucket")
	}
	var list struct {
		Names []string `xml:"Buckets>Bucket>Name"`
	}
	_ = xml.Unmarshal(w.s3OK(w.s3(w.aRead, "GET", "/v1/s3", nil), 200).body, &list)
	if fmt.Sprint(list.Names) != "[bucket-a]" {
		t.Fatalf("bucket token lists %v", list.Names)
	}
	// Bucket B is untouched.
	r := w.s3OK(w.s3(w.admin, "GET", "/v1/s3/bucket-b/doc.txt", nil), 200)
	if string(r.body) != "secret of bucket-b" {
		t.Fatalf("bucket-b changed: %q", r.body)
	}
	w.s3Fails(w.s3(w.admin, "HEAD", "/v1/s3/bucket-b/new.txt", nil), 404, "")
}

// Row 2: CopyObject into A from B with a token for A: denied (the source
// needs read), and out of A into B too.
func TestS3IsolationCopyAcrossBuckets(t *testing.T) {
	w := newS3World(t, nil)
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/stolen.txt", nil, "x-amz-copy-source", "bucket-b/doc.txt"), 404, "NoSuchBucket")
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-b/leak.txt", nil, "x-amz-copy-source", "bucket-a/doc.txt"), 404, "NoSuchBucket")
	w.s3Fails(w.s3(w.aWrite, "HEAD", "/v1/s3/bucket-a/stolen.txt", nil), 404, "")
	// Read on B and write on A isn't one token, so it's still refused.
	w.s3Fails(w.s3(w.bRead, "PUT", "/v1/s3/bucket-a/stolen.txt", nil, "x-amz-copy-source", "bucket-b/doc.txt"), 404, "NoSuchBucket")
	// The instance admin may copy across buckets.
	w.s3OK(w.s3(w.admin, "PUT", "/v1/s3/bucket-a/copied.txt", nil, "x-amz-copy-source", "bucket-b/doc.txt"), 200)
}

// Row 3: a read token can't PUT, DELETE, DeleteObjects, copy, run any
// multipart step or presign a write.
func TestS3IsolationReadToken(t *testing.T) {
	w := newS3World(t, nil)
	var up struct {
		UploadID string `xml:"UploadId"`
	}
	_ = xml.Unmarshal(w.s3OK(w.s3(w.admin, "POST", "/v1/s3/bucket-a/big?uploads", nil), 200).body, &up)
	for _, op := range []bucketOp{
		{"put", "PUT", "/v1/s3/bucket-a/new.txt", []byte("x"), nil},
		{"delete", "DELETE", "/v1/s3/bucket-a/doc.txt", nil, nil},
		{"delete objects", "POST", "/v1/s3/bucket-a?delete", []byte(`<Delete><Object><Key>doc.txt</Key></Object></Delete>`), nil},
		{"copy in", "PUT", "/v1/s3/bucket-a/copy.txt", nil, []string{"x-amz-copy-source", "bucket-a/doc.txt"}},
		{"create upload", "POST", "/v1/s3/bucket-a/big2?uploads", nil, nil},
		{"upload part", "PUT", "/v1/s3/bucket-a/big?partNumber=1&uploadId=" + up.UploadID, []byte("x"), nil},
		{"complete", "POST", "/v1/s3/bucket-a/big?uploadId=" + up.UploadID, []byte(`<CompleteMultipartUpload></CompleteMultipartUpload>`), nil},
		{"abort", "DELETE", "/v1/s3/bucket-a/big?uploadId=" + up.UploadID, nil, nil},
		{"delete bucket", "DELETE", "/v1/s3/bucket-a", nil, nil},
		{"create bucket", "PUT", "/v1/s3/bucket-new", nil, nil},
	} {
		r := w.s3(w.aRead, op.method, op.path, op.body, op.hdr...)
		if r.status != http.StatusForbidden || s3Code(r) != "AccessDenied" {
			t.Fatalf("read token %s: %d %s", op.name, r.status, r.body)
		}
	}
	// A presigned PUT from a read token opens nothing.
	w.s3Fails(w.fetch("PUT", w.presign(w.aRead, "PUT", "/v1/s3/bucket-a/new.txt", time.Minute, time.Now()), []byte("x")), 403, "AccessDenied")
	w.s3Fails(w.s3(w.admin, "HEAD", "/v1/s3/bucket-a/new.txt", nil), 404, "")
	w.s3OK(w.s3(w.admin, "GET", "/v1/s3/bucket-a/doc.txt", nil), 200)
	// Reads work.
	w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/doc.txt", nil), 200)
	w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a?list-type=2", nil), 200)
}

// Row 4: a write token can't create or delete buckets or make tokens.
func TestS3IsolationWriteToken(t *testing.T) {
	w := newS3World(t, nil)
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-new", nil), 403, "AccessDenied")
	w.s3Fails(w.s3(w.aWrite, "DELETE", "/v1/s3/bucket-a", nil), 403, "AccessDenied")
	w.s3Fails(w.s3(w.aAdmin, "PUT", "/v1/s3/bucket-new", nil), 403, "AccessDenied")
	w.s3Fails(w.s3(w.aAdmin, "DELETE", "/v1/s3/bucket-a", nil), 403, "AccessDenied")
	w.fails(w.do(w.aWrite.Token, "POST", "/v1/tokens", "/v1/tokens", map[string]any{"name": "x", "bucket": "bucket-a", "access": "read"}), 403, "forbidden")
	// A bucket admin makes tokens for its bucket only, never broader.
	w.ok(w.do(w.aAdmin.Token, "POST", "/v1/tokens", "/v1/tokens", map[string]any{"name": "x", "bucket": "bucket-a", "access": "read"}), 201)
	w.fails(w.do(w.aAdmin.Token, "POST", "/v1/tokens", "/v1/tokens", map[string]any{"name": "x", "bucket": "bucket-b", "access": "read"}), 404, "bucket_not_found")
	w.fails(w.do(w.aAdmin.Token, "POST", "/v1/tokens", "/v1/tokens", map[string]any{"name": "x", "access": "read"}), 403, "forbidden")
	w.fails(w.do(w.aAdmin.Token, "POST", "/v1/tokens", "/v1/tokens", map[string]any{"name": "x", "database": "app", "access": "read"}), 404, "database_not_found")
	w.fails(w.do(w.aAdmin.Token, "POST", "/v1/tokens", "/v1/tokens", map[string]any{"name": "x", "bucket": "bucket-a", "database": "app", "access": "read"}), 400, "bad_request")
}

// Row 5: a database token on any bucket operation, and a bucket token on
// databases, /libsql and the SQL MCP tools: denied.
func TestS3IsolationAcrossKinds(t *testing.T) {
	w := newS3World(t, nil)
	for _, op := range bucketOps("bucket-a") {
		if r := w.s3(w.dbAdmin, op.method, op.path, op.body, op.hdr...); r.status != http.StatusNotFound {
			t.Fatalf("database token %s on a bucket: %d %s", op.name, r.status, r.body)
		}
	}
	w.s3Fails(w.s3(w.dbAdmin, "PUT", "/v1/s3/bucket-new", nil), 403, "AccessDenied")
	var list struct {
		Names []string `xml:"Buckets>Bucket>Name"`
	}
	_ = xml.Unmarshal(w.s3OK(w.s3(w.dbAdmin, "GET", "/v1/s3", nil), 200).body, &list)
	if len(list.Names) != 0 {
		t.Fatalf("database token lists buckets %v", list.Names)
	}
	// The database token's bearer form too.
	if r := w.send(func() *http.Request {
		req, _ := http.NewRequest("GET", w.http.URL+"/v1/s3/bucket-a/doc.txt", nil)
		req.Header.Set("Authorization", "Bearer "+w.dbAdmin.Token)
		return req
	}()); r.status != http.StatusNotFound {
		t.Fatalf("database bearer token on a bucket: %d", r.status)
	}

	bt := w.aAdmin.Token
	if got := w.ok(w.do(bt, "GET", "/v1/databases", "/v1/databases", nil), 200).json(t)["data"].([]any); len(got) != 0 {
		t.Fatalf("bucket token lists databases: %v", got)
	}
	w.fails(w.do(bt, "POST", "/v1/databases/{db}/query", "/v1/databases/app/query", map[string]any{"sql": "SELECT 1"}), 404, "database_not_found")
	w.fails(w.do(bt, "GET", "/v1/databases/{db}", "/v1/databases/app", nil), 404, "database_not_found")
	w.fails(w.do(bt, "POST", "/v1/databases", "/v1/databases", map[string]any{"name": "evil"}), 403, "forbidden")
	r := w.do(bt, "POST", "", "/libsql/app/main/v2/pipeline", map[string]any{"requests": []any{map[string]any{"type": "execute", "stmt": map[string]any{"sql": "SELECT 1"}}}})
	if r.status == http.StatusOK {
		t.Fatalf("bucket token ran SQL over libSQL: %s", r.body)
	}
	mcp := w.do(bt, "POST", "", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "query", "arguments": map[string]any{"database": "app", "sql": "SELECT 1"}}},
		"Accept", "application/json, text/event-stream")
	if !bytes.Contains(mcp.body, []byte("database_not_found")) {
		t.Fatalf("bucket token on the MCP query tool: %s", mcp.body)
	}
}

// Row 6: a multipart upload id from bucket B used under bucket A, or by
// another bucket's token: NoSuchUpload.
func TestS3IsolationUploadIDs(t *testing.T) {
	w := newS3World(t, nil)
	var up struct {
		UploadID string `xml:"UploadId"`
	}
	_ = xml.Unmarshal(w.s3OK(w.s3(w.admin, "POST", "/v1/s3/bucket-b/big?uploads", nil), 200).body, &up)
	for _, k := range []key{w.admin, w.aWrite} {
		w.s3Fails(w.s3(k, "PUT", "/v1/s3/bucket-a/big?partNumber=1&uploadId="+up.UploadID, []byte("x")), 404, "NoSuchUpload")
		w.s3Fails(w.s3(k, "DELETE", "/v1/s3/bucket-a/big?uploadId="+up.UploadID, nil), 404, "NoSuchUpload")
		w.s3Fails(w.s3(k, "GET", "/v1/s3/bucket-a/big?uploadId="+up.UploadID, nil), 404, "NoSuchUpload")
		w.s3Fails(w.s3(k, "POST", "/v1/s3/bucket-a/big?uploadId="+up.UploadID, []byte(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>x</ETag></Part></CompleteMultipartUpload>`)), 404, "NoSuchUpload")
	}
	// Under the same bucket with another key, too.
	w.s3Fails(w.s3(w.admin, "PUT", "/v1/s3/bucket-b/other?partNumber=1&uploadId="+up.UploadID, []byte("x")), 404, "NoSuchUpload")
	// The upload in B still works for its own bucket and key.
	w.s3OK(w.s3(w.admin, "PUT", "/v1/s3/bucket-b/big?partNumber=1&uploadId="+up.UploadID, []byte("x")), 200)
}

// Row 7: a presigned URL with its path, method, expiry or headers changed;
// expired; signed by a revoked token: denied.
func TestS3IsolationPresigned(t *testing.T) {
	w := newS3World(t, nil)
	good := w.presign(w.aAdmin, "GET", "/v1/s3/bucket-a/doc.txt", time.Minute, time.Now())
	w.s3OK(w.fetch("GET", good, nil), 200)
	w.s3Fails(w.fetch("GET", strings.Replace(good, "doc.txt", "other.txt", 1), nil), 403, "SignatureDoesNotMatch")
	w.s3Fails(w.fetch("PUT", good, []byte("x")), 403, "SignatureDoesNotMatch")
	w.s3Fails(w.fetch("GET", strings.Replace(good, "X-Amz-Expires=60", "X-Amz-Expires=600", 1), nil), 403, "SignatureDoesNotMatch")
	w.s3Fails(w.fetch("GET", good+"&response-content-type=text%2Fhtml", nil), 403, "SignatureDoesNotMatch")
	w.s3Fails(w.fetch("PUT", w.presign(w.aAdmin, "PUT", "/v1/s3/bucket-a/x.txt", time.Minute, time.Now()), nil,
		"x-amz-copy-source", "bucket-a/doc.txt"), 403, "AccessDenied")
	w.s3Fails(w.fetch("GET", w.presign(w.aAdmin, "GET", "/v1/s3/bucket-a/doc.txt", time.Minute, time.Now().Add(-2*time.Minute)), nil), 403, "AccessDenied")
	w.s3Fails(w.fetch("GET", w.presign(w.aAdmin, "GET", "/v1/s3/bucket-a/doc.txt", 8*24*time.Hour, time.Now()), nil), 400, "AuthorizationQueryParametersError")
	w.ok(w.do(w.admin.Token, "DELETE", "/v1/tokens/{token_id}", "/v1/tokens/"+w.aAdmin.ID, nil), 204)
	w.s3Fails(w.fetch("GET", good, nil), 403, "InvalidAccessKeyId")
	w.s3Fails(w.s3(w.aAdmin, "GET", "/v1/s3/bucket-a/doc.txt", nil), 403, "InvalidAccessKeyId")
}

// Row 8: a signed payload hash that doesn't match the body, a wrong secret,
// clock skew, unsigned x-amz headers: denied, nothing stored.
func TestS3IsolationSignatures(t *testing.T) {
	w := newS3World(t, nil)
	r := w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/tampered", []byte("evil"), "x-amz-content-sha256", sha256hex([]byte("good")))
	w.s3Fails(r, 400, "XAmzContentSHA256Mismatch")
	w.s3Fails(w.s3(w.admin, "HEAD", "/v1/s3/bucket-a/tampered", nil), 404, "")
	if entries, _ := os.ReadDir(filepath.Join(w.srv.Engine.Layout().TmpDir())); len(entries) != 0 {
		t.Fatalf("tmp/ not empty after a refused upload: %v", entries)
	}
	bad := w.aWrite
	bad.Secret = strings.Repeat("x", 40)
	w.s3Fails(w.s3(bad, "GET", "/v1/s3/bucket-a/doc.txt", nil), 403, "SignatureDoesNotMatch")
	w.s3Fails(w.s3At(w.aWrite, time.Now().Add(-20*time.Minute), "GET", "/v1/s3/bucket-a/doc.txt", nil), 403, "RequestTimeTooSkewed")
	unknown := w.aWrite
	unknown.AKID = "LMB" + strings.Repeat("Z", 20)
	w.s3Fails(w.s3(unknown, "GET", "/v1/s3/bucket-a/doc.txt", nil), 403, "InvalidAccessKeyId")
	// An x-amz header added after signing.
	req := w.s3Request(w.aWrite, time.Now(), "PUT", "/v1/s3/bucket-a/meta.txt", []byte("x"))
	req.Header.Set("X-Amz-Meta-Injected", "1")
	w.s3Fails(w.send(req), 403, "AccessDenied")
	// Signature V2 is refused.
	req, _ = http.NewRequest("GET", w.http.URL+"/v1/s3/bucket-a/doc.txt", nil)
	req.Header.Set("Authorization", "AWS "+w.aWrite.AKID+":c2ln")
	w.s3Fails(w.send(req), 400, "InvalidRequest")
	// A tampered signed chunk is refused, nothing stored.
	body, hdr := signedChunks(t, w.env, w.aWrite, "/v1/s3/bucket-a/chunked", []byte(strings.Repeat("z", 70000)))
	ok := w.send(chunkRequest(w.env, "/v1/s3/bucket-a/chunked", body, hdr))
	w.s3OK(ok, 200)
	got := w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/chunked", nil), 200)
	if string(got.body) != strings.Repeat("z", 70000) {
		t.Fatalf("chunked upload read back %d bytes", len(got.body))
	}
	body, hdr = signedChunks(t, w.env, w.aWrite, "/v1/s3/bucket-a/chunked2", []byte(strings.Repeat("z", 70000)))
	body = bytes.Replace(body, []byte("zzzz"), []byte("zzzy"), 1)
	w.s3Fails(w.send(chunkRequest(w.env, "/v1/s3/bucket-a/chunked2", body, hdr)), 403, "SignatureDoesNotMatch")
	w.s3Fails(w.s3(w.admin, "HEAD", "/v1/s3/bucket-a/chunked2", nil), 404, "")
}

// signedChunks makes an aws-chunked body with chunk signatures, as SDKs that
// sign each chunk send it.
func signedChunks(t *testing.T, e *env, k key, rawPath string, data []byte) ([]byte, http.Header) {
	t.Helper()
	at := time.Now()
	const chunk = 65536
	req := e.s3Request(k, at, "PUT", rawPath, nil, "x-amz-content-sha256", s3proto.StreamingSigned,
		"x-amz-decoded-content-length", fmt.Sprint(len(data)), "content-encoding", "aws-chunked")
	a, perr := s3proto.ParseHeader(req)
	if perr != nil {
		t.Fatal(perr)
	}
	signer := &s3proto.ChunkSigner{Key: s3proto.SigningKey(k.Secret, a.Scope), Time: a.Time, Scope: a.Scope, Prev: a.Signature}
	next := func(b []byte) string {
		sts := "AWS4-HMAC-SHA256-PAYLOAD\n" + at.UTC().Format(s3proto.TimeFormat) + "\n" + a.Scope.String() + "\n" + signer.Prev + "\n" + s3proto.EmptySHA256 + "\n" + sha256hex(b)
		signer.Prev = s3proto.Sign(signer.Key, sts)
		return signer.Prev
	}
	var buf bytes.Buffer
	for off := 0; off < len(data); off += chunk {
		b := data[off:min(off+chunk, len(data))]
		fmt.Fprintf(&buf, "%x;chunk-signature=%s\r\n", len(b), next(b))
		buf.Write(b)
		buf.WriteString("\r\n")
	}
	fmt.Fprintf(&buf, "0;chunk-signature=%s\r\n\r\n", next(nil))
	return buf.Bytes(), req.Header
}

func chunkRequest(e *env, rawPath string, body []byte, h http.Header) *http.Request {
	req, _ := http.NewRequest("PUT", e.http.URL+rawPath, bytes.NewReader(body))
	req.Header = h.Clone()
	return req
}

// Row 9: anonymous PUT, DELETE or list on a public bucket, anonymous GET on a
// private or missing bucket: denied, private and missing look the same.
func TestS3IsolationAnonymous(t *testing.T) {
	w := newS3World(t, nil)
	ctx := context.Background()
	b, _ := w.srv.Engine.GetBucket(ctx, "bucket-a")
	pub := true
	if _, err := w.srv.Engine.UpdateBucket(ctx, b.ID, meta.BucketUpdate{PublicRead: &pub}); err != nil {
		t.Fatal(err)
	}
	anon := key{}
	r := w.s3OK(w.s3(anon, "GET", "/v1/s3/bucket-a/doc.txt", nil), 200)
	if string(r.body) != "secret of bucket-a" {
		t.Fatalf("public read: %q", r.body)
	}
	w.s3OK(w.s3(anon, "HEAD", "/v1/s3/bucket-a/doc.txt", nil), 200)
	for _, op := range bucketOps("bucket-a") {
		if op.name == "get" || op.name == "head" {
			continue
		}
		r := w.s3(anon, op.method, op.path, op.body, op.hdr...)
		if r.status != http.StatusForbidden {
			t.Fatalf("anonymous %s on a public bucket: %d %s", op.name, r.status, r.body)
		}
	}
	w.s3Fails(w.s3(anon, "GET", "/v1/s3", nil), 403, "AccessDenied")
	private := w.s3(anon, "GET", "/v1/s3/bucket-b/doc.txt", nil)
	missing := w.s3(anon, "GET", "/v1/s3/bucket-zzz/doc.txt", nil)
	w.s3Fails(private, 403, "AccessDenied")
	w.s3Fails(missing, 403, "AccessDenied")
	// A missing key in a public bucket is NoSuchKey; response-* overrides
	// need a signed request.
	w.s3Fails(w.s3(anon, "GET", "/v1/s3/bucket-a/nope", nil), 404, "NoSuchKey")
	r = w.s3OK(w.s3(anon, "GET", "/v1/s3/bucket-a/doc.txt?response-content-type=text%2Fhtml", nil), 200)
	if r.header.Get("Content-Type") == "text/html" {
		t.Fatal("an anonymous request overrode the content type")
	}
	if _, err := w.srv.Engine.ListBuckets(ctx); err != nil {
		t.Fatal(err)
	}
}

// filesOutsideObjects hashes every file in the data folder except object
// storage and meta.db, to prove a request touched nothing else.
func filesOutsideObjects(t testing.TB, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		if d.IsDir() {
			if top == "objects" || top == "uploads" || top == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(rel, "meta.db") || rel == "lumbungd.lock" {
			return nil
		}
		b, _ := os.ReadFile(p)
		out[rel] = sha256hex(b)
		return nil
	})
	return out
}

// Row 10: keys like ../../meta.db, %2e%2e/x, /abs, a//b, 1025 bytes or
// control characters are opaque keys or refused; no file outside the
// bucket's folder is touched.
func TestS3IsolationKeys(t *testing.T) {
	w := newS3World(t, nil)
	w.ok(w.do(w.admin.Token, "POST", "/v1/databases/{db}/query", "/v1/databases/app/query", map[string]any{"sql": "CREATE TABLE t(x)"}), 200)
	root := w.srv.Engine.Layout().Root
	before := filesOutsideObjects(t, root)
	keys := map[string]string{ // raw path suffix -> key
		"..%2F..%2Fmeta.db":     "../../meta.db",
		"../../meta.db":         "../../meta.db",
		"%252e%252e/x":          "%2e%2e/x",
		"/abs":                  "/abs",
		"a//b":                  "a//b",
		"dbs/x.db":              "dbs/x.db",
		"secrets/service-token": "secrets/service-token",
		"%2E%2E":                "..",
	}
	for raw, k := range keys {
		w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/"+raw, []byte("v:"+k)), 200)
		r := w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/"+s3proto.URIEncode(k, true), nil), 200)
		if string(r.body) != "v:"+k {
			t.Fatalf("%q read back %q", k, r.body)
		}
	}
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/"+strings.Repeat("k", 1025), []byte("x")), 400, "KeyTooLongError")
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/a%00b", []byte("x")), 400, "")
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/a%0Ab", []byte("x")), 400, "")
	if after := filesOutsideObjects(t, root); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("files outside object storage changed:\nbefore %v\nafter  %v", before, after)
	}
	var list struct {
		Keys []string `xml:"Contents>Key"`
	}
	_ = xml.Unmarshal(w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a?list-type=2", nil), 200).body, &list)
	if len(list.Keys) != 8 { // the 8 raw paths name 7 distinct keys, plus doc.txt
		t.Fatalf("listing %v", list.Keys)
	}
}

// Row 11: object, bucket or instance storage past its limit: refused,
// nothing partial kept. (The free-disk check is tested in the engine.)
func TestS3IsolationQuotas(t *testing.T) {
	w := newS3World(t, func(c *config.Config) {
		c.MaxObjectBytes = 1000
		c.MaxBucketBytes = 1500
		c.MaxObjectStorageBytes = 2400
	})
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/big", bytes.Repeat([]byte("x"), 1001)), 400, "EntityTooLarge")
	w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/one", bytes.Repeat([]byte("x"), 1000)), 200)
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/two", bytes.Repeat([]byte("x"), 600)), 403, "QuotaExceeded")
	w.s3OK(w.s3(w.admin, "PUT", "/v1/s3/bucket-b/one", bytes.Repeat([]byte("x"), 1000)), 200)
	w.s3Fails(w.s3(w.admin, "PUT", "/v1/s3/bucket-b/two", bytes.Repeat([]byte("x"), 450)), 403, "QuotaExceeded")
	for _, k := range []string{"bucket-a/big", "bucket-a/two", "bucket-b/two"} {
		w.s3Fails(w.s3(w.admin, "HEAD", "/v1/s3/"+k, nil), 404, "")
	}
	// A body shorter than its declared length isn't kept.
	req := w.s3Request(w.aWrite, time.Now(), "PUT", "/v1/s3/bucket-a/short", []byte("abc"), "x-amz-content-sha256", s3proto.UnsignedPayload)
	req.ContentLength = 10
	req.Body = io.NopCloser(io.MultiReader(strings.NewReader("abc"), errAfter{}))
	if res, err := http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
	}
	w.s3Fails(w.s3(w.admin, "HEAD", "/v1/s3/bucket-a/short", nil), 404, "")
}

type errAfter struct{}

func (errAfter) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// Row 12: the access key secret is never in meta.db or a meta-only backup
// in plain text.
func TestS3IsolationSecretAtRest(t *testing.T) {
	w := newS3World(t, nil)
	root := w.srv.Engine.Layout().Root
	for _, name := range []string{"meta.db", "meta.db-wal"} {
		b, _ := os.ReadFile(filepath.Join(root, name))
		for _, k := range []key{w.admin, w.aRead} {
			if bytes.Contains(b, []byte(k.Secret)) {
				t.Fatalf("%s holds an access key secret in plain text", name)
			}
		}
	}
	dir, _, err := backup.Run(context.Background(), backup.Options{DataDir: root, To: t.TempDir(), MetaOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "meta.db"))
	if bytes.Contains(b, []byte(w.admin.Secret)) {
		t.Fatal("a meta-only backup holds an access key secret in plain text")
	}
}

func TestS3FailedAuthIsRateLimited(t *testing.T) {
	w := newS3World(t, func(c *config.Config) { c.AuthFailuresPerMin = 3 })
	bad := w.aRead
	bad.Secret = strings.Repeat("y", 40)
	codes := []string{}
	for i := 0; i < 6; i++ {
		codes = append(codes, s3Code(w.s3(bad, "GET", "/v1/s3/bucket-a/doc.txt", nil)))
	}
	if codes[len(codes)-1] != "SlowDown" {
		t.Fatalf("failed signatures aren't limited: %v", codes)
	}
	// A valid key still gets in.
	w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/doc.txt", nil), 200)
}

func TestS3ObjectReads(t *testing.T) {
	w := newS3World(t, nil)
	w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/r.txt", []byte("0123456789"), "Content-Type", "text/plain", "x-amz-meta-color", "Blue"), 200)
	cases := []struct {
		rng, body, cr string
		status        int
	}{
		{"bytes=2-4", "234", "bytes 2-4/10", 206},
		{"bytes=7-", "789", "bytes 7-9/10", 206},
		{"bytes=-3", "789", "bytes 7-9/10", 206},
		{"bytes=5-100", "56789", "bytes 5-9/10", 206},
		{"bytes=1-2,4-5", "0123456789", "", 200},
		{"lines=1-2", "0123456789", "", 200},
	}
	for _, c := range cases {
		r := w.s3(w.aRead, "GET", "/v1/s3/bucket-a/r.txt", nil, "Range", c.rng)
		if r.status != c.status || string(r.body) != c.body || r.header.Get("Content-Range") != c.cr {
			t.Fatalf("%s: %d %q %q", c.rng, r.status, r.body, r.header.Get("Content-Range"))
		}
	}
	r := w.s3(w.aRead, "GET", "/v1/s3/bucket-a/r.txt", nil, "Range", "bytes=10-")
	w.s3Fails(r, 416, "InvalidRange")
	if r.header.Get("Content-Range") != "bytes */10" {
		t.Fatalf("416 Content-Range %q", r.header.Get("Content-Range"))
	}
	head := w.s3OK(w.s3(w.aRead, "HEAD", "/v1/s3/bucket-a/r.txt", nil), 200)
	etag := head.header.Get("ETag")
	if head.header.Get("X-Amz-Meta-Color") != "Blue" || head.header.Get("Content-Length") != "10" || len(head.body) != 0 {
		t.Fatalf("head: %v", head.header)
	}
	w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/r.txt", nil, "If-None-Match", etag), 304)
	w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/r.txt", nil, "If-Match", etag), 200)
	w.s3Fails(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/r.txt", nil, "If-Match", `"nope"`), 412, "PreconditionFailed")
	w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/r.txt", nil, "If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)), 304)
	r = w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/r.txt?response-content-type=application%2Fjson&response-content-disposition=attachment", nil), 200)
	if r.header.Get("Content-Type") != "application/json" || r.header.Get("Content-Disposition") != "attachment" {
		t.Fatalf("overrides: %v", r.header)
	}
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/r.txt", []byte("x"), "If-None-Match", "*"), 412, "PreconditionFailed")
	w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/empty", nil), 200)
	if r := w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/empty", nil), 200); len(r.body) != 0 || r.header.Get("Content-Length") != "0" {
		t.Fatalf("empty object: %v %q", r.header, r.body)
	}
	// A bearer token works too.
	req, _ := http.NewRequest("GET", w.http.URL+"/v1/s3/bucket-a/r.txt", nil)
	req.Header.Set("Authorization", "Bearer "+w.aRead.Token)
	if r := w.send(req); r.status != 200 || string(r.body) != "0123456789" {
		t.Fatalf("bearer: %d %s", r.status, r.body)
	}
	w.s3Fails(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/r.txt?tagging", nil), 501, "NotImplemented")
}

func TestS3Listing(t *testing.T) {
	w := newS3World(t, nil)
	for _, k := range []string{"p/a", "p/b/1", "p/b/2", "p/c", "p/d e", "q"} {
		w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/"+s3proto.URIEncode(k, true), []byte(k)), 200)
	}
	type listing struct {
		Keys      []string `xml:"Contents>Key"`
		Prefixes  []string `xml:"CommonPrefixes>Prefix"`
		Truncated bool     `xml:"IsTruncated"`
		Next      string   `xml:"NextContinuationToken"`
		Marker    string   `xml:"NextMarker"`
		KeyCount  int      `xml:"KeyCount"`
	}
	get := func(q string) listing {
		var l listing
		_ = xml.Unmarshal(w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a?"+q, nil), 200).body, &l)
		return l
	}
	l := get("list-type=2&prefix=p%2F&delimiter=%2F")
	if fmt.Sprint(l.Keys, l.Prefixes) != "[p/a p/c p/d e] [p/b/]" || l.KeyCount != 4 {
		t.Fatalf("v2: %+v", l)
	}
	l = get("list-type=2&prefix=p%2F&delimiter=%2F&encoding-type=url")
	if fmt.Sprint(l.Keys) != "[p/a p/c p/d%20e]" {
		t.Fatalf("url encoding: %v", l.Keys)
	}
	var all []string
	q := "list-type=2&max-keys=2&delimiter=%2F&prefix=p%2F"
	for i := 0; i < 5; i++ {
		l := get(q)
		all = append(all, append(l.Keys, l.Prefixes...)...)
		if !l.Truncated {
			break
		}
		q = "list-type=2&max-keys=2&delimiter=%2F&prefix=p%2F&continuation-token=" + url.QueryEscape(l.Next)
	}
	if fmt.Sprint(all) != "[p/a p/b/ p/c p/d e]" {
		t.Fatalf("pages: %v", all)
	}
	// v1 with a marker on a common prefix doesn't repeat it.
	l = get("prefix=p%2F&delimiter=%2F&max-keys=2")
	if fmt.Sprintf("%v %v %v", l.Keys, l.Prefixes, l.Marker) != "[p/a] [p/b/] p/b/" {
		t.Fatalf("v1 page 1: %+v", l)
	}
	l = get("prefix=p%2F&delimiter=%2F&max-keys=2&marker=" + url.QueryEscape(l.Marker))
	if fmt.Sprint(l.Keys, l.Prefixes) != "[p/c p/d e] []" {
		t.Fatalf("v1 page 2: %+v", l)
	}
	w.s3Fails(w.s3(w.aRead, "GET", "/v1/s3/bucket-a?list-type=2&continuation-token=%25%25", nil), 400, "InvalidArgument")
}

func TestS3CORS(t *testing.T) {
	w := newS3World(t, nil)
	ctx := context.Background()
	b, _ := w.srv.Engine.GetBucket(ctx, "bucket-a")
	origins := []string{"https://app.example"}
	if _, err := w.srv.Engine.UpdateBucket(ctx, b.ID, meta.BucketUpdate{CORSOrigins: &origins}); err != nil {
		t.Fatal(err)
	}
	pre := w.fetch("OPTIONS", w.http.URL+"/v1/s3/bucket-a/up.txt", nil, "Origin", "https://app.example",
		"Access-Control-Request-Method", "PUT", "Access-Control-Request-Headers", "content-type")
	if pre.status != 200 || pre.header.Get("Access-Control-Allow-Origin") != "https://app.example" ||
		pre.header.Get("Access-Control-Allow-Headers") != "content-type" {
		t.Fatalf("preflight: %d %v", pre.status, pre.header)
	}
	for _, bad := range [][2]string{{"https://evil.example", "/v1/s3/bucket-a/up.txt"}, {"https://app.example", "/v1/s3/bucket-b/up.txt"}, {"https://app.example", "/v1/s3/nope/x"}} {
		r := w.fetch("OPTIONS", w.http.URL+bad[1], nil, "Origin", bad[0], "Access-Control-Request-Method", "GET")
		if r.status != 403 || r.header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("preflight %v: %d %v", bad, r.status, r.header)
		}
	}
	r := w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/doc.txt", nil, "Origin", "https://app.example"), 200)
	if r.header.Get("Access-Control-Allow-Origin") != "https://app.example" || !strings.Contains(r.header.Get("Access-Control-Expose-Headers"), "ETag") {
		t.Fatalf("CORS on a response: %v", r.header)
	}
	r = w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/doc.txt", nil, "Origin", "https://evil.example"), 200)
	if r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header for an origin not on the list")
	}
}

func TestS3MultipartFlow(t *testing.T) {
	w := newS3World(t, nil)
	var up struct {
		UploadID string `xml:"UploadId"`
	}
	_ = xml.Unmarshal(w.s3OK(w.s3(w.aWrite, "POST", "/v1/s3/bucket-a/mp?uploads", nil, "Content-Type", "text/csv"), 200).body, &up)
	p1 := bytes.Repeat([]byte("a"), 5<<20)
	e1 := w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/mp?partNumber=1&uploadId="+up.UploadID, p1), 200).header.Get("ETag")
	e2 := w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/mp?partNumber=2&uploadId="+up.UploadID, []byte("tail")), 200).header.Get("ETag")
	body := fmt.Sprintf(`<CompleteMultipartUpload><Part><PartNumber>2</PartNumber><ETag>%s</ETag></Part><Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part></CompleteMultipartUpload>`, e2, e1)
	w.s3Fails(w.s3(w.aWrite, "POST", "/v1/s3/bucket-a/mp?uploadId="+up.UploadID, []byte(body)), 400, "InvalidPartOrder")
	body = fmt.Sprintf(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part><Part><PartNumber>2</PartNumber><ETag>%s</ETag></Part></CompleteMultipartUpload>`, e1, e2)
	r := w.s3OK(w.s3(w.aWrite, "POST", "/v1/s3/bucket-a/mp?uploadId="+up.UploadID, []byte(body)), 200)
	if !bytes.Contains(r.body, []byte(`-2&#34;</ETag>`)) && !bytes.Contains(r.body, []byte(`-2"</ETag>`)) {
		t.Fatalf("complete: %s", r.body)
	}
	got := w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/mp", nil, "Range", fmt.Sprintf("bytes=%d-", (5<<20)-1)), 206)
	if string(got.body) != "atail" || got.header.Get("Content-Type") != "text/csv" {
		t.Fatalf("assembled: %q %v", got.body, got.header.Get("Content-Type"))
	}
	w.s3Fails(w.s3(w.aWrite, "POST", "/v1/s3/bucket-a/mp?uploadId="+up.UploadID, []byte(body)), 404, "NoSuchUpload")
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/mp?partNumber=0&uploadId="+up.UploadID, []byte("x")), 400, "InvalidArgument")
}

// FuzzS3Path sends random methods, paths and headers through the S3
// handler and fails if any file outside object storage is created or
// changed (PLAN 16.5).
func FuzzS3Path(f *testing.F) {
	for _, seed := range []string{"bucket-a/doc.txt", "bucket-a/../../meta.db", "bucket-a/%2e%2e/%2e%2e/meta.db", "bucket-a//x",
		"../secrets/service-token", "bucket-a/x?uploads", "bucket-a?delete", "bucket-a/x?partNumber=1&uploadId=up_aaaaaaaaaaaaaaaaaaaa",
		"%00", "bucket-a/dbs%2F..%2F..%2Fmeta.db", "bucket-a/" + strings.Repeat("a/", 300)} {
		f.Add("PUT", seed, "x", "")
		f.Add("GET", seed, "", "")
	}
	f.Add("PUT", "bucket-a/c", "", "../bucket-b/doc.txt")
	f.Add("POST", "bucket-a?delete", "<Delete><Object><Key>../../meta.db</Key></Object></Delete>", "")
	e := newEnv(f, nil)
	tok := e.admin
	for _, b := range []string{"bucket-a", "bucket-b"} {
		req, _ := http.NewRequest("PUT", e.http.URL+"/v1/s3/"+b, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		if r := e.send(req); r.status != 200 {
			f.Fatalf("create %s: %d %s", b, r.status, r.body)
		}
	}
	root := e.srv.Engine.Layout().Root
	before := filesOutsideObjects(f, root)
	f.Fuzz(func(t *testing.T, method, path, body, copySource string) {
		switch method {
		case "GET", "PUT", "POST", "DELETE", "HEAD", "OPTIONS":
		default:
			return
		}
		u, err := url.Parse(e.http.URL + "/v1/s3/" + path)
		if err != nil || u.Host == "" {
			return
		}
		req, err := http.NewRequest(method, u.String(), strings.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if copySource != "" {
			req.Header.Set("X-Amz-Copy-Source", copySource)
		}
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}
		if after := filesOutsideObjects(t, root); fmt.Sprint(after) != fmt.Sprint(before) {
			t.Fatalf("%s %s changed files outside object storage:\nbefore %v\nafter  %v", method, path, before, after)
		}
	})
}
