package api

// The MCP endpoint (PLAN 8): streamable HTTP at /mcp, the same bearer tokens
// and scopes as REST, every tool annotated, and results capped to fit a
// model's context.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
	"github.com/zakiego/lumbung/apps/engine/internal/value"
	"github.com/zakiego/lumbung/apps/engine/internal/version"
)

// Caps for tool results, sized for a model's context window.
const (
	mcpDefaultRows = 100
	mcpMaxRows     = 1000
	mcpMaxBytes    = 64 << 10
	mcpMaxCell     = 2000 // characters of one text value shown
)

// mountMCP adds /mcp to the router.
func (s *Server) mountMCP() {
	server := mcp.NewServer(&mcp.Implementation{Name: "lumbung", Title: "Lumbung", Version: version.Version}, &mcp.ServerOptions{
		Instructions: "Lumbung holds SQLite databases, each with branches (full copies). Use list_databases first. " +
			"query runs read-only SQL; execute runs writes and needs a write token. Branches never merge back: " +
			"to test a schema change, create_branch, execute the migration there, diff_branches against main, then run the same SQL on main.",
	})
	s.addMCPTools(server)
	s.mcpServer = server
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:           true,
		JSONResponse:        true,
		Logger:              s.log,
		MaxRequestBodyBytes: s.eng.Config().MaxRequestBytes,
		// The localhost Host check guards unauthenticated local servers from
		// DNS rebinding. Every request here needs a bearer token a rebinding
		// page can't have, and behind a proxy the Host is the public name.
		DisableLocalhostProtection: true,
	})
	verify := func(ctx context.Context, token string, r *http.Request) (*mcpauth.TokenInfo, error) {
		p, err := s.authenticate(ctx, "Bearer "+token)
		if err != nil {
			if apierr.As(err).Code == apierr.Unauthorized {
				if ok, _ := s.failures.Allow(s.clientIPFromRequest(r)); !ok {
					return nil, fmt.Errorf("%w: too many failed authentications from this address", mcpauth.ErrInvalidToken)
				}
				return nil, fmt.Errorf("%w: %s", mcpauth.ErrInvalidToken, apierr.As(err).Message)
			}
			return nil, err
		}
		info := &mcpauth.TokenInfo{UserID: p.TokenID, Extra: map[string]any{"principal": p}}
		if p.ExpiresAt != nil {
			info.Expiration = *p.ExpiresAt
		}
		return info, nil
	}
	protected := mcpauth.RequireBearerToken(verify, &mcpauth.RequireBearerTokenOptions{AllowMissingExpiration: true})(handler)
	s.mux.Handle("/mcp", protected)
}

func (s *Server) clientIPFromRequest(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if s.ipHeader == "" {
		return host
	}
	return s.clientIPFrom(host, r.Header.Get(s.ipHeader))
}

// mcpPrincipal is who a tool call acts as, after the per-token rate limit.
func (s *Server) mcpPrincipal(req *mcp.CallToolRequest) (auth.Principal, error) {
	info := req.Extra.TokenInfo
	if info == nil {
		return auth.Principal{}, errors.New("unauthorized: no token")
	}
	p, ok := info.Extra["principal"].(auth.Principal)
	if !ok {
		return auth.Principal{}, errors.New("unauthorized: no token")
	}
	if ok, wait := s.perToken.Allow(p.TokenID); !ok {
		return auth.Principal{}, fmt.Errorf("rate_limited: too many requests for this token, retry in %s", wait.Round(time.Second))
	}
	return p, nil
}

// toolErr turns an engine error into a message a model can act on.
func toolErr(err error) error {
	ae := apierr.As(err)
	if ae.Code == apierr.Internal {
		return errors.New("internal: something failed inside the engine")
	}
	msg := ae.Code + ": " + ae.Message
	if ae.Detail != "" {
		msg += " (" + ae.Detail + ")"
	}
	return errors.New(msg)
}

// audited runs a management tool and records it in the audit log.
func (s *Server) audited(p auth.Principal, action string, f func() (string, error)) error {
	target, err := f()
	e := meta.AuditEntry{TokenID: p.TokenID, Action: action, Target: target, Result: "ok", RequestID: "mcp"}
	if err != nil {
		e.Result, e.Code = "error", apierr.As(err).Code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if aerr := s.store.AddAudit(ctx, e, s.eng.Config().AuditMaxRows); aerr != nil {
		s.log.Error("audit", "err", aerr, "action", action)
	}
	return err
}

func ptr[T any](v T) *T { return &v }

var (
	readOnly    = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}
	additive    = &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}
	destructive = &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(false)}
	idempotentD = &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)}
)

