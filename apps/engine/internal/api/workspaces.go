package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// Workspaces (PLAN 17, ADR 0008). A workspace token only ever sees its own
// workspace here; creating one and changing one's name or limits take an
// instance admin.

// WorkspaceLimitsOut are a workspace's own caps.
type WorkspaceLimitsOut struct {
	MaxDatabases     int   `json:"max_databases" doc:"Most databases, counting the trash. 0: no limit of its own."`
	MaxBuckets       int   `json:"max_buckets" doc:"Most buckets. 0: no limit of its own."`
	MaxDatabaseBytes int64 `json:"max_database_bytes" doc:"Most bytes the branch files of its databases may take together. 0: no limit of its own."`
	MaxObjectBytes   int64 `json:"max_object_bytes" doc:"Most bytes its buckets may hold together, counting multipart uploads in progress. 0: no limit of its own."`
}

// WorkspaceUsageOut is what a workspace holds right now.
type WorkspaceUsageOut struct {
	Databases     int   `json:"databases" doc:"Databases, counting the trash."`
	Buckets       int   `json:"buckets"`
	Tokens        int   `json:"tokens" doc:"Live tokens."`
	DatabaseBytes int64 `json:"database_bytes" doc:"Bytes the branch files of its live databases take."`
	ObjectBytes   int64 `json:"object_bytes" doc:"Bytes its buckets hold, counting multipart uploads in progress."`
}

// WorkspaceOut is a workspace.
type WorkspaceOut struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Default    bool                `json:"default" doc:"The instance's default workspace, where a request with no workspace of its own acts. It can't be deleted."`
	Limits     *WorkspaceLimitsOut `json:"limits,omitempty" doc:"The workspace's own limits. The instance limits always apply on top. Left out for a token that doesn't reach the whole workspace."`
	Usage      *WorkspaceUsageOut  `json:"usage,omitempty" doc:"What the workspace holds now. Left out for a token that doesn't reach the whole workspace."`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
	DeletedAt  *time.Time          `json:"deleted_at" doc:"When it went to the trash, or null."`
	PurgeAfter *time.Time          `json:"purge_after" doc:"When a trashed workspace is removed for good, or null."`
}

// workspaceOut describes a workspace as far as the principal may see it.
// Its limits and usage are totals over everything in it, so only a
// principal that reaches the whole workspace gets them: a database or
// bucket token would learn from them what else the workspace holds.
func (s *Server) workspaceOut(ctx context.Context, p auth.Principal, w meta.Workspace) (WorkspaceOut, error) {
	out := WorkspaceOut{ID: w.ID, Name: w.Name, Default: w.Default,
		CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt, DeletedAt: w.DeletedAt, PurgeAfter: w.PurgeAfter}
	if !p.SeesWorkspaceUsage(w.ID) {
		return out, nil
	}
	u, err := s.eng.WorkspaceUsage(ctx, w.ID)
	if err != nil {
		return WorkspaceOut{}, err
	}
	out.Limits = &WorkspaceLimitsOut{MaxDatabases: w.Limits.MaxDatabases, MaxBuckets: w.Limits.MaxBuckets,
		MaxDatabaseBytes: w.Limits.MaxDatabaseBytes, MaxObjectBytes: w.Limits.MaxObjectBytes}
	out.Usage = &WorkspaceUsageOut{Databases: u.Databases, Buckets: u.Buckets, Tokens: u.Tokens,
		DatabaseBytes: u.DatabaseBytes, ObjectBytes: u.ObjectBytes}
	return out, nil
}

type workspaceBody struct{ Body WorkspaceOut }

type workspaceList struct {
	Body struct {
		Data       []WorkspaceOut `json:"data"`
		NextCursor string         `json:"next_cursor" doc:"Pass as cursor for the next page; empty on the last page."`
	}
}

// WorkspacePath names a workspace in the path.
type WorkspacePath struct {
	Workspace string `path:"ws" doc:"Workspace name or id."`
}

type createWorkspaceIn struct {
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"200" doc:"Retrying with the same key returns the first response instead of creating twice."`
	Body           struct {
		Name             string `json:"name" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Lowercase letters, digits and hyphens. Unique on the instance."`
		MaxDatabases     *int   `json:"max_databases,omitempty" minimum:"0" doc:"Omit for the instance's setting for new workspaces. 0: no limit of its own."`
		MaxBuckets       *int   `json:"max_buckets,omitempty" minimum:"0"`
		MaxDatabaseBytes *int64 `json:"max_database_bytes,omitempty" minimum:"0"`
		MaxObjectBytes   *int64 `json:"max_object_bytes,omitempty" minimum:"0"`
	}
}

