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
	"net/url"
	"path"
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
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
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
	// publicLimit counts requests without a token (public object reads and
	// CORS preflights) per client address.
	publicLimit *ratelimit.Limiter
	proxies     []*net.IPNet
	ipHeader    string
	now         func() time.Time
	extra       []func(huma.API) // more operations, added by other packages (MCP)

	touchMu sync.Mutex
	touched map[string]time.Time

	mcpServer *mcp.Server
	libsql    *libsqlStreams

	slowQ     chan slowEntry // slow-query rows for the background writer
	stop      chan struct{}  // closed by Close
	closeOnce sync.Once

	waiters   waiters       // waits held open, by token (wait.go)
	draining  chan struct{} // closed by Drain
	drainOnce sync.Once
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
		eng:         eng,
		store:       eng.Meta(),
		log:         log,
		mux:         http.NewServeMux(),
		stats:       stats.NewRecorder(),
		perToken:    ratelimit.New(float64(cfg.RateLimit), cfg.RateBurst),
		failures:    ratelimit.New(float64(cfg.AuthFailuresPerMin)/60, cfg.AuthFailuresPerMin),
		publicLimit: ratelimit.New(float64(cfg.PublicRateLimit), 2*cfg.PublicRateLimit),
		now:         time.Now,
		touched:     map[string]time.Time{},
		slowQ:       make(chan slowEntry, 64),
		stop:        make(chan struct{}),
		draining:    make(chan struct{}),
		ipHeader:    cfg.ClientIPHeader,
	}
	for _, o := range opts {
		o(s)
	}
	s.proxies, _ = config.ParseNetworks(cfg.TrustedProxies) // checked by config.Load
	s.api = humago.New(s.mux, Config())
	s.api.UseMiddleware(s.middleware)
	s.register()
	s.mountMCP()
	s.mountLibSQL()
	go s.slowWorker()
	s.mux.HandleFunc("GET /health", s.health)
	return s
}

// Config is the Huma configuration: the OpenAPI document at
// /v1/openapi.json, no docs UI or schema links, bearer auth.
func Config() huma.Config {
	// The document version is the API version, not the binary version, so
	// the committed openapi.json only changes when the API does.
	cfg := huma.DefaultConfig("Lumbung API", "1")
	cfg.Info.Description = "Self-hosted SQLite databases with branches. Every request needs `Authorization: Bearer <token>`. Errors are `application/problem+json` with a stable `code`.\n\n" +
		"Databases, buckets and tokens live in workspaces. A request acts in the workspace of its token; an instance token, which belongs to none, acts in the default workspace. " +
		"To name a workspace, put `/v1/workspaces/{workspace}` in front of a path under `/v1/databases`, `/v1/trash`, `/v1/buckets`, `/v1/tokens`, `/v1/audit`, `/v1/workspace` or `/v1/s3` " +
		"(for example `/v1/workspaces/acme/databases`), or of `/libsql` and `/mcp`. A token that belongs to a workspace can only name its own; any other gets `workspace_not_found`."
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

// Handler returns the HTTP handler. The S3-compatible API is matched on the
// raw path first: the router cleans paths, which would change object keys.
// Requests no route matches get a problem+json 404 or 405 instead of the
// router's plain text.
//
// A qualified URL, /v1/workspaces/{ws}/..., is the same request as its short
// form with the workspace named (PLAN 17.3): the prefix is taken off here,
// kept in the context, and the request goes to the same handler as the
// short URL. Nothing is decided here. Whether the caller may act in that
// workspace is checked after authentication, in act.
func (s *Server) Handler() http.Handler {
	return s.recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// X-Lumbung-Actor-Access names one level. An empty or repeated one
		// is a caller's bug, and treating it as absent would run the request
		// with the service token's full access, so it is refused outright.
		if vals, ok := r.Header[actorAccessHeader]; ok && (len(vals) != 1 || strings.TrimSpace(vals[0]) == "") {
			writeProblemHTTP(w, apierr.New(apierr.BadRequest, actorAccessHeader+" must be read, write or admin"), newRequestID())
			return
		}
		raw := r.URL.EscapedPath()
		sel, rest, qualified := splitQualified(raw)
		if !qualified {
			rest = raw
		}
		if isS3Path(rest) {
			s.serveS3(w, r, sel, rest)
			return
		}
		if qualified {
			decoded, err := url.PathUnescape(rest)
			// The router answers a path with empty, "." or ".." segments
			// with a redirect to the cleaned path. Here that would be the
			// short path, without the workspace, and a client that follows
			// it would act in its default workspace instead. So such a path
			// is no endpoint, and any redirect the router still makes is
			// turned into the same answer.
			if err != nil || path.Clean(decoded) != decoded {
				writeProblemHTTP(w, apierr.New(apierr.NotFound, "no such endpoint"), newRequestID())
				return
			}
			r = r.Clone(context.WithValue(r.Context(), keySel, sel))
			r.URL.Path, r.URL.RawPath = decoded, rest
			w = &noRedirect{ResponseWriter: w}
		}
		s.mux.ServeHTTP(&problemFallback{ResponseWriter: w}, r)
	}))
}