// ---- inputs and outputs

type dbIn struct {
	Database string `json:"database" jsonschema:"database name or id"`
}

type branchIn struct {
	Database string `json:"database" jsonschema:"database name or id"`
	Branch   string `json:"branch,omitempty" jsonschema:"branch name or id; defaults to the database's default branch"`
}

type mcpDatabase struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
	BranchCount   int    `json:"branch_count"`
	SizeBytes     int64  `json:"size_bytes"`
	CreatedAt     string `json:"created_at"`
}

type mcpBranch struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Default   bool   `json:"default"`
	Protected bool   `json:"protected"`
	ParentID  string `json:"parent_id,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
}

type mcpSnapshot struct {
	ID         string `json:"id"`
	BranchName string `json:"branch_name"`
	Kind       string `json:"kind"`
	Label      string `json:"label,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`
	CreatedAt  string `json:"created_at"`
}

type sqlIn struct {
	Database string `json:"database" jsonschema:"database name or id"`
	Branch   string `json:"branch,omitempty" jsonschema:"branch name or id; defaults to the default branch"`
	SQL      string `json:"sql" jsonschema:"exactly one SQL statement"`
	Args     []any  `json:"args,omitempty" jsonschema:"values for ? placeholders, in order: strings, numbers, booleans or null"`
	MaxRows  int    `json:"max_rows,omitempty" jsonschema:"most rows to return, 1 to 1000; default 100"`
}

type execIn struct {
	Database string `json:"database" jsonschema:"database name or id"`
	Branch   string `json:"branch,omitempty" jsonschema:"branch name or id; defaults to the default branch"`
	SQL      string `json:"sql" jsonschema:"exactly one SQL statement"`
	Args     []any  `json:"args,omitempty" jsonschema:"values for ? placeholders, in order: strings, numbers, booleans or null"`
}

type sqlOut struct {
	Columns      []string `json:"columns"`
	Rows         [][]any  `json:"rows" jsonschema:"rows as arrays in column order; integers past 2^53 and blobs are strings"`
	RowCount     int      `json:"row_count"`
	Truncated    bool     `json:"truncated"`
	Note         string   `json:"note,omitempty"`
	RowsAffected int64    `json:"rows_affected,omitempty"`
}

func timeStr(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// plainArgs converts JSON arguments to values. A whole float64 becomes an
// integer, since JSON tool arguments have no separate integer type.
func plainArgs(args []any) ([]value.Value, error) {
	out := make([]value.Value, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case nil:
			out[i] = value.NullValue()
		case bool:
			out[i] = value.IntValue(map[bool]int64{true: 1, false: 0}[v])
		case string:
			out[i] = value.TextValue(v)
		case float64:
			if v == math.Trunc(v) && math.Abs(v) <= 1<<53 {
				out[i] = value.IntValue(int64(v))
			} else {
				out[i] = value.FloatValue(v)
			}
		default:
			return nil, fmt.Errorf("invalid_params: argument %d must be a string, number, boolean or null", i+1)
		}
	}
	return out, nil
}

// plainValue renders a value as plain JSON a model reads easily.
func plainValue(v value.Value) any {
	switch v.Kind {
	case value.Integer:
		if v.Int > 1<<53 || v.Int < -(1<<53) {
			return strconv.FormatInt(v.Int, 10)
		}
		return v.Int
	case value.Float:
		if math.IsInf(v.Float, 0) {
			return strconv.FormatFloat(v.Float, 'g', -1, 64)
		}
		return v.Float
	case value.Text:
		if r := []rune(v.Text); len(r) > mcpMaxCell {
			return string(r[:mcpMaxCell]) + fmt.Sprintf("... (%d characters)", len(r))
		}
		return v.Text
	case value.Blob:
		shown := v.Blob
		if len(shown) > 48 {
			shown = shown[:48]
		}
		return fmt.Sprintf("<blob %d bytes, base64 %s%s>", len(v.Blob), base64.StdEncoding.EncodeToString(shown), map[bool]string{true: "...", false: ""}[len(v.Blob) > 48])
	}
	return nil
}

func sqlResult(r *engine.Result) sqlOut {
	out := sqlOut{Columns: make([]string, len(r.Columns)), Rows: make([][]any, len(r.Rows)), RowCount: len(r.Rows),
		Truncated: r.Truncated, RowsAffected: r.RowsAffected}
	for i, c := range r.Columns {
		out.Columns[i] = c.Name
	}
	for i, row := range r.Rows {
		vs := make([]any, len(row))
		for j, v := range row {
			vs[j] = plainValue(v)
		}
		out.Rows[i] = vs
	}
	if r.Truncated {
		out.Note = fmt.Sprintf("Only the first %d rows are shown. Add a WHERE or LIMIT, or select fewer columns, to see the rest.", len(r.Rows))
	}
	return out
}

