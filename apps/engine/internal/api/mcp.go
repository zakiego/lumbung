package api

// The MCP endpoint (PLAN 8): streamable HTTP at /mcp, the same bearer tokens
// and scopes as REST, every tool annotated, and results capped to fit a
// model's context.

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	mcpListMax     = 200  // items in a list result
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
	// mcpGate has already authenticated the request; the verifier only hands
	// its principal to the go-sdk, which passes it to tool handlers.
	verify := func(_ context.Context, _ string, r *http.Request) (*mcpauth.TokenInfo, error) {
		c, ok := r.Context().Value(keyMCPCall{}).(mcpCall)
		if !ok {
			return nil, mcpauth.ErrInvalidToken
		}
		info := &mcpauth.TokenInfo{UserID: c.p.TokenID, Extra: map[string]any{"call": c}}
		if c.p.ExpiresAt != nil {
			info.Expiration = *c.p.ExpiresAt
		}
		return info, nil
	}
	protected := mcpauth.RequireBearerToken(verify, &mcpauth.RequireBearerTokenOptions{AllowMissingExpiration: true})(handler)
	s.mux.Handle("/mcp", s.mcpGate(protected))
}

// mcpCall is who an MCP request acts as and the id it's logged under.
type mcpCall struct {
	p         auth.Principal
	requestID string
}

type keyMCPCall struct{}

