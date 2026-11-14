import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest"
import type { Role } from "@/lib/roles"
import { resetConfig } from "./config"
import { type Actor, accessHeaders, engineFor, engineIn, serviceClient } from "./engine"

const TOKEN = "lmb_service_testsecret"
const WS = "ws_bbbbbbbbbbbbbbbbbbbb"

const ana: Actor = { email: "ana@example.com", owner: true }
const ben: Actor = { email: "ben@example.com", owner: false }

beforeEach(() => {
  // Nothing here opens the dashboard's database, so the folder is never made.
  process.env.LUMBUNG_DASHBOARD_DATA_DIR = join(tmpdir(), "lumbung-dashboard-engine-test")
  process.env.LUMBUNG_AUTH_SECRET = "test-secret-that-is-long-enough-for-auth"
  process.env.LUMBUNG_DASHBOARD_URL = "http://localhost:4871"
  process.env.LUMBUNG_ENGINE_URL = "http://engine.test:4870"
  process.env.LUMBUNG_SERVICE_TOKEN = TOKEN
  resetConfig()
})

afterEach(() => {
  vi.unstubAllGlobals()
  resetConfig()
})

describe("accessHeaders", () => {
  test("an owner is only named: the service token's own reach applies", () => {
    expect(accessHeaders(ana, "owner")).toEqual({ "X-Lumbung-Actor": "dashboard:ana@example.com" })
  })

  test.each<[Role, string]>([
    ["admin", "admin"],
    ["member", "write"],
    ["readonly", "read"],
  ])("a non-owner with role %s is sent at level %s", (role, level) => {
    expect(accessHeaders(ben, role)).toEqual({ "X-Lumbung-Actor": "dashboard:ben@example.com", "X-Lumbung-Actor-Access": level })
  })

  test("a non-owner passed the owner role fails closed to read", () => {
    expect(accessHeaders(ben, "owner")).toEqual({ "X-Lumbung-Actor": "dashboard:ben@example.com", "X-Lumbung-Actor-Access": "read" })
  })

  test.each<[Role, string]>([
    ["admin", "admin"],
    ["member", "write"],
    ["readonly", "read"],
  ])("an owner passed the lower role %s is held to level %s", (role, level) => {
    expect(accessHeaders(ana, role)).toEqual({ "X-Lumbung-Actor": "dashboard:ana@example.com", "X-Lumbung-Actor-Access": level })
  })
})

// What the SDK client really puts on the wire.
function captureFetch(body: unknown = { data: [], next_cursor: "" }) {
  const calls: { url: URL; method: string; headers: Headers }[] = []
  vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: new URL(String(input)), method: init?.method ?? "GET", headers: new Headers(init?.headers) })
    return Response.json(body)
  })
  return calls
}

describe("engineIn", () => {
  test("names the workspace in the path and the person's level in the headers", async () => {
    const calls = captureFetch()
    await engineIn(ben, WS, "member").databases.list()
    expect(calls).toHaveLength(1)
    const { url, method, headers } = calls[0]!
    expect(method).toBe("GET")
    expect(url.origin).toBe("http://engine.test:4870")
    expect(url.pathname).toBe(`/v1/workspaces/${WS}/databases`)
    expect(headers.get("authorization")).toBe(`Bearer ${TOKEN}`)
    expect(headers.get("x-lumbung-actor")).toBe("dashboard:ben@example.com")
    expect(headers.get("x-lumbung-actor-access")).toBe("write")
  })

  test.each<[Role, string]>([
    ["admin", "admin"],
    ["member", "write"],
    ["readonly", "read"],
    ["owner", "read"],
  ])("a non-owner as %s reaches the engine at %s, on writes too", async (role, level) => {
    const calls = captureFetch({ id: "db_1", name: "shop" })
    await engineIn(ben, WS, role).databases.create("shop")
    expect(calls[0]!.method).toBe("POST")
    expect(calls[0]!.url.pathname).toBe(`/v1/workspaces/${WS}/databases`)
    expect(calls[0]!.headers.get("x-lumbung-actor-access")).toBe(level)
  })

  test("an owner's requests name the workspace and carry no level", async () => {
    const calls = captureFetch()
    await engineIn(ana, WS, "owner").buckets.list()
    expect(calls[0]!.url.pathname).toBe(`/v1/workspaces/${WS}/buckets`)
    expect(calls[0]!.headers.get("x-lumbung-actor")).toBe("dashboard:ana@example.com")
    expect(calls[0]!.headers.has("x-lumbung-actor-access")).toBe(false)
  })

  test("object requests carry the workspace and the level too", async () => {
    const calls = captureFetch({ key: "a.txt", size: 2 })
    await engineIn(ben, WS, "readonly").bucket("photos").put("a.txt", "hi")
    expect(calls[0]!.url.pathname).toBe(`/v1/workspaces/${WS}/buckets/photos/object`)
    expect(calls[0]!.headers.get("x-lumbung-actor")).toBe("dashboard:ben@example.com")
    expect(calls[0]!.headers.get("x-lumbung-actor-access")).toBe("read")
  })

  test("refuses a workspace it can't put in a path", () => {
    captureFetch()
    expect(() => engineIn(ben, "..", "member")).toThrow()
    expect(() => engineIn(ben, "", "member")).toThrow()
    expect(() => engineIn(ben, `${WS}/../ws_aaaaaaaaaaaaaaaaaaaa`, "member")).toThrow()
  })
})

describe("engineFor", () => {
  test("throws for anyone but an owner, before any request", () => {
    const calls = captureFetch()
    expect(() => engineFor(ben)).toThrow("only an owner acts on the instance")
    expect(calls).toHaveLength(0)
  })

  test("an owner acts on the instance by name, with no workspace and no level", async () => {
    const calls = captureFetch()
    await engineFor(ana).workspaces.list()
    expect(calls[0]!.url.pathname).toBe("/v1/workspaces")
    expect(calls[0]!.headers.get("authorization")).toBe(`Bearer ${TOKEN}`)
    expect(calls[0]!.headers.get("x-lumbung-actor")).toBe("dashboard:ana@example.com")
    expect(calls[0]!.headers.has("x-lumbung-actor-access")).toBe(false)
  })
})

describe("serviceClient", () => {
  test("names nobody", async () => {
    const calls = captureFetch()
    await serviceClient().workspaces.list()
    expect(calls[0]!.url.pathname).toBe("/v1/workspaces")
    expect(calls[0]!.headers.get("authorization")).toBe(`Bearer ${TOKEN}`)
    expect(calls[0]!.headers.has("x-lumbung-actor")).toBe(false)
    expect(calls[0]!.headers.has("x-lumbung-actor-access")).toBe(false)
  })

  test("needs a token that looks like one", () => {
    process.env.LUMBUNG_SERVICE_TOKEN = "not-a-token"
    resetConfig()
    expect(() => serviceClient()).toThrow("isn't a Lumbung token")
  })
})
