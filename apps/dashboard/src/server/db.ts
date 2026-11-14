// The dashboard's own small SQLite file: users, sessions and passkeys (owned
// by Better Auth) plus invites. Databases, branches and tokens live in the
// engine, never here.
import { mkdirSync } from "node:fs"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import { config } from "./config"

let db: DatabaseSync | undefined

export function getDb(): DatabaseSync {
  if (db) return db
  const dir = config().dataDir
  mkdirSync(dir, { recursive: true, mode: 0o700 })
  db = openDb(join(dir, "dashboard.db"))
  return db
}

export function openDb(path: string): DatabaseSync {
  const d = new DatabaseSync(path)
  d.exec("PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;")
  return d
}

/** Tables the dashboard owns. Better Auth creates its own. */
export function migrateOwn(d: DatabaseSync) {
  d.exec(`
    CREATE TABLE IF NOT EXISTS invite (
      id TEXT PRIMARY KEY,
      token_hash TEXT NOT NULL UNIQUE,
      email TEXT NOT NULL,
      role TEXT NOT NULL,
      created_by TEXT NOT NULL,
      created_at INTEGER NOT NULL,
      expires_at INTEGER NOT NULL,
      used_at INTEGER
    );
    -- A person's role in one workspace. Owners have no rows: they reach all.
    CREATE TABLE IF NOT EXISTS workspace_member (
      user_id TEXT NOT NULL,
      workspace_id TEXT NOT NULL,
      role TEXT NOT NULL,
      created_at INTEGER NOT NULL,
      PRIMARY KEY (user_id, workspace_id)
    );
    -- Who made which token through the dashboard. workspace_id is '' for an instance token.
    CREATE TABLE IF NOT EXISTS minted_token (
      token_id TEXT PRIMARY KEY,
      workspace_id TEXT NOT NULL,
      user_id TEXT NOT NULL,
      created_at INTEGER NOT NULL
    );
    CREATE TABLE IF NOT EXISTS dashboard_meta (
      key TEXT PRIMARY KEY,
      value TEXT NOT NULL
    );
  `)
  // The workspaces an invite grants, as JSON. Null on an owner's invite and on
  // invites made before workspaces, which grant the default workspace.
  const columns = d.prepare(`PRAGMA table_info(invite)`).all() as { name: string }[]
  if (!columns.some((c) => c.name === "workspaces")) d.exec(`ALTER TABLE invite ADD COLUMN workspaces TEXT`)
}

/** Only for tests. */
export function setDb(d: DatabaseSync | undefined) {
  db = d
}
