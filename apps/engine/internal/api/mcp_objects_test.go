package api_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMCPObjectTools(t *testing.T) {
	e := newEnv(t, nil)
	admin := e.accessKey(map[string]any{"access": "admin"})
	s := e.mcp(admin.Token)
	if _, msg := call(t, s, "create_bucket", map[string]any{"name": "notes"}); msg != "" {
		t.Fatal(msg)
	}
	if _, msg := call(t, s, "create_bucket", map[string]any{"name": "other"}); msg != "" {
		t.Fatal(msg)
	}
	out, msg := call(t, s, "put_object", map[string]any{"bucket": "notes", "key": "a/hello.md", "text": "# Hello", "content_type": "text/markdown"})
	if msg != "" || out["size"].(float64) != 7 {
		t.Fatalf("put: %v %s", out, msg)
	}
	bin := base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 255})
	if _, msg := call(t, s, "put_object", map[string]any{"bucket": "notes", "key": "a/blob.bin", "content_base64": bin}); msg != "" {
		t.Fatal(msg)
	}
	long := strings.Repeat("é", 40000) // 80000 bytes, past the 64 KB cap
	if _, msg := call(t, s, "put_object", map[string]any{"bucket": "notes", "key": "long.txt", "text": long}); msg != "" {
		t.Fatal(msg)
	}
	if _, msg := call(t, s, "put_object", map[string]any{"bucket": "notes", "key": "x", "text": "a", "content_base64": bin}); !strings.Contains(msg, "bad_request") {
		t.Fatalf("text and base64 together: %s", msg)
	}

	list, msg := call(t, s, "list_objects", map[string]any{"bucket": "notes", "delimiter": "/"})
	if msg != "" || len(list["objects"].([]any)) != 1 || list["prefixes"].([]any)[0] != "a/" {
		t.Fatalf("list: %v %s", list, msg)
	}
	got, msg := call(t, s, "get_object", map[string]any{"bucket": "notes", "key": "a/hello.md"})
	if msg != "" || got["text"] != "# Hello" || got["binary"] != false {
		t.Fatalf("get text: %v %s", got, msg)
	}
	got, _ = call(t, s, "get_object", map[string]any{"bucket": "notes", "key": "a/blob.bin"})
	if got["binary"] != true || got["text"] != nil {
		t.Fatalf("get binary: %v", got)
	}
	got, _ = call(t, s, "get_object", map[string]any{"bucket": "notes", "key": "long.txt"})
	text, _ := got["text"].(string)
	if got["truncated"] != true || len(text) > 64<<10 || !strings.HasPrefix(long, text) || !strings.Contains(got["note"].(string), "offset") {
		t.Fatalf("get long: truncated=%v len=%d", got["truncated"], len(text))
	}

	link, msg := call(t, s, "presign_object", map[string]any{"bucket": "notes", "key": "a/hello.md", "expires_in_seconds": 60})
	if msg != "" {
		t.Fatal(msg)
	}
	res, err := http.Get(link["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != "# Hello" {
		t.Fatalf("presigned GET: %d %s", res.StatusCode, body)
	}

	del, msg := call(t, s, "delete_object", map[string]any{"bucket": "notes", "key": "a/hello.md"})
	if msg != "" || del["deleted"] != true {
		t.Fatalf("delete: %v %s", del, msg)
	}
	if _, msg := call(t, s, "get_object", map[string]any{"bucket": "notes", "key": "a/hello.md"}); !strings.Contains(msg, "object_not_found") {
		t.Fatalf("deleted object: %s", msg)
	}

	// A read token for one bucket: no other bucket, no writes, no presign without an access key.
	r := e.ok(e.do(admin.Token, "POST", "/v1/tokens", "/v1/tokens", map[string]any{"name": "r", "bucket": "notes", "access": "read"}), 201)
	rs := e.mcp(r.json(t)["token"].(string))
	buckets, _ := call(t, rs, "list_buckets", map[string]any{})
	if bs := buckets["buckets"].([]any); len(bs) != 1 || bs[0].(map[string]any)["name"] != "notes" {
		t.Fatalf("read token buckets: %v", buckets)
	}
	for name, args := range map[string]map[string]any{
		"list_objects":   {"bucket": "other"},
		"get_object":     {"bucket": "other", "key": "x"},
		"put_object":     {"bucket": "other", "key": "x", "text": "x"},
		"delete_object":  {"bucket": "other", "key": "x"},
		"presign_object": {"bucket": "other", "key": "x"},
	} {
		if _, msg := call(t, rs, name, args); !strings.Contains(msg, "bucket_not_found") {
			t.Errorf("%s on another bucket: %q", name, msg)
		}
	}
	if _, msg := call(t, rs, "put_object", map[string]any{"bucket": "notes", "key": "x", "text": "x"}); !strings.Contains(msg, "forbidden") {
		t.Errorf("read token put: %q", msg)
	}
	if _, msg := call(t, rs, "presign_object", map[string]any{"bucket": "notes", "key": "long.txt"}); !strings.Contains(msg, "no_access_key") {
		t.Errorf("presign without an access key: %q", msg)
	}
	if _, msg := call(t, rs, "presign_object", map[string]any{"bucket": "notes", "key": "long.txt", "method": "PUT"}); !strings.Contains(msg, "forbidden") {
		t.Errorf("read token presign PUT: %q", msg)
	}
	if _, msg := call(t, rs, "create_bucket", map[string]any{"name": "evil"}); !strings.Contains(msg, "forbidden") {
		t.Errorf("read token create bucket: %q", msg)
	}
	if _, msg := call(t, rs, "query", map[string]any{"database": "app", "sql": "SELECT 1"}); !strings.Contains(msg, "database_not_found") {
		t.Errorf("bucket token query: %q", msg)
	}
}
