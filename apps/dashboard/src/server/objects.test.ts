import { LumbungError } from "@lumbung/client"
import { afterEach, describe, expect, test, vi } from "vitest"
import { type Permission, type Role, can } from "@/lib/roles"
import { resetConfig } from "./config"
import { type ObjectDeps, abortUpload, downloadObject, multipartObject, s3Path, uploadObject } from "./objects"
import { AccessError, type Viewer, denied } from "./session"

const ORIGIN = "http://localhost:4871"
// Requests name the workspace as the browser does, by name. The engine gets its id.
const WS_NAME = "agents"
const WS = "ws_bbbbbbbbbbbbbbbbbbbb"

// `access` answers like accessTo: a 404 for a workspace that isn't theirs, a 403 for a role that is too low.
function deps(role: Role | null, bucket: Record<string, unknown> = {}) {
  const b = {
    put: vi.fn(async (key: string) => ({ key, size: 3 })),
    get: vi.fn(async () => ({ response: new Response("<script>x</script>", { headers: { "content-length": "18", "content-type": "text/html" } }) })),
    ...bucket,
  }
  const bucketOf = vi.fn((_name: string) => b)
  const access = vi.fn(async (viewer: Viewer, ws: string, p: Permission) => {
    if (!role || (ws !== WS_NAME && ws !== WS)) throw new AccessError("That workspace doesn't exist.", 404)
    if (!can(role, p)) throw new AccessError(denied(p), 403)
    return { viewer, workspace: { id: WS }, role, engine: { bucket: bucketOf } as never }
  })
  const d: ObjectDeps = {
    viewer: async () => (role ? { id: "u1", name: "Ann", email: "ann@example.com", owner: role === "owner" } : null),
    access,
  }
  return { d, b, access, bucketOf }
}

const bare = (q: string) => `${ORIGIN}/app/api/objects?${q}`
const url = (q: string) => bare(`ws=${WS_NAME}&${q}`)
const putTo = (target: string, body: BodyInit | null = "abc", headers: Record<string, string> = {}) =>
  new Request(target, { method: "PUT", body, headers: { origin: ORIGIN, "content-type": "text/plain", "content-length": "3", ...headers } })
const put = (q: string, body: BodyInit | null = "abc", headers: Record<string, string> = {}) => putTo(url(q), body, headers)

