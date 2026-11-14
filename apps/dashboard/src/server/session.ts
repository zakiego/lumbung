// Who is asking, and whether they may. Every server function that reads or
// changes something goes through requireIn (inside a workspace) or
// requireOwner (the instance) first.
import type { Client } from "@lumbung/client"
import { getRequest } from "@tanstack/react-start/server"
import { type Permission, type Role, can } from "@/lib/roles"
import { NoWorkspace, type VisibleWorkspace, workspaceFor } from "./access"
import { crossOriginWrite, getAuth } from "./auth"
import { engineIn } from "./engine"

export interface Viewer {
  id: string
  name: string
  email: string
  /** An owner manages the instance and has every workspace. Everyone else has a role per workspace. */
  owner: boolean
}

export class AccessError extends Error {
  constructor(
    message: string,
    readonly status: 401 | 403 | 404 | 502,
  ) {
    super(message)
  }
}

export async function viewerFromRequest(request: Request): Promise<Viewer | null> {
  const auth = await getAuth(request)
  const session = await auth.api.getSession({ headers: request.headers })
  if (!session) return null
  const u = session.user as { id: string; name: string; email: string; role?: unknown }
  return { id: u.id, name: u.name, email: u.email, owner: u.role === "owner" }
}

export function getViewer(): Promise<Viewer | null> {
  return viewerFromRequest(getRequest())
}

/** Rejects writes sent from another site. GET server functions don't change anything. */
export function requireSameOrigin() {
  if (crossOriginWrite(getRequest())) throw new AccessError("Invalid origin.", 403)
}

export async function requireViewer(): Promise<Viewer> {
  requireSameOrigin()
  const viewer = await getViewer()
  if (!viewer) throw new AccessError("Sign in again.", 401)
  return viewer
}

/** For what belongs to the instance: workspaces, members, settings. */
export async function requireOwner(): Promise<Viewer> {
  const viewer = await requireViewer()
  if (!viewer.owner) throw new AccessError("Only an owner can do that.", 403)
  return viewer
}

/** A person inside one workspace: who, where, as what, and the engine client that carries all three. */
export interface Access {
  viewer: Viewer
  workspace: VisibleWorkspace
  role: Role
  engine: Client
}

export function denied(p: Permission): string {
  if (p === "owner") return "Only an owner can do that."
  if (p === "admin") return "Only an admin of this workspace can do that."
  return "Your role can't make changes."
}

/** The workspace a request names and the person's place in it, or the reason they have none. */
export async function accessTo(viewer: Viewer, ws: string, p: Permission): Promise<Access> {
  let workspace: VisibleWorkspace
  try {
    workspace = await workspaceFor(viewer, ws)
  } catch (e) {
    if (e instanceof NoWorkspace) throw new AccessError(e.message, 404)
    // The engine's own error names its address, which isn't for the browser.
    throw new AccessError("Can't reach the engine.", 502)
  }
  if (!can(workspace.role, p)) throw new AccessError(denied(p), 403)
  return { viewer, workspace, role: workspace.role, engine: engineIn(viewer, workspace.id, workspace.role) }
}

/** Guards a server function that acts inside the workspace `ws` (a name or an id). */
export async function requireIn(ws: string, p: Permission): Promise<Access> {
  return accessTo(await requireViewer(), ws, p)
}
