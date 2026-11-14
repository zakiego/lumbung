// Dashboard roles. Shared by the server guards and the UI, which hides what a
// role can't do. The server is what enforces it, and the engine enforces it
// again: the dashboard sends each person's level with every request.
//
// An owner manages the whole instance: every workspace, the members and the
// settings. Everyone else has a role in each workspace they were given, and a
// workspace they have no role in doesn't exist for them.
export const WORKSPACE_ROLES = ["admin", "member", "readonly"] as const
export type WorkspaceRole = (typeof WORKSPACE_ROLES)[number]

/** A person's role in one workspace. An owner is an owner in all of them. */
export type Role = "owner" | WorkspaceRole

export const ROLE_LABEL: Record<Role, string> = {
  owner: "Owner",
  admin: "Admin",
  member: "Member",
  readonly: "Read-only",
}

/**
 * read: see databases and buckets, run read-only SQL, download.
 * write: change data, schema, branches, snapshots and objects.
 * admin: create and delete databases and buckets, tokens, the audit log, the trash.
 * owner: workspaces and their limits, moving between them, members, the instance.
 */
export type Permission = "read" | "write" | "admin" | "owner"

export function can(role: Role | undefined, p: Permission): boolean {
  if (!role) return false
  if (p === "read") return true
  if (p === "write") return role !== "readonly"
  if (p === "admin") return role === "owner" || role === "admin"
  return role === "owner"
}

/** The engine access level a workspace role maps to. */
export const ROLE_ACCESS: Record<WorkspaceRole, "read" | "write" | "admin"> = {
  readonly: "read",
  member: "write",
  admin: "admin",
}

export function isWorkspaceRole(v: unknown): v is WorkspaceRole {
  return typeof v === "string" && (WORKSPACE_ROLES as readonly string[]).includes(v)
}

/** One workspace a person may use, and as what. */
export interface WorkspaceGrant {
  workspace: string
  role: WorkspaceRole
}
