// Copies the dashboard's SQLite file with VACUUM INTO, checks the copy, puts
// the generated sign-in secret next to it, and keeps the newest N copies of
// one kind. Runs inside the dashboard container (node:sqlite, no other
// dependencies), fed on stdin, so the image doesn't need to ship it:
//
//   docker compose exec -T dashboard node - <dir> <prefix> <keep> < ops/dashboard-backup.mjs
//
// Writes <dir>/<prefix>-<UTC time>.db and, when the dashboard generated one,
// <dir>/<prefix>-<UTC time>.auth-secret (secrets/auth-secret, mode 600):
// dashboard.db restored without it signs everyone out. Prints
// {"backup": path, "auth_secret": path or null, "bytes": n, "pruned": n}.
// Exits 2 when there's no dashboard.db yet, 3 when the copy is complete but
// removing old copies failed (so a caller doesn't take another), 1 on any
// other failure.
// LUMBUNG_DASHBOARD_DATA_DIR (set in the image) says where dashboard.db is.
import { chmodSync, copyFileSync, existsSync, mkdirSync, readdirSync, statSync, unlinkSync } from "node:fs"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"

const args = process.argv.slice(process.argv[1] === "-" ? 2 : 1)
const [dir, prefix, keepArg] = args
const keep = Number(keepArg)
if (!dir || !prefix || !/^[a-z0-9-]+$/.test(prefix) || !Number.isInteger(keep) || keep < 1) {
  console.error("usage: node - <dir> <prefix> <keep>")
  process.exit(1)
}

const dataDir = process.env.LUMBUNG_DASHBOARD_DATA_DIR || "/data"
const source = join(dataDir, "dashboard.db")
const secretSource = join(dataDir, "secrets", "auth-secret")
if (!existsSync(source)) {
  console.error(`no dashboard.db at ${source}`)
  process.exit(2)
}

const stamp = new Date().toISOString().replace(/[-:]/g, "").replace(/\.\d+Z$/, "Z")
const dest = join(dir, `${prefix}-${stamp}.db`)
const secretDest = join(dir, `${prefix}-${stamp}.auth-secret`)
mkdirSync(dir, { recursive: true, mode: 0o700 })
if (existsSync(dest)) {
  console.error(`${dest} already exists; wait a second and run again`)
  process.exit(1)
}

function fail(message) {
  for (const f of [dest, secretDest]) if (existsSync(f)) unlinkSync(f)
  console.error(message)
  process.exit(1)
}

// VACUUM INTO reads one consistent snapshot while the dashboard keeps
// writing (WAL), and the copy comes out as a single file.
const src = new DatabaseSync(source, { timeout: 10_000 })
try {
  src.prepare("VACUUM INTO ?").run(dest)
} finally {
  src.close()
}

const copy = new DatabaseSync(dest, { readOnly: true })
let result
try {
  result = copy.prepare("PRAGMA integrity_check").all().map((r) => Object.values(r)[0])
} finally {
  copy.close()
}
if (result.length !== 1 || result[0] !== "ok") fail(`integrity check of the copy failed: ${result.join("; ")}`)

// The secret only exists when LUMBUNG_AUTH_SECRET isn't set (then the
// dashboard generates and keeps one).
let secret = null
if (existsSync(secretSource)) {
  try {
    copyFileSync(secretSource, secretDest)
    chmodSync(secretDest, 0o600)
    secret = secretDest
  } catch (e) {
    fail(`copying ${secretSource} failed: ${e.message}`)
  }
}

// Timestamps sort by name: drop everything after the newest `keep` of each.
let pruned = 0
let retentionError
try {
  for (const ext of ["db", "auth-secret"]) {
    const pattern = new RegExp(`^${prefix}-\\d{8}T\\d{6}Z\\.${ext.replace("-", "\\-")}$`)
    const old = readdirSync(dir).filter((n) => pattern.test(n)).sort().reverse().slice(keep)
    for (const name of old) {
      unlinkSync(join(dir, name))
      if (ext === "db") pruned++
    }
  }
} catch (e) {
  retentionError = e
}

console.log(JSON.stringify({ backup: dest, auth_secret: secret, bytes: statSync(dest).size, pruned }))
if (retentionError) {
  console.error(`${dest} is complete, but removing old copies failed: ${retentionError.message}`)
  process.exit(3)
}
