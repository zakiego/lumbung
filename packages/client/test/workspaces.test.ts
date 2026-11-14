import { randomUUID } from "node:crypto"
import { describe, expect, inject, test } from "vitest"

import { createClient, LumbungError } from "../src/index.ts"

const url = inject("url")
const token = inject("token")
// The engine's service token: an instance admin, which belongs to no workspace.
const admin = createClient({ url, token })

const unique = (prefix: string) => `${prefix}-${randomUUID().slice(0, 8)}`

async function code(p: Promise<unknown>): Promise<string> {
  try {
    await p
  } catch (err) {
    if (err instanceof LumbungError) return err.code
    throw err
  }
  return "no error"
}

/** A new workspace and a client holding an admin token for it. */
async function workspaceWithToken(prefix: string) {
  const name = unique(prefix)
  const ws = await admin.workspaces.create(name)
  const made = await admin.workspace(name).tokens.create({ name: "agent", access: "admin", accessKey: true })
  return { name, id: ws.id, client: createClient({ url, token: made.token }), made }
}

describe("workspaces", () => {
  test("create, read, change, delete and restore one", async () => {
    const name = unique("ws")
    const ws = await admin.workspaces.create(name, { maxDatabases: 3, maxObjectBytes: 1000 })
    expect(ws.name).toBe(name)
    expect(ws.default).toBe(false)
    expect(ws.limits).toMatchObject({ max_databases: 3, max_object_bytes: 1000 })
    expect(ws.usage).toMatchObject({ databases: 0, buckets: 0, tokens: 0 })

    expect((await admin.workspaces.get(name)).id).toBe(ws.id)
    expect((await admin.workspaces.get(ws.id)).name).toBe(name)
    expect((await admin.workspaces.list({ limit: 200 })).data.map((w) => w.id)).toContain(ws.id)
    expect(await code(admin.workspaces.create(name))).toBe("name_taken")

    const renamed = unique("renamed")
    const changed = await admin.workspaces.update(name, { name: renamed, maxDatabases: 0 })
    expect(changed.name).toBe(renamed)
    expect(changed.limits).toMatchObject({ max_databases: 0, max_object_bytes: 1000 })

    const gone = await admin.workspaces.delete(renamed)
    expect(gone.deleted_at).not.toBeNull()
    expect(await code(admin.workspaces.get(renamed))).toBe("workspace_not_found")
    expect((await admin.workspaces.trash({ limit: 200 })).data.map((w) => w.id)).toContain(ws.id)

    const back = await admin.workspaces.restore(ws.id)
    expect(back.deleted_at).toBeNull()
    expect((await admin.workspaces.get(renamed)).id).toBe(ws.id)
  })

  test("the default workspace is where an instance token acts", async () => {
    const current = await admin.workspaces.current()
    expect(current.default).toBe(true)
    expect(await code(admin.workspaces.delete(current.id))).toBe("forbidden")
  })

  test("a workspace token makes and uses its own databases and buckets", async () => {
    const { client, id, name } = await workspaceWithToken("agent")
    expect((await client.workspaces.current()).id).toBe(id)
    // Its list of workspaces is a list of one.
    expect((await client.workspaces.list()).data.map((w) => w.name)).toEqual([name])

    const created = await client.databases.create("notes")
    expect(created.workspace_id).toBe(id)
    const db = client.database("notes")
    await db.query("CREATE TABLE t(v)")
    await db.query("INSERT INTO t VALUES (?)", ["kept"])
    expect((await db.query("SELECT v FROM t")).rows).toEqual([{ v: "kept" }])
    expect((await client.databases.list()).data.map((d) => d.name)).toEqual(["notes"])

    await client.buckets.create("files")
    const files = client.bucket("files")
    await files.put("a.txt", "hello")
    expect(await (await files.get("a.txt")).text()).toBe("hello")

    // A token it makes without a target is a workspace token, never an instance one.
    const child = await client.tokens.create({ name: "reader", access: "read" })
    expect(child.scope).toBe("workspace")
    expect(child.workspace_id).toBe(id)
    const reader = createClient({ url, token: child.token, database: "notes" })
    expect((await reader.query("SELECT count(*) AS n FROM t")).rows[0]!.n).toBe(1)
    expect(await code(reader.query("INSERT INTO t VALUES ('x')"))).toBe("read_only")

    // What belongs to the instance is closed to it.
    expect(await code(client.workspaces.create(unique("mine")))).toBe("forbidden")
    expect(await code(client.workspaces.update(name, { maxDatabases: 0 }))).toBe("forbidden")
    expect(await code(client.instance.tokens.list())).toBe("forbidden")
    expect(await code(client.instance.audit.list())).toBe("forbidden")
    expect(await code(client.databases.move("notes", "default"))).toBe("forbidden")
  })

  test("two workspaces hold the same names and can't see each other", async () => {
    const a = await workspaceWithToken("a")
    const b = await workspaceWithToken("b")
    for (const [ws, secret] of [[a, "of a"], [b, "of b"]] as const) {
      await ws.client.databases.create("notes")
      await ws.client.database("notes").batch(["CREATE TABLE t(v)", { sql: "INSERT INTO t VALUES (?)", args: [secret] }])
      await ws.client.buckets.create("files")
      await ws.client.bucket("files").put("doc.txt", secret)
    }
    expect((await a.client.database("notes").query("SELECT v FROM t")).rows).toEqual([{ v: "of a" }])
    expect(await (await b.client.bucket("files").get("doc.txt")).text()).toBe("of b")

    // The other workspace's things by id, and the other workspace by name or id.
    const bNotes = (await b.client.databases.get("notes")).id
    const bFiles = (await b.client.buckets.get("files")).id
    expect(await code(a.client.databases.get(bNotes))).toBe("database_not_found")
    expect(await code(a.client.database(bNotes).query("SELECT 1"))).toBe("database_not_found")
    expect(await code(a.client.buckets.get(bFiles))).toBe("bucket_not_found")
    expect(await code(a.client.bucket(bFiles).get("doc.txt"))).toBe("bucket_not_found")
    expect(await code(a.client.workspaces.get(b.name))).toBe("workspace_not_found")
    for (const other of [b.name, b.id, unique("nope")]) {
      const reach = createClient({ url, token: a.made.token, workspace: other })
      expect(await code(reach.databases.list())).toBe("workspace_not_found")
      expect(await code(reach.database("notes").query("SELECT v FROM t"))).toBe("workspace_not_found")
      expect(await code(reach.bucket("files").get("doc.txt"))).toBe("workspace_not_found")
    }
    // Its own workspace named in full is the same as not naming it.
    const own = createClient({ url, token: a.made.token, workspace: a.name })
    expect((await own.database("notes").query("SELECT v FROM t")).rows).toEqual([{ v: "of a" }])
  })

  test("an instance token works inside a workspace by naming it", async () => {
    const { name, id } = await workspaceWithToken("named")
    const inside = admin.workspace(name)
    expect((await inside.workspaces.current()).id).toBe(id)
    await inside.databases.create("app")
    const db = createClient({ url, token, workspace: name, database: "app" })
    await db.query("CREATE TABLE t(v)")
    expect((await inside.databases.list()).data.map((d) => d.name)).toEqual(["app"])
    // The default workspace, where the token acts without a workspace, has no such database.
    expect(await code(admin.databases.get("app"))).toBe("database_not_found")

    // Objects, and a presigned URL made there, which names the workspace.
    await inside.buckets.create("files")
    await inside.bucket("files").put("a.txt", "inside")
    expect(await (await inside.bucket("files").get("a.txt")).text()).toBe("inside")
    const key = await inside.tokens.create({ name: "s3", access: "read", accessKey: true })
    const signed = await createClient({ url, token: key.token }).bucket("files").presign("a.txt")
    expect(await (await fetch(signed.url)).text()).toBe("inside")

    // Download and import go through the same prefix.
    const file = await inside.download("app", "main")
    expect((await file.arrayBuffer()).byteLength).toBeGreaterThan(0)
  })

  test("moving a database and a bucket to another workspace", async () => {
    const from = await workspaceWithToken("from")
    const to = await workspaceWithToken("to")
    await from.client.databases.create("moving")
    await from.client.database("moving").query("CREATE TABLE t(v)")
    await from.client.buckets.create("moving-files")
    const scoped = await from.client.tokens.create({ name: "scoped", access: "write", database: "moving" })

    const moved = await admin.workspace(from.name).databases.move("moving", to.name)
    expect(moved.workspace_id).toBe(to.id)
    const bucket = await admin.workspace(from.name).buckets.move("moving-files", to.id)
    expect(bucket.workspace_id).toBe(to.id)

    expect(await code(from.client.databases.get("moving"))).toBe("database_not_found")
    expect((await to.client.databases.get("moving")).id).toBe(moved.id)
    expect((await to.client.buckets.get("moving-files")).id).toBe(bucket.id)
    // The token that reached the database is revoked.
    expect(await code(createClient({ url, token: scoped.token, database: "moving" }).query("SELECT 1"))).toBe("unauthorized")
  })

  test("instance tokens and the instance audit log", async () => {
    const { name, id, client } = await workspaceWithToken("audited")
    await client.databases.create("logged")

    const made = await admin.instance.tokens.create({ name: unique("operator"), access: "read", expiresInDays: 1 })
    expect(made.scope).toBe("instance")
    expect(made.workspace_id).toBe("")
    expect((await admin.instance.tokens.list({ limit: 200 })).data.map((t) => t.id)).toContain(made.id)
    // A workspace's token list holds no instance tokens.
    expect((await admin.tokens.list({ limit: 200 })).data.some((t) => t.scope === "instance")).toBe(false)
    // An instance token below admin reads every workspace and manages none.
    const operator = createClient({ url, token: made.token })
    expect((await operator.workspace(name).databases.list()).data.map((d) => d.name)).toEqual(["logged"])
    expect(await code(operator.workspaces.create(unique("x")))).toBe("forbidden")
    await admin.instance.tokens.revoke(made.id)
    expect(await code(operator.workspaces.list())).toBe("unauthorized")

    const own = await client.audit.list({ limit: 50 })
    expect(own.data.every((e) => e.workspace_id === id)).toBe(true)
    expect(own.data.map((e) => e.action)).toContain("database.create")
    const filtered = await admin.instance.audit.list({ workspace: name, limit: 50 })
    expect(filtered.data.map((e) => e.id)).toEqual(own.data.map((e) => e.id))
    const all = await admin.instance.audit.list({ limit: 200 })
    expect(all.data.some((e) => e.action === "workspace.create" && e.workspace_id === "")).toBe(true)
  })
})

