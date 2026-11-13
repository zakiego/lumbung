// Package config holds every engine setting, its environment variable, its
// default and its documentation. The Configuration reference page in the
// docs is generated from Settings, so this table is the only place a setting
// is described.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config is the engine's resolved configuration.
type Config struct {
	DataDir string
	Addr    string

	StatementTimeout time.Duration
	BusyTimeout      time.Duration
	MaxResultRows    int
	MaxResultBytes   int64
	MaxRequestBytes  int64
	MaxParams        int
	MaxValueBytes    int64
	MaxSQLBytes      int64

	MaxBranchBytes   int64
	MaxInstanceBytes int64
	MinFreeDiskBytes int64
	MaxDatabases     int
	MaxBranches      int

	ReadersPerBranch int
	MaxOpenBranches  int
	IdleConnTimeout  time.Duration
	ConnMaxMemory    int64
}

// Kind is how a setting's value is written.
type Kind string

const (
	KindString   Kind = "string"
	KindDuration Kind = "duration" // Go duration: 30s, 5m, 1h
	KindBytes    Kind = "bytes"    // 1048576, 512KiB, 64MiB, 10GiB
	KindInt      Kind = "integer"
)

// Setting describes one environment variable.
type Setting struct {
	Env     string
	Kind    Kind
	Default string
	Doc     string
	set     func(*Config, string) error
}

func str(f func(*Config) *string) func(*Config, string) error {
	return func(c *Config, v string) error { *f(c) = v; return nil }
}

func dur(f func(*Config) *time.Duration) func(*Config, string) error {
	return func(c *Config, v string) error {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("want a positive duration like 30s, got %q", v)
		}
		*f(c) = d
		return nil
	}
}

func bytesOf(f func(*Config) *int64) func(*Config, string) error {
	return func(c *Config, v string) error {
		n, err := ParseBytes(v)
		if err != nil {
			return err
		}
		*f(c) = n
		return nil
	}
}

func integer(f func(*Config) *int) func(*Config, string) error {
	return func(c *Config, v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return fmt.Errorf("want a positive integer, got %q", v)
		}
		*f(c) = n
		return nil
	}
}