type listWorkspacesIn struct {
	PageIn
	Trashed bool `query:"trashed" doc:"List the workspaces in the trash instead. Needs an instance admin."`
}

type restoreWorkspaceIn struct {
	WorkspacePath
	Body *struct {
		Name string `json:"name,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Restore under this name instead of the old one."`
	}
}

type moveIn struct {
	Workspace string `json:"workspace" minLength:"1" doc:"The workspace to move it to, by name or id."`
}

type moveDatabaseIn struct {
	DBPath
	Body moveIn
}

type moveBucketIn struct {
	BucketPath
	Body moveIn
}

type updateWorkspaceIn struct {
	WorkspacePath
	Body struct {
		Name             *string `json:"name,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$"`
		MaxDatabases     *int    `json:"max_databases,omitempty" minimum:"0" doc:"0: no limit of its own."`
		MaxBuckets       *int    `json:"max_buckets,omitempty" minimum:"0"`
		MaxDatabaseBytes *int64  `json:"max_database_bytes,omitempty" minimum:"0"`
		MaxObjectBytes   *int64  `json:"max_object_bytes,omitempty" minimum:"0"`
	}
}

// workspace resolves a workspace the principal may know about. One it may
// not answers exactly like one that doesn't exist, so a workspace token
// can't probe for names.
func (s *Server) workspace(ctx context.Context, ref string) (meta.Workspace, error) {
	p := principalFrom(ctx)
	w, err := s.eng.GetWorkspace(ctx, ref)
	if err != nil {
		return meta.Workspace{}, err
	}
	if !p.SeesWorkspace(w.ID) {
		return meta.Workspace{}, apierr.Newf(apierr.WorkspaceNotFound, "no workspace %q", ref)
	}
	return w, nil
}

