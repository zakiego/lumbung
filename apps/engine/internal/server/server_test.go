package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zakiego/lumbung/apps/engine/internal/auth"
	"github.com/zakiego/lumbung/apps/engine/internal/config"
	"github.com/zakiego/lumbung/apps/engine/internal/meta"
)

func start(t *testing.T, cfg config.Config) (*Server, error) {
	t.Helper()
	s, err := New(context.Background(), cfg, nil)
	if err == nil {
		t.Cleanup(func() { _ = s.Engine.Close() })
	}
	return s, err
}

func liveService(t *testing.T, s *Server) meta.Token {
	t.Helper()
	tok, ok, err := s.Engine.Meta().ServiceToken(context.Background())
	if err != nil || !ok {
		t.Fatalf("no live service token: %v", err)
	}
	return tok
}

func TestServiceTokenRotation(t *testing.T) {
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()

	s, err := start(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	fileTok, err := os.ReadFile(filepath.Join(cfg.DataDir, ServiceTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(cfg.DataDir, ServiceTokenFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode %v", info.Mode().Perm())
	}
	t1 := strings.TrimSpace(string(fileTok))
	if !auth.Matches(t1, liveService(t, s).SecretHash) {
		t.Fatal("file token isn't the live service token")
	}
	_ = s.Engine.Close()

	// The variable wins over the file.
	_, t2 := auth.New()
	cfg.ServiceToken = t2
	s, err = start(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Matches(t2, liveService(t, s).SecretHash) {
		t.Fatal("LUMBUNG_SERVICE_TOKEN not used")
	}
	_ = s.Engine.Close()

	// Unset it again: the file's token comes back.
	cfg.ServiceToken = ""
	s, err = start(t, cfg)
	if err != nil {
		t.Fatalf("restart with the file token: %v", err)
	}
	if !auth.Matches(t1, liveService(t, s).SecretHash) {
		t.Fatal("file token not restored")
	}

	// An id that belongs to another token is refused, and nothing changes.
	other, otherTok := auth.New()
	if _, err := s.Engine.Meta().CreateToken(context.Background(), meta.Token{ID: other, Name: "x", SecretHash: auth.Hash(otherTok), Scope: "instance", Access: "read"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Engine.Close()
	cfg.ServiceToken = otherTok
	if _, err := start(t, cfg); err == nil {
		t.Fatal("a non-service token's id was accepted as the service token")
	}
	cfg.ServiceToken = ""
	s, err = start(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Matches(t1, liveService(t, s).SecretHash) {
		t.Fatal("refused rotation changed the live service token")
	}

	cfg.ServiceToken = "not-a-token"
	_ = s.Engine.Close()
	if _, err := start(t, cfg); err == nil {
		t.Fatal("malformed service token accepted")
	}
}
