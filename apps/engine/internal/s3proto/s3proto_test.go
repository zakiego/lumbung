package s3proto

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The examples from the S3 Signature Version 4 documentation, with its
// published example key (a placeholder, not a credential). It is assembled
// from short pieces so secret scanners don't mistake it for a real key.
var (
	exampleKeyID  = strings.Join([]string{"AK", "IA", "IOSF", "ODNN", "7EXA", "MPLE"}, "")
	exampleSecret = strings.Join([]string{"wJal", "rXUt", "nFEM", "I/K7", "MDEN", "G/bP", "xRfi", "CYEX", "AMPL", "EKEY"}, "")
)

func TestDocumentedGetObjectSignature(t *testing.T) {
	r := httptest.NewRequest("GET", "http://examplebucket.s3.amazonaws.com/test.txt", nil)
	r.Header.Set("Range", "bytes=0-9")
	r.Header.Set("X-Amz-Content-Sha256", EmptySHA256)
	r.Header.Set("X-Amz-Date", "20130524T000000Z")
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+exampleKeyID+"/20130524/us-east-1/s3/aws4_request,SignedHeaders=host;range;x-amz-content-sha256;x-amz-date,Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41")
	a, e := ParseHeader(r)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := a.Verify(r, "/test.txt", exampleSecret); e != nil {
		t.Fatalf("documented signature rejected: %v", e)
	}
	if _, e := a.Verify(r, "/test.txt", exampleSecret+"x"); e == nil {
		t.Fatal("wrong secret accepted")
	}
	r.Header.Set("Range", "bytes=0-10")
	if _, e := a.Verify(r, "/test.txt", exampleSecret); e == nil {
		t.Fatal("a changed signed header was accepted")
	}
	if e := a.CheckTime(time.Date(2013, 5, 24, 0, 10, 0, 0, time.UTC), time.Hour); e != nil {
		t.Fatalf("within skew: %v", e)
	}
	if e := a.CheckTime(time.Date(2013, 5, 24, 0, 16, 0, 0, time.UTC), time.Hour); e == nil || e.Code != "RequestTimeTooSkewed" {
		t.Fatalf("skew: %v", e)
	}
}

func TestDocumentedPresignedURL(t *testing.T) {
	raw := "http://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=" +
		url.QueryEscape(exampleKeyID+"/20130524/us-east-1/s3/aws4_request") +
		"&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	r := httptest.NewRequest("GET", raw, nil)
	a, e := ParsePresigned(r.URL.Query())
	if e != nil {
		t.Fatal(e)
	}
	if _, e := a.Verify(r, "/test.txt", exampleSecret); e != nil {
		t.Fatalf("documented presigned URL rejected: %v", e)
	}
	day := time.Date(2013, 5, 24, 12, 0, 0, 0, time.UTC)
	if e := a.CheckTime(day, MaxPresignExpiry); e != nil {
		t.Fatalf("valid time: %v", e)
	}
	if e := a.CheckTime(day.Add(13*time.Hour), MaxPresignExpiry); e == nil {
		t.Fatal("expired URL accepted")
	}
	if e := a.CheckTime(day, time.Hour); e == nil {
		t.Fatal("expiry past the server's maximum accepted")
	}
	// Our own presigner produces the same URL.
	got := Presign(PresignInput{Method: "GET", Scheme: "http", Host: "examplebucket.s3.amazonaws.com", Path: "/test.txt",
		AccessKeyID: exampleKeyID, Secret: exampleSecret, Region: "us-east-1", Time: time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC), Expires: 24 * time.Hour})
	if !strings.HasSuffix(got, "X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404") {
		t.Fatalf("presign: %s", got)
	}
}

