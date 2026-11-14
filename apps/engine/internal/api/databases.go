package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// DatabaseOut is a database.
type DatabaseOut struct {
	ID              string     `json:"id"`
	WorkspaceID     string     `json:"workspace_id" doc:"The workspace the database is in."`
	Name            string     `json:"name"`
	DefaultBranch   string     `json:"default_branch" doc:"Name of the default branch. Empty for a token that can't reach that branch."`
	DefaultBranchID string     `json:"default_branch_id" doc:"Empty for a token that can't reach the default branch."`
	BranchCount     int        `json:"branch_count" doc:"Branches the token reaches: all of them, unless it is limited to some."`
	SizeBytes       int64      `json:"size_bytes" doc:"Space the branches the token reaches take on disk."`
	LastActivityAt  *time.Time `json:"last_activity_at" doc:"Start of the last minute with a query on a branch the token reaches, or null."`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at" doc:"When it went to the trash, or null."`
	PurgeAfter      *time.Time `json:"purge_after" doc:"When a trashed database is removed for good, or null."`
}

// databaseOut describes a database as far as the principal reaches it. A
// token limited to some branches gets counts, size, activity and the
// default branch only for the branches it reaches, so the list never tells
// it about branches it couldn't open.
func (s *Server) databaseOut(ctx context.Context, p auth.Principal, d meta.Database) (DatabaseOut, error) {
	out := DatabaseOut{ID: d.ID, WorkspaceID: d.WorkspaceID, Name: d.Name, CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt, DeletedAt: d.DeletedAt, PurgeAfter: d.PurgeAfter}
	branches, err := s.store.ListBranches(ctx, d.ID)
	if err != nil {
		return DatabaseOut{}, err
	}
	ids := make([]string, 0, len(branches))
	for _, b := range branches {
		if !p.SeesBranch(d, b.ID) {
			continue
		}
		if b.ID == d.DefaultBranchID {
			out.DefaultBranch, out.DefaultBranchID = b.Name, b.ID
		}
		out.SizeBytes += s.eng.BranchBytes(b.ID)
		ids = append(ids, b.ID)
	}
	out.BranchCount = len(ids)
	out.LastActivityAt = s.lastActivity(ctx, ids)
	return out, nil
}

type databaseBody struct{ Body DatabaseOut }

type databaseList struct {
	Body struct {
		Data       []DatabaseOut `json:"data"`
		NextCursor string        `json:"next_cursor" doc:"Pass as cursor for the next page; empty on the last page."`
	}
}

type createDatabaseIn struct {
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"200" doc:"Retrying with the same key returns the first response instead of creating twice."`
	Body           struct {
		Name string `json:"name" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Lowercase letters, digits and hyphens."`
	}
}

type updateDatabaseIn struct {
	DBPath
	Body struct {
		Name string `json:"name" pattern:"^[a-z0-9][a-z0-9-]{0,62}$"`
	}
}

type restoreDatabaseIn struct {
	DBPath
	Body *struct {
		Name string `json:"name,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Restore under this name instead of the old one."`
	}
}

