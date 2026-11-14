// Who is asking, and whether they may. Every server function that reads or
// changes something goes through requireViewer or requireCan first.
import { getRequest } from "@tanstack/react-start/server"
import { type Permission, type Role, can, isRole } from "@/lib/roles"
import { getAuth } from "./auth"
import { config } from "./config"

export interface Viewer {
  id: string
  name: string
  email: string
  role: Role
}

export class AccessError extends Error {
  constructor(
    message: string,
    readonly status: 401 | 403,
  ) {
    super(message)
  }
}

export async function getViewer(): Promise<Viewer | null> {
  const auth = await getAuth()
  const session = await auth.api.getSession({ headers: getRequest().headers })
  if (!session) return null
  const u = session.user as { id: string; name: string; email: string; role?: unknown }
  return { id: u.id, name: u.name, email: u.email, role: isRole(u.role) ? u.role : "readonly" }
}

/** Rejects writes sent from another site. GET server functions don't change anything. */
export function requireSameOrigin() {
  const req = getRequest()
  if (req.method === "GET") return
  if (req.headers.get("sec-fetch-site") === "cross-site") throw new AccessError("Invalid origin.", 403)
  const origin = req.headers.get("origin")
  if (origin && origin !== config().publicUrl) throw new AccessError("Invalid origin.", 403)
}

export async function requireViewer(): Promise<Viewer> {
  requireSameOrigin()
  const viewer = await getViewer()
  if (!viewer) throw new AccessError("Sign in again.", 401)
  return viewer
}

export async function requireCan(p: Permission): Promise<Viewer> {
  const viewer = await requireViewer()
  if (!can(viewer.role, p)) throw new AccessError(p === "admin" ? "Only an owner can do that." : "Your role can't make changes.", 403)
  return viewer
}
