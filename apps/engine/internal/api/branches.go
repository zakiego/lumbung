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

// BranchOut is a branch.
type BranchOut struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	DatabaseID       string     `json:"database_id"`
	Default          bool       `json:"default" doc:"Whether this is the database's default branch."`
	Protected        bool       `json:"protected" doc:"Protected branches can't be deleted or reset."`
	ParentID         string     `json:"parent_id" doc:"The branch it was copied from, or empty."`
	SourceSnapshotID string     `json:"source_snapshot_id" doc:"The snapshot it was restored from, or empty."`
	ExpiresAt        *time.Time `json:"expires_at" doc:"When it is deleted automatically, or null."`
	SizeBytes        int64      `json:"size_bytes"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (s *Server) branchOut(d meta.Database, b meta.Branch) BranchOut {
	return BranchOut{
		ID: b.ID, Name: b.Name, DatabaseID: b.DatabaseID, Default: b.ID == d.DefaultBranchID,
		Protected: b.Protected, ParentID: b.ParentID, SourceSnapshotID: b.SourceSnapshotID,
		ExpiresAt: b.ExpiresAt, SizeBytes: s.eng.BranchBytes(b.ID), CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
	}
}

type branchBody struct{ Body BranchOut }

type branchList struct {
	Body struct {
		Data       []BranchOut `json:"data"`
		NextCursor string      `json:"next_cursor"`
	}
}

type listBranchesIn struct {
	DBPath
	PageIn
}

type createBranchIn struct {
	DBPath
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"200"`
	Body           struct {
		Name          string `json:"name" pattern:"^[a-z0-9][a-z0-9-]{0,62}$"`
		From          string `json:"from,omitempty" doc:"Branch to copy, name or id. Defaults to the default branch."`
		FromSnapshot  string `json:"from_snapshot,omitempty" doc:"Snapshot id to copy instead of a branch."`
		Protected     bool   `json:"protected,omitempty"`
		ExpiresInDays int    `json:"expires_in_days,omitempty" minimum:"0" maximum:"3650" doc:"Delete the branch automatically after this many days. 0 keeps it."`
	}
}

type updateBranchIn struct {
	BranchPath
	Body struct {
		Name          *string `json:"name,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$"`
		Protected     *bool   `json:"protected,omitempty"`
		ExpiresInDays *int    `json:"expires_in_days,omitempty" minimum:"0" maximum:"3650" doc:"Days from now until automatic deletion. 0 clears the expiry."`
	}
}

type diffIn struct {
	BranchPath
	Against string `query:"against" required:"true" doc:"Branch to compare with, name or id."`
}

type diffBody struct{ Body *engine.Diff }
type schemaBody struct{ Body *engine.Schema }

