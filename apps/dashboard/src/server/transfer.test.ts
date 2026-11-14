import { LumbungError } from "@lumbung/client"
import { describe, expect, test, vi } from "vitest"
import { type Permission, type Role, can } from "@/lib/roles"
import { AccessError, type Viewer, denied } from "./session"
import { type TransferDeps, exportRoute, importRoute } from "./transfer"

const ORIGIN = "http://localhost:4871"
// Requests name the workspace as the browser does, by name.
const WS_NAME = "agents"
const WS = "ws_bbbbbbbbbbbbbbbbbbbb"

type Engine = Awaited<ReturnType<TransferDeps["access"]>>["engine"]

// `access` answers like accessTo: a 404 for a workspace that isn't theirs, a 403 for a role that is too low.
function deps(role: Role | null, engine: Partial<Engine> = {}) {
  const client = {
    import: vi.fn(async () => ({ name: "main" })),
    download: vi.fn(async () => new Response("SQLite format 3\0", { headers: { "content-length": "16" } })),
    ...engine,
  }
  const access = vi.fn(async (_viewer: Viewer, ws: string, p: Permission) => {
    if (!role || (ws !== WS_NAME && ws !== WS)) throw new AccessError("That workspace doesn't exist.", 404)
    if (!can(role, p)) throw new AccessError(denied(p), 403)
    return { engine: client as never }
  })
  const d: TransferDeps = {
    viewer: async () => (role ? { id: "u1", name: "Ann", email: "ann@example.com", owner: role === "owner" } : null),
    access,
  }
  return { d, client, access }
}

function upload(query: string, body: BodyInit | null = "CREATE TABLE t (a);", headers: Record<string, string> = {}) {
  return new Request(`${ORIGIN}/app/api/import?${query}`, { method: "POST", body, headers: { origin: ORIGIN, ...headers } })
}

const ok = "ws=agents&db=shop&branch=main&format=sql&mode=empty"

