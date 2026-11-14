// The workspace a page is in, on the browser's side. The server decides who
// may use which; this only carries the answer around.
import { APP_BASE } from "./paths"
import type { Role } from "./roles"

/** A workspace the signed-in person may use, with their role in it. */
export interface WorkspaceInfo {
  id: string
  name: string
  default: boolean
  role: Role
}

/** The signed-in person as the interface sees them inside a workspace. */
export interface Person {
  id: string
  name: string
  email: string
  owner: boolean
  /** Their role in the workspace on screen. */
  role: Role
}

// Pages outside a workspace (members, the instance's settings, the account)
// keep the sidebar of the workspace the person came from. The cookie is only
// a hint: the server picks among the workspaces the person may use.
export const LAST_WORKSPACE_COOKIE = "lumbung.workspace"

let last: string | undefined

export function rememberWorkspace(id: string) {
  if (last === id) return
  last = id
  if (typeof document === "undefined" || !/^ws_[a-z0-9]{20}$/.test(id)) return
  document.cookie = `${LAST_WORKSPACE_COOKIE}=${id}; path=${APP_BASE}; max-age=31536000; samesite=lax`
}

export function lastWorkspace(): string | undefined {
  return last
}

/** The workspace to show when the URL names none: the last one opened, else the default, else any. */
export function pickWorkspace(all: WorkspaceInfo[], want?: string | null): WorkspaceInfo | null {
  return (want ? all.find((w) => w.id === want || w.name === want) : undefined) ?? all.find((w) => w.default) ?? all[0] ?? null
}

export function findWorkspace(all: WorkspaceInfo[], ref: string): WorkspaceInfo | undefined {
  return all.find((w) => w.id === ref || w.name === ref)
}

/**
 * Where a URL from before workspaces goes now: the same page in the default
 * workspace (or the one the person can use). Null when they have none.
 */
export function legacyHref(all: WorkspaceInfo[] | null, rest: string, search = ""): string | null {
  const workspace = (all ?? []).find((w) => w.default) ?? all?.[0]
  if (!workspace) return null
  const path = rest
    .split("/")
    .filter((s) => s && s !== "." && s !== "..")
    .map(encodeURIComponent)
    .join("/")
  return `${APP_BASE}/w/${workspace.name}${path ? `/${path}` : ""}${search}`
}
