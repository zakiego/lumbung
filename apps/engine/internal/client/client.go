// Package client is the Go client the lumbung CLI uses to talk to the REST
// API. It mirrors the response shapes it needs and returns *Error with the
// API's stable code.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/value"
)

// Client calls one Lumbung instance with one token.
type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
	// UserAgent identifies the CLI.
	UserAgent string
	// Workspace is the workspace to act in, by name or id. Empty means the
	// token's own workspace, or the default one for an instance token.
	Workspace string
}

// scoped is every part of the API that acts inside a workspace. Only these
// take the /v1/workspaces/<workspace> prefix; /v1/workspaces and
// /v1/instance are about the instance and never do.
var scoped = map[string]bool{"databases": true, "trash": true, "buckets": true, "tokens": true, "audit": true, "workspace": true, "s3": true}

// workspaceRef is a workspace name or id. Anything else never goes in a
// path: ".." and an empty segment are resolved away on the way, and the
// request would land in another workspace.
var workspaceRef = regexp.MustCompile(`^(?:[a-z0-9][a-z0-9-]{0,62}|ws_[a-z0-9]{20})$`)

// ValidWorkspace reports whether ref can name a workspace.
func ValidWorkspace(ref string) error {
	if !workspaceRef.MatchString(ref) {
		return fmt.Errorf("%q can't name a workspace: use its name (lowercase letters, digits and dashes) or its id", ref)
	}
	return nil
}

// path names the client's workspace in a path that acts inside one.
func (c *Client) path(p string) string {
	if c.Workspace == "" {
		return p
	}
	rest, ok := strings.CutPrefix(p, "/v1/")
	if !ok {
		return p
	}
	head, _, _ := strings.Cut(rest, "/")
	if !scoped[head] {
		return p
	}
	return "/v1/workspaces/" + seg(c.Workspace) + "/" + rest
}

// New returns a client.
func New(baseURL, token string) *Client {
	// No overall timeout: imports and downloads can take as long as they
	// take. Requests end with their context (Ctrl-C).
	// Redirects are never followed: the API doesn't use them, and following
	// one would send the token and the body to a path nobody asked for.
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{URL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{CheckRedirect: noRedirect}, UserAgent: "lumbung-cli"}
}

// Error is a problem response from the API.
type Error struct {
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	RequestID string `json:"request_id"`
}

func (e *Error) Error() string {
	msg := e.Code + ": " + e.Title
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

// Do sends a request and decodes a JSON response into out (which may be nil).
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	res, err := c.send(ctx, method, path, query, rd, "application/json")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil || res.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Upload PUTs a body of a known length with extra headers and decodes the
// JSON response into out.
func (c *Client) Upload(ctx context.Context, path string, query url.Values, body io.Reader, size int64, contentType string, h http.Header, out any) error {
	if c.Workspace != "" {
		if err := ValidWorkspace(c.Workspace); err != nil {
			return err
		}
	}
	u := c.URL + c.path(path)
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	for k, vs := range h {
		req.Header[k] = vs
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Content-Type", contentType)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("can't reach %s: %w", c.URL, err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		e := &Error{Status: res.StatusCode}
		if json.NewDecoder(res.Body).Decode(e) != nil || e.Code == "" {
			e.Code, e.Title = "http_error", res.Status
		}
		return e
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Raw sends a body as is and returns the response for streaming. The caller
// closes it.
func (c *Client) Raw(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	return c.send(ctx, method, path, query, body, contentType)
}

func (c *Client) send(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	if c.Workspace != "" {
		if err := ValidWorkspace(c.Workspace); err != nil {
			return nil, err
		}
	}
	u := c.URL + c.path(path)
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't reach %s: %w", c.URL, err)
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		e := &Error{Status: res.StatusCode}
		if json.NewDecoder(res.Body).Decode(e) != nil || e.Code == "" {
			e.Code, e.Title = "http_error", res.Status
		}
		return nil, e
	}
	return res, nil
}

// Path segments are names or ids; escape them anyway.
func seg(s string) string { return url.PathEscape(s) }

// DBPath and BranchPath build API paths.
func DBPath(db string) string { return "/v1/databases/" + seg(db) }
func BranchPath(db, branch string) string {
	return DBPath(db) + "/branches/" + seg(branch)
}

// WorkspaceLimits are a workspace's own limits; 0 means none of its own.
type WorkspaceLimits struct {
	MaxDatabases     int   `json:"max_databases"`
	MaxBuckets       int   `json:"max_buckets"`
	MaxDatabaseBytes int64 `json:"max_database_bytes"`
	MaxObjectBytes   int64 `json:"max_object_bytes"`
}

// WorkspaceUsage is what a workspace holds.
type WorkspaceUsage struct {
	Databases     int   `json:"databases"`
	Buckets       int   `json:"buckets"`
	Tokens        int   `json:"tokens"`
	DatabaseBytes int64 `json:"database_bytes"`
	ObjectBytes   int64 `json:"object_bytes"`
}

// Workspace is a workspace. Limits and Usage are nil for a token that
// doesn't reach the whole workspace.
type Workspace struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Default    bool             `json:"default"`
	Limits     *WorkspaceLimits `json:"limits,omitempty"`
	Usage      *WorkspaceUsage  `json:"usage,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	DeletedAt  *time.Time       `json:"deleted_at"`
	PurgeAfter *time.Time       `json:"purge_after"`
}

// Database is a database.
type Database struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspace_id"`
	Name           string     `json:"name"`
	DefaultBranch  string     `json:"default_branch"`
	BranchCount    int        `json:"branch_count"`
	SizeBytes      int64      `json:"size_bytes"`
	LastActivityAt *time.Time `json:"last_activity_at"`
	CreatedAt      time.Time  `json:"created_at"`
	DeletedAt      *time.Time `json:"deleted_at"`
	PurgeAfter     *time.Time `json:"purge_after"`
}

// Branch is a branch.
type Branch struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Default          bool       `json:"default"`
	Protected        bool       `json:"protected"`
	ParentID         string     `json:"parent_id"`
	SourceSnapshotID string     `json:"source_snapshot_id"`
	ExpiresAt        *time.Time `json:"expires_at"`
	SizeBytes        int64      `json:"size_bytes"`
	CreatedAt        time.Time  `json:"created_at"`
}

