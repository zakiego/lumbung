package api

import (
	"testing"

	"github.com/zakiego/lumbung/apps/engine/internal/config"
)

func TestClientIP(t *testing.T) {
	cfg := config.Defaults()
	cfg.ClientIPHeader = "X-Forwarded-For"
	cfg.TrustedProxies = "127.0.0.1, 10.0.0.0/8"
	nets, err := config.ParseNetworks(cfg.TrustedProxies)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{proxies: nets, ipHeader: cfg.ClientIPHeader}
	cases := []struct{ host, header, want string }{
		// The client can put anything on the left; the proxy appends the real one.
		{"127.0.0.1", "6.6.6.6, 203.0.113.9", "203.0.113.9"},
		{"127.0.0.1", "203.0.113.9, 10.1.2.3", "203.0.113.9"},
		{"127.0.0.1", "203.0.113.9", "203.0.113.9"},
		// Not from a trusted proxy: the header is ignored.
		{"198.51.100.7", "203.0.113.9", "198.51.100.7"},
		// Malformed or only proxies: fall back to the connection.
		{"127.0.0.1", "garbage", "127.0.0.1"},
		{"127.0.0.1", "10.0.0.1", "127.0.0.1"},
	}
	for _, c := range cases {
		if got := s.clientIPFrom(c.host, c.header); got != c.want {
			t.Errorf("%s %q: got %s, want %s", c.host, c.header, got, c.want)
		}
	}
	if _, err := config.ParseNetworks("10.0.0.0/8, nope"); err == nil {
		t.Fatal("bad proxy entry accepted")
	}
}
