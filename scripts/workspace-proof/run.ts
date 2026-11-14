// workspace-proof: the check PLAN 17 is built for, against a real engine on
// its own port.
//
//   node scripts/workspace-proof/run.ts        (pnpm proof:workspaces)
//
// It starts a fresh lumbungd, makes two workspaces, and then:
//
//   1. A workspace token, used through MCP, makes two databases and a
//      bucket, writes to them and reads them back.
//   2. A token of the other workspace can't see or touch any of it through
//      MCP.
//   3. The same two checks through REST, the libSQL endpoint and the S3 API.
//
// MCP goes through mcporter when it is installed, since that is the client
// we use day to day. Without it (CI), the script speaks MCP's JSON-RPC over
// HTTP itself and says so. The engine is local and on its own port on
// purpose: an ad-hoc mcporter call to a URL that matches a saved server
// reuses the saved server's headers, which would test the wrong token.
//
// libSQL uses @libsql/client from examples/libsql and S3 uses the AWS SDK
// from examples/s3, both unchanged.
import { spawnSync } from "node:child_process"
import { createRequire } from "node:module"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

import { engineBinary, startEngine, type Running } from "../../packages/client/test/engine.ts"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..")

let count = 0
async function step<T>(name: string, fn: () => T | Promise<T>): Promise<T> {
  count++
  const label = `${String(count).padStart(2)}. ${name}`
  try {
    const out = await fn()
    console.log(`PASS ${label}`)
    return out
  } catch (err) {
    console.log(`FAIL ${label}`)
    throw err
  }
}

function assert(ok: unknown, message: string): asserts ok {
  if (!ok) throw new Error(message)
}

// ---------------------------------------------------------------------------
// REST

interface Key {
  token: string
  access_key_id: string
  secret_access_key: string
}

async function rest(engine: Running, token: string, method: string, path: string, body?: unknown): Promise<{ status: number; json: any; text: string }> {
  const res = await fetch(engine.url + path, {
    method,
    headers: { authorization: `Bearer ${token}`, ...(body === undefined ? {} : { "content-type": "application/json" }) },
    body: body === undefined ? undefined : typeof body === "string" ? body : JSON.stringify(body),
  })
  const text = await res.text()
  let json: any
  try {
    json = JSON.parse(text)
  } catch {
    json = undefined
  }
  return { status: res.status, json, text }
}

async function ok(engine: Running, token: string, method: string, path: string, body?: unknown) {
  const r = await rest(engine, token, method, path, body)
  assert(r.status < 300, `${method} ${path}: ${r.status} ${r.text}`)
  return r.json
}

// ---------------------------------------------------------------------------
// MCP: mcporter, or JSON-RPC by hand

type ToolResult = { ok: true; data: any } | { ok: false; error: string }
type MCP = (tool: string, args?: Record<string, unknown>) => Promise<ToolResult>

// WORKSPACE_PROOF_MCP=jsonrpc skips mcporter even when it is installed, to
// run the path CI takes.
const haveMcporter = process.env.WORKSPACE_PROOF_MCP !== "jsonrpc" && spawnSync("mcporter", ["--version"], { encoding: "utf8" }).status === 0

function viaMcporter(engine: Running, token: string, name: string): MCP {
  return async (tool, args = {}) => {
    const r = spawnSync(
      "mcporter",
      ["call", "--http-url", `${engine.url}/mcp`, "--allow-http", "--name", name, "--header", `Authorization=Bearer ${token}`, "--tool", tool, "--args", JSON.stringify(args), "--output", "json"],
      { encoding: "utf8", maxBuffer: 16 << 20 },
    )
    let parsed: any
    try {
      parsed = JSON.parse(r.stdout)
    } catch {
      throw new Error(`mcporter ${tool}: unreadable output (exit ${r.status}): ${r.stdout}${r.stderr}`)
    }
    if (parsed?.isError) return { ok: false, error: (parsed.content ?? []).map((c: any) => c.text ?? "").join(" ") }
    assert(r.status === 0, `mcporter ${tool} exited ${r.status}: ${r.stderr}`)
    return { ok: true, data: parsed }
  }
}

