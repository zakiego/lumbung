import { LumbungError } from "@lumbung/client"
import { describe, expect, test, vi } from "vitest"
import type { Role } from "@/lib/roles"
import { type ObjectDeps, abortUpload, downloadObject, multipartObject, uploadObject } from "./objects"

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

test("a stored Content-Encoding drops the encoded length, and filenames follow RFC 5987", async () => {
  const { d } = deps("owner", {
    get: vi.fn(async () => ({ response: new Response("decoded body", { headers: { "content-length": "5", "content-encoding": "gzip" } }) })),
  })
  const res = await downloadObject(new Request(url("bucket=photos&key=" + encodeURIComponent("it's (final)*.js"))), d)
  expect(res.headers.get("content-length")).toBeNull()
  expect(res.headers.get("content-disposition")).toContain("filename*=UTF-8''it%27s%20%28final%29%2A.js")
  expect(await res.text()).toBe("decoded body")
})

describe("multipart", () => {
  const UP = "up_aaaaaaaaaaaaaaaaaaaa"
  function withS3(role: Role | null, respond: (method: string, path: string, q: Record<string, string>) => Response) {
    const calls: { method: string; path: string; q: Record<string, string>; body?: unknown }[] = []
    const base = deps(role).d
    const d: ObjectDeps = {
      ...base,
      s3: async (method, path, q, init) => {
        calls.push({ method, path, q, body: init?.body })
        return respond(method, path, q)
      },
    }
    return { d, calls }
  }
  const post = (q: string, body?: unknown) =>
    new Request(url(q), { method: "POST", body: body === undefined ? undefined : JSON.stringify(body), headers: { origin: ORIGIN, "x-object-content-type": "video/mp4" } })

  test("start, send parts, complete, abort", async () => {
    const { d, calls } = withS3("member", (method, _path, q) => {
      if (method === "POST" && "uploads" in q) return new Response(`<InitiateMultipartUploadResult><UploadId>${UP}</UploadId></InitiateMultipartUploadResult>`)
      if (method === "PUT") return new Response(null, { headers: { etag: `"${"a".repeat(32)}"` } })
      if (method === "POST") return new Response(`<CompleteMultipartUploadResult><ETag>&#34;${"b".repeat(32)}-2&#34;</ETag></CompleteMultipartUploadResult>`)
      return new Response(null, { status: 204 })
    })
    const key = "videos/../a b.mp4"
    const start = await multipartObject(post(`bucket=photos&key=${encodeURIComponent(key)}&uploads`), d)
    expect(await start.json()).toEqual({ uploadId: UP })
    expect(calls[0]!.path).toBe("/v1/s3/photos/videos/%2E%2E/a%20b.mp4")
    const part = await uploadObject(put(`bucket=photos&key=${encodeURIComponent(key)}&uploadId=${UP}&partNumber=1`), d)
    expect(await part.json()).toEqual({ etag: "a".repeat(32) })
    expect(calls[1]!.q).toEqual({ uploadId: UP, partNumber: "1" })
    const done = await multipartObject(post(`bucket=photos&key=${encodeURIComponent(key)}&uploadId=${UP}`, { parts: [{ number: 1, etag: "a".repeat(32) }] }), d)
    expect(await done.json()).toEqual({ key, etag: `${"b".repeat(32)}-2` })
    expect(String(calls[2]!.body)).toContain(`<PartNumber>1</PartNumber><ETag>"${"a".repeat(32)}"</ETag>`)
    const abort = await abortUpload(new Request(url(`bucket=photos&key=x&uploadId=${UP}`), { method: "DELETE", headers: { origin: ORIGIN } }), d)
    expect(abort.status).toBe(204)
  })

  test("guards and bad input", async () => {
    const ro = withS3("readonly", () => new Response())
    expect((await multipartObject(post("bucket=photos&key=a&uploads"), ro.d)).status).toBe(403)
    expect((await uploadObject(put(`bucket=photos&key=a&uploadId=${UP}&partNumber=1`), ro.d)).status).toBe(403)
    expect(ro.calls).toHaveLength(0)
    const { d, calls } = withS3("owner", () => new Response())
    expect((await uploadObject(put("bucket=photos&key=a&uploadId=nope&partNumber=1"), d)).status).toBe(400)
    expect((await uploadObject(put(`bucket=photos&key=a&uploadId=${UP}&partNumber=0`), d)).status).toBe(400)
    expect((await multipartObject(post(`bucket=photos&key=a&uploadId=${UP}`, { parts: [{ number: 1, etag: "<x/>" }] }), d)).status).toBe(400)
    expect((await multipartObject(post(`bucket=photos&key=a&uploadId=${UP}`, { parts: [] }), d)).status).toBe(400)
    expect(calls).toHaveLength(0)
    const evil = new Request(url("bucket=photos&key=a&uploads"), { method: "POST", headers: { origin: "https://evil.example" } })
    expect((await multipartObject(evil, d)).status).toBe(403)
  })

  test("engine errors come back with their message", async () => {
    const { d } = withS3("owner", () => new Response("<Error><Code>EntityTooSmall</Code><Message>part 1 is too small</Message></Error>", { status: 400 }))
    const res = await multipartObject(post(`bucket=photos&key=a&uploadId=${UP}`, { parts: [{ number: 1, etag: "a".repeat(32) }] }), d)
    expect(res.status).toBe(400)
    expect(await res.json()).toEqual({ error: "part 1 is too small" })
  })
})
