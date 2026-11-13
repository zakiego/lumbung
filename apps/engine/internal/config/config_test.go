package config

import (
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c := Defaults()
	if c.StatementTimeout != 30*time.Second || c.MaxBranchBytes != 1<<30 || c.ReadersPerBranch != 4 {
		t.Fatalf("defaults: %+v", c)
	}
	seen := map[string]bool{}
	for _, s := range Settings {
		if seen[s.Env] {
			t.Errorf("%s listed twice", s.Env)
		}
		seen[s.Env] = true
		if s.Doc == "" || s.Default == "" {
			t.Errorf("%s needs a default and a doc", s.Env)
		}
	}
}

func TestLoadOverrides(t *testing.T) {
	env := map[string]string{"LUMBUNG_STATEMENT_TIMEOUT": "2s", "LUMBUNG_MAX_BRANCH_BYTES": "5MB", "LUMBUNG_MAX_RESULT_ROWS": " 7 "}
	c, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.StatementTimeout != 2*time.Second || c.MaxBranchBytes != 5_000_000 || c.MaxResultRows != 7 {
		t.Fatalf("overrides: %+v", c)
	}
}

func TestLoadRejects(t *testing.T) {
	for k, v := range map[string]string{
		"LUMBUNG_STATEMENT_TIMEOUT": "-1s",
		"LUMBUNG_MAX_BRANCH_BYTES":  "lots",
		"LUMBUNG_MAX_RESULT_ROWS":   "0",
		"LUMBUNG_MAX_VALUE_BYTES":   "2GiB",
		"LUMBUNG_CONN_MAX_MEMORY":   "1MiB",
	} {
		if _, err := Load(func(key string) string {
			if key == k {
				return v
			}
			return ""
		}); err == nil {
			t.Errorf("%s=%s accepted", k, v)
		}
	}
}

func TestParseBytes(t *testing.T) {
	for in, want := range map[string]int64{"1": 1, "512KiB": 512 << 10, "64MiB": 64 << 20, "10GiB": 10 << 30, "3MB": 3e6, "7B": 7} {
		if got, err := ParseBytes(in); err != nil || got != want {
			t.Errorf("%s: got %d %v, want %d", in, got, err, want)
		}
	}
}
