import { randomUUID } from "node:crypto"
import { describe, expect, inject, test } from "vitest"

import { createClient, decodeValue, encodeValue, LumbungError } from "../src/index.ts"

const url = inject("url")
const token = inject("token")
const admin = createClient({ url, token })

async function freshDatabase() {
  const name = `t-${randomUUID().slice(0, 8)}`
  await admin.databases.create(name)
  return name
}

describe("values", () => {
  test("round-trip every type exactly", async () => {
    const name = await freshDatabase()
    const db = createClient({ url, token, database: name, intMode: "bigint" })
    // Untyped columns, so the stored type is exactly what was sent.
    await db.query("CREATE TABLE v(i, f, big, nz, s, b, n)")
    const blob = new Uint8Array([0, 1, 2, 255])
    await db.query("INSERT INTO v VALUES (?, ?, ?, ?, ?, ?, ?)", [9007199254740993n, 1.5, 1e21, -0, "héllo\u0000x", blob, null])
    const r = await db.query("SELECT i, f, big, typeof(big) AS tb, typeof(nz) AS tz, s, b, n FROM v")
    expect(r.rows[0]).toEqual({ i: 9007199254740993n, f: 1.5, big: 1e21, tb: "real", tz: "real", s: "héllo\u0000x", b: blob, n: null })
  })

  test("intMode number refuses to round, string keeps digits", async () => {
    const name = await freshDatabase()
    const big = createClient({ url, token, database: name })
    await expect(big.query("SELECT 9007199254740993")).rejects.toThrow(RangeError)
    const str = createClient({ url, token, database: name, intMode: "string" })
    expect((await str.query("SELECT 9007199254740993 AS x")).rows[0]!.x).toBe("9007199254740993")
    expect((await big.query("SELECT 42 AS x")).rows[0]!.x).toBe(42)
  })

  test("encoding rules", () => {
    expect(encodeValue(true)).toEqual({ type: "integer", value: "1" })
    expect(encodeValue(1.5)).toEqual({ type: "float", value: 1.5 })
    expect(encodeValue(-Infinity)).toEqual({ type: "float", value: "-Infinity" })
    expect(encodeValue(new Date("2026-01-02T03:04:05.000Z"))).toEqual({ type: "text", value: "2026-01-02T03:04:05.000Z" })
    expect(() => encodeValue(2 ** 60)).toThrow(RangeError)
    expect(encodeValue(1e21)).toEqual({ type: "float", value: 1e21 })
    expect(encodeValue(-0)).toEqual({ type: "float", value: -0 })
    expect(() => encodeValue(NaN)).toThrow(RangeError)
    expect(() => encodeValue(2n ** 64n)).toThrow(RangeError)
    expect(decodeValue({ type: "float", value: "Infinity" }, "number")).toBe(Infinity)
  })

  test("repeated column names keep both values", async () => {
    const db = createClient({ url, token, database: await freshDatabase() })
    const r = await db.query("SELECT 1 AS a, 2 AS a")
    expect(r.columns).toEqual(["a", "a"])
    expect(r.values[0]).toEqual([1, 2])
  })
})

describe("queries", () => {
  test("named params, batch, rowsAffected and lastInsertRowid", async () => {
    const db = createClient({ url, token, database: await freshDatabase() })
    await db.query("CREATE TABLE t(id INTEGER PRIMARY KEY, name TEXT UNIQUE)")
    const ins = await db.query("INSERT INTO t (name) VALUES (:name)", { name: "a" })
    expect(ins.rowsAffected).toBe(1)
    expect(ins.lastInsertRowid).toBe(1n)
    const res = await db.batch([
      { sql: "INSERT INTO t (name) VALUES (?)", args: ["b"] },
      "SELECT count(*) AS n FROM t",
    ])
    expect(res[1]!.rows[0]!.n).toBe(2)
    // All or nothing.
    await expect(db.batch([{ sql: "INSERT INTO t (name) VALUES (?)", args: ["c"] }, { sql: "INSERT INTO t (name) VALUES (?)", args: ["a"] }])).rejects.toMatchObject({
      code: "constraint_violation",
    })
    expect((await db.query("SELECT count(*) AS n FROM t")).rows[0]!.n).toBe(2)
  })

  test("wait answers on a change and times out without one", async () => {
    const db = createClient({ url, token, database: await freshDatabase() })
    await db.query("CREATE TABLE t(x)")
    const first = await db.wait()
    expect(first.changed).toBe(false)
    expect(await db.wait(first.cursor, { timeout: 0 })).toEqual({ cursor: first.cursor, changed: false })
    const held = db.wait(first.cursor, { timeout: 30 })
    await new Promise((r) => setTimeout(r, 100))
    await db.query("INSERT INTO t VALUES (1)")
    const next = await held
    expect(next.changed).toBe(true)
    expect(next.cursor).not.toBe(first.cursor)
  })

  test("typed errors carry the API code", async () => {
    const db = createClient({ url, token, database: await freshDatabase() })
    const err = await db.query("ATTACH 'x.db' AS x").catch((e: unknown) => e)
    expect(err).toBeInstanceOf(LumbungError)
    expect(err).toMatchObject({ code: "statement_denied", status: 403 })
    expect((err as LumbungError).requestId).toMatch(/^[0-9a-f]+$/)
    const missing = createClient({ url, token, database: "does-not-exist" })
    await expect(missing.query("SELECT 1")).rejects.toMatchObject({ code: "database_not_found", status: 404 })
    const bad = createClient({ url, token: "lmb_aaaaaaaaaaaaaaaaaaaa_" + "b".repeat(40), database: "x" })
    await expect(bad.query("SELECT 1")).rejects.toMatchObject({ code: "unauthorized", status: 401 })
  })

  test("readOnly uses a read-only connection", async () => {
    const db = createClient({ url, token, database: await freshDatabase() })
    await db.query("CREATE TABLE t(x)")
    await expect(db.query("INSERT INTO t VALUES (1)", [], { readOnly: true })).rejects.toMatchObject({ code: "read_only" })
  })

  test("branch clients", async () => {
    const name = await freshDatabase()
    const db = createClient({ url, token, database: name })
    await db.query("CREATE TABLE t(x)")
    await admin.branches.create(name, "dev")
    const dev = db.branch("dev")
    await dev.query("INSERT INTO t VALUES (1)")
    expect((await dev.query("SELECT count(*) AS n FROM t")).rows[0]!.n).toBe(1)
    expect((await db.query("SELECT count(*) AS n FROM t")).rows[0]!.n).toBe(0)
  })
})