function viaJSONRPC(engine: Running, token: string): MCP {
  let id = 1
  let ready: Promise<void> | undefined
  const rpc = async (method: string, params: unknown) => {
    const res = await fetch(`${engine.url}/mcp`, {
      method: "POST",
      headers: { authorization: `Bearer ${token}`, "content-type": "application/json", accept: "application/json, text/event-stream" },
      body: JSON.stringify({ jsonrpc: "2.0", id: id++, method, params }),
    })
    const text = await res.text()
    assert(res.ok, `MCP ${method}: HTTP ${res.status} ${text}`)
    const body = JSON.parse(text) as any
    assert(!body.error, `MCP ${method}: ${JSON.stringify(body.error)}`)
    return body.result
  }
  return async (tool, args = {}) => {
    ready ??= rpc("initialize", { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "workspace-proof", version: "1" } }).then(() => undefined)
    await ready
    const result = await rpc("tools/call", { name: tool, arguments: args })
    if (result.isError) return { ok: false, error: (result.content ?? []).map((c: any) => c.text ?? "").join(" ") }
    return { ok: true, data: result.structuredContent }
  }
}

async function must(mcp: MCP, tool: string, args?: Record<string, unknown>) {
  const r = await mcp(tool, args)
  assert(r.ok, `${tool}: ${r.ok ? "" : r.error}`)
  return r.data
}

async function refused(mcp: MCP, tool: string, args: Record<string, unknown>, code: string) {
  const r = await mcp(tool, args)
  assert(!r.ok, `${tool} ${JSON.stringify(args)} went through: ${JSON.stringify(r.ok ? r.data : "")}`)
  assert(r.error.includes(code), `${tool}: expected ${code}, got: ${r.error}`)
}

// ---------------------------------------------------------------------------

