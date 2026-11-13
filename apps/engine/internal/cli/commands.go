package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zakiego/lumbung/apps/engine/internal/client"
)

func ts(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

func tsPtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return ts(*t)
}

func (a *app) loginCmd() *cobra.Command {
	var db string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Save the engine URL and a token as a profile",
		Long:  "Saves the engine URL and a token (and optionally a default database) to the config file, mode 600, as the profile named by --profile (default \"default\"), and makes it current. The token is read from --token, LUMBUNG_TOKEN, or one line on standard input.",
		Example: "  lumbung login --url http://127.0.0.1:4870 --db app < token.txt\n" +
			"  lumbung login --url https://db.example.com --token lmb_... --profile prod",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u := a.flagURL
			if u == "" {
				u = a.getenv("LUMBUNG_URL")
			}
			if u == "" {
				return errors.New("pass --url")
			}
			tok := a.flagToken
			if tok == "" {
				tok = a.getenv("LUMBUNG_TOKEN")
			}
			if tok == "" {
				fmt.Fprint(a.errOut, "Token: ")
				line, err := bufio.NewReader(a.in).ReadString('\n')
				if err != nil && line == "" {
					return errors.New("no token given")
				}
				tok = strings.TrimSpace(line)
			}
			c := client.New(u, tok)
			if err := c.Do(cmd.Context(), http.MethodGet, "/v1/databases", url.Values{"limit": {"1"}}, nil, nil); err != nil {
				return fmt.Errorf("checking the token: %w", err)
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			name := a.flagProfile
			if name == "" {
				name = "default"
			}
			cfg.Profiles[name] = Profile{URL: strings.TrimRight(u, "/"), Token: tok, Database: db}
			cfg.Current = name
			if err := a.saveConfig(cfg); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(map[string]string{"profile": name, "url": u, "config": a.cfgPath})
			}
			fmt.Fprintf(a.out, "Logged in to %s as profile %q (saved in %s)\n", u, name, a.cfgPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&db, "db", "", "default database for commands that need one")
	return cmd
}

// ---- databases

func (a *app) dbCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "db", Short: "List, create, delete and restore databases"}
	cmd.AddCommand(
		&cobra.Command{
			Use: "list", Short: "List databases", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				c, _, err := a.client()
				if err != nil {
					return err
				}
				dbs, err := client.List[client.Database](cmd.Context(), c, "/v1/databases")
				if err != nil {
					return err
				}
				if a.json {
					return a.printJSON(dbs)
				}
				rows := [][]string{}
				for _, d := range dbs {
					rows = append(rows, []string{d.Name, d.ID, strconv.Itoa(d.BranchCount), humanBytes(d.SizeBytes), tsPtr(d.LastActivityAt)})
				}
				a.table([]string{"NAME", "ID", "BRANCHES", "SIZE", "LAST ACTIVITY"}, rows)
				return nil
			},
		},
		&cobra.Command{
			Use: "create NAME", Short: "Create a database with an empty main branch", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, _, err := a.client()
				if err != nil {
					return err
				}
				var d client.Database
				if err := c.Do(cmd.Context(), http.MethodPost, "/v1/databases", nil, map[string]string{"name": args[0]}, &d); err != nil {
					return err
				}
				if a.json {
					return a.printJSON(d)
				}
				fmt.Fprintf(a.out, "Created database %s (%s)\n", d.Name, d.ID)
				return nil
			},
		},
		&cobra.Command{
			Use: "delete NAME", Short: "Move a database to the trash", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, _, err := a.client()
				if err != nil {
					return err
				}
				if err := a.confirm("move database "+args[0]+" to the trash", args[0]); err != nil {
					return err
				}
				var d client.Database
				if err := c.Do(cmd.Context(), http.MethodDelete, client.DBPath(args[0]), nil, nil, &d); err != nil {
					return err
				}
				if a.json {
					return a.printJSON(d)
				}
				fmt.Fprintf(a.out, "Moved %s to the trash. Restore it with `lumbung db restore %s` before %s.\n", d.Name, d.ID, tsPtr(d.PurgeAfter))
				return nil
			},
		},
		a.dbRestoreCmd(),
		&cobra.Command{
			Use: "trash", Short: "List databases in the trash", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				c, _, err := a.client()
				if err != nil {
					return err
				}
				dbs, err := client.List[client.Database](cmd.Context(), c, "/v1/trash")
				if err != nil {
					return err
				}
				if a.json {
					return a.printJSON(dbs)
				}
				rows := [][]string{}
				for _, d := range dbs {
					rows = append(rows, []string{d.Name, d.ID, tsPtr(d.DeletedAt), tsPtr(d.PurgeAfter)})
				}
				a.table([]string{"NAME", "ID", "DELETED", "PURGED AFTER"}, rows)
				return nil
			},
		},
	)
	return cmd
}

