// Better Auth for the dashboard: invite-only email and password plus
// passkeys, with a role on each user. Nobody signs up through the API. The
// owner comes from first-run setup and everyone else from an invite, both of
// which create the user on the server (see users.ts).
import { passkey } from "@better-auth/passkey"
import { betterAuth } from "better-auth"
import { getMigrations } from "better-auth/db/migration"
import { tanstackStartCookies } from "better-auth/tanstack-start"
import { AUTH_BASE } from "@/lib/paths"
import { authSecretValue, config } from "./config"
import { getDb, migrateOwn } from "./db"

export const COOKIE_PREFIX = "lumbung"
const CLIENT_IP_HEADER = "x-lumbung-client-ip"

function create() {
  const url = new URL(config().publicUrl)
  return betterAuth({
    appName: "Lumbung",
    baseURL: url.origin,
    basePath: AUTH_BASE,
    secret: authSecretValue(),
    trustedOrigins: [url.origin],
    database: getDb(),
    emailAndPassword: { enabled: true, disableSignUp: true, minPasswordLength: 10, maxPasswordLength: 128 },
    user: {
      additionalFields: {
        role: { type: "string" as const, required: false, defaultValue: "member", input: false },
      },
    },
    session: { expiresIn: 30 * 24 * 60 * 60, updateAge: 24 * 60 * 60 },
    advanced: {
      cookiePrefix: COOKIE_PREFIX,
      useSecureCookies: url.protocol === "https:",
      defaultCookieAttributes: { httpOnly: true, sameSite: "lax" as const },
      ipAddress: { ipAddressHeaders: [CLIENT_IP_HEADER] },
    },
    rateLimit: {
      enabled: process.env.NODE_ENV !== "test",
      storage: "memory" as const,
      window: 60,
      max: 100,
      customRules: {
        "/sign-in/email": { window: 60, max: 5 },
        "/change-password": { window: 60, max: 5 },
        "/passkey/verify-authentication": { window: 60, max: 5 },
        "/passkey/generate-authenticate-options": { window: 60, max: 10 },
        "/passkey/verify-registration": { window: 60, max: 5 },
        "/passkey/generate-register-options": { window: 60, max: 10 },
      },
    },
    plugins: [passkey({ rpID: url.hostname, rpName: "Lumbung", origin: url.origin }), tanstackStartCookies()],
  })
}

export type Auth = ReturnType<typeof create>
export type SessionUser = Auth["$Infer"]["Session"]["user"]

let instance: Auth | undefined
let ready: Promise<Auth> | undefined

/** The auth instance, after its tables exist. Migrations run once per process. */
export function getAuth(): Promise<Auth> {
  ready ??= (async () => {
    const auth = create()
    const { runMigrations } = await getMigrations(auth.options)
    await runMigrations()
    migrateOwn(getDb())
    instance = auth
    return auth
  })().catch((e) => {
    ready = undefined
    throw e
  })
  return ready
}

/** Only for tests. */
export function resetAuth() {
  instance = undefined
  ready = undefined
}

export function authInstance(): Auth | undefined {
  return instance
}

// Endpoints the browser may reach. Everything else, sign-up included, is 404.
const ALLOWED = [
  /^\/get-session$/,
  /^\/sign-out$/,
  /^\/sign-in\/email$/,
  /^\/change-password$/,
  /^\/passkey\/(generate-register-options|verify-registration|generate-authenticate-options|verify-authentication|list-user-passkeys|delete-passkey|update-passkey)$/,
  /^\/ok$/,
  /^\/error$/,
]

export function blockedAuthPath(request: Request): boolean {
  let path = new URL(request.url).pathname
  try {
    path = decodeURIComponent(path)
  } catch {
    return true
  }
  path = path.replace(/\/{2,}/g, "/").toLowerCase()
  if (!path.startsWith(`${AUTH_BASE}/`)) return true
  const rest = path.slice(AUTH_BASE.length).replace(/\/$/, "")
  return !ALLOWED.some((re) => re.test(rest))
}

/** True for a write from another site. Better Auth checks origins too; this covers our own endpoints. */
export function crossOriginWrite(request: Request): boolean {
  if (request.method === "GET" || request.method === "HEAD" || request.method === "OPTIONS") return false
  if (request.headers.get("sec-fetch-site") === "cross-site") return true
  const origin = request.headers.get("origin")
  return !!origin && origin !== config().publicUrl
}

/** The client address, from the proxy headers we trust, copied into one header Better Auth reads. */
export function clientIp(headers: Headers): string {
  return (
    headers.get("cf-connecting-ip") ??
    headers.get("x-forwarded-for")?.split(",")[0]?.trim() ??
    headers.get("x-real-ip") ??
    "local"
  )
}

async function withClientIp(request: Request): Promise<Request> {
  const headers = new Headers(request.headers)
  headers.set(CLIENT_IP_HEADER, clientIp(request.headers))
  const hasBody = request.method !== "GET" && request.method !== "HEAD"
  return new Request(request.url, { method: request.method, headers, body: hasBody ? await request.arrayBuffer() : undefined })
}

export async function handleAuth(request: Request): Promise<Response> {
  if (blockedAuthPath(request)) return Response.json({ code: "NOT_FOUND", message: "Not found" }, { status: 404 })
  if (crossOriginWrite(request)) return Response.json({ code: "INVALID_ORIGIN", message: "Invalid origin" }, { status: 403 })
  const auth = await getAuth()
  return auth.handler(await withClientIp(request))
}

/** Headers for a server-side auth call made on behalf of the current request. */
export function authHeaders(request: Request): Headers {
  const headers = new Headers(request.headers)
  headers.set(CLIENT_IP_HEADER, clientIp(request.headers))
  return headers
}