async function main() {
  const engine = await step("start a fresh lumbungd on its own port", async () => startEngine(engineBinary(), { LUMBUNG_RATE_LIMIT: "100000", LUMBUNG_RATE_BURST: "100000" }))
  try {
    const admin = engine.token // the service token: an instance admin

    const tokens = await step("the instance admin makes two workspaces and one admin token for each", async () => {
      const out: Record<string, Key> = {}
      for (const name of ["agent", "other"]) {
        await ok(engine, admin, "POST", "/v1/workspaces", { name })
        out[name] = await ok(engine, admin, "POST", `/v1/workspaces/${name}/tokens`, { name: "admin", access: "admin", access_key: true })
      }
      return out
    })
    const agent = tokens.agent!
    const other = tokens.other!

    console.log(haveMcporter ? "     MCP client: mcporter" : "     MCP client: JSON-RPC over HTTP (mcporter not used)")
    const agentMCP = haveMcporter ? viaMcporter(engine, agent.token, "lumbung-proof-agent") : viaJSONRPC(engine, agent.token)
    const otherMCP = haveMcporter ? viaMcporter(engine, other.token, "lumbung-proof-other") : viaJSONRPC(engine, other.token)

    // 1. MCP: the agent makes its own and uses them.
    await step("MCP: the agent's workspace starts empty", async () => {
      assert((await must(agentMCP, "list_databases")).databases.length === 0, "a new workspace has databases")
      assert((await must(agentMCP, "list_buckets")).buckets.length === 0, "a new workspace has buckets")
      const ws = await must(agentMCP, "get_workspace")
      assert(ws.name === "agent" && ws.usage.databases === 0, `get_workspace: ${JSON.stringify(ws)}`)
    })
    for (const db of ["memory", "tasks"]) {
      await step(`MCP: the agent creates database ${db}, writes to it and reads it back`, async () => {
        await must(agentMCP, "create_database", { name: db })
        await must(agentMCP, "execute", { database: db, sql: "CREATE TABLE items (id INTEGER PRIMARY KEY, body TEXT NOT NULL)" })
        await must(agentMCP, "execute", { database: db, sql: "INSERT INTO items (body) VALUES (?)", args: [`kept in ${db}`] })
        const rows = await must(agentMCP, "query", { database: db, sql: "SELECT body FROM items" })
        assert(JSON.stringify(rows).includes(`kept in ${db}`), `read back ${db}: ${JSON.stringify(rows)}`)
      })
    }
    await step("MCP: the agent creates bucket attachments, writes an object and reads it back", async () => {
      await must(agentMCP, "create_bucket", { name: "attachments" })
      await must(agentMCP, "put_object", { bucket: "attachments", key: "notes/a.md", text: "# kept in attachments" })
      const got = await must(agentMCP, "get_object", { bucket: "attachments", key: "notes/a.md" })
      assert(got.text === "# kept in attachments", `get_object: ${JSON.stringify(got)}`)
      assert((await must(agentMCP, "list_databases")).databases.length === 2, "list_databases after creating two")
    })

    // 2. MCP: the other workspace.
    await step("MCP: the other workspace's token sees no databases and no buckets", async () => {
      assert((await must(otherMCP, "list_databases")).databases.length === 0, "the other workspace lists databases")
      assert((await must(otherMCP, "list_buckets")).buckets.length === 0, "the other workspace lists buckets")
    })
    await step("MCP: the other workspace's token can't read, write or delete any of it", async () => {
      await refused(otherMCP, "query", { database: "memory", sql: "SELECT * FROM items" }, "database_not_found")
      await refused(otherMCP, "execute", { database: "tasks", sql: "DROP TABLE items" }, "database_not_found")
      await refused(otherMCP, "get_database", { database: "memory" }, "database_not_found")
      await refused(otherMCP, "delete_database", { database: "memory" }, "database_not_found")
      await refused(otherMCP, "get_object", { bucket: "attachments", key: "notes/a.md" }, "bucket_not_found")
      await refused(otherMCP, "put_object", { bucket: "attachments", key: "x", text: "x" }, "bucket_not_found")
      await refused(otherMCP, "delete_object", { bucket: "attachments", key: "notes/a.md" }, "bucket_not_found")
      await refused(otherMCP, "list_objects", { bucket: "attachments" }, "bucket_not_found")
      await refused(otherMCP, "delete_bucket", { bucket: "attachments" }, "bucket_not_found")
    })
    await step("MCP: a name the agent uses is free in the other workspace", async () => {
      await must(otherMCP, "create_database", { name: "memory" })
      await must(otherMCP, "create_bucket", { name: "attachments" })
      const rows = await must(agentMCP, "query", { database: "memory", sql: "SELECT body FROM items" })
      assert(JSON.stringify(rows).includes("kept in memory"), "the agent's memory changed")
    })
    const ids = await step("REST: collect the agent's database and bucket ids, to aim at by id", async () => {
      const dbs = (await ok(engine, agent.token, "GET", "/v1/databases")).data as { id: string; name: string }[]
      const buckets = (await ok(engine, agent.token, "GET", "/v1/buckets")).data as { id: string; name: string }[]
      assert(dbs.length === 2 && buckets.length === 1, `the agent's lists: ${JSON.stringify({ dbs, buckets })}`)
      return { memory: dbs.find((d) => d.name === "memory")!.id, attachments: buckets[0]!.id }
    })
    await step("MCP: the other workspace's token can't reach them by id either", async () => {
      await refused(otherMCP, "query", { database: ids.memory, sql: "SELECT * FROM items" }, "database_not_found")
      await refused(otherMCP, "get_object", { bucket: ids.attachments, key: "notes/a.md" }, "bucket_not_found")
    })

    // 3. REST.
    await step("REST: the agent reads its rows and its object", async () => {
      const rows = await ok(engine, agent.token, "POST", "/v1/databases/tasks/query", { sql: "SELECT body FROM items" })
      assert(JSON.stringify(rows).includes("kept in tasks"), `REST rows: ${JSON.stringify(rows)}`)
      const obj = await rest(engine, agent.token, "GET", "/v1/buckets/attachments/object?key=notes/a.md")
      assert(obj.status === 200 && obj.text === "# kept in attachments", `REST object: ${obj.status} ${obj.text}`)
    })
    await step("REST: the other workspace's token gets not found, by name, by id and by naming the workspace", async () => {
      const expect = async (method: string, path: string, body: unknown, status: number, code: string) => {
        const r = await rest(engine, other.token, method, path, body)
        assert(r.status === status && r.json?.code === code, `${method} ${path}: ${r.status} ${r.text}`)
      }
      await expect("POST", "/v1/databases/tasks/query", { sql: "SELECT * FROM items" }, 404, "database_not_found")
      await expect("POST", `/v1/databases/${ids.memory}/query`, { sql: "SELECT * FROM items" }, 404, "database_not_found")
      await expect("DELETE", "/v1/databases/tasks", undefined, 404, "database_not_found")
      await expect("GET", `/v1/buckets/${ids.attachments}/object?key=notes/a.md`, undefined, 404, "bucket_not_found")
      await expect("GET", "/v1/workspaces/agent/databases", undefined, 404, "workspace_not_found")
      await expect("GET", "/v1/workspaces/no-such-workspace/databases", undefined, 404, "workspace_not_found")
      await expect("POST", "/v1/tokens", { name: "x", database: ids.memory, access: "read" }, 404, "database_not_found")
      const list = await ok(engine, other.token, "GET", "/v1/databases")
      assert(JSON.stringify(list.data.map((d: any) => d.name)) === '["memory"]', `the other workspace's list: ${JSON.stringify(list.data)}`)
      const workspaces = await ok(engine, other.token, "GET", "/v1/workspaces")
      assert(workspaces.data.length === 1 && workspaces.data[0].name === "other", `the other token's workspaces: ${JSON.stringify(workspaces.data)}`)
    })

    // 4. libSQL, with @libsql/client unchanged.
    const libsql = createRequire(join(root, "examples/libsql/package.json"))("@libsql/client") as typeof import("@libsql/client")
    await step("libSQL: the agent reads and writes with @libsql/client, on the usual URL", async () => {
      const client = libsql.createClient({ url: `${engine.url}/libsql/memory/main/`, authToken: agent.token })
      await client.execute({ sql: "INSERT INTO items (body) VALUES (?)", args: ["written over libSQL"] })
      const rows = await client.execute("SELECT body FROM items ORDER BY id")
      assert(rows.rows.length === 2 && rows.rows[1]!.body === "written over libSQL", `libSQL rows: ${JSON.stringify(rows.rows)}`)
      client.close()
    })
    await step("libSQL: the other workspace's token can't open the agent's databases", async () => {
      for (const db of ["tasks", ids.memory]) {
        const client = libsql.createClient({ url: `${engine.url}/libsql/${db}/main/`, authToken: other.token })
        let message = ""
        try {
          await client.execute("SELECT * FROM items")
        } catch (err) {
          message = String(err)
        } finally {
          client.close()
        }
        assert(message.includes("no database") || message.includes("database_not_found") || message.includes("404"), `libSQL on ${db}: ${message || "the query went through"}`)
      }
      // Its own database of the same name holds nothing of the agent's.
      const own = libsql.createClient({ url: `${engine.url}/libsql/memory/main/`, authToken: other.token })
      const tables = await own.execute("SELECT count(*) AS n FROM sqlite_schema")
      own.close()
      assert(Number(tables.rows[0]!.n) === 0, "the other workspace's memory has the agent's tables")
    })

    // 5. S3, with the AWS SDK unchanged.
    const aws = createRequire(join(root, "examples/s3/package.json"))("@aws-sdk/client-s3") as typeof import("@aws-sdk/client-s3")
    const s3 = (key: Key) =>
      new aws.S3Client({
        endpoint: `${engine.url}/v1/s3`,
        forcePathStyle: true,
        region: "us-east-1",
        credentials: { accessKeyId: key.access_key_id, secretAccessKey: key.secret_access_key },
      })
    await step("S3: the agent reads its object and writes another with the AWS SDK", async () => {
      const client = s3(agent)
      const got = await client.send(new aws.GetObjectCommand({ Bucket: "attachments", Key: "notes/a.md" }))
      assert((await got.Body!.transformToString()) === "# kept in attachments", "S3 read")
      await client.send(new aws.PutObjectCommand({ Bucket: "attachments", Key: "notes/b.md", Body: "written over S3" }))
      const list = await client.send(new aws.ListObjectsV2Command({ Bucket: "attachments" }))
      assert(list.KeyCount === 2, `S3 list: ${list.KeyCount}`)
      const buckets = await client.send(new aws.ListBucketsCommand({}))
      assert(buckets.Buckets?.length === 1 && buckets.Buckets[0]!.Name === "attachments", "S3 ListBuckets for the agent")
    })
    await step("S3: the other workspace's key gets NoSuchBucket for the agent's bucket, and its own of that name is empty", async () => {
      const client = s3(other)
      const name = async (run: () => Promise<unknown>) => {
        try {
          await run()
          return "no error"
        } catch (err) {
          return (err as { name?: string }).name ?? String(err)
        }
      }
      // By id: the name is the other workspace's own, empty bucket.
      assert((await name(() => client.send(new aws.GetObjectCommand({ Bucket: ids.attachments, Key: "notes/a.md" })))) === "NoSuchBucket", "S3 read by bucket id")
      assert((await name(() => client.send(new aws.PutObjectCommand({ Bucket: ids.attachments, Key: "x", Body: "x" })))) === "NoSuchBucket", "S3 write by bucket id")
      assert((await name(() => client.send(new aws.DeleteObjectCommand({ Bucket: ids.attachments, Key: "notes/a.md" })))) === "NoSuchBucket", "S3 delete by bucket id")
      assert((await name(() => client.send(new aws.GetObjectCommand({ Bucket: "attachments", Key: "notes/a.md" })))) === "NoSuchKey", "S3 read of the same name in its own workspace")
      const own = await client.send(new aws.ListObjectsV2Command({ Bucket: "attachments" }))
      assert((own.KeyCount ?? 0) === 0, "the other workspace's bucket holds the agent's objects")
      // A copy can't pull an object across either.
      assert(
        (await name(() => client.send(new aws.CopyObjectCommand({ Bucket: "attachments", Key: "stolen.md", CopySource: `${ids.attachments}/notes/a.md` })))) === "NoSuchBucket",
        "S3 copy from the agent's bucket",
      )
    })

    await step("after all of it, the agent's workspace holds exactly what the agent put there", async () => {
      const usage = (await ok(engine, agent.token, "GET", "/v1/workspace")).usage
      assert(usage.databases === 2 && usage.buckets === 1, `usage: ${JSON.stringify(usage)}`)
      const memory = await ok(engine, agent.token, "POST", "/v1/databases/memory/query", { sql: "SELECT body FROM items ORDER BY id" })
      assert(memory.rows.length === 2, `memory rows: ${JSON.stringify(memory.rows)}`)
      const tasks = await ok(engine, agent.token, "POST", "/v1/databases/tasks/query", { sql: "SELECT count(*) AS n FROM items" })
      assert(JSON.stringify(tasks.rows).includes('"1"'), `tasks rows: ${JSON.stringify(tasks.rows)}`)
      const objects = await ok(engine, agent.token, "GET", "/v1/buckets/attachments/objects")
      assert(JSON.stringify(objects.data.map((o: any) => o.key)) === '["notes/a.md","notes/b.md"]', `objects: ${JSON.stringify(objects.data)}`)
    })
  } finally {
    await engine.stop()
  }
  console.log(`\nworkspace-proof: all ${count} steps passed`)
}

main().catch((err) => {
  console.error(`\nworkspace-proof: failed\n${err instanceof Error ? (err.stack ?? err.message) : String(err)}`)
  process.exit(1)
})
