// Package api is the REST API under /v1 (PLAN 7). Handlers are typed with
// Huma, which also generates the OpenAPI document committed as
// apps/engine/openapi.json.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/ratelimit"
	"github.com/zakiego/lumbung/apps/engine/internal/stats"
	"github.com/zakiego/lumbung/apps/engine/internal/version"
)

// Server is the HTTP API.
type Server struct {
	eng      *engine.Engine
	store    *meta.Store
	log      *slog.Logger
	api      huma.API
	mux      *http.ServeMux
	stats    *stats.Recorder
	perToken *ratelimit.Limiter
	failures *ratelimit.Limiter
	proxies  []*net.IPNet
	ipHeader string
	now      func() time.Time
	extra    []func(huma.API) // more operations, added by other packages (MCP)

	touchMu sync.Mutex
	touched map[string]time.Time

	mcpServer *mcp.Server
}

// Option changes a server at construction.
type Option func(*Server)

// WithRecorder shares a stats recorder (the MCP endpoint records into it too).
func WithRecorder(r *stats.Recorder) Option { return func(s *Server) { s.stats = r } }

// New builds the API on a fresh mux.
func New(eng *engine.Engine, log *slog.Logger, opts ...Option) *Server {
	if log == nil {
		log = slog.Default()
	}
	cfg := eng.Config()
	s := &Server{
		eng:      eng,
		store:    eng.Meta(),
		log:      log,
		mux:      http.NewServeMux(),
		stats:    stats.NewRecorder(),
		perToken: ratelimit.New(float64(cfg.RateLimit), cfg.RateBurst),
		failures: ratelimit.New(float64(cfg.AuthFailuresPerMin)/60, cfg.AuthFailuresPerMin),
		now:      time.Now,
		touched:  map[string]time.Time{},
		ipHeader: cfg.ClientIPHeader,
	}
	for _, o := range opts {
		o(s)
	}
	s.proxies, _ = config.ParseNetworks(cfg.TrustedProxies) // checked by config.Load
	s.api = humago.New(s.mux, Config())
	s.api.UseMiddleware(s.middleware)
	s.register()
	s.mountMCP()
	s.mux.HandleFunc("GET /health", s.health)
	return s
}

// Config is the Huma configuration: the OpenAPI document at
// /v1/openapi.json, no docs UI or schema links, bearer auth.
func Config() huma.Config {
	// The document version is the API version, not the binary version, so
	// the committed openapi.json only changes when the API does.
	cfg := huma.DefaultConfig("Lumbung API", "1")
	cfg.Info.Description = "Self-hosted SQLite databases with branches. Every request needs `Authorization: Bearer <token>`. Errors are `application/problem+json` with a stable `code`."
	cfg.OpenAPIPath = "/v1/openapi"
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	// One transformer: stamp the request id on every problem, including the
	// validation errors Huma raises before an operation runs.
	cfg.Transformers = []huma.Transformer{stampRequestID}
	cfg.CreateHooks = nil
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer": {Type: "http", Scheme: "bearer", Description: "A Lumbung token: lmb_<id>_<secret>."},
	}
	cfg.Security = []map[string][]string{{"bearer": {}}}
	return cfg
}

func stampRequestID(ctx huma.Context, _ string, v any) (any, error) {
	if p, ok := v.(*Problem); ok && p.RequestID == "" {
		if info := requestInfoFrom(ctx.Context()); info != nil {
			p.RequestID = info.id
			if info.code == "" {
				info.code = p.Code
			}
		}
	}
	return v, nil
}

// Handler returns the HTTP handler. Requests no route matches get a
// problem+json 404 or 405 instead of the router's plain text.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mux.ServeHTTP(&problemFallback{ResponseWriter: w}, r)
	})
}

// problemFallback rewrites the router's own text/plain 404 and 405 into
// problems. Responses from handlers already have a JSON content type.
type problemFallback struct {
	http.ResponseWriter
	swallow bool
}

func (f *problemFallback) WriteHeader(status int) {
	h := f.Header()
	if (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) && strings.HasPrefix(h.Get("Content-Type"), "text/plain") {
		f.swallow = true
		code := apierr.NotFound
		msg := "no such endpoint"
		if status == http.StatusMethodNotAllowed {
			code, msg = apierr.MethodNotAllowed, "this endpoint doesn't accept that method"
		}
		h.Set("Content-Type", "application/problem+json")
		h.Del("X-Content-Type-Options")
		f.ResponseWriter.WriteHeader(status)
		_ = json.NewEncoder(f.ResponseWriter).Encode(problemFrom(apierr.New(code, msg)))
		return
	}
	f.ResponseWriter.WriteHeader(status)
}

func (f *problemFallback) Write(b []byte) (int, error) {
	if f.swallow {
		return len(b), nil
	}
	return f.ResponseWriter.Write(b)
}

