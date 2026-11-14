// Snapshots, tokens and the audit log of a workspace, and what belongs to the
// instance: members, invites and its settings.
import type { AuditOut, ScheduleOut, SnapshotOut, TokenOut } from "@lumbung/client"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { APP_BASE } from "@/lib/paths"
import { WORKSPACE_ROLES } from "@/lib/roles"
import { allWorkspaces } from "@/server/access"
import { config } from "@/server/config"
import { allPages, call, engineFor } from "@/server/engine"
import { forgetMinted, recordMinted, revokeOutlived } from "@/server/minted"
import { type Viewer, requireIn, requireOwner } from "@/server/session"
import { UserError, createInvite, listInvites, listMembers, removeMember, revokeInvite, setAccess } from "@/server/users"

const ref = z.string().min(1).max(100)
// The workspace a call acts in, by name or id. Every call names one.
const ws = z.string().min(1).max(100)
const branchName = z.string().regex(/^[a-z0-9][a-z0-9-]{0,62}$/, "Use lowercase letters, digits and dashes, up to 63 characters.")

function userError<T>(fn: () => Promise<T>): Promise<T> {
  return fn().catch((e) => {
    if (e instanceof UserError) throw new Error(e.message)
    throw e
  })
}

export const listSnapshots = createServerFn({ method: "GET" })
  .validator(z.object({ ws, db: ref, branch: ref, cursor: z.string().max(500).optional() }))
  .handler(async ({ data }): Promise<{ data: SnapshotOut[]; nextCursor: string; schedule: ScheduleOut | null }> => {
    const access = await requireIn(data.ws, "read")
    const e = access.engine
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
  .validator(z.object({ ws, db: ref, branch: ref, label: z.string().trim().max(200).optional() }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    return call(() => access.engine.snapshots.create(data.db, data.branch, { label: data.label || undefined }))
  })

export const restoreSnapshot = createServerFn({ method: "POST" })
  .validator(z.object({ ws, db: ref, snapshot: ref, name: branchName }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    return call(() => access.engine.snapshots.restore(data.db, data.snapshot, data.name))
  })

export const deleteSnapshot = createServerFn({ method: "POST" })
  .validator(z.object({ ws, db: ref, snapshot: ref }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    await call(() => access.engine.snapshots.delete(data.db, data.snapshot))
    return { ok: true }
  })

export const setSchedule = createServerFn({ method: "POST" })
  .validator(
    z.object({
      ws,
      db: ref,
      branch: ref,
      everyMinutes: z.number().int().min(1).max(525600),
      keepCount: z.number().int().min(0).max(10000).optional(),
      keepDays: z.number().int().min(0).max(3650).optional(),
    }),
  )
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    return call(() => access.engine.snapshots.schedule.set(data.db, data.branch, data))
  })

export const deleteSchedule = createServerFn({ method: "POST" })
  .validator(z.object({ ws, db: ref, branch: ref }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    await call(() => access.engine.snapshots.schedule.delete(data.db, data.branch))
    return { ok: true }
  })

export const listTokens = createServerFn({ method: "GET" })
  .validator(z.object({ ws }))
  .handler(async ({ data }): Promise<TokenOut[]> => {
    const { engine } = await requireIn(data.ws, "admin")
    return call(() => allPages((cursor) => engine.tokens.list({ limit: 100, cursor }), 2000))
  })

const level = z.enum(["read", "write", "admin"])

export const createToken = createServerFn({ method: "POST" })
  .validator(
    z.object({
      ws,
      name: z.string().trim().min(1).max(100),
      access: level.optional(),
      database: ref.optional(),
      branch: ref.optional(),
      bucket: ref.optional(),
      grants: z
        .array(z.object({ access: level, database: ref.optional(), branch: ref.optional(), bucket: ref.optional() }))
        .min(1)
        .max(20)
        .optional(),
      accessKey: z.boolean().optional(),
      expiresInDays: z.number().int().min(1).max(3650).optional(),
    }),
  )
  .handler(async ({ data }) => {
    const { engine, viewer, workspace } = await requireIn(data.ws, "admin")
    const { name, accessKey, expiresInDays } = data
    // One token for several databases and buckets goes as grants; anything else keeps the plain shape.
    // Without a database, a bucket or grants, the token reaches the whole workspace.
    if (!data.grants && !data.access) throw new Error("Pick an access level.")
    const { access = "read", database, branch, bucket } = data
    const created = await call(() =>
      data.grants ? engine.tokens.create({ name, accessKey, expiresInDays, grants: data.grants }) : engine.tokens.create({ name, accessKey, expiresInDays, access, database, branch, bucket }),
    )
    // Remembered, so it can be revoked if its maker stops being an admin here.
    recordMinted(created.id, workspace.id, viewer.id)
    return created
  })

