// e2e: the end-to-end script from PLAN 13. It boots a real lumbungd and the
// built dashboard, then walks the main flow once, the way a person and their
// app would:
//
//   node scripts/e2e/run.ts        (pnpm e2e)
//
// 1. The owner is set up through the dashboard over HTTP, as the browser
//    does it: a POST to the setupOwner server function with the engine's
//    service token. The session cookie it sets is then used for the rest of
//    the dashboard calls.
// 2. The database is created through the dashboard as the owner, so the
//    engine's audit log names them (dashboard:<email>).
// 3. Everything else goes through the SDK (packages/client/dist, so build it
//    first) and the engine's REST and MCP endpoints.
//
// Settings:
//   LUMBUNG_BIN_DIR=<dir>   use <dir>/lumbungd instead of building it
//   E2E_SKIP_BUILD=1        reuse apps/dashboard/.output instead of building
//
// Each step prints one PASS or FAIL line. Both processes are stopped and
// every temp folder removed, whatever happens.
import { spawn, spawnSync, type ChildProcess } from "node:child_process"
import { existsSync, mkdtempSync, readdirSync, readFileSync, realpathSync, rmSync, statSync } from "node:fs"
import { createServer, type AddressInfo } from "node:net"
import { tmpdir } from "node:os"
import { dirname, join, relative, resolve } from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

import { startEngine, type Running } from "../../packages/client/test/engine.ts"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..")
const dashboardDir = join(root, "apps/dashboard")
const serverBuild = join(dashboardDir, ".output/server")

const OWNER = { name: "E2E Owner", email: "owner@example.com", password: "correct-horse-battery" }
const DB = "e2e-app"

// ---------------------------------------------------------------------------
// Steps and cleanup

class StepFailed extends Error {}

let stepNo = 0
async function step<T>(name: string, fn: () => Promise<T> | T): Promise<T> {
  stepNo++
  const label = `${String(stepNo).padStart(2, " ")}. ${name}`
  const started = Date.now()
  try {
    const out = await fn()
    console.log(`PASS ${label} (${Date.now() - started} ms)`)
    return out
  } catch (e) {
    console.log(`FAIL ${label}\n     ${(e instanceof Error ? e.message : String(e)).replaceAll("\n", "\n     ")}`)
    throw new StepFailed()
  }
}

function check(cond: unknown, message: string): asserts cond {
  if (!cond) throw new Error(message)
}

const cleanups: (() => Promise<void> | void)[] = []
let cleaning: Promise<void> | undefined
function cleanup(): Promise<void> {
  cleaning ??= (async () => {
    for (const fn of cleanups.reverse()) {
      try {
        await fn()
      } catch (e) {
        console.error(`cleanup: ${e instanceof Error ? e.message : String(e)}`)
      }
    }
  })()
  return cleaning
}
for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.once(signal, () => {
    void cleanup().then(() => process.exit(130))
  })
}

function tempDir(prefix: string): string {
  const dir = mkdtempSync(join(tmpdir(), prefix))
  cleanups.push(() => rmSync(dir, { recursive: true, force: true }))
  return dir
}

/** Runs a build command quietly and shows its output only when it fails. */
function run(cmd: string, args: string[], cwd: string) {
  const r = spawnSync(cmd, args, { cwd, encoding: "utf8", maxBuffer: 64 << 20 })
  if (r.status !== 0) throw new Error(`${cmd} ${args.join(" ")} failed with ${r.status ?? r.signal}:\n${(r.stdout + r.stderr).slice(-4000)}`)
}

function freePort(): Promise<number> {
  return new Promise((done, fail) => {
    const s = createServer()
    s.on("error", fail)
    s.listen(0, "127.0.0.1", () => {
      const { port } = s.address() as AddressInfo
      s.close(() => done(port))
    })
  })
}

// ---------------------------------------------------------------------------
// The dashboard process and its server functions

interface DashboardProcess {
  url: string
  log: () => string
}