describe("import", () => {
  test("needs a session", async () => {
    const { d, client } = deps(null)
    expect((await importRoute(upload(ok), d)).status).toBe(401)
    expect(client.import).not.toHaveBeenCalled()
  })

  test("a read-only member can't import", async () => {
    const { d, client } = deps("readonly")
    const res = await importRoute(upload(ok), d)
    expect(res.status).toBe(403)
    expect(client.import).not.toHaveBeenCalled()
  })

  test.each<Record<string, string>>([
    { origin: "https://evil.example" },
    { "sec-fetch-site": "cross-site" },
  ])("refuses a request from another site %o", async (headers) => {
    const { d, client } = deps("owner")
    const res = await importRoute(upload(ok, "x", headers), d)
    expect(res.status).toBe(403)
    expect(client.import).not.toHaveBeenCalled()
  })

  test.each(["ws=agents&db=shop&format=sql", "ws=agents&branch=main&format=sql", "ws=agents&db=shop&branch=main&format=csv", "ws=agents&db=shop&branch=main&format=db&mode=merge"])(
    "rejects bad parameters %s",
    async (query) => {
      const { d, client, access } = deps("member")
      expect((await importRoute(upload(query), d)).status).toBe(400)
      // The workspace was fine: the refusal is about the parameter.
      expect(access).toHaveBeenCalledOnce()
      expect(client.import).not.toHaveBeenCalled()
    },
  )

  test("rejects an empty file", async () => {
    const { d } = deps("member")
    expect((await importRoute(upload(ok, ""), d)).status).toBe(400)
  })

  test("stops a body over the cap, by header and while streaming", async () => {
    const { d, client } = deps("member")
    const declared = await importRoute(upload(ok, "x", { "content-length": String(600 * 1024 * 1024) }), d)
    expect(declared.status).toBe(413)
    expect((await declared.json()).error).toBe("The file is over 512 MB.")

    const stream = new ReadableStream({
      start(c) {
        c.enqueue(new Uint8Array(600))
        c.enqueue(new Uint8Array(600))
        c.close()
      },
    })
    const streamed = await importRoute(new Request(`${ORIGIN}/app/api/import?${ok}`, { method: "POST", body: stream, duplex: "half", headers: { origin: ORIGIN } } as RequestInit), d, 1000)
    expect(streamed.status).toBe(413)
    expect(client.import).not.toHaveBeenCalled()
  })

  test("passes the file to the engine", async () => {
    const { d, client } = deps("member")
    const res = await importRoute(upload("ws=agents&db=shop&branch=dev&format=sql&mode=replace"), d)
    expect(res.status).toBe(200)
    expect(client.import).toHaveBeenCalledWith("shop", "dev", "CREATE TABLE t (a);", { format: "sql", mode: "replace" })

    await importRoute(upload("ws=agents&db=shop&branch=dev&format=db", new Uint8Array([1, 2, 3])), d)
    expect(client.import).toHaveBeenLastCalledWith("shop", "dev", new Uint8Array([1, 2, 3]), { format: "db", mode: "empty" })
  })

  test("runs one import at a time", async () => {
    let finish!: () => void
    const gate = new Promise<void>((r) => (finish = r))
    const { d, client } = deps("member", {
      import: vi.fn(async () => {
        await gate
        return { name: "main" } as never
      }),
    })
    const first = importRoute(upload(ok), d)
    await vi.waitFor(() => expect(client.import).toHaveBeenCalledTimes(1))
    const busy = await importRoute(upload(ok), d)
    expect(busy.status).toBe(429)
    expect((await busy.json()).error).toBe("Another import is running. Try again soon.")
    finish()
    expect((await first).status).toBe(200)
    expect((await importRoute(upload(ok), d)).status).toBe(200)
  })

  test("a stalled upload times out with 408 and frees the lock", async () => {
    const { d, client } = deps("member")
    const stalled = new ReadableStream({
      start(c) {
        c.enqueue(new Uint8Array(10))
      },
    })
    const req = new Request(`${ORIGIN}/app/api/import?${ok}`, { method: "POST", body: stalled, duplex: "half", headers: { origin: ORIGIN } } as RequestInit)
    const res = await importRoute(req, d, 1000, 20)
    expect(res.status).toBe(408)
    expect((await res.json()).error).toBe("The upload stalled. Try again.")
    expect(client.import).not.toHaveBeenCalled()
    expect((await importRoute(upload(ok), d)).status).toBe(200)
  })

  test("an upload the client cut off is a 400, not an engine error", async () => {
    const { d, client } = deps("member")
    const broken = new ReadableStream({
      start(c) {
        c.enqueue(new Uint8Array(10))
        c.error(new Error("aborted"))
      },
    })
    const req = new Request(`${ORIGIN}/app/api/import?${ok}`, { method: "POST", body: broken, duplex: "half", headers: { origin: ORIGIN } } as RequestInit)
    const res = await importRoute(req, d)
    expect(res.status).toBe(400)
    expect((await res.json()).error).toBe("The upload was cut off. Try again.")
    expect(client.import).not.toHaveBeenCalled()
    expect((await importRoute(upload(ok), d)).status).toBe(200)
  })

  test("shows the engine's error with its status", async () => {
    const { d } = deps("owner", {
      import: vi.fn(async () => {
        throw new LumbungError("not_empty", 409, "branch isn't empty", { detail: "The branch already has tables." })
      }),
    })
    const res = await importRoute(upload(ok), d)
    expect(res.status).toBe(409)
    expect((await res.json()).error).toBe("The branch already has tables.")
  })
})

describe("export", () => {
  const get = (query: string) => new Request(`${ORIGIN}/app/api/export?${query}`)

  test("needs a session", async () => {
    const { d, client } = deps(null)
    expect((await exportRoute(get("ws=agents&db=shop&branch=main"), d)).status).toBe(401)
    expect(client.download).not.toHaveBeenCalled()
  })

  test("a read-only member can download, with a filename", async () => {
    const { d, client } = deps("readonly")
    const res = await exportRoute(get("ws=agents&db=shop&branch=main"), d)
    expect(res.status).toBe(200)
    expect(client.download).toHaveBeenCalledWith("shop", "main", "db")
    expect(res.headers.get("content-disposition")).toBe('attachment; filename="shop-main.db"')
    expect(res.headers.get("content-length")).toBe("16")
    expect(await res.text()).toBe("SQLite format 3\0")
  })

  test("names a SQL dump .sql and keeps odd characters out of the filename", async () => {
    const { d } = deps("member")
    const res = await exportRoute(get(`ws=agents&db=${encodeURIComponent('a"b')}&branch=main&format=sql`), d)
    expect(res.headers.get("content-disposition")).toBe('attachment; filename="a_b-main.sql"')
    expect(res.headers.get("content-type")).toContain("application/sql")
  })

  test("rejects an unknown format", async () => {
    const { d } = deps("member")
    expect((await exportRoute(get("ws=agents&db=shop&branch=main&format=csv"), d)).status).toBe(400)
  })

  test("passes the engine's missing branch through", async () => {
    const { d } = deps("member", {
      download: vi.fn(async () => {
        throw new LumbungError("branch_not_found", 404, "branch not found")
      }),
    })
    expect((await exportRoute(get("ws=agents&db=shop&branch=nope"), d)).status).toBe(404)
  })
})