func (a *app) dbRestoreCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use: "restore NAME_OR_ID", Short: "Restore a database from the trash", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			body := map[string]string{}
			if name != "" {
				body["name"] = name
			}
			var d client.Database
			if err := c.Do(cmd.Context(), http.MethodPost, client.DBPath(args[0])+"/restore", nil, body, &d); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(d)
			}
			fmt.Fprintf(a.out, "Restored %s (%s)\n", d.Name, d.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "restore under this name instead of the old one")
	return cmd
}

// ---- branches

func (a *app) branchCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "branch", Short: "List, create, delete, reset, promote and diff branches"}
	list := &cobra.Command{
		Use: "list", Short: "List a database's branches", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			bs, err := client.List[client.Branch](cmd.Context(), c, client.DBPath(db)+"/branches")
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(bs)
			}
			rows := [][]string{}
			for _, b := range bs {
				flags := []string{}
				if b.Default {
					flags = append(flags, "default")
				}
				if b.Protected {
					flags = append(flags, "protected")
				}
				if b.ExpiresAt != nil {
					flags = append(flags, "expires "+tsPtr(b.ExpiresAt))
				}
				rows = append(rows, []string{b.Name, b.ID, humanBytes(b.SizeBytes), strings.Join(flags, ", ")})
			}
			a.table([]string{"NAME", "ID", "SIZE", ""}, rows)
			return nil
		},
	}
	a.dbFlags(list, false)

	var from, fromSnapshot string
	var protected bool
	var expires int
	create := &cobra.Command{
		Use: "create NAME", Short: "Create a branch as a copy of another branch or a snapshot", Args: cobra.ExactArgs(1),
		Example: "  lumbung branch create pr-42 --db app --expires-in-days 7",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			body := map[string]any{"name": args[0], "protected": protected, "expires_in_days": expires}
			if from != "" {
				body["from"] = from
			}
			if fromSnapshot != "" {
				body["from_snapshot"] = fromSnapshot
			}
			var b client.Branch
			if err := c.Do(cmd.Context(), http.MethodPost, client.DBPath(db)+"/branches", nil, body, &b); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(b)
			}
			fmt.Fprintf(a.out, "Created branch %s (%s)\n", b.Name, b.ID)
			return nil
		},
	}
	a.dbFlags(create, false)
	create.Flags().StringVar(&from, "from", "", "branch to copy (default: the default branch)")
	create.Flags().StringVar(&fromSnapshot, "from-snapshot", "", "snapshot id to copy instead")
	create.Flags().BoolVar(&protected, "protected", false, "protect the branch from delete and reset")
	create.Flags().IntVar(&expires, "expires-in-days", 0, "delete the branch automatically after this many days")

	simple := func(use, short, verb, method, suffix string, destructive bool) *cobra.Command {
		c := &cobra.Command{
			Use: use + " BRANCH", Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl, p, err := a.client()
				if err != nil {
					return err
				}
				db, err := a.database(p)
				if err != nil {
					return err
				}
				if destructive {
					if err := a.confirm(verb+" branch "+args[0], args[0]); err != nil {
						return err
					}
				}
				var b client.Branch
				var out any = &b
				if method == http.MethodDelete {
					out = nil
				}
				if err := cl.Do(cmd.Context(), method, client.BranchPath(db, args[0])+suffix, nil, nil, out); err != nil {
					return err
				}
				if a.json {
					if out == nil {
						return a.printJSON(map[string]string{"deleted": args[0]})
					}
					return a.printJSON(b)
				}
				fmt.Fprintf(a.out, "Done: %s %s\n", verb, args[0])
				return nil
			},
		}
		a.dbFlags(c, false)
		return c
	}

	var against string
	diff := &cobra.Command{
		Use: "diff BRANCH", Short: "Show how a branch's schema differs from another branch", Args: cobra.ExactArgs(1),
		Example: "  lumbung branch diff pr-42 --db app --against main",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			if against == "" {
				saved := a.flagBranch
				a.flagBranch = ""
				def, err := a.branchName(cmd.Context(), c, db)
				a.flagBranch = saved
				if err != nil {
					return err
				}
				against = def
			}
			var d client.Diff
			if err := c.Do(cmd.Context(), http.MethodGet, client.BranchPath(db, args[0])+"/diff", url.Values{"against": {against}}, nil, &d); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(d)
			}
			if len(d.Changes) == 0 {
				fmt.Fprintf(a.out, "No schema changes between %s and %s\n", d.Branch, d.Against)
				return nil
			}
			for _, ch := range d.Changes {
				fmt.Fprintf(a.out, "%s %s %s\n", map[string]string{"added": "+", "removed": "-", "changed": "~"}[ch.Change], ch.Kind, ch.Name)
				if ch.Before != "" {
					fmt.Fprintf(a.out, "    - %s\n", oneLine(ch.Before))
				}
				if ch.After != "" {
					fmt.Fprintf(a.out, "    + %s\n", oneLine(ch.After))
				}
			}
			return nil
		},
	}
	a.dbFlags(diff, false)
	diff.Flags().StringVar(&against, "against", "", "branch to compare with (default: the default branch)")

	cmd.AddCommand(list, create,
		simple("delete", "Delete a branch", "delete", http.MethodDelete, "", true),
		simple("reset", "Reset a branch to a fresh copy of its parent", "reset", http.MethodPost, "/reset", true),
		simple("promote", "Make a branch the database's default", "promote", http.MethodPost, "/promote", false),
		diff)
	return cmd
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// ---- snapshots