async function startDashboard(engine: Running): Promise<DashboardProcess> {
  const port = await freePort()
  const url = `http://127.0.0.1:${port}`
  let log = ""
  const child: ChildProcess = spawn(process.execPath, [join(serverBuild, "index.mjs")], {
    cwd: dashboardDir,
    env: {
      ...process.env,
      NODE_ENV: "production",
      HOST: "127.0.0.1",
      PORT: String(port),
      LUMBUNG_ENGINE_URL: engine.url,
      LUMBUNG_SERVICE_TOKEN_FILE: join(engine.dataDir, "secrets", "service-token"),
      LUMBUNG_DASHBOARD_URL: url,
      LUMBUNG_DASHBOARD_DATA_DIR: tempDir("lumbung-e2e-dashboard-"),
    },
    stdio: ["ignore", "pipe", "pipe"],
  })
  child.stdout!.on("data", (c: Buffer) => (log += c.toString()))
  child.stderr!.on("data", (c: Buffer) => (log += c.toString()))
  cleanups.push(
    () =>
      new Promise<void>((done) => {
        if (child.exitCode !== null || child.signalCode !== null) return done()
        const timer = setTimeout(() => child.kill("SIGKILL"), 10_000)
        child.once("exit", () => {
          clearTimeout(timer)
          done()
        })
        child.kill("SIGTERM")
      }),
  )

  const deadline = Date.now() + 30_000
  for (;;) {
    if (child.exitCode !== null) throw new Error(`the dashboard exited with ${child.exitCode}:\n${log.slice(-3000)}`)
    try {
      const res = await fetch(`${url}/app/api/health`)
      if (res.ok) break
    } catch {
      // not listening yet
    }
    if (Date.now() > deadline) throw new Error(`/app/api/health didn't answer 200 within 30s:\n${log.slice(-3000)}`)
    await new Promise((r) => setTimeout(r, 200))
  }
  return { url, log: () => log }
}

// Server function ids, read from the built server code: each one is
// registered as createServerRpc({ id, name, filename }, ...). The browser's
// bundle carries the same ids.
function serverFnIds(): Map<string, string> {
  const ids = new Map<string, string>()
  const re = /id:\s*"([0-9a-f]{16,})",\s*name:\s*"(\w+)",\s*filename:\s*"([^"]+)"/g
  const walk = (dir: string) => {
    for (const name of readdirSync(dir)) {
      const p = join(dir, name)
      if (statSync(p).isDirectory()) walk(p)
      else if (name.endsWith(".mjs")) for (const m of readFileSync(p, "utf8").matchAll(re)) ids.set(`${m[3]}#${m[2]}`, m[1]!)
    }
  }
  walk(serverBuild)
  return ids
}

// Server function bodies and replies are seroval JSON, with the router's
// plugins (errors, for one, use them). Neither is a direct dependency here,
// so load the copies the dashboard's TanStack Start uses, as ES modules.
interface Seroval {
  toJSONAsync: (value: unknown, opts: { plugins: unknown[] }) => Promise<unknown>
  fromCrossJSON: (value: unknown, opts: { refs: Map<number, unknown>; plugins: unknown[] }) => unknown
  plugins: unknown[]
}

/** The folder of package `name` as Node resolves it from `from`. */
function packageDir(from: string, name: string): string {
  for (let dir = from; ; dir = dirname(dir)) {
    const candidate = join(dir, "node_modules", name)
    if (existsSync(join(candidate, "package.json"))) return realpathSync(candidate)
    if (dirname(dir) === dir) throw new Error(`can't find ${name} from ${from}`)
  }
}

/** The ES module entry of `subpath` in a package's exports map. */
function moduleEntry(dir: string, subpath: string): string {
  const { exports } = JSON.parse(readFileSync(join(dir, "package.json"), "utf8")) as { exports: Record<string, unknown> }
  let target: unknown = exports[subpath]
  while (target && typeof target === "object") {
    const t = target as Record<string, unknown>
    target = t.import ?? t.default
  }
  check(typeof target === "string", `${dir}: no import entry for ${subpath}`)
  return pathToFileURL(join(dir, target)).href
}

async function loadSeroval(): Promise<Seroval> {
  const start = packageDir(dashboardDir, "@tanstack/react-start")
  const clientCore = packageDir(start, "@tanstack/start-client-core")
  const routerCore = packageDir(clientCore, "@tanstack/router-core")
  const seroval = await import(moduleEntry(packageDir(clientCore, "seroval"), "."))
  const ssr = await import(moduleEntry(routerCore, "./ssr/client"))
  return { toJSONAsync: seroval.toJSONAsync, fromCrossJSON: seroval.fromCrossJSON, plugins: ssr.createDefaultSerovalPlugins() }
}

