package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/zakiego/lumbung/apps/engine/internal/client"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

// Workspaces (PLAN 17.7). Listing shows a workspace token only its own;
// creating, changing, deleting and restoring one need an instance admin.

func wsPath(ref string) string { return "/v1/workspaces/" + url.PathEscape(ref) }

// limit prints a limit: 0 means the workspace has none of its own.
func limit(n int64, bytes bool) string {
	switch {
	case n == 0:
		return "none"
	case bytes:
		return humanBytes(n)
	}
	return strconv.FormatInt(n, 10)
}

// limitFlags are a workspace's limits as flags. Only the ones that were
// passed are sent, so the rest keep their value (or take the instance's
// setting for a new workspace).
type limitFlags struct {
	databases, buckets         int
	databaseBytes, objectBytes string
}

func (l *limitFlags) add(cmd *cobra.Command) {
	cmd.Flags().IntVar(&l.databases, "max-databases", 0, "most databases, counting the trash (0: no limit of its own)")
	cmd.Flags().IntVar(&l.buckets, "max-buckets", 0, "most buckets (0: no limit of its own)")
	cmd.Flags().StringVar(&l.databaseBytes, "max-database-bytes", "", "most space its databases may take together, like 2GiB (0: no limit of its own)")
	cmd.Flags().StringVar(&l.objectBytes, "max-object-bytes", "", "most space its buckets may hold together, like 5GiB (0: no limit of its own)")
}

func (l *limitFlags) body(cmd *cobra.Command, body map[string]any) error {
	if cmd.Flags().Changed("max-databases") {
		body["max_databases"] = l.databases
	}
	if cmd.Flags().Changed("max-buckets") {
		body["max_buckets"] = l.buckets
	}
	for flag, field := range map[string]string{"max-database-bytes": "max_database_bytes", "max-object-bytes": "max_object_bytes"} {
		if !cmd.Flags().Changed(flag) {
			continue
		}
		raw, _ := cmd.Flags().GetString(flag)
		n, err := config.ParseBytes(raw)
		if err != nil {
			return fmt.Errorf("--%s: %w", flag, err)
		}
		body[field] = n
	}
	return nil
}

func (a *app) printWorkspace(w client.Workspace) error {
	if a.json {
		return a.printJSON(w)
	}
	rows := [][]string{{"name", w.Name}, {"id", w.ID}, {"default", strconv.FormatBool(w.Default)}}
	if w.Limits != nil && w.Usage != nil {
		rows = append(rows,
			[]string{"databases", fmt.Sprintf("%d of %s", w.Usage.Databases, limit(int64(w.Limits.MaxDatabases), false))},
			[]string{"buckets", fmt.Sprintf("%d of %s", w.Usage.Buckets, limit(int64(w.Limits.MaxBuckets), false))},
			[]string{"database size", fmt.Sprintf("%s of %s", humanBytes(w.Usage.DatabaseBytes), limit(w.Limits.MaxDatabaseBytes, true))},
			[]string{"object size", fmt.Sprintf("%s of %s", humanBytes(w.Usage.ObjectBytes), limit(w.Limits.MaxObjectBytes, true))},
			[]string{"tokens", strconv.Itoa(w.Usage.Tokens)},
		)
	}
	if w.DeletedAt != nil {
		rows = append(rows, []string{"deleted", tsPtr(w.DeletedAt)}, []string{"purged after", tsPtr(w.PurgeAfter)})
	}
	a.table([]string{"FIELD", "VALUE"}, rows)
	return nil
}