// Snapshot is a snapshot.
type Snapshot struct {
	ID         string    `json:"id"`
	BranchName string    `json:"branch_name"`
	Kind       string    `json:"kind"`
	Label      string    `json:"label"`
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

// Token is a token without its secret; Secret is set once, on create.
type Token struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	DatabaseID string `json:"database_id"`
	BranchID   string `json:"branch_id"`
	BucketID   string `json:"bucket_id"`
	// Grants is everything the token reaches below the instance, each with
	// its own level. A grants token has several.
	Grants     []TokenGrant `json:"grants"`
	Access     string       `json:"access"`
	Service    bool         `json:"service"`
	CreatedAt  time.Time    `json:"created_at"`
	ExpiresAt  *time.Time   `json:"expires_at"`
	LastUsedAt *time.Time   `json:"last_used_at"`
	Secret     string       `json:"token,omitempty"`
	// AccessKeyID and SecretAccessKey are the token's S3 access key; the
	// secret only comes back when the token is created.
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
}

// TokenGrant is one thing a token reaches: a database (or one branch of it)
// or a bucket.
type TokenGrant struct {
	DatabaseID string `json:"database_id"`
	BranchID   string `json:"branch_id"`
	BucketID   string `json:"bucket_id"`
	Access     string `json:"access"`
}

// Bucket is a bucket.
type Bucket struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	PublicRead  bool      `json:"public_read"`
	CORSOrigins []string  `json:"cors_origins"`
	SizeBytes   int64     `json:"size_bytes"`
	ObjectCount int64     `json:"object_count"`
	CreatedAt   time.Time `json:"created_at"`
}

// Object is an object's record.
type Object struct {
	Key          string            `json:"key"`
	Size         int64             `json:"size"`
	ETag         string            `json:"etag"`
	ContentType  string            `json:"content_type"`
	LastModified time.Time         `json:"last_modified"`
	Headers      map[string]string `json:"headers,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// ObjectPage is one page of an object listing.
type ObjectPage struct {
	Data       []Object `json:"data"`
	Prefixes   []string `json:"prefixes"`
	NextCursor string   `json:"next_cursor"`
}

// Column is a result column.
type Column struct {
	Name     string `json:"name"`
	DeclType string `json:"decltype"`
}

// Result is one statement's result.
type Result struct {
	Columns         []Column        `json:"columns"`
	Rows            [][]value.Value `json:"rows"`
	RowsAffected    int64           `json:"rows_affected"`
	LastInsertRowID string          `json:"last_insert_rowid"`
}

// Statement is a statement to run.
type Statement struct {
	SQL  string        `json:"sql"`
	Args []value.Value `json:"args,omitempty"`
}

// Change is one schema difference.
type Change struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Change string `json:"change"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// Diff is a schema diff.
type Diff struct {
	Branch  string   `json:"branch"`
	Against string   `json:"against"`
	Changes []Change `json:"changes"`
}

// List fetches every page of a list endpoint.
func List[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	cursor := ""
	for {
		q := url.Values{"limit": {"200"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Data       []T    `json:"data"`
			NextCursor string `json:"next_cursor"`
		}
		if err := c.Do(ctx, http.MethodGet, path, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// Query runs one statement.
func (c *Client) Query(ctx context.Context, db, branch string, st Statement, readOnly bool) (*Result, error) {
	path := DBPath(db) + "/query"
	if branch != "" {
		path = BranchPath(db, branch) + "/query"
	}
	body := map[string]any{"sql": st.SQL, "args": st.Args}
	if readOnly {
		body["read_only"] = true
	}
	var r Result
	if err := c.Do(ctx, http.MethodPost, path, nil, body, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Batch runs statements in one transaction. migration turns foreign key
// enforcement off for the batch and checks it once before commit.
func (c *Client) Batch(ctx context.Context, db, branch string, sts []Statement, migration bool) ([]Result, error) {
	path := DBPath(db) + "/batch"
	if branch != "" {
		path = BranchPath(db, branch) + "/batch"
	}
	var r struct {
		Results []Result `json:"results"`
	}
	body := map[string]any{"statements": sts}
	if migration {
		body["migration"] = true
	}
	if err := c.Do(ctx, http.MethodPost, path, nil, body, &r); err != nil {
		return nil, err
	}
	return r.Results, nil
}
