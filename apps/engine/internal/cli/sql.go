package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/zakiego/lumbung/apps/engine/internal/client"
	"github.com/zakiego/lumbung/apps/engine/internal/sqlsplit"
	"github.com/zakiego/lumbung/apps/engine/internal/value"
)

// parseArg reads a --arg. Anything that looks like JSON (a number, true,
// false, null, a quoted string or a typed object) must be valid JSON;
// anything else is the literal text.
func parseArg(s string) (value.Value, error) {
	t := strings.TrimSpace(s)
	jsonish := t == "true" || t == "false" || t == "null" ||
		(t != "" && (strings.ContainsRune("-0123456789\"{[", rune(t[0]))))
	if !jsonish {
		return value.TextValue(s), nil
	}
	var v value.Value
	if err := json.Unmarshal([]byte(t), &v); err != nil {
		return value.Value{}, fmt.Errorf("--arg %s: %w (quote text that starts like a number: '\"%s\"')", s, err, s)
	}
	return v, nil
}

// show renders a value for a table cell.
func show(v value.Value) string {
	switch v.Kind {
	case value.Null:
		return "NULL"
	case value.Integer:
		return strconv.FormatInt(v.Int, 10)
	case value.Float:
		if math.IsInf(v.Float, 0) {
			if v.Float > 0 {
				return "Inf"
			}
			return "-Inf"
		}
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case value.Text:
		return strings.NewReplacer("\n", `\n`, "\t", `\t`, "\r", `\r`).Replace(v.Text)
	case value.Blob:
		if len(v.Blob) > 32 {
			return fmt.Sprintf("x'%s...' (%d bytes)", hex.EncodeToString(v.Blob[:32]), len(v.Blob))
		}
		return "x'" + hex.EncodeToString(v.Blob) + "'"
	}
	return "?"
}

func (a *app) printResult(r *client.Result, took time.Duration) {
	if len(r.Columns) > 0 {
		header := make([]string, len(r.Columns))
		for i, c := range r.Columns {
			header[i] = c.Name
		}
		rows := make([][]string, len(r.Rows))
		for i, row := range r.Rows {
			cells := make([]string, len(row))
			for j, v := range row {
				cells[j] = show(v)
			}
			rows[i] = cells
		}
		a.table(header, rows)
		fmt.Fprintf(a.errOut, "%s (%s)\n", count(len(r.Rows), "row"), took.Round(time.Millisecond))
		return
	}
	fmt.Fprintf(a.errOut, "OK, %s changed (%s)\n", count(int(r.RowsAffected), "row"), took.Round(time.Millisecond))
}

func (a *app) queryCmd() *cobra.Command {
	var args []string
	var readOnly bool
	cmd := &cobra.Command{
		Use:   "query SQL",
		Short: "Run one SQL statement",
		Long:  "Runs one statement with bound parameters in its own transaction. Each --arg binds the next ? placeholder; it is read as JSON (42, 1.5, true, null, \"text\") when it parses, otherwise as text.",
		Example: "  lumbung query \"SELECT * FROM users WHERE id = ?\" --db app --arg 1\n" +
			"  lumbung query \"INSERT INTO users (name) VALUES (?)\" --db app --branch dev --arg Ana",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, pos []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			st := client.Statement{SQL: pos[0]}
			for _, s := range args {
				v, err := parseArg(s)
				if err != nil {
					return err
				}
				st.Args = append(st.Args, v)
			}
			start := time.Now()
			r, err := c.Query(cmd.Context(), db, a.flagBranch, st, readOnly)
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(r)
			}
			a.printResult(r, time.Since(start))
			return nil
		},
	}
	a.dbFlags(cmd, true)
	cmd.Flags().StringArrayVar(&args, "arg", nil, "a parameter value; repeat for each ?")
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "run on a read-only connection")
	return cmd
}

