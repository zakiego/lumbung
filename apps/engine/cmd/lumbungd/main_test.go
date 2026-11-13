package main

import "testing"

func TestEnvOr(t *testing.T) {
	t.Setenv("LUMBUNG_TEST_ENV_OR", "")
	if got := envOr("LUMBUNG_TEST_ENV_OR", "fallback"); got != "fallback" {
		t.Fatalf("empty env: got %q, want fallback", got)
	}
	t.Setenv("LUMBUNG_TEST_ENV_OR", "set")
	if got := envOr("LUMBUNG_TEST_ENV_OR", "fallback"); got != "set" {
		t.Fatalf("set env: got %q, want set", got)
	}
}
