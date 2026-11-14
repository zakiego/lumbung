// Object uploads and downloads move file bodies, too big for server
// functions, so they use a plain route under /app/api. Both stream: an
// upload goes to the engine as it arrives, never buffered, and the engine
// enforces the size limits.
import { type Client, LumbungError } from "@lumbung/client"
import { type Permission, can } from "@/lib/roles"
import { crossOriginWrite } from "./auth"
import { engineFor, userMessage } from "./engine"
import { type Viewer, viewerFromRequest } from "./session"

export type ObjectDeps = {
  viewer: (request: Request) => Promise<Viewer | null>
  engine: (viewer: Viewer) => Pick<Client, "bucket">
}

const defaults: ObjectDeps = { viewer: viewerFromRequest, engine: engineFor }

function fail(status: number, message: string): Response {
  return Response.json({ error: message }, { status, headers: { "cache-control": "no-store" } })
}

async function guard(request: Request, p: Permission, deps: ObjectDeps): Promise<Viewer | Response> {
  if (crossOriginWrite(request)) return fail(403, "Invalid origin.")
  const viewer = await deps.viewer(request)
  if (!viewer) return fail(401, "Sign in again.")
  if (!can(viewer.role, p)) return fail(403, "Your role can't do that.")
  return viewer
}

function engineFailure(e: unknown): Response {
  const status = e instanceof LumbungError && e.status >= 400 && e.status < 600 ? e.status : 502
  return fail(status, userMessage(e))
}

function target(request: Request): { bucket: string; key: string } | null {
  const q = new URL(request.url).searchParams
  const bucket = q.get("bucket") ?? ""
  const key = q.get("key") ?? ""
  if (!bucket || bucket.length > 100 || !key || new TextEncoder().encode(key).length > 1024) return null
  return { bucket, key }
}

/** PUT /app/api/objects?bucket=&key=, the file as the body with a Content-Length. */
export async function uploadObject(request: Request, deps: ObjectDeps = defaults): Promise<Response> {
  const viewer = await guard(request, "write", deps)
  if (viewer instanceof Response) return viewer
  const t = target(request)
  if (!t) return fail(400, "Pick a bucket and a key.")
  const length = Number(request.headers.get("content-length"))
  if (!request.headers.has("content-length") || !Number.isSafeInteger(length) || length < 0) return fail(411, "The upload needs a length.")
  try {
    const body = request.body ?? new ReadableStream<Uint8Array>({ start: (c) => c.close() })
    const out = await deps.engine(viewer).bucket(t.bucket).put(t.key, body, {
      contentLength: length,
      contentType: request.headers.get("content-type") || "application/octet-stream",
    })
    return Response.json(out, { headers: { "cache-control": "no-store" } })
  } catch (e) {
    return engineFailure(e)
  }
}

/**
 * GET /app/api/objects?bucket=&key= streams an object as a download. This
 * is the dashboard's own origin, so the content is always an attachment,
 * never rendered, and sandboxed in case a browser renders it anyway.
 */
export async function downloadObject(request: Request, deps: ObjectDeps = defaults): Promise<Response> {
  const viewer = await guard(request, "read", deps)
  if (viewer instanceof Response) return viewer
  const t = target(request)
  if (!t) return fail(400, "Pick a bucket and a key.")
  let obj
  try {
    obj = await deps.engine(viewer).bucket(t.bucket).get(t.key)
  } catch (e) {
    return engineFailure(e)
  }
  const base = t.key.split("/").pop() || "download"
  const ascii = base.replace(/[^\w.-]/g, "_")
  const headers = new Headers({
    "content-type": "application/octet-stream",
    "content-disposition": `attachment; filename="${ascii}"; filename*=UTF-8''${encodeURIComponent(base)}`,
    "content-security-policy": "sandbox; default-src 'none'",
    "x-content-type-options": "nosniff",
    "cache-control": "no-store",
  })
  const length = obj.response.headers.get("content-length")
  if (length) headers.set("content-length", length)
  return new Response(obj.response.body, { status: 200, headers })
}
