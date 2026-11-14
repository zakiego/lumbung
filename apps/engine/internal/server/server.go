// Package server runs lumbungd: the engine, the HTTP API, the service
// token bootstrap and the background flushes.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zakiego/lumbung/apps/engine/internal/api"
	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/engine"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

// ServiceTokenFile is where the generated service token lives, relative to
// the data directory.
const ServiceTokenFile = "secrets/service-token"

// Server is a running lumbungd.
type Server struct {
	Engine *engine.Engine
	API    *api.Server
	cfg    config.Config
	log    *slog.Logger
}

// New opens the engine, makes sure the service token exists and builds the
// API. Extra options configure the API (the MCP endpoint adds one).
func New(ctx context.Context, cfg config.Config, log *slog.Logger, opts ...api.Option) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	eng, err := engine.Open(ctx, cfg, log)
	if err != nil {
		return nil, err
	}
	if err := eng.Ready(ctx); err == nil {
		if err := ensureServiceToken(ctx, eng, cfg); err != nil {
			_ = eng.Close()
			return nil, err
		}
	}
	return &Server{Engine: eng, API: api.New(eng, log, opts...), cfg: cfg, log: log}, nil
}

// ensureServiceToken keeps one live service token, an instance admin for
// the dashboard. LUMBUNG_SERVICE_TOKEN wins; otherwise the token in the
// secrets file is used, and generated on first start (mode 600).
func ensureServiceToken(ctx context.Context, eng *engine.Engine, cfg config.Config) error {
	path := filepath.Join(eng.Layout().Root, ServiceTokenFile)
	token := strings.TrimSpace(cfg.ServiceToken)
	if token == "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			token = strings.TrimSpace(string(b))
		case errors.Is(err, os.ErrNotExist):
			_, token = auth.New()
			if err := writeSecret(path, token); err != nil {
				return err
			}
		default:
			return fmt.Errorf("server: read %s: %w", path, err)
		}
	}
	id, err := auth.Parse(token)
	if err != nil {
		return fmt.Errorf("server: the service token isn't a valid Lumbung token (lmb_<id>_<secret>)")
	}
	err = eng.Meta().SetServiceToken(ctx, id, auth.Hash(token))
	if errors.Is(err, meta.ErrTokenIDTaken) {
		return fmt.Errorf("server: the service token's id %s belongs to another token or secret; set a newly generated token", id)
	}
	return err
}

func writeSecret(path, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Run serves until ctx is done, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.API.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	errc := make(chan error, 1)
	go func() {
		s.log.Info("lumbungd listening", "addr", ln.Addr().String())
		errc <- srv.Serve(ln)
	}()

	flush := time.NewTicker(30 * time.Second)
	defer flush.Stop()
	for {
		select {
		case err := <-errc:
			s.close()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-flush.C:
			s.flushStats()
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := srv.Shutdown(shutdownCtx)
			cancel()
			s.close()
			return err
		}
	}
}

func (s *Server) flushStats() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.API.Recorder().Flush(ctx, s.Engine.Meta(), s.cfg.StatsRetention); err != nil {
		s.log.Error("flush stats", "err", err)
	}
}

func (s *Server) close() {
	s.API.Close()
	s.flushStats()
	if err := s.Engine.Close(); err != nil {
		s.log.Error("close engine", "err", err)
	}
}

// OpenAPI returns the API document as indented JSON. It builds the API on a
// throwaway data directory, so it needs no running instance.
func OpenAPI() ([]byte, error) {
	dir, err := os.MkdirTemp("", "lumbung-openapi-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cfg := config.Defaults()
	cfg.DataDir = dir
	eng, err := engine.Open(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		return nil, err
	}
	defer eng.Close()
	a := api.New(eng, nil)
	defer a.Close()
	b, err := json.MarshalIndent(a.API().OpenAPI(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// WithThrowawayAPI builds the API on a temporary data directory and calls
// f with it, for generating documents that need no running instance.
func WithThrowawayAPI(f func(*api.Server) error) error {
	dir, err := os.MkdirTemp("", "lumbung-docs-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cfg := config.Defaults()
	cfg.DataDir = dir
	eng, err := engine.Open(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		return err
	}
	defer eng.Close()
	a := api.New(eng, nil)
	defer a.Close()
	return f(a)
}
