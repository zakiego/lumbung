import type { Schema, Table } from "@lumbung/client"
import { DatabaseSync } from "node:sqlite"
import { describe, expect, test } from "vitest"
import { encodeKey, encodePreview } from "@/lib/cells"
import { browseSql, cellSql, deleteSql, gridTable, insertSql, likePattern, listObjects, paramFor, quoteIdent, updateSql } from "./grid"

function col(name: string, type = "TEXT", extra: Partial<Table["columns"][number]> = {}): Table["columns"][number] {
  return { name, type, not_null: false, pk: 0, default: null, hidden: 0, ...extra }
}

function table(name: string, columns: Table["columns"], sql = `CREATE TABLE "${name}" (...)`, virtual = false): Table {
  return { name, sql, virtual, columns }
}

const schema: Schema = {
  tables: [
    table("users", [col("id", "INTEGER", { pk: 1 }), col("email"), col("score", "REAL"), col("full", "TEXT", { hidden: 3 })]),
    table("tags", [col("name"), col("note", "")]),
    table("pairs", [col("a", "TEXT", { pk: 2 }), col("b", "INTEGER", { pk: 1 }), col("v")], 'CREATE TABLE "pairs" (a TEXT, b INTEGER, v TEXT, PRIMARY KEY (b, a)) WITHOUT ROWID'),
    table("codes", [col("code", "TEXT", { pk: 1 }), col("label")]),
    table("bigid", [col("id", "BIGINT", { pk: 1 }), col("x")]),
    table("slugs", [col("slug", "TEXT", { pk: 1, not_null: true }), col("x")]),
    table("pairs2", [col("a", "TEXT", { pk: 1, not_null: true }), col("b", "TEXT", { pk: 2 })]),
    table("shadowed", [col("rowid"), col("_rowid_"), col("oid")]),
    table("half", [col("rowid"), col("x")]),
    table('we"ird', [col('co"l')]),
    table("docs", [col("body"), col("x", "", { hidden: 1 })], "CREATE VIRTUAL TABLE docs USING fts5(body)", true),
    table("sqlite_sequence", [col("name"), col("seq")]),
  ],
  views: [{ name: "active_users", sql: "CREATE VIEW active_users AS SELECT 1" }],
  indexes: [],
  triggers: [],
}

const users = gridTable(schema, "users")!
const tags = gridTable(schema, "tags")!
const pairs = gridTable(schema, "pairs")!

test("quoteIdent doubles quotes", () => {
  expect(quoteIdent('a"b')).toBe('"a""b"')
  expect(quoteIdent("select")).toBe('"select"')
})

test("likePattern escapes wildcards and the escape character", () => {
  expect(likePattern("50%_off\\")).toBe("%50\\%\\_off\\\\%")
})

test("listObjects leaves out SQLite's tables", () => {
  expect(listObjects(schema).map((o) => o.name)).not.toContain("sqlite_sequence")
  expect(listObjects(schema).at(-1)).toEqual({ name: "active_users", kind: "view" })
})

