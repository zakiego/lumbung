// Users, roles and invites. Users are created here on the server (first-run
// setup and accepted invites), never through a public sign-up endpoint.
import { createHash, randomBytes, randomUUID, timingSafeEqual } from "node:crypto"
import { type Role, isRole } from "@/lib/roles"
import { getAuth } from "./auth"
import { getDb } from "./db"

export class UserError extends Error {}

export interface Member {
  id: string
  name: string
  email: string
  role: Role
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

async function createUser(input: { email: string; name: string; password: string; role: Role }) {
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
  return rows.map((r) => ({
    id: String(r.id),
    name: String(r.name ?? ""),
    email: String(r.email),
    role: isRole(r.role) ? r.role : "readonly",
    createdAt: toIso(r.createdAt),
  }))
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

function roleOf(id: string): Role | undefined {
  const row = getDb().prepare(`SELECT role FROM "user" WHERE id = ?`).get(id) as { role?: string } | undefined
  return row && isRole(row.role) ? row.role : undefined
}

export function setRole(actorId: string, id: string, role: Role) {
  return serial(async () => {
    const auth = await getAuth()
    const current = roleOf(id)
    if (!current) throw new UserError("That member doesn't exist.")
    if (id === actorId) throw new UserError("You can't change your own role.")
    if (current === "owner" && role !== "owner" && owners() <= 1) throw new UserError("Keep at least one owner.")
    const ctx = await auth.$context
    await ctx.internalAdapter.updateUser(id, { role })
    // A new role applies from the next request; drop sessions so it's immediate.
    await ctx.internalAdapter.deleteUserSessions(id)
  })
}

export function removeMember(actorId: string, id: string) {
  return serial(async () => {
    const auth = await getAuth()
    const current = roleOf(id)
    if (!current) throw new UserError("That member doesn't exist.")
    if (id === actorId) throw new UserError("You can't remove yourself.")
    if (current === "owner" && owners() <= 1) throw new UserError("Keep at least one owner.")
    const ctx = await auth.$context
    await ctx.internalAdapter.deleteUserSessions(id)
    getDb().prepare(`DELETE FROM passkey WHERE userId = ?`).run(id)
    getDb().prepare(`DELETE FROM account WHERE userId = ?`).run(id)
    getDb().prepare(`DELETE FROM "user" WHERE id = ?`).run(id)
  })
}

export interface Invite {
  id: string
  email: string
  role: Role
  createdAt: string
  expiresAt: string
}

/** Creates an invite and returns its token, shown once. Only the hash is kept. */
export async function createInvite(actorId: string, emailIn: string, role: Role): Promise<{ invite: Invite; token: string }> {
  await getAuth()
  const email = normalizeEmail(emailIn)
  const db = getDb()
  const taken = db.prepare(`SELECT 1 FROM "user" WHERE email = ?`).get(email)
  if (taken) throw new UserError("Someone already uses that email.")
  const token = randomBytes(24).toString("base64url")
  const now = Date.now()
  const id = randomUUID()
  // A new invite for the same email replaces the old one.
  db.prepare(`DELETE FROM invite WHERE email = ? AND used_at IS NULL`).run(email)
  db.prepare(`INSERT INTO invite (id, token_hash, email, role, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`).run(
    id,
    sha256(token),
    email,
    role,
    actorId,
    now,
    now + INVITE_TTL_MS,
  )
  return { invite: { id, email, role, createdAt: new Date(now).toISOString(), expiresAt: new Date(now + INVITE_TTL_MS).toISOString() }, token }
}

export async function listInvites(): Promise<Invite[]> {
  await getAuth()
  const rows = getDb()
    .prepare(`SELECT id, email, role, created_at, expires_at FROM invite WHERE used_at IS NULL AND expires_at > ? ORDER BY created_at DESC`)
    .all(Date.now()) as { id: string; email: string; role: string; created_at: number; expires_at: number }[]
  return rows.map((r) => ({
    id: r.id,
    email: r.email,
    role: isRole(r.role) ? r.role : "readonly",
    createdAt: new Date(Number(r.created_at)).toISOString(),
    expiresAt: new Date(Number(r.expires_at)).toISOString(),
  }))
}

export async function revokeInvite(id: string) {
  await getAuth()
  getDb().prepare(`DELETE FROM invite WHERE id = ? AND used_at IS NULL`).run(id)
}

/** The invite behind a token, if it's still open. */
export async function findInvite(token: string): Promise<{ id: string; email: string; role: Role } | undefined> {
  await getAuth()
  const row = getDb()
    .prepare(`SELECT id, email, role FROM invite WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`)
    .get(sha256(token), Date.now()) as { id: string; email: string; role: string } | undefined
  if (!row || !isRole(row.role)) return undefined
  return { id: row.id, email: row.email, role: row.role }
}

/** Accepts an invite: creates the user with the invite's email and role, and closes the invite. */
export function acceptInvite(token: string, input: { name: string; password: string }) {
  return serial(async () => {
    const invite = await findInvite(token)
    if (!invite) throw new UserError("This invite expired or was already used.")
    const db = getDb()
    // Claim it first, so a second request with the same token can't also use it.
    const claimed = db.prepare(`UPDATE invite SET used_at = ? WHERE id = ? AND used_at IS NULL`).run(Date.now(), invite.id)
    if (Number(claimed.changes) !== 1) throw new UserError("This invite expired or was already used.")
    try {
      const user = await createUser({ email: invite.email, name: input.name, password: input.password, role: invite.role })
      return { user, email: invite.email }
    } catch (e) {
      db.prepare(`UPDATE invite SET used_at = NULL WHERE id = ?`).run(invite.id)
      throw e
    }
  })
}
