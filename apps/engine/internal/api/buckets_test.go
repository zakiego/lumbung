package api_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// putObject uploads through REST and returns the response.
func (e *env) putObject(token, bucket, key, body string, hdr ...string) resp {
	e.t.Helper()
	h := append([]string{"Content-Type", "text/plain"}, hdr...)
	return e.do(token, "PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/"+bucket+"/object?key="+url.QueryEscape(key), []byte(body), h...)
}

func TestRESTBucketsAndObjects(t *testing.T) {
	e := newEnv(t, nil)
	b := e.ok(e.do(e.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "photos"}), 201).json(t)
	if b["name"] != "photos" || b["public_read"] != false {
		t.Fatalf("create: %v", b)
	}
	e.fails(e.do(e.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "photos"}), 409, "name_taken")
	e.fails(e.do(e.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "ab"}), 422, "invalid_request")

	o := e.ok(e.putObject(e.admin, "photos", "a b/c.txt", "hello world", "x-amz-meta-owner", "ana", "Cache-Control", "no-cache"), 200).json(t)
	if o["key"] != "a b/c.txt" || o["size"].(float64) != 11 || o["etag"] != "5eb63bbbe01eeed093cb22bb8f5acdc3" || o["content_type"] != "text/plain" {
		t.Fatalf("put: %v", o)
	}
	path := "/v1/buckets/photos/object?key=" + url.QueryEscape("a b/c.txt")
	r := e.ok(e.do(e.admin, "GET", "", path, nil), 200)
	if string(r.body) != "hello world" || r.header.Get("X-Amz-Meta-Owner") != "ana" || r.header.Get("Cache-Control") != "no-cache" || r.header.Get("ETag") != `"5eb63bbbe01eeed093cb22bb8f5acdc3"` {
		t.Fatalf("get: %s %v", r.body, r.header)
	}
	r = e.ok(e.do(e.admin, "GET", "", path, nil, "Range", "bytes=6-"), 206)
	if string(r.body) != "world" || r.header.Get("Content-Range") != "bytes 6-10/11" {
		t.Fatalf("range: %q %v", r.body, r.header)
	}
	e.ok(e.do(e.admin, "GET", "", path, nil, "If-None-Match", `"5eb63bbbe01eeed093cb22bb8f5acdc3"`), 304)
	e.fails(e.do(e.admin, "GET", "", path, nil, "Range", "bytes=50-"), 416, "invalid_range")
	head := e.ok(e.do(e.admin, "HEAD", "", path, nil), 200)
	if head.header.Get("Content-Length") != "11" || len(head.body) != 0 {
		t.Fatalf("head: %v", head.header)
	}
	e.fails(e.do(e.admin, "GET", "", "/v1/buckets/photos/object?key=nope", nil), 404, "object_not_found")

	e.ok(e.putObject(e.admin, "photos", "a b/d.txt", "x"), 200)
	e.ok(e.putObject(e.admin, "photos", "top.txt", "x"), 200)
	l := e.ok(e.do(e.admin, "GET", "/v1/buckets/{bucket}/objects", "/v1/buckets/photos/objects?delimiter=/&limit=1", nil), 200).json(t)
	if len(l["data"].([]any)) != 0 || l["prefixes"].([]any)[0] != "a b/" || l["next_cursor"] == "" {
		t.Fatalf("list page 1: %v", l)
	}
	l = e.ok(e.do(e.admin, "GET", "/v1/buckets/{bucket}/objects", "/v1/buckets/photos/objects?delimiter=/&limit=1&cursor="+l["next_cursor"].(string), nil), 200).json(t)
	if l["data"].([]any)[0].(map[string]any)["key"] != "top.txt" || l["next_cursor"] != "" {
		t.Fatalf("list page 2: %v", l)
	}

	e.ok(e.do(e.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "backups"}), 201)
	cp := e.ok(e.do(e.admin, "POST", "/v1/buckets/{bucket}/copy", "/v1/buckets/backups/copy", map[string]any{"from_bucket": "photos", "from_key": "a b/c.txt", "key": "c.txt"}), 200).json(t)
	if cp["etag"] != "5eb63bbbe01eeed093cb22bb8f5acdc3" || cp["metadata"].(map[string]any)["owner"] != "ana" {
		t.Fatalf("copy: %v", cp)
	}
	upd := e.ok(e.do(e.admin, "PATCH", "/v1/buckets/{bucket}", "/v1/buckets/backups", map[string]any{"public_read": true, "cors_origins": []string{"https://app.example"}}), 200).json(t)
	if upd["public_read"] != true || upd["cors_origins"].([]any)[0] != "https://app.example" {
		t.Fatalf("update: %v", upd)
	}
	e.fails(e.do(e.admin, "PATCH", "/v1/buckets/{bucket}", "/v1/buckets/backups", map[string]any{"cors_origins": []string{"not an origin"}}), 400, "bad_request")
	res, err := http.Get(e.http.URL + "/v1/s3/backups/c.txt")
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(pub) != "hello world" {
		t.Fatalf("public read after PATCH: %d %s", res.StatusCode, pub)
	}

	e.fails(e.do(e.admin, "DELETE", "/v1/buckets/{bucket}", "/v1/buckets/backups", nil), 409, "bucket_not_empty")
	e.ok(e.do(e.admin, "DELETE", "/v1/buckets/{bucket}/object", "/v1/buckets/backups/object?key=c.txt", nil), 204)
	e.ok(e.do(e.admin, "DELETE", "/v1/buckets/{bucket}/object", "/v1/buckets/backups/object?key=c.txt", nil), 204)
	e.ok(e.do(e.admin, "DELETE", "/v1/buckets/{bucket}", "/v1/buckets/backups", nil), 204)
	e.fails(e.do(e.admin, "GET", "/v1/buckets/{bucket}", "/v1/buckets/backups", nil), 404, "bucket_not_found")

	// Uploads need a length; a key can't be empty.
	req, _ := http.NewRequest("PUT", e.http.URL+"/v1/buckets/photos/object?key=chunked", io.NopCloser(strings.NewReader("abc")))
	req.Header.Set("Authorization", "Bearer "+e.admin)
	req.ContentLength = -1
	if r := e.send(req); r.status != 411 || r.code(t) != "length_required" {
		t.Fatalf("chunked upload: %d %s", r.status, r.body)
	}
	e.fails(e.do(e.admin, "PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/photos/object?key=", []byte("x")), 422, "invalid_request")

	// Presign needs an access key on the calling token.
	e.fails(e.do(e.admin, "POST", "/v1/buckets/{bucket}/presign", "/v1/buckets/photos/presign", map[string]any{"key": "top.txt"}), 400, "no_access_key")
	k := e.accessKey(map[string]any{"bucket": "photos", "access": "write"})
	ps := e.ok(e.do(k.Token, "POST", "/v1/buckets/{bucket}/presign", "/v1/buckets/photos/presign", map[string]any{"key": "new.txt", "method": "PUT", "expires_in": 60}), 200).json(t)
	put, _ := http.NewRequest("PUT", ps["url"].(string), strings.NewReader("via presigned"))
	if r := e.send(put); r.status != 200 {
		t.Fatalf("presigned PUT: %d %s", r.status, r.body)
	}
	r = e.ok(e.do(k.Token, "GET", "", "/v1/buckets/photos/object?key=new.txt", nil), 200)
	if string(r.body) != "via presigned" {
		t.Fatalf("presigned upload content: %q", r.body)
	}
	e.fails(e.do(k.Token, "POST", "/v1/buckets/{bucket}/presign", "/v1/buckets/photos/presign", map[string]any{"key": "x", "expires_in": 700000}), 422, "invalid_request")
}