func (a *app) shellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shell",
		Short: "An interactive SQL prompt",
		Long:  "Reads SQL until a statement ends with a semicolon, runs it, and prints the result. Ctrl-C cancels a running statement or clears the line; Ctrl-D or .quit leaves. Commands: .tables, .schema [TABLE], .branch NAME, .help, .quit.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			branch := a.flagBranch
			// Ctrl-C cancels the statement in flight; at the prompt it clears
			// the buffer. It never ends the shell.
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt)
			defer signal.Stop(sig)
			var mu sync.Mutex
			var cancelRun context.CancelFunc
			cleared := make(chan struct{}, 1)
			go func() {
				for range sig {
					mu.Lock()
					if cancelRun != nil {
						cancelRun()
					} else {
						select {
						case cleared <- struct{}{}:
						default:
						}
					}
					mu.Unlock()
				}
			}()

			prompt := func(cont bool) {
				name := branch
				if name == "" {
					name = "default"
				}
				if cont {
					fmt.Fprint(a.errOut, strings.Repeat(" ", len(db)+len(name)+1)+"...> ")
				} else {
					fmt.Fprintf(a.errOut, "%s/%s> ", db, name)
				}
			}
			run := func(sql string) {
				ctx, cancel := context.WithCancel(context.Background())
				mu.Lock()
				cancelRun = cancel
				mu.Unlock()
				defer func() {
					mu.Lock()
					cancelRun = nil
					mu.Unlock()
					cancel()
				}()
				start := time.Now()
				r, err := c.Query(ctx, db, branch, client.Statement{SQL: sql}, false)
				if err != nil {
					if ctx.Err() != nil {
						fmt.Fprintln(a.errOut, "cancelled")
						return
					}
					fmt.Fprintln(a.errOut, "error:", err)
					return
				}
				if a.json {
					_ = a.printJSON(r)
					return
				}
				a.printResult(r, time.Since(start))
			}
			sc := bufio.NewScanner(a.in)
			sc.Buffer(make([]byte, 1<<20), 64<<20)
			var buf strings.Builder
			prompt(false)
			for sc.Scan() {
				select {
				case <-cleared:
					buf.Reset()
				default:
				}
				line := sc.Text()
				if buf.Len() == 0 && strings.HasPrefix(strings.TrimSpace(line), ".") {
					f := strings.Fields(strings.TrimSpace(line))
					switch f[0] {
					case ".quit", ".exit":
						return nil
					case ".help":
						fmt.Fprintln(a.errOut, ".tables  .schema [TABLE]  .branch NAME  .quit")
					case ".tables":
						run("SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite\\_%' ESCAPE '\\' ORDER BY name")
					case ".schema":
						q := "SELECT sql FROM sqlite_schema WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite\\_%' ESCAPE '\\'"
						if len(f) > 1 {
							q += " AND tbl_name = '" + strings.ReplaceAll(f[1], "'", "''") + "'"
						}
						run(q + " ORDER BY type DESC, name")
					case ".branch":
						if len(f) > 1 {
							branch = f[1]
						}
					default:
						fmt.Fprintln(a.errOut, "unknown command; try .help")
					}
					prompt(false)
					continue
				}
				buf.WriteString(line)
				buf.WriteByte('\n')
				stmts, rest := sqlsplit.Split(buf.String())
				for _, st := range stmts {
					run(st)
				}
				buf.Reset()
				buf.WriteString(rest)
				prompt(rest != "")
			}
			if err := sc.Err(); err != nil {
				return err
			}
			// Input ended mid-statement (piped SQL without a final
			// semicolon): run what's there instead of dropping it.
			if rest := strings.TrimSpace(buf.String()); rest != "" {
				run(rest)
			}
			fmt.Fprintln(a.errOut)
			return nil
		},
	}
	a.dbFlags(cmd, true)
	return cmd
}

// ---- migrations

const migrationsTable = `CREATE TABLE IF NOT EXISTS _lumbung_migrations (
  name       TEXT PRIMARY KEY,
  checksum   TEXT NOT NULL,
  applied_at TEXT NOT NULL
)`

// maxBatch is the API's limit on statements in one batch.
const maxBatch = 1000

type migration struct {
	Name     string `json:"name"`
	Checksum string `json:"checksum"`
	Status   string `json:"status"` // applied, pending or changed
	path     string
	body     string
}

