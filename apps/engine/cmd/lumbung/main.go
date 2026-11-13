// Command lumbung is the Lumbung CLI. See `lumbung --help`.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/zakiego/lumbung/apps/engine/internal/cli"
	"github.com/zakiego/lumbung/apps/engine/internal/client"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root := cli.Root(cli.Options{})
	if err := root.ExecuteContext(ctx); err != nil {
		var apiErr *client.Error
		if errors.As(err, &apiErr) && apiErr.RequestID != "" {
			fmt.Fprintf(os.Stderr, "error: %v [request %s]\n", err, apiErr.RequestID)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}
}
