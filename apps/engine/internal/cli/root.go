// Package cli is the lumbung command line tool (PLAN 9). Every command talks
// to the REST API, so it works against any running lumbungd.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/zakiego/lumbung/apps/engine/internal/client"
	"github.com/zakiego/lumbung/apps/engine/internal/version"
)

// Profile is a saved login.
type Profile struct {
	URL      string `json:"url"`
	Token    string `json:"token"`
	Database string `json:"database,omitempty"`
	// Workspace is the workspace commands act in. Only an instance token
	// needs one; a workspace token always acts in its own.
	Workspace string `json:"workspace,omitempty"`
}

// Config is the CLI's config file.
type Config struct {
	Current  string             `json:"current"`
	Profiles map[string]Profile `json:"profiles"`
}

// app holds what every command shares.
type app struct {
	in      io.Reader
	out     io.Writer
	errOut  io.Writer
	getenv  func(string) string
	cfgPath string

	flagURL, flagToken, flagProfile, flagDB, flagBranch, flagWorkspace string
	json, yes                                                          bool
}

// Options configure Root for tests.
type Options struct {
	In         io.Reader
	Out        io.Writer
	Err        io.Writer
	Getenv     func(string) string
	ConfigPath string
}

// Root returns the lumbung command tree.
func Root(o Options) *cobra.Command {
	a := &app{in: o.In, out: o.Out, errOut: o.Err, getenv: o.Getenv, cfgPath: o.ConfigPath}
	if a.in == nil {
		a.in = os.Stdin
	}
	if a.out == nil {
		a.out = os.Stdout
	}
	if a.errOut == nil {
		a.errOut = os.Stderr
	}
	if a.getenv == nil {
		a.getenv = os.Getenv
	}
	if a.cfgPath == "" {
		a.cfgPath = defaultConfigPath(a.getenv)
	}
	root := &cobra.Command{
		Use:           "lumbung",
		Short:         "Work with a Lumbung instance: databases, branches, queries, migrations, snapshots, buckets and tokens",
		Long:          "lumbung talks to a Lumbung engine over its REST API.\n\nConnection settings come from flags, then LUMBUNG_URL, LUMBUNG_TOKEN, LUMBUNG_DATABASE and LUMBUNG_WORKSPACE, then the profile saved by `lumbung login`.\n\nA token for a workspace acts in that workspace. An instance token acts in the default workspace, or in the one --workspace names.",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(a.in)
	root.SetOut(a.out)
	root.SetErr(a.errOut)
	pf := root.PersistentFlags()
	pf.StringVar(&a.flagURL, "url", "", "engine URL (or LUMBUNG_URL)")
	pf.StringVar(&a.flagToken, "token", "", "API token (or LUMBUNG_TOKEN)")
	pf.StringVar(&a.flagProfile, "profile", "", "saved profile to use")
	pf.StringVarP(&a.flagWorkspace, "workspace", "w", "", "workspace to act in, by name or id (or LUMBUNG_WORKSPACE); only an instance token needs it")
	pf.BoolVar(&a.json, "json", false, "print JSON instead of tables")
	pf.BoolVarP(&a.yes, "yes", "y", false, "don't ask before destructive actions")

	root.AddCommand(a.loginCmd(), a.dbCmd(), a.branchCmd(), a.queryCmd(), a.shellCmd(), a.migrateCmd(),
		a.snapshotCmd(), a.tokenCmd(), a.importCmd(), a.exportCmd(), a.bucketCmd(), a.objectCmd(), a.workspaceCmd(), a.versionCmd())
	return root
}

func defaultConfigPath(getenv func(string) string) string {
	if p := getenv("LUMBUNG_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "lumbung", "config.json")
}

func (a *app) loadConfig() (Config, error) {
	cfg := Config{Profiles: map[string]Profile{}}
	b, err := os.ReadFile(a.cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", a.cfgPath, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	return cfg, nil
}

func (a *app) saveConfig(cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(a.cfgPath), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	tmp := a.cfgPath + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.cfgPath)
}

// profile resolves the connection: flags, then the environment, then the
// saved profile.
func (a *app) profile() (Profile, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return Profile{}, err
	}
	name := a.flagProfile
	if name == "" {
		name = cfg.Current
	}
	saved, ok := cfg.Profiles[name]
	if a.flagProfile != "" && !ok {
		return Profile{}, fmt.Errorf("no saved profile %q; run `lumbung login --profile %s`", a.flagProfile, a.flagProfile)
	}
	override := func(flag, env string) string {
		if flag != "" {
			return flag
		}
		return a.getenv(env)
	}
	p := Profile{URL: override(a.flagURL, "LUMBUNG_URL"), Token: override(a.flagToken, "LUMBUNG_TOKEN"), Database: a.getenv("LUMBUNG_DATABASE")}
	// The saved token only goes to the saved URL, so a --url pointing
	// elsewhere (or a typo) never receives it.
	if p.Token == "" {
		if p.URL != "" && strings.TrimRight(p.URL, "/") != strings.TrimRight(saved.URL, "/") {
			return Profile{}, errors.New("--url or LUMBUNG_URL points somewhere other than the saved profile: pass --token or LUMBUNG_TOKEN for it too")
		}
		p.Token = saved.Token
	}
	if p.URL == "" {
		p.URL = saved.URL
	}
	if p.Database == "" {
		p.Database = saved.Database
	}
	if p.Workspace = override(a.flagWorkspace, "LUMBUNG_WORKSPACE"); p.Workspace == "" {
		p.Workspace = saved.Workspace
	}
	if p.URL == "" || p.Token == "" {
		return Profile{}, errors.New("not logged in: run `lumbung login`, or set LUMBUNG_URL and LUMBUNG_TOKEN")
	}
	return p, nil
}

func (a *app) client() (*client.Client, Profile, error) {
	p, err := a.profile()
	if err != nil {
		return nil, Profile{}, err
	}
	c := client.New(p.URL, p.Token)
	c.UserAgent = "lumbung-cli/" + version.Version
	c.Workspace = p.Workspace
	return c, p, nil
}

// database picks the database: --db, then the profile's default.
func (a *app) database(p Profile) (string, error) {
	if a.flagDB != "" {
		return a.flagDB, nil
	}
	if p.Database != "" {
		return p.Database, nil
	}
	return "", errors.New("no database: pass --db, set LUMBUNG_DATABASE, or log in with --db")
}

// branchName is --branch, or else the database's current default branch,
// looked up so a promoted branch is honored.
func (a *app) branchName(ctx context.Context, c *client.Client, db string) (string, error) {
	if a.flagBranch != "" {
		return a.flagBranch, nil
	}
	var d client.Database
	if err := c.Do(ctx, http.MethodGet, client.DBPath(db), nil, nil, &d); err != nil {
		var apiErr *client.Error
		if errors.As(err, &apiErr) && apiErr.Code == "forbidden" {
			return "", errors.New("this token is limited to one branch: pass --branch")
		}
		return "", err
	}
	return d.DefaultBranch, nil
}

func (a *app) dbFlags(cmd *cobra.Command, branch bool) {
	cmd.Flags().StringVar(&a.flagDB, "db", "", "database name or id (default: the profile's)")
	if branch {
		cmd.Flags().StringVar(&a.flagBranch, "branch", "", "branch name or id (default: the database's default branch)")
	}
}

// confirm asks before a destructive action, unless --yes. The user must
// type the name back.
func (a *app) confirm(what, name string) error {
	if a.yes {
		return nil
	}
	fmt.Fprintf(a.errOut, "This will %s. Type %q to confirm: ", what, name)
	line, _ := bufio.NewReader(a.in).ReadString('\n')
	if strings.TrimSpace(line) != name {
		return errors.New("not confirmed")
	}
	return nil
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table prints rows under a header, aligned.
func (a *app) table(header []string, rows [][]string) {
	tw := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	_ = tw.Flush()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if a.json {
				return a.printJSON(map[string]string{"version": version.Version})
			}
			fmt.Fprintln(a.out, version.Version)
			return nil
		},
	}
}
