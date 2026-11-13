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
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
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
	now      func() time.Time
	extra    []func(huma.API) // more operations, added by other packages (MCP)
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
	}
	for _, o := range opts {
		o(s)
	}
	for _, cidr := range strings.Split(cfg.TrustedProxies, ",") {
		if _, n, err := net.ParseCIDR(strings.TrimSpace(cidr)); err == nil {
			s.proxies = append(s.proxies, n)
		}
	}
	s.api = humago.New(s.mux, Config())
	s.api.UseMiddleware(s.middleware)
	s.register()
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

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

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

// clientIP is the connection's address, or the proxy header's when the
// connection comes from a trusted proxy.
func (s *Server) clientIP(ctx huma.Context) string {
	host, _, err := net.SplitHostPort(ctx.RemoteAddr())
	if err != nil {
		host = ctx.RemoteAddr()
	}
	hdr := s.eng.Config().ClientIPHeader
	if hdr == "" {
		return host
	}
	ip := net.ParseIP(host)
	trusted := false
	for _, n := range s.proxies {
		if ip != nil && n.Contains(ip) {
			trusted = true
			break
		}
	}
	if !trusted {
		return host
	}
	v := ctx.Header(hdr)
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i] // X-Forwarded-For: the first entry is the client
	}
	if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
		return ip.String()
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
	_ = s.store.TouchToken(ctx, rec.ID, s.now())
	return p, nil
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

	ip := s.clientIP(ctx)
	if ok, wait := s.failuresAllowed(ip); !ok {
		ctx.SetHeader("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeProblem(ctx, apierr.New(apierr.RateLimited, "too many failed authentications from this address"), id)
		return
	}
	p, err := s.authenticate(ctx.Context(), ctx.Header("Authorization"))
	if err != nil {
		ae := apierr.As(err)
		if ae.Code == apierr.Unauthorized {
			s.failures.Allow(ip)
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

// failuresAllowed checks, without spending, whether an address may still try.
func (s *Server) failuresAllowed(ip string) (bool, time.Duration) {
	return s.failures.Peek(ip)
}
