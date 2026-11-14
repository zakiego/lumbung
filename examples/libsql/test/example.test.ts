// The libSQL client and Drizzle's libsql driver, unchanged, against a real
// lumbungd: plain statements, batches, interactive transactions with commit
// and rollback, and the scopes and branch they're limited to.
import { createClient, LibsqlError, type Client } from "@libsql/client"
import { eq } from "drizzle-orm"
import { drizzle } from "drizzle-orm/libsql"
import { migrate } from "drizzle-orm/libsql/migrator"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { afterAll, beforeAll, describe, expect, test } from "vitest"

import { engineBinary, startEngine, type Running } from "../../../packages/client/test/engine.ts"
import { connect, libsqlURL, orm, transfer } from "../src/app.ts"
import * as schema from "../src/schema.ts"

let engine: Running
let client: Client

async function api(method: string, path: string, body?: unknown) {
  const res = await fetch(engine.url + path, {
    method,
    headers: { authorization: `Bearer ${engine.token}`, "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`)
  return res.json() as Promise<Record<string, unknown>>
}

beforeAll(async () => {
  engine = await startEngine(engineBinary())
  await api("POST", "/v1/databases", { name: "bank" })
  await api("POST", "/v1/databases/bank/branches", { name: "dev" })
  client = connect(engine.url, engine.token, "bank", "dev")
  await client.execute("CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL UNIQUE, data BLOB)")
})

afterAll(async () => {
  client?.close()
  await engine?.stop()
})

const count = async (c: Client, table: string) => Number((await c.execute(`SELECT count(*) AS n FROM ${table}`)).rows[0]!.n)

describe("@libsql/client", () => {
  test("execute with positional and named arguments, and values round-trip", async () => {
    const ins = await client.execute({ sql: "INSERT INTO notes (body, data) VALUES (?, ?)", args: ["hello", new Uint8Array([0, 1, 2])] })
    expect(ins.rowsAffected).toBe(1)
    expect(typeof ins.lastInsertRowid).toBe("bigint")
    const rs = await client.execute({ sql: "SELECT id, body, data, 1.5 AS f, NULL AS n FROM notes WHERE body = :body", args: { body: "hello" } })
    expect(rs.columns).toEqual(["id", "body", "data", "f", "n"])
    expect(rs.columnTypes.slice(0, 2)).toEqual(["INTEGER", "TEXT"])
    const row = rs.rows[0]!
    expect(row.body).toBe("hello")
    expect(new Uint8Array(row.data as ArrayBuffer)).toEqual(new Uint8Array([0, 1, 2]))
    expect(row.f).toBe(1.5)
    expect(row.n).toBeNull()
    // Integers past 2^53 arrive intact; the client's default intMode refuses
    // them as numbers, so ask for bigints.
    const exact = createClient({ url: libsqlURL(engine.url, "bank", "dev"), authToken: engine.token, intMode: "bigint" })
    expect((await exact.execute("SELECT 9007199254740993 AS big")).rows[0]!.big).toBe(9007199254740993n)
    exact.close()
  })

  test("batch is all or nothing", async () => {
    await client.batch(
      [
        { sql: "INSERT INTO notes (body) VALUES (?)", args: ["a"] },
        { sql: "INSERT INTO notes (body) VALUES (?)", args: ["b"] },
      ],
      "write",
    )
    expect(await count(client, "notes")).toBe(3)
    await expect(
      client.batch(["INSERT INTO notes (body) VALUES ('c')", "INSERT INTO notes (body) VALUES ('a')"], "write"),
    ).rejects.toMatchObject({ code: "constraint_violation" })
    expect(await count(client, "notes")).toBe(3)
    const [sel] = await client.batch(["SELECT body FROM notes ORDER BY id"], "read")
    expect(sel!.rows.map((r) => r.body)).toEqual(["hello", "a", "b"])
  })

  test("an interactive transaction commits across requests", async () => {
    const other = connect(engine.url, engine.token, "bank", "dev")
    const tx = await client.transaction("write")
    await tx.execute({ sql: "INSERT INTO notes (body) VALUES (?)", args: ["in-tx"] })
    const seen = await tx.execute("SELECT count(*) AS n FROM notes")
    expect(Number(seen.rows[0]!.n)).toBe(4)
    // Outside, the insert isn't visible yet, and reading doesn't wait for it.
    expect(await count(other, "notes")).toBe(3)
    await tx.batch([{ sql: "INSERT INTO notes (body) VALUES (?)", args: ["in-tx-2"] }])
    await tx.commit()
    expect(tx.closed).toBe(true)
    expect(await count(other, "notes")).toBe(5)
    other.close()
  })

  test("an interactive transaction rolls back", async () => {
    const tx = await client.transaction("write")
    await tx.execute("DELETE FROM notes")
    expect(Number((await tx.execute("SELECT count(*) AS n FROM notes")).rows[0]!.n)).toBe(0)
    await tx.rollback()
    expect(await count(client, "notes")).toBe(5)

    // Closing without commit rolls back too, and frees the writer.
    const left = await client.transaction("write")
    await left.execute("DELETE FROM notes")
    left.close()
    await client.execute("INSERT INTO notes (body) VALUES ('after-close')")
    expect(await count(client, "notes")).toBe(6)
  })

  test("a failed statement inside a transaction leaves it usable, as in SQLite", async () => {
    const tx = await client.transaction("write")
    await expect(tx.execute("INSERT INTO notes (body) VALUES ('a')")).rejects.toMatchObject({ code: "constraint_violation" })
    await tx.execute("INSERT INTO notes (body) VALUES ('kept')")
    await tx.commit()
    expect((await client.execute("SELECT count(*) AS n FROM notes WHERE body = 'kept'")).rows[0]!.n).toBe(1)
  })

  test("read transactions and read tokens can't write", async () => {
    const tx = await client.transaction("read")
    expect(Number((await tx.execute("SELECT count(*) AS n FROM notes")).rows[0]!.n)).toBeGreaterThan(0)
    await expect(tx.execute("DELETE FROM notes")).rejects.toMatchObject({ code: "read_only" })
    await tx.rollback()

    const { token } = (await api("POST", "/v1/tokens", { name: "reader", database: "bank", access: "read" })) as { token: string }
    const reader = connect(engine.url, token, "bank", "dev")
    expect(await count(reader, "notes")).toBeGreaterThan(0)
    await expect(reader.execute("DELETE FROM notes")).rejects.toMatchObject({ code: "read_only" })
    const rtx = await reader.transaction("write")
    await expect(rtx.execute("DELETE FROM notes")).rejects.toMatchObject({ code: "read_only" })
    await rtx.rollback()
    reader.close()
  })

  test("a branch token reaches only its branch", async () => {
    const { token } = (await api("POST", "/v1/tokens", { name: "dev-only", database: "bank", branch: "dev", access: "write" })) as { token: string }
    const dev = connect(engine.url, token, "bank", "dev")
    expect(await count(dev, "notes")).toBeGreaterThan(0)
    const main = connect(engine.url, token, "bank", "main")
    await expect(main.execute("SELECT 1")).rejects.toMatchObject({ code: "forbidden" })
    dev.close()
    main.close()
  })

  test("executeMultiple, and the main branch is untouched", async () => {
    await client.executeMultiple("CREATE TABLE tags (name TEXT); INSERT INTO tags VALUES ('x'); INSERT INTO tags VALUES ('y');")
    expect(await count(client, "tags")).toBe(2)
    const main = connect(engine.url, engine.token, "bank", "main")
    await expect(main.execute("SELECT * FROM notes")).rejects.toBeInstanceOf(LibsqlError)
    main.close()
  })

  test("a URL without the trailing slash fails clearly", async () => {
    const bad = createClient({ url: `${engine.url}/libsql/bank/dev`, authToken: engine.token })
    await expect(bad.execute("SELECT 1")).rejects.toMatchObject({ code: "not_found" })
    bad.close()
  })
})

describe("drizzle-orm/libsql", () => {
  test("queries, transactions with commit and rollback, nested transactions and batch", async () => {
    const db = orm(client)
    await client.execute("CREATE TABLE accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, owner TEXT NOT NULL UNIQUE, balance INTEGER NOT NULL)")
    await db.insert(schema.accounts).values([
      { owner: "ana", balance: 100 },
      { owner: "budi", balance: 20 },
    ])
    const balances = async () =>
      Object.fromEntries((await db.select().from(schema.accounts).orderBy(schema.accounts.owner)).map((a) => [a.owner, a.balance]))

    await transfer(db, "ana", "budi", 30)
    expect(await balances()).toEqual({ ana: 70, budi: 50 })

    // tx.rollback() inside db.transaction throws and undoes everything.
    await expect(transfer(db, "budi", "ana", 500)).rejects.toThrow(/rollback/i)
    expect(await balances()).toEqual({ ana: 70, budi: 50 })

    // An error thrown in the callback rolls back the updates made before it.
    await expect(
      db.transaction(async (tx) => {
        await tx.update(schema.accounts).set({ balance: 0 }).where(eq(schema.accounts.owner, "ana"))
        throw new Error("boom")
      }),
    ).rejects.toThrow("boom")
    expect(await balances()).toEqual({ ana: 70, budi: 50 })

    // A nested transaction is a savepoint: its rollback keeps the outer work.
    await db.transaction(async (tx) => {
      await tx.update(schema.accounts).set({ balance: 71 }).where(eq(schema.accounts.owner, "ana"))
      await expect(
        tx.transaction(async (inner) => {
          await inner.update(schema.accounts).set({ balance: 0 }).where(eq(schema.accounts.owner, "budi"))
          inner.rollback()
        }),
      ).rejects.toThrow(/rollback/i)
    })
    expect(await balances()).toEqual({ ana: 71, budi: 50 })

    const [inserted, all] = await db.batch([
      db.insert(schema.accounts).values({ owner: "citra", balance: 5 }).returning(),
      db.select().from(schema.accounts),
    ])
    expect(inserted[0]!.owner).toBe("citra")
    expect(all).toHaveLength(3)
  })

  test("migrate() applies drizzle-kit migrations, and a table rebuild keeps child rows", async () => {
    await api("POST", "/v1/databases", { name: "library" })
    const lib = connect(engine.url, engine.token, "library", "main")
    const migrationsFolder = resolve(dirname(fileURLToPath(import.meta.url)), "../drizzle")
    await migrate(drizzle(lib), { migrationsFolder })
    // The rebuild dropped and recreated authors. With foreign keys on, that
    // drop would have cascaded to books.
    expect(await count(lib, "books")).toBe(1)
    expect((await lib.execute("SELECT bio FROM authors")).rows[0]!.bio).toBe("")
    // Running it again applies nothing.
    await migrate(drizzle(lib), { migrationsFolder })
    expect(await count(lib, "__drizzle_migrations")).toBe(2)
    // Foreign keys are back on afterwards.
    await lib.execute("DELETE FROM authors")
    expect(await count(lib, "books")).toBe(0)
    lib.close()
  })

  test("an executeMultiple table rebuild needs BEGIN and COMMIT once foreign keys are off", async () => {
    await api("POST", "/v1/databases", { name: "shelf" })
    const shelf = connect(engine.url, engine.token, "shelf", "main")
    await shelf.executeMultiple(`
      CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
      CREATE TABLE books (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL REFERENCES authors(id) ON DELETE CASCADE);
      INSERT INTO authors VALUES (1, 'Ana');
      INSERT INTO books VALUES (1, 1);
    `)
    const rebuild = `
      CREATE TABLE new_authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL, bio TEXT NOT NULL DEFAULT '');
      INSERT INTO new_authors (id, name) SELECT id, name FROM authors;
      DROP TABLE authors;
      ALTER TABLE new_authors RENAME TO authors;
    `
    // Statement by statement, the DROP would cascade: refused before anything runs.
    await expect(shelf.executeMultiple(`PRAGMA foreign_keys = OFF; ${rebuild}`)).rejects.toMatchObject({ code: "bad_request" })
    await shelf.execute("PRAGMA foreign_keys = ON")
    expect(await count(shelf, "books")).toBe(1)
    // As one transaction it works and keeps the books.
    await shelf.executeMultiple(`PRAGMA foreign_keys = OFF; BEGIN; ${rebuild} COMMIT; PRAGMA foreign_keys = ON;`)
    expect(await count(shelf, "books")).toBe(1)
    expect((await shelf.execute("SELECT bio FROM authors")).rows[0]!.bio).toBe("")
    shelf.close()
  })
})
