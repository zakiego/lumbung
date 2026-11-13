package meta

// migrations are applied in order, each once. Never edit or reorder an entry
// that has shipped; add a new one.
var migrations = []string{
	// 1: databases and branches.
	`
CREATE TABLE databases (
  id                TEXT PRIMARY KEY,
  name              TEXT NOT NULL,
  default_branch_id TEXT NOT NULL,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL,
  deleted_at        TEXT,
  purge_after       TEXT
) STRICT;
CREATE UNIQUE INDEX databases_live_name ON databases(name) WHERE deleted_at IS NULL;

CREATE TABLE branches (
  id                 TEXT PRIMARY KEY,
  database_id        TEXT NOT NULL REFERENCES databases(id),
  name               TEXT NOT NULL,
  parent_id          TEXT,
  source_snapshot_id TEXT,
  protected          INTEGER NOT NULL DEFAULT 0,
  expires_at         TEXT,
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX branches_name ON branches(database_id, name);
`,

	// 2: snapshots and snapshot schedules.
	`
CREATE TABLE snapshots (
  id          TEXT PRIMARY KEY,
  database_id TEXT NOT NULL REFERENCES databases(id),
  branch_id   TEXT NOT NULL,
  branch_name TEXT NOT NULL,
  kind        TEXT NOT NULL CHECK (kind IN ('manual', 'scheduled')),
  label       TEXT NOT NULL DEFAULT '',
  size_bytes  INTEGER NOT NULL,
  created_at  TEXT NOT NULL
) STRICT;
CREATE INDEX snapshots_by_branch ON snapshots(database_id, branch_id, created_at);

CREATE TABLE snapshot_schedules (
  branch_id     TEXT PRIMARY KEY REFERENCES branches(id) ON DELETE CASCADE,
  database_id   TEXT NOT NULL REFERENCES databases(id),
  every_seconds INTEGER NOT NULL CHECK (every_seconds >= 60),
  keep_count    INTEGER NOT NULL DEFAULT 0 CHECK (keep_count >= 0),
  keep_seconds  INTEGER NOT NULL DEFAULT 0 CHECK (keep_seconds >= 0),
  last_run_at   TEXT,
  created_at    TEXT NOT NULL
) STRICT;
`,

	// 3: tokens, audit log, stats, slow queries and idempotency keys.
	`
CREATE TABLE tokens (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  secret_hash  BLOB NOT NULL,
  scope        TEXT NOT NULL CHECK (scope IN ('instance', 'database')),
  database_id  TEXT REFERENCES databases(id),
  branch_id    TEXT,
  access       TEXT NOT NULL CHECK (access IN ('read', 'write', 'admin')),
  service      INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  expires_at   TEXT,
  last_used_at TEXT,
  revoked_at   TEXT,
  CHECK ((scope = 'instance') = (database_id IS NULL)),
  CHECK (branch_id IS NULL OR database_id IS NOT NULL)
) STRICT;

CREATE TABLE audit (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  at         TEXT NOT NULL,
  token_id   TEXT NOT NULL DEFAULT '',
  actor      TEXT NOT NULL DEFAULT '',
  action     TEXT NOT NULL,
  target     TEXT NOT NULL DEFAULT '',
  result     TEXT NOT NULL CHECK (result IN ('ok', 'error')),
  code       TEXT NOT NULL DEFAULT '',
  request_id TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX audit_at ON audit(at);

CREATE TABLE stats_minute (
  branch_id     TEXT NOT NULL,
  minute        TEXT NOT NULL,
  queries       INTEGER NOT NULL,
  rows_read     INTEGER NOT NULL,
  rows_written  INTEGER NOT NULL,
  errors        INTEGER NOT NULL,
  duration_hist TEXT NOT NULL,
  PRIMARY KEY (branch_id, minute)
) STRICT, WITHOUT ROWID;

CREATE TABLE slow_queries (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  branch_id   TEXT NOT NULL,
  at          TEXT NOT NULL,
  duration_ms INTEGER NOT NULL,
  sql         TEXT NOT NULL,
  error_code  TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX slow_queries_branch ON slow_queries(branch_id, id);

CREATE TABLE idempotency (
  token_id     TEXT NOT NULL,
  key          TEXT NOT NULL,
  method       TEXT NOT NULL,
  path         TEXT NOT NULL,
  request_hash BLOB NOT NULL,
  status       INTEGER NOT NULL,
  body         BLOB NOT NULL,
  created_at   TEXT NOT NULL,
  PRIMARY KEY (token_id, key)
) STRICT;
`,
}