// noRedirect turns a redirect from the router into a 404 problem. It wraps
// the response of a qualified URL, whose Location would name the short path.
type noRedirect struct {
	http.ResponseWriter
	blocked bool
}

func (n *noRedirect) WriteHeader(status int) {
	if status >= 300 && status < 400 && status != http.StatusNotModified {
		n.blocked = true
		h := n.Header()
		h.Del("Location")
		h.Set("Content-Type", "application/problem+json")
		n.ResponseWriter.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(n.ResponseWriter).Encode(problemFrom(apierr.New(apierr.NotFound, "no such endpoint")))
		return
	}
	n.ResponseWriter.WriteHeader(status)
}

func (n *noRedirect) Write(b []byte) (int, error) {
	if n.blocked {
		return len(b), nil
	}
	return n.ResponseWriter.Write(b)
}

func (n *noRedirect) Flush() {
	if fl, ok := n.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

func (n *noRedirect) Unwrap() http.ResponseWriter { return n.ResponseWriter }

const workspacesPrefix = "/v1/workspaces/"

// splitQualified splits a qualified URL path into the workspace it names
// and the short path it stands for. Only the paths that act inside a
// workspace have a qualified form; /v1/workspaces/{ws} itself and its own
// sub-resources are ordinary routes.
func splitQualified(escaped string) (sel, rest string, ok bool) {
	after, found := strings.CutPrefix(escaped, workspacesPrefix)
	if !found {
		return "", "", false
	}
	ref, tail, found := strings.Cut(after, "/")
	if !found || !(ids.ValidName(ref) || ids.IsID(ref, ids.Workspace)) {
		return "", "", false
	}
	head, _, _ := strings.Cut(tail, "/")
	switch head {
	case "databases", "trash", "buckets", "tokens", "audit", "s3", "workspace":
		return ref, "/v1/" + tail, true
	case "libsql", "mcp":
		return ref, "/" + tail, true
	}
	return "", "", false
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
	p, err := s.principalOf(ctx, rec)
	if err != nil {
		return auth.Principal{}, err
	}
	s.touch(ctx, rec.ID)
	return p, nil
}

// principalOf builds the principal of a token record whose secret the
// caller has already checked. It fails with unauthorized for a revoked or
// expired token, and for a token whose workspace is gone: such a token
// answers exactly like one that doesn't exist.
func (s *Server) principalOf(ctx context.Context, rec meta.Token) (auth.Principal, error) {
	p, err := auth.FromToken(rec, s.now())
	if err != nil {
		return auth.Principal{}, apierr.New(apierr.Unauthorized, err.Error())
	}
	if p.Bound() {
		if _, err := s.eng.GetWorkspace(ctx, p.WorkspaceID); err != nil {
			if apierr.As(err).Code == apierr.WorkspaceNotFound {
				return auth.Principal{}, apierr.New(apierr.Unauthorized, "unknown token")
			}
			return auth.Principal{}, err
		}
	}
	return p, nil
}

// act resolves the workspace a request acts in and returns the principal
// acting there (PLAN 17.3). sel is the workspace a qualified URL names, by
// name or id; it is empty for a short URL, which means the caller's own
// workspace: a bound token's, or the default workspace for an instance
// token.
//
// A bound token naming any workspace but its own gets workspace_not_found,
// worded exactly like a workspace that doesn't exist, so it can't tell
// which names are taken.
func (s *Server) act(ctx context.Context, p auth.Principal, sel string) (auth.Principal, error) {
	ws, err := s.requestWorkspace(ctx, p.Bound(), p.WorkspaceID, sel)
	if err != nil {
		return auth.Principal{}, err
	}
	acting, ok := p.In(ws.ID)
	if !ok {
		return auth.Principal{}, apierr.Newf(apierr.WorkspaceNotFound, "no workspace %q", sel)
	}
	return acting, nil
}

// actorAccessHeader carries the level of the dashboard user the service
// token acts for (PLAN 17.8).
const actorAccessHeader = "X-Lumbung-Actor-Access"

// narrowed lowers the service token's principal to the level its
// X-Lumbung-Actor-Access header names, in the workspace the request acts
// in, so the engine enforces a dashboard user's role and not only the
// dashboard. The header is ignored on every other token: it can only take
// reach away, and only the service token is trusted to say who it acts for.
//
// The request must name its workspace (a qualified URL). On a short URL the
// service token acts in the default workspace, and a user's level in some
// other workspace must never be applied there because a prefix was left
// off.
func narrowed(p auth.Principal, level string, namesWorkspace bool) (auth.Principal, error) {
	if level == "" || !p.Service {
		return p, nil
	}
	if !namesWorkspace {
		return auth.Principal{}, apierr.New(apierr.BadRequest, actorAccessHeader+" needs a URL that names the workspace: /v1/workspaces/{workspace}/...")
	}
	q, ok := p.Narrow(level)
	if !ok {
		return auth.Principal{}, apierr.New(apierr.BadRequest, actorAccessHeader+" must be read, write or admin")
	}
	return q, nil
}

// requestWorkspace finds the workspace a request names or implies.
func (s *Server) requestWorkspace(ctx context.Context, bound bool, own, sel string) (meta.Workspace, error) {
	switch {
	case sel != "":
		return s.eng.GetWorkspace(ctx, sel)
	case bound:
		ws, err := s.eng.GetWorkspace(ctx, own)
		if err != nil && apierr.As(err).Code == apierr.WorkspaceNotFound {
			// Deleted between authentication and here.
			return meta.Workspace{}, apierr.New(apierr.Unauthorized, "unknown token")
		}
		return ws, err
	}
	return s.eng.DefaultWorkspace(ctx)
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

// rateKey is what a principal's requests are counted under: its token. The
// service token is one token for everyone who uses the dashboard, so its
// requests are counted per person it names. One person, in one workspace,
// then can't use up what the others need.
func rateKey(p auth.Principal) string {
	if p.Service && p.Actor != "" {
		return p.TokenID + "\x00" + p.Actor
	}
	return p.TokenID
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
	if a := ctx.Header("X-Lumbung-Actor"); a != "" && p.Service && actorRe.MatchString(a) {
		p.Actor = a
	}
	if ok, wait := s.perToken.Allow(rateKey(p)); !ok {
		ctx.SetHeader("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeProblem(ctx, apierr.New(apierr.RateLimited, "too many requests for this token"), id)
		return
	}
	// Every operation runs as a principal acting in exactly one workspace.
	p, err = s.act(ctx.Context(), p, selFrom(ctx.Context()))
	if err == nil {
		p, err = narrowed(p, ctx.Header(actorAccessHeader), selFrom(ctx.Context()) != "")
	}
	if err != nil {
		ae := apierr.As(err)
		if ae.Code == apierr.Internal {
			s.log.Error("resolve workspace", "err", err)
		}
		writeProblem(ctx, ae, id)
		return
	}
	info.principal = p

	next(ctx)

	if action, _ := ctx.Operation().Metadata["audit"].(string); action != "" {
		e := meta.AuditEntry{WorkspaceID: p.WorkspaceID, TokenID: p.TokenID, Actor: p.Actor, Action: action, Target: info.target, Result: "ok", RequestID: id}
		if instance, _ := ctx.Operation().Metadata["instance"].(bool); instance && !p.Bound() {
			// Workspaces, instance tokens and moves belong to no workspace's
			// log. A workspace token that tries one (and is refused) is logged
			// in its own workspace: its attempts must not be able to push the
			// instance's history out.
			e.WorkspaceID = ""
		}
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
