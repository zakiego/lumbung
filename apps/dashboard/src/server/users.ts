// Users, their access and invites. Users are created here on the server
// (first-run setup and accepted invites), never through a public sign-up
// endpoint.
//
// A user is an owner or not: the role column is "owner" for one and anything
// else for the rest. What anyone but an owner may use is a list of workspaces
// with a role in each, kept in workspace_member.
//
// The column is also what a dashboard from before workspaces reads as an
// instance-wide role. So for someone invited or changed since, it holds
// NOT_OWNER, the least that dashboard knows: a rollback gives nobody more
// than they had in any workspace.
import { createHash, randomBytes, randomUUID, timingSafeEqual } from "node:crypto"
import { type WorkspaceGrant, isWorkspaceRole } from "@/lib/roles"
import { grantsOf, setGrants } from "./access"
import { getAuth } from "./auth"
import { getDb } from "./db"

export class UserError extends Error {}

const NOT_OWNER = "readonly"

/** What a person may use: everything, or a role in each workspace listed (by workspace id). */
export type MemberAccess = { owner: true } | { owner: false; grants: WorkspaceGrant[] }

export interface Member {
  id: string
  name: string
  email: string
  owner: boolean
  grants: WorkspaceGrant[]
  createdAt: string
}

const INVITE_TTL_MS = 7 * 24 * 60 * 60 * 1000

function sha256(s: string): string {
  return createHash("sha256").update(s).digest("hex")
}

export function sameSecret(a: string, b: string): boolean {
  const x = Buffer.from(sha256(a))
  const y = Buffer.from(sha256(b))
  return timingSafeEqual(x, y)
}

export function normalizeEmail(email: string): string {
  return email.trim().toLowerCase()
}

// One user creation at a time, so two first-run requests can't both become owner.
let queue: Promise<unknown> = Promise.resolve()
function serial<T>(fn: () => Promise<T>): Promise<T> {
  const run = queue.then(fn, fn)
  queue = run.catch(() => {})
  return run
}

export async function userCount(): Promise<number> {
  await getAuth()
  const row = getDb().prepare(`SELECT COUNT(*) AS n FROM "user"`).get() as { n: number }
  return Number(row.n)
}

async function createUser(input: { email: string; name: string; password: string; role: string }) {
  const auth = await getAuth()
  const ctx = await auth.$context
  const email = normalizeEmail(input.email)
  if (await ctx.internalAdapter.findUserByEmail(email)) throw new UserError("Someone already uses that email.")
  const { minPasswordLength, maxPasswordLength } = ctx.password.config
  if (input.password.length < minPasswordLength) throw new UserError(`Use at least ${minPasswordLength} characters.`)
  if (input.password.length > maxPasswordLength) throw new UserError(`Use at most ${maxPasswordLength} characters.`)
  const hash = await ctx.password.hash(input.password)
  const user = await ctx.internalAdapter.createUser({ email, name: input.name.trim(), emailVerified: false, role: input.role }, { method: "email-password" })
  await ctx.internalAdapter.linkAccount({ userId: user.id, providerId: "credential", accountId: user.id, password: hash })
  return user
}

/** First-run setup: creates the owner, only while there are no users. */
export function createOwner(input: { email: string; name: string; password: string }) {
  return serial(async () => {
    if ((await userCount()) > 0) throw new UserError("This dashboard already has an owner.")
    return createUser({ ...input, role: "owner" })
  })
}

export async function listMembers(): Promise<Member[]> {
  await getAuth()
  const rows = getDb().prepare(`SELECT id, name, email, role, createdAt FROM "user" ORDER BY createdAt`).all() as Record<string, unknown>[]
  return rows.map((r) => {
    const owner = r.role === "owner"
    return {
      id: String(r.id),
      name: String(r.name ?? ""),
      email: String(r.email),
      owner,
      grants: owner ? [] : grantsOf(String(r.id)),
      createdAt: toIso(r.createdAt),
    }
  })
}