describe("the workspace option", () => {
  // No engine: only where each request goes.
  const calls: string[] = []
  const recorder: typeof fetch = async (input) => {
    calls.push(new URL(String(input instanceof Request ? input.url : input)).pathname)
    return new Response(JSON.stringify({ data: [], next_cursor: "", columns: [], column_types: [], rows: [], rows_affected: 0, last_insert_rowid: "0", prefixes: [] }), {
      headers: { "content-type": "application/json" },
    })
  }
  const paths = async (run: () => Promise<unknown>) => {
    calls.length = 0
    await run().catch(() => {})
    return calls
  }

  test("names the workspace on what acts inside one, and on nothing else", async () => {
    const c = createClient({ url: "http://engine.test", token: "lmb_x", workspace: "acme", database: "notes", fetch: recorder })
    expect(await paths(() => c.query("SELECT 1"))).toEqual(["/v1/workspaces/acme/databases/notes/query"])
    expect(await paths(() => c.branch("dev").batch(["SELECT 1"]))).toEqual(["/v1/workspaces/acme/databases/notes/branches/dev/batch"])
    expect(await paths(() => c.databases.list())).toEqual(["/v1/workspaces/acme/databases"])
    expect(await paths(() => c.databases.trash())).toEqual(["/v1/workspaces/acme/trash"])
    expect(await paths(() => c.buckets.list())).toEqual(["/v1/workspaces/acme/buckets"])
    expect(await paths(() => c.tokens.list())).toEqual(["/v1/workspaces/acme/tokens"])
    expect(await paths(() => c.audit.list())).toEqual(["/v1/workspaces/acme/audit"])
    expect(await paths(() => c.workspaces.current())).toEqual(["/v1/workspaces/acme/workspace"])
    expect(await paths(() => c.bucket("files").list())).toEqual(["/v1/workspaces/acme/buckets/files/objects"])
    expect(await paths(() => c.bucket("files").get("a b.txt"))).toEqual(["/v1/workspaces/acme/buckets/files/object"])
    expect(await paths(() => c.bucket("files").put("k", "v"))).toEqual(["/v1/workspaces/acme/buckets/files/object"])
    expect(await paths(() => c.download("notes", "main"))).toEqual(["/v1/workspaces/acme/databases/notes/branches/main/export"])
    expect(await paths(() => c.databases.move("notes", "other"))).toEqual(["/v1/workspaces/acme/databases/notes/move"])
    // The instance's own endpoints never take the prefix.
    expect(await paths(() => c.workspaces.list())).toEqual(["/v1/workspaces"])
    expect(await paths(() => c.workspaces.get("other"))).toEqual(["/v1/workspaces/other"])
    expect(await paths(() => c.workspaces.delete("other"))).toEqual(["/v1/workspaces/other"])
    expect(await paths(() => c.workspaces.restore("other"))).toEqual(["/v1/workspaces/other/restore"])
    expect(await paths(() => c.instance.tokens.list())).toEqual(["/v1/instance/tokens"])
    expect(await paths(() => c.instance.audit.list())).toEqual(["/v1/instance/audit"])
  })

  test("is left out without the option, and escaped with it", async () => {
    const plain = createClient({ url: "http://engine.test/", token: "lmb_x", database: "notes", fetch: recorder })
    expect(await paths(() => plain.query("SELECT 1"))).toEqual(["/v1/databases/notes/query"])
    expect(await paths(() => plain.workspaces.current())).toEqual(["/v1/workspace"])
    expect(await paths(() => plain.workspace("acme").databases.list())).toEqual(["/v1/workspaces/acme/databases"])
  })

  // ".." is resolved away when a URL is parsed, and an empty workspace
  // reads as "none": both would act in the default workspace.
  test("refuses a workspace that isn't a name or an id, before any request", async () => {
    const plain = createClient({ url: "http://engine.test", token: "lmb_x", fetch: recorder })
    calls.length = 0
    for (const bad of ["", "..", ".", "a/../b", "../default", "Acme", " ", "%2e%2e", "ws_short", "a".repeat(64), null, 7]) {
      expect(() => createClient({ url: "http://engine.test", token: "lmb_x", workspace: bad as string, fetch: recorder }), JSON.stringify(bad)).toThrow(TypeError)
      expect(() => plain.workspace(bad as string), JSON.stringify(bad)).toThrow(TypeError)
    }
    expect(calls).toEqual([])
    for (const good of ["default", "a", "agent-1", "ws_174a999a2be2baa5443f"]) {
      expect(await paths(() => plain.workspace(good).databases.list())).toEqual([`/v1/workspaces/${good}/databases`])
    }
    expect(await paths(() => createClient({ url: "http://engine.test", token: "lmb_x", workspace: undefined, fetch: recorder }).databases.list())).toEqual(["/v1/databases"])
  })

  test("refuses a name that would leave its place in the path", async () => {
    const c = createClient({ url: "http://engine.test", token: "lmb_x", workspace: "acme", fetch: recorder })
    const tries: (() => Promise<unknown>)[] = [
      () => c.databases.restore(".."),
      () => c.databases.delete(".."),
      () => c.databases.get("."),
      () => c.databases.rename("..", "x"),
      () => c.databases.move("..", "other"),
      () => c.branches.list(".."),
      () => c.branches.delete("notes", ".."),
      () => c.snapshots.restore("..", "..", "x"),
      () => c.snapshots.delete("notes", ""),
      () => c.tokens.revoke(".."),
      () => c.instance.tokens.revoke(""),
      () => c.buckets.delete(".."),
      () => c.buckets.move("..", "other"),
      () => c.bucket("..").list(),
      () => c.bucket("..").put("k", "v"),
      () => c.workspaces.get(".."),
      () => c.workspaces.delete(""),
      () => c.workspaces.restore(".."),
      () => c.database("..").query("SELECT 1"),
      () => c.database("notes", "..").query("SELECT 1"),
      () => c.download("..", "main"),
    ]
    calls.length = 0
    for (const run of tries) {
      let thrown: unknown
      try {
        await run()
      } catch (err) {
        thrown = err
      }
      expect(thrown, run.toString()).toBeInstanceOf(TypeError)
    }
    expect(calls).toEqual([])
    // A slash or a percent sign is still just part of one segment.
    expect(await paths(() => c.databases.get("a/../b"))).toEqual(["/v1/workspaces/acme/databases/a%2F..%2Fb"])
  })
})

describe("names that would leave their place, against a real engine", () => {
  test("a database called .. never reaches the workspace's own endpoints", async () => {
    const { name, client } = await workspaceWithToken("dots")
    await admin.workspaces.delete(name)
    // In the trash. Restoring "the database .." must not restore the workspace.
    await expect(admin.workspace(name).databases.restore("..")).rejects.toBeInstanceOf(TypeError)
    expect((await admin.workspaces.trash({ limit: 200 })).data.map((w) => w.name)).toContain(name)
    expect(await code(client.databases.list())).toBe("unauthorized")
  })
})