func (s *Server) registerWorkspaces(api huma.API) {
	huma.Register(api, op("get-current-workspace", http.MethodGet, "/v1/workspace", "Get the workspace this request acts in", "Workspaces", ""),
		func(ctx context.Context, _ *struct{}) (*workspaceBody, error) {
			w, err := s.workspace(ctx, principalFrom(ctx).WorkspaceID)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			o, err := s.workspaceOut(ctx, principalFrom(ctx), w)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &workspaceBody{Body: o}, nil
		})

	huma.Register(api, op("list-workspaces", http.MethodGet, "/v1/workspaces", "List workspaces", "Workspaces", ""),
		func(ctx context.Context, in *listWorkspacesIn) (*workspaceList, error) {
			p := principalFrom(ctx)
			var all []meta.Workspace
			var err error
			if in.Trashed {
				// The check comes first, so a workspace token learns nothing
				// about what was deleted.
				if err := requireInstanceAdmin(p); err != nil {
					return nil, toProblem(ctx, err)
				}
				all, err = s.eng.ListDeletedWorkspaces(ctx)
			} else {
				all, err = s.eng.ListWorkspaces(ctx)
			}
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			// A workspace token gets a list of one: its own.
			visible := []meta.Workspace{}
			for _, w := range all {
				if p.SeesWorkspace(w.ID) {
					visible = append(visible, w)
				}
			}
			items, next, err := page(visible, func(w meta.Workspace) string { return w.Name + "\x00" + w.ID }, in.PageIn)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			out := &workspaceList{}
			out.Body.Data = []WorkspaceOut{}
			out.Body.NextCursor = next
			for _, w := range items {
				o, err := s.workspaceOut(ctx, principalFrom(ctx), w)
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				out.Body.Data = append(out.Body.Data, o)
			}
			return out, nil
		})

	huma.Register(api, instanceOp(created(op("create-workspace", http.MethodPost, "/v1/workspaces", "Create a workspace", "Workspaces", "workspace.create"))),
		func(ctx context.Context, in *createWorkspaceIn) (*workspaceBody, error) {
			if err := requireInstanceAdmin(principalFrom(ctx)); err != nil {
				return nil, toProblem(ctx, err)
			}
			return idempotent(ctx, s, "create-workspace", in.IdempotencyKey, in.Body, func() (*workspaceBody, error) {
				w, err := s.eng.CreateWorkspace(ctx, in.Body.Name, engine.NewWorkspaceLimits{
					MaxDatabases: in.Body.MaxDatabases, MaxBuckets: in.Body.MaxBuckets,
					MaxDatabaseBytes: in.Body.MaxDatabaseBytes, MaxObjectBytes: in.Body.MaxObjectBytes,
				})
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				setTarget(ctx, w.ID)
				o, err := s.workspaceOut(ctx, principalFrom(ctx), w)
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				return &workspaceBody{Body: o}, nil
			})
		})

	huma.Register(api, op("get-workspace", http.MethodGet, "/v1/workspaces/{ws}", "Get a workspace", "Workspaces", ""),
		func(ctx context.Context, in *WorkspacePath) (*workspaceBody, error) {
			w, err := s.workspace(ctx, in.Workspace)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			o, err := s.workspaceOut(ctx, principalFrom(ctx), w)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &workspaceBody{Body: o}, nil
		})

	huma.Register(api, instanceOp(op("update-workspace", http.MethodPatch, "/v1/workspaces/{ws}", "Rename a workspace or change its limits", "Workspaces", "workspace.update")),
		func(ctx context.Context, in *updateWorkspaceIn) (*workspaceBody, error) {
			// The limits are the operator's hold on a workspace, so its own
			// admin can't lift them. The check comes before the lookup: a
			// workspace token learns nothing about other names from it.
			if err := requireInstanceAdmin(principalFrom(ctx)); err != nil {
				return nil, toProblem(ctx, err)
			}
			w, err := s.workspace(ctx, in.Workspace)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, w.ID)
			w, err = s.eng.UpdateWorkspace(ctx, w.ID, meta.WorkspaceUpdate{
				Name: in.Body.Name, MaxDatabases: in.Body.MaxDatabases, MaxBuckets: in.Body.MaxBuckets,
				MaxDatabaseBytes: in.Body.MaxDatabaseBytes, MaxObjectBytes: in.Body.MaxObjectBytes,
			})
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			o, err := s.workspaceOut(ctx, principalFrom(ctx), w)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &workspaceBody{Body: o}, nil
		})

	huma.Register(api, instanceOp(op("delete-workspace", http.MethodDelete, "/v1/workspaces/{ws}", "Move a workspace to the trash", "Workspaces", "workspace.delete")),
		func(ctx context.Context, in *WorkspacePath) (*workspaceBody, error) {
			p := principalFrom(ctx)
			if err := requireInstanceAdmin(p); err != nil {
				return nil, toProblem(ctx, err)
			}
			w, err := s.workspace(ctx, in.Workspace)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, w.ID)
			if w, err = s.eng.DeleteWorkspace(ctx, w.ID); err != nil {
				return nil, toProblem(ctx, err)
			}
			o, err := s.workspaceOut(ctx, p, w)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &workspaceBody{Body: o}, nil
		})

	huma.Register(api, instanceOp(op("restore-workspace", http.MethodPost, "/v1/workspaces/{ws}/restore", "Restore a workspace from the trash", "Workspaces", "workspace.restore")),
		func(ctx context.Context, in *restoreWorkspaceIn) (*workspaceBody, error) {
			p := principalFrom(ctx)
			if err := requireInstanceAdmin(p); err != nil {
				return nil, toProblem(ctx, err)
			}
			name := ""
			if in.Body != nil {
				name = in.Body.Name
			}
			w, err := s.eng.RestoreWorkspace(ctx, in.Workspace, name)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, w.ID)
			o, err := s.workspaceOut(ctx, p, w)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &workspaceBody{Body: o}, nil
		})

	// Moves take an instance admin: they cross the boundary a workspace
	// token can't see past. The database or bucket is resolved in the
	// workspace the request acts in, like any other.
	huma.Register(api, instanceOp(op("move-database", http.MethodPost, "/v1/databases/{db}/move", "Move a database to another workspace", "Databases", "database.move")),
		func(ctx context.Context, in *moveDatabaseIn) (*databaseBody, error) {
			p := principalFrom(ctx)
			if err := requireInstanceAdmin(p); err != nil {
				return nil, toProblem(ctx, err)
			}
			d, err := s.eng.MoveDatabase(ctx, p.WorkspaceID, in.DB, in.Body.Workspace)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID+"/"+p.WorkspaceID+"/"+d.WorkspaceID)
			// Describe it as the instance admin sees it where it now lives.
			there, _ := p.In(d.WorkspaceID)
			o, err := s.databaseOut(ctx, there, d)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &databaseBody{Body: o}, nil
		})

	huma.Register(api, instanceOp(op("move-bucket", http.MethodPost, "/v1/buckets/{bucket}/move", "Move a bucket to another workspace", "Buckets", "bucket.move")),
		func(ctx context.Context, in *moveBucketIn) (*bucketBody, error) {
			p := principalFrom(ctx)
			if err := requireInstanceAdmin(p); err != nil {
				return nil, toProblem(ctx, err)
			}
			b, err := s.eng.MoveBucket(ctx, p.WorkspaceID, in.Bucket, in.Body.Workspace)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, b.ID+"/"+p.WorkspaceID+"/"+b.WorkspaceID)
			return &bucketBody{Body: bucketOut(b)}, nil
		})
}