describe("gridTable", () => {
  test("primary key columns, in key order, are the identity", () => {
    expect(users.identity).toEqual({ kind: "pk", columns: ["id"] })
    expect(pairs.identity).toEqual({ kind: "pk", columns: ["b", "a"] })
    expect(users.readOnly).toBeNull()
    expect(users.columns.find((c) => c.name === "full")?.generated).toBe(true)
  })

  test("a key that can hold NULLs on a rowid table is passed over for rowid", () => {
    expect(gridTable(schema, "codes")!.identity).toEqual({ kind: "rowid", name: "rowid" })
    expect(gridTable(schema, "bigid")!.identity).toEqual({ kind: "rowid", name: "rowid" })
    expect(gridTable(schema, "pairs2")!.identity).toEqual({ kind: "rowid", name: "rowid" })
  })

  test("a NOT NULL key on a rowid table is the identity, since VACUUM can renumber rowids", () => {
    expect(gridTable(schema, "slugs")!.identity).toEqual({ kind: "pk", columns: ["slug"] })
  })

  test("no primary key falls back to rowid, or the first alias a column doesn't take", () => {
    expect(tags.identity).toEqual({ kind: "rowid", name: "rowid" })
    expect(gridTable(schema, "half")!.identity).toEqual({ kind: "rowid", name: "_rowid_" })
  })

  test("a table whose every rowid alias is a column is read-only", () => {
    const t = gridTable(schema, "shadowed")!
    expect(t.identity).toBeNull()
    expect(t.readOnly).toMatch(/no primary key or rowid/)
  })

  test("views and virtual tables are read-only, hidden columns are left out", () => {
    expect(gridTable(schema, "active_users", [{ name: "id", type: "INTEGER" }])).toMatchObject({ kind: "view", readOnly: "Views are read-only.", identity: null })
    const docs = gridTable(schema, "docs")!
    expect(docs.readOnly).toMatch(/Virtual/)
    expect(docs.columns.map((c) => c.name)).toEqual(["body"])
  })

  test("only names in the schema are found", () => {
    expect(gridTable(schema, "nope")).toBeNull()
    expect(gridTable(schema, "sqlite_sequence")).toBeNull()
    expect(gridTable(schema, "USERS")).toBeNull()
  })
})

describe("browseSql", () => {
  test("a page orders by identity and counts the same rows", () => {
    const q = browseSql(users, { page: 3 })
    expect(q.select.sql).toMatch(/^SELECT "id", CASE typeof\("id"\)/)
    expect(q.select.sql).toMatch(/ FROM "users" ORDER BY "id" LIMIT 50 OFFSET 100$/)
    expect(q.count.sql).toBe('SELECT count(*) FROM "users"')
    expect(q.layout).toEqual({ keyCount: 1, withLength: true })
  })

  test("rowid tables select the rowid first", () => {
    const q = browseSql(tags)
    expect(q.select.sql).toMatch(/^SELECT rowid, /)
    expect(q.select.sql).toMatch(/ORDER BY rowid LIMIT 50 OFFSET 0$/)
  })

  test("filters are ANDed and bound", () => {
    const q = browseSql(users, {
      filters: [
        { column: "email", op: "contains", value: "50%" },
        { column: "score", op: ">=", value: "1.5" },
        { column: "id", op: "is not null" },
        { column: "email", op: "is null" },
        { column: "id", op: "!=", value: "3" },
      ],
    })
    const where = ` WHERE "email" LIKE ? ESCAPE '\\' AND "score" >= ? AND "id" IS NOT NULL AND "email" IS NULL AND "id" != ?`
    expect(q.count.sql).toBe(`SELECT count(*) FROM "users"${where}`)
    expect(q.select.sql).toContain(where)
    expect(q.select.args).toEqual(["%50\\%%", "1.5", "3"])
    expect(q.count.args).toEqual(q.select.args)
  })

  test("sorting keeps the identity as a tiebreak", () => {
    expect(browseSql(users, { sort: { column: "email", dir: "desc" } }).select.sql).toContain('ORDER BY "email" DESC, "id" LIMIT')
  })

  test("unknown columns are refused, never spliced in", () => {
    expect(() => browseSql(users, { sort: { column: 'id"; DROP TABLE users; --', dir: "asc" } })).toThrow(/No column/)
    expect(() => browseSql(users, { filters: [{ column: "nope", op: "=", value: "1" }] })).toThrow(/No column/)
  })

  test("odd names stay quoted", () => {
    const t = gridTable(schema, 'we"ird')!
    expect(browseSql(t).select.sql).toContain('FROM "we""ird"')
    expect(browseSql(t).select.sql).toContain('typeof("co""l")')
  })

  test("a 300-column table selects a preview and a length per column", () => {
    const wide = gridTable({ ...schema, tables: [table("wide", Array.from({ length: 300 }, (_, i) => col(`c${i}`)))] }, "wide")!
    const q = browseSql(wide)
    expect(q.select.sql.match(/CASE typeof/g)).toHaveLength(600)
  })

  test("comparison filters bind by the column's affinity", () => {
    const q = browseSql(tags, { filters: [{ column: "note", op: ">", value: "10" }, { column: "name", op: "=", value: "10" }] })
    expect(q.select.args).toEqual([10n, "10"])
    const v = gridTable(schema, "active_users", [{ name: "n", type: "" }])!
    expect(browseSql(v, { filters: [{ column: "n", op: "<=", value: "2.5" }] }).select.args).toEqual([2.5])
  })

  test("past 900 columns the length column is dropped to stay under SQLite's limit", () => {
    const wide = gridTable({ ...schema, tables: [table("wide", Array.from({ length: 1500 }, (_, i) => col(`c${i}`)))] }, "wide")!
    const q = browseSql(wide)
    expect(q.layout.withLength).toBe(false)
    expect(q.select.sql.match(/CASE typeof/g)).toHaveLength(1500)
  })

  test("a view has no identity and no order", () => {
    const v = gridTable(schema, "active_users", [{ name: "id", type: "INTEGER" }])!
    expect(browseSql(v).select.sql).toMatch(/FROM "active_users" LIMIT 50 OFFSET 0$/)
  })
})

