// The engine, as the dashboard sees it: SDK clients with the service token,
// tagged with the signed-in person so the engine's audit log says who did
// it. The service token never leaves the server.
//
// The service token is an instance admin, so what a person may do is decided
// twice. The dashboard checks their role, and every request inside a
// workspace names that workspace and, for anyone but an owner, carries their
// level in X-Lumbung-Actor-Access. The engine then treats the request as a
// workspace token of that level, so a server function that forgets a check
// still can't do more than the person may.
import { type Client, LumbungError, createClient } from "@lumbung/client"
import { ROLE_ACCESS, type Role } from "@/lib/roles"
import { config, engineToken } from "./config"

export interface Actor {
  email: string
  owner: boolean
}

function actorHeader(actor: Actor): Record<string, string> {
  return { "X-Lumbung-Actor": `dashboard:${actor.email}` }
}

/** The headers that name a person and, unless they own the instance, their level in the workspace. */
export function accessHeaders(actor: Actor, role: Role): Record<string, string> {
  const headers = actorHeader(actor)
  if (role !== "owner" || !actor.owner) headers["X-Lumbung-Actor-Access"] = ROLE_ACCESS[role === "owner" ? "readonly" : role]
  return headers
}

/**
 * The service token with nobody behind it. Only for the dashboard's own
 * bookkeeping, like listing workspaces to find the one a URL names. Never
 * for something a person asked for.
 */
export function serviceClient(): Client {
  return createClient({ url: config().engineUrl, token: engineToken(), intMode: "bigint" })
}

/** Calls about the instance itself: workspaces, moves, instance tokens and its audit log. Owners only. */
export function engineFor(actor: Actor): Client {
  if (!actor.owner) throw new Error("engineFor: only an owner acts on the instance")
  return createClient({ url: config().engineUrl, token: engineToken(), headers: actorHeader(actor), intMode: "bigint" })
}

/** Calls inside one workspace, by id, at the person's level there. */
export function engineIn(actor: Actor, workspaceId: string, role: Role): Client {
  return createClient({ url: config().engineUrl, token: engineToken(), workspace: workspaceId, headers: accessHeaders(actor, role), intMode: "bigint" })
}

/** Turns an engine error into one the browser can show. Server functions only pass the message through. */
export function userMessage(e: unknown): string {
  if (e instanceof LumbungError) {
    if (e.code === "network_error") return "Can't reach the engine."
    return e.detail || e.message
  }
  return e instanceof Error ? e.message : String(e)
}

/** Runs an engine call and rethrows its error as a plain Error with a readable message. */
export async function call<T>(fn: () => Promise<T>): Promise<T> {
  try {
    return await fn()
  } catch (e) {
    const err = new Error(userMessage(e))
    if (e instanceof LumbungError) (err as Error & { code?: string }).code = e.code
    throw err
  }
}

/** Every page of a cursor list, up to `max` items. */
export async function allPages<T>(page: (cursor?: string) => Promise<{ data: T[]; nextCursor: string }>, max = 1000): Promise<T[]> {
  const out: T[] = []
  let cursor: string | undefined
  do {
    const p = await page(cursor)
    out.push(...p.data)
    cursor = p.nextCursor || undefined
  } while (cursor && out.length < max)
  return out
}
