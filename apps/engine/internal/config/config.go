// Package config holds every engine setting, its environment variable, its
// default and its documentation. The Configuration reference page in the
// docs is generated from Settings, so this table is the only place a setting
// is described.
package config

import (
	"fmt"
	"net"
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

	TrashRetention time.Duration
	MaxImportBytes int64
	ImportTimeout  time.Duration

	ServiceToken       string
	RateLimit          int
	RateBurst          int
	AuthFailuresPerMin int
	ClientIPHeader     string
	TrustedProxies     string
	SlowQuery          time.Duration
	StatsRetention     time.Duration
	AuditMaxRows       int

	ReadersPerBranch int
	MaxOpenBranches  int
	IdleConnTimeout  time.Duration
	ConnMaxMemory    int64

	LibSQLStreamIdle      time.Duration
	LibSQLTxTimeout       time.Duration
	LibSQLMaxStreams      int
	LibSQLStreamsPerToken int

	MaxBuckets            int
	MaxObjectBytes        int64
	MaxBucketBytes        int64
	MaxObjectStorageBytes int64
	MultipartExpiry       time.Duration
	PresignMaxExpiry      time.Duration
	PublicRateLimit       int
	ObjectGCDelay         time.Duration

	MaxWorkspaces             int
	MaxTokensPerWorkspace     int
	WorkspaceMaxDatabases     int
	WorkspaceMaxBuckets       int
	WorkspaceMaxDatabaseBytes int64
	WorkspaceMaxObjectBytes   int64
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

// count is integer for a setting where 0 means "no limit".
func count(f func(*Config) *int) func(*Config, string) error {
	return func(c *Config, v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("want 0 or a positive integer, got %q", v)
		}
		*f(c) = n
		return nil
	}
}