describe("writes", () => {
  const key = [encodeKey(7n)]

  test("update sets one column by identity", () => {
    expect(updateSql(users, key, "email", "a@b.c")).toEqual({ sql: 'UPDATE "users" SET "email" = ? WHERE "id" = ?', args: ["a@b.c", 7n] })
    expect(updateSql(users, key, "email", null).args[0]).toBeNull()
    expect(updateSql(tags, [encodeKey(1n)], "name", "x").sql).toBe('UPDATE "tags" SET "name" = ? WHERE rowid = ?')
    expect(updateSql(pairs, [encodeKey(1n), encodeKey("a")], "v", "x").sql).toBe('UPDATE "pairs" SET "v" = ? WHERE "b" = ? AND "a" = ?')
  })

  test("refuses read-only tables, generated columns and keys of the wrong shape", () => {
    expect(() => updateSql(users, key, "full", "x")).toThrow(/generated/)
    expect(() => updateSql(users, [], "email", "x")).toThrow(/key/)
    expect(() => updateSql(gridTable(schema, "shadowed")!, key, "oid", "x")).toThrow(/no primary key/)
    expect(() => deleteSql(gridTable(schema, "active_users", [])!, key)).toThrow(/Views/)
  })

  test("insert names only the columns given", () => {
    expect(insertSql(users, { email: "a", score: null })).toEqual({ sql: 'INSERT INTO "users" ("email", "score") VALUES (?, ?)', args: ["a", null] })
    expect(insertSql(users, {}).sql).toBe('INSERT INTO "users" DEFAULT VALUES')
    expect(() => insertSql(users, { nope: "1" })).toThrow(/No column/)
  })

  test("delete by identity", () => {
    expect(deleteSql(users, key)).toEqual({ sql: 'DELETE FROM "users" WHERE "id" = ?', args: [7n] })
  })

  test("a cell read is capped at 1 MB", () => {
    const q = cellSql(users, key, "email")
    expect(q.sql).toContain('length(CAST("email" AS BLOB)) > 1048576 THEN NULL')
    expect(q.args).toEqual([7n])
  })
})

test("paramFor sends text to declared columns and numbers to typeless ones", () => {
  const typed = users.columns.find((c) => c.name === "score")!
  const typeless = tags.columns.find((c) => c.name === "note")!
  expect(paramFor(typed, "12")).toBe("12")
  expect(paramFor(typeless, "12")).toBe(12n)
  expect(paramFor(typeless, "-1.5e3")).toBe(-1500)
  expect(paramFor(typeless, "99999999999999999999")).toBe("99999999999999999999")
  expect(paramFor(typeless, "007")).toBe("007")
  expect(paramFor(typeless, "+7")).toBe("+7")
  expect(paramFor(typeless, "-0")).toBe("-0")
  expect(paramFor(typeless, "-7")).toBe(-7n)
  expect(paramFor(typeless, "0")).toBe(0n)
  expect(paramFor(typeless, "12 apples")).toBe("12 apples")
  expect(paramFor(typeless, null)).toBeNull()
})

