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
}