// Settings lists every setting in documentation order.
var Settings = []Setting{
	{"LUMBUNG_DATA_DIR", KindString, "./data", "Folder for meta.db, branch files, snapshots, trash, object files and generated secrets. Use a local disk.", str(func(c *Config) *string { return &c.DataDir })},
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
	{"LUMBUNG_MAX_DATABASES", KindInt, "100", "Most databases on the instance, in every workspace together, counting ones in the trash.", integer(func(c *Config) *int { return &c.MaxDatabases })},
	{"LUMBUNG_MAX_BRANCHES", KindInt, "50", "Most branches in one database.", integer(func(c *Config) *int { return &c.MaxBranches })},

	{"LUMBUNG_TRASH_RETENTION", KindDuration, "168h", "How long a deleted database stays in the trash, restorable, before its files are removed.", dur(func(c *Config) *time.Duration { return &c.TrashRetention })},
	{"LUMBUNG_MAX_IMPORT_BYTES", KindBytes, "1GiB", "Largest `.db` file or SQL dump an import accepts. The branch size limit also applies.", bytesOf(func(c *Config) *int64 { return &c.MaxImportBytes })},
	{"LUMBUNG_IMPORT_TIMEOUT", KindDuration, "10m", "Longest an import may run, in place of the statement timeout.", dur(func(c *Config) *time.Duration { return &c.ImportTimeout })},

	{"LUMBUNG_SERVICE_TOKEN", KindString, "", "An instance admin token for the dashboard. When unset, lumbungd generates one on first start and writes it to `secrets/service-token` in the data folder, mode 600.", str(func(c *Config) *string { return &c.ServiceToken })},
	{"LUMBUNG_RATE_LIMIT", KindInt, "50", "Requests per second each token may make, on average. Past it the API answers `rate_limited`.", integer(func(c *Config) *int { return &c.RateLimit })},
	{"LUMBUNG_RATE_BURST", KindInt, "100", "Requests a token may make at once before the rate limit applies.", integer(func(c *Config) *int { return &c.RateBurst })},
	{"LUMBUNG_AUTH_FAILURES_PER_MINUTE", KindInt, "20", "Failed authentications allowed per client address per minute.", integer(func(c *Config) *int { return &c.AuthFailuresPerMin })},
	{"LUMBUNG_CLIENT_IP_HEADER", KindString, "", "Header holding the real client address when lumbungd runs behind a proxy, such as `X-Forwarded-For` or `CF-Connecting-IP`. Only trusted when the connection comes from `LUMBUNG_TRUSTED_PROXIES`. Empty uses the connection's address.", str(func(c *Config) *string { return &c.ClientIPHeader })},
	{"LUMBUNG_TRUSTED_PROXIES", KindString, "127.0.0.1/32,::1/128", "Comma-separated networks whose connections may set the client IP header.", str(func(c *Config) *string { return &c.TrustedProxies })},
	{"LUMBUNG_SLOW_QUERY", KindDuration, "1s", "Queries slower than this go to the branch's slow-query log (the newest 50 are kept).", dur(func(c *Config) *time.Duration { return &c.SlowQuery })},
	{"LUMBUNG_STATS_RETENTION", KindDuration, "48h", "How long per-minute branch stats are kept.", dur(func(c *Config) *time.Duration { return &c.StatsRetention })},
	{"LUMBUNG_AUDIT_MAX_ROWS", KindInt, "100000", "Most audit log entries kept for each workspace, and for the instance's own actions; the oldest are dropped first.", integer(func(c *Config) *int { return &c.AuditMaxRows })},

	{"LUMBUNG_READERS_PER_BRANCH", KindInt, "4", "Read-only connections kept per open branch, next to its one writer.", integer(func(c *Config) *int { return &c.ReadersPerBranch })},
	{"LUMBUNG_MAX_OPEN_BRANCHES", KindInt, "64", "Most branches with open connections at once. The least recently used idle branch is closed to make room.", integer(func(c *Config) *int { return &c.MaxOpenBranches })},
	{"LUMBUNG_IDLE_CONN_TIMEOUT", KindDuration, "5m", "A branch unused this long has its connections closed.", dur(func(c *Config) *time.Duration { return &c.IdleConnTimeout })},
	{"LUMBUNG_CONN_MAX_MEMORY", KindBytes, "128MiB", "Memory ceiling for each SQLite connection. A statement that needs more fails instead of growing the process.", bytesOf(func(c *Config) *int64 { return &c.ConnMaxMemory })},

	{"LUMBUNG_LIBSQL_STREAM_IDLE_TIMEOUT", KindDuration, "10s", "A libSQL protocol stream with no request for this long is closed, and its open transaction rolled back.", dur(func(c *Config) *time.Duration { return &c.LibSQLStreamIdle })},
	{"LUMBUNG_LIBSQL_TX_TIMEOUT", KindDuration, "60s", "Longest an interactive transaction over the libSQL protocol may stay open, from its BEGIN. Past it the transaction is rolled back and its stream closed. An open write transaction holds the branch's only writer, so other writes wait for it.", dur(func(c *Config) *time.Duration { return &c.LibSQLTxTimeout })},
	{"LUMBUNG_LIBSQL_MAX_STREAMS", KindInt, "128", "Most open libSQL protocol streams on the instance. Past it new streams fail with `too_many_streams`.", integer(func(c *Config) *int { return &c.LibSQLMaxStreams })},
	{"LUMBUNG_LIBSQL_MAX_STREAMS_PER_TOKEN", KindInt, "16", "Most open libSQL protocol streams per token.", integer(func(c *Config) *int { return &c.LibSQLStreamsPerToken })},

	{"LUMBUNG_MAX_BUCKETS", KindInt, "100", "Most buckets on the instance, in every workspace together.", integer(func(c *Config) *int { return &c.MaxBuckets })},
	{"LUMBUNG_MAX_OBJECT_BYTES", KindBytes, "5GiB", "Largest object, sent in one piece or assembled from a multipart upload. Larger ones fail with `object_too_large`.", bytesOf(func(c *Config) *int64 { return &c.MaxObjectBytes })},
	{"LUMBUNG_MAX_BUCKET_BYTES", KindBytes, "10GiB", "Most bytes one bucket may hold, counting its multipart uploads in progress. Writes past it fail with `bucket_size_limit`.", bytesOf(func(c *Config) *int64 { return &c.MaxBucketBytes })},
	{"LUMBUNG_MAX_OBJECT_STORAGE_BYTES", KindBytes, "20GiB", "Most bytes all buckets together may hold, counting multipart uploads in progress. Writes past it fail with `object_storage_quota_exceeded`. Separate from `LUMBUNG_MAX_INSTANCE_BYTES`, which covers branch files.", bytesOf(func(c *Config) *int64 { return &c.MaxObjectStorageBytes })},
	{"LUMBUNG_MULTIPART_EXPIRY", KindDuration, "24h", "A multipart upload not completed this long after it started is aborted and its parts removed.", dur(func(c *Config) *time.Duration { return &c.MultipartExpiry })},
	{"LUMBUNG_PRESIGN_MAX_EXPIRY", KindDuration, "168h", "Longest a presigned URL may stay valid. The S3 protocol allows at most 7 days.", dur(func(c *Config) *time.Duration { return &c.PresignMaxExpiry })},
	{"LUMBUNG_PUBLIC_RATE_LIMIT", KindInt, "100", "Requests per second each client address may make without a token, to read objects in public buckets.", integer(func(c *Config) *int { return &c.PublicRateLimit })},
	{"LUMBUNG_OBJECT_GC_DELAY", KindDuration, "1h", "How long the file of a replaced or deleted object is kept before it is removed, so a backup that already copied meta.db can still copy it.", dur(func(c *Config) *time.Duration { return &c.ObjectGCDelay })},

	{"LUMBUNG_MAX_WORKSPACES", KindInt, "100", "Most workspaces on the instance, counting ones in the trash.", integer(func(c *Config) *int { return &c.MaxWorkspaces })},
	{"LUMBUNG_MAX_TOKENS_PER_WORKSPACE", KindInt, "1000", "Most live tokens one workspace may hold. Past it creating a token fails with `limit_reached`.", integer(func(c *Config) *int { return &c.MaxTokensPerWorkspace })},
	{"LUMBUNG_WORKSPACE_MAX_DATABASES", KindInt, "20", "Most databases a new workspace may hold, counting its trash, when the request doesn't say. 0: no limit of its own. Each workspace's limit can be changed later; the default workspace starts with none.", count(func(c *Config) *int { return &c.WorkspaceMaxDatabases })},
	{"LUMBUNG_WORKSPACE_MAX_BUCKETS", KindInt, "20", "Most buckets a new workspace may hold, when the request doesn't say. 0: no limit of its own.", count(func(c *Config) *int { return &c.WorkspaceMaxBuckets })},
	{"LUMBUNG_WORKSPACE_MAX_DATABASE_BYTES", KindBytes, "2GiB", "Most bytes the branch files of a new workspace's databases may take together, when the request doesn't say. 0: no limit of its own. Writes past a workspace's limit fail with `workspace_quota_exceeded`.", bytesOf(func(c *Config) *int64 { return &c.WorkspaceMaxDatabaseBytes })},
	{"LUMBUNG_WORKSPACE_MAX_OBJECT_BYTES", KindBytes, "5GiB", "Most bytes a new workspace's buckets may hold together, counting multipart uploads in progress, when the request doesn't say. 0: no limit of its own.", bytesOf(func(c *Config) *int64 { return &c.WorkspaceMaxObjectBytes })},
}

