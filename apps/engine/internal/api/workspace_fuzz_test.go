package api_test

// PLAN 17.9: requests with one workspace's token must never read or change
// anything of another workspace, whatever the method, path, body or header.
//
// CI runs it for a bounded time:
//
//	go test -run '^$' -fuzz '^FuzzWorkspaceRequests$' -fuzztime 60s ./internal/api
//
// Without -fuzz, go test runs the seed corpus below as a normal test.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// state is everything a workspace holds that its own admin can see: its
// databases with their branches and rows, its buckets with their objects,
// and its tokens. Two equal states mean nothing in it was created, changed
// or removed.
func (w *wsWorld) state(base, token string, dbs, buckets []string) string {
	// Plain HTTP calls: this runs inside the fuzz function, where the
	// helpers bound to the outer test can't be used.
	var out []string
	call := func(method, path, body string) string {
		req, _ := http.NewRequest(method, w.http.URL+base+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return path + " " + err.Error()
		}
		defer res.Body.Close()
		got, _ := io.ReadAll(res.Body)
		return fmt.Sprintf("%s %d %s", path, res.StatusCode, got)
	}
	get := func(path string) string { return call("GET", path, "") }
	post := func(path, body string) string { return call("POST", path, body) }
	// Lists carry ids, names, counts and sizes. last_used_at and
	// last_activity_at move when the owner itself reads, so the token list
	// is reduced to ids and the database list is read once per database.
	out = append(out, get("/buckets"))
	toks := get("/tokens?limit=200")
	var ids []string
	for _, part := range strings.Split(toks, `"id":"`)[1:] {
		ids = append(ids, part[:strings.Index(part, `"`)])
	}
	sort.Strings(ids)
	out = append(out, "tokens "+strings.Join(ids, ","))
	for _, db := range dbs {
		out = append(out, get("/databases/"+db+"/branches"))
		out = append(out, post("/databases/"+db+"/query", `{"sql":"SELECT * FROM t ORDER BY 1","read_only":true}`))
		out = append(out, post("/databases/"+db+"/query", `{"sql":"SELECT type, name, sql FROM sqlite_schema ORDER BY 1, 2","read_only":true}`))
	}
	for _, b := range buckets {
		out = append(out, get("/buckets/"+b+"/objects"))
		out = append(out, get("/buckets/"+b+"/object?key=doc.txt"))
	}
	names := get("/databases")
	var dbNames []string
	for _, part := range strings.Split(names, `"name":"`)[1:] {
		dbNames = append(dbNames, part[:strings.Index(part, `"`)])
	}
	out = append(out, "databases "+strings.Join(dbNames, ","))
	return strings.Join(out, "\n")
}

func randomMarker(t testing.TB) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "marker-" + hex.EncodeToString(b)
}