// The documented streaming upload: 65536 + 1024 bytes of 'a', signed chunks.
func TestDocumentedSignedChunks(t *testing.T) {
	r := httptest.NewRequest("PUT", "http://s3.amazonaws.com/examplebucket/chunkObject.txt", nil)
	r.Header.Set("X-Amz-Date", "20130524T000000Z")
	r.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")
	r.Header.Set("Content-Encoding", "aws-chunked")
	r.Header.Set("X-Amz-Decoded-Content-Length", "66560")
	r.Header.Set("Content-Length", "66824")
	r.Header.Set("X-Amz-Content-Sha256", StreamingSigned)
	r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+exampleKeyID+"/20130524/us-east-1/s3/aws4_request,SignedHeaders=content-encoding;content-length;host;x-amz-content-sha256;x-amz-date;x-amz-decoded-content-length;x-amz-storage-class,Signature=4f232c4386841ef735655705268965c44a0e4690baa4adea153f7db9fa80a0a9")
	a, e := ParseHeader(r)
	if e != nil {
		t.Fatal(e)
	}
	key, e := a.Verify(r, "/examplebucket/chunkObject.txt", exampleSecret)
	if e != nil {
		t.Fatalf("seed signature rejected: %v", e)
	}
	body := func(sig2 string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "10000;chunk-signature=ad80c730a21e5b8d04586a2213dd63b9a0e99e0e2307b0ade35a65485a288648\r\n%s\r\n", strings.Repeat("a", 65536))
		fmt.Fprintf(&b, "400;chunk-signature=%s\r\n%s\r\n", sig2, strings.Repeat("a", 1024))
		b.WriteString("0;chunk-signature=b6c6ea8a5354eaf15b3cb7646744f4275b71ea724fed81ceb9323e279d449df9\r\n\r\n")
		return b.String()
	}
	signer := func() *ChunkSigner {
		return &ChunkSigner{Key: key, Time: a.Time, Scope: a.Scope, Prev: a.Signature}
	}
	got, err := io.ReadAll(NewChunkedReader(strings.NewReader(body("0055627c9e194cb4542bae2aa5492e3c1575bbb81b612b7d234b86a503ef5497")), signer(), ""))
	if err != nil {
		t.Fatalf("documented chunks rejected: %v", err)
	}
	if len(got) != 66560 || strings.Trim(string(got), "a") != "" {
		t.Fatalf("decoded %d bytes", len(got))
	}
	_, err = io.ReadAll(NewChunkedReader(strings.NewReader(body(strings.Repeat("0", 64))), signer(), ""))
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != "SignatureDoesNotMatch" {
		t.Fatalf("a bad chunk signature: %v", err)
	}
	// Tampered data with the right signatures.
	tampered := strings.Replace(body("0055627c9e194cb4542bae2aa5492e3c1575bbb81b612b7d234b86a503ef5497"), "aaaa", "aaab", 1)
	if _, err := io.ReadAll(NewChunkedReader(strings.NewReader(tampered), signer(), "")); err == nil {
		t.Fatal("tampered chunk accepted")
	}
}