// mcpGate does for /mcp what middleware does for REST: a request id,
// authentication with the failed-attempt limit per address, the per-token
// rate limit (for every MCP message, not only tool calls) and the actor
// header for the service token. Failures are problem+json, as on REST.
func (s *Server) mcpGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !requestIDRe.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		p, err := s.authenticate(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			ae := apierr.As(err)
			if ae.Code == apierr.Unauthorized {
				if ok, wait := s.failures.Allow(s.clientIPFromRequest(r)); !ok {
					w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
					ae = apierr.New(apierr.RateLimited, "too many failed authentications from this address")
				}
			} else {
				s.log.Error("authenticate", "err", err, "request_id", id)
				ae = apierr.New(apierr.Internal, "internal error")
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="lumbung"`)
			writeProblemHTTP(w, ae, id)
			return
		}
		if ok, wait := s.perToken.Allow(p.TokenID); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			writeProblemHTTP(w, apierr.New(apierr.RateLimited, "too many requests for this token"), id)
			return
		}
		if a := r.Header.Get("X-Lumbung-Actor"); a != "" && p.Service && actorRe.MatchString(a) {
			p.Actor = a
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyMCPCall{}, mcpCall{p: p, requestID: id})))
	})
}

func writeProblemHTTP(w http.ResponseWriter, e *apierr.Error, requestID string) {
	p := problemFrom(e)
	p.RequestID = requestID
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
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

// mcpCallOf is who a tool call acts as, set by mcpGate.
func mcpCallOf(req *mcp.CallToolRequest) (mcpCall, error) {
	if info := req.Extra.TokenInfo; info != nil {
		if c, ok := info.Extra["call"].(mcpCall); ok {
			return c, nil
		}
	}
	return mcpCall{}, errors.New("unauthorized: no token")
}

// mcpPrincipal is who a tool call acts as.
func (s *Server) mcpPrincipal(req *mcp.CallToolRequest) (auth.Principal, error) {
	c, err := mcpCallOf(req)
	return c.p, err
}

// toolErr turns an engine error into a message a model can act on. Internal
// errors are logged with the request id and shown as a generic message.
func (s *Server) toolErr(req *mcp.CallToolRequest, err error) error {
	ae := apierr.As(err)
	if ae.Code == apierr.Internal {
		c, _ := mcpCallOf(req)
		s.log.Error("mcp tool", "err", err, "tool", req.Params.Name, "request_id", c.requestID)
		return fmt.Errorf("internal: something failed inside the engine (request id %s)", c.requestID)
	}
	msg := ae.Code + ": " + ae.Message
	if ae.Detail != "" {
		msg += " (" + ae.Detail + ")"
	}
	return errors.New(msg)
}

// audited runs a management tool and records it in the audit log.
func (s *Server) audited(req *mcp.CallToolRequest, action string, f func() (string, error)) error {
	c, _ := mcpCallOf(req)
	target, err := f()
	e := meta.AuditEntry{TokenID: c.p.TokenID, Actor: c.p.Actor, Action: action, Target: target, Result: "ok", RequestID: c.requestID}
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

type schemaIn struct {
	Database string `json:"database" jsonschema:"database name or id"`
	Branch   string `json:"branch,omitempty" jsonschema:"branch name or id; defaults to the default branch"`
	Table    string `json:"table,omitempty" jsonschema:"only this table, with its indexes and triggers"`
}

type mcpSchema struct {
	Tables    []engine.Table   `json:"tables"`
	Indexes   []engine.Index   `json:"indexes"`
	Views     []engine.View    `json:"views"`
	Triggers  []engine.Trigger `json:"triggers"`
	NamesOnly bool             `json:"names_only" jsonschema:"true when the schema was too large and only names are listed"`
	Truncated bool             `json:"truncated"`
	Note      string           `json:"note,omitempty"`
}

// schemaResult filters a schema to one table when asked, and falls back to
// names only when the full schema wouldn't fit mcpMaxBytes.
func schemaResult(sc *engine.Schema, table string) (mcpSchema, error) {
	out := mcpSchema{Tables: sc.Tables, Indexes: sc.Indexes, Views: sc.Views, Triggers: sc.Triggers}
	if table != "" {
		out = mcpSchema{Tables: []engine.Table{}, Indexes: []engine.Index{}, Views: []engine.View{}, Triggers: []engine.Trigger{}}
		for _, t := range sc.Tables {
			if t.Name == table {
				out.Tables = append(out.Tables, t)
			}
		}
		for _, v := range sc.Views {
			if v.Name == table {
				out.Views = append(out.Views, v)
			}
		}
		if len(out.Tables)+len(out.Views) == 0 {
			return mcpSchema{}, apierr.Newf(apierr.NotFound, "no table or view named %q", table)
		}
		for _, ix := range sc.Indexes {
			if ix.Table == table {
				out.Indexes = append(out.Indexes, ix)
			}
		}
		for _, tr := range sc.Triggers {
			if tr.Table == table {
				out.Triggers = append(out.Triggers, tr)
			}
		}
	}
	if b, _ := json.Marshal(out); len(b) <= mcpMaxBytes {
		return out, nil
	}
	names := mcpSchema{NamesOnly: true, Tables: []engine.Table{}, Indexes: []engine.Index{}, Views: []engine.View{}, Triggers: []engine.Trigger{}}
	for _, t := range out.Tables {
		names.Tables = append(names.Tables, engine.Table{Name: t.Name, Virtual: t.Virtual, Columns: []engine.ColumnInfo{}})
	}
	for _, ix := range out.Indexes {
		names.Indexes = append(names.Indexes, engine.Index{Name: ix.Name, Table: ix.Table, Columns: []string{}})
	}
	for _, v := range out.Views {
		names.Views = append(names.Views, engine.View{Name: v.Name})
	}
	for _, tr := range out.Triggers {
		names.Triggers = append(names.Triggers, engine.Trigger{Name: tr.Name, Table: tr.Table})
	}
	var cut [4]bool
	names.Tables, cut[0] = capList(names.Tables)
	names.Indexes, cut[1] = capList(names.Indexes)
	names.Views, cut[2] = capList(names.Views)
	names.Triggers, cut[3] = capList(names.Triggers)
	names.Truncated = cut[0] || cut[1] || cut[2] || cut[3]
	names.Note = "The schema is too large to show in full, so only names are listed. Pass table to get one table with its columns and CREATE statement."
	if names.Truncated {
		names.Note += fmt.Sprintf(" Only the first %d names of each kind are shown; query sqlite_schema to list the rest.", mcpListMax)
	}
	return names, nil
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
			// Arguments reach the handler as float64, which can't hold every
			// integer past 2^53: binding the rounded number would match a
			// different row. Such integers must come as strings; SQLite's
			// column affinity still compares them as numbers.
			switch {
			case v != math.Trunc(v):
				out[i] = value.FloatValue(v)
			case math.Abs(v) < 1<<53:
				out[i] = value.IntValue(int64(v))
			default:
				return nil, fmt.Errorf("invalid_params: argument %d is a whole number too large to pass exactly as JSON; pass integers at or past 2^53 as strings, like \"9007199254740993\"", i+1)
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

// sqlResult renders a result for a model: at most maxRows rows, and rows
// only while the rendered JSON stays under mcpMaxBytes. Sizes are measured
// after long text and blobs are shortened, so a wide cell costs what it
// shows. write says the statement changed data, which changes the note: the
// write applied in full even when not every returned row is shown.
func sqlResult(r *engine.Result, maxRows int, write bool) sqlOut {
	out := sqlOut{Columns: make([]string, len(r.Columns)), Rows: [][]any{}, Truncated: r.Truncated, RowsAffected: r.RowsAffected}
	for i, c := range r.Columns {
		out.Columns[i] = c.Name
	}
	used := 0
	for i, row := range r.Rows {
		if i >= maxRows {
			out.Truncated = true
			break
		}
		vs := make([]any, len(row))
		for j, v := range row {
			vs[j] = plainValue(v)
		}
		b, _ := json.Marshal(vs)
		if used+len(b) > mcpMaxBytes {
			out.Truncated = true
			break
		}
		used += len(b)
		out.Rows = append(out.Rows, vs)
	}
	out.RowCount = len(out.Rows)
	switch {
	case !out.Truncated:
	case write:
		out.Note = fmt.Sprintf("The statement applied to all %d rows; only the first %d returned rows are shown.", r.RowsAffected, len(out.Rows))
	case len(out.Rows) == 0:
		out.Note = "A single row is too wide to show. Select fewer or narrower columns (substr() shortens text)."
	default:
		out.Note = fmt.Sprintf("Only the first %d rows are shown. Add a WHERE or LIMIT, or select fewer columns, to see the rest.", len(out.Rows))
	}
	return out
}

// capList keeps items while there are at most mcpListMax of them and their
// JSON stays under mcpMaxBytes, and says whether any were dropped.
func capList[T any](items []T) ([]T, bool) {
	used := 0
	for i, it := range items {
		b, _ := json.Marshal(it)
		if i >= mcpListMax || used+len(b) > mcpMaxBytes {
			return items[:i], true
		}
		used += len(b)
	}
	return items, false
}

func listNote(truncated bool, shown int, hint string) string {
	if !truncated {
		return ""
	}
	return fmt.Sprintf("Only the first %d are shown. %s", shown, hint)
}

func (s *Server) addMCPTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "list_databases", Description: "List the databases this token can see, with their default branch and size.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct {
			Databases []mcpDatabase `json:"databases"`
			Truncated bool          `json:"truncated"`
			Note      string        `json:"note,omitempty"`
		}, error) {
			var out struct {
				Databases []mcpDatabase `json:"databases"`
				Truncated bool          `json:"truncated"`
				Note      string        `json:"note,omitempty"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			all, err := s.eng.ListDatabases(ctx)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			list := []mcpDatabase{}
			for _, d := range all {
				if !p.SeesDatabase(d.ID) {
					continue
				}
				if len(list) == mcpListMax {
					out.Truncated = true
					break
				}
				o, err := s.mcpDatabaseOf(ctx, d)
				if err != nil {
					return nil, out, s.toolErr(req, err)
				}
				list = append(list, o)
			}
			var cut bool
			out.Databases, cut = capList(list)
			out.Truncated = out.Truncated || cut
			out.Note = listNote(out.Truncated, len(out.Databases), "Use get_database with a name to look up one database.")
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
				return nil, mcpDatabase{}, s.toolErr(req, err)
			}
			o, err := s.mcpDatabaseOf(ctx, d)
			if err != nil {
				return nil, mcpDatabase{}, s.toolErr(req, err)
			}
			return nil, o, nil
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
			err = s.audited(req, "database.create", func() (string, error) {
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
				return nil, mcpDatabase{}, s.toolErr(req, err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "list_branches", Description: "List a database's branches, marking the default and protected ones.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in dbIn) (*mcp.CallToolResult, struct {
			Branches  []mcpBranch `json:"branches"`
			Truncated bool        `json:"truncated"`
			Note      string      `json:"note,omitempty"`
		}, error) {
			var out struct {
				Branches  []mcpBranch `json:"branches"`
				Truncated bool        `json:"truncated"`
				Note      string      `json:"note,omitempty"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			d, err := s.database(ctx, p, in.Database, auth.AccessRead)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			bs, err := s.eng.ListBranches(ctx, d.ID)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			list := []mcpBranch{}
			for i, b := range bs {
				if i == mcpListMax {
					out.Truncated = true
					break
				}
				list = append(list, s.mcpBranch(d, b))
			}
			var cut bool
			out.Branches, cut = capList(list)
			out.Truncated = out.Truncated || cut
			out.Note = listNote(out.Truncated, len(out.Branches), "Pass a branch name to the other tools to work with one directly.")
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
			err = s.audited(req, "branch.create", func() (string, error) {
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
				return nil, mcpBranch{}, s.toolErr(req, err)
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
			err = s.audited(req, "branch.delete", func() (string, error) {
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
				return nil, out, s.toolErr(req, err)
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
			err = s.audited(req, "branch.reset", func() (string, error) {
				d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessWrite)
				if err != nil {
					return "", err
				}
				if !p.AtLeast(auth.AccessWrite, d.ID, "") {
					return "", apierr.New(apierr.Forbidden, "resetting needs write access to the whole database")
				}
				target := d.ID + "/" + b.ID
				reset, err := s.eng.ResetBranch(ctx, d.ID, b.ID)
				if err != nil {
					return target, err
				}
				out = s.mcpBranch(d, reset)
				return target, nil
			})
			if err != nil {
				return nil, mcpBranch{}, s.toolErr(req, err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "diff_branches", Description: "Compare the schema (tables, columns, indexes, views, triggers) of a branch against another branch. Data isn't compared.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Database string `json:"database" jsonschema:"database name or id"`
			Branch   string `json:"branch" jsonschema:"the branch with the changes"`
			Against  string `json:"against,omitempty" jsonschema:"the branch to compare with; defaults to the default branch"`
		}) (*mcp.CallToolResult, struct {
			Branch    string          `json:"branch"`
			Against   string          `json:"against"`
			Changes   []engine.Change `json:"changes"`
			Truncated bool            `json:"truncated"`
			Note      string          `json:"note,omitempty"`
		}, error) {
			var out struct {
				Branch    string          `json:"branch"`
				Against   string          `json:"against"`
				Changes   []engine.Change `json:"changes"`
				Truncated bool            `json:"truncated"`
				Note      string          `json:"note,omitempty"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			_, a, err := s.branch(ctx, p, d.ID, in.Against, auth.AccessRead)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			diff, err := s.eng.DiffBranches(ctx, d.ID, b.ID, a.ID)
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			out.Branch, out.Against = diff.Branch, diff.Against
			out.Changes, out.Truncated = capList(diff.Changes)
			out.Note = listNote(out.Truncated, len(out.Changes), "Use get_schema with a table on each branch to compare the rest.")
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "get_schema", Description: "Get a branch's schema: tables with their columns, indexes, views and triggers, with the CREATE statements. Pass table to get one table with its indexes and triggers. A schema over about 64 KB comes back as names only.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in schemaIn) (*mcp.CallToolResult, mcpSchema, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpSchema{}, err
			}
			d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, mcpSchema{}, s.toolErr(req, err)
			}
			sc, err := s.eng.Schema(ctx, engine.Target{Database: d.ID, Branch: b.ID})
			if err != nil {
				return nil, mcpSchema{}, s.toolErr(req, err)
			}
			out, err := schemaResult(sc, in.Table)
			if err != nil {
				return nil, mcpSchema{}, s.toolErr(req, err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "query", Description: "Run one read-only SQL statement (SELECT, PRAGMA table_info, ...) on a read-only connection. Returns at most max_rows rows (default 100) and about 64 KB; truncated results say so.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, in sqlIn) (*mcp.CallToolResult, sqlOut, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, sqlOut{}, err
			}
			d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, sqlOut{}, s.toolErr(req, err)
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
			// The engine keeps one row past the display caps so a cut is
			// reported; sqlResult then trims rows to fit mcpMaxBytes as shown.
			r, err := s.eng.QueryCapped(ctx, engine.Target{Database: d.ID, Branch: b.ID}, engine.Statement{SQL: in.SQL, Args: args}, maxRows, s.eng.Config().MaxResultBytes)
			s.record(b.ID, []StatementIn{{SQL: in.SQL}}, start, results(r), err)
			if err != nil {
				return nil, sqlOut{}, s.toolErr(req, err)
			}
			return nil, sqlResult(r, maxRows, false), nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "execute", Description: "Run one SQL statement that changes data or schema (INSERT, UPDATE, DELETE, CREATE, ALTER, DROP) in its own transaction. Needs a write token. Prefer testing schema changes on a branch first. Rows from RETURNING are shown up to 1000 rows and about 64 KB.", Annotations: destructive},
		func(ctx context.Context, req *mcp.CallToolRequest, in execIn) (*mcp.CallToolResult, sqlOut, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, sqlOut{}, err
			}
			d, b, err := s.branch(ctx, p, in.Database, in.Branch, auth.AccessWrite)
			if err != nil {
				return nil, sqlOut{}, s.toolErr(req, err)
			}
			args, err := plainArgs(in.Args)
			if err != nil {
				return nil, sqlOut{}, err
			}
			start := time.Now()
			r, err := s.eng.Query(ctx, engine.Target{Database: d.ID, Branch: b.ID}, engine.Write, engine.Statement{SQL: in.SQL, Args: args})
			s.record(b.ID, []StatementIn{{SQL: in.SQL}}, start, results(r), err)
			if err != nil {
				return nil, sqlOut{}, s.toolErr(req, err)
			}
			return nil, sqlResult(r, mcpMaxRows, true), nil
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
				return nil, out, s.toolErr(req, err)
			}
			sns, err := s.eng.ListSnapshots(ctx, engine.Target{Database: d.ID, Branch: b.ID})
			if err != nil {
				return nil, out, s.toolErr(req, err)
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
			err = s.audited(req, "snapshot.create", func() (string, error) {
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
				return nil, mcpSnapshot{}, s.toolErr(req, err)
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

// mcpDatabaseOf describes a database without the last-activity lookup the
// REST view does, which the MCP tools don't show.
func (s *Server) mcpDatabaseOf(ctx context.Context, d meta.Database) (mcpDatabase, error) {
	out := mcpDatabase{ID: d.ID, Name: d.Name, CreatedAt: timeStr(d.CreatedAt)}
	branches, err := s.store.ListBranches(ctx, d.ID)
	if err != nil {
		return mcpDatabase{}, err
	}
	for _, b := range branches {
		if b.ID == d.DefaultBranchID {
			out.DefaultBranch = b.Name
		}
		out.SizeBytes += s.eng.BranchBytes(b.ID)
	}
	out.BranchCount = len(branches)
	return out, nil
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