// Settings lists every setting in documentation order.
var Settings = []Setting{
	{"LUMBUNG_DATA_DIR", KindString, "./data", "Folder for meta.db, branch files, snapshots, trash and generated secrets. Use a local disk.", str(func(c *Config) *string { return &c.DataDir })},
	{"LUMBUNG_ADDR", KindString, "127.0.0.1:4870", "Address the HTTP server listens on.", str(func(c *Config) *string { return &c.Addr })},

	{"LUMBUNG_STATEMENT_TIMEOUT", KindDuration, "30s", "Longest a query or batch may run. Longer ones are cancelled with `statement_timeout` and the connection stays usable.", dur(func(c *Config) *time.Duration { return &c.StatementTimeout })},
	{"LUMBUNG_BUSY_TIMEOUT", KindDuration, "5s", "How long a statement waits for a lock held by another connection before failing with `busy`.", dur(func(c *Config) *time.Duration { return &c.BusyTimeout })},
	{"LUMBUNG_MAX_RESULT_ROWS", KindInt, "10000", "Most rows one statement may return. Past it the request fails with `result_too_large`; nothing partial is returned.", integer(func(c *Config) *int { return &c.MaxResultRows })},
	{"LUMBUNG_MAX_RESULT_BYTES", KindBytes, "16MiB", "Most bytes of values one request may return, counted across all its statements.", bytesOf(func(c *Config) *int64 { return &c.MaxResultBytes })},
	{"LUMBUNG_MAX_REQUEST_BYTES", KindBytes, "32MiB", "Largest JSON request body.", bytesOf(func(c *Config) *int64 { return &c.MaxRequestBytes })},
	{"LUMBUNG_MAX_PARAMS", KindInt, "10000", "Most bound parameters in one statement.", integer(func(c *Config) *int { return &c.MaxParams })},
	{"LUMBUNG_MAX_VALUE_BYTES", KindBytes, "16MiB", "Largest single string or blob a statement may create.", bytesOf(func(c *Config) *int64 { return &c.MaxValueBytes })},
	{"LUMBUNG_MAX_SQL_BYTES", KindBytes, "1MiB", "Longest statement text.", bytesOf(func(c *Config) *int64 { return &c.MaxSQLBytes })},

	{"LUMBUNG_MAX_BRANCH_BYTES", KindBytes, "1GiB", "Largest a branch file may grow. Writes past it fail with `branch_size_limit`.", bytesOf(func(c *Config) *int64 { return &c.MaxBranchBytes })},
	{"LUMBUNG_MAX_INSTANCE_BYTES", KindBytes, "10GiB", "Total size of all branch files. Writes that would pass it fail with `instance_quota_exceeded`.", bytesOf(func(c *Config) *int64 { return &c.MaxInstanceBytes })},
	{"LUMBUNG_MIN_FREE_DISK", KindBytes, "2GiB", "Creating a database, branch, snapshot or import is refused with HTTP 507 when the disk has less free space than this.", bytesOf(func(c *Config) *int64 { return &c.MinFreeDiskBytes })},
	{"LUMBUNG_MAX_DATABASES", KindInt, "100", "Most databases on the instance, counting ones in the trash.", integer(func(c *Config) *int { return &c.MaxDatabases })},
	{"LUMBUNG_MAX_BRANCHES", KindInt, "50", "Most branches in one database.", integer(func(c *Config) *int { return &c.MaxBranches })},

	{"LUMBUNG_READERS_PER_BRANCH", KindInt, "4", "Read-only connections kept per open branch, next to its one writer.", integer(func(c *Config) *int { return &c.ReadersPerBranch })},
	{"LUMBUNG_MAX_OPEN_BRANCHES", KindInt, "64", "Most branches with open connections at once. The least recently used idle branch is closed to make room.", integer(func(c *Config) *int { return &c.MaxOpenBranches })},
	{"LUMBUNG_IDLE_CONN_TIMEOUT", KindDuration, "5m", "A branch unused this long has its connections closed.", dur(func(c *Config) *time.Duration { return &c.IdleConnTimeout })},
	{"LUMBUNG_CONN_MAX_MEMORY", KindBytes, "128MiB", "Memory ceiling for each SQLite connection. A statement that needs more fails instead of growing the process.", bytesOf(func(c *Config) *int64 { return &c.ConnMaxMemory })},
}

// Load resolves the configuration: each setting's environment value when
// set and not empty, otherwise its default.
func Load(getenv func(string) string) (Config, error) {
	var c Config
	for _, s := range Settings {
		v := strings.TrimSpace(getenv(s.Env))
		if v == "" {
			v = s.Default
		}
		if err := s.set(&c, v); err != nil {
			return Config{}, fmt.Errorf("config: %s: %w", s.Env, err)
		}
	}
	if c.MaxValueBytes > 1<<30 || c.MaxSQLBytes > 1<<30 {
		return Config{}, fmt.Errorf("config: LUMBUNG_MAX_VALUE_BYTES and LUMBUNG_MAX_SQL_BYTES can't pass 1GiB")
	}
	if c.ConnMaxMemory < 4<<20 {
		return Config{}, fmt.Errorf("config: LUMBUNG_CONN_MAX_MEMORY must be at least 4MiB")
	}
	return c, nil
}

// Defaults returns the configuration with no environment set.
func Defaults() Config {
	c, err := Load(func(string) string { return "" })
	if err != nil {
		panic(err)
	}
	return c
}

var units = []struct {
	suffix string
	mult   int64
}{
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
	{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000}, {"TB", 1000 * 1000 * 1000 * 1000},
	{"B", 1},
}

// ParseBytes reads a byte size: a plain number or one with a unit suffix
// (KiB, MiB, GiB, TiB, KB, MB, GB, TB, B).
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	mult := int64(1)
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			mult = u.mult
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n > (1<<62)/mult {
		return 0, fmt.Errorf("want a positive size like 64MiB, got %q", s)
	}
	return n * mult, nil
}
