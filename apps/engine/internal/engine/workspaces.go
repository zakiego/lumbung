package engine

import (
	"context"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/ids"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// Workspaces (PLAN 17, ADR 0008). Every database and bucket is in one, and
// every function here that takes a database or bucket name takes its
// workspace too: nothing resolves a name without one.

// DefaultWorkspace returns the instance's default workspace, where callers
// with no workspace of their own act.
func (e *Engine) DefaultWorkspace(ctx context.Context) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	return e.meta.DefaultWorkspace(ctx)
}

// GetWorkspace finds a live workspace by name or id.
func (e *Engine) GetWorkspace(ctx context.Context, ref string) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	return e.meta.GetWorkspace(ctx, ref)
}

// ListWorkspaces returns live workspaces by name.
func (e *Engine) ListWorkspaces(ctx context.Context) ([]meta.Workspace, error) {
	if e.refused != nil {
		return nil, e.refused
	}
	return e.meta.ListWorkspaces(ctx)
}

// NewWorkspaceLimits are a new workspace's limits. A nil field takes the
// instance's setting for new workspaces.
type NewWorkspaceLimits struct {
	MaxDatabases     *int
	MaxBuckets       *int
	MaxDatabaseBytes *int64
	MaxObjectBytes   *int64
}

// CreateWorkspace creates an empty workspace.
func (e *Engine) CreateWorkspace(ctx context.Context, name string, l NewWorkspaceLimits) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	if !ids.ValidName(name) {
		return meta.Workspace{}, apierr.Newf(apierr.InvalidName, "invalid workspace name %q", name)
	}
	limits := meta.WorkspaceLimits{
		MaxDatabases:     e.cfg.WorkspaceMaxDatabases,
		MaxBuckets:       e.cfg.WorkspaceMaxBuckets,
		MaxDatabaseBytes: e.cfg.WorkspaceMaxDatabaseBytes,
		MaxObjectBytes:   e.cfg.WorkspaceMaxObjectBytes,
	}
	if l.MaxDatabases != nil {
		limits.MaxDatabases = *l.MaxDatabases
	}
	if l.MaxBuckets != nil {
		limits.MaxBuckets = *l.MaxBuckets
	}
	if l.MaxDatabaseBytes != nil {
		limits.MaxDatabaseBytes = *l.MaxDatabaseBytes
	}
	if l.MaxObjectBytes != nil {
		limits.MaxObjectBytes = *l.MaxObjectBytes
	}
	return e.meta.CreateWorkspace(ctx, ids.New(ids.Workspace), name, limits, e.cfg.MaxWorkspaces)
}

// UpdateWorkspace renames a workspace or changes its limits.
func (e *Engine) UpdateWorkspace(ctx context.Context, id string, u meta.WorkspaceUpdate) (meta.Workspace, error) {
	if e.refused != nil {
		return meta.Workspace{}, e.refused
	}
	return e.meta.UpdateWorkspace(ctx, id, u)
}

// WorkspaceUsage is what a workspace holds right now.
type WorkspaceUsage struct {
	Databases     int // counting the trash
	Buckets       int
	Tokens        int   // live
	DatabaseBytes int64 // branch files of live databases
	ObjectBytes   int64 // objects plus multipart parts in progress
}

// WorkspaceUsage measures what a workspace holds.
func (e *Engine) WorkspaceUsage(ctx context.Context, wsID string) (WorkspaceUsage, error) {
	if e.refused != nil {
		return WorkspaceUsage{}, e.refused
	}
	c, err := e.meta.CountWorkspace(ctx, wsID)
	if err != nil {
		return WorkspaceUsage{}, err
	}
	u := WorkspaceUsage{Databases: c.Databases, Buckets: c.Buckets, Tokens: c.Tokens, ObjectBytes: c.ObjectBytes}
	branches, err := e.meta.ListWorkspaceBranches(ctx, wsID)
	if err != nil {
		return WorkspaceUsage{}, err
	}
	for _, b := range branches {
		u.DatabaseBytes += e.BranchBytes(b.ID)
	}
	return u, nil
}
