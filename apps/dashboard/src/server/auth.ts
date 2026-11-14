// Better Auth for the dashboard: invite-only email and password plus
// passkeys, with a role on each user. Nobody signs up through the API. The
// owner comes from first-run setup and everyone else from an invite, both of
// which create the user on the server (see users.ts).
import { type BlockList, isIP } from "node:net"
import { passkey } from "@better-auth/passkey"
import { betterAuth } from "better-auth"
import { getMigrations } from "better-auth/db/migration"
import { tanstackStartCookies } from "better-auth/tanstack-start"
import { AUTH_BASE } from "@/lib/paths"
import { type DashboardConfig, authSecretValue, config } from "./config"
import { getDb, migrateOwn } from "./db"

export const COOKIE_PREFIX = "lumbung"
const CLIENT_IP_HEADER = "x-lumbung-client-ip"

function create(origin: string) {
  const url = new URL(origin)
  // Passkeys always belong to the dashboard URL. On an extra origin the
  // passkey endpoints are refused before they get here (see handleAuth).
  const main = new URL(config().publicUrl)
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
    plugins: [passkey({ rpID: main.hostname, rpName: "Lumbung", origin: main.origin }), tanstackStartCookies()],
  })
}

export type Auth = ReturnType<typeof create>
export type SessionUser = Auth["$Infer"]["Session"]["user"]

// One instance per origin, since Better Auth fixes the cookie names and the
// Secure flag per instance: an http extra origin can't use the __Secure-
// cookie the https dashboard URL sets. They all share one database, and the
// memory rate limiter is module-wide, keyed by client address and path.
const instances = new Map<string, Auth>()
let ready: Promise<void> | undefined

function instanceFor(origin: string): Auth {
  let auth = instances.get(origin)
  if (!auth) {
    auth = create(origin)
    instances.set(origin, auth)
  }
  return auth
}

/**
 * The auth instance for the origin a request was sent to (the dashboard URL
 * when there's no request), after its tables exist. Migrations run once per
 * process.
 */
export async function getAuth(request?: Request): Promise<Auth> {
  ready ??= (async () => {
    const { runMigrations } = await getMigrations(instanceFor(config().publicUrl).options)
    await runMigrations()
    migrateOwn(getDb())
  })().catch((e) => {
    ready = undefined
    throw e
  })
  await ready
  return instanceFor(request ? requestOrigin(request) : config().publicUrl)
}

/** Only for tests. */
export function resetAuth() {
  instances.clear()
  ready = undefined
}

/**
 * The origin a request was sent to, by its Host header: the extra origin with
 * that host, otherwise the dashboard URL. X-Forwarded-Host is never read.
 */
export function requestOrigin(request: Request): string {
  const cfg = config()
  if (cfg.extraOrigins.length === 0) return cfg.publicUrl
  let host = request.headers.get("host")
  if (!host) {
    try {
      host = new URL(request.url).host
    } catch {
      return cfg.publicUrl
    }
  }
  host = host.toLowerCase()
  return cfg.extraOrigins.find((o) => new URL(o).host === host) ?? cfg.publicUrl
}

/** Passkeys are bound to the dashboard URL, so they only work on a request sent there. */
export function passkeysAvailable(request: Request): boolean {
  return requestOrigin(request) === config().publicUrl
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

/** The endpoint under AUTH_BASE, normalized, or null for a path outside it. */
function authEndpoint(request: Request): string | null {
  let path = new URL(request.url).pathname
  try {
    path = decodeURIComponent(path)
  } catch {
    return null
  }
  path = path.replace(/\/{2,}/g, "/").toLowerCase()
  if (!path.startsWith(`${AUTH_BASE}/`)) return null
  return path.slice(AUTH_BASE.length).replace(/\/$/, "")
}

export function blockedAuthPath(request: Request): boolean {
  const rest = authEndpoint(request)
  return rest === null || !ALLOWED.some((re) => re.test(rest))
}

/** True for a write from another site, or from an origin other than the one it was sent to. Better Auth, server functions and the transfer routes all use this one check. */
export function crossOriginWrite(request: Request): boolean {
  if (request.method === "GET" || request.method === "HEAD" || request.method === "OPTIONS") return false
  if (request.headers.get("sec-fetch-site") === "cross-site") return true
  const origin = request.headers.get("origin")
  return !!origin && origin !== requestOrigin(request)
}

/** The socket address, as the Node server (srvx) puts it on the request. Same source as Start's getRequestIP. */
function socketAddress(request: Request): string | undefined {
  const r = request as Request & { ip?: string; context?: { clientAddress?: string } }
  return r.ip || r.context?.clientAddress || undefined
}

// An IPv4 client on a dual-stack socket shows up as ::ffff:a.b.c.d.
function plainIp(value: string): string | null {
  const v = value.trim()
  const mapped = /^::ffff:(\d+\.\d+\.\d+\.\d+)$/i.exec(v)?.[1]
  if (mapped && isIP(mapped) === 4) return mapped
  return isIP(v) ? v : null
}

function trusted(proxies: BlockList, ip: string | null): boolean {
  if (!ip) return false
  return proxies.check(ip, isIP(ip) === 4 ? "ipv4" : "ipv6")
}

type ClientIpSettings = Pick<DashboardConfig, "clientIpHeader" | "trustedProxies">

/**
 * The client address for rate limits, with the engine's rules: the socket
 * address, unless the connection comes from a trusted proxy and the client IP
 * header is set. A list (X-Forwarded-For) is read from the right, skipping
 * trusted proxies, since the left end is whatever the client sent.
 */
export function clientIp(request: Request, settings: ClientIpSettings = config()): string {
  const raw = socketAddress(request)
  const host = (raw && plainIp(raw)) || raw || "local"
  if (!settings.clientIpHeader || !trusted(settings.trustedProxies, plainIp(host))) return host
  const parts = (request.headers.get(settings.clientIpHeader) ?? "").split(",")
  for (let i = parts.length - 1; i >= 0; i--) {
    const ip = plainIp(parts[i]!)
    if (!ip) return host
    if (!trusted(settings.trustedProxies, ip)) return ip
  }
  return host
}

async function withClientIp(request: Request): Promise<Request> {
  const headers = new Headers(request.headers)
  headers.set(CLIENT_IP_HEADER, clientIp(request))
  const hasBody = request.method !== "GET" && request.method !== "HEAD"
  return new Request(request.url, { method: request.method, headers, body: hasBody ? await request.arrayBuffer() : undefined })
}

export async function handleAuth(request: Request): Promise<Response> {
  if (blockedAuthPath(request)) return Response.json({ code: "NOT_FOUND", message: "Not found" }, { status: 404 })
  if (crossOriginWrite(request)) return Response.json({ code: "INVALID_ORIGIN", message: "Invalid origin" }, { status: 403 })
  if (authEndpoint(request)?.startsWith("/passkey/") && !passkeysAvailable(request)) {
    const message = `Passkeys only work on ${config().publicUrl}. Sign in with your password here.`
    return Response.json({ code: "PASSKEY_UNAVAILABLE", message }, { status: 403 })
  }
  const auth = await getAuth(request)
  return auth.handler(await withClientIp(request))
}

/** Headers for a server-side auth call made on behalf of the current request. */
export function authHeaders(request: Request): Headers {
  const headers = new Headers(request.headers)
  headers.set(CLIENT_IP_HEADER, clientIp(request))
  return headers
}