func (s *Server) addMCPTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "list_databases", Description: "List the databases this token can see, with their default branch and size.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct {
			Databases []mcpDatabase `json:"databases"`
		}, error) {
			var out struct {
				Databases []mcpDatabase `json:"databases"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			all, err := s.eng.ListDatabases(ctx)
			if err != nil {
				return nil, out, toolErr(err)
			}
			out.Databases = []mcpDatabase{}
			for _, d := range all {
				if !p.SeesDatabase(d.ID) {
					continue
				}
				o, err := s.databaseOut(ctx, d)
				if err != nil {
					return nil, out, toolErr(err)
				}
				out.Databases = append(out.Databases, mcpDatabase{o.ID, o.Name, o.DefaultBranch, o.BranchCount, o.SizeBytes, timeStr(o.CreatedAt)})
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "get_database", Description: "Get one database: its id, default branch, branch count and size.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in dbIn) (*mcp.CallToolResult, mcpDatabase, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpDatabase{}, err
			}
			d, err := s.database(ctx, p, in.Database, auth.AccessRead)
			if err != nil {
				return nil, mcpDatabase{}, toolErr(err)
			}
			o, err := s.databaseOut(ctx, d)
			if err != nil {
				return nil, mcpDatabase{}, toolErr(err)
			}
			return nil, mcpDatabase{o.ID, o.Name, o.DefaultBranch, o.BranchCount, o.SizeBytes, timeStr(o.CreatedAt)}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "create_database", Description: "Create a database with an empty default branch named main. Needs an instance admin token. Names: lowercase letters, digits and hyphens.", Annotations: additive},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Name string `json:"name" jsonschema:"lowercase letters, digits and hyphens, starting with a letter or digit"`
		}) (*mcp.CallToolResult, mcpDatabase, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpDatabase{}, err
			}
			var out mcpDatabase
			err = s.audited(p, "database.create", func() (string, error) {
				if err := requireInstanceAdmin(p); err != nil {
					return "", err
				}
				d, err := s.eng.CreateDatabase(ctx, in.Name)
				if err != nil {
					return "", err
				}
				out = mcpDatabase{ID: d.ID, Name: d.Name, DefaultBranch: "main", BranchCount: 1, CreatedAt: timeStr(d.CreatedAt)}
				return d.ID, nil
			})
			if err != nil {
				return nil, mcpDatabase{}, toolErr(err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "list_branches", Description: "List a database's branches, marking the default and protected ones.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in dbIn) (*mcp.CallToolResult, struct {
			Branches []mcpBranch `json:"branches"`
		}, error) {
			var out struct {
				Branches []mcpBranch `json:"branches"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			d, err := s.database(ctx, p, in.Database, auth.AccessRead)
			if err != nil {
				return nil, out, toolErr(err)
			}
			bs, err := s.eng.ListBranches(ctx, d.ID)
			if err != nil {
				return nil, out, toolErr(err)
			}
			out.Branches = []mcpBranch{}
			for _, b := range bs {
				out.Branches = append(out.Branches, s.mcpBranch(d, b))
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "create_branch", Description: "Create a branch as a full copy of another branch (default: the default branch). Changes on it never merge back. Use it to test migrations safely.", Annotations: additive},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Database      string `json:"database" jsonschema:"database name or id"`
			Name          string `json:"name" jsonschema:"name for the new branch: lowercase letters, digits and hyphens"`
			From          string `json:"from,omitempty" jsonschema:"branch to copy; defaults to the default branch"`
			ExpiresInDays int    `json:"expires_in_days,omitempty" jsonschema:"delete the branch automatically after this many days"`
		}) (*mcp.CallToolResult, mcpBranch, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpBranch{}, err
			}
			var out mcpBranch
			err = s.audited(p, "branch.create", func() (string, error) {
				d, err := s.database(ctx, p, in.Database, auth.AccessWrite)
				if err != nil {
					return "", err
				}
				if in.From != "" {
					if _, _, err := s.branch(ctx, p, d.ID, in.From, auth.AccessRead); err != nil {
						return "", err
					}
				}
				if in.ExpiresInDays < 0 || in.ExpiresInDays > 3650 {
					return "", apierr.New(apierr.BadRequest, "expires_in_days must be 0 to 3650")
				}
				b, err := s.eng.CreateBranch(ctx, d.ID, in.Name, engine.BranchSource{Branch: in.From},
					engine.NewBranchOptions{ExpiresIn: time.Duration(in.ExpiresInDays) * 24 * time.Hour})
				if err != nil {
					return "", err
				}
				out = s.mcpBranch(d, b)
				return d.ID + "/" + b.ID, nil
			})
			if err != nil {
				return nil, mcpBranch{}, toolErr(err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "delete_branch", Description: "Delete a branch and its data. Protected branches and the default branch can't be deleted. This can't be undone.", Annotations: destructive},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Database string `json:"database" jsonschema:"database name or id"`
			Branch   string `json:"branch" jsonschema:"branch name or id to delete"`
		}) (*mcp.CallToolResult, struct {
			Deleted string `json:"deleted"`
		}, error) {
			var out struct {
				Deleted string `json:"deleted"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			err = s.audited(p, "branch.delete", func() (string, error) {
				d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessAdmin)
				if err != nil {
					return "", err
				}
				if err := s.eng.DeleteBranch(ctx, d.ID, b.ID); err != nil {
					return d.ID + "/" + b.ID, err
				}
				out.Deleted = b.Name
				return d.ID + "/" + b.ID, nil
			})
			if err != nil {
				return nil, out, toolErr(err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "reset_branch", Description: "Throw away a branch's changes by copying its parent again. Not allowed on protected branches or the default branch.", Annotations: idempotentD},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Database string `json:"database" jsonschema:"database name or id"`
			Branch   string `json:"branch" jsonschema:"branch name or id to reset"`
		}) (*mcp.CallToolResult, mcpBranch, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpBranch{}, err
			}
			var out mcpBranch
			err = s.audited(p, "branch.reset", func() (string, error) {
				d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessWrite)
				if err != nil {
					return "", err
				}
				if !p.AtLeast(auth.AccessWrite, d.ID, "") {
					return "", apierr.New(apierr.Forbidden, "resetting needs write access to the whole database")
				}
				b, err = s.eng.ResetBranch(ctx, d.ID, b.ID)
				if err != nil {
					return d.ID + "/" + b.ID, err
				}
				out = s.mcpBranch(d, b)
				return d.ID + "/" + b.ID, nil
			})
			if err != nil {
				return nil, mcpBranch{}, toolErr(err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "diff_branches", Description: "Compare the schema (tables, columns, indexes, views, triggers) of a branch against another branch. Data isn't compared.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Database string `json:"database" jsonschema:"database name or id"`
			Branch   string `json:"branch" jsonschema:"the branch with the changes"`
			Against  string `json:"against,omitempty" jsonschema:"the branch to compare with; defaults to the default branch"`
		}) (*mcp.CallToolResult, struct {
			Branch  string          `json:"branch"`
			Against string          `json:"against"`
			Changes []engine.Change `json:"changes"`
		}, error) {
			var out struct {
				Branch  string          `json:"branch"`
				Against string          `json:"against"`
				Changes []engine.Change `json:"changes"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, out, toolErr(err)
			}
			_, a, err := s.branch(ctx, p, d.ID, in.Against, auth.AccessRead)
			if err != nil {
				return nil, out, toolErr(err)
			}
			diff, err := s.eng.DiffBranches(ctx, d.ID, b.ID, a.ID)
			if err != nil {
				return nil, out, toolErr(err)
			}
			out.Branch, out.Against, out.Changes = diff.Branch, diff.Against, diff.Changes
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "get_schema", Description: "Get a branch's schema: tables with their columns, indexes, views and triggers, with the CREATE statements.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in branchIn) (*mcp.CallToolResult, *engine.Schema, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, nil, err
			}
			d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, nil, toolErr(err)
			}
			sc, err := s.eng.Schema(ctx, engine.Target{Database: d.ID, Branch: b.ID})
			if err != nil {
				return nil, nil, toolErr(err)
			}
			return nil, sc, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "query", Description: "Run one read-only SQL statement (SELECT, PRAGMA table_info, ...) on a read-only connection. Returns at most max_rows rows (default 100) and about 64 KB; truncated results say so.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in sqlIn) (*mcp.CallToolResult, sqlOut, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, sqlOut{}, err
			}
			d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, sqlOut{}, toolErr(err)
			}
			args, err := plainArgs(in.Args)
			if err != nil {
				return nil, sqlOut{}, err
			}
			maxRows := in.MaxRows
			if maxRows <= 0 {
				maxRows = mcpDefaultRows
			}
			maxRows = min(maxRows, mcpMaxRows)
			start := time.Now()
			r, err := s.eng.QueryCapped(ctx, engine.Target{Database: d.ID, Branch: b.ID}, engine.Statement{SQL: in.SQL, Args: args}, maxRows, mcpMaxBytes)
			s.record(b.ID, []StatementIn{{SQL: in.SQL}}, start, results(r), err)
			if err != nil {
				return nil, sqlOut{}, toolErr(err)
			}
			return nil, sqlResult(r), nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "execute", Description: "Run one SQL statement that changes data or schema (INSERT, UPDATE, DELETE, CREATE, ALTER, DROP) in its own transaction. Needs a write token. Prefer testing schema changes on a branch first.", Annotations: destructive},
		func(ctx context.Context, req *mcp.CallToolRequest, in execIn) (*mcp.CallToolResult, sqlOut, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, sqlOut{}, err
			}
			d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessWrite)
			if err != nil {
				return nil, sqlOut{}, toolErr(err)
			}
			args, err := plainArgs(in.Args)
			if err != nil {
				return nil, sqlOut{}, err
			}
			start := time.Now()
			r, err := s.eng.Query(ctx, engine.Target{Database: d.ID, Branch: b.ID}, engine.Write, engine.Statement{SQL: in.SQL, Args: args})
			s.record(b.ID, []StatementIn{{SQL: in.SQL}}, start, results(r), err)
			if err != nil {
				return nil, sqlOut{}, toolErr(err)
			}
			if len(r.Rows) > mcpMaxRows {
				r.Rows, r.Truncated = r.Rows[:mcpMaxRows], true
			}
			return nil, sqlResult(r), nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "list_snapshots", Description: "List a branch's snapshots, newest first.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in branchIn) (*mcp.CallToolResult, struct {
			Snapshots []mcpSnapshot `json:"snapshots"`
		}, error) {
			var out struct {
				Snapshots []mcpSnapshot `json:"snapshots"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, out, toolErr(err)
			}
			sns, err := s.eng.ListSnapshots(ctx, engine.Target{Database: d.ID, Branch: b.ID})
			if err != nil {
				return nil, out, toolErr(err)
			}
			out.Snapshots = []mcpSnapshot{}
			for i, sn := range sns {
				if i == 200 {
					break
				}
				out.Snapshots = append(out.Snapshots, mcpSnapshot{sn.ID, sn.BranchName, sn.Kind, sn.Label, sn.SizeBytes, timeStr(sn.CreatedAt)})
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "create_snapshot", Description: "Take a snapshot of a branch. Restoring one later makes a new branch; it never overwrites.", Annotations: additive},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Database string `json:"database" jsonschema:"database name or id"`
			Branch   string `json:"branch,omitempty" jsonschema:"branch name or id; defaults to the default branch"`
			Label    string `json:"label,omitempty" jsonschema:"a note to find the snapshot by"`
		}) (*mcp.CallToolResult, mcpSnapshot, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpSnapshot{}, err
			}
			var out mcpSnapshot
			err = s.audited(p, "snapshot.create", func() (string, error) {
				d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessWrite)
				if err != nil {
					return "", err
				}
				if len(in.Label) > 200 {
					return "", apierr.New(apierr.BadRequest, "a label is at most 200 bytes")
				}
				sn, err := s.eng.CreateSnapshot(ctx, engine.Target{Database: d.ID, Branch: b.ID}, in.Label)
				if err != nil {
					return d.ID + "/" + b.ID, err
				}
				out = mcpSnapshot{sn.ID, sn.BranchName, sn.Kind, sn.Label, sn.SizeBytes, timeStr(sn.CreatedAt)}
				return d.ID + "/" + b.ID + "/" + sn.ID, nil
			})
			if err != nil {
				return nil, mcpSnapshot{}, toolErr(err)
			}
			return nil, out, nil
		})
}

func results(r *engine.Result) []*engine.Result {
	if r == nil {
		return nil
	}
	return []*engine.Result{r}
}

func (s *Server) mcpBranch(d meta.Database, b meta.Branch) mcpBranch {
	out := mcpBranch{ID: b.ID, Name: b.Name, Default: b.ID == d.DefaultBranchID, Protected: b.Protected, ParentID: b.ParentID, SizeBytes: s.eng.BranchBytes(b.ID)}
	if b.ExpiresAt != nil {
		out.ExpiresAt = timeStr(*b.ExpiresAt)
	}
	return out
}

// MCPServer returns the MCP server, for generating the tools reference.
func (s *Server) MCPServer() *mcp.Server { return s.mcpServer }
