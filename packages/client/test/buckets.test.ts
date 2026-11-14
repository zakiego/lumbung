import { randomUUID } from "node:crypto"
import { describe, expect, inject, test } from "vitest"

import { createClient, LumbungError } from "../src/index.ts"

const url = inject("url")
const token = inject("token")
const admin = createClient({ url, token })

async function freshBucket() {
  const name = `b-${randomUUID().slice(0, 8)}`
  await admin.buckets.create(name)
  return name
}

describe("buckets", () => {
  test("create, list, update and delete", async () => {
    const name = await freshBucket()
    const { data } = await admin.buckets.list({ limit: 200 })
    expect(data.map((b) => b.name)).toContain(name)
    const b = await admin.buckets.update(name, { publicRead: true, corsOrigins: ["https://app.example"] })
    expect(b.public_read).toBe(true)
    expect(b.cors_origins).toEqual(["https://app.example"])
    await expect(admin.buckets.create(name)).rejects.toMatchObject({ code: "name_taken", status: 409 })
    await admin.buckets.delete(name)
    await expect(admin.buckets.get(name)).rejects.toMatchObject({ code: "bucket_not_found" })
  })
})

describe("objects", () => {
  test("put, get with a range, head, list, copy and delete", async () => {
    const name = await freshBucket()
    const bucket = admin.bucket(name)
    const o = await bucket.put("docs/hello.txt", "hello, granary", { metadata: { Author: "ana" }, cacheControl: "no-cache" })
    expect(o).toMatchObject({ key: "docs/hello.txt", size: 14, content_type: "text/plain; charset=utf-8" })

    const got = await bucket.get("docs/hello.txt")
    expect(await got.text()).toBe("hello, granary")
    expect(got.metadata).toEqual({ author: "ana" })
    expect(got.etag).toBe(o.etag)
    const part = await bucket.get("docs/hello.txt", { range: { start: 7, end: 13 } })
    expect(await part.text()).toBe("granary")
    expect([part.size, part.totalSize]).toEqual([7, 14])
    const tail = await bucket.get("docs/hello.txt", { range: { suffix: 3 } })
    expect(await tail.text()).toBe("ary")

    const bytes = new Uint8Array([0, 1, 2, 255])
    await bucket.put("bin/data", bytes)
    expect(await (await bucket.get("bin/data")).bytes()).toEqual(bytes)
    const stream = new ReadableStream<Uint8Array>({
      start(c) {
        c.enqueue(new TextEncoder().encode("streamed "))
        c.enqueue(new TextEncoder().encode("body"))
        c.close()
      },
    })
    await bucket.put("bin/stream.txt", stream, { contentLength: 13, contentType: "text/plain" })
    expect(await (await bucket.get("bin/stream.txt")).text()).toBe("streamed body")
    await expect(bucket.put("bin/x", new ReadableStream())).rejects.toThrow(TypeError)

    expect(await bucket.head("docs/hello.txt")).toMatchObject({ key: "docs/hello.txt", size: 14, metadata: { author: "ana" } })
    expect(await bucket.head("nope")).toBeNull()

    const top = await bucket.list({ delimiter: "/" })
    expect(top.objects).toEqual([])
    expect(top.prefixes).toEqual(["bin/", "docs/"])
    const page1 = await bucket.list({ limit: 2 })
    expect(page1.objects.map((x) => x.key)).toEqual(["bin/data", "bin/stream.txt"])
    const page2 = await bucket.list({ limit: 2, cursor: page1.nextCursor })
    expect(page2.objects.map((x) => x.key)).toEqual(["docs/hello.txt"])
    expect(page2.nextCursor).toBe("")

    const other = await freshBucket()
    const copy = await admin.bucket(other).copy({ fromBucket: name, fromKey: "docs/hello.txt", key: "copy.txt" })
    expect(copy.etag).toBe(o.etag)
    await bucket.delete("docs/hello.txt")
    await bucket.delete("docs/hello.txt")
    expect(await bucket.head("docs/hello.txt")).toBeNull()
    await expect(bucket.get("docs/hello.txt")).rejects.toMatchObject({ code: "object_not_found", status: 404 })
    expect(await (await admin.bucket(other).get("copy.txt")).text()).toBe("hello, granary")
  })

  test("presign with an access key, and the scope a bucket token has", async () => {
    const name = await freshBucket()
    const other = await freshBucket()
    await admin.bucket(other).put("secret.txt", "not yours")
    await expect(admin.bucket(name).presign("x")).rejects.toMatchObject({ code: "no_access_key" })

    const t = await admin.tokens.create({ name: "sdk", bucket: name, access: "write", accessKey: true })
    expect(t.scope).toBe("bucket")
    expect(t.access_key_id).toMatch(/^LMB/)
    expect(t.secret_access_key).toHaveLength(40)
    const scoped = createClient({ url, token: t.token })
    const bucket = scoped.bucket(name)

    const put = await bucket.presign("up.txt", { method: "PUT", expiresIn: 120 })
    expect(put.expiresAt.getTime()).toBeGreaterThan(Date.now())
    expect((await fetch(put.url, { method: "PUT", body: "through a URL" })).status).toBe(200)
    const get = await bucket.presign("up.txt")
    expect(await (await fetch(get.url)).text()).toBe("through a URL")

    await expect(scoped.bucket(other).get("secret.txt")).rejects.toMatchObject({ code: "bucket_not_found" })
    await expect(scoped.bucket(other).put("x", "x")).rejects.toMatchObject({ code: "bucket_not_found" })
    await expect(scoped.buckets.create("evil-bucket")).rejects.toBeInstanceOf(LumbungError)
    expect((await scoped.buckets.list()).data.map((b) => b.name)).toEqual([name])
  })
})