function toIso(v: unknown): string {
  if (typeof v === "number") return new Date(v).toISOString()
  const d = new Date(String(v))
  return Number.isNaN(d.getTime()) ? String(v) : d.toISOString()
}

function owners(): number {
  const row = getDb().prepare(`SELECT COUNT(*) AS n FROM "user" WHERE role = 'owner'`).get() as { n: number }
  return Number(row.n)
}

function isOwner(id: string): boolean | undefined {
  const row = getDb().prepare(`SELECT role FROM "user" WHERE id = ?`).get(id) as { role?: string } | undefined
  return row ? row.role === "owner" : undefined
}

/** One role per workspace, and only roles that exist. */
function cleanGrants(grants: WorkspaceGrant[]): WorkspaceGrant[] {
  const seen = new Map<string, WorkspaceGrant>()
  for (const g of grants) {
    if (!g.workspace || !isWorkspaceRole(g.role)) throw new UserError("Pick a role for each workspace.")
    seen.set(g.workspace, { workspace: g.workspace, role: g.role })
  }
  return [...seen.values()]
}

/** Changes what a member may use: make them an owner, or give them a role per workspace. */
export function setAccess(actorId: string, id: string, access: MemberAccess) {
  return serial(async () => {
    const auth = await getAuth()
    const current = isOwner(id)
    if (current === undefined) throw new UserError("That member doesn't exist.")
    if (id === actorId) throw new UserError("You can't change your own access.")
    if (current && !access.owner && owners() <= 1) throw new UserError("Keep at least one owner.")
    const grants = access.owner ? [] : cleanGrants(access.grants)
    const ctx = await auth.$context
    // Grants first: if a later step fails, a demoted owner is still an owner
    // and a promoted member has no grants, never the old reach under a new role.
    setGrants(id, grants)
    await ctx.internalAdapter.updateUser(id, { role: access.owner ? "owner" : NOT_OWNER })
    // New access applies from the next request; drop sessions so it's immediate.
    await ctx.internalAdapter.deleteUserSessions(id)
  })
}

export function removeMember(actorId: string, id: string) {
  return serial(async () => {
    const auth = await getAuth()
    const current = isOwner(id)
    if (current === undefined) throw new UserError("That member doesn't exist.")
    if (id === actorId) throw new UserError("You can't remove yourself.")
    if (current && owners() <= 1) throw new UserError("Keep at least one owner.")
    const ctx = await auth.$context
    await ctx.internalAdapter.deleteUserSessions(id)
    getDb().prepare(`DELETE FROM workspace_member WHERE user_id = ?`).run(id)
    getDb().prepare(`DELETE FROM passkey WHERE userId = ?`).run(id)
    getDb().prepare(`DELETE FROM account WHERE userId = ?`).run(id)
    getDb().prepare(`DELETE FROM "user" WHERE id = ?`).run(id)
  })
}

export interface Invite {
  id: string
  email: string
  owner: boolean
  grants: WorkspaceGrant[]
  createdAt: string
  expiresAt: string
}

type InviteRow = { id: string; email: string; role: string; workspaces: string | null; created_at: number; expires_at: number }

function readGrants(json: string | null): WorkspaceGrant[] {
  if (!json) return []
  try {
    const v = JSON.parse(json) as unknown
    if (!Array.isArray(v)) return []
    return v.flatMap((g) => (g && typeof g.workspace === "string" && isWorkspaceRole(g.role) ? [{ workspace: g.workspace as string, role: g.role }] : []))
  } catch {
    return []
  }
}

function toInvite(r: InviteRow): Invite {
  return {
    id: r.id,
    email: r.email,
    owner: r.role === "owner",
    grants: r.role === "owner" ? [] : readGrants(r.workspaces),
    createdAt: new Date(Number(r.created_at)).toISOString(),
    expiresAt: new Date(Number(r.expires_at)).toISOString(),
  }
}

