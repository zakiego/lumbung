// Object uploads and downloads move file bodies, too big for server
// functions, so they use a plain route under /app/api. Both stream: an
// upload goes to the engine as it arrives, never buffered, and the engine
// enforces the size limits. Large files go up as a multipart upload, part
// by part, so no single request runs long enough to meet the server's
// request timeout.
import { type Client, LumbungError } from "@lumbung/client"
import { type Permission, can } from "@/lib/roles"
import { crossOriginWrite } from "./auth"
import { config, engineToken } from "./config"
import { engineFor, userMessage } from "./engine"
import { type Viewer, viewerFromRequest } from "./session"

/** A request to the engine's S3 API, with the service token as a bearer token. */
export type S3Call = (method: string, path: string, query: Record<string, string>, init?: { body?: BodyInit | null; headers?: Record<string, string> }) => Promise<Response>

const BUCKET_RE = /^(?:[a-z0-9][a-z0-9-]{1,61}[a-z0-9]|bk_[a-z0-9]{20})$/

/** Whether a key has a "." or ".." segment, which URL parsers resolve away. */
export function hasDotSegment(key: string): boolean {
  return key.split("/").some((s) => s === "." || s === "..")
}

export type ObjectDeps = {
  viewer: (request: Request) => Promise<Viewer | null>
  engine: (viewer: Viewer) => Pick<Client, "bucket">
  s3?: S3Call
}

const engineS3: S3Call = (method, path, query, init = {}) => {
  const url = `${config().engineUrl}${path}${Object.keys(query).length ? `?${new URLSearchParams(query)}` : ""}`
  // The service token is an instance admin: the request must reach exactly
  // the object path asked for, never another endpoint.
  const expected = new URL(config().engineUrl).pathname.replace(/\/$/, "") + path
  if (!path.startsWith("/v1/s3/") || new URL(url).pathname !== expected) {
    return Promise.reject(new LumbungError("bad_request", 400, "That key can't be sent through the dashboard."))
  }
  return fetch(url, {
    method,
    body: init.body,
    headers: { authorization: `Bearer ${engineToken()}`, ...init.headers },
    ...(init.body instanceof ReadableStream ? { duplex: "half" } : {}),
  } as RequestInit)
}

const defaults: ObjectDeps = { viewer: viewerFromRequest, engine: engineFor, s3: engineS3 }

/**
 * The S3 path of an object. Callers check the bucket name and refuse keys
 * with dot segments first: URL parsers resolve "." and ".." (and their
 * percent-encoded forms), which would send the request somewhere else.
 */
export function s3Path(bucket: string, key: string): string {
  if (!BUCKET_RE.test(bucket) || hasDotSegment(key)) throw new LumbungError("bad_request", 400, "That key can't be sent through the dashboard.")
  return `/v1/s3/${bucket}/${key.split("/").map(encodeURIComponent).join("/")}`
}

function xmlText(s: string): string {
  return s
    .replace(/&#(\d+);/g, (_, n) => String.fromCharCode(Number(n)))
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&")
}

async function s3Failure(res: Response): Promise<Response> {
  const text = await res.text()
  const message = xmlText(text.match(/<Message>([^<]*)<\/Message>/)?.[1] ?? `HTTP ${res.status}`)
  return fail(res.status >= 400 && res.status < 600 ? res.status : 502, message)
}

/** The signed-in person, for the engine's audit log. */
function actor(viewer: Viewer): Record<string, string> {
  return { "x-lumbung-actor": `dashboard:${viewer.email}` }
}

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
  if (!BUCKET_RE.test(bucket) || !key || new TextEncoder().encode(key).length > 1024) return null
  return { bucket, key }
}

/** A multipart target: a key the S3 path can carry as is. */
function multipartTarget(request: Request): { bucket: string; key: string } | Response {
  const t = target(request)
  if (!t) return fail(400, "Pick a bucket and a key.")
  if (hasDotSegment(t.key)) return fail(400, "A key with . or .. segments can't be uploaded in parts; upload it in one piece.")
  return t
}

/**
 * PUT /app/api/objects?bucket=&key=, the file as the body with a Content-Length.
 * With &uploadId=&partNumber=, the body is one part of a multipart upload.
 */