describe("upload", () => {
  test("needs a session and a role that can write", async () => {
    const none = deps(null)
    expect((await uploadObject(put("bucket=photos&key=a.txt"), none.d)).status).toBe(401)
    expect(none.access).not.toHaveBeenCalled()
    const ro = deps("readonly")
    expect((await uploadObject(put("bucket=photos&key=a.txt"), ro.d)).status).toBe(403)
    expect(ro.access).toHaveBeenCalledWith(expect.objectContaining({ id: "u1" }), WS_NAME, "write")
    expect(ro.b.put).not.toHaveBeenCalled()
  })

  test.each<Record<string, string>>([{ origin: "https://evil.example" }, { "sec-fetch-site": "cross-site" }])("refuses another site %o", async (headers) => {
    const { d, b, access } = deps("owner")
    expect((await uploadObject(put("bucket=photos&key=a.txt", "abc", headers), d)).status).toBe(403)
    expect(access).not.toHaveBeenCalled()
    expect(b.put).not.toHaveBeenCalled()
  })

  test("streams the body to the engine with its length and type", async () => {
    const { d, b, bucketOf } = deps("member")
    const res = await uploadObject(put("bucket=photos&key=" + encodeURIComponent("2026/ünï cat.txt")), d)
    expect(res.status).toBe(200)
    expect(bucketOf).toHaveBeenCalledWith("photos")
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
    const { d, access } = deps("readonly")
    const res = await downloadObject(new Request(url("bucket=photos&key=" + encodeURIComponent("pages/ünï.html"))), d)
    expect(res.status).toBe(200)
    expect(access).toHaveBeenCalledWith(expect.objectContaining({ id: "u1" }), WS_NAME, "read")
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

const UP = "up_aaaaaaaaaaaaaaaaaaaa"

type S3Seen = { method: string; path: string; q: Record<string, string>; body?: unknown; headers: Record<string, string> }

function withS3(role: Role | null, respond: (method: string, path: string, q: Record<string, string>) => Response = () => new Response()) {
  const calls: S3Seen[] = []
  const base = deps(role)
  const d: ObjectDeps = {
    ...base.d,
    s3: async (method, path, q, init) => {
      calls.push({ method, path, q, body: init?.body, headers: init?.headers ?? {} })
      return respond(method, path, q)
    },
  }
  return { ...base, d, calls }
}

const post = (q: string, body?: unknown) =>
  new Request(url(q), { method: "POST", body: body === undefined ? undefined : JSON.stringify(body), headers: { origin: ORIGIN, "x-object-content-type": "video/mp4" } })
const del = (q: string) => new Request(url(q), { method: "DELETE", headers: { origin: ORIGIN } })

// The engine's answers to a whole multipart upload.
function multipartEngine(method: string, _path: string, q: Record<string, string>): Response {
  if (method === "POST" && "uploads" in q) return new Response(`<InitiateMultipartUploadResult><UploadId>${UP}</UploadId></InitiateMultipartUploadResult>`)
  if (method === "PUT") return new Response(null, { headers: { etag: `"${"a".repeat(32)}"` } })
  if (method === "POST") return new Response(`<CompleteMultipartUploadResult><ETag>&#34;${"b".repeat(32)}-2&#34;</ETag></CompleteMultipartUploadResult>`)
  return new Response(null, { status: 204 })
}

describe("multipart", () => {
  test("start, send parts, complete, abort", async () => {
    const { d, calls } = withS3("member", multipartEngine)
    const key = "videos/2026 #1?/a b%.mp4"
    const start = await multipartObject(post(`bucket=photos&key=${encodeURIComponent(key)}&uploads`), d)
    expect(await start.json()).toEqual({ uploadId: UP })
    expect(calls[0]!.path).toBe(`/v1/workspaces/${WS}/s3/photos/videos/2026%20%231%3F/a%20b%25.mp4`)
    expect(calls[0]!.q).toEqual({ uploads: "" })
    const part = await uploadObject(put(`bucket=photos&key=${encodeURIComponent(key)}&uploadId=${UP}&partNumber=1`), d)
    expect(await part.json()).toEqual({ etag: "a".repeat(32) })
    expect(calls[1]!.q).toEqual({ uploadId: UP, partNumber: "1" })
    const done = await multipartObject(post(`bucket=photos&key=${encodeURIComponent(key)}&uploadId=${UP}`, { parts: [{ number: 1, etag: "a".repeat(32) }] }), d)
    expect(await done.json()).toEqual({ key, etag: `${"b".repeat(32)}-2` })
    expect(String(calls[2]!.body)).toContain(`<PartNumber>1</PartNumber><ETag>"${"a".repeat(32)}"</ETag>`)
    const abort = await abortUpload(del(`bucket=photos&key=x&uploadId=${UP}`), d)
    expect(abort.status).toBe(204)
    expect(calls.map((c) => c.method)).toEqual(["POST", "PUT", "POST", "DELETE"])
  })

  test("guards and bad input", async () => {
    const ro = withS3("readonly")
    expect((await multipartObject(post("bucket=photos&key=a&uploads"), ro.d)).status).toBe(403)
    expect((await uploadObject(put(`bucket=photos&key=a&uploadId=${UP}&partNumber=1`), ro.d)).status).toBe(403)
    expect((await abortUpload(del(`bucket=photos&key=a&uploadId=${UP}`), ro.d)).status).toBe(403)
    expect(ro.calls).toHaveLength(0)
    const { d, calls } = withS3("owner")
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

describe("multipart can't leave the object path", () => {
  test.each([
    ["a bucket of ..", "bucket=..&key=tokens%2Ftok_x"],
    ["a bucket with a slash", "bucket=..%2Fv1&key=x"],
    ["a dot-dot key", "bucket=photos&key=..%2F..%2Ftokens%2Ftok_x"],
    ["an encoded dot-dot key", "bucket=photos&key=%252E%252E%2F%252E%252E%2Ftokens"],
    ["a dot key", "bucket=photos&key=a%2F.%2Fb"],
  ])("refuses %s", async (_, q) => {
    const calls: string[] = []
    const d: ObjectDeps = {
      ...deps("owner").d,
      s3: async (_m, path) => {
        calls.push(path)
        return new Response(null, { status: 204 })
      },
    }
    const aborted = await abortUpload(del(`${q}&uploadId=${UP}`), d)
    const start = await multipartObject(new Request(url(`${q}&uploads`), { method: "POST", headers: { origin: ORIGIN } }), d)
    const part = await uploadObject(put(`${q}&uploadId=${UP}&partNumber=1`), d)
    // An encoded %2E is a literal key here, not a dot segment: it may pass,
    // but only as an object path under the bucket, in the workspace.
    for (const p of calls) expect(p.startsWith(`/v1/workspaces/${WS}/s3/photos/`)).toBe(true)
    if (!q.includes("%252E")) {
      expect([aborted.status, start.status, part.status]).toEqual([400, 400, 400])
      expect(calls).toHaveLength(0)
    }
  })

  test("the engine call checks where the URL really goes", () => {
    expect(() => s3Path(WS, "..", "x")).toThrow()
    expect(() => s3Path(WS, "photos", "a/../b")).toThrow()
    expect(s3Path(WS, "photos", "%2E%2E/x")).toBe(`/v1/workspaces/${WS}/s3/photos/%252E%252E/x`)
    expect(new URL("http://h" + s3Path(WS, "photos", "%2E%2E/x")).pathname).toBe(`/v1/workspaces/${WS}/s3/photos/%252E%252E/x`)
  })

  test.each([
    ["..", ".."],
    ["an empty id", ""],
    ["a workspace name", WS_NAME],
    ["the default workspace's name", "default"],
    ["an id with a path after it", `${WS}/../ws_aaaaaaaaaaaaaaaaaaaa`],
    ["an encoded dot-dot", "%2E%2E"],
    ["an id in capitals", WS.toUpperCase()],
    ["an id that is too short", "ws_bbbb"],
    ["an id with a space after it", `${WS} `],
    ["an id with a line break after it", `${WS}\n`],
  ])("s3Path refuses %s in place of the workspace id", (_, id) => {
    expect(() => s3Path(id, "photos", "a.txt")).toThrow("That key can't be sent through the dashboard.")
    expect(s3Path(WS, "photos", "a.txt")).toBe(`/v1/workspaces/${WS}/s3/photos/a.txt`)
  })
})

describe("the workspace", () => {
  const requests: [string, (d: ObjectDeps, q: string) => Promise<Response>][] = [
    ["upload", (d, q) => uploadObject(putTo(bare(`${q}bucket=photos&key=a.txt`)), d)],
    ["upload part", (d, q) => uploadObject(putTo(bare(`${q}bucket=photos&key=a.txt&uploadId=${UP}&partNumber=1`)), d)],
    ["download", (d, q) => downloadObject(new Request(bare(`${q}bucket=photos&key=a.txt`)), d)],
    ["multipart start", (d, q) => multipartObject(new Request(bare(`${q}bucket=photos&key=a.txt&uploads`), { method: "POST", headers: { origin: ORIGIN } }), d)],
    [
      "multipart complete",
      (d, q) =>
        multipartObject(
          new Request(bare(`${q}bucket=photos&key=a.txt&uploadId=${UP}`), { method: "POST", body: JSON.stringify({ parts: [{ number: 1, etag: "a".repeat(32) }] }), headers: { origin: ORIGIN } }),
          d,
        ),
    ],
    ["abort", (d, q) => abortUpload(new Request(bare(`${q}bucket=photos&key=a.txt&uploadId=${UP}`), { method: "DELETE", headers: { origin: ORIGIN } }), d)],
  ]

  test.each(requests)("%s: a request that names no workspace is a 400", async (_, send) => {
    for (const q of ["", "ws=&", `ws=${"w".repeat(101)}&`]) {
      const { d, b, access, calls } = withS3("owner", multipartEngine)
      const res = await send(d, q)
      expect(res.status, q).toBe(400)
      expect(await res.json()).toEqual({ error: "Pick a workspace." })
      expect(access).not.toHaveBeenCalled()
      expect(b.put).not.toHaveBeenCalled()
      expect(b.get).not.toHaveBeenCalled()
      expect(calls).toHaveLength(0)
    }
  })

  test.each(requests)("%s: a workspace the person can't see is a 404, and nothing reaches the engine", async (_, send) => {
    for (const ws of ["staging", "ws_cccccccccccccccccccc"]) {
      const { d, b, access, bucketOf, calls } = withS3("admin", multipartEngine)
      const res = await send(d, `ws=${ws}&`)
      expect(res.status, ws).toBe(404)
      expect(await res.json()).toEqual({ error: "That workspace doesn't exist." })
      expect(res.headers.get("cache-control")).toBe("no-store")
      expect(access).toHaveBeenCalledOnce()
      expect(access.mock.calls[0]![1]).toBe(ws)
      expect(bucketOf).not.toHaveBeenCalled()
      expect(b.put).not.toHaveBeenCalled()
      expect(b.get).not.toHaveBeenCalled()
      expect(calls).toHaveLength(0)
    }
  })

  test.each(requests.filter(([name]) => name !== "download"))("%s: a role that is too low is a 403, and nothing reaches the engine", async (_, send) => {
    const { d, b, access, bucketOf, calls } = withS3("readonly", multipartEngine)
    const res = await send(d, `ws=${WS_NAME}&`)
    expect(res.status).toBe(403)
    expect(await res.json()).toEqual({ error: "Your role can't make changes." })
    expect(access.mock.calls[0]!.slice(1)).toEqual([WS_NAME, "write"])
    expect(bucketOf).not.toHaveBeenCalled()
    expect(b.put).not.toHaveBeenCalled()
    expect(calls).toHaveLength(0)
  })

  test("the status and message of an AccessError come back as they are", async () => {
    for (const [status, message] of [
      [404, "That workspace doesn't exist."],
      [403, "Only an admin of this workspace can do that."],
      [401, "Sign in again."],
    ] as const) {
      const { d, b, calls } = withS3("owner", multipartEngine)
      d.access = async () => {
        throw new AccessError(message, status)
      }
      for (const [, send] of requests) {
        const res = await send(d, `ws=${WS_NAME}&`)
        expect(res.status).toBe(status)
        expect(await res.json()).toEqual({ error: message })
      }
      expect(b.put).not.toHaveBeenCalled()
      expect(b.get).not.toHaveBeenCalled()
      expect(calls).toHaveLength(0)
    }
  })

  test("a failure that isn't about access isn't answered as one", async () => {
    const { d, calls } = withS3("owner", multipartEngine)
    d.access = async () => {
      throw new Error("engine down")
    }
    await expect(downloadObject(new Request(url("bucket=photos&key=a")), d)).rejects.toThrow("engine down")
    await expect(multipartObject(post("bucket=photos&key=a&uploads"), d)).rejects.toThrow("engine down")
    expect(calls).toHaveLength(0)
  })

  test("the S3 path names the workspace by id, whether the request used its name or its id", async () => {
    for (const ref of [WS_NAME, WS]) {
      const { d, calls } = withS3("member", multipartEngine)
      const q = `ws=${ref}&`
      for (const [name, send] of requests) {
        if (name === "upload" || name === "download") continue
        expect((await send(d, q)).ok, name).toBe(true)
      }
      expect(calls).toHaveLength(4)
      for (const c of calls) expect(c.path).toBe(`/v1/workspaces/${WS}/s3/photos/a.txt`)
    }
  })

  test("a workspace id that isn't an id never reaches the engine", async () => {
    for (const id of [WS_NAME, "", "..", `${WS}/..`]) {
      const { d, calls } = withS3("owner", multipartEngine)
      const access = d.access
      d.access = async (viewer, ws, p) => ({ ...(await access(viewer, ws, p)), workspace: { id } })
      for (const [name, send] of requests) {
        if (name === "upload" || name === "download") continue
        const res = await send(d, `ws=${WS_NAME}&`)
        expect(res.status, `${name} ${JSON.stringify(id)}`).toBe(400)
        expect(await res.json()).toEqual({ error: "That key can't be sent through the dashboard." })
      }
      expect(calls).toHaveLength(0)
    }
  })

  test.each<[Role, string | undefined]>([
    ["admin", "admin"],
    ["member", "write"],
    ["owner", undefined],
  ])("S3 calls for role %s carry the person and the level %s", async (role, level) => {
    const { d, calls } = withS3(role, multipartEngine)
    for (const [name, send] of requests) {
      if (name === "upload" || name === "download") continue
      expect((await send(d, `ws=${WS_NAME}&`)).ok, name).toBe(true)
    }
    expect(calls.map((c) => c.method)).toEqual(["PUT", "POST", "POST", "DELETE"])
    for (const c of calls) {
      expect(c.headers["X-Lumbung-Actor"], c.method).toBe("dashboard:ann@example.com")
      expect(c.headers["X-Lumbung-Actor-Access"], c.method).toBe(level)
    }
  })

  test("a non-owner who reaches this far as owner is still sent at read", async () => {
    const { d, calls } = withS3("owner", multipartEngine)
    d.viewer = async () => ({ id: "u2", name: "Bo", email: "bo@example.com", owner: false })
    await multipartObject(post("bucket=photos&key=a.txt&uploads"), d)
    expect(calls[0]!.headers).toMatchObject({ "X-Lumbung-Actor": "dashboard:bo@example.com", "X-Lumbung-Actor-Access": "read" })
  })
})

describe("without an S3 override", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    delete process.env.LUMBUNG_ENGINE_URL
    delete process.env.LUMBUNG_SERVICE_TOKEN
    resetConfig()
  })

  test("the request goes to the engine's S3 path in that workspace, as the person", async () => {
    process.env.LUMBUNG_DASHBOARD_URL = ORIGIN
    process.env.LUMBUNG_ENGINE_URL = "http://engine.test:4870"
    process.env.LUMBUNG_SERVICE_TOKEN = "lmb_service_testsecret"
    resetConfig()
    const seen: { url: URL; method: string; headers: Headers }[] = []
    vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
      seen.push({ url: new URL(String(input)), method: init?.method ?? "GET", headers: new Headers(init?.headers) })
      return new Response(`<InitiateMultipartUploadResult><UploadId>${UP}</UploadId></InitiateMultipartUploadResult>`)
    })
    const { d } = deps("member")
    const res = await multipartObject(post(`bucket=photos&key=${encodeURIComponent("videos/a b.mp4")}&uploads`), d)
    expect(await res.json()).toEqual({ uploadId: UP })
    expect(seen).toHaveLength(1)
    expect(seen[0]!.method).toBe("POST")
    expect(seen[0]!.url.origin).toBe("http://engine.test:4870")
    expect(seen[0]!.url.pathname).toBe(`/v1/workspaces/${WS}/s3/photos/videos/a%20b.mp4`)
    expect(seen[0]!.url.searchParams.has("uploads")).toBe(true)
    expect(seen[0]!.headers.get("authorization")).toBe("Bearer lmb_service_testsecret")
    expect(seen[0]!.headers.get("x-lumbung-actor")).toBe("dashboard:ann@example.com")
    expect(seen[0]!.headers.get("x-lumbung-actor-access")).toBe("write")
    expect(seen[0]!.headers.get("content-type")).toBe("video/mp4")
  })
})