describe("the workspace", () => {
  const rest = "db=shop&branch=main&format=sql&mode=empty"
  const requests: [string, Permission, (d: TransferDeps, q: string) => Promise<Response>][] = [
    ["import", "write", (d, q) => importRoute(upload(`${q}${rest}`), d)],
    ["export", "read", (d, q) => exportRoute(new Request(`${ORIGIN}/app/api/export?${q}db=shop&branch=main`), d)],
  ]

  test.each(requests)("%s: a request that names no workspace is a 400", async (_, _p, send) => {
    for (const q of ["", "ws=&", `ws=${"w".repeat(101)}&`]) {
      const { d, client, access } = deps("owner")
      const res = await send(d, q)
      expect(res.status, q).toBe(400)
      expect(await res.json()).toEqual({ error: "Pick a workspace." })
      expect(access).not.toHaveBeenCalled()
      expect(client.import).not.toHaveBeenCalled()
      expect(client.download).not.toHaveBeenCalled()
    }
  })

  test.each(requests)("%s: a workspace the person can't see is a 404, and nothing reaches the engine", async (_, p, send) => {
    for (const ws of ["staging", "ws_cccccccccccccccccccc"]) {
      const { d, client, access } = deps("admin")
      const res = await send(d, `ws=${ws}&`)
      expect(res.status, ws).toBe(404)
      expect(await res.json()).toEqual({ error: "That workspace doesn't exist." })
      expect(res.headers.get("cache-control")).toBe("no-store")
      expect(access).toHaveBeenCalledOnce()
      expect(access.mock.calls[0]!.slice(1)).toEqual([ws, p])
      expect(client.import).not.toHaveBeenCalled()
      expect(client.download).not.toHaveBeenCalled()
    }
  })

  test("a role that is too low is a 403, and nothing reaches the engine", async () => {
    const { d, client, access } = deps("readonly")
    const res = await importRoute(upload(ok), d)
    expect(res.status).toBe(403)
    expect(await res.json()).toEqual({ error: "Your role can't make changes." })
    expect(access.mock.calls[0]!.slice(1)).toEqual([WS_NAME, "write"])
    expect(client.import).not.toHaveBeenCalled()
  })

  test.each(requests)("%s: the status and message of an AccessError come back as they are", async (_, _p, send) => {
    for (const [status, message] of [
      [404, "That workspace doesn't exist."],
      [403, "Only an admin of this workspace can do that."],
      [401, "Sign in again."],
    ] as const) {
      const { d, client } = deps("owner")
      d.access = async () => {
        throw new AccessError(message, status)
      }
      const res = await send(d, `ws=${WS_NAME}&`)
      expect(res.status).toBe(status)
      expect(await res.json()).toEqual({ error: message })
      expect(client.import).not.toHaveBeenCalled()
      expect(client.download).not.toHaveBeenCalled()
    }
  })

  test.each(requests)("%s: a failure that isn't about access isn't answered as one", async (_, _p, send) => {
    const { d, client } = deps("owner")
    d.access = async () => {
      throw new Error("engine down")
    }
    await expect(send(d, `ws=${WS_NAME}&`)).rejects.toThrow("engine down")
    expect(client.import).not.toHaveBeenCalled()
    expect(client.download).not.toHaveBeenCalled()
  })

  test.each(requests)("%s: the workspace can be named by id, and access is asked as the signed-in person", async (_, p, send) => {
    const { d, client, access } = deps("member")
    expect((await send(d, `ws=${WS}&`)).status).toBe(200)
    expect(access).toHaveBeenCalledWith({ id: "u1", name: "Ann", email: "ann@example.com", owner: false }, WS, p)
    expect(client.import).toHaveBeenCalledTimes(p === "write" ? 1 : 0)
    expect(client.download).toHaveBeenCalledTimes(p === "read" ? 1 : 0)
  })

  test("a refused import doesn't hold the one-at-a-time lock", async () => {
    const none = deps("admin")
    expect((await importRoute(upload(`ws=staging&${rest}`), none.d)).status).toBe(404)
    const { d } = deps("member")
    expect((await importRoute(upload(ok), d)).status).toBe(200)
  })
})