/** Creates an invite and returns its token, shown once. Only the hash is kept. */
export async function createInvite(actorId: string, emailIn: string, access: MemberAccess): Promise<{ invite: Invite; token: string }> {
  await getAuth()
  const email = normalizeEmail(emailIn)
  const grants = access.owner ? [] : cleanGrants(access.grants)
  if (!access.owner && grants.length === 0) throw new UserError("Pick at least one workspace.")
  const db = getDb()
  const taken = db.prepare(`SELECT 1 FROM "user" WHERE email = ?`).get(email)
  if (taken) throw new UserError("Someone already uses that email.")
  const token = randomBytes(24).toString("base64url")
  const now = Date.now()
  const id = randomUUID()
  const row: InviteRow = {
    id,
    email,
    role: access.owner ? "owner" : NOT_OWNER,
    workspaces: access.owner ? null : JSON.stringify(grants),
    created_at: now,
    expires_at: now + INVITE_TTL_MS,
  }
  // A new invite for the same email replaces the old one.
  db.prepare(`DELETE FROM invite WHERE email = ? AND used_at IS NULL`).run(email)
  db.prepare(`INSERT INTO invite (id, token_hash, email, role, workspaces, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`).run(
    id,
    sha256(token),
    email,
    row.role,
    row.workspaces,
    actorId,
    now,
    row.expires_at,
  )
  return { invite: toInvite(row), token }
}

export async function listInvites(): Promise<Invite[]> {
  await getAuth()
  const rows = getDb()
    .prepare(`SELECT id, email, role, workspaces, created_at, expires_at FROM invite WHERE used_at IS NULL AND expires_at > ? ORDER BY created_at DESC`)
    .all(Date.now()) as InviteRow[]
  return rows.map(toInvite)
}

export async function revokeInvite(id: string) {
  await getAuth()
  getDb().prepare(`DELETE FROM invite WHERE id = ? AND used_at IS NULL`).run(id)
}

type OpenInvite = { id: string; email: string; role: string; owner: boolean; grants: WorkspaceGrant[] }

/** The invite behind a token, if it's still open. */
export async function findInvite(token: string): Promise<OpenInvite | undefined> {
  await getAuth()
  const row = getDb()
    .prepare(`SELECT id, email, role, workspaces, created_at, expires_at FROM invite WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`)
    .get(sha256(token), Date.now()) as InviteRow | undefined
  if (!row) return undefined
  const invite = toInvite(row)
  return { id: row.id, email: row.email, role: row.role, owner: invite.owner, grants: invite.grants }
}

/** Accepts an invite: creates the user with the invite's email and access, and closes the invite. */
export function acceptInvite(token: string, input: { name: string; password: string }) {
  return serial(async () => {
    const invite = await findInvite(token)
    if (!invite) throw new UserError("This invite expired or was already used.")
    const db = getDb()
    // Claim it first, so a second request with the same token can't also use it.
    const claimed = db.prepare(`UPDATE invite SET used_at = ? WHERE id = ? AND used_at IS NULL`).run(Date.now(), invite.id)
    if (Number(claimed.changes) !== 1) throw new UserError("This invite expired or was already used.")
    try {
      // An invite from before workspaces keeps its old role on the user, and
      // the upgrade step turns it into a role in the default workspace.
      const user = await createUser({ email: invite.email, name: input.name, password: input.password, role: invite.owner ? "owner" : invite.grants.length ? NOT_OWNER : invite.role })
      if (!invite.owner) {
        try {
          setGrants(user.id, invite.grants)
        } catch (e) {
          // Without its grants the account would hold the email and be able to do nothing. Take it back.
          db.prepare(`DELETE FROM account WHERE userId = ?`).run(user.id)
          db.prepare(`DELETE FROM "user" WHERE id = ?`).run(user.id)
          throw e
        }
      }
      return { user, email: invite.email }
    } catch (e) {
      db.prepare(`UPDATE invite SET used_at = NULL WHERE id = ?`).run(invite.id)
      throw e
    }
  })
}