func FuzzWorkspaceRequests(f *testing.F) {
	w := newWSWorld(f, nil)
	a, b := w.a, w.b
	// The default workspace holds the same names too, and both victims hold
	// a marker no request of A's could know: if it shows up in a response,
	// something leaked.
	w.createDB("notes")
	w.ok(w.sql(w.admin, "notes", "CREATE TABLE t(v)"), 200)
	w.ok(w.do(w.admin, "POST", "/v1/buckets", "/v1/buckets", map[string]any{"name": "files"}), 201)
	markers := []string{randomMarker(f), randomMarker(f)}
	for i, tok := range []string{b.admin.Token, w.admin} {
		w.ok(w.sql(tok, "notes", "INSERT INTO t VALUES (?)", markers[i]), 200)
		w.ok(w.putObject(tok, "files", "doc.txt", markers[i]), 200)
	}
	stateB := func() string {
		return w.state("/v1", b.admin.Token, []string{"notes", b.only}, []string{"files", b.onlyBucket})
	}
	stateDefault := func() string { return w.state("/v1", w.admin, []string{"notes"}, []string{"files"}) }
	beforeB, beforeDefault := stateB(), stateDefault()
	if !strings.Contains(beforeB, markers[0]) || !strings.Contains(beforeDefault, markers[1]) {
		f.Fatal("the victims' state doesn't hold their markers")
	}

	for _, path := range []string{
		"v1/databases", "v1/databases/notes", "v1/databases/notes/query", "v1/databases/" + b.only + "/query", "v1/databases/" + b.notesID + "/query",
		"v1/databases/" + b.notesID, "v1/databases/" + b.notesID + "/branches/" + b.devID, "v1/databases/notes/branches/" + b.devID + "/query",
		"v1/databases/" + b.notesID + "/move", "v1/databases/notes/snapshots/" + b.snapshotID + "/restore",
		"v1/buckets", "v1/buckets/files/object?key=doc.txt", "v1/buckets/" + b.onlyBucket + "/object?key=doc.txt", "v1/buckets/" + b.filesID + "/objects",
		"v1/buckets/files/copy", "v1/buckets/" + b.filesID + "/move",
		"v1/tokens", "v1/tokens/" + b.admin.ID, "v1/audit", "v1/trash", "v1/workspace", "v1/workspaces", "v1/workspaces/globex", "v1/workspaces/" + b.id,
		"v1/workspaces/globex/restore", "v1/workspaces/default", "v1/instance/tokens", "v1/instance/audit", "v1/instance/tokens/" + b.admin.ID,
		"v1/workspaces/globex/databases", "v1/workspaces/globex/databases/notes/query", "v1/workspaces/" + b.id + "/databases/notes/query",
		"v1/workspaces/default/databases/notes/query", "v1/workspaces/globex/buckets/files/object?key=doc.txt", "v1/workspaces/globex/tokens",
		"v1/workspaces/globex/audit", "v1/workspaces/globex//databases/notes/query", "v1/workspaces/globex/databases//notes/query",
		"v1/workspaces/acme/../globex/databases", "v1/workspaces/acme/databases/../../globex/databases", "v1/workspaces/globex%2Fdatabases",
		"v1/workspaces/acme%2F..%2Fglobex/databases", "v1/workspaces/GLOBEX/databases", "v1/workspaces/globex/s3", "v1/workspaces/globex/s3/files/doc.txt",
		"v1/workspaces/default/s3/files/doc.txt", "v1/workspaces/globex/libsql/notes/main/v2/pipeline", "v1/workspaces/globex/mcp",
		"v1/s3", "v1/s3/files/doc.txt", "v1/s3/" + b.onlyBucket + "/doc.txt", "v1/s3/" + b.filesID + "/doc.txt", "v1/s3/files/big?uploadId=" + b.uploadID,
		"v1/s3/files?delete", "v1/s3/files/x?uploads", "v1/s3/../workspaces/globex/s3/files/doc.txt",
		"libsql/notes/main/v2/pipeline", "libsql/" + b.only + "/main/v2/pipeline", "libsql/" + b.notesID + "/" + b.mainID + "/v3/pipeline", "mcp",
	} {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			f.Add(method, path, `{"name":"x","sql":"SELECT * FROM t","workspace":"acme","access":"admin","database":"`+b.notesID+`"}`, "", "")
		}
	}
	f.Add("POST", "v1/databases/notes/query", `{"sql":"ATTACH 'x' AS y"}`, "", "")
	f.Add("POST", "v1/tokens", `{"name":"x","grants":[{"database":"`+b.notesID+`","access":"admin"},{"bucket":"`+b.filesID+`","access":"admin"}]}`, "", "")
	f.Add("POST", "v1/buckets/files/copy", `{"from_bucket":"`+b.filesID+`","from_key":"doc.txt","key":"stolen"}`, "", "")
	f.Add("PUT", "v1/s3/files/stolen", "", "X-Amz-Copy-Source", b.onlyBucket+"/doc.txt")
	f.Add("PUT", "v1/s3/files/stolen", "", "X-Amz-Copy-Source", "/"+b.filesID+"/doc.txt")
	f.Add("GET", "v1/databases", "", "X-Lumbung-Actor-Access", "admin")
	f.Add("POST", "v1/workspaces", `{"name":"mine"}`, "X-Lumbung-Actor", "dashboard:owner@example.com")
	f.Add("POST", "v1/databases", `{"name":"x"}`, "Idempotency-Key", "k")
	f.Add("POST", "libsql/notes/main/v2/pipeline", `{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"SELECT * FROM t"}}]}`, "", "")
	f.Add("POST", "mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"query","arguments":{"database":"`+b.notesID+`","sql":"SELECT * FROM t"}}}`, "Accept", "application/json, text/event-stream")

	tokens := []string{a.admin.Token, a.read.Token, a.dbAdmin, a.grants.Token}
	n := 0
	f.Fuzz(func(t *testing.T, method, path, body, header, value string) {
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			return
		}
		u, err := url.Parse(w.http.URL + "/" + path)
		if err != nil || u.Host == "" || strings.Contains(u.Host, "@") {
			return
		}
		base, _ := url.Parse(w.http.URL)
		if u.Host != base.Host {
			return
		}
		req, err := http.NewRequest(method, u.String(), strings.NewReader(body))
		if err != nil {
			return
		}
		n++
		req.Header.Set("Authorization", "Bearer "+tokens[n%len(tokens)])
		req.Header.Set("Content-Type", "application/json")
		if header != "" && !strings.EqualFold(header, "Authorization") {
			func() {
				defer func() { _ = recover() }() // a header name net/http refuses
				req.Header.Set(header, value)
			}()
		}
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			got, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
			res.Body.Close()
			for _, m := range markers {
				if bytes.Contains(got, []byte(m)) {
					t.Fatalf("%s /%s returned another workspace's data: %s", method, path, got)
				}
			}
		}
		if after := stateB(); after != beforeB {
			t.Fatalf("%s /%s (body %q, %s: %s) changed workspace B:\nbefore %s\nafter  %s", method, path, body, header, value, beforeB, after)
		}
		if after := stateDefault(); after != beforeDefault {
			t.Fatalf("%s /%s (body %q, %s: %s) changed the default workspace:\nbefore %s\nafter  %s", method, path, body, header, value, beforeDefault, after)
		}
	})
}
