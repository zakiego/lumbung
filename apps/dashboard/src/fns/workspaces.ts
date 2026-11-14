// Workspaces: one workspace's settings, and what only an owner does with
// them (create, limits, delete, restore, move), plus the instance's own
// tokens and audit log.
import type { AuditOut, TokenOut, WorkspaceOut } from "@lumbung/client"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import type { Role } from "@/lib/roles"
import { forgetWorkspaces } from "@/server/access"
import { allPages, call, engineFor } from "@/server/engine"
import { forgetMinted, recordMinted, revokeOutlived } from "@/server/minted"
import { requireIn, requireOwner } from "@/server/session"

const ws = z.string().min(1).max(100)
const ref = z.string().min(1).max(100)
const name = z.string().regex(/^[a-z0-9][a-z0-9-]{0,62}$/, "Use lowercase letters, digits and dashes, up to 63 characters.")
const count = z.number().int().min(0).max(1_000_000)
const bytes = z.number().int().min(0).max(Number.MAX_SAFE_INTEGER)
const limits = { maxDatabases: count.optional(), maxBuckets: count.optional(), maxDatabaseBytes: bytes.optional(), maxObjectBytes: bytes.optional() }

/** One workspace with its limits and usage, for its settings page. */
export const getWorkspace = createServerFn({ method: "GET" })
  .validator(z.object({ ws }))
  .handler(async ({ data }): Promise<{ workspace: WorkspaceOut; role: Role }> => {
    const access = await requireIn(data.ws, "admin")
    return { workspace: await call(() => access.engine.workspaces.current()), role: access.role }
  })

export const createWorkspace = createServerFn({ method: "POST" })
  .validator(z.object({ name, ...limits }))
  .handler(async ({ data }) => {
    const viewer = await requireOwner()
    const { name: n, ...l } = data
    const made = await call(() => engineFor(viewer).workspaces.create(n, l))
    forgetWorkspaces()
    return made
  })

export const updateWorkspace = createServerFn({ method: "POST" })
  .validator(z.object({ ws, name: name.optional(), ...limits }))
  .handler(async ({ data }) => {
    const { viewer, workspace } = await requireIn(data.ws, "owner")
    const { ws: _, ...patch } = data
    const changed = await call(() => engineFor(viewer).workspaces.update(workspace.id, patch))
    forgetWorkspaces()
    return changed
  })

export const deleteWorkspace = createServerFn({ method: "POST" })
  .validator(z.object({ ws }))
  .handler(async ({ data }) => {
    const { viewer, workspace } = await requireIn(data.ws, "owner")
    const gone = await call(() => engineFor(viewer).workspaces.delete(workspace.id))
    forgetWorkspaces()
    return gone
  })

export const listDeletedWorkspaces = createServerFn({ method: "GET" }).handler(async (): Promise<WorkspaceOut[]> => {
  const viewer = await requireOwner()
  const e = engineFor(viewer)
  return call(() => allPages((cursor) => e.workspaces.trash({ limit: 200, cursor })))
})

export const restoreWorkspace = createServerFn({ method: "POST" })
  .validator(z.object({ id: ref, name: name.optional() }))
  .handler(async ({ data }) => {
    const viewer = await requireOwner()
    const back = await call(() => engineFor(viewer).workspaces.restore(data.id, data.name))
    forgetWorkspaces()
    // A restore brings the workspace's tokens back. Those whose maker lost
    // their place while it was in the trash don't come back with it.
    await revokeOutlived(viewer).catch(() => 0)
    return back
  })

// Moving revokes every token that reached the database or bucket, so it is
// an owner's call. `to` is the target workspace's id.
export const moveDatabase = createServerFn({ method: "POST" })
  .validator(z.object({ ws, db: ref, to: ref }))
  .handler(async ({ data }) => {
    const { viewer, workspace } = await requireIn(data.ws, "owner")
    return call(() => engineFor(viewer).workspace(workspace.id).databases.move(data.db, data.to))
  })

export const moveBucket = createServerFn({ method: "POST" })
  .validator(z.object({ ws, bucket: ref, to: ref }))
  .handler(async ({ data }) => {
    const { viewer, workspace } = await requireIn(data.ws, "owner")
    return call(() => engineFor(viewer).workspace(workspace.id).buckets.move(data.bucket, data.to))
  })

export const listInstanceTokens = createServerFn({ method: "GET" }).handler(async (): Promise<TokenOut[]> => {
  const viewer = await requireOwner()
  const e = engineFor(viewer)
  return call(() => allPages((cursor) => e.instance.tokens.list({ limit: 100, cursor }), 2000))
})

export const createInstanceToken = createServerFn({ method: "POST" })
  .validator(
    z.object({
      name: z.string().trim().min(1).max(100),
      access: z.enum(["read", "write", "admin"]),
      accessKey: z.boolean().optional(),
      expiresInDays: z.number().int().min(1).max(3650).optional(),
    }),
  )
  .handler(async ({ data }) => {
    const viewer = await requireOwner()
    const created = await call(() => engineFor(viewer).instance.tokens.create(data))
    recordMinted(created.id, null, viewer.id)
    return created
  })

export const revokeInstanceToken = createServerFn({ method: "POST" })
  .validator(z.object({ id: ref }))
  .handler(async ({ data }) => {
    const viewer = await requireOwner()
    await call(() => engineFor(viewer).instance.tokens.revoke(data.id))
    forgetMinted(data.id)
    return { ok: true }
  })

export const listInstanceAudit = createServerFn({ method: "GET" })
  .validator(z.object({ cursor: z.string().max(500).optional() }))
  .handler(async ({ data }): Promise<{ data: AuditOut[]; nextCursor: string }> => {
    const viewer = await requireOwner()
    return call(() => engineFor(viewer).instance.audit.list({ limit: 50, cursor: data.cursor }))
  })
