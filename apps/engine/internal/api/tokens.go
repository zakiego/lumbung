package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/stats"
)

// TokenOut is a token, without its secret.
type TokenOut struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope" enum:"instance,database"`
	DatabaseID string     `json:"database_id" doc:"For a database token, the database it reaches."`
	BranchID   string     `json:"branch_id" doc:"When set, the token only reaches this branch."`
	Access     string     `json:"access" enum:"read,write,admin"`
	Service    bool       `json:"service" doc:"The dashboard's service token."`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func tokenOut(t meta.Token) TokenOut {
	return TokenOut{ID: t.ID, Name: t.Name, Scope: t.Scope, DatabaseID: t.DatabaseID, BranchID: t.BranchID,
		Access: t.Access, Service: t.Service, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt}
}

type tokenList struct {
	Body struct {
		Data       []TokenOut `json:"data"`
		NextCursor string     `json:"next_cursor"`
	}
}

// createTokenIn takes no Idempotency-Key: replaying a create would have to
// store the secret, which is only ever shown once.
type createTokenIn struct {
	Body struct {
		Name          string `json:"name" minLength:"1" maxLength:"100"`
		Database      string `json:"database,omitempty" doc:"Database name or id. Omit for an instance token."`
		Branch        string `json:"branch,omitempty" doc:"Branch name or id, to limit a database token to one branch."`
		Access        string `json:"access" enum:"read,write,admin"`
		ExpiresInDays int    `json:"expires_in_days,omitempty" minimum:"0" maximum:"3650" doc:"0: never expires. A token that expires can't create one that outlives it: the expiry is capped at the creator's."`
	}
}

type createTokenOut struct {
	Body struct {
		TokenOut
		Token string `json:"token" doc:"The full token. Shown once; only its hash is stored."`
	}
}

type tokenPath struct {
	TokenID string `path:"token_id"`
}

type auditIn struct {
	PageIn
}

// AuditOut is one audit log entry.
type AuditOut struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	TokenID   string    `json:"token_id"`
	Actor     string    `json:"actor" doc:"The dashboard user, for actions taken in the dashboard."`
	Action    string    `json:"action"`
	Target    string    `json:"target" doc:"Ids the action touched, slash-separated."`
	Result    string    `json:"result" enum:"ok,error"`
	Code      string    `json:"code" doc:"The error code when result is error."`
	RequestID string    `json:"request_id"`
}

type auditList struct {
	Body struct {
		Data       []AuditOut `json:"data"`
		NextCursor string     `json:"next_cursor"`
	}
}

type statsIn struct {
	BranchPath
	Window string `query:"window" enum:"1h,6h,24h,48h" default:"1h"`
}

// SlowQueryOut is one slow-query log entry.
type SlowQueryOut struct {
	At         time.Time `json:"at"`
	DurationMS int64     `json:"duration_ms"`
	SQL        string    `json:"sql"`
	ErrorCode  string    `json:"error_code"`
}

type statsOut struct {
	Body struct {
		stats.Summary
		SizeBytes   int64          `json:"size_bytes"`
		SlowQueries []SlowQueryOut `json:"slow_queries"`
	}
}

