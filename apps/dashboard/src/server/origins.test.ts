// Signing in from LUMBUNG_DASHBOARD_EXTRA_ORIGINS, against a real Better Auth
// instance and database.
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, test } from "vitest"
import { AUTH_BASE } from "@/lib/paths"
import { crossOriginWrite, getAuth, handleAuth, passkeysAvailable, requestOrigin, resetAuth } from "./auth"
import { config, parseOrigins, resetConfig } from "./config"
import { getDb, setDb } from "./db"
import { createOwner } from "./users"

const MAIN = "https://lumbung.example.com"
const LOOPBACK = "http://127.0.0.1:4806"
const LOCALHOST = "http://localhost:4806"
const email = "owner@example.com"
const password = "correct horse battery"

let dir = ""

function setUp(extra: string | undefined) {
  process.env.LUMBUNG_DASHBOARD_URL = MAIN
  if (extra === undefined) delete process.env.LUMBUNG_DASHBOARD_EXTRA_ORIGINS
  else process.env.LUMBUNG_DASHBOARD_EXTRA_ORIGINS = extra
  resetConfig()
  resetAuth()
}

beforeEach(async () => {
  dir = mkdtempSync(join(tmpdir(), "lumbung-dashboard-"))
  process.env.LUMBUNG_DASHBOARD_DATA_DIR = dir
  process.env.LUMBUNG_AUTH_SECRET = "test-secret-that-is-long-enough-for-auth"
  setDb(undefined)
})

afterEach(() => {
  delete process.env.LUMBUNG_DASHBOARD_EXTRA_ORIGINS
  resetConfig()
  getDb().close()
  setDb(undefined)
  resetAuth()
  rmSync(dir, { recursive: true, force: true })
})

/** A request sent to `host` (scheme, host and port) from a page on `origin`. */
function authRequest(host: string, path: string, origin: string | null, init: RequestInit = {}) {
  const headers = new Headers(init.headers)
  if (origin) headers.set("origin", origin)
  return new Request(`${host}${AUTH_BASE}${path}`, { ...init, headers })
}

function signIn(host: string, origin: string | null = host) {
  return handleAuth(
    authRequest(host, "/sign-in/email", origin, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ email, password }),
    }),
  )
}

function sessionCookie(res: Response) {
  const cookie = res.headers.getSetCookie().find((c) => /session_token=/.test(c))
  expect(cookie, "no session cookie").toBeDefined()
  return cookie!
}

describe("without LUMBUNG_DASHBOARD_EXTRA_ORIGINS", () => {
  beforeEach(async () => {
    setUp(undefined)
    await createOwner({ email, name: "Ana", password })
  })

  test("signs in on the dashboard URL as before", async () => {
    const res = await signIn(MAIN)
    expect(res.status).toBe(200)
    const cookie = sessionCookie(res)
    expect(cookie).toMatch(/^__Secure-lumbung\.session_token=/)
    expect(cookie).toMatch(/; Secure/i)
  })

  test.each([LOOPBACK, LOCALHOST, "https://evil.example"])("refuses a sign-in from %s", async (origin) => {
    expect((await signIn(MAIN, origin)).status).toBe(403)
  })

  test("a loopback host still gets the dashboard URL's origin check", async () => {
    expect((await signIn(LOOPBACK)).status).toBe(403)
    expect(requestOrigin(new Request(`${LOOPBACK}/app`))).toBe(MAIN)
    expect(passkeysAvailable(new Request(`${LOOPBACK}/app`))).toBe(true)
  })
})

