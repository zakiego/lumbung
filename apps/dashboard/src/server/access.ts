// Who may use which workspace. Workspaces live in the engine; a person's role
// in each one lives here, in the dashboard's own database. An owner reaches
// every workspace. Anyone else only the ones they were given, and to them
// the rest look exactly like workspaces that don't exist.
import type { WorkspaceOut } from "@lumbung/client"
import { type Role, type WorkspaceGrant, type WorkspaceRole, isWorkspaceRole } from "@/lib/roles"
import { getAuth } from "./auth"
import { getDb } from "./db"
import { allPages, serviceClient } from "./engine"

export interface Workspace {
  id: string
  name: string
  default: boolean
}

export interface VisibleWorkspace extends Workspace {
  role: Role
}

export class NoWorkspace extends Error {
  readonly status = 404
  constructor() {
    super("That workspace doesn't exist.")
  }
}

// The engine's workspace list, kept for a moment so the several server
// functions one page load fires share one request. Roles are never cached:
// they are read from the database on every call.
const LIST_TTL_MS = 2000
let listed: { at: number; list: Promise<Workspace[]> } | undefined

export function forgetWorkspaces() {
  listed = undefined
}

function slim(w: WorkspaceOut): Workspace {
  return { id: w.id, name: w.name, default: w.default }
}

async function fetchWorkspaces(): Promise<Workspace[]> {
  const e = serviceClient()
  return (await allPages((cursor) => e.workspaces.list({ limit: 200, cursor }), 100_000)).map(slim)
}

/** Every live workspace on the instance. Callers filter it by who is asking. */
export async function allWorkspaces(): Promise<Workspace[]> {
  if (!listed || Date.now() - listed.at >= LIST_TTL_MS) {
    const list = fetchWorkspaces()
    listed = { at: Date.now(), list }
    list.catch(() => {
      if (listed?.list === list) listed = undefined
    })
  }
  const all = await listed.list
  const first = all.find((w) => w.default)
  if (first) await adoptLegacyRoles(first.id)
  return all
}

let adopted = false

/**
 * The upgrade to workspaces, once: a member or read-only person from before
 * gets that role in the default workspace, and so does an open invite. It
 * runs on first use because it needs the default workspace's id.
 */
async function adoptLegacyRoles(defaultWorkspaceId: string) {
  if (adopted) return
  await getAuth()
  const db = getDb()
  if (db.prepare(`SELECT 1 FROM dashboard_meta WHERE key = 'workspace_roles'`).get()) {
    adopted = true
    return
  }
  db.exec("BEGIN IMMEDIATE")
  try {
    if (!db.prepare(`SELECT 1 FROM dashboard_meta WHERE key = 'workspace_roles'`).get()) {
      const now = Date.now()
      // Someone who already has a role somewhere was placed by an owner, not carried over.
      const users = db
        .prepare(`SELECT id, role FROM "user" WHERE (role IS NULL OR role != 'owner') AND id NOT IN (SELECT user_id FROM workspace_member)`)
        .all() as { id: string; role: string | null }[]
      const add = db.prepare(`INSERT OR IGNORE INTO workspace_member (user_id, workspace_id, role, created_at) VALUES (?, ?, ?, ?)`)
      for (const u of users) add.run(u.id, defaultWorkspaceId, u.role === "member" ? "member" : "readonly", now)
      // The role column is left as it was. From here on it only says owner or
      // not, and a dashboard from before workspaces that is ever put back on
      // this database still finds everyone's old role.
      const invites = db.prepare(`SELECT id, role FROM invite WHERE used_at IS NULL AND workspaces IS NULL AND role != 'owner'`).all() as { id: string; role: string }[]
      const grant = db.prepare(`UPDATE invite SET workspaces = ? WHERE id = ?`)
      for (const i of invites) grant.run(JSON.stringify([{ workspace: defaultWorkspaceId, role: i.role === "member" ? "member" : "readonly" }]), i.id)
      db.prepare(`INSERT INTO dashboard_meta (key, value) VALUES ('workspace_roles', ?)`).run(String(now))
    }
    db.exec("COMMIT")
  } catch (e) {
    db.exec("ROLLBACK")
    throw e
  }
  adopted = true
}

/** Only for tests. */
export function resetAccess() {
  adopted = false
  listed = undefined
}

/** The workspaces a person who isn't an owner was given, by workspace id. */
export function grantsOf(userId: string): WorkspaceGrant[] {
  const rows = getDb().prepare(`SELECT workspace_id, role FROM workspace_member WHERE user_id = ? ORDER BY created_at, workspace_id`).all(userId) as {
    workspace_id: string
    role: string
  }[]
  return rows.flatMap((r) => (isWorkspaceRole(r.role) ? [{ workspace: r.workspace_id, role: r.role }] : []))
}

/** Replaces everything a person was given. */
export function setGrants(userId: string, grants: WorkspaceGrant[]) {
  const db = getDb()
  const now = Date.now()
  db.exec("BEGIN IMMEDIATE")
  try {
    db.prepare(`DELETE FROM workspace_member WHERE user_id = ?`).run(userId)
    const add = db.prepare(`INSERT OR REPLACE INTO workspace_member (user_id, workspace_id, role, created_at) VALUES (?, ?, ?, ?)`)
    for (const g of grants) add.run(userId, g.workspace, g.role, now)
    db.exec("COMMIT")
  } catch (e) {
    db.exec("ROLLBACK")
    throw e
  }
}

export interface Asker {
  id: string
  owner: boolean
}

/** The workspaces a person may use, each with their role in it. */
export async function visibleWorkspaces(who: Asker): Promise<VisibleWorkspace[]> {
  const all = await allWorkspaces()
  if (who.owner) return all.map((w) => ({ ...w, role: "owner" as const }))
  const mine = new Map<string, WorkspaceRole>(grantsOf(who.id).map((g) => [g.workspace, g.role]))
  return all.flatMap((w) => {
    const role = mine.get(w.id)
    return role ? [{ ...w, role }] : []
  })
}

/**
 * The workspace a name or an id refers to, for this person. One they have no
 * role in and one that doesn't exist get the same answer.
 */
export async function workspaceFor(who: Asker, ref: string): Promise<VisibleWorkspace> {
  const found = (await visibleWorkspaces(who)).find((w) => w.id === ref || w.name === ref)
  if (!found) throw new NoWorkspace()
  return found
}