func (s *Server) registerDatabases(api huma.API) {
	huma.Register(api, op("list-databases", http.MethodGet, "/v1/databases", "List databases", "Databases", ""),
		func(ctx context.Context, in *PageIn) (*databaseList, error) {
			p := principalFrom(ctx)
			all, err := s.eng.ListDatabases(ctx, p.WorkspaceID)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			visible := []meta.Database{}
			for _, d := range all {
				if p.SeesDatabase(d) {
					visible = append(visible, d)
				}
			}
			items, next, err := page(visible, func(d meta.Database) string { return d.Name }, *in)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			out := &databaseList{}
			out.Body.Data = []DatabaseOut{}
			out.Body.NextCursor = next
			for _, d := range items {
				o, err := s.databaseOut(ctx, p, d)
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				out.Body.Data = append(out.Body.Data, o)
			}
			return out, nil
		})

	huma.Register(api, created(op("create-database", http.MethodPost, "/v1/databases", "Create a database", "Databases", "database.create")),
		func(ctx context.Context, in *createDatabaseIn) (*databaseBody, error) {
			p := principalFrom(ctx)
			if err := requireWorkspaceAdmin(p); err != nil {
				return nil, toProblem(ctx, err)
			}
			return idempotent(ctx, s, "create-database", in.IdempotencyKey, in.Body, func() (*databaseBody, error) {
				d, err := s.eng.CreateDatabase(ctx, p.WorkspaceID, in.Body.Name)
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				setTarget(ctx, d.ID)
				o, err := s.databaseOut(ctx, p, d)
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				return &databaseBody{Body: o}, nil
			})
		})

	huma.Register(api, op("get-database", http.MethodGet, "/v1/databases/{db}", "Get a database", "Databases", ""),
		func(ctx context.Context, in *DBPath) (*databaseBody, error) {
			p := principalFrom(ctx)
			d, err := s.eng.GetDatabase(ctx, p.WorkspaceID, in.DB)
			if err == nil && !p.SeesDatabase(d) {
				err = apierr.Newf(apierr.DatabaseNotFound, "no database %q", in.DB)
			}
			if err == nil && !p.WholeDatabase(d) {
				// The database's details cover every branch, which a token
				// limited to one branch doesn't reach.
				err = apierr.New(apierr.Forbidden, "this token is limited to one branch")
			}
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			o, err := s.databaseOut(ctx, p, d)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &databaseBody{Body: o}, nil
		})

	huma.Register(api, op("update-database", http.MethodPatch, "/v1/databases/{db}", "Rename a database", "Databases", "database.rename"),
		func(ctx context.Context, in *updateDatabaseIn) (*databaseBody, error) {
			p := principalFrom(ctx)
			if err := requireWorkspaceAdmin(p); err != nil {
				return nil, toProblem(ctx, err)
			}
			d, err := s.eng.RenameDatabase(ctx, p.WorkspaceID, in.DB, in.Body.Name)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID)
			o, err := s.databaseOut(ctx, p, d)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &databaseBody{Body: o}, nil
		})

	huma.Register(api, op("delete-database", http.MethodDelete, "/v1/databases/{db}", "Move a database to the trash", "Databases", "database.delete"),
		func(ctx context.Context, in *DBPath) (*databaseBody, error) {
			p := principalFrom(ctx)
			if err := requireWorkspaceAdmin(p); err != nil {
				return nil, toProblem(ctx, err)
			}
			d, err := s.eng.DeleteDatabase(ctx, p.WorkspaceID, in.DB)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID)
			o, err := s.databaseOut(ctx, p, d)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &databaseBody{Body: o}, nil
		})

	huma.Register(api, op("restore-database", http.MethodPost, "/v1/databases/{db}/restore", "Restore a database from the trash", "Databases", "database.restore"),
		func(ctx context.Context, in *restoreDatabaseIn) (*databaseBody, error) {
			p := principalFrom(ctx)
			if err := requireWorkspaceAdmin(p); err != nil {
				return nil, toProblem(ctx, err)
			}
			name := ""
			if in.Body != nil {
				name = in.Body.Name
			}
			d, err := s.eng.RestoreDatabase(ctx, p.WorkspaceID, in.DB, name)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID)
			o, err := s.databaseOut(ctx, p, d)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &databaseBody{Body: o}, nil
		})

	huma.Register(api, op("list-trash", http.MethodGet, "/v1/trash", "List databases in the trash", "Databases", ""),
		func(ctx context.Context, in *PageIn) (*databaseList, error) {
			p := principalFrom(ctx)
			if err := requireWorkspaceAdmin(p); err != nil {
				return nil, toProblem(ctx, err)
			}
			all, err := s.eng.ListTrash(ctx, p.WorkspaceID)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			items, next, err := page(all, func(d meta.Database) string { return d.DeletedAt.UTC().Format(meta.TimeLayout) + d.ID }, *in)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			out := &databaseList{}
			out.Body.Data = []DatabaseOut{}
			out.Body.NextCursor = next
			for _, d := range items {
				o, err := s.databaseOut(ctx, p, d)
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				out.Body.Data = append(out.Body.Data, o)
			}
			return out, nil
		})
}