// PLAN 16.5 through REST: the same isolation rows as the S3 API.
func TestRESTBucketIsolation(t *testing.T) {
	w := newS3World(t, nil)
	type op struct {
		method, template, path string
		body                   any
	}
	ops := func(bucket string) []op {
		return []op{
			{"GET", "/v1/buckets/{bucket}", "/v1/buckets/" + bucket, nil},
			{"PATCH", "/v1/buckets/{bucket}", "/v1/buckets/" + bucket, map[string]any{"public_read": true}},
			{"DELETE", "/v1/buckets/{bucket}", "/v1/buckets/" + bucket, nil},
			{"GET", "/v1/buckets/{bucket}/objects", "/v1/buckets/" + bucket + "/objects", nil},
			{"GET", "/v1/buckets/{bucket}/object", "/v1/buckets/" + bucket + "/object?key=doc.txt", nil},
			{"PUT", "/v1/buckets/{bucket}/object", "/v1/buckets/" + bucket + "/object?key=new.txt", []byte("x")},
			{"DELETE", "/v1/buckets/{bucket}/object", "/v1/buckets/" + bucket + "/object?key=doc.txt", nil},
			{"POST", "/v1/buckets/{bucket}/copy", "/v1/buckets/" + bucket + "/copy", map[string]any{"from_key": "doc.txt", "key": "c.txt"}},
			{"POST", "/v1/buckets/{bucket}/presign", "/v1/buckets/" + bucket + "/presign", map[string]any{"key": "doc.txt"}},
		}
	}
	// A token for bucket A, and a database token, on bucket B: not found,
	// the same as a bucket that doesn't exist.
	for _, k := range []key{w.aAdmin, w.aWrite, w.aRead, w.dbAdmin} {
		for _, o := range ops("bucket-b") {
			r := w.do(k.Token, o.method, o.template, o.path, o.body)
			if r.status != 404 || r.code(t) != "bucket_not_found" {
				t.Fatalf("%s %s with another scope: %d %s", o.method, o.path, r.status, r.body)
			}
		}
	}
	// Copy across buckets needs read on the source in the same token.
	w.fails(w.do(w.aWrite.Token, "POST", "/v1/buckets/{bucket}/copy", "/v1/buckets/bucket-a/copy", map[string]any{"from_bucket": "bucket-b", "from_key": "doc.txt", "key": "x"}), 404, "bucket_not_found")
	// A read token can't write, and a write token can't change settings or delete the bucket.
	for _, o := range ops("bucket-a")[1:] {
		if o.method == "GET" || (o.method == "POST" && strings.HasSuffix(o.path, "presign")) {
			continue
		}
		w.fails(w.do(w.aRead.Token, o.method, o.template, o.path, o.body), 403, "forbidden")
	}
	w.fails(w.do(w.aRead.Token, "POST", "/v1/buckets/{bucket}/presign", "/v1/buckets/bucket-a/presign", map[string]any{"key": "x", "method": "PUT"}), 403, "forbidden")
	w.fails(w.do(w.aWrite.Token, "PATCH", "/v1/buckets/{bucket}", "/v1/buckets/bucket-a", map[string]any{"public_read": true}), 403, "forbidden")
	w.fails(w.do(w.aAdmin.Token, "DELETE", "/v1/buckets/{bucket}", "/v1/buckets/bucket-a", nil), 403, "forbidden")
	w.fails(w.do(w.aAdmin.Token, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "bucket-new"}), 403, "forbidden")
	// No token at all.
	w.fails(w.do("", "GET", "", "/v1/buckets/bucket-a/object?key=doc.txt", nil), 401, "unauthorized")
	// Lists only show what the token can see.
	list := w.ok(w.do(w.aRead.Token, "GET", "/v1/buckets", "/v1/buckets", nil), 200).json(t)["data"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["name"] != "bucket-a" {
		t.Fatalf("bucket token lists %v", list)
	}
	if list := w.ok(w.do(w.dbAdmin.Token, "GET", "/v1/buckets", "/v1/buckets", nil), 200).json(t)["data"].([]any); len(list) != 0 {
		t.Fatalf("database token lists buckets %v", list)
	}
	// Bucket B is untouched.
	r := w.ok(w.do(w.admin.Token, "GET", "", "/v1/buckets/bucket-b/object?key=doc.txt", nil), 200)
	if string(r.body) != "secret of bucket-b" {
		t.Fatalf("bucket-b changed: %q", r.body)
	}
}