class ServerFnError extends Error {}

/** A browser session on the dashboard: its cookies, and server function calls the way the client bundle makes them. */
class Browser {
  #cookies = new Map<string, string>()
  readonly dashboard: DashboardProcess
  readonly ids: Map<string, string>
  readonly seroval: Seroval

  constructor(dashboard: DashboardProcess, ids: Map<string, string>, seroval: Seroval) {
    this.dashboard = dashboard
    this.ids = ids
    this.seroval = seroval
  }

  cookieNames(): string[] {
    return [...this.#cookies.keys()]
  }

  #keep(res: Response) {
    for (const line of res.headers.getSetCookie()) {
      const [pair] = line.split(";")
      const eq = pair!.indexOf("=")
      const name = pair!.slice(0, eq).trim()
      const value = pair!.slice(eq + 1).trim()
      if (!value || /max-age=0\b/i.test(line)) this.#cookies.delete(name)
      else this.#cookies.set(name, value)
    }
  }

  headers(): Record<string, string> {
    const h: Record<string, string> = { origin: this.dashboard.url }
    if (this.#cookies.size) h.cookie = [...this.#cookies].map(([k, v]) => `${k}=${v}`).join("; ")
    return h
  }

  async get(path: string): Promise<Response> {
    const res = await fetch(this.dashboard.url + path, { headers: this.headers() })
    this.#keep(res)
    return res
  }

  /** POSTs to /app/_serverFn/<id>, as the client's serverFnFetcher does. */
  async call<T>(fn: string, data: unknown): Promise<T> {
    const id = this.ids.get(fn)
    check(id, `no server function ${fn} in ${relative(root, serverBuild)}`)
    const res = await fetch(`${this.dashboard.url}/app/_serverFn/${id}`, {
      method: "POST",
      headers: { ...this.headers(), "x-tsr-serverFn": "true", accept: "application/json", "content-type": "application/json" },
      body: JSON.stringify(await this.seroval.toJSONAsync({ data }, { plugins: this.seroval.plugins })),
    })
    this.#keep(res)
    const text = await res.text()
    check(res.headers.get("x-tss-serialized"), `${fn}: HTTP ${res.status}, not a server function reply: ${text.slice(0, 500)}`)
    const out = this.seroval.fromCrossJSON(JSON.parse(text), { refs: new Map(), plugins: this.seroval.plugins }) as { result?: T; error?: unknown }
    if ("error" in out) throw new ServerFnError(out.error instanceof Error ? out.error.message : JSON.stringify(out.error))
    return out.result as T
  }
}

// ---------------------------------------------------------------------------
// MCP over streamable HTTP (stateless, JSON responses)

function mcpClient(engineUrl: string, token: string) {
  let nextId = 1
  let protocolVersion: string | undefined
  return async function rpc(method: string, params?: unknown): Promise<Record<string, unknown>> {
    const headers: Record<string, string> = {
      authorization: `Bearer ${token}`,
      "content-type": "application/json",
      accept: "application/json, text/event-stream",
    }
    if (protocolVersion) headers["mcp-protocol-version"] = protocolVersion
    const res = await fetch(`${engineUrl}/mcp`, { method: "POST", headers, body: JSON.stringify({ jsonrpc: "2.0", id: nextId++, method, params }) })
    const text = await res.text()
    check(res.ok, `${method}: HTTP ${res.status} ${text.slice(0, 500)}`)
    const msg = JSON.parse(text) as { result?: Record<string, unknown>; error?: unknown }
    check(!msg.error && msg.result, `${method}: ${JSON.stringify(msg.error ?? msg)}`)
    if (method === "initialize") protocolVersion = String(msg.result.protocolVersion)
    return msg.result
  }
}

// ---------------------------------------------------------------------------
// The flow

async function main() {
  const clientDist = join(root, "packages/client/dist/index.js")
  if (!existsSync(clientDist)) throw new Error("packages/client/dist is missing. Run `pnpm --filter @lumbung/client build` first.")
  const { createClient } = (await import(pathToFileURL(clientDist).href)) as typeof import("../../packages/client/src/index.ts")

  const bin = await step("build lumbungd", () => {
    if (process.env.LUMBUNG_BIN_DIR) {
      const b = join(process.env.LUMBUNG_BIN_DIR, "lumbungd")
      check(existsSync(b), `${b} doesn't exist`)
      return b
    }
    const dir = tempDir("lumbung-e2e-bin-")
    run("go", ["build", "-o", join(dir, "lumbungd"), "./cmd/lumbungd"], join(root, "apps/engine"))
    return join(dir, "lumbungd")
  })

  await step(process.env.E2E_SKIP_BUILD === "1" ? "reuse the dashboard build" : "build the dashboard", () => {
    if (process.env.E2E_SKIP_BUILD !== "1") run("pnpm", ["--filter", "@lumbung/dashboard", "build"], root)
    check(existsSync(join(serverBuild, "index.mjs")), `${relative(root, serverBuild)}/index.mjs is missing`)
  })

  const engine = await step("start lumbungd", async () => {
    const e = await startEngine(bin)
    cleanups.push(() => e.stop())
    const health = await fetch(`${e.url}/health`)
    check(health.ok, `/health: ${health.status}`)
    return e
  })
  const service = createClient({ url: engine.url, token: engine.token })

  const dashboard = await step("start the dashboard, /app/api/health is 200", () => startDashboard(engine))
  const ids = serverFnIds()
  const seroval = await loadSeroval()
  const owner = new Browser(dashboard, ids, seroval)

  try {
    await step("set up the owner (setupOwner server function)", async () => {
      const r = await owner.call<{ ok: boolean }>("src/fns/session.ts#setupOwner", { serviceToken: engine.token, ...OWNER })
      check(r?.ok === true, `unexpected result ${JSON.stringify(r)}`)
      check(owner.cookieNames().some((n) => n.includes("session_token")), `no session cookie, got: ${owner.cookieNames().join(", ") || "none"}`)
    })

    await step("the session cookie signs the owner in (get-session)", async () => {
      const res = await owner.get("/app/api/auth/get-session")
      const body = (await res.json()) as { user?: { email: string; role?: string } } | null
      check(res.ok, `HTTP ${res.status}`)
      check(body?.user?.email === OWNER.email, `get-session returned ${JSON.stringify(body)}`)
      check(body.user.role === "owner", `role is ${body.user.role}, not owner`)
    })

    await step("a second setup is refused", async () => {
      const stranger = new Browser(dashboard, ids, seroval)
      try {
        await stranger.call("src/fns/session.ts#setupOwner", { serviceToken: engine.token, name: "Second", email: "second@example.com", password: "another-password-1" })
      } catch (e) {
        check(e instanceof ServerFnError && /already has an owner/.test(e.message), `wrong error: ${String(e)}`)
        check(!stranger.cookieNames().some((n) => n.includes("session_token")), "the refused setup still set a session cookie")
        return
      }
      throw new Error("the second setupOwner call succeeded")
    })

    await step(`create database ${DB} through the dashboard as the owner`, async () => {
      const d = await owner.call<{ name: string; default_branch: string }>("src/fns/databases.ts#createDatabase", { name: DB })
      check(d.name === DB && d.default_branch === "main", `unexpected result ${JSON.stringify(d)}`)
    })
  } catch (e) {
    if (e instanceof StepFailed) console.log(`\n--- dashboard output (tail)\n${dashboard.log().slice(-3000)}`)
    throw e
  }

  await step(`the audit log names dashboard:${OWNER.email} for it`, async () => {
    const { data } = await service.audit.list({ limit: 100 })
    const entry = data.find((a) => a.action === "database.create")
    check(entry, `no database.create entry in ${JSON.stringify(data.map((a) => a.action))}`)
    check(entry.actor === `dashboard:${OWNER.email}`, `actor is ${JSON.stringify(entry.actor)}`)
    check(entry.result === "ok", `result is ${entry.result}`)
  })

  const app = service.database(DB)

  await step("create a table and insert a row (SDK batch)", async () => {
    await app.batch(["CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)", { sql: "INSERT INTO notes (body) VALUES (?)", args: ["first"] }])
    const r = await app.query("SELECT id, body FROM notes")
    check(r.rows.length === 1 && r.rows[0]!.body === "first", `rows: ${JSON.stringify(r.rows)}`)
  })

  await step("create branch feature from main", async () => {
    const b = await service.branches.create(DB, "feature", { from: "main" })
    check(b.name === "feature" && !b.default, `unexpected branch ${JSON.stringify(b)}`)
  })

  await step("ALTER TABLE on the branch", async () => {
    await service.database(DB, "feature").query("ALTER TABLE notes ADD COLUMN priority INTEGER NOT NULL DEFAULT 0")
    const main = await app.query("SELECT name FROM pragma_table_info('notes') WHERE name = 'priority'")
    check(main.rows.length === 0, "the column showed up on main too")
  })

  await step("diff feature against main shows the new column", async () => {
    const d = await service.branches.diff(DB, "feature", "main")
    const change = d.changes.find((c) => c.name === "notes")
    check(change, `no change for notes in ${JSON.stringify(d.changes)}`)
    check(change.after.includes("priority") && !change.before.includes("priority"), `change: ${JSON.stringify(change)}`)
  })

  await step("promote feature to the default branch", async () => {
    await service.branches.promote(DB, "feature")
    const d = await service.databases.get(DB)
    check(d.default_branch === "feature", `default branch is ${d.default_branch}`)
    const r = await app.query("SELECT priority FROM notes")
    check(r.rows.length === 1 && r.rows[0]!.priority === 0, `default branch rows: ${JSON.stringify(r.rows)}`)
  })

  const snapshot = await step("snapshot the default branch, then write past it", async () => {
    const s = await service.snapshots.create(DB, "feature", { label: "e2e" })
    check(s.branch_name === "feature", `snapshot of ${s.branch_name}`)
    await app.query("INSERT INTO notes (body, priority) VALUES (?, ?)", ["after the snapshot", 2])
    return s
  })

  await step("restore the snapshot to a new branch and check its data", async () => {
    const b = await service.snapshots.restore(DB, snapshot.id, "restored")
    check(b.source_snapshot_id === snapshot.id, `source_snapshot_id is ${b.source_snapshot_id}`)
    const r = await service.database(DB, "restored").query("SELECT body, priority FROM notes ORDER BY id")
    check(r.rows.length === 1 && r.rows[0]!.body === "first" && r.rows[0]!.priority === 0, `restored rows: ${JSON.stringify(r.rows)}`)
  })

  const token = await step(`create a read token scoped to ${DB}`, async () => {
    const t = await service.tokens.create({ name: "e2e-read", access: "read", database: DB })
    check(t.scope === "database" && t.access === "read" && /^lmb_/.test(t.token), `unexpected token ${JSON.stringify({ ...t, token: "<hidden>" })}`)
    return t.token
  })

  await step("query through the SDK with that token", async () => {
    const scoped = createClient({ url: engine.url, token, database: DB })
    const r = await scoped.query("SELECT body FROM notes ORDER BY id")
    check(r.rows.map((x) => x.body).join(",") === "first,after the snapshot", `rows: ${JSON.stringify(r.rows)}`)
  })

  const rpc = mcpClient(engine.url, token)

  await step("MCP initialize", async () => {
    const r = await rpc("initialize", { protocolVersion: "2025-11-25", capabilities: {}, clientInfo: { name: "lumbung-e2e", version: "0.0.0" } })
    check((r.serverInfo as { name?: string })?.name === "lumbung", `serverInfo: ${JSON.stringify(r.serverInfo)}`)
  })

  await step("MCP tools/list includes query", async () => {
    const r = await rpc("tools/list")
    const names = (r.tools as { name: string }[]).map((t) => t.name)
    check(names.includes("query"), `tools: ${names.join(", ")}`)
  })

  await step("MCP tools/call query returns the rows", async () => {
    const r = await rpc("tools/call", { name: "query", arguments: { database: DB, sql: "SELECT body FROM notes ORDER BY id" } })
    check(!r.isError, `tool error: ${JSON.stringify(r.content)}`)
    const out = r.structuredContent as { rows: unknown[][]; row_count: number }
    check(out?.row_count === 2 && out.rows[0]![0] === "first", `result: ${JSON.stringify(out)}`)
  })
}

let failed = false
try {
  await main()
} catch (e) {
  failed = true
  if (!(e instanceof StepFailed)) console.log(`FAIL ${e instanceof Error ? e.message : String(e)}`)
} finally {
  await cleanup()
}
console.log(failed ? "\ne2e: failed" : `\ne2e: all ${stepNo} steps passed`)
process.exit(failed ? 1 : 0)
