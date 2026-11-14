// Snapshots, tokens, members, invites, audit and instance info.
import type { AuditOut, ScheduleOut, SnapshotOut, TokenOut } from "@lumbung/client"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { ROLES } from "@/lib/roles"
import { config } from "@/server/config"
import { call, engineFor } from "@/server/engine"
import { requireCan } from "@/server/session"
import { UserError, createInvite, listInvites, listMembers, removeMember, revokeInvite, setRole } from "@/server/users"

const ref = z.string().min(1).max(100)
const branchName = z.string().regex(/^[a-z0-9][a-z0-9-]{0,62}$/, "Use lowercase letters, digits and dashes, up to 63 characters.")

function userError<T>(fn: () => Promise<T>): Promise<T> {
  return fn().catch((e) => {
    if (e instanceof UserError) throw new Error(e.message)
    throw e
  })
}

export const listSnapshots = createServerFn({ method: "GET" })
  .validator(z.object({ db: ref, branch: ref, cursor: z.string().max(500).optional() }))
  .handler(async ({ data }): Promise<{ data: SnapshotOut[]; nextCursor: string; schedule: ScheduleOut | null }> => {
    const viewer = await requireCan("read")
    const e = engineFor(viewer)
    return call(async () => {
      const [page, schedule] = await Promise.all([
        e.snapshots.list(data.db, data.branch, { limit: 50, cursor: data.cursor }),
        e.snapshots.schedule.get(data.db, data.branch).catch((err: { status?: number }) => {
          if (err.status === 404) return null
          throw err
        }),
      ])
      return { data: page.data, nextCursor: page.nextCursor, schedule }
    })
  })

export const createSnapshot = createServerFn({ method: "POST" })
  .validator(z.object({ db: ref, branch: ref, label: z.string().trim().max(200).optional() }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("write")
    return call(() => engineFor(viewer).snapshots.create(data.db, data.branch, { label: data.label || undefined }))
  })

export const restoreSnapshot = createServerFn({ method: "POST" })
  .validator(z.object({ db: ref, snapshot: ref, name: branchName }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("write")
    return call(() => engineFor(viewer).snapshots.restore(data.db, data.snapshot, data.name))
  })

export const deleteSnapshot = createServerFn({ method: "POST" })
  .validator(z.object({ db: ref, snapshot: ref }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("write")
    await call(() => engineFor(viewer).snapshots.delete(data.db, data.snapshot))
    return { ok: true }
  })

export const setSchedule = createServerFn({ method: "POST" })
  .validator(
    z.object({
      db: ref,
      branch: ref,
      everyMinutes: z.number().int().min(1).max(525600),
      keepCount: z.number().int().min(0).max(10000).optional(),
      keepDays: z.number().int().min(0).max(3650).optional(),
    }),
  )
  .handler(async ({ data }) => {
    const viewer = await requireCan("write")
    return call(() => engineFor(viewer).snapshots.schedule.set(data.db, data.branch, data))
  })

export const deleteSchedule = createServerFn({ method: "POST" })
  .validator(z.object({ db: ref, branch: ref }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("write")
    await call(() => engineFor(viewer).snapshots.schedule.delete(data.db, data.branch))
    return { ok: true }
  })

export const listTokens = createServerFn({ method: "GET" }).handler(async (): Promise<TokenOut[]> => {
  const viewer = await requireCan("admin")
  const e = engineFor(viewer)
  return call(async () => {
    const out: TokenOut[] = []
    let cursor: string | undefined
    do {
      const p = await e.tokens.list({ limit: 100, cursor })
      out.push(...p.data)
      cursor = p.nextCursor || undefined
    } while (cursor && out.length < 2000)
    return out
  })
})

export const createToken = createServerFn({ method: "POST" })
  .validator(
    z.object({
      name: z.string().trim().min(1).max(100),
      access: z.enum(["read", "write", "admin"]),
      database: ref.optional(),
      branch: ref.optional(),
      expiresInDays: z.number().int().min(1).max(3650).optional(),
    }),
  )
  .handler(async ({ data }) => {
    const viewer = await requireCan("admin")
    return call(() => engineFor(viewer).tokens.create(data))
  })

export const revokeToken = createServerFn({ method: "POST" })
  .validator(z.object({ id: ref }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("admin")
    await call(() => engineFor(viewer).tokens.revoke(data.id))
    return { ok: true }
  })

export const listAudit = createServerFn({ method: "GET" })
  .validator(z.object({ cursor: z.string().max(500).optional() }))
  .handler(async ({ data }): Promise<{ data: AuditOut[]; nextCursor: string }> => {
    const viewer = await requireCan("write")
    return call(() => engineFor(viewer).audit.list({ limit: 50, cursor: data.cursor }))
  })

export const getMembers = createServerFn({ method: "GET" }).handler(async () => {
  await requireCan("admin")
  const [members, invites] = await Promise.all([listMembers(), listInvites()])
  return { members, invites }
})

export const inviteMember = createServerFn({ method: "POST" })
  .validator(z.object({ email: z.string().trim().email().max(200), role: z.enum(ROLES) }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("admin")
    const { invite, token } = await userError(() => createInvite(viewer.id, data.email, data.role))
    return { invite, url: `${config().publicUrl}/app/invite/${token}` }
  })

export const cancelInvite = createServerFn({ method: "POST" })
  .validator(z.object({ id: ref }))
  .handler(async ({ data }) => {
    await requireCan("admin")
    await revokeInvite(data.id)
    return { ok: true }
  })

export const changeRole = createServerFn({ method: "POST" })
  .validator(z.object({ id: ref, role: z.enum(ROLES) }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("admin")
    await userError(() => setRole(viewer.id, data.id, data.role))
    return { ok: true }
  })

export const removeUser = createServerFn({ method: "POST" })
  .validator(z.object({ id: ref }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("admin")
    await userError(() => removeMember(viewer.id, data.id))
    return { ok: true }
  })

export const getInstance = createServerFn({ method: "GET" }).handler(async () => {
  await requireCan("admin")
  const cfg = config()
  let health: { status: string; version?: string; meta_version?: number } | null = null
  try {
    const res = await fetch(`${cfg.engineUrl}/health`, { signal: AbortSignal.timeout(3000) })
    health = (await res.json()) as typeof health
  } catch {
    health = null
  }
  return { engineUrl: cfg.engineUrl, publicUrl: cfg.publicUrl, health }
})