func readMigrations(dir string) ([]migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, migration{Name: e.Name(), Checksum: hex.EncodeToString(sum[:]), path: path, body: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// migrationStatus marks each file applied, pending or changed (applied, but
// the file differs from what was applied).
func (a *app) migrationStatus(cmd *cobra.Command, c *client.Client, db, dir string) ([]migration, error) {
	files, err := readMigrations(dir)
	if err != nil {
		return nil, err
	}
	// Read-only: a missing table means nothing has been applied yet.
	exists, err := c.Query(cmd.Context(), db, a.flagBranch, client.Statement{SQL: "SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = '_lumbung_migrations'"}, true)
	if err != nil {
		return nil, err
	}
	applied := map[string]string{}
	if len(exists.Rows) == 1 && exists.Rows[0][0].Int == 1 {
		r, err := c.Query(cmd.Context(), db, a.flagBranch, client.Statement{SQL: "SELECT name, checksum FROM _lumbung_migrations"}, true)
		if err != nil {
			return nil, err
		}
		for _, row := range r.Rows {
			applied[row[0].Text] = row[1].Text
		}
	}
	for i := range files {
		sum, ok := applied[files[i].Name]
		switch {
		case !ok:
			files[i].Status = "pending"
		case sum != files[i].Checksum:
			files[i].Status = "changed"
		default:
			files[i].Status = "applied"
		}
	}
	return files, nil
}

func (a *app) migrateCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply a folder of .sql migrations in order",
		Long:  "Runs each .sql file in --dir in name order, each in its own transaction, and records it in the _lumbung_migrations table. Works with the SQL files drizzle-kit generates (their meta folder is ignored).",
	}
	apply := &cobra.Command{
		Use: "apply", Short: "Apply pending migrations", Args: cobra.NoArgs,
		Example: "  lumbung migrate apply --db app --dir drizzle\n  lumbung migrate apply --db app --branch pr-42 --dir migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			files, err := a.migrationStatus(cmd, c, db, dir)
			if err != nil {
				return err
			}
			for _, f := range files {
				if f.Status == "changed" {
					return fmt.Errorf("%s was applied but the file has changed since; restore it, or add a new migration instead of editing an applied one", f.Name)
				}
			}
			var done []string
			for _, f := range files {
				if f.Status != "pending" {
					continue
				}
				stmts := []client.Statement{{SQL: migrationsTable}}
				for _, s := range sqlsplit.Statements(f.body) {
					stmts = append(stmts, client.Statement{SQL: s})
				}
				stmts = append(stmts, client.Statement{
					SQL:  "INSERT INTO _lumbung_migrations (name, checksum, applied_at) VALUES (?, ?, ?)",
					Args: []value.Value{value.TextValue(f.Name), value.TextValue(f.Checksum), value.TextValue(time.Now().UTC().Format(time.RFC3339))},
				})
				if len(stmts) > maxBatch {
					return fmt.Errorf("%s has %d statements; one migration file can hold at most %d (a batch's limit), so split it", f.Name, len(stmts)-2, maxBatch-2)
				}
				// Migration mode: foreign keys are checked once at the end, so
				// a table rebuild doesn't cascade-delete child rows.
				if _, err := c.Batch(cmd.Context(), db, a.flagBranch, stmts, true); err != nil {
					return fmt.Errorf("%s: %w (it was rolled back; %d applied before it)", f.Name, err, len(done))
				}
				done = append(done, f.Name)
				if !a.json {
					fmt.Fprintf(a.out, "applied %s\n", f.Name)
				}
			}
			if a.json {
				if done == nil {
					done = []string{}
				}
				return a.printJSON(map[string]any{"applied": done})
			}
			if len(done) == 0 {
				fmt.Fprintln(a.out, "Nothing to apply")
			}
			return nil
		},
	}
	status := &cobra.Command{
		Use: "status", Short: "Show which migrations are applied", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, p, err := a.client()
			if err != nil {
				return err
			}
			db, err := a.database(p)
			if err != nil {
				return err
			}
			files, err := a.migrationStatus(cmd, c, db, dir)
			if err != nil {
				return err
			}
			if a.json {
				if files == nil {
					files = []migration{}
				}
				return a.printJSON(files)
			}
			rows := [][]string{}
			for _, f := range files {
				rows = append(rows, []string{f.Name, f.Status})
			}
			a.table([]string{"MIGRATION", "STATUS"}, rows)
			return nil
		},
	}
	for _, c := range []*cobra.Command{apply, status} {
		a.dbFlags(c, true)
		c.Flags().StringVar(&dir, "dir", "migrations", "folder of .sql files")
	}
	cmd.AddCommand(apply, status)
	return cmd
}

// count says "1 row" or "3 rows".
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
