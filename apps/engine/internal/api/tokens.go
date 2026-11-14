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

// GrantOut is one thing a token reaches.
type GrantOut struct {
	DatabaseID string `json:"database_id" doc:"The database this grant reaches. Empty for a bucket grant."`
	BranchID   string `json:"branch_id" doc:"When set, the grant only reaches this branch."`
	BucketID   string `json:"bucket_id" doc:"The bucket this grant reaches. Empty for a database grant."`
	Access     string `json:"access" enum:"read,write,admin"`
}

// TokenOut is a token, without its secret.
type TokenOut struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Scope       string     `json:"scope" enum:"instance,database,bucket,grants"`
	DatabaseID  string     `json:"database_id" doc:"For a database token, the database it reaches."`
	BranchID    string     `json:"branch_id" doc:"When set, the token only reaches this branch."`
	BucketID    string     `json:"bucket_id" doc:"For a bucket token, the bucket it reaches."`
	Grants      []GrantOut `json:"grants" doc:"Everything the token reaches below the instance: one entry for a database or bucket token, several for a grants token, none for an instance token."`
	AccessKeyID string     `json:"access_key_id" doc:"The S3 access key id, when the token has an access key."`
	Access      string     `json:"access" enum:"read,write,admin" doc:"The token's level. For a grants token, the highest level among its grants; each grant has its own."`
	Service     bool       `json:"service" doc:"The dashboard's service token."`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
}

func tokenOut(t meta.Token) TokenOut {
	out := TokenOut{ID: t.ID, Name: t.Name, Scope: t.Scope, DatabaseID: t.DatabaseID, BranchID: t.BranchID, BucketID: t.BucketID,
		Access: t.Access, Service: t.Service, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, Grants: []GrantOut{}}
	for _, g := range auth.TokenGrants(t) {
		out.Grants = append(out.Grants, GrantOut{DatabaseID: g.DatabaseID, BranchID: g.BranchID, BucketID: g.BucketID, Access: g.Access})
	}
	if len(t.AccessSecret) > 0 {
		out.AccessKeyID = auth.AccessKeyID(t.ID)
	}
	return out
}

type tokenList struct {
	Body struct {
		Data       []TokenOut `json:"data"`
		NextCursor string     `json:"next_cursor"`
	}
}

// GrantIn is one thing a new token reaches: a database (or one branch of
// it) or a bucket.
type GrantIn struct {
	Database string `json:"database,omitempty" doc:"Database name or id. Not with bucket."`
	Branch   string `json:"branch,omitempty" doc:"Branch name or id, to limit the grant to one branch of the database."`
	Bucket   string `json:"bucket,omitempty" doc:"Bucket name or id. Not with database."`
	Access   string `json:"access" enum:"read,write,admin"`
}

// createTokenIn takes no Idempotency-Key: replaying a create would have to
// store the secret, which is only ever shown once.
type createTokenIn struct {
	Body struct {
		Name     string `json:"name" minLength:"1" maxLength:"100"`
		Database string `json:"database,omitempty" doc:"Database name or id. Omit for an instance token."`
		Branch   string `json:"branch,omitempty" doc:"Branch name or id, to limit a database token to one branch."`
		Bucket   string `json:"bucket,omitempty" doc:"Bucket name or id, for a bucket token. Not with database."`
		// At most 20: every request by the token checks the list, so it stays short.
		Grants        []GrantIn `json:"grants,omitempty" maxItems:"20" doc:"Reach several databases and buckets with one token, each at its own level. Use it instead of database, branch, bucket and access."`
		AccessKey     bool      `json:"access_key,omitempty" doc:"Also make an S3 access key for this token. Its secret is shown once, like the token."`
		Access        string    `json:"access,omitempty" enum:"read,write,admin" doc:"The token's level. Required unless grants is set."`
		ExpiresInDays int       `json:"expires_in_days,omitempty" minimum:"0" maximum:"3650" doc:"0: never expires. A token that expires can't create one that outlives it: the expiry is capped at the creator's."`
	}
}

