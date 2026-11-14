// SQL for browsing and editing one table. Pure: no engine calls, so every
// statement it builds is unit tested. Names are always quoted and values are
// always bound, never spliced into the text.
import type { InValue, Schema } from "@lumbung/client"
import { BLOB_PREVIEW, type RowKey, TEXT_PREVIEW, decodeKey } from "@/lib/cells"

export const PAGE_SIZE = 50
/** getCell refuses values bigger than this, in bytes. */
export const CELL_EDIT_MAX = 1024 * 1024
/** Above this many columns a browse skips the length column, to stay under SQLite's result column limit. */
const MAX_PREVIEW_COLUMNS = 900

/** Filters live in the URL, so their values stay short. */
export const FILTER_VALUE_MAX = 1000

export const FILTER_OPS = ["=", "!=", "<", "<=", ">", ">=", "contains", "is null", "is not null"] as const
export type FilterOp = (typeof FILTER_OPS)[number]

export interface GridFilter {
  column: string
  op: FilterOp
  value?: string
}

export interface GridColumn {
  name: string
  type: string
  notNull: boolean
  /** Position in the primary key, 0 when not part of it. */
  pk: number
  default: string | null
  /** Generated columns can't be written. */
  generated: boolean
}

export type Identity = { kind: "pk"; columns: string[] } | { kind: "rowid"; name: string }

export interface GridTable {
  name: string
  kind: "table" | "view"
  columns: GridColumn[]
  /** How a row is found again for an edit. Null for views and tables with no usable rowid. */
  identity: Identity | null
  /** Why rows can't be changed, or null when they can. */
  readOnly: string | null
}

export interface Stmt {
  sql: string
  args: InValue[]
}

export function quoteIdent(name: string): string {
  return `"${name.replaceAll('"', '""')}"`
}

/** Escapes LIKE wildcards so `contains` matches the text literally. The ESCAPE character is a backslash. */
export function likePattern(text: string): string {
  return `%${text.replace(/[\\%_]/g, (m) => `\\${m}`)}%`
}

const ROWID_NAMES = ["rowid", "_rowid_", "oid"]

/** Tables and views that may be browsed: the schema's own, without SQLite's internal ones. */
export function listObjects(schema: Schema): { name: string; kind: "table" | "view" }[] {
  return [
    ...schema.tables.filter((t) => !t.name.startsWith("sqlite_")).map((t) => ({ name: t.name, kind: "table" as const })),
    ...schema.views.map((v) => ({ name: v.name, kind: "view" as const })),
  ]
}

/**
 * The table or view named `name`, as the grid sees it, or null when the
 * schema has no such object. A view's columns aren't in the schema, so the
 * caller passes the names a query reported.
 */
export function gridTable(schema: Schema, name: string, viewColumns?: { name: string; type: string }[]): GridTable | null {
  const table = schema.tables.find((t) => t.name === name && !t.name.startsWith("sqlite_"))
  if (table) {
    const columns: GridColumn[] = table.columns
      .filter((c) => c.hidden !== 1)
      .map((c) => ({ name: c.name, type: c.type, notNull: c.not_null, pk: c.pk, default: c.default, generated: c.hidden === 2 || c.hidden === 3 }))
    const pk = columns.filter((c) => c.pk > 0).sort((a, b) => a.pk - b.pk)
    const pkIdentity: Identity | null = pk.length ? { kind: "pk", columns: pk.map((c) => c.name) } : null
    const withoutRowid = /\bWITHOUT\s+ROWID\b/i.test(table.sql)
    // A key on a rowid table can hold several NULLs unless it's INTEGER PRIMARY
    // KEY (the rowid alias) or every column is NOT NULL. Only then is rowid
    // used, since VACUUM can renumber rowids.
    const rowidAlias = pk.length === 1 && pk[0]!.type.trim().toUpperCase() === "INTEGER"
    const nonNullKey = pk.length > 0 && pk.every((c) => c.notNull)
    let identity: Identity | null = null
    if (withoutRowid || rowidAlias || nonNullKey) identity = pkIdentity
    else {
      const taken = new Set(table.columns.map((c) => c.name.toLowerCase()))
      const free = ROWID_NAMES.find((n) => !taken.has(n))
      identity = free ? { kind: "rowid", name: free } : pkIdentity
    }
    let readOnly: string | null = null
    if (table.virtual) readOnly = "Virtual tables can't be edited here."
    else if (!identity) readOnly = "This table has no primary key or rowid to find rows by."
    return { name: table.name, kind: "table", columns, identity: table.virtual ? null : identity, readOnly }
  }
  const view = schema.views.find((v) => v.name === name)
  if (view) {
    const columns = (viewColumns ?? []).map((c) => ({ name: c.name, type: c.type, notNull: false, pk: 0, default: null, generated: false }))
    return { name: view.name, kind: "view", columns, identity: null, readOnly: "Views are read-only." }
  }
  return null
}