// Flush lets streamed downloads through.
func (f *problemFallback) Flush() {
	if fl, ok := f.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer (read deadlines).
func (f *problemFallback) Unwrap() http.ResponseWriter { return f.ResponseWriter }

// API exposes the Huma API, for generating the OpenAPI document.
func (s *Server) API() huma.API { return s.api }

// Recorder returns the stats recorder.
func (s *Server) Recorder() *stats.Recorder { return s.stats }

// Mux returns the router, so other endpoints (MCP) can share it.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// health answers 200 only when the engine can serve (PLAN 6.10).
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.eng.Ready(ctx); err != nil {
		ae := apierr.As(err)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		p := problemFrom(apierr.New(apierr.Unavailable, ae.Message))
		_ = json.NewEncoder(w).Encode(p)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok", "version": version.Version, "meta_version": s.store.Version(),
	})
}

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) trusted(ip net.IP) bool {
	for _, n := range s.proxies {
		if ip != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP is the connection's address, or, when the connection comes from a
// trusted proxy, the address the proxy header names. A list (X-Forwarded-For)
// is read from the right, skipping trusted proxies: proxies append, so the
// left end is whatever the client chose to send.
func (s *Server) clientIP(ctx huma.Context) string {
	host, _, err := net.SplitHostPort(ctx.RemoteAddr())
	if err != nil {
		host = ctx.RemoteAddr()
	}
	if s.ipHeader == "" {
		return host
	}
	return s.clientIPFrom(host, ctx.Header(s.ipHeader))
}

func (s *Server) clientIPFrom(host, header string) string {
	if s.ipHeader == "" || !s.trusted(net.ParseIP(host)) {
		return host
	}
	parts := strings.Split(header, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil {
			return host // malformed: don't guess
		}
		if !s.trusted(ip) {
			return ip.String()
		}
	}
	return host
}

func bearer(h string) (string, bool) {
	const p = "Bearer "
	if len(h) <= len(p) || !strings.EqualFold(h[:len(p)], p) {
		return "", false
	}
	return strings.TrimSpace(h[len(p):]), true
}

// authenticate resolves the bearer token to a principal.
func (s *Server) authenticate(ctx context.Context, header string) (auth.Principal, error) {
	tok, ok := bearer(header)
	if !ok {
		return auth.Principal{}, apierr.New(apierr.Unauthorized, "send Authorization: Bearer <token>")
	}
	id, err := auth.Parse(tok)
	if err != nil {
		return auth.Principal{}, apierr.New(apierr.Unauthorized, "malformed token")
	}
	rec, err := s.store.GetToken(ctx, id)
	if errors.Is(err, meta.ErrNoToken) {
		// Compare anyway so a missing id takes as long as a wrong secret.
		auth.Matches(tok, make([]byte, 32))
		return auth.Principal{}, apierr.New(apierr.Unauthorized, "unknown token")
	}
	if err != nil {
		return auth.Principal{}, err
	}
	if !auth.Matches(tok, rec.SecretHash) {
		return auth.Principal{}, apierr.New(apierr.Unauthorized, "unknown token")
	}
	p, err := auth.FromToken(rec, s.now())
	if err != nil {
		return auth.Principal{}, apierr.New(apierr.Unauthorized, err.Error())
	}
	s.touch(ctx, rec.ID)
	return p, nil
}

// touch records a token's last use, writing meta.db at most once a minute
// per token so the hot path doesn't take the write lock on every request.
func (s *Server) touch(ctx context.Context, tokenID string) {
	now := s.now()
	s.touchMu.Lock()
	last, ok := s.touched[tokenID]
	if ok && now.Sub(last) < time.Minute {
		s.touchMu.Unlock()
		return
	}
	s.touched[tokenID] = now
	s.touchMu.Unlock()
	if err := s.store.TouchToken(ctx, tokenID, now); err != nil {
		s.log.Error("record token use", "err", err)
	}
}

var actorRe = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,200}$`)

// middleware runs around every operation: request id, authentication with
// failed-attempt limits per address, the per-token rate limit, and the audit
// entry for management actions.
func (s *Server) middleware(ctx huma.Context, next func(huma.Context)) {
	id := ctx.Header("X-Request-Id")
	if !requestIDRe.MatchString(id) {
		id = newRequestID()
	}
	ctx.SetHeader("X-Request-Id", id)
	info := &requestInfo{id: id}
	ctx = huma.WithValue(ctx, keyInfo, info)
	ctx = huma.WithValue(ctx, keyLog, s.log)

	// Authenticate first: a valid token always gets in, so a flood of bad
	// tokens from a shared address (a proxy, a tunnel) can't lock real
	// clients out. Failures are what the per-address limit counts.
	p, err := s.authenticate(ctx.Context(), ctx.Header("Authorization"))
	if err != nil {
		ae := apierr.As(err)
		if ae.Code == apierr.Unauthorized {
			if ok, wait := s.failures.Allow(s.clientIP(ctx)); !ok {
				ctx.SetHeader("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
				ae = apierr.New(apierr.RateLimited, "too many failed authentications from this address")
			}
		} else {
			s.log.Error("authenticate", "err", err)
			ae = apierr.New(apierr.Internal, "internal error")
		}
		ctx.SetHeader("WWW-Authenticate", `Bearer realm="lumbung"`)
		writeProblem(ctx, ae, id)
		return
	}
	if ok, wait := s.perToken.Allow(p.TokenID); !ok {
		ctx.SetHeader("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeProblem(ctx, apierr.New(apierr.RateLimited, "too many requests for this token"), id)
		return
	}
	if a := ctx.Header("X-Lumbung-Actor"); a != "" && p.Service && actorRe.MatchString(a) {
		p.Actor = a
	}
	info.principal = p

	next(ctx)

	if action, _ := ctx.Operation().Metadata["audit"].(string); action != "" {
		e := meta.AuditEntry{TokenID: p.TokenID, Actor: p.Actor, Action: action, Target: info.target, Result: "ok", RequestID: id}
		if ctx.Status() >= 400 {
			e.Result, e.Code = "error", info.code
			if e.Code == "" {
				e.Code = codeForStatus(ctx.Status())
			}
		}
		actx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.store.AddAudit(actx, e, s.eng.Config().AuditMaxRows); err != nil {
			s.log.Error("audit", "err", err, "action", action)
		}
	}
}