type createTokenOut struct {
	Body struct {
		TokenOut
		Token           string `json:"token" doc:"The full token. Shown once; only its hash is stored."`
		SecretAccessKey string `json:"secret_access_key,omitempty" doc:"The S3 secret access key, when access_key was set. Shown once."`
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
			if !p.HasAdmin() {
				return nil, toProblem(ctx, apierr.New(apierr.Forbidden, "listing tokens needs an admin token"))
			}
			// Tokens are few; read them all, then filter and page.
			all, err := s.store.ListTokens(ctx, "", "", 100000)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			visible := []meta.Token{}
			for _, t := range all {
				if p.InstanceAdmin() || p.CoversToken(t.Scope, auth.TokenGrants(t)) {
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
			body := in.Body
			var grants []auth.Grant
			switch {
			case len(body.Grants) > 0:
				if body.Database != "" || body.Branch != "" || body.Bucket != "" || body.Access != "" {
					return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "send grants, or database, branch, bucket and access, not both"))
				}
				for _, g := range body.Grants {
					grant, err := s.resolveGrant(ctx, p, g)
					if err != nil {
						return nil, toProblem(ctx, err)
					}
					for _, seen := range grants {
						if seen.DatabaseID != grant.DatabaseID || seen.BucketID != grant.BucketID {
							continue
						}
						if seen.BranchID == grant.BranchID {
							return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "two grants name the same database, branch or bucket"))
						}
						// A grant for the whole database already reaches every
						// branch at its level, so a branch grant next to it
						// would only look like a limit.
						if seen.BranchID == "" || grant.BranchID == "" {
							return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "a grant for a whole database already covers its branches; grant the branches one by one instead"))
						}
					}
					grants = append(grants, grant)
				}
			case body.Access == "":
				return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "access is required"))
			case body.Bucket != "" && (body.Database != "" || body.Branch != ""):
				return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "to reach a database and a bucket with one token, send them as grants"))
			case body.Bucket != "" || body.Database != "":
				grant, err := s.resolveGrant(ctx, p, GrantIn{Database: body.Database, Branch: body.Branch, Bucket: body.Bucket, Access: body.Access})
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				grants = []auth.Grant{grant}
			case body.Branch != "":
				return nil, toProblem(ctx, apierr.New(apierr.BadRequest, "a branch needs a database"))
			}
			// One grant is a plain database or bucket token, however it was
			// asked for, so each reach has one way to be stored.
			t := meta.Token{Name: body.Name, Scope: auth.ScopeInstance, Access: body.Access}
			switch {
			case len(grants) == 1 && grants[0].BucketID != "":
				t.Scope, t.BucketID, t.Access = auth.ScopeBucket, grants[0].BucketID, grants[0].Access
			case len(grants) == 1:
				t.Scope, t.DatabaseID, t.BranchID, t.Access = auth.ScopeDatabase, grants[0].DatabaseID, grants[0].BranchID, grants[0].Access
			case len(grants) > 1:
				t.Scope, t.Grants, t.Access = auth.ScopeGrants, grants, highestAccess(grants)
			}
			if !p.CoversToken(t.Scope, grants) {
				return nil, toProblem(ctx, apierr.New(apierr.Forbidden, "a token can only create tokens within its own admin scope"))
			}
			id, secret := auth.New()
			t.ID, t.SecretHash = id, auth.Hash(secret)
			var accessSecret string
			if body.AccessKey {
				accessSecret = auth.NewAccessSecret()
				t.AccessSecret = s.eng.Sealer().Seal(id, accessSecret)
			}
			var requested *time.Time
			if n := body.ExpiresInDays; n > 0 {
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
			out.Body.SecretAccessKey = accessSecret
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
			if !p.InstanceAdmin() && !p.CoversToken(t.Scope, auth.TokenGrants(t)) {
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

// resolveGrant turns the names in a grant into ids. A database or bucket the
// principal can't see is not found, so tokens don't learn which names exist.
func (s *Server) resolveGrant(ctx context.Context, p auth.Principal, g GrantIn) (auth.Grant, error) {
	switch {
	case g.Bucket != "" && (g.Database != "" || g.Branch != ""):
		return auth.Grant{}, apierr.New(apierr.BadRequest, "a grant reaches a database or a bucket, not both")
	case g.Bucket != "":
		b, err := s.eng.GetBucket(ctx, g.Bucket)
		if err != nil || !p.SeesBucket(b.ID) {
			return auth.Grant{}, apierr.Newf(apierr.BucketNotFound, "no bucket %q", g.Bucket)
		}
		return auth.Grant{BucketID: b.ID, Access: g.Access}, nil
	case g.Database != "":
		d, err := s.eng.GetDatabase(ctx, g.Database)
		if err != nil || !p.SeesDatabase(d.ID) {
			return auth.Grant{}, apierr.Newf(apierr.DatabaseNotFound, "no database %q", g.Database)
		}
		out := auth.Grant{DatabaseID: d.ID, Access: g.Access}
		if g.Branch != "" {
			b, err := s.eng.GetBranch(ctx, d.ID, g.Branch)
			if !p.WholeDatabase(d.ID) && (err != nil || !p.SeesBranch(d.ID, b.ID)) {
				// The same answer for a missing branch and someone else's,
				// so a branch token can't probe for branch names.
				return auth.Grant{}, apierr.New(apierr.Forbidden, "this token can't do that on this branch")
			}
			if err != nil {
				return auth.Grant{}, err
			}
			out.BranchID = b.ID
		}
		return out, nil
	case g.Branch != "":
		return auth.Grant{}, apierr.New(apierr.BadRequest, "a branch needs a database")
	}
	return auth.Grant{}, apierr.New(apierr.BadRequest, "a grant needs a database or a bucket")
}

// highestAccess is the level shown for a grants token as a whole.
func highestAccess(grants []auth.Grant) string {
	out := auth.AccessRead
	for _, g := range grants {
		if g.Access == auth.AccessAdmin || (g.Access == auth.AccessWrite && out == auth.AccessRead) {
			out = g.Access
		}
	}
	return out
}

func (s *Server) register() {
	s.registerDatabases(s.api)
	s.registerBranches(s.api)
	s.registerQuery(s.api)
	s.registerSnapshots(s.api)
	s.registerTransfer(s.api)
	s.registerTokens(s.api)
	s.registerBuckets(s.api)
	for _, f := range s.extra {
		f(s.api)
	}
}
