// The SQL tab. A read-only role runs everything on a read-only connection.
import type { Result } from "@lumbung/client"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { type Cell, encodeCell } from "@/lib/cells"
import { can } from "@/lib/roles"
import { call } from "@/server/engine"
import { requireIn } from "@/server/session"

/** Rows sent to the browser per statement. The engine's own cap still applies before this. */
export const ROW_CAP = 1000
const MAX_SQL = 1024 * 1024

const ref = z.string().min(1).max(100)
// The workspace a call acts in, by name or id. Every call names one.
const ws = z.string().min(1).max(100)

export interface StatementResult {
  columns: string[]
  rows: Cell[][]
  /** Rows the statement returned, before the cap. */
  rowCount: number
  capped: boolean
  rowsAffected: number
  lastInsertRowid: string
}

export interface RunResult {
  results: StatementResult[]
  ms: number
  readOnly: boolean
}

function shape(r: Result): StatementResult {
  return {
    columns: r.columns,
    rows: r.values.slice(0, ROW_CAP).map((row) => row.map(encodeCell)),
    rowCount: r.values.length,
    capped: r.values.length > ROW_CAP,
    rowsAffected: r.rowsAffected,
    lastInsertRowid: r.lastInsertRowid.toString(),
  }
}

export const runSql = createServerFn({ method: "POST" })
  .validator(z.object({ ws, db: ref, branch: ref, statements: z.array(z.string().min(1).max(MAX_SQL)).min(1).max(200) }))
  .handler(async ({ data }): Promise<RunResult> => {
    const access = await requireIn(data.ws, "read")
    const readOnly = !can(access.role, "write")
    const client = access.engine.database(data.db, data.branch)
    const started = performance.now()
    const results =
      data.statements.length === 1
        ? [await call(() => client.query(data.statements[0]!, undefined, { readOnly }))]
        : await call(() => client.batch(data.statements, { readOnly }))
    return { results: results.map(shape), ms: Math.round(performance.now() - started), readOnly }
  })

export interface PlanStep {
  id: number
  parent: number
  detail: string
}

export const explainSql = createServerFn({ method: "POST" })
  .validator(z.object({ ws, db: ref, branch: ref, sql: z.string().min(1).max(MAX_SQL) }))
  .handler(async ({ data }): Promise<PlanStep[]> => {
    const access = await requireIn(data.ws, "read")
    const client = access.engine.database(data.db, data.branch)
    const sql = data.sql.trim().replace(/;+\s*$/, "")
    const r = await call(() => client.query(`EXPLAIN QUERY PLAN ${sql}`, undefined, { readOnly: true }))
    const at = (name: string) => r.columns.indexOf(name)
    return r.values.map((row) => ({ id: Number(row[at("id")] ?? 0), parent: Number(row[at("parent")] ?? 0), detail: String(row[at("detail")] ?? "") }))
  })
