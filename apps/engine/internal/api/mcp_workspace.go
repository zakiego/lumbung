package api

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/lumbung/apps/engine/internal/auth"
)

// MCP tools for a workspace token that manages its own databases and
// buckets (PLAN 17.7): what it may use, and the deletes and restores that
// go with the creates. Each one resolves in the token's workspace through
// the same helpers as REST.

type mcpWorkspace struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
	// Limits and usage are left out for a token that doesn't reach the
	// whole workspace.
	Limits *WorkspaceLimitsOut `json:"limits,omitempty"`
	Usage  *WorkspaceUsageOut  `json:"usage,omitempty"`
	Note   string              `json:"note,omitempty"`
}

func (s *Server) addWorkspaceTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "get_workspace", Description: "Show the workspace this token works in: its name, its limits (most databases, buckets and bytes; 0 means no limit of its own) and what it holds now. Use it before creating databases or buckets to see how much room is left.", Annotations: readOnly},
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, mcpWorkspace, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpWorkspace{}, err
			}
			w, err := s.eng.GetWorkspace(ctx, p.WorkspaceID)
			if err != nil {
				return nil, mcpWorkspace{}, s.toolErr(req, err)
			}
			o, err := s.workspaceOut(ctx, p, w)
			if err != nil {
				return nil, mcpWorkspace{}, s.toolErr(req, err)
			}
			out := mcpWorkspace{ID: o.ID, Name: o.Name, Default: o.Default, Limits: o.Limits, Usage: o.Usage}
			if o.Limits == nil {
				out.Note = "This token reaches part of the workspace, so its limits and totals aren't shown."
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "delete_database", Description: "Move a database to the trash. Its branches stop answering at once and its name is free to reuse. It can be brought back with restore_database until the trash retention passes (7 days by default), then it is removed for good. Needs an admin token for the whole workspace.", Annotations: destructive},
		func(ctx context.Context, req *mcp.CallToolRequest, in dbIn) (*mcp.CallToolResult, struct {
			Deleted    string `json:"deleted"`
			PurgeAfter string `json:"purge_after,omitempty"`
		}, error) {
			var out struct {
				Deleted    string `json:"deleted"`
				PurgeAfter string `json:"purge_after,omitempty"`
			}
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, out, err
			}
			err = s.audited(req, "database.delete", func() (string, error) {
				if err := requireWorkspaceAdmin(p); err != nil {
					return "", err
				}
				d, err := s.eng.DeleteDatabase(ctx, p.WorkspaceID, in.Database)
				if err != nil {
					return "", err
				}
				out.Deleted = d.Name
				if d.PurgeAfter != nil {
					out.PurgeAfter = timeStr(*d.PurgeAfter)
				}
				return d.ID, nil
			})
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "restore_database", Description: "Bring a database back from the trash with its branches and snapshots. If another database took its name meanwhile, pass a new name. Needs an admin token for the whole workspace.", Annotations: additive},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Database string `json:"database" jsonschema:"name or id of the database in the trash"`
			Name     string `json:"name,omitempty" jsonschema:"restore it under this name instead of the old one"`
		}) (*mcp.CallToolResult, mcpDatabase, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpDatabase{}, err
			}
			var out mcpDatabase
			err = s.audited(req, "database.restore", func() (string, error) {
				if err := requireWorkspaceAdmin(p); err != nil {
					return "", err
				}
				d, err := s.eng.RestoreDatabase(ctx, p.WorkspaceID, in.Database, in.Name)
				if err != nil {
					return "", err
				}
				if out, err = s.mcpDatabaseOf(ctx, p, d); err != nil {
					return d.ID, err
				}
				return d.ID, nil
			})
			if err != nil {
				return nil, mcpDatabase{}, s.toolErr(req, err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "delete_bucket", Description: "Delete a bucket. It must be empty: delete its objects first. This can't be undone. Needs an admin token for the whole workspace.", Annotations: destructive},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Bucket string `json:"bucket" jsonschema:"bucket name"`
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
			err = s.audited(req, "bucket.delete", func() (string, error) {
				b, err := s.bucketFor(ctx, p, in.Bucket, auth.AccessRead)
				if err != nil {
					return "", err
				}
				if err := requireWorkspaceAdmin(p); err != nil {
					return b.ID, err
				}
				if err := s.eng.DeleteBucket(ctx, b.ID); err != nil {
					return b.ID, err
				}
				out.Deleted = b.Name
				return b.ID, nil
			})
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "restore_snapshot", Description: "Restore a snapshot into a new branch. It never overwrites a branch: check the new one, then use it or promote it. Needs write access to the whole database.", Annotations: additive},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Database string `json:"database" jsonschema:"database name or id"`
			Snapshot string `json:"snapshot" jsonschema:"snapshot id, from list_snapshots"`
			Name     string `json:"name" jsonschema:"name for the new branch"`
		}) (*mcp.CallToolResult, mcpBranch, error) {
			p, err := s.mcpPrincipal(req)
			if err != nil {
				return nil, mcpBranch{}, err
			}
			var out mcpBranch
			err = s.audited(req, "snapshot.restore", func() (string, error) {
				d, err := s.database(ctx, p, in.Database, auth.AccessWrite)
				if err != nil {
					return "", err
				}
				b, err := s.eng.RestoreSnapshot(ctx, d.WorkspaceID, d.ID, in.Snapshot, in.Name)
				if err != nil {
					return d.ID + "/" + in.Snapshot, err
				}
				out = s.mcpBranch(d, b)
				return d.ID + "/" + in.Snapshot + "/" + b.ID, nil
			})
			if err != nil {
				return nil, mcpBranch{}, s.toolErr(req, err)
			}
			return nil, out, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "delete_snapshot", Description: "Delete a snapshot. Branches made from it keep working. This can't be undone. Needs admin access to the whole database.", Annotations: destructive},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Database string `json:"database" jsonschema:"database name or id"`
			Snapshot string `json:"snapshot" jsonschema:"snapshot id, from list_snapshots"`
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
			err = s.audited(req, "snapshot.delete", func() (string, error) {
				d, err := s.database(ctx, p, in.Database, auth.AccessAdmin)
				if err != nil {
					return "", err
				}
				if err := s.eng.DeleteSnapshot(ctx, d.WorkspaceID, d.ID, in.Snapshot); err != nil {
					return d.ID + "/" + in.Snapshot, err
				}
				out.Deleted = in.Snapshot
				return d.ID + "/" + in.Snapshot, nil
			})
			if err != nil {
				return nil, out, s.toolErr(req, err)
			}
			return nil, out, nil
		})
}
