// Command lumbungd is the Lumbung engine: it owns databases, branches and
// tokens, and serves the REST API and MCP.
//
//	lumbungd            serve (settings come from LUMBUNG_* environment variables)
//	lumbungd openapi    print the OpenAPI document
//	lumbungd version    print the version
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/server"
	"github.com/zakiego/lumbung/apps/engine/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("lumbungd stopped", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
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
	case "", "serve":
	default:
		return fmt.Errorf("unknown command %q (want serve, openapi or version)", cmd)
	}

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
