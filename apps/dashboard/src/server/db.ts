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
  `)
}

/** Only for tests. */
export function setDb(d: DatabaseSync | undefined) {
  db = d
}