describe("management", () => {
  test("databases, branches, snapshots, tokens", async () => {
    const name = await freshDatabase()
    const db = admin.database(name)
    await db.query("CREATE TABLE t(x)")
    const page = await admin.databases.list({ limit: 1 })
    expect(page.data).toHaveLength(1)
    expect(typeof page.nextCursor).toBe("string")

    const dev = await admin.branches.create(name, "dev", { expiresInDays: 1 })
    expect(dev.expires_at).not.toBeNull()
    await db.branch("dev").query("ALTER TABLE t ADD COLUMN y")
    const diff = await admin.branches.diff(name, "dev", "main")
    expect(diff.changes.map((c) => `${c.kind}:${c.name}:${c.change}`)).toEqual(["table:t:changed"])
    expect((await admin.branches.schema(name, "dev")).tables[0]!.columns).toHaveLength(2)

    const sn = await admin.snapshots.create(name, "main", { label: "before" })
    const restored = await admin.snapshots.restore(name, sn.id, "restored")
    expect(restored.source_snapshot_id).toBe(sn.id)
    await admin.snapshots.schedule.set(name, "main", { everyMinutes: 60, keepCount: 3 })
    expect((await admin.snapshots.schedule.get(name, "main")).keep_count).toBe(3)
    await admin.snapshots.schedule.delete(name, "main")
    await admin.snapshots.delete(name, sn.id)

    const t = await admin.tokens.create({ name: "ci", database: name, access: "read" })
    const reader = createClient({ url, token: t.token, database: name })
    await reader.query("SELECT * FROM t")
    await expect(reader.query("INSERT INTO t VALUES (1)")).rejects.toMatchObject({ code: "read_only" })
    await admin.tokens.revoke(t.id)
    await expect(reader.query("SELECT 1")).rejects.toMatchObject({ code: "unauthorized" })

    const created = await admin.databases.create(`${name}-i`, { idempotencyKey: name })
    const again = await admin.databases.create(`${name}-i`, { idempotencyKey: name })
    expect(again.id).toBe(created.id)

    await admin.branches.promote(name, "restored")
    expect((await admin.databases.get(name)).default_branch).toBe("restored")
    await admin.databases.delete(name)
    expect((await admin.databases.trash()).data.some((d) => d.name === name)).toBe(true)
    await admin.databases.restore(name)
    expect((await admin.audit.list({ limit: 5 })).data.length).toBe(5)
  })

  test("import and download", async () => {
    const src = await freshDatabase()
    await admin.database(src).query("CREATE TABLE t(x)")
    await admin.database(src).query("INSERT INTO t VALUES (7)")
    const file = new Uint8Array(await (await admin.download(src, "main", "db")).arrayBuffer())
    const dump = await (await admin.download(src, "main", "sql")).text()
    const a = await freshDatabase()
    await admin.import(a, "main", file)
    const b = await freshDatabase()
    await admin.import(b, "main", dump)
    for (const d of [a, b]) {
      expect((await admin.database(d).query("SELECT x FROM t")).rows[0]!.x).toBe(7)
    }
  })
})

describe("retries", () => {
  test("safe reads retry on busy, writes don't", async () => {
    let calls = 0
    const flaky: typeof fetch = async (input, init) => {
      calls++
      if (calls === 1) return new Response(JSON.stringify({ type: "about:blank", title: "busy", status: 503, code: "busy" }), { status: 503 })
      return fetch(input, init)
    }
    const db = createClient({ url, token, fetch: flaky })
    await db.databases.list()
    expect(calls).toBe(2)
    calls = 0
    const name = await freshDatabase()
    const w = createClient({ url, token, database: name, fetch: flaky })
    await expect(w.query("CREATE TABLE t(x)")).rejects.toMatchObject({ code: "busy" })
    expect(calls).toBe(1)
  })
})