func (s *Server) registerTokens(api huma.API) {
	huma.Register(api, op("list-tokens", http.MethodGet, "/v1/tokens", "List tokens", "Tokens", ""),
		func(ctx context.Context, in *PageIn) (*tokenList, error) {
			p := principalFrom(ctx)
			if !p.InstanceAdmin() && p.Access != auth.AccessAdmin {
				return nil, toProblem(ctx, apierr.New(apierr.Forbidden, "listing tokens needs an admin token"))
			}
			// Tokens are few; read them all, then filter and page.
			all, err := s.store.ListTokens(ctx, "", "", 100000)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			visible := []meta.Token{}
			for _, t := range all {
				if p.InstanceAdmin() || p.Covers(t.Scope, t.DatabaseID, t.BranchID, t.Access) {
					visible = append(visible, t)
				}
			}
			items, next, err := page(visible, func(t meta.Token) string { return invertedTime(t.CreatedAt) + t.ID }, *in)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			out := &tokenList{}
			out.Body.Data = make([]TokenOut, 0, len(items))
			out.Body.NextCursor = next
			for _, t := range items {
				out.Body.Data = append(out.Body.Data, tokenOut(t))
			}
			return out, nil
		})

	huma.Register(api, created(op("create-token", http.MethodPost, "/v1/tokens", "Create a token", "Tokens", "token.create")),
		func(ctx context.Context, in *createTokenIn) (*createTokenOut, error) {
			p := principalFrom(ctx)
			scope, dbID, branchID := auth.ScopeInstance, "", ""
			if in.Body.Database != "" {
				scope = auth.ScopeDatabase
				d, err := s.eng.GetDatabase(ctx, in.Body.Database)
				if err != nil || !p.SeesDatabase(d.ID) {
					return nil, toProblem(ctx, apierr.Newf(apierr.DatabaseNotFound, "no database %q", in.Body.Database))
				}
				dbID = d.ID
				if in.Body.Branch != "" {
					b, err := s.eng.GetBranch(ctx, d.ID, in.Body.Branch)
					if err != nil {
						return nil, toProblem(ctx, err)
					}
					branchID = b.ID
				}
			} else if in.Body.Branch != "" {
				return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "a branch needs a database"))
			}
			if !p.Covers(scope, dbID, branchID, in.Body.Access) {
				return nil, toProblem(ctx, apierr.New(apierr.Forbidden, "a token can only create tokens within its own admin scope"))
			}
			id, secret := auth.New()
			t := meta.Token{ID: id, Name: in.Body.Name, SecretHash: auth.Hash(secret), Scope: scope,
				DatabaseID: dbID, BranchID: branchID, Access: in.Body.Access}
			var requested *time.Time
			if n := in.Body.ExpiresInDays; n > 0 {
				at := time.Now().Add(time.Duration(n) * 24 * time.Hour)
				requested = &at
			}
			t.ExpiresAt = p.ChildExpiry(requested)
			rec, err := s.store.CreateToken(ctx, t)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, rec.ID)
			out := &createTokenOut{}
			out.Body.TokenOut = tokenOut(rec)
			out.Body.Token = secret
			return out, nil
		})

	huma.Register(api, noContent(op("revoke-token", http.MethodDelete, "/v1/tokens/{token_id}", "Revoke a token", "Tokens", "token.revoke")),
		func(ctx context.Context, in *tokenPath) (*struct{}, error) {
			p := principalFrom(ctx)
			setTarget(ctx, in.TokenID)
			t, err := s.store.GetToken(ctx, in.TokenID)
			if errors.Is(err, meta.ErrNoToken) || (err == nil && t.RevokedAt != nil) {
				return nil, toProblem(ctx, apierr.New(apierr.TokenNotFound, "no live token with that id"))
			}
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			if !p.InstanceAdmin() && !p.Covers(t.Scope, t.DatabaseID, t.BranchID, t.Access) {
				return nil, toProblem(ctx, apierr.New(apierr.TokenNotFound, "no live token with that id"))
			}
			if t.Service {
				return nil, toProblem(ctx, apierr.New(apierr.Forbidden, "the service token can't be revoked through the API; replace it with LUMBUNG_SERVICE_TOKEN or the secrets file"))
			}
			if _, err := s.store.RevokeToken(ctx, t.ID); err != nil {
				return nil, toProblem(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("list-audit", http.MethodGet, "/v1/audit", "Read the audit log", "Audit", ""),
		func(ctx context.Context, in *auditIn) (*auditList, error) {
			if err := requireInstanceAdmin(principalFrom(ctx)); err != nil {
				return nil, toProblem(ctx, err)
			}
			var before int64
			if in.Cursor != "" {
				n, err := strconv.ParseInt(in.Cursor, 10, 64)
				if err != nil || n <= 0 {
					return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "invalid cursor"))
				}
				before = n
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 50
			}
			rows, err := s.store.ListAudit(ctx, before, limit+1)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			out := &auditList{}
			out.Body.Data = []AuditOut{}
			for i, e := range rows {
				if i == limit {
					out.Body.NextCursor = strconv.FormatInt(rows[limit-1].ID, 10)
					break
				}
				out.Body.Data = append(out.Body.Data, AuditOut{ID: e.ID, At: e.At, TokenID: e.TokenID, Actor: e.Actor,
					Action: e.Action, Target: e.Target, Result: e.Result, Code: e.Code, RequestID: e.RequestID})
			}
			return out, nil
		})

	huma.Register(api, op("branch-stats", http.MethodGet, "/v1/databases/{db}/branches/{branch}/stats", "Get a branch's activity", "Branches", ""),
		func(ctx context.Context, in *statsIn) (*statsOut, error) {
			_, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			window, _ := time.ParseDuration(in.Window)
			step := time.Minute
			if window > time.Hour {
				step = 15 * time.Minute
			}
			now := time.Now()
			since := now.Add(-window)
			stored, err := s.store.ListStats(ctx, b.ID, since)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			buckets := append(stored, s.stats.Pending(b.ID)...)
			slow, err := s.store.ListSlowQueries(ctx, b.ID)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			out := &statsOut{}
			out.Body.Summary = stats.Summarize(buckets, since, now, step)
			out.Body.SizeBytes = s.eng.BranchBytes(b.ID)
			out.Body.SlowQueries = make([]SlowQueryOut, 0, len(slow))
			for _, q := range slow {
				out.Body.SlowQueries = append(out.Body.SlowQueries, SlowQueryOut{At: q.At, DurationMS: q.DurationMS, SQL: q.SQL, ErrorCode: q.ErrorCode})
			}
			return out, nil
		})
}

func (s *Server) register() {
	s.registerDatabases(s.api)
	s.registerBranches(s.api)
	s.registerQuery(s.api)
	s.registerSnapshots(s.api)
	s.registerTransfer(s.api)
	s.registerTokens(s.api)
	for _, f := range s.extra {
		f(s.api)
	}
}
