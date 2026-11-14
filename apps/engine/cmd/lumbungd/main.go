// Command lumbungd is the Lumbung engine: it owns databases, branches and
// tokens, and serves the REST API and MCP.
//
//	lumbungd                         serve (settings come from LUMBUNG_* environment variables)
//	lumbungd backup --to DIR         copy meta.db, branches and object files to a timestamped folder
//	lumbungd restore --from DIR --to DATA_DIR
//	lumbungd restore --meta-only --from DIR   roll meta.db back, lumbungd stopped
//	lumbungd healthcheck             exit 0 when the running engine answers /health
//	lumbungd service-token           print the generated service token
//	lumbungd openapi                 print the OpenAPI document
//	lumbungd version                 print the version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/backup"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/server"
	"github.com/zakiego/lumbung/apps/engine/internal/version"
)

const usage = `Usage:
  lumbungd                         serve (settings come from LUMBUNG_* environment variables)
  lumbungd backup --to DIR         copy meta.db, branches and object files to a timestamped folder
  lumbungd restore --from DIR --to DATA_DIR
  lumbungd restore --meta-only --from DIR   roll meta.db back, with lumbungd stopped
  lumbungd healthcheck             exit 0 when the running engine answers /health
  lumbungd service-token           print the generated service token
  lumbungd openapi                 print the OpenAPI document
  lumbungd version                 print the version
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "lumbungd:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		fmt.Print(usage)
		return nil
	}
	// These take no arguments. Refusing them keeps `service-token --help`
	// from printing a secret when someone only wanted help.
	switch cmd {
	case "", "serve", "version", "openapi", "healthcheck", "service-token":
		if len(args) > 0 {
			fmt.Fprint(os.Stderr, usage)
			if args[0] == "-h" || args[0] == "--help" {
				return nil
			}
			return fmt.Errorf("%s takes no arguments", cmd)
		}
	}
	switch cmd {
	case "version", "--version", "-version":
		fmt.Println(version.Version)
		return nil
	case "openapi":
		b, err := server.OpenAPI()
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	case "backup":
		return runBackup(args)
	case "restore":
		return runRestore(args)
	case "healthcheck":
		return runHealthcheck()
	case "service-token":
		return printServiceToken()
	case "", "serve":
		return serve()
	default:
		return fmt.Errorf("unknown command %q (want serve, backup, restore, healthcheck, openapi or version)", cmd)
	}
}

func serve() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, err := server.New(ctx, cfg, log)
	if err != nil {
		return err
	}
	log.Info("lumbungd starting", "version", version.Version, "data_dir", srv.Engine.Layout().Root)
	return srv.Run(ctx)
}

func runBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	to := fs.String("to", "", "folder for backups (required); each backup is a timestamped subfolder")
	all := fs.Bool("all-branches", false, "copy every branch, not only each database's default branch")
	metaOnly := fs.Bool("meta-only", false, "copy only meta.db, as before an upgrade (named <time>-meta)")
	skipObjects := fs.Bool("skip-objects", false, "leave object files out; meta.db still lists the objects, and a restore drops them")
	keep := fs.Int("keep", 0, "keep the newest N backups of this kind in --to and delete older ones (0 keeps all)")
	if err := fs.Parse(args); err != nil {
		return helpOK(err)
	}
	if *to == "" {
		return fmt.Errorf("backup: --to is required")
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dir, m, err := backup.Run(ctx, backup.Options{DataDir: cfg.DataDir, To: *to, AllBranches: *all, MetaOnly: *metaOnly, SkipObjects: *skipObjects, Keep: *keep, Limits: engine.Limits(cfg)})
	if dir == "" && err != nil {
		return err
	}
	out := map[string]any{"backup": dir, "files": len(m.Files), "bytes": m.Bytes(), "object_files": m.ObjectFiles(), "linked": m.Linked, "skipped": len(m.Skipped)}
	if len(m.Skipped) > 0 {
		out["skipped"] = m.Skipped
	}
	if encErr := json.NewEncoder(os.Stdout).Encode(out); encErr != nil {
		return encErr
	}
	return err
}

func runRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	from := fs.String("from", "", "a backup folder, or a folder of backups to take the newest from (required)")
	to := fs.String("to", "", "the data folder to restore into: an empty one, or with --meta-only the existing one (default LUMBUNG_DATA_DIR)")
	metaOnly := fs.Bool("meta-only", false, "roll meta.db in an existing data folder back to a backup, with lumbungd stopped")
	if err := fs.Parse(args); err != nil {
		return helpOK(err)
	}
	if *from == "" {
		return fmt.Errorf("restore: --from is required")
	}
	if *to == "" {
		if !*metaOnly {
			return fmt.Errorf("restore: --to is required: an empty folder to restore into")
		}
		cfg, err := config.Load(os.Getenv)
		if err != nil {
			return err
		}
		*to = cfg.DataDir
	}
	src := *from
	if _, err := os.Stat(filepath.Join(src, "manifest.json")); err != nil {
		latest, lerr := backup.Latest(src, *metaOnly)
		if lerr != nil {
			return fmt.Errorf("restore: %s holds no backup of that kind: %w", src, lerr)
		}
		src = latest
	}
	if *metaOnly {
		m, aside, err := backup.RestoreMeta(context.Background(), src, *to, time.Now())
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"restored": src, "into": *to, "meta_only": true, "previous": aside, "created_at": m.CreatedAt})
	}
	m, err := backup.Restore(context.Background(), src, *to)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"restored": src, "into": *to, "files": len(m.Files), "created_at": m.CreatedAt})
}

// helpOK turns -h into a clean exit: the flag package already printed the
// usage.
func helpOK(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

// printServiceToken prints the service token lumbungd generated in the data
// folder (or the one LUMBUNG_SERVICE_TOKEN sets), so an admin can log in
// with the CLI without reading files inside a container.
func printServiceToken() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	if cfg.ServiceToken != "" {
		fmt.Println(cfg.ServiceToken)
		return nil
	}
	b, err := os.ReadFile(filepath.Join(cfg.DataDir, server.ServiceTokenFile))
	if err != nil {
		return fmt.Errorf("no service token yet: start lumbungd once first (%w)", err)
	}
	fmt.Println(strings.TrimSpace(string(b)))
	return nil
}

// runHealthcheck asks the running engine for /health, for a container's
// health check where there's no curl.
func runHealthcheck() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get("http://" + net.JoinHostPort(strings.Trim(host, "[]"), port) + "/health")
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("health: %s", res.Status)
	}
	return nil
}