// Load resolves the configuration: each setting's environment value when
// set and not empty, otherwise its default. A setting whose default is empty
// stays empty unless set.
func Load(getenv func(string) string) (Config, error) {
	var c Config
	for _, s := range Settings {
		v := strings.TrimSpace(getenv(s.Env))
		if v == "" {
			v = s.Default
		}
		if v == "" {
			continue
		}
		if err := s.set(&c, v); err != nil {
			return Config{}, fmt.Errorf("config: %s: %w", s.Env, err)
		}
	}
	if c.MaxValueBytes > 1<<30 || c.MaxSQLBytes > 1<<30 {
		return Config{}, fmt.Errorf("config: LUMBUNG_MAX_VALUE_BYTES and LUMBUNG_MAX_SQL_BYTES can't pass 1GiB")
	}
	if _, err := ParseNetworks(c.TrustedProxies); err != nil {
		return Config{}, fmt.Errorf("config: LUMBUNG_TRUSTED_PROXIES: %w", err)
	}
	if c.PresignMaxExpiry > 7*24*time.Hour {
		return Config{}, fmt.Errorf("config: LUMBUNG_PRESIGN_MAX_EXPIRY can't pass 168h (7 days)")
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

// ParseNetworks reads a comma-separated list of CIDR networks or bare IPs
// (a bare IP is one address).
func ParseNetworks(list string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			ip := net.ParseIP(part)
			if ip == nil {
				return nil, fmt.Errorf("%q isn't an IP or a CIDR network", part)
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("%q isn't an IP or a CIDR network", part)
		}
		out = append(out, n)
	}
	return out, nil
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