function identityExprs(t: GridTable): string[] {
  if (!t.identity) return []
  return t.identity.kind === "rowid" ? [t.identity.name] : t.identity.columns.map(quoteIdent)
}

/**
 * Text and blobs come back cut down, with their full length beside them when
 * cut. Without the length column, one extra character or byte is selected so
 * a cut value is still known to be cut.
 */
function previewExprs(column: string, withLength: boolean): string[] {
  const c = quoteIdent(column)
  const extra = withLength ? 0 : 1
  const value = `CASE typeof(${c}) WHEN 'text' THEN substr(${c}, 1, ${TEXT_PREVIEW + extra}) WHEN 'blob' THEN substr(${c}, 1, ${BLOB_PREVIEW + extra}) ELSE ${c} END`
  if (!withLength) return [value]
  const length = `CASE typeof(${c}) WHEN 'text' THEN CASE WHEN length(${c}) > ${TEXT_PREVIEW} THEN length(${c}) END WHEN 'blob' THEN length(${c}) END`
  return [value, length]
}

export interface BrowseQuery {
  filters?: GridFilter[]
  sort?: { column: string; dir: "asc" | "desc" } | null
  page?: number
}

export class GridError extends Error {}

function column(t: GridTable, name: string): GridColumn {
  const c = t.columns.find((x) => x.name === name)
  if (!c) throw new GridError(`No column named ${name} in ${t.name}.`)
  return c
}

function whereClause(t: GridTable, filters: GridFilter[]): Stmt {
  const parts: string[] = []
  const args: InValue[] = []
  for (const f of filters) {
    const col = column(t, f.column)
    const c = quoteIdent(col.name)
    switch (f.op) {
      case "is null":
        parts.push(`${c} IS NULL`)
        break
      case "is not null":
        parts.push(`${c} IS NOT NULL`)
        break
      case "contains":
        parts.push(`${c} LIKE ? ESCAPE '\\'`)
        args.push(likePattern(f.value ?? ""))
        break
      default:
        parts.push(`${c} ${f.op} ?`)
        args.push(paramFor(col, f.value ?? ""))
    }
  }
  return { sql: parts.length ? ` WHERE ${parts.join(" AND ")}` : "", args }
}

export interface BrowseLayout {
  /** How many leading values are the row's identity. */
  keyCount: number
  /** Whether each column is followed by its length value. */
  withLength: boolean
}

/** A page of rows and the matching row count. Run both in one read-only batch. */
export function browseSql(t: GridTable, q: BrowseQuery = {}): { select: Stmt; count: Stmt; layout: BrowseLayout } {
  const from = ` FROM ${quoteIdent(t.name)}`
  const where = whereClause(t, q.filters ?? [])
  const keys = identityExprs(t)
  const withLength = t.columns.length <= MAX_PREVIEW_COLUMNS
  const exprs = [...keys, ...t.columns.flatMap((c) => previewExprs(c.name, withLength))]
  const order: string[] = []
  if (q.sort) order.push(`${quoteIdent(column(t, q.sort.column).name)} ${q.sort.dir === "desc" ? "DESC" : "ASC"}`)
  order.push(...keys)
  const page = Math.max(1, Math.floor(q.page ?? 1))
  const orderBy = order.length ? ` ORDER BY ${order.join(", ")}` : ""
  return {
    select: { sql: `SELECT ${exprs.join(", ") || "NULL"}${from}${where.sql}${orderBy} LIMIT ${PAGE_SIZE} OFFSET ${(page - 1) * PAGE_SIZE}`, args: where.args },
    count: { sql: `SELECT count(*)${from}${where.sql}`, args: where.args },
    layout: { keyCount: keys.length, withLength },
  }
}