describe("with LUMBUNG_DASHBOARD_EXTRA_ORIGINS", () => {
  beforeEach(async () => {
    setUp(`${LOOPBACK}, ${LOCALHOST}/`)
    await createOwner({ email, name: "Ana", password })
  })

  test("parses the list", () => {
    expect(config().extraOrigins).toEqual([LOOPBACK, LOCALHOST])
  })

  test.each([LOOPBACK, LOCALHOST])("signs in on %s with a plain http cookie", async (host) => {
    const res = await signIn(host)
    expect(res.status).toBe(200)
    const cookie = sessionCookie(res)
    expect(cookie).toMatch(/^lumbung\.session_token=/)
    expect(cookie).not.toMatch(/; Secure/i)
    expect(cookie).not.toMatch(/; Domain=/i)

    // The cookie signs the next request in, on the same host.
    const token = cookie.split(";")[0]!
    const session = await handleAuth(authRequest(host, "/get-session", null, { headers: { cookie: token } }))
    expect(session.status).toBe(200)
    expect(((await session.json()) as { user?: { email: string } } | null)?.user?.email).toBe(email)
  })

  test("matches the origin by the Host header", async () => {
    const req = new Request(`http://dashboard:4871${AUTH_BASE}/get-session`, { headers: { host: "127.0.0.1:4806" } })
    expect(requestOrigin(req)).toBe(LOOPBACK)
    const viewer = await getAuth(req)
    expect(viewer.options.baseURL).toBe(LOOPBACK)
  })

  test("the dashboard URL keeps its Secure cookie and passkeys", async () => {
    const res = await signIn(MAIN)
    expect(res.status).toBe(200)
    expect(sessionCookie(res)).toMatch(/^__Secure-lumbung\.session_token=.*; Secure/i)
    const options = await handleAuth(authRequest(MAIN, "/passkey/generate-authenticate-options", null))
    expect(options.status).toBe(200)
    expect(((await options.json()) as { rpId?: string }).rpId).toBe("lumbung.example.com")
  })

  test.each([
    ["an unlisted origin", LOOPBACK, "https://evil.example"],
    ["another extra origin", LOOPBACK, LOCALHOST],
    ["the dashboard URL", LOCALHOST, MAIN],
    ["an extra origin on the dashboard URL's host", MAIN, LOOPBACK],
    ["an unlisted origin on an unlisted host", "http://192.168.1.5:4806", "http://192.168.1.5:4806"],
  ])("refuses a sign-in from %s", async (_, host, origin) => {
    const res = await signIn(host, origin)
    expect(res.status).toBe(403)
    expect(res.headers.getSetCookie()).toEqual([])
  })

  test("refuses a cross-site write even from a listed origin", async () => {
    const req = new Request(`${LOOPBACK}/app/api/import`, { method: "POST", headers: { origin: LOOPBACK, "sec-fetch-site": "cross-site" } })
    expect(crossOriginWrite(req)).toBe(true)
    const same = new Request(`${LOOPBACK}/app/api/import`, { method: "POST", headers: { origin: LOOPBACK } })
    expect(crossOriginWrite(same)).toBe(false)
  })

  test("passkeys are refused with a clear message on an extra origin", async () => {
    expect(passkeysAvailable(new Request(`${LOOPBACK}/app`))).toBe(false)
    for (const path of ["/passkey/generate-authenticate-options", "/passkey/list-user-passkeys"]) {
      const res = await handleAuth(authRequest(LOOPBACK, path, null))
      expect(res.status).toBe(403)
      expect(await res.json()).toEqual({ code: "PASSKEY_UNAVAILABLE", message: `Passkeys only work on ${MAIN}. Sign in with your password here.` })
    }
  })
})

describe("parseOrigins", () => {
  test("keeps scheme, host and port, and skips blanks", () => {
    expect(parseOrigins(" http://LOCALHOST:4806/ ,, https://backup.example.com:443", MAIN)).toEqual([
      "http://localhost:4806",
      "https://backup.example.com",
    ])
    expect(parseOrigins("", MAIN)).toEqual([])
  })

  test.each([
    ["localhost:4806", /isn't an http or https origin|isn't a URL/],
    ["not a url", /isn't a URL/],
    ["ftp://127.0.0.1", /isn't an http or https origin/],
    ["http://127.0.0.1:4806/app", /isn't an origin/],
    ["http://user:pw@127.0.0.1:4806", /isn't an origin/],
    ["http://lumbung.example.com", /repeats the host lumbung\.example\.com/],
    ["http://127.0.0.1:4806,https://127.0.0.1:4806", /repeats the host 127\.0\.0\.1:4806/],
  ])("rejects %s", (list, error) => {
    expect(() => parseOrigins(list, MAIN)).toThrow(error)
  })

  test("names the setting when it is wrong", () => {
    setUp("http://127.0.0.1:4806/app")
    expect(() => config()).toThrow(/^LUMBUNG_DASHBOARD_EXTRA_ORIGINS: /)
  })
})