func (a *app) snapshotCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "snapshot", Short: "List, take and restore snapshots"}
	list := &cobra.Command{
		Use: "list", Short: "List a branch's snapshots", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			branch, err := a.branchName(cmd.Context(), c, db)
			if err != nil {
				return err
			}
			sns, err := client.List[client.Snapshot](cmd.Context(), c, client.BranchPath(db, branch)+"/snapshots")
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(sns)
			}
			rows := [][]string{}
			for _, s := range sns {
				rows = append(rows, []string{s.ID, s.Kind, s.Label, humanBytes(s.SizeBytes), ts(s.CreatedAt)})
			}
			a.table([]string{"ID", "KIND", "LABEL", "SIZE", "TAKEN"}, rows)
			return nil
		},
	}
	a.dbFlags(list, true)
	var label string
	create := &cobra.Command{
		Use: "create", Short: "Take a snapshot of a branch", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			branch, err := a.branchName(cmd.Context(), c, db)
			if err != nil {
				return err
			}
			var s client.Snapshot
			if err := c.Do(cmd.Context(), http.MethodPost, client.BranchPath(db, branch)+"/snapshots", nil, map[string]string{"label": label}, &s); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(s)
			}
			fmt.Fprintf(a.out, "Took snapshot %s (%s)\n", s.ID, humanBytes(s.SizeBytes))
			return nil
		},
	}
	a.dbFlags(create, true)
	create.Flags().StringVar(&label, "label", "", "a note to find it by")
	var into string
	restore := &cobra.Command{
		Use: "restore SNAPSHOT_ID", Short: "Restore a snapshot into a new branch", Args: cobra.ExactArgs(1),
		Example: "  lumbung snapshot restore sn_... --db app --into before-migration",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			if into == "" {
				return errors.New("pass --into NAME for the new branch")
			}
			var b client.Branch
			if err := c.Do(cmd.Context(), http.MethodPost, client.DBPath(db)+"/snapshots/"+url.PathEscape(args[0])+"/restore", nil, map[string]string{"name": into}, &b); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(b)
			}
			fmt.Fprintf(a.out, "Restored into new branch %s. Promote it with `lumbung branch promote %s` if it should be the default.\n", b.Name, b.Name)
			return nil
		},
	}
	a.dbFlags(restore, false)
	restore.Flags().StringVar(&into, "into", "", "name of the new branch")
	cmd.AddCommand(list, create, restore)
	return cmd
}

// ---- tokens

func (a *app) tokenCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "token", Short: "List, create and revoke API tokens"}
	cmd.AddCommand(&cobra.Command{
		Use: "list", Short: "List tokens", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			toks, err := client.List[client.Token](cmd.Context(), c, "/v1/tokens")
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(toks)
			}
			rows := [][]string{}
			for _, t := range toks {
				scope := t.Scope
				if t.DatabaseID != "" {
					scope = t.DatabaseID
					if t.BranchID != "" {
						scope += "/" + t.BranchID
					}
				}
				rows = append(rows, []string{t.ID, t.Name, scope, t.Access, tsPtr(t.ExpiresAt), tsPtr(t.LastUsedAt)})
			}
			a.table([]string{"ID", "NAME", "SCOPE", "ACCESS", "EXPIRES", "LAST USED"}, rows)
			return nil
		},
	})
	var name, access, branch string
	var expires int
	create := &cobra.Command{
		Use: "create", Short: "Create a token; the full token is printed once", Args: cobra.NoArgs,
		Example: "  lumbung token create --name ci --db app --access write --expires-in-days 90",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			body := map[string]any{"name": name, "access": access, "expires_in_days": expires}
			if a.flagDB != "" {
				body["database"] = a.flagDB
			}
			if branch != "" {
				body["branch"] = branch
			}
			var t client.Token
			if err := c.Do(cmd.Context(), http.MethodPost, "/v1/tokens", nil, body, &t); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(t)
			}
			fmt.Fprintf(a.errOut, "Created token %s. Copy it now; it won't be shown again:\n", t.ID)
			fmt.Fprintln(a.out, t.Secret)
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "what the token is for")
	create.Flags().StringVar(&access, "access", "read", "read, write or admin")
	create.Flags().StringVar(&a.flagDB, "db", "", "limit the token to this database (default: the whole instance)")
	create.Flags().StringVar(&branch, "branch", "", "limit the token to this branch of --db")
	create.Flags().IntVar(&expires, "expires-in-days", 0, "expire after this many days (0: never)")
	_ = create.MarkFlagRequired("name")
	cmd.AddCommand(create, &cobra.Command{
		Use: "revoke TOKEN_ID", Short: "Revoke a token", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := a.client()
			if err != nil {
				return err
			}
			if err := a.confirm("revoke token "+args[0], args[0]); err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), http.MethodDelete, "/v1/tokens/"+url.PathEscape(args[0]), nil, nil, nil); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(map[string]string{"revoked": args[0]})
			}
			fmt.Fprintf(a.out, "Revoked %s\n", args[0])
			return nil
		},
	})
	return cmd
}