/** The column names of a view, from a query that returns no rows. */
export function viewColumnsSql(name: string): string {
  return `SELECT * FROM ${quoteIdent(name)} LIMIT 0`
}

function writable(t: GridTable): Identity {
  if (t.readOnly || !t.identity) throw new GridError(t.readOnly ?? "This table is read-only.")
  return t.identity
}

function keyWhere(t: GridTable, key: RowKey): Stmt {
  writable(t)
  const exprs = identityExprs(t)
  if (key.length !== exprs.length) throw new GridError("That row key doesn't match the table.")
  // `=`, not IS: a NULL key matches no row rather than every NULL row.
  const args = key.map(decodeKey)
  return { sql: ` WHERE ${exprs.map((e) => `${e} = ?`).join(" AND ")}`, args }
}

const INT = /^[+-]?\d+$/
const REAL = /^[+-]?(\d+\.?\d*|\.\d+)(e[+-]?\d+)?$/i
const INT64_MIN = -(2n ** 63n)
const INT64_MAX = 2n ** 63n - 1n

/**
 * The parameter for text typed into a cell. Declared columns convert text by
 * their affinity, so the text goes as is. A column with no declared type
 * keeps whatever it's given, so a number typed there goes as a number.
 */
export function paramFor(c: GridColumn, text: string | null): InValue {
  if (text === null) return null
  if (c.type.trim() !== "" && c.type.toUpperCase() !== "ANY") return text
  // Only canonical digits become a number, so '007' and '+7' stay text.
  if (INT.test(text)) {
    const n = BigInt(text)
    if (n >= INT64_MIN && n <= INT64_MAX && String(n) === text) return n
  }
  if (REAL.test(text) && /[.eE]/.test(text)) return Number(text)
  return text
}

function editable(t: GridTable, name: string): GridColumn {
  const c = column(t, name)
  if (c.generated) throw new GridError(`${c.name} is generated and can't be written.`)
  return c
}

export function updateSql(t: GridTable, key: RowKey, columnName: string, text: string | null): Stmt {
  const c = editable(t, columnName)
  const where = keyWhere(t, key)
  return { sql: `UPDATE ${quoteIdent(t.name)} SET ${quoteIdent(c.name)} = ?${where.sql}`, args: [paramFor(c, text), ...where.args] }
}

/** Columns left out take their default. */
export function insertSql(t: GridTable, values: Record<string, string | null>): Stmt {
  writable(t)
  const names = Object.keys(values)
  if (!names.length) return { sql: `INSERT INTO ${quoteIdent(t.name)} DEFAULT VALUES`, args: [] }
  const cols = names.map((n) => editable(t, n))
  return {
    sql: `INSERT INTO ${quoteIdent(t.name)} (${cols.map((c) => quoteIdent(c.name)).join(", ")}) VALUES (${cols.map(() => "?").join(", ")})`,
    args: cols.map((c) => paramFor(c, values[c.name] ?? null)),
  }
}

export function deleteSql(t: GridTable, key: RowKey): Stmt {
  const where = keyWhere(t, key)
  return { sql: `DELETE FROM ${quoteIdent(t.name)}${where.sql}`, args: where.args }
}

/**
 * One cell's full value, unless it's bigger than CELL_EDIT_MAX bytes: then the
 * value comes back NULL and the size says why. Columns: value, typeof, bytes.
 */
export function cellSql(t: GridTable, key: RowKey, columnName: string): Stmt {
  const c = quoteIdent(column(t, columnName).name)
  const where = keyWhere(t, key)
  return {
    sql: `SELECT CASE WHEN length(CAST(${c} AS BLOB)) > ${CELL_EDIT_MAX} THEN NULL ELSE ${c} END, typeof(${c}), length(CAST(${c} AS BLOB)) FROM ${quoteIdent(t.name)}${where.sql}`,
    args: where.args,
  }
}
