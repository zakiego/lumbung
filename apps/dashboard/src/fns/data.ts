// The Data tab: browse one table, edit cells, insert and delete rows.
import type { Client, Schema } from "@lumbung/client"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { type Cell, type RowKey, encodeKey, encodePreview } from "@/lib/cells"
import { can } from "@/lib/roles"
import { call, engineFor } from "@/server/engine"
import {
  CELL_EDIT_MAX,
  FILTER_OPS,
  FILTER_VALUE_MAX,
  type GridTable,
  PAGE_SIZE,
  browseSql,
  cellSql,
  deleteSql,
  gridTable,
  insertSql,
  listObjects,
  updateSql,
  viewColumnsSql,
} from "@/server/grid"
import { type Viewer, requireCan } from "@/server/session"

const ref = z.string().min(1).max(100)
const objectName = z.string().min(1).max(1000)
const columnName = z.string().max(1000)
const target = { db: ref, branch: ref, table: objectName }

const keyValue = z.discriminatedUnion("t", [
  z.object({ t: z.literal("null") }),
  z.object({ t: z.literal("int"), v: z.string().regex(/^-?\d{1,19}$/) }),
  z.object({ t: z.literal("real"), v: z.string().max(40) }),
  z.object({ t: z.literal("text"), v: z.string().max(CELL_EDIT_MAX) }),
  z.object({ t: z.literal("blob"), b64: z.string().max(CELL_EDIT_MAX * 2) }),
])
const rowKey = z.array(keyValue).min(1).max(64)

export interface GridObject {
  name: string
  kind: "table" | "view"
}

export interface GridRow {
  key: RowKey | null
  cells: Cell[]
}

export interface BrowseResult {
  objects: GridObject[]
  /** Null when the database has no tables, or the named one doesn't exist. */
  table: (Omit<GridTable, "identity"> & { editable: boolean }) | null
  missing: boolean
  rows: GridRow[]
  total: number
  page: number
  pageSize: number
  error: string | null
}

function branchClient(viewer: Viewer, db: string, branch: string): Client {
  return engineFor(viewer).database(db, branch)
}

async function loadTable(client: Client, schema: Schema, name: string): Promise<GridTable | null> {
  if (!schema.views.some((v) => v.name === name)) return gridTable(schema, name)
  const r = await client.query(viewColumnsSql(name), undefined, { readOnly: true })
  return gridTable(
    schema,
    name,
    r.columns.map((c, i) => ({ name: c, type: r.columnTypes[i] ?? "" })),
  )
}

/** The table named in the request, checked against the branch's schema. */
async function tableFor(viewer: Viewer, client: Client, d: { db: string; branch: string; table: string }): Promise<GridTable> {
  const schema = await engineFor(viewer).branches.schema(d.db, d.branch)
  const t = await loadTable(client, schema, d.table)
  if (!t) throw new Error(`No table or view named ${d.table} on ${d.branch}.`)
  return t
}

const filter = z.object({ column: columnName, op: z.enum(FILTER_OPS), value: z.string().max(FILTER_VALUE_MAX).optional() })

