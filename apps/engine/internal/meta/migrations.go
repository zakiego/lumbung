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
	// 4: object storage (PLAN 16, ADR 0006): buckets, objects, multipart
	// uploads, delayed file removal, and tokens with the bucket scope and an
	// encrypted S3 access key secret. The tokens table names its scopes in
	// CHECK constraints, so it is rebuilt.
	`
CREATE TABLE buckets (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  public_read  INTEGER NOT NULL DEFAULT 0,
  cors_origins TEXT NOT NULL DEFAULT '[]',
  size_bytes   INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
  object_count INTEGER NOT NULL DEFAULT 0 CHECK (object_count >= 0),
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX buckets_name ON buckets(name);

CREATE TABLE objects (
  bucket_id    TEXT NOT NULL REFERENCES buckets(id),
  key          TEXT NOT NULL,
  blob_id      TEXT NOT NULL,
  size         INTEGER NOT NULL CHECK (size >= 0),
  etag         TEXT NOT NULL,
  content_type TEXT NOT NULL,
  headers      TEXT NOT NULL DEFAULT '{}',
  metadata     TEXT NOT NULL DEFAULT '{}',
  parts        INTEGER NOT NULL DEFAULT 0,
  modified_at  TEXT NOT NULL,
  PRIMARY KEY (bucket_id, key)
) STRICT, WITHOUT ROWID;
CREATE UNIQUE INDEX objects_blob ON objects(blob_id);

CREATE TABLE uploads (
  id           TEXT PRIMARY KEY,
  bucket_id    TEXT NOT NULL REFERENCES buckets(id),
  key          TEXT NOT NULL,
  content_type TEXT NOT NULL,
  headers      TEXT NOT NULL DEFAULT '{}',
  metadata     TEXT NOT NULL DEFAULT '{}',
  token_id     TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL
) STRICT;
CREATE INDEX uploads_bucket ON uploads(bucket_id, key, created_at);

CREATE TABLE upload_parts (
  upload_id   TEXT NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
  part_number INTEGER NOT NULL CHECK (part_number BETWEEN 1 AND 10000),
  file        TEXT NOT NULL,
  size        INTEGER NOT NULL CHECK (size >= 0),
  etag        TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  PRIMARY KEY (upload_id, part_number)
) STRICT;

CREATE TABLE blob_gc (
  path         TEXT PRIMARY KEY,
  delete_after TEXT NOT NULL
) STRICT;

CREATE TABLE tokens_new (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  secret_hash   BLOB NOT NULL,
  scope         TEXT NOT NULL CHECK (scope IN ('instance', 'database', 'bucket')),
  database_id   TEXT REFERENCES databases(id),
  branch_id     TEXT,
  bucket_id     TEXT,
  access        TEXT NOT NULL CHECK (access IN ('read', 'write', 'admin')),
  service       INTEGER NOT NULL DEFAULT 0,
  access_secret BLOB,
  created_at    TEXT NOT NULL,
  expires_at    TEXT,
  last_used_at  TEXT,
  revoked_at    TEXT,
  CHECK ((scope = 'database') = (database_id IS NOT NULL)),
  CHECK ((scope = 'bucket') = (bucket_id IS NOT NULL)),
  CHECK (branch_id IS NULL OR database_id IS NOT NULL)
) STRICT;
INSERT INTO tokens_new (id, name, secret_hash, scope, database_id, branch_id, access, service, created_at, expires_at, last_used_at, revoked_at)
  SELECT id, name, secret_hash, scope, database_id, branch_id, access, service, created_at, expires_at, last_used_at, revoked_at FROM tokens;
DROP TABLE tokens;
ALTER TABLE tokens_new RENAME TO tokens;
CREATE INDEX tokens_bucket ON tokens(bucket_id) WHERE bucket_id IS NOT NULL;
`,
	// 5: tokens that reach several databases and buckets (ADR 0007). Such a
	// token has the scope 'grants' and one token_grants row per thing it
	// reaches, each with its own access level. The tokens table names its
	// scopes in a CHECK constraint, so it is rebuilt again.
	//
	// token_grants is created after the rebuild on purpose. It references
	// tokens with ON DELETE CASCADE, and with foreign keys on, DROP TABLE
	// tokens deletes its rows first. A later migration that rebuilds tokens
	// the same way must copy token_grants aside and back, or every grants
	// token loses its grants.
	`
CREATE TABLE tokens_new (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  secret_hash   BLOB NOT NULL,
  scope         TEXT NOT NULL CHECK (scope IN ('instance', 'database', 'bucket', 'grants')),
  database_id   TEXT REFERENCES databases(id),
  branch_id     TEXT,
  bucket_id     TEXT,
  access        TEXT NOT NULL CHECK (access IN ('read', 'write', 'admin')),
  service       INTEGER NOT NULL DEFAULT 0,
  access_secret BLOB,
  created_at    TEXT NOT NULL,
  expires_at    TEXT,
  last_used_at  TEXT,
  revoked_at    TEXT,
  CHECK ((scope = 'database') = (database_id IS NOT NULL)),
  CHECK ((scope = 'bucket') = (bucket_id IS NOT NULL)),
  CHECK (branch_id IS NULL OR database_id IS NOT NULL)
) STRICT;
INSERT INTO tokens_new (id, name, secret_hash, scope, database_id, branch_id, bucket_id, access, service, access_secret, created_at, expires_at, last_used_at, revoked_at)
  SELECT id, name, secret_hash, scope, database_id, branch_id, bucket_id, access, service, access_secret, created_at, expires_at, last_used_at, revoked_at FROM tokens;
DROP TABLE tokens;
ALTER TABLE tokens_new RENAME TO tokens;
CREATE INDEX tokens_bucket ON tokens(bucket_id) WHERE bucket_id IS NOT NULL;

CREATE TABLE token_grants (
  token_id    TEXT NOT NULL REFERENCES tokens(id) ON DELETE CASCADE,
  position    INTEGER NOT NULL,
  database_id TEXT REFERENCES databases(id),
  branch_id   TEXT,
  bucket_id   TEXT,
  access      TEXT NOT NULL CHECK (access IN ('read', 'write', 'admin')),
  PRIMARY KEY (token_id, position),
  CHECK ((database_id IS NULL) != (bucket_id IS NULL)),
  CHECK (branch_id IS NULL OR database_id IS NOT NULL)
) STRICT, WITHOUT ROWID;
CREATE INDEX token_grants_database ON token_grants(database_id) WHERE database_id IS NOT NULL;
CREATE INDEX token_grants_branch ON token_grants(branch_id) WHERE branch_id IS NOT NULL;
CREATE INDEX token_grants_bucket ON token_grants(bucket_id) WHERE bucket_id IS NOT NULL;
`,
}