func (s *Server) registerBranches(api huma.API) {
	base := "/v1/databases/{db}/branches"

	huma.Register(api, op("list-branches", http.MethodGet, base, "List branches", "Branches", ""),
		func(ctx context.Context, in *listBranchesIn) (*branchList, error) {
			p := principalFrom(ctx)
			d, err := s.database(ctx, p, in.DB, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			all, err := s.eng.ListBranches(ctx, d.ID)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			items, next, err := page(all, func(b meta.Branch) string { return b.CreatedAt.UTC().Format(time.RFC3339Nano) + b.ID }, in.PageIn)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			out := &branchList{}
			out.Body.Data = make([]BranchOut, 0, len(items))
			out.Body.NextCursor = next
			for _, b := range items {
				out.Body.Data = append(out.Body.Data, s.branchOut(d, b))
			}
			return out, nil
		})

	huma.Register(api, created(op("create-branch", http.MethodPost, base, "Create a branch", "Branches", "branch.create")),
		func(ctx context.Context, in *createBranchIn) (*branchBody, error) {
			p := principalFrom(ctx)
			d, err := s.database(ctx, p, in.DB, auth.AccessWrite)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			if in.Body.From != "" {
				if _, _, err := s.branch(ctx, p, d.ID, in.Body.From, auth.AccessRead); err != nil {
					return nil, toProblem(ctx, err)
				}
			}
			return idempotent(ctx, s, "create-branch", in.IdempotencyKey, in.Body, func() (*branchBody, error) {
				opts := engine.NewBranchOptions{Protected: in.Body.Protected, ExpiresIn: time.Duration(in.Body.ExpiresInDays) * 24 * time.Hour}
				b, err := s.eng.CreateBranch(ctx, d.ID, in.Body.Name, engine.BranchSource{Branch: in.Body.From, Snapshot: in.Body.FromSnapshot}, opts)
				if err != nil {
					return nil, toProblem(ctx, err)
				}
				setTarget(ctx, d.ID+"/"+b.ID)
				return &branchBody{Body: s.branchOut(d, b)}, nil
			})
		})

	huma.Register(api, op("get-branch", http.MethodGet, base+"/{branch}", "Get a branch", "Branches", ""),
		func(ctx context.Context, in *BranchPath) (*branchBody, error) {
			d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &branchBody{Body: s.branchOut(d, b)}, nil
		})

	huma.Register(api, op("update-branch", http.MethodPatch, base+"/{branch}", "Rename a branch or change its protection or expiry", "Branches", "branch.update"),
		func(ctx context.Context, in *updateBranchIn) (*branchBody, error) {
			d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessAdmin)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID+"/"+b.ID)
			u := engine.BranchChanges{Name: in.Body.Name, Protected: in.Body.Protected}
			if in.Body.ExpiresInDays != nil {
				var at *time.Time
				if n := *in.Body.ExpiresInDays; n > 0 {
					t := time.Now().Add(time.Duration(n) * 24 * time.Hour)
					at = &t
				}
				u.ExpiresAt = &at
			}
			b, err = s.eng.UpdateBranch(ctx, d.ID, b.ID, u)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &branchBody{Body: s.branchOut(d, b)}, nil
		})

	huma.Register(api, noContent(op("delete-branch", http.MethodDelete, base+"/{branch}", "Delete a branch", "Branches", "branch.delete")),
		func(ctx context.Context, in *BranchPath) (*struct{}, error) {
			d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessAdmin)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID+"/"+b.ID)
			if err := s.eng.DeleteBranch(ctx, d.ID, b.ID); err != nil {
				return nil, toProblem(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("reset-branch", http.MethodPost, base+"/{branch}/reset", "Reset a branch to a fresh copy of its parent", "Branches", "branch.reset"),
		func(ctx context.Context, in *BranchPath) (*branchBody, error) {
			d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessWrite)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			setTarget(ctx, d.ID+"/"+b.ID)
			b, err = s.eng.ResetBranch(ctx, d.ID, b.ID)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &branchBody{Body: s.branchOut(d, b)}, nil
		})

	huma.Register(api, op("promote-branch", http.MethodPost, base+"/{branch}/promote", "Make a branch the default", "Branches", "branch.promote"),
		func(ctx context.Context, in *BranchPath) (*branchBody, error) {
			p := principalFrom(ctx)
			d, b, err := s.branch(ctx, p, in.DB, in.Branch, auth.AccessAdmin)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			if !p.AtLeast(auth.AccessAdmin, d.ID, "") {
				return nil, toProblem(ctx, apierr.New(apierr.Forbidden, "promoting needs admin access to the whole database"))
			}
			setTarget(ctx, d.ID+"/"+b.ID)
			b, err = s.eng.PromoteBranch(ctx, d.ID, b.ID)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			d.DefaultBranchID = b.ID
			return &branchBody{Body: s.branchOut(d, b)}, nil
		})

	huma.Register(api, op("diff-branch", http.MethodGet, base+"/{branch}/diff", "Compare a branch's schema with another branch", "Branches", ""),
		func(ctx context.Context, in *diffIn) (*diffBody, error) {
			p := principalFrom(ctx)
			d, b, err := s.branch(ctx, p, in.DB, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			_, a, err := s.branch(ctx, p, d.ID, in.Against, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			diff, err := s.eng.DiffBranches(ctx, d.ID, b.ID, a.ID)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &diffBody{Body: diff}, nil
		})

	huma.Register(api, op("get-schema", http.MethodGet, base+"/{branch}/schema", "Get a branch's schema", "Branches", ""),
		func(ctx context.Context, in *BranchPath) (*schemaBody, error) {
			d, b, err := s.branch(ctx, principalFrom(ctx), in.DB, in.Branch, auth.AccessRead)
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			sc, err := s.eng.Schema(ctx, engine.Target{Database: d.ID, Branch: b.ID})
			if err != nil {
				return nil, toProblem(ctx, err)
			}
			return &schemaBody{Body: sc}, nil
		})
}
