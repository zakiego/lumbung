package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/server"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden error files")

// env is a running API on a fresh data directory.
type env struct {
	t       *testing.T
	srv     *server.Server
	http    *httptest.Server
	admin   string // the service token
	spec    map[string]any
	compile *jsonschema.Compiler
	mu      sync.Mutex
	schemas map[string]*jsonschema.Schema
}

func newEnv(t *testing.T, mod func(*config.Config)) *env {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.MinFreeDiskBytes = 1
	if mod != nil {
		mod(&cfg)
	}
	srv, err := server.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.API.Handler())
	t.Cleanup(func() {
		hs.Close()
		_ = srv.Engine.Close()
	})
	tok, err := os.ReadFile(filepath.Join(cfg.DataDir, server.ServiceTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	specJSON, err := json.Marshal(srv.API.API().OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(specJSON))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("https://lumbung.test/openapi.json", doc); err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	_ = json.Unmarshal(specJSON, &spec)
	return &env{t: t, srv: srv, http: hs, admin: strings.TrimSpace(string(tok)), spec: spec, compile: c, schemas: map[string]*jsonschema.Schema{}}
}

func pointerEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// check validates a JSON response body against the schema the OpenAPI
// document gives for that operation, status and content type.
func (e *env) check(method, template string, status int, contentType string, body []byte) {
	e.t.Helper()
	ops, ok := e.spec["paths"].(map[string]any)[template].(map[string]any)
	if !ok {
		e.t.Fatalf("no path %s in the OpenAPI document", template)
	}
	opObj, ok := ops[strings.ToLower(method)].(map[string]any)
	if !ok {
		e.t.Fatalf("no %s %s in the OpenAPI document", method, template)
	}
	responses := opObj["responses"].(map[string]any)
	code := fmt.Sprint(status)
	resp, ok := responses[code].(map[string]any)
	if !ok {
		code = "default"
		if resp, ok = responses["default"].(map[string]any); !ok {
			e.t.Fatalf("%s %s: status %d isn't documented", method, template, status)
		}
	}
	if status == http.StatusNoContent {
		return
	}
	ct := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	content, _ := resp["content"].(map[string]any)
	if _, ok := content[ct]; !ok {
		e.t.Fatalf("%s %s %d: content type %q isn't documented (have %v)", method, template, status, ct, keys(content))
	}
	if !strings.Contains(ct, "json") {
		return
	}
	loc := "https://lumbung.test/openapi.json#/paths/" + pointerEscape(template) + "/" + strings.ToLower(method) +
		"/responses/" + code + "/content/" + pointerEscape(ct) + "/schema"
	e.mu.Lock()
	sch, ok := e.schemas[loc]
	if !ok {
		var err error
		sch, err = e.compile.Compile(loc)
		if err != nil {
			e.mu.Unlock()
			e.t.Fatalf("compile %s: %v", loc, err)
		}
		e.schemas[loc] = sch
	}
	e.mu.Unlock()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		e.t.Fatalf("%s %s: body isn't JSON: %v\n%s", method, template, err, body)
	}
	if err := sch.Validate(inst); err != nil {
		e.t.Fatalf("%s %s %d: response doesn't match the OpenAPI schema: %v\n%s", method, template, status, err, body)
	}
}

func keys(m map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

type resp struct {
	status int
	header http.Header
	body   []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("not JSON: %s", r.body)
	}
	return m
}

func (r resp) code(t *testing.T) string {
	t.Helper()
	c, _ := r.json(t)["code"].(string)
	return c
}

// do sends a request. template is the OpenAPI path the request matches,
// used for the contract check; path fills it in.
func (e *env) do(token, method, template, path string, body any, headers ...string) resp {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case string:
		rd = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, e.http.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	r := resp{status: res.StatusCode, header: res.Header, body: b}
	if template != "" {
		e.check(method, template, r.status, res.Header.Get("Content-Type"), b)
	}
	return r
}

func (e *env) ok(r resp, want int) resp {
	e.t.Helper()
	if r.status != want {
		e.t.Fatalf("status %d, want %d: %s", r.status, want, r.body)
	}
	return r
}

func (e *env) fails(r resp, status int, code string) resp {
	e.t.Helper()
	if r.status != status || r.code(e.t) != code {
		e.t.Fatalf("got %d %s, want %d %s: %s", r.status, r.code(e.t), status, code, r.body)
	}
	if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		e.t.Fatalf("error content type %q", ct)
	}
	return r
}

// golden compares an error body with testdata/golden/<name>.json, ignoring
// the request id.
func golden(t *testing.T, name string, r resp) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatal(err)
	}
	if m["request_id"] == "" || m["request_id"] == nil {
		t.Fatalf("%s: no request_id", name)
	}
	delete(m, "request_id")
	got, _ := json.MarshalIndent(m, "", "  ")
	got = append(got, '\n')
	path := filepath.Join("testdata", "golden", name+".json")
	if *updateGolden {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run go test -run %s -update)", name, err, t.Name())
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s changed:\n got %s\nwant %s", name, got, want)
	}
}
