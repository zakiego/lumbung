// Dashboard roles. Shared by the server guards and the UI, which hides what a
// role can't do. The server is what enforces it.
export const ROLES = ["owner", "member", "readonly"] as const
export type Role = (typeof ROLES)[number]

export const ROLE_LABEL: Record<Role, string> = {
  owner: "Owner",
  member: "Member",
  readonly: "Read-only",
}

/**
 * read: see databases, run read-only SQL.
 * write: change data, schema, branches, snapshots and databases.
 * admin: tokens, members, invites and instance settings.
 */
export type Permission = "read" | "write" | "admin"

export function can(role: Role | undefined, p: Permission): boolean {
  if (!role) return false
  if (p === "read") return true
  if (p === "write") return role === "owner" || role === "member"
  return role === "owner"
}

export function isRole(v: unknown): v is Role {
  return typeof v === "string" && (ROLES as readonly string[]).includes(v)
}
