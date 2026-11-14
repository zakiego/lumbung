import { LumbungError } from "@lumbung/client"
import { describe, expect, test, vi } from "vitest"
import type { Role } from "@/lib/roles"
import { type TransferDeps, exportRoute, importRoute } from "./transfer"

const ORIGIN = "http://localhost:4871"

function deps(role: Role | null, engine: Partial<ReturnType<TransferDeps["engine"]>> = {}) {
  const client = {
    import: vi.fn(async () => ({ name: "main" })),
    download: vi.fn(async () => new Response("SQLite format 3\0", { headers: { "content-length": "16" } })),
    ...engine,
  }
  const d: TransferDeps = {
    viewer: async () => (role ? { id: "u1", name: "Ann", email: "ann@example.com", role } : null),
    engine: () => client as never,
  }
  return { d, client }
}

function upload(query: string, body: BodyInit | null = "CREATE TABLE t (a);", headers: Record<string, string> = {}) {
  return new Request(`${ORIGIN}/app/api/import?${query}`, { method: "POST", body, headers: { origin: ORIGIN, ...headers } })
}

const ok = "db=shop&branch=main&format=sql&mode=empty"

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

  test.each(["db=shop&format=sql", "branch=main&format=sql", "db=shop&branch=main&format=csv", "db=shop&branch=main&format=db&mode=merge"])(
    "rejects bad parameters %s",
    async (query) => {
      const { d } = deps("member")
      expect((await importRoute(upload(query), d)).status).toBe(400)
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
    const res = await importRoute(upload("db=shop&branch=dev&format=sql&mode=replace"), d)
    expect(res.status).toBe(200)
    expect(client.import).toHaveBeenCalledWith("shop", "dev", "CREATE TABLE t (a);", { format: "sql", mode: "replace" })

    await importRoute(upload("db=shop&branch=dev&format=db", new Uint8Array([1, 2, 3])), d)
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
    expect((await exportRoute(get("db=shop&branch=main"), d)).status).toBe(401)
    expect(client.download).not.toHaveBeenCalled()
  })

  test("a read-only member can download, with a filename", async () => {
    const { d, client } = deps("readonly")
    const res = await exportRoute(get("db=shop&branch=main"), d)
    expect(res.status).toBe(200)
    expect(client.download).toHaveBeenCalledWith("shop", "main", "db")
    expect(res.headers.get("content-disposition")).toBe('attachment; filename="shop-main.db"')
    expect(res.headers.get("content-length")).toBe("16")
    expect(await res.text()).toBe("SQLite format 3\0")
  })

  test("names a SQL dump .sql and keeps odd characters out of the filename", async () => {
    const { d } = deps("member")
    const res = await exportRoute(get(`db=${encodeURIComponent('a"b')}&branch=main&format=sql`), d)
    expect(res.headers.get("content-disposition")).toBe('attachment; filename="a_b-main.sql"')
    expect(res.headers.get("content-type")).toContain("application/sql")
  })

  test("rejects an unknown format", async () => {
    const { d } = deps("member")
    expect((await exportRoute(get("db=shop&branch=main&format=csv"), d)).status).toBe(400)
  })

  test("passes the engine's missing branch through", async () => {
    const { d } = deps("member", {
      download: vi.fn(async () => {
        throw new LumbungError("branch_not_found", 404, "branch not found")
      }),
    })
    expect((await exportRoute(get("db=shop&branch=nope"), d)).status).toBe(404)
  })
})
