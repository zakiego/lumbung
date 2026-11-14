package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	"github.com/zakiego/lumbung/apps/engine/internal/apierr"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// errorStatuses are the statuses of every documented error code. Any
// operation can meet most of them (auth, limits, a busy branch), so each
// operation documents them all with the problem schema.
var errorStatuses = func() []int {
	seen := map[int]bool{}
	var out []int
	for _, c := range apierr.Codes {
		if !seen[c.Status] {
			seen[c.Status] = true
			out = append(out, c.Status)
		}
	}
	sort.Ints(out)
	return out
}()

// op builds an operation. audit names the action for the audit log; empty
// for reads.
func op(id, method, path, summary, tag, audit string) huma.Operation {
	o := huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Tags:        []string{tag},
		Metadata:    map[string]any{},
		Errors:      errorStatuses,
	}
	if audit != "" {
		o.Metadata["audit"] = audit
	}
	return o
}

func created(o huma.Operation) huma.Operation {
	o.DefaultStatus = http.StatusCreated
	return o
}

func noContent(o huma.Operation) huma.Operation {
	o.DefaultStatus = http.StatusNoContent
	return o
}

// DBPath and BranchPath are the path parameters most routes share.
type DBPath struct {
	DB string `path:"db" doc:"Database name or id."`
}

type BranchPath struct {
	DB     string `path:"db" doc:"Database name or id."`
	Branch string `path:"branch" doc:"Branch name or id."`
}

// PageIn is the cursor pagination every list takes.
type PageIn struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"Most items to return."`
	Cursor string `query:"cursor" doc:"The next_cursor from the previous page."`
}

// page returns the items after the cursor, sorted by key, and the cursor for
// the next page ("" on the last one).
func page[T any](items []T, key func(T) string, in PageIn) ([]T, string, error) {
	sort.SliceStable(items, func(i, j int) bool { return key(items[i]) < key(items[j]) })
	start := 0
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil {
			return nil, "", apierr.New(apierr.BadRequest, "invalid cursor")
		}
		after := string(raw)
		start = sort.Search(len(items), func(i int) bool { return key(items[i]) > after })
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	end := min(start+limit, len(items))
	out := items[start:end]
	next := ""
	if end < len(items) {
		next = base64.RawURLEncoding.EncodeToString([]byte(key(items[end-1])))
	}
	if out == nil {
		out = []T{}
	}
	return out, next, nil
}

func requireInstanceAdmin(p auth.Principal) error {
	if !p.InstanceAdmin() {
		return apierr.New(apierr.Forbidden, "this needs an instance admin token")
	}
	return nil
}

// database resolves a database the principal can see and checks its access
// to the database as a whole. A database it can't see is not found, so
// tokens don't learn which names exist.
func (s *Server) database(ctx context.Context, p auth.Principal, ref, access string) (meta.Database, error) {
	d, err := s.eng.GetDatabase(ctx, ref)
	if err != nil {
		return meta.Database{}, err
	}
	if !p.SeesDatabase(d.ID) {
		return meta.Database{}, apierr.Newf(apierr.DatabaseNotFound, "no database %q", ref)
	}
	if !p.AtLeast(access, d.ID, "") {
		return meta.Database{}, apierr.New(apierr.Forbidden, "this token can't do that on this database")
	}
	return d, nil
}

// branch resolves a branch and checks the principal's access to it.
// An empty ref means the default branch.
func (s *Server) branch(ctx context.Context, p auth.Principal, dbRef, ref, access string) (meta.Database, meta.Branch, error) {
	d, err := s.eng.GetDatabase(ctx, dbRef)
	if err != nil {
		return meta.Database{}, meta.Branch{}, err
	}
	if !p.SeesDatabase(d.ID) {
		return meta.Database{}, meta.Branch{}, apierr.Newf(apierr.DatabaseNotFound, "no database %q", dbRef)
	}
	if ref == "" {
		ref = d.DefaultBranchID
	}
	b, err := s.eng.GetBranch(ctx, d.ID, ref)
	if !p.WholeDatabase(d.ID) && (err != nil || !p.SeesBranch(d.ID, b.ID)) {
		// Answer the same for a missing branch and someone else's, so a
		// branch token can't probe for branch names.
		return meta.Database{}, meta.Branch{}, apierr.New(apierr.Forbidden, "this token can't do that on this branch")
	}
	if err != nil {
		return meta.Database{}, meta.Branch{}, err
	}
	if !p.AtLeast(access, d.ID, b.ID) {
		return meta.Database{}, meta.Branch{}, apierr.New(apierr.Forbidden, "this token can't do that on this branch")
	}
	return d, b, nil
}