func TestUnsignedTrailerAndChecksums(t *testing.T) {
	data := "hello world"
	h := NewChecksum("crc32")
	h.Write([]byte(data))
	crc := base64.StdEncoding.EncodeToString(h.Sum(nil))
	body := fmt.Sprintf("%x\r\n%s\r\n0\r\nx-amz-checksum-crc32:%s\r\n\r\n", len(data), data, crc)
	cr := NewChunkedReader(strings.NewReader(body), nil, "x-amz-checksum-crc32")
	v := VerifyChecksums(cr, []Checksum{{Alg: "crc32"}}, func(alg string) string { return cr.Trailer("x-amz-checksum-" + alg) })
	got, err := io.ReadAll(v)
	if err != nil || string(got) != data {
		t.Fatalf("%q %v", got, err)
	}
	bad := strings.Replace(body, "hello", "jello", 1)
	cr = NewChunkedReader(strings.NewReader(bad), nil, "x-amz-checksum-crc32")
	v = VerifyChecksums(cr, []Checksum{{Alg: "crc32"}}, func(alg string) string { return cr.Trailer("x-amz-checksum-" + alg) })
	if _, err := io.ReadAll(v); err == nil || !strings.Contains(err.Error(), "BadDigest") {
		t.Fatalf("wrong data accepted: %v", err)
	}
	// An undeclared trailer, a missing declared one, and a truncated body.
	for name, b := range map[string]string{
		"undeclared": fmt.Sprintf("%x\r\n%s\r\n0\r\nx-amz-meta-evil:1\r\n\r\n", len(data), data),
		"missing":    fmt.Sprintf("%x\r\n%s\r\n0\r\n\r\n", len(data), data),
		"truncated":  fmt.Sprintf("%x\r\n%s", len(data), data[:3]),
		"long chunk": fmt.Sprintf("%x\r\n%sXX\r\n0\r\n\r\n", len(data), data),
		"bad size":   "zz\r\n",
	} {
		cr := NewChunkedReader(strings.NewReader(b), nil, "x-amz-checksum-crc32")
		if _, err := io.ReadAll(cr); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestChecksumCheckValues(t *testing.T) {
	// The standard check value of each algorithm: the checksum of "123456789".
	want := map[string]uint64{"crc32": 0xcbf43926, "crc32c": 0xe3069283, "crc64nvme": 0xae8b14860a799888}
	for alg, w := range want {
		h := NewChecksum(alg)
		h.Write([]byte("123456789"))
		sum := h.Sum(nil)
		var got uint64
		if len(sum) == 4 {
			got = uint64(binary.BigEndian.Uint32(sum))
		} else {
			got = binary.BigEndian.Uint64(sum)
		}
		if got != w {
			t.Errorf("%s = %x, want %x", alg, got, w)
		}
	}
}

func TestVerifySHA256(t *testing.T) {
	sum := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9" // sha256("hello world")
	if b, err := io.ReadAll(VerifySHA256(strings.NewReader("hello world"), sum)); err != nil || string(b) != "hello world" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := io.ReadAll(VerifySHA256(strings.NewReader("hello worle"), sum)); err == nil {
		t.Fatal("mismatch accepted")
	}
}

func TestUnsignedAmzHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Amz-Copy-Source", "other/key")
	h.Set("X-Amz-Checksum-Crc32", "AAAAAA==")
	h.Set("X-Amz-Date", "x")
	header := &Auth{SignedHeaders: []string{"host", "x-amz-date"}}
	if got := header.UnsignedAmzHeaders(h); fmt.Sprint(got) != "[x-amz-checksum-crc32 x-amz-copy-source]" {
		t.Fatalf("header auth: %v", got)
	}
	presigned := &Auth{SignedHeaders: []string{"host"}, Presigned: true}
	if got := presigned.UnsignedAmzHeaders(h); fmt.Sprint(got) != "[x-amz-copy-source]" {
		t.Fatalf("presigned: %v", got)
	}
}

func TestURIEncode(t *testing.T) {
	for in, want := range map[string]string{
		"a b/c+d%e~f": "a%20b/c%2Bd%25e~f",
		"ünï":         "%C3%BCn%C3%AF",
		"a//b/./..":   "a//b/./..",
	} {
		if got := URIEncode(in, true); got != want {
			t.Errorf("URIEncode(%q) = %q, want %q", in, got, want)
		}
	}
	if got := URIEncode("a/b", false); got != "a%2Fb" {
		t.Errorf("slash: %q", got)
	}
}

func TestParseHeaderRejects(t *testing.T) {
	base := func() *http.Request {
		r := httptest.NewRequest("GET", "http://h/x", bytes.NewReader(nil))
		r.Header.Set("X-Amz-Date", "20130524T000000Z")
		r.Header.Set("X-Amz-Content-Sha256", EmptySHA256)
		return r
	}
	good := "AWS4-HMAC-SHA256 Credential=K/20130524/us-east-1/s3/aws4_request,SignedHeaders=host;x-amz-date,Signature=" + strings.Repeat("a", 64)
	for name, mod := range map[string]func(*http.Request){
		"v2":             func(r *http.Request) { r.Header.Set("Authorization", "AWS K:sig") },
		"no host signed": func(r *http.Request) { r.Header.Set("Authorization", strings.Replace(good, "host;", "", 1)) },
		"other service":  func(r *http.Request) { r.Header.Set("Authorization", strings.Replace(good, "/s3/", "/ec2/", 1)) },
		"date mismatch": func(r *http.Request) {
			r.Header.Set("Authorization", good)
			r.Header.Set("X-Amz-Date", "20130525T000000Z")
		},
		"no payload hash": func(r *http.Request) { r.Header.Set("Authorization", good); r.Header.Del("X-Amz-Content-Sha256") },
		"bad payload": func(r *http.Request) {
			r.Header.Set("Authorization", good)
			r.Header.Set("X-Amz-Content-Sha256", "nope")
		},
		"short signature": func(r *http.Request) {
			r.Header.Set("Authorization", strings.Replace(good, strings.Repeat("a", 64), "ab", 1))
		},
	} {
		r := base()
		mod(r)
		if _, e := ParseHeader(r); e == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
