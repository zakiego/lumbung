import { drizzle as proxy, type SqliteRemoteDatabase } from "drizzle-orm/sqlite-proxy"

import { createClient, type Client, type ClientOptions } from "./client.ts"
import type { InValue } from "./values.ts"

type Method = "run" | "all" | "values" | "get"

/**
 * Drizzle on a Lumbung branch, through Drizzle's sqlite-proxy driver.
 * Pass a client, or the options to make one. `db.batch([...])` runs as one
 * Lumbung batch: one transaction, all or nothing.
 */
export function drizzle<TSchema extends Record<string, unknown> = Record<string, never>>(
  client: Client | ClientOptions,
  config: { schema?: TSchema; logger?: boolean } = {},
): SqliteRemoteDatabase<TSchema> {
  const c = "query" in client ? client : createClient(client)
  const shape = (method: Method, values: unknown[][]): { rows: unknown[] } => {
    if (method === "get") return { rows: values[0] as unknown[] }
    return { rows: values }
  }
  return proxy<TSchema>(
    async (sql, params, method) => {
      const r = await c.query({ sql, args: params as InValue[] })
      return shape(method, r.values)
    },
    async (queries) => {
      const results = await c.batch(queries.map((q) => ({ sql: q.sql, args: q.params as InValue[] })))
      return results.map((r, i) => shape(queries[i]!.method, r.values))
    },
    config,
  )
}