func (a *app) workspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "List, create, change, delete and restore workspaces",
		Long: "A workspace owns databases, buckets and tokens. A token for a workspace sees only its own workspace here. " +
			"Creating, changing, deleting and restoring a workspace need an instance admin token.",
	}
	list := func(use, short string, trashed bool) *cobra.Command {
		return &cobra.Command{
			Use: use, Short: short, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				c, _, err := a.client()
				if err != nil {
					return err
				}
				all, err := listWorkspaces(cmd, c, trashed)
				if err != nil {
					return err
				}
				if a.json {
					return a.printJSON(all)
				}
				rows := [][]string{}
				for _, w := range all {
					row := []string{w.Name, w.ID}
					switch {
					case trashed:
						row = append(row, tsPtr(w.DeletedAt), tsPtr(w.PurgeAfter))
					case w.Usage != nil && w.Limits != nil:
						row = append(row, fmt.Sprintf("%d of %s", w.Usage.Databases, limit(int64(w.Limits.MaxDatabases), false)),
							fmt.Sprintf("%d of %s", w.Usage.Buckets, limit(int64(w.Limits.MaxBuckets), false)),
							humanBytes(w.Usage.DatabaseBytes+w.Usage.ObjectBytes))
					default:
						row = append(row, "", "", "")
					}
					rows = append(rows, row)
				}
				if trashed {
					a.table([]string{"NAME", "ID", "DELETED", "PURGED AFTER"}, rows)
				} else {
					a.table([]string{"NAME", "ID", "DATABASES", "BUCKETS", "SIZE"}, rows)
				}
				return nil
			},
		}
	}

	show := &cobra.Command{
		Use: "show [NAME_OR_ID]", Short: "Show a workspace's limits and what it holds; without a name, the one this token acts in", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			path := "/v1/workspace"
			if len(args) == 1 {
				path = wsPath(args[0])
			}
			var w client.Workspace
			if err := c.Do(cmd.Context(), http.MethodGet, path, nil, nil, &w); err != nil {
				return err
			}
			return a.printWorkspace(w)
		},
	}

	var createLimits limitFlags
	create := &cobra.Command{
		Use: "create NAME", Short: "Create a workspace", Args: cobra.ExactArgs(1),
		Long:    "Creates an empty workspace. Limits that aren't passed take the instance's settings for new workspaces. Make its first token with `lumbung token create --workspace NAME`.",
		Example: "  lumbung workspace create agent --max-databases 20 --max-object-bytes 5GiB\n  lumbung token create --name agent --access admin --workspace agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			body := map[string]any{"name": args[0]}
			if err := createLimits.body(cmd, body); err != nil {
				return err
			}
			var w client.Workspace
			if err := c.Do(cmd.Context(), http.MethodPost, "/v1/workspaces", nil, body, &w); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(w)
			}
			fmt.Fprintf(a.out, "Created workspace %s (%s)\n", w.Name, w.ID)
			return nil
		},
	}
	createLimits.add(create)

	var updateLimits limitFlags
	var rename string
	update := &cobra.Command{
		Use: "update NAME_OR_ID", Short: "Rename a workspace or change its limits", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			body := map[string]any{}
			if rename != "" {
				body["name"] = rename
			}
			if err := updateLimits.body(cmd, body); err != nil {
				return err
			}
			if len(body) == 0 {
				return errors.New("nothing to change: pass --name or a limit")
			}
			var w client.Workspace
			if err := c.Do(cmd.Context(), http.MethodPatch, wsPath(args[0]), nil, body, &w); err != nil {
				return err
			}
			return a.printWorkspace(w)
		},
	}
	update.Flags().StringVar(&rename, "name", "", "the workspace's new name")
	updateLimits.add(update)

	del := &cobra.Command{
		Use: "delete NAME", Short: "Move a workspace to the trash, with everything in it", Args: cobra.ExactArgs(1),
		Long: "Moves a workspace to the trash. At once its tokens stop working and its databases and buckets stop answering. It can be restored, with everything in it, until the trash retention passes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			if err := a.confirmPlain("move workspace "+args[0]+" and everything in it to the trash", args[0]); err != nil {
				return err
			}
			var w client.Workspace
			if err := c.Do(cmd.Context(), http.MethodDelete, wsPath(args[0]), nil, nil, &w); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(w)
			}
			fmt.Fprintf(a.out, "Moved workspace %s to the trash. Restore it with `lumbung workspace restore %s` before %s.\n", w.Name, w.ID, tsPtr(w.PurgeAfter))
			return nil
		},
	}

	var restoreName string
	restore := &cobra.Command{
		Use: "restore NAME_OR_ID", Short: "Restore a workspace from the trash", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			body := map[string]string{}
			if restoreName != "" {
				body["name"] = restoreName
			}
			var w client.Workspace
			if err := c.Do(cmd.Context(), http.MethodPost, wsPath(args[0])+"/restore", nil, body, &w); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(w)
			}
			fmt.Fprintf(a.out, "Restored workspace %s (%s)\n", w.Name, w.ID)
			return nil
		},
	}
	restore.Flags().StringVar(&restoreName, "name", "", "restore under this name instead of the old one")

	cmd.AddCommand(list("list", "List workspaces", false), show, create, update, del, restore, list("trash", "List workspaces in the trash", true))
	return cmd
}

// listWorkspaces fetches every page of the workspace list, or of the trash.
func listWorkspaces(cmd *cobra.Command, c *client.Client, trashed bool) ([]client.Workspace, error) {
	var all []client.Workspace
	cursor := ""
	for {
		q := url.Values{"limit": {"200"}}
		if trashed {
			q.Set("trashed", "true")
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Data       []client.Workspace `json:"data"`
			NextCursor string             `json:"next_cursor"`
		}
		if err := c.Do(cmd.Context(), http.MethodGet, "/v1/workspaces", q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// moveCmd is `db move` and `bucket move`: put one in another workspace.
func (a *app) moveCmd(what string, path func(name string) string) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use: "move NAME", Short: "Move a " + what + " to another workspace", Args: cobra.ExactArgs(1),
		Long: "Moves a " + what + " from the workspace the command acts in to another one. Needs an instance admin token. " +
			"Every token that reached the " + what + " is revoked: make new ones in the new workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			from := ""
			if a.workspace != "" {
				from = " from workspace " + a.workspace
			}
			if err := a.confirmPlain("move "+what+" "+args[0]+from+" to workspace "+to+" and revoke the tokens that reach it", args[0]); err != nil {
				return err
			}
			var moved struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				WorkspaceID string `json:"workspace_id"`
			}
			if err := c.Do(cmd.Context(), http.MethodPost, path(args[0]), nil, map[string]string{"workspace": to}, &moved); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(moved)
			}
			fmt.Fprintf(a.out, "Moved %s %s to workspace %s (%s)\n", what, moved.Name, to, moved.WorkspaceID)
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "the workspace to move it to, by name or id")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}
