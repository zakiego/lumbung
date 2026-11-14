// Databases and branches, through the engine's REST API.
import type { BranchOut, DatabaseOut, Diff, Schema, StatsOutBody } from "@lumbung/client"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { WINDOWS, type WorkspaceStats, mergeStats } from "@/lib/stats"
import { allPages, call } from "@/server/engine"
import { requireIn } from "@/server/session"

const name = z.string().regex(/^[a-z0-9][a-z0-9-]{0,62}$/, "Use lowercase letters, digits and dashes, up to 63 characters.")
const ref = z.string().min(1).max(100)
// The workspace a call acts in, by name or id. Every call names one.
const ws = z.string().min(1).max(100)
const db = z.object({ ws, db: ref })
const branch = z.object({ ws, db: ref, branch: ref })

export const listDatabases = createServerFn({ method: "GET" })
  .validator(z.object({ ws }))
  .handler(async ({ data }): Promise<DatabaseOut[]> => {
  const access = await requireIn(data.ws, "read")
  const e = access.engine
  return call(() => allPages((cursor) => e.databases.list({ limit: 100, cursor })))
})

export const listTrash = createServerFn({ method: "GET" })
  .validator(z.object({ ws }))
  .handler(async ({ data }): Promise<DatabaseOut[]> => {
  const access = await requireIn(data.ws, "admin")
  const e = access.engine
  return call(() => allPages((cursor) => e.databases.trash({ limit: 100, cursor })))
})

export const getDatabase = createServerFn({ method: "GET" })
  .validator(db)
  .handler(async ({ data }): Promise<{ database: DatabaseOut; branches: BranchOut[] }> => {
    const access = await requireIn(data.ws, "read")
    const e = access.engine
    return call(async () => {
      const [database, branches] = await Promise.all([e.databases.get(data.db), allPages((cursor) => e.branches.list(data.db, { limit: 100, cursor }))])
      return { database, branches }
    })
  })

export const createDatabase = createServerFn({ method: "POST" })
  .validator(z.object({ ws, name }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "admin")
    return call(() => access.engine.databases.create(data.name))
  })

export const renameDatabase = createServerFn({ method: "POST" })
  .validator(z.object({ ws, db: ref, name }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "admin")
    return call(() => access.engine.databases.rename(data.db, data.name))
  })

export const deleteDatabase = createServerFn({ method: "POST" })
  .validator(db)
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "admin")
    return call(() => access.engine.databases.delete(data.db))
  })

export const restoreDatabase = createServerFn({ method: "POST" })
  .validator(z.object({ ws, db: ref, name: name.optional() }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "admin")
    return call(() => access.engine.databases.restore(data.db, data.name))
  })

export const createBranch = createServerFn({ method: "POST" })
  .validator(z.object({ ws, db: ref, name, from: ref.optional(), expiresInDays: z.number().int().min(1).max(3650).optional() }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    return call(() => access.engine.branches.create(data.db, data.name, { from: data.from, expiresInDays: data.expiresInDays }))
  })

export const updateBranch = createServerFn({ method: "POST" })
  .validator(
    z.object({ ws, db: ref, branch: ref, name: name.optional(), protected: z.boolean().optional(), expiresInDays: z.number().int().min(0).max(3650).optional() }),
  )
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    const { db: d, branch: b, ...patch } = data
    return call(() => access.engine.branches.update(d, b, patch))
  })

export const deleteBranch = createServerFn({ method: "POST" })
  .validator(branch)
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    await call(() => access.engine.branches.delete(data.db, data.branch))
    return { ok: true }
  })

export const resetBranch = createServerFn({ method: "POST" })
  .validator(branch)
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    return call(() => access.engine.branches.reset(data.db, data.branch))
  })

export const promoteBranch = createServerFn({ method: "POST" })
  .validator(branch)
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    return call(() => access.engine.branches.promote(data.db, data.branch))
  })

export const diffBranch = createServerFn({ method: "GET" })
  .validator(z.object({ ws, db: ref, branch: ref, against: ref }))
  .handler(async ({ data }): Promise<Diff> => {
    const access = await requireIn(data.ws, "read")
    return call(() => access.engine.branches.diff(data.db, data.branch, data.against))
  })

export const getSchema = createServerFn({ method: "GET" })
  .validator(branch)
  .handler(async ({ data }): Promise<Schema> => {
    const access = await requireIn(data.ws, "read")
    return call(() => access.engine.branches.schema(data.db, data.branch))
  })

export const getStats = createServerFn({ method: "GET" })
  .validator(z.object({ ws, db: ref, branch: ref, window: z.enum(WINDOWS).default("24h") }))
  .handler(async ({ data }): Promise<StatsOutBody> => {
    const access = await requireIn(data.ws, "read")
    return call(() => access.engine.branches.stats(data.db, data.branch, data.window))
  })

// Traffic on every database's default branch, added up. A database whose
// stats fail is listed in `failed` instead of failing the whole call.
export const getWorkspaceStats = createServerFn({ method: "GET" })
  .validator(z.object({ ws, window: z.enum(WINDOWS).default("24h"), databases: z.array(z.object({ name: ref, branch: ref })).max(1000) }))
  .handler(async ({ data }): Promise<WorkspaceStats> => {
    const access = await requireIn(data.ws, "read")
    const e = access.engine
    const dbs = data.databases
    const results: { db: string; stats: StatsOutBody | null }[] = []
    const queue = [...dbs]
    const worker = async () => {
      for (let d = queue.shift(); d; d = queue.shift()) {
        const stats = await e.branches.stats(d.name, d.branch, data.window).catch(() => null)
        results.push({ db: d.name, stats })
      }
    }
    await Promise.all(Array.from({ length: Math.min(8, dbs.length) }, worker))
    const merged = mergeStats(results)
    if (dbs.length > 0 && merged.failed.length === dbs.length) throw new Error("Couldn't load stats for any database.")
    return merged
  })