export const revokeToken = createServerFn({ method: "POST" })
  .validator(z.object({ ws, id: ref }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "admin")
    await call(() => access.engine.tokens.revoke(data.id))
    forgetMinted(data.id)
    return { ok: true }
  })

export const listAudit = createServerFn({ method: "GET" })
  .validator(z.object({ ws, cursor: z.string().max(500).optional() }))
  .handler(async ({ data }): Promise<{ data: AuditOut[]; nextCursor: string }> => {
    const access = await requireIn(data.ws, "admin")
    return call(() => access.engine.audit.list({ limit: 50, cursor: data.cursor }))
  })

const grants = z.array(z.object({ workspace: ref, role: z.enum(WORKSPACE_ROLES) })).max(200)
const memberAccess = z.discriminatedUnion("owner", [z.object({ owner: z.literal(true) }), z.object({ owner: z.literal(false), grants })])

/**
 * A grant must name a workspace that exists, so a typo can't sit in the table
 * waiting for one. Listing the workspaces also runs the upgrade from
 * instance-wide roles first, so it never mistakes someone invited just now
 * for a member from before workspaces.
 */
async function known(viewer: Viewer, access: z.infer<typeof memberAccess>) {
  const ids = new Set((await call(() => allWorkspaces())).map((w) => w.id))
  if (access.owner) return
  if (access.grants.every((g) => ids.has(g.workspace))) return
  // A role in a deleted workspace is kept across a save, for the restore.
  const e = engineFor(viewer)
  for (const w of await call(() => allPages((cursor) => e.workspaces.trash({ limit: 200, cursor })))) ids.add(w.id)
  for (const g of access.grants) if (!ids.has(g.workspace)) throw new Error("That workspace doesn't exist.")
}

export const getMembers = createServerFn({ method: "GET" }).handler(async () => {
  const viewer = await requireOwner()
  const e = engineFor(viewer)
  const [members, invites, workspaces, trashed] = await Promise.all([
    listMembers(),
    listInvites(),
    call(() => allWorkspaces()),
    // A role in a deleted workspace comes back with it on a restore, so the page shows it.
    allPages((cursor) => e.workspaces.trash({ limit: 200, cursor })).catch(() => []),
  ])
  return { members, invites, workspaces, deleted: trashed.map((w) => ({ id: w.id, name: w.name })) }
})

export const inviteMember = createServerFn({ method: "POST" })
  .validator(z.object({ email: z.string().trim().email().max(200), access: memberAccess }))
  .handler(async ({ data }) => {
    const viewer = await requireOwner()
    await known(viewer, data.access)
    const { invite, token } = await userError(() => createInvite(viewer.id, data.email, data.access))
    return { invite, url: `${config().publicUrl}${APP_BASE}/invite/${token}` }
  })

export const cancelInvite = createServerFn({ method: "POST" })
  .validator(z.object({ id: ref }))
  .handler(async ({ data }) => {
    await requireOwner()
    await revokeInvite(data.id)
    return { ok: true }
  })

export const changeAccess = createServerFn({ method: "POST" })
  .validator(z.object({ id: ref, access: memberAccess }))
  .handler(async ({ data }) => {
    const viewer = await requireOwner()
    await known(viewer, data.access)
    await userError(() => setAccess(viewer.id, data.id, data.access))
    // Tokens they made where they are no longer an admin stop working with their access.
    return { ok: true, revoked: await revokeOutlived(viewer).catch(() => 0) }
  })

export const removeUser = createServerFn({ method: "POST" })
  .validator(z.object({ id: ref }))
  .handler(async ({ data }) => {
    const viewer = await requireOwner()
    await userError(() => removeMember(viewer.id, data.id))
    return { ok: true, revoked: await revokeOutlived(viewer).catch(() => 0) }
  })

export const getInstance = createServerFn({ method: "GET" }).handler(async () => {
  await requireOwner()
  const cfg = config()
  type Health = { status: string; version?: string; meta_version?: number }
  let health: Health | null
  try {
    const res = await fetch(`${cfg.engineUrl}/health`, { signal: AbortSignal.timeout(3000) })
    health = (await res.json()) as Health
  } catch {
    health = null
  }
  return { engineUrl: cfg.engineUrl, publicUrl: cfg.publicUrl, health }
})
