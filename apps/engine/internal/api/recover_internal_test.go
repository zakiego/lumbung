package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func panicServer() (*Server, *logBuffer) {
	var logs logBuffer
	return &Server{log: slog.New(slog.NewTextHandler(&logs, nil))}, &logs
}

// A handler that panics before writing gets a 500 problem; one that panics
// mid-response has its connection aborted. The panic is logged both times
// and the server keeps serving.
func TestRecoverPanicsHTTP(t *testing.T) {
	s, logs := panicServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /early", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-Id", "req_test")
		panic("early")
	})
	mux.HandleFunc("GET /late", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "partial")
		panic("late")
	})
	mux.HandleFunc("GET /fine", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "fine") })
	hs := httptest.NewServer(s.recoverPanics(mux))
	defer hs.Close()

	res, err := http.Get(hs.URL + "/early")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var p Problem
	_ = json.Unmarshal(body, &p)
	if res.StatusCode != 500 || p.Code != "internal" || p.RequestID != "req_test" || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/problem+json") {
		t.Errorf("early panic: %d %s", res.StatusCode, body)
	}

	if res, err := http.Get(hs.URL + "/late"); err == nil {
		_, err = io.ReadAll(res.Body)
		res.Body.Close()
		if err == nil {
			t.Error("late panic: the response ended cleanly, want it aborted")
		}
	}

	res, err = http.Get(hs.URL + "/fine")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "fine" {
		t.Errorf("after the panics: %q", body)
	}
	out := logs.String()
	for _, want := range []string{"panic=early", "panic=late", "request_id=req_test", "recover_internal_test.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q: %s", want, out)
		}
	}
}

// A panic in an MCP method becomes that call's error. The SDK runs methods
// on its own goroutines, where nothing else recovers it.
func TestRecoverMCP(t *testing.T) {
	s, logs := panicServer()
	h := s.recoverMCP(func(context.Context, string, mcp.Request) (mcp.Result, error) { panic("tool") })
	res, err := h(context.Background(), "tools/call", nil)
	if res != nil || err == nil || err.Error() != "internal error" {
		t.Fatalf("got %v, %v", res, err)
	}
	if out := logs.String(); !strings.Contains(out, "method=tools/call") || !strings.Contains(out, "panic=tool") {
		t.Errorf("log: %s", out)
	}
	ok := s.recoverMCP(func(context.Context, string, mcp.Request) (mcp.Result, error) { return &mcp.CallToolResult{}, nil })
	if res, err := ok(context.Background(), "tools/call", nil); res == nil || err != nil {
		t.Errorf("without a panic: %v, %v", res, err)
	}
}

// The libSQL stream timer and the slow query writer run outside any
// request; a panic in either is logged.
func TestRecoverBackground(t *testing.T) {
	s, logs := panicServer()
	// A stream with no timer panics on a nil pointer when it expires.
	st := &libsqlStream{reg: &libsqlStreams{log: s.log}}
	st.expire()
	// No store: writing the slow query panics on a nil pointer.
	s.addSlow("br", meta.SlowQuery{SQL: "SELECT 1"})
	out := logs.String()
	for _, want := range []string{"in=\"libsql stream expiry\"", "in=\"slow query log\""} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s: %s", want, out)
		}
	}
}
