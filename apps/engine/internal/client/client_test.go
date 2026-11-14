package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A redirect is never followed: the token and the body would go to a path
// nobody asked for.
func TestRedirectsAreNotFollowed(t *testing.T) {
	var followed atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/databases" {
			followed.Add(1)
			return
		}
		http.Redirect(w, r, "/v1/databases", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := New(srv.URL, "lmb_test")
	c.Workspace = "ws"
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		err := c.Do(context.Background(), method, "/v1/databases/x", nil, nil, nil)
		var e *Error
		if !errors.As(err, &e) || e.Status != http.StatusTemporaryRedirect {
			t.Fatalf("%s: got %v, want the 307 as an error", method, err)
		}
	}
	if err := c.Upload(context.Background(), "/v1/buckets/b/object", nil, strings.NewReader("x"), 1, "text/plain", nil, nil); err == nil {
		t.Fatal("upload followed a redirect")
	}
	if n := followed.Load(); n != 0 {
		t.Fatalf("%d requests followed the redirect", n)
	}
}

func TestWorkspaceRefNeverLeavesAsAPath(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	for _, bad := range []string{"..", ".", "a/b", "../default", "A", " ", "%2e%2e", "ws_short", strings.Repeat("a", 64)} {
		c := New(srv.URL, "lmb_test")
		c.Workspace = bad
		if err := c.Do(context.Background(), http.MethodDelete, "/v1/databases/x", nil, nil, nil); err == nil {
			t.Errorf("workspace %q was sent", bad)
		}
		if err := c.Upload(context.Background(), "/v1/buckets/b/object", nil, strings.NewReader("x"), 1, "text/plain", nil, nil); err == nil {
			t.Errorf("workspace %q was sent on an upload", bad)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
	for _, good := range []string{"default", "a", "agent-1", "ws_174a999a2be2baa5443f"} {
		if err := ValidWorkspace(good); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}