describe("against SQLite", () => {
  const run = (db: DatabaseSync, q: { sql: string; args: unknown[] }) => db.prepare(q.sql).run(...(q.args as never[]))
  const all = (db: DatabaseSync, q: { sql: string; args: unknown[] }) => {
    const st = db.prepare(q.sql)
    st.setReadBigInts(true)
    st.setReturnArrays(true)
    return st.all(...(q.args as never[])) as unknown as unknown[][]
  }

  test("a 950-column table still marks cut text and blobs as cut", () => {
    const db = new DatabaseSync(":memory:")
    const names = Array.from({ length: 950 }, (_, i) => `c${i}`)
    db.exec(`CREATE TABLE wide (${names.map((n) => `${n} TEXT`).join(", ")})`)
    db.prepare("INSERT INTO wide (c0, c1, c2) VALUES (?, ?, ?)").run("x".repeat(5000), "short", new Uint8Array(100))
    const t = gridTable({ ...schema, tables: [table("wide", names.map((n) => col(n)))] }, "wide")!
    const q = browseSql(t)
    expect(q.layout.withLength).toBe(false)
    const [row] = all(db, q.select)
    const cell = (i: number) => encodePreview((row![q.layout.keyCount + i] ?? null) as never, "unknown")
    const long = cell(0)
    expect(long).toMatchObject({ t: "text", cut: true, len: null })
    expect(long.t === "text" && long.v.length).toBe(1000)
    expect(cell(1)).toEqual({ t: "text", v: "short", len: 5, cut: false })
    expect(cell(2)).toMatchObject({ t: "blob", size: null })
    expect(cell(3)).toEqual({ t: "null" })
  })

  test("editing a row with a NULL text key touches only that row", () => {
    const db = new DatabaseSync(":memory:")
    db.exec("CREATE TABLE codes (code TEXT PRIMARY KEY, label TEXT)")
    db.exec("INSERT INTO codes VALUES (NULL, 'a'), (NULL, 'b'), ('x', 'c')")
    const t = gridTable(schema, "codes")!
    const rows = all(db, browseSql(t).select)
    const key = [encodeKey(rows[1]![0] as bigint)]
    expect(run(db, updateSql(t, key, "label", "B")).changes).toBe(1)
    expect(run(db, deleteSql(t, [encodeKey(rows[0]![0] as bigint)])).changes).toBe(1)
    expect(db.prepare("SELECT code, label FROM codes ORDER BY rowid").all()).toEqual([
      { code: null, label: "B" },
      { code: "x", label: "c" },
    ])
  })

  test("a NULL key on a table with every rowid name shadowed matches nothing", () => {
    const db = new DatabaseSync(":memory:")
    db.exec("CREATE TABLE s (rowid TEXT, _rowid_ TEXT, oid TEXT, k TEXT PRIMARY KEY, v TEXT)")
    db.exec("INSERT INTO s (k, v) VALUES (NULL, 'a'), (NULL, 'b')")
    const t = gridTable({ ...schema, tables: [table("s", [col("rowid"), col("_rowid_"), col("oid"), col("k", "TEXT", { pk: 1 }), col("v")])] }, "s")!
    expect(t.identity).toEqual({ kind: "pk", columns: ["k"] })
    expect(run(db, updateSql(t, [{ t: "null" }], "v", "z")).changes).toBe(0)
  })

  test("a number filter matches a typeless column", () => {
    const db = new DatabaseSync(":memory:")
    db.exec("CREATE TABLE tags (name TEXT, note)")
    db.exec("INSERT INTO tags VALUES ('a', 5), ('b', 20)")
    const q = browseSql(tags, { filters: [{ column: "note", op: ">", value: "10" }] })
    expect(all(db, q.select).map((r) => r[1])).toEqual(["b"])
  })
})