export async function uploadObject(request: Request, deps: ObjectDeps = defaults): Promise<Response> {
  const viewer = await guard(request, "write", deps)
  if (viewer instanceof Response) return viewer
  const t = target(request)
  if (!t) return fail(400, "Pick a bucket and a key.")
  const length = Number(request.headers.get("content-length"))
  if (!request.headers.has("content-length") || !Number.isSafeInteger(length) || length < 0) return fail(411, "The upload needs a length.")
  const q = new URL(request.url).searchParams
  const uploadId = q.get("uploadId")
  if (uploadId !== null) {
    const part = q.get("partNumber") ?? ""
    if (!/^up_[a-z0-9]{20}$/.test(uploadId) || !/^[1-9][0-9]{0,4}$/.test(part)) return fail(400, "Pick an upload and a part number.")
    const mt = multipartTarget(request)
    if (mt instanceof Response) return mt
    try {
      const res = await (deps.s3 ?? engineS3)("PUT", s3Path(mt.bucket, mt.key), { uploadId, partNumber: part }, {
        body: request.body ?? "",
        headers: { "content-length": String(length), ...actor(viewer) },
      })
      if (!res.ok) return s3Failure(res)
      return Response.json({ etag: (res.headers.get("etag") ?? "").replace(/"/g, "") }, { headers: { "cache-control": "no-store" } })
    } catch (e) {
      return engineFailure(e)
    }
  }
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
  // RFC 5987 leaves ' ( ) * out of attr-char too.
  const encoded = encodeURIComponent(base).replace(/['()*]/g, (c) => `%${c.charCodeAt(0).toString(16).toUpperCase()}`)
  const headers = new Headers({
    "content-type": "application/octet-stream",
    "content-disposition": `attachment; filename="${ascii}"; filename*=UTF-8''${encoded}`,
    "content-security-policy": "sandbox; default-src 'none'",
    "x-content-type-options": "nosniff",
    "cache-control": "no-store",
  })
  // fetch decodes a stored Content-Encoding (gzip, br), so the engine's
  // length is the encoded one; only pass it on for bodies left as they are.
  const length = obj.response.headers.get("content-length")
  if (length && !obj.response.headers.get("content-encoding")) headers.set("content-length", length)
  return new Response(obj.response.body, { status: 200, headers })
}

/**
 * POST /app/api/objects?bucket=&key=&uploads starts a multipart upload.
 * POST ...&uploadId= with {"parts": [{"number", "etag"}]} completes it.
 */
export async function multipartObject(request: Request, deps: ObjectDeps = defaults): Promise<Response> {
  const viewer = await guard(request, "write", deps)
  if (viewer instanceof Response) return viewer
  const t = multipartTarget(request)
  if (t instanceof Response) return t
  const q = new URL(request.url).searchParams
  const s3 = deps.s3 ?? engineS3
  try {
    if (q.has("uploads")) {
      const res = await s3("POST", s3Path(t.bucket, t.key), { uploads: "" }, {
        headers: { "content-type": request.headers.get("x-object-content-type") || "application/octet-stream", ...actor(viewer) },
      })
      if (!res.ok) return s3Failure(res)
      const uploadId = (await res.text()).match(/<UploadId>([^<]+)<\/UploadId>/)?.[1]
      if (!uploadId) return fail(502, "The engine didn't return an upload id.")
      return Response.json({ uploadId }, { headers: { "cache-control": "no-store" } })
    }
    const uploadId = q.get("uploadId") ?? ""
    if (!/^up_[a-z0-9]{20}$/.test(uploadId)) return fail(400, "Pick an upload.")
    const body = (await request.json().catch(() => null)) as { parts?: { number: number; etag: string }[] } | null
    const parts = body?.parts
    if (!Array.isArray(parts) || parts.length === 0 || parts.length > 10000) return fail(400, "List the uploaded parts.")
    const xml = parts
      .map((p) => {
        if (!p || typeof p !== "object" || !Number.isInteger(p.number) || p.number < 1 || p.number > 10000 || typeof p.etag !== "string" || !/^[0-9a-f]{32}$/.test(p.etag)) {
          throw new Error("bad part")
        }
        return `<Part><PartNumber>${p.number}</PartNumber><ETag>"${p.etag}"</ETag></Part>`
      })
      .join("")
    const res = await s3("POST", s3Path(t.bucket, t.key), { uploadId }, {
      body: `<CompleteMultipartUpload>${xml}</CompleteMultipartUpload>`,
      headers: { "content-type": "application/xml", ...actor(viewer) },
    })
    if (!res.ok) return s3Failure(res)
    const etag = (await res.text()).match(/<ETag>&#34;([^&<]+)&#34;<\/ETag>|<ETag>"?([^"<]+)"?<\/ETag>/)
    return Response.json({ key: t.key, etag: etag?.[1] ?? etag?.[2] ?? "" }, { headers: { "cache-control": "no-store" } })
  } catch (e) {
    if (e instanceof Error && e.message === "bad part") return fail(400, "A part number or ETag isn't valid.")
    return engineFailure(e)
  }
}

/** DELETE /app/api/objects?bucket=&key=&uploadId= aborts a multipart upload. */
export async function abortUpload(request: Request, deps: ObjectDeps = defaults): Promise<Response> {
  const viewer = await guard(request, "write", deps)
  if (viewer instanceof Response) return viewer
  const t = multipartTarget(request)
  if (t instanceof Response) return t
  const uploadId = new URL(request.url).searchParams.get("uploadId") ?? ""
  if (!/^up_[a-z0-9]{20}$/.test(uploadId)) return fail(400, "Pick an upload.")
  try {
    const res = await (deps.s3 ?? engineS3)("DELETE", s3Path(t.bucket, t.key), { uploadId }, { headers: actor(viewer) })
    if (!res.ok && res.status !== 404) return s3Failure(res)
    return new Response(null, { status: 204 })
  } catch (e) {
    return engineFailure(e)
  }
}
