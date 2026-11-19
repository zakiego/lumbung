package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"runtime/debug"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
)

// recoverPanics answers a request whose handler panicked with a 500
// problem and logs the panic with its stack. When part of the response is
// already out, it aborts the connection instead. net/http recovers a
// handler's own goroutine too, but only by dropping the connection, which a
// client reads as EOF.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &startedWriter{ResponseWriter: w}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			id := w.Header().Get("X-Request-Id")
			s.log.Error("recovered panic", "in", "http", "method", r.Method, "path", r.URL.Path, "request_id", id, "panic", v, "stack", string(debug.Stack()))
			if sw.started {
				panic(http.ErrAbortHandler)
			}
			if id == "" {
				id = newRequestID()
			}
			writeProblemHTTP(w, apierr.New(apierr.Internal, "internal error"), id)
		}()
		next.ServeHTTP(sw, r)
	})
}

// startedWriter records whether any of the response was sent.
type startedWriter struct {
	http.ResponseWriter
	started bool
}

func (w *startedWriter) WriteHeader(status int) {
	if status >= 200 {
		w.started = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *startedWriter) Write(b []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(b)
}

// ReadFrom keeps io.Copy into the response on the underlying writer's fast
// path (sendfile for object downloads).
func (w *startedWriter) ReadFrom(r io.Reader) (int64, error) {
	w.started = true
	return io.Copy(w.ResponseWriter, r)
}

func (w *startedWriter) Flush() {
	w.started = true
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *startedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// recoverMCP turns a panic in an MCP method (a tool call, say) into an
// internal error for that call. The MCP SDK runs methods on goroutines of
// its own, outside the request's, where an unrecovered panic ends the
// process.
func (s *Server) recoverMCP(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (res mcp.Result, err error) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("recovered panic", "in", "mcp", "method", method, "panic", v, "stack", string(debug.Stack()))
				res, err = nil, errors.New("internal error")
			}
		}()
		return next(ctx, method, req)
	}
}
