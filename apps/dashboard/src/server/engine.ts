// The engine, as the dashboard sees it: one SDK client with the service
// token, tagged with the signed-in person so the engine's audit log says who
// did it. The service token never leaves the server.
import { type Client, LumbungError, createClient } from "@lumbung/client"
import { config, engineToken } from "./config"
import type { Viewer } from "./session"

export function engineFor(viewer: Viewer | null): Client {
  const headers: Record<string, string> = {}
  if (viewer) headers["X-Lumbung-Actor"] = `dashboard:${viewer.email}`
  return createClient({ url: config().engineUrl, token: engineToken(), headers, intMode: "bigint" })
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
