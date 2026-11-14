import { LumbungError } from "@lumbung/client"
import { describe, expect, test, vi } from "vitest"
import type { Role } from "@/lib/roles"
import { type ObjectDeps, downloadObject, uploadObject } from "./objects"

const ORIGIN = "http://localhost:4871"

function deps(role: Role | null, bucket: Record<string, unknown> = {}) {
  const b = {
    put: vi.fn(async (key: string) => ({ key, size: 3 })),
    get: vi.fn(async () => ({ response: new Response("<script>x</script>", { headers: { "content-length": "18", "content-type": "text/html" } }) })),
    ...bucket,
  }
  const d: ObjectDeps = {
    viewer: async () => (role ? { id: "u1", name: "Ann", email: "ann@example.com", role } : null),
    engine: () => ({ bucket: () => b }) as never,
  }
  return { d, b }
}

const url = (q: string) => `${ORIGIN}/app/api/objects?${q}`
const put = (q: string, body: BodyInit | null = "abc", headers: Record<string, string> = {}) =>
  new Request(url(q), { method: "PUT", body, headers: { origin: ORIGIN, "content-type": "text/plain", "content-length": "3", ...headers } })

describe("upload", () => {
  test("needs a session and a role that can write", async () => {
    const none = deps(null)
    expect((await uploadObject(put("bucket=photos&key=a.txt"), none.d)).status).toBe(401)
    const ro = deps("readonly")
    expect((await uploadObject(put("bucket=photos&key=a.txt"), ro.d)).status).toBe(403)
    expect(ro.b.put).not.toHaveBeenCalled()
  })

  test.each<Record<string, string>>([{ origin: "https://evil.example" }, { "sec-fetch-site": "cross-site" }])("refuses another site %o", async (headers) => {
    const { d, b } = deps("owner")
    expect((await uploadObject(put("bucket=photos&key=a.txt", "abc", headers), d)).status).toBe(403)
    expect(b.put).not.toHaveBeenCalled()
  })

  test("streams the body to the engine with its length and type", async () => {
    const { d, b } = deps("member")
    const res = await uploadObject(put("bucket=photos&key=" + encodeURIComponent("2026/ünï cat.txt")), d)
    expect(res.status).toBe(200)
    expect(b.put).toHaveBeenCalledOnce()
    const [key, body, opts] = b.put.mock.calls[0] as unknown as [string, ReadableStream, { contentLength: number; contentType: string }]
    expect(key).toBe("2026/ünï cat.txt")
    expect(body).toBeInstanceOf(ReadableStream)
    expect(opts).toEqual({ contentLength: 3, contentType: "text/plain" })
  })

  test("needs a bucket, a key and a length", async () => {
    const { d, b } = deps("owner")
    expect((await uploadObject(put("bucket=photos"), d)).status).toBe(400)
    expect((await uploadObject(put("key=a"), d)).status).toBe(400)
    expect((await uploadObject(put("bucket=photos&key=" + "k".repeat(1025)), d)).status).toBe(400)
    const chunked = new Request(url("bucket=photos&key=a"), {
      method: "PUT",
      body: new ReadableStream({ start: (c) => c.close() }),
      headers: { origin: ORIGIN },
      duplex: "half",
    } as RequestInit)
    expect((await uploadObject(chunked, d)).status).toBe(411)
    expect(b.put).not.toHaveBeenCalled()
  })

  test("passes the engine's error through", async () => {
    const { d } = deps("owner", { put: vi.fn(async () => Promise.reject(new LumbungError("bucket_size_limit", 507, "the bucket is full"))) })
    const res = await uploadObject(put("bucket=photos&key=a"), d)
    expect(res.status).toBe(507)
    expect(await res.json()).toEqual({ error: "the bucket is full" })
  })
})

describe("download", () => {
  test("is always an inert attachment on the dashboard's origin", async () => {
    const { d } = deps("readonly")
    const res = await downloadObject(new Request(url("bucket=photos&key=" + encodeURIComponent("pages/ünï.html"))), d)
    expect(res.status).toBe(200)
    expect(res.headers.get("content-type")).toBe("application/octet-stream")
    expect(res.headers.get("content-disposition")).toBe(`attachment; filename="_n_.html"; filename*=UTF-8''%C3%BCn%C3%AF.html`)
    expect(res.headers.get("content-security-policy")).toContain("sandbox")
    expect(res.headers.get("x-content-type-options")).toBe("nosniff")
    expect(res.headers.get("content-length")).toBe("18")
    expect(await res.text()).toBe("<script>x</script>")
  })

  test("needs a session; a missing object is a 404", async () => {
    expect((await downloadObject(new Request(url("bucket=photos&key=a")), deps(null).d)).status).toBe(401)
    const { d } = deps("owner", { get: vi.fn(async () => Promise.reject(new LumbungError("object_not_found", 404, "no object with that key"))) })
    const res = await downloadObject(new Request(url("bucket=photos&key=a")), d)
    expect(res.status).toBe(404)
  })
})
