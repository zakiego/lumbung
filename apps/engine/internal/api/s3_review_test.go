package api_test

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/s3proto"
)

// Objects are served inert: uploaded HTML or SVG must never run as the
// engine's (and the dashboard's) origin.
func TestObjectResponsesAreInert(t *testing.T) {
	w := newS3World(t, nil)
	w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/x.html", []byte("<script>alert(1)</script>"), "Content-Type", "text/html"), 200)
	check := func(name string, r resp) {
		t.Helper()
		if r.header.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(r.header.Get("Content-Security-Policy"), "sandbox") {
			t.Fatalf("%s: %d %v", name, r.status, r.header)
		}
	}
	check("S3 GET", w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/x.html", nil), 200))
	check("S3 HEAD", w.s3OK(w.s3(w.aRead, "HEAD", "/v1/s3/bucket-a/x.html", nil), 200))
	check("S3 override", w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/x.html?response-content-type=text%2Fhtml", nil), 200))
	check("REST GET", w.ok(w.do(w.aRead.Token, "GET", "", "/v1/buckets/bucket-a/object?key=x.html", nil), 200))
	check("presigned GET", w.s3OK(w.fetch("GET", w.presign(w.aRead, "GET", "/v1/s3/bucket-a/x.html", time.Minute, time.Now()), nil), 200))
}

func TestS3ConditionalWrites(t *testing.T) {
	w := newS3World(t, nil)
	w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/lock", []byte("one"), "If-None-Match", "*"), 200)
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/lock", []byte("two"), "If-None-Match", "*"), 412, "PreconditionFailed")
	etag := fmt.Sprintf(`"%x"`, md5.Sum([]byte("one")))
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/lock", []byte("three"), "If-Match", `"nope"`), 412, "PreconditionFailed")
	w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/lock", []byte("three"), "If-Match", etag), 200)
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/missing", []byte("x"), "If-Match", etag), 412, "PreconditionFailed")
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/lock", []byte("x"), "If-None-Match", etag), 501, "NotImplemented")
	if r := w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/lock", nil), 200); string(r.body) != "three" {
		t.Fatalf("content after conditional writes: %q", r.body)
	}
	// Concurrent creates with If-None-Match: * let exactly one through.
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			req := w.s3Request(w.aWrite, time.Now(), "PUT", "/v1/s3/bucket-a/race", []byte(fmt.Sprint(i)), "If-None-Match", "*")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				results <- 0
				return
			}
			res.Body.Close()
			results <- res.StatusCode
		}(i)
	}
	ok := 0
	for i := 0; i < 8; i++ {
		if <-results == 200 {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d concurrent conditional creates succeeded", ok)
	}
	// Copy conditions that don't hold are 412.
	future := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/copy", nil, "x-amz-copy-source", "bucket-a/lock", "x-amz-copy-source-if-modified-since", future), 412, "PreconditionFailed")
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/copy", nil, "x-amz-copy-source", "bucket-a/lock", "x-amz-copy-source-if-none-match", `"`+strings.Trim(w.s3OK(w.s3(w.aRead, "HEAD", "/v1/s3/bucket-a/lock", nil), 200).header.Get("ETag"), `"`)+`"`), 412, "PreconditionFailed")
}

func TestS3BucketSettingsArentCreateBucket(t *testing.T) {
	w := newS3World(t, nil)
	for _, q := range []string{"versioning", "location", "tagging", "acl", "uploads"} {
		r := w.s3(w.admin, "PUT", "/v1/s3/bucket-new?"+q, []byte("<x/>"))
		if r.status != 501 {
			t.Fatalf("PUT ?%s: %d %s", q, r.status, r.body)
		}
	}
	w.s3Fails(w.s3(w.admin, "HEAD", "/v1/s3/bucket-new", nil), 404, "")
}

func TestS3RevokedAndUnknownKeysLookTheSame(t *testing.T) {
	w := newS3World(t, nil)
	w.ok(w.do(w.admin.Token, "DELETE", "/v1/tokens/{token_id}", "/v1/tokens/"+w.aRead.ID, nil), 204)
	revoked := w.s3(w.aRead, "GET", "/v1/s3/bucket-a/doc.txt", nil)
	unknown := w.aRead
	unknown.AKID = "LMB" + strings.Repeat("Q", 20)
	missing := w.s3(unknown, "GET", "/v1/s3/bucket-a/doc.txt", nil)
	msg := func(r resp) string {
		i, j := bytes.Index(r.body, []byte("<Message>")), bytes.Index(r.body, []byte("</Message>"))
		return string(r.body[i:j])
	}
	if s3Code(revoked) != "InvalidAccessKeyId" || msg(revoked) != msg(missing) {
		t.Fatalf("revoked %s, unknown %s", revoked.body, missing.body)
	}
}

func TestS3BearerPayloads(t *testing.T) {
	w := newS3World(t, nil)
	send := func(body []byte, hdr ...string) resp {
		req, _ := http.NewRequest("PUT", w.http.URL+"/v1/s3/bucket-a/bearer", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+w.aWrite.Token)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		return w.send(req)
	}
	data := "unsigned chunks"
	crc := crc32.ChecksumIEEE([]byte(data))
	sum := base64.StdEncoding.EncodeToString([]byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)})
	chunked := fmt.Sprintf("%x\r\n%s\r\n0\r\nx-amz-checksum-crc32:%s\r\n\r\n", len(data), data, sum)
	w.s3OK(send([]byte(chunked), "x-amz-content-sha256", s3proto.StreamingUnsignedTrail, "x-amz-decoded-content-length", fmt.Sprint(len(data)),
		"x-amz-trailer", "x-amz-checksum-crc32", "Content-Encoding", "aws-chunked"), 200)
	if r := w.s3OK(w.s3(w.aRead, "GET", "/v1/s3/bucket-a/bearer", nil), 200); string(r.body) != data {
		t.Fatalf("bearer chunked upload stored %q", r.body)
	}
	w.s3Fails(send([]byte("evil"), "x-amz-content-sha256", sha256hex([]byte("good"))), 400, "XAmzContentSHA256Mismatch")
	w.s3Fails(send([]byte(chunked), "x-amz-content-sha256", s3proto.StreamingSigned, "x-amz-decoded-content-length", fmt.Sprint(len(data))), 400, "InvalidRequest")
}

func TestS3ChecksumsAndShortBodiesStoreNothing(t *testing.T) {
	w := newS3World(t, nil)
	tmp := w.srv.Engine.Layout().TmpDir()
	empty := func(name string) {
		t.Helper()
		if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
			t.Fatalf("%s left files in tmp/: %v", name, entries)
		}
		w.s3Fails(w.s3(w.admin, "HEAD", "/v1/s3/bucket-a/"+name, nil), 404, "")
	}
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/crc", []byte("data"), "x-amz-checksum-crc32", "AAAAAA=="), 400, "BadDigest")
	empty("crc")
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/md5", []byte("data"), "Content-MD5", base64.StdEncoding.EncodeToString(make([]byte, 16))), 400, "BadDigest")
	empty("md5")
	w.s3Fails(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/badmd5", []byte("data"), "Content-MD5", "nope"), 400, "InvalidDigest")
	empty("badmd5")
	req := w.s3Request(w.aWrite, time.Now(), "PUT", "/v1/s3/bucket-a/short", []byte("abc"), "x-amz-content-sha256", s3proto.UnsignedPayload)
	req.ContentLength = 10
	req.Body = &shortBody{r: strings.NewReader("abc")}
	if res, err := http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
	}
	time.Sleep(100 * time.Millisecond) // the handler sees the cut body after the client gives up
	empty("short")
}

type shortBody struct{ r *strings.Reader }

func (b *shortBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil {
		return n, fmt.Errorf("client went away")
	}
	return n, nil
}
func (b *shortBody) Close() error { return nil }

func TestS3ServerPresignMaximum(t *testing.T) {
	w := newS3World(t, func(c *config.Config) { c.PresignMaxExpiry = time.Hour })
	w.s3OK(w.fetch("GET", w.presign(w.aRead, "GET", "/v1/s3/bucket-a/doc.txt", 30*time.Minute, time.Now()), nil), 200)
	w.s3Fails(w.fetch("GET", w.presign(w.aRead, "GET", "/v1/s3/bucket-a/doc.txt", 2*time.Hour, time.Now()), nil), 400, "AuthorizationQueryParametersError")
	k := w.accessKey(map[string]any{"bucket": "bucket-a", "access": "read"})
	w.fails(w.do(k.Token, "POST", "/v1/buckets/{bucket}/presign", "/v1/buckets/bucket-a/presign", map[string]any{"key": "doc.txt", "expires_in": 7200}), 400, "bad_request")
}

func TestPresignedURLsKeepDotSegments(t *testing.T) {
	w := newS3World(t, nil)
	for _, key := range []string{"a/../b", "./c", "d/./e", ".."} {
		w.s3OK(w.s3(w.aWrite, "PUT", "/v1/s3/bucket-a/"+s3proto.URIEncode(key, true), []byte("v:"+key)), 200)
		k := w.accessKey(map[string]any{"bucket": "bucket-a", "access": "read"})
		ps := w.ok(w.do(k.Token, "POST", "/v1/buckets/{bucket}/presign", "/v1/buckets/bucket-a/presign", map[string]any{"key": key}), 200).json(t)
		u, _ := url.Parse(ps["url"].(string))
		// What a browser or curl would do: resolve dot segments.
		resolved := u.ResolveReference(&url.URL{}).String()
		r := w.fetch("GET", resolved, nil)
		if r.status != 200 || string(r.body) != "v:"+key {
			t.Fatalf("%q: %s -> %d %s", key, resolved, r.status, r.body)
		}
	}
}