// ---- import and download

func (a *app) importCmd() *cobra.Command {
	var format, mode string
	cmd := &cobra.Command{
		Use:     "import FILE",
		Short:   "Import a SQLite file or SQL dump into a branch",
		Long:    "Imports FILE into a branch. A file starting with the SQLite header is imported as a database file, anything else as a SQL dump, unless --format says otherwise. By default the branch must have no tables; --mode replace overwrites it (not allowed on a protected branch).",
		Example: "  lumbung import app.db --db app\n  lumbung import dump.sql --db app --branch staging --mode replace",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			branch, err := a.branchName(cmd.Context(), c, db)
			if err != nil {
				return err
			}
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			if format == "" {
				head := make([]byte, 16)
				n, _ := io.ReadFull(f, head)
				format = "sql"
				if n == 16 && string(head) == "SQLite format 3\x00" {
					format = "db"
				}
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					return err
				}
			}
			if mode == "replace" {
				if err := a.confirm("replace everything in branch "+branch, branch); err != nil {
					return err
				}
			}
			ct := "application/octet-stream"
			if format == "sql" {
				ct = "application/sql"
			}
			res, err := c.Raw(cmd.Context(), http.MethodPost, client.BranchPath(db, branch)+"/import", url.Values{"format": {format}, "mode": {mode}}, f, ct)
			if err != nil {
				return err
			}
			defer res.Body.Close()
			_, _ = io.Copy(io.Discard, res.Body)
			if a.json {
				return a.printJSON(map[string]string{"imported": args[0], "format": format})
			}
			fmt.Fprintf(a.out, "Imported %s into %s/%s\n", args[0], db, branch)
			return nil
		},
	}
	a.dbFlags(cmd, true)
	cmd.Flags().StringVar(&format, "format", "", "db or sql (default: detected from the file)")
	cmd.Flags().StringVar(&mode, "mode", "empty", "empty or replace")
	return cmd
}

func (a *app) exportCmd() *cobra.Command {
	var format, output string
	cmd := &cobra.Command{
		Use:     "export",
		Short:   "Download a branch as a SQLite file or SQL dump",
		Example: "  lumbung export --db app -o app.db\n  lumbung export --db app --format sql > dump.sql",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			if a.json && (output == "" || output == "-") {
				return errors.New("--json needs -o FILE: the download itself goes to standard output otherwise")
			}
			branch, err := a.branchName(cmd.Context(), c, db)
			if err != nil {
				return err
			}
			res, err := c.Raw(cmd.Context(), http.MethodGet, client.BranchPath(db, branch)+"/export", url.Values{"format": {format}}, nil, "")
			if err != nil {
				return err
			}
			defer res.Body.Close()
			var w io.Writer = a.out
			if output != "" && output != "-" {
				tmp := output + ".partial"
				f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
				if err != nil {
					return err
				}
				n, err := io.Copy(f, res.Body)
				if err != nil {
					f.Close()
					os.Remove(tmp)
					return fmt.Errorf("download cut off: %w", err)
				}
				if err := f.Close(); err != nil {
					return err
				}
				if err := os.Rename(tmp, output); err != nil {
					return err
				}
				if a.json {
					return a.printJSON(map[string]any{"saved": output, "bytes": n, "format": format})
				}
				fmt.Fprintf(a.errOut, "Saved %s (%s)\n", output, humanBytes(n))
				return nil
			}
			_, err = io.Copy(w, res.Body)
			return err
		},
	}
	a.dbFlags(cmd, true)
	cmd.Flags().StringVar(&format, "format", "db", "db or sql")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to this file instead of standard output")
	return cmd
}