// POST, though it only reads: filters and row keys can be too long for a URL.
export const browseTable = createServerFn({ method: "POST" })
  .validator(
    z.object({
      db: ref,
      branch: ref,
      table: objectName.optional(),
      page: z.number().int().min(1).max(10_000_000).default(1),
      sort: z.object({ column: columnName, dir: z.enum(["asc", "desc"]) }).nullish(),
      filters: z.array(filter).max(20).default([]),
    }),
  )
  .handler(async ({ data }): Promise<BrowseResult> => {
    const viewer = await requireCan("read")
    const canWrite = can(viewer.role, "write")
    const client = branchClient(viewer, data.db, data.branch)
    const schema = await call(() => engineFor(viewer).branches.schema(data.db, data.branch))
    const objects = listObjects(schema)
    const empty = { objects, rows: [], total: 0, page: data.page, pageSize: PAGE_SIZE, error: null }
    const name = data.table ?? objects[0]?.name
    if (!name) return { ...empty, table: null, missing: false }
    const t = await call(() => loadTable(client, schema, name))
    if (!t) return { ...empty, table: null, missing: true }
    const { identity, ...rest } = t
    const table = { ...rest, editable: canWrite && !t.readOnly }
    try {
      const read = (n: number) => {
        const q = browseSql(t, { filters: data.filters, sort: data.sort ?? null, page: n })
        return call(() => client.batch([q.select, q.count], { readOnly: true })).then(([rows, count]) => ({ q, rows: rows!, total: Number(count!.values[0]?.[0] ?? 0) }))
      }
      let at = data.page
      let r = await read(at)
      // A page past the end, from an old link or after deletes: show the last one.
      if (!r.rows.values.length && r.total > 0 && at > 1) {
        at = Math.ceil(r.total / PAGE_SIZE)
        r = await read(at)
      }
      const { q, rows: page, total } = r
      const { keyCount, withLength } = q.layout
      const rows = page.values.map((v) => ({
        key: identity ? v.slice(0, keyCount).map(encodeKey) : null,
        cells: t.columns.map((_, i) => {
          const pos = keyCount + (withLength ? i * 2 : i)
          return encodePreview(v[pos] ?? null, withLength ? (v[pos + 1] ?? null) : "unknown")
        }),
      }))
      return { ...empty, table, missing: false, rows, total, page: at }
    } catch (e) {
      return { ...empty, table, missing: false, error: e instanceof Error ? e.message : String(e) }
    }
  })

export const updateCell = createServerFn({ method: "POST" })
  .validator(z.object({ ...target, key: rowKey, column: columnName, value: z.string().max(CELL_EDIT_MAX).nullable() }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("write")
    const client = branchClient(viewer, data.db, data.branch)
    const t = await call(() => tableFor(viewer, client, data))
    const q = updateSql(t, data.key, data.column, data.value)
    const r = await call(() => client.query(q.sql, q.args))
    if (r.rowsAffected !== 1) throw new Error("That row changed or is gone. Reload and try again.")
    return { ok: true }
  })

export const insertRow = createServerFn({ method: "POST" })
  .validator(z.object({ ...target, values: z.record(columnName, z.string().max(CELL_EDIT_MAX).nullable()) }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("write")
    const client = branchClient(viewer, data.db, data.branch)
    const t = await call(() => tableFor(viewer, client, data))
    const q = insertSql(t, data.values)
    await call(() => client.query(q.sql, q.args))
    return { ok: true }
  })

export const deleteRows = createServerFn({ method: "POST" })
  .validator(z.object({ ...target, keys: z.array(rowKey).min(1).max(500) }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("write")
    const client = branchClient(viewer, data.db, data.branch)
    const t = await call(() => tableFor(viewer, client, data))
    const results = await call(() => client.batch(data.keys.map((k) => deleteSql(t, k))))
    return { deleted: results.reduce((n, r) => n + r.rowsAffected, 0) }
  })

export type FullCell = { t: "null" } | { t: "int" | "real" | "text"; v: string }

export const getCell = createServerFn({ method: "POST" })
  .validator(z.object({ ...target, key: rowKey, column: columnName }))
  .handler(async ({ data }): Promise<FullCell> => {
    const viewer = await requireCan("read")
    const client = branchClient(viewer, data.db, data.branch)
    const t = await call(() => tableFor(viewer, client, data))
    const q = cellSql(t, data.key, data.column)
    const r = await call(() => client.query(q.sql, q.args, { readOnly: true }))
    const row = r.values[0]
    if (!row) throw new Error("That row changed or is gone. Reload and try again.")
    const [value, type, bytes] = row
    if (type === "blob") throw new Error("Blobs can't be edited here.")
    if (Number(bytes ?? 0) > CELL_EDIT_MAX) throw new Error("This value is over 1 MB. Change it with SQL.")
    if (value === null) return { t: "null" }
    if (typeof value === "bigint") return { t: "int", v: value.toString() }
    if (typeof value === "number") return { t: "real", v: String(value) }
    return { t: "text", v: String(value) }
  })
