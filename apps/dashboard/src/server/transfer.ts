// Import and export move whole databases, too big for server functions, so they
// use plain routes under /app/api. Each checks the session itself. Imports are
// buffered (capped, one at a time) because the SDK's import takes bytes.
import { type Client, LumbungError } from "@lumbung/client"
import { type Permission, can } from "@/lib/roles"
import { crossOriginWrite } from "./auth"
import { engineFor, userMessage } from "./engine"
import { type Viewer, viewerFromRequest } from "./session"

export const MAX_IMPORT_BYTES = 512 * 1024 * 1024

export type TransferDeps = {
  viewer: (request: Request) => Promise<Viewer | null>
  engine: (viewer: Viewer) => Pick<Client, "import" | "download">
}

const defaults: TransferDeps = { viewer: viewerFromRequest, engine: engineFor }

function fail(status: number, message: string): Response {
  return Response.json({ error: message }, { status, headers: { "cache-control": "no-store" } })
}

async function guard(request: Request, p: Permission, deps: TransferDeps): Promise<Viewer | Response> {
  if (crossOriginWrite(request)) return fail(403, "Invalid origin.")
  const viewer = await deps.viewer(request)
  if (!viewer) return fail(401, "Sign in again.")
  if (!can(viewer.role, p)) return fail(403, "Your role can't do that.")
  return viewer
}

const ref = (v: string | null): v is string => !!v && v.length <= 100

function engineFailure(e: unknown): Response {
  const status = e instanceof LumbungError && e.status >= 400 && e.status < 600 ? e.status : 502
  return fail(status, userMessage(e))
}

export const IMPORT_STALL_MS = 30_000

type Read = { bytes: Uint8Array } | { fail: "too-big" | "stalled" | "aborted" }

/**
 * Reads the body up to the cap, so a huge upload stops early instead of
 * filling memory, and gives up when no bytes arrive for `stallMs`.
 */
async function readCapped(request: Request, cap: number, stallMs: number): Promise<Read> {
  if (!request.body) return { bytes: new Uint8Array() }
  const chunks: Uint8Array[] = []
  let size = 0
  const reader = request.body.getReader()
  const stop = (fail: "too-big" | "stalled" | "aborted"): Read => {
    reader.cancel().catch(() => {})
    return { fail }
  }
  for (;;) {
    let timer: ReturnType<typeof setTimeout> | undefined
    const stalled = new Promise<"stalled">((resolve) => (timer = setTimeout(() => resolve("stalled"), stallMs)))
    let step: ReadableStreamReadResult<Uint8Array> | "stalled"
    try {
      step = await Promise.race([reader.read(), stalled])
    } catch {
      return stop("aborted")
    } finally {
      clearTimeout(timer)
    }
    if (step === "stalled") return stop("stalled")
    if (step.done) break
    size += step.value.byteLength
    if (size > cap) return stop("too-big")
    chunks.push(step.value)
  }
  const out = new Uint8Array(size)
  let at = 0
  for (const c of chunks) {
    out.set(c, at)
    at += c.byteLength
  }
  return { bytes: out }
}

let importing = false

export async function importRoute(request: Request, deps: TransferDeps = defaults, cap = MAX_IMPORT_BYTES, stallMs = IMPORT_STALL_MS): Promise<Response> {
  const viewer = await guard(request, "write", deps)
  if (viewer instanceof Response) return viewer
  const q = new URL(request.url).searchParams
  const db = q.get("db")
  const branch = q.get("branch")
  const format = q.get("format")
  const mode = q.get("mode") ?? "empty"
  if (!ref(db) || !ref(branch)) return fail(400, "Pick a database and a branch.")
  if (format !== "db" && format !== "sql") return fail(400, "The file must be a .db or .sql file.")
  if (mode !== "empty" && mode !== "replace") return fail(400, "Mode must be empty or replace.")
  const tooBig = `The file is over ${Math.round(cap / 1024 / 1024)} MB.`
  if (Number(request.headers.get("content-length")) > cap) return fail(413, tooBig)
  // Each import buffers up to the cap, so only one runs at a time.
  if (importing) return fail(429, "Another import is running. Try again soon.")
  importing = true
  try {
    const read = await readCapped(request, cap, stallMs)
    if ("fail" in read) {
      if (read.fail === "too-big") return fail(413, tooBig)
      if (read.fail === "stalled") return fail(408, "The upload stalled. Try again.")
      return fail(400, "The upload was cut off. Try again.")
    }
    const body = read.bytes
    if (body.byteLength === 0) return fail(400, "The file is empty.")
    const data = format === "sql" ? new TextDecoder().decode(body) : body
    const out = await deps.engine(viewer).import(db, branch, data, { format, mode })
    return Response.json(out)
  } catch (e) {
    return engineFailure(e)
  } finally {
    importing = false
  }
}

export async function exportRoute(request: Request, deps: TransferDeps = defaults): Promise<Response> {
  const viewer = await guard(request, "read", deps)
  if (viewer instanceof Response) return viewer
  const q = new URL(request.url).searchParams
  const db = q.get("db")
  const branch = q.get("branch")
  const format = q.get("format") ?? "db"
  if (!ref(db) || !ref(branch)) return fail(400, "Pick a database and a branch.")
  if (format !== "db" && format !== "sql") return fail(400, "Format must be db or sql.")
  let res: Response
  try {
    res = await deps.engine(viewer).download(db, branch, format)
  } catch (e) {
    return engineFailure(e)
  }
  const safe = (s: string) => s.replace(/[^a-zA-Z0-9._-]/g, "_")
  const filename = `${safe(db)}-${safe(branch)}.${format === "sql" ? "sql" : "db"}`
  const headers = new Headers({
    "content-type": format === "sql" ? "application/sql; charset=utf-8" : "application/vnd.sqlite3",
    "content-disposition": `attachment; filename="${filename}"`,
    "cache-control": "no-store",
    "x-content-type-options": "nosniff",
  })
  const length = res.headers.get("content-length")
  if (length) headers.set("content-length", length)
  return new Response(res.body, { status: 200, headers })
}
