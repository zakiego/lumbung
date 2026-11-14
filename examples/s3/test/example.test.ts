// The AWS SDK for JavaScript, unchanged, against a real lumbungd: buckets,
// put and get (with a range), streamed uploads (aws-chunked with checksum
// trailers), listing, copy, delete, multipart by hand and with the upload
// helper, presigned URLs, and the scopes tokens are limited to.
import {
  AbortMultipartUploadCommand,
  CompleteMultipartUploadCommand,
  CopyObjectCommand,
  CreateBucketCommand,
  CreateMultipartUploadCommand,
  DeleteBucketCommand,
  DeleteObjectCommand,
  DeleteObjectsCommand,
  GetObjectCommand,
  HeadBucketCommand,
  HeadObjectCommand,
  ListBucketsCommand,
  ListMultipartUploadsCommand,
  ListObjectsV2Command,
  ListPartsCommand,
  PutObjectCommand,
  S3Client,
  UploadPartCommand,
  paginateListObjectsV2,
} from "@aws-sdk/client-s3"
import { Upload } from "@aws-sdk/lib-storage"
import { getSignedUrl } from "@aws-sdk/s3-request-presigner"
import { createHash, randomBytes } from "node:crypto"
import { Readable } from "node:stream"
import { afterAll, beforeAll, describe, expect, test } from "vitest"

import { engineBinary, startEngine, type Running } from "../../../packages/client/test/engine.ts"
import { connect } from "../src/app.ts"

let engine: Running
let admin: S3Client

type Created = { id: string; token: string; access_key_id: string; secret_access_key: string }

async function api(method: string, path: string, body?: unknown) {
  const res = await fetch(engine.url + path, {
    method,
    headers: { authorization: `Bearer ${engine.token}`, "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`)
  return res.json() as Promise<Record<string, unknown>>
}

async function accessKey(body: Record<string, unknown>): Promise<S3Client> {
  const t = (await api("POST", "/v1/tokens", { name: "s3", access_key: true, ...body })) as unknown as Created
  expect(t.access_key_id).toMatch(/^LMB[A-Z0-9]{20}$/)
  return connect(engine.url, t.access_key_id, t.secret_access_key)
}

const md5 = (b: Uint8Array | string) => createHash("md5").update(b).digest("hex")

beforeAll(async () => {
  engine = await startEngine(engineBinary())
  admin = await accessKey({ access: "admin" })
  await admin.send(new CreateBucketCommand({ Bucket: "photos" }))
  await admin.send(new CreateBucketCommand({ Bucket: "backups" }))
})

afterAll(async () => {
  admin?.destroy()
  await engine?.stop()
})

describe("buckets", () => {
  test("list, head, and delete only when empty", async () => {
    const { Buckets } = await admin.send(new ListBucketsCommand({}))
    expect(Buckets!.map((b) => b.Name)).toEqual(["backups", "photos"])
    await admin.send(new HeadBucketCommand({ Bucket: "photos" }))
    await expect(admin.send(new HeadBucketCommand({ Bucket: "nope" }))).rejects.toMatchObject({ $metadata: { httpStatusCode: 404 } })
    await admin.send(new CreateBucketCommand({ Bucket: "scratch" }))
    await admin.send(new PutObjectCommand({ Bucket: "scratch", Key: "x", Body: "x" }))
    await expect(admin.send(new DeleteBucketCommand({ Bucket: "scratch" }))).rejects.toMatchObject({ name: "BucketNotEmpty" })
    await admin.send(new DeleteObjectCommand({ Bucket: "scratch", Key: "x" }))
    await admin.send(new DeleteBucketCommand({ Bucket: "scratch" }))
  })
})

describe("objects", () => {
  test("put and get, with a range, metadata and headers", async () => {
    const put = await admin.send(
      new PutObjectCommand({
        Bucket: "photos",
        Key: "albums/2026/cat.txt",
        Body: "hello, granary",
        ContentType: "text/plain",
        CacheControl: "max-age=60",
        Metadata: { camera: "x100" },
      }),
    )
    expect(put.ETag).toBe(`"${md5("hello, granary")}"`)
    const got = await admin.send(new GetObjectCommand({ Bucket: "photos", Key: "albums/2026/cat.txt" }))
    expect(await got.Body!.transformToString()).toBe("hello, granary")
    expect(got.ContentType).toBe("text/plain")
    expect(got.CacheControl).toBe("max-age=60")
    expect(got.Metadata).toEqual({ camera: "x100" })
    const part = await admin.send(new GetObjectCommand({ Bucket: "photos", Key: "albums/2026/cat.txt", Range: "bytes=7-13" }))
    expect(part.ContentRange).toBe("bytes 7-13/14")
    expect(await part.Body!.transformToString()).toBe("granary")
    const head = await admin.send(new HeadObjectCommand({ Bucket: "photos", Key: "albums/2026/cat.txt" }))
    expect(head.ContentLength).toBe(14)
    await expect(admin.send(new GetObjectCommand({ Bucket: "photos", Key: "missing" }))).rejects.toMatchObject({ name: "NoSuchKey" })
    await expect(
      admin.send(new GetObjectCommand({ Bucket: "photos", Key: "albums/2026/cat.txt", Range: "bytes=100-" })),
    ).rejects.toMatchObject({ $metadata: { httpStatusCode: 416 } })
  })

  test("a streamed body (aws-chunked with a checksum trailer) and odd keys", async () => {
    const data = randomBytes(300_000)
    const put = await admin.send(
      new PutObjectCommand({ Bucket: "photos", Key: "raw/a b+c%d/ünï 🍚.bin", Body: Readable.from([data]), ContentLength: data.length }),
    )
    expect(put.ETag).toBe(`"${md5(data)}"`)
    const got = await admin.send(new GetObjectCommand({ Bucket: "photos", Key: "raw/a b+c%d/ünï 🍚.bin" }))
    expect(Buffer.from(await got.Body!.transformToByteArray()).equals(data)).toBe(true)
    for (const key of ["a//b", "./dot", "../up"]) {
      await admin.send(new PutObjectCommand({ Bucket: "photos", Key: `odd/${key}`, Body: key }))
      const o = await admin.send(new GetObjectCommand({ Bucket: "photos", Key: `odd/${key}` }))
      expect(await o.Body!.transformToString()).toBe(key)
    }
  })

  test("list with a prefix, a delimiter and pages", async () => {
    for (const k of ["list/a", "list/b", "list/c/1", "list/c/2", "list/d"]) {
      await admin.send(new PutObjectCommand({ Bucket: "photos", Key: k, Body: k }))
    }
    const top = await admin.send(new ListObjectsV2Command({ Bucket: "photos", Prefix: "list/", Delimiter: "/" }))
    expect(top.Contents!.map((o) => o.Key)).toEqual(["list/a", "list/b", "list/d"])
    expect(top.CommonPrefixes!.map((p) => p.Prefix)).toEqual(["list/c/"])
    const keys: string[] = []
    for await (const page of paginateListObjectsV2({ client: admin, pageSize: 2 }, { Bucket: "photos", Prefix: "list/" })) {
      expect(page.KeyCount).toBeLessThanOrEqual(2)
      keys.push(...(page.Contents ?? []).map((o) => o.Key!))
    }
    expect(keys).toEqual(["list/a", "list/b", "list/c/1", "list/c/2", "list/d"])
  })

  test("copy within and across buckets, and delete", async () => {
    await admin.send(new PutObjectCommand({ Bucket: "photos", Key: "copy/src", Body: "copy me", Metadata: { a: "1" } }))
    const cp = await admin.send(new CopyObjectCommand({ Bucket: "backups", Key: "copy/dst", CopySource: "photos/copy/src" }))
    expect(cp.CopyObjectResult!.ETag).toBe(`"${md5("copy me")}"`)
    const head = await admin.send(new HeadObjectCommand({ Bucket: "backups", Key: "copy/dst" }))
    expect(head.Metadata).toEqual({ a: "1" })
    await admin.send(
      new CopyObjectCommand({ Bucket: "photos", Key: "copy/src", CopySource: "photos/copy/src", MetadataDirective: "REPLACE", ContentType: "text/x-copy" }),
    )
    expect((await admin.send(new HeadObjectCommand({ Bucket: "photos", Key: "copy/src" }))).ContentType).toBe("text/x-copy")
    await admin.send(new DeleteObjectCommand({ Bucket: "photos", Key: "copy/src" }))
    const res = await admin.send(
      new DeleteObjectsCommand({ Bucket: "backups", Delete: { Objects: [{ Key: "copy/dst" }, { Key: "never-there" }] } }),
    )
    expect(res.Deleted!.map((d) => d.Key).sort()).toEqual(["copy/dst", "never-there"])
    await expect(admin.send(new HeadObjectCommand({ Bucket: "backups", Key: "copy/dst" }))).rejects.toMatchObject({ $metadata: { httpStatusCode: 404 } })
  })
})

describe("multipart", () => {
  test("by hand: create, upload parts, list, complete", async () => {
    const a = randomBytes(5 * 1024 * 1024)
    const b = randomBytes(1234)
    const { UploadId } = await admin.send(new CreateMultipartUploadCommand({ Bucket: "photos", Key: "big/manual.bin", ContentType: "application/x-test" }))
    const p1 = await admin.send(new UploadPartCommand({ Bucket: "photos", Key: "big/manual.bin", UploadId, PartNumber: 1, Body: a }))
    const p2 = await admin.send(new UploadPartCommand({ Bucket: "photos", Key: "big/manual.bin", UploadId, PartNumber: 2, Body: b }))
    const uploads = await admin.send(new ListMultipartUploadsCommand({ Bucket: "photos" }))
    expect(uploads.Uploads!.map((u) => u.UploadId)).toContain(UploadId)
    const parts = await admin.send(new ListPartsCommand({ Bucket: "photos", Key: "big/manual.bin", UploadId }))
    expect(parts.Parts!.map((p) => p.Size)).toEqual([a.length, b.length])
    const done = await admin.send(
      new CompleteMultipartUploadCommand({
        Bucket: "photos",
        Key: "big/manual.bin",
        UploadId,
        MultipartUpload: { Parts: [{ PartNumber: 1, ETag: p1.ETag }, { PartNumber: 2, ETag: p2.ETag }] },
      }),
    )
    expect(done.ETag).toMatch(/^"[0-9a-f]{32}-2"$/)
    const got = await admin.send(new GetObjectCommand({ Bucket: "photos", Key: "big/manual.bin", Range: `bytes=${a.length - 2}-${a.length + 1}` }))
    expect(Buffer.from(await got.Body!.transformToByteArray()).equals(Buffer.concat([a.subarray(-2), b.subarray(0, 2)]))).toBe(true)
    expect(got.ContentType).toBe("application/x-test")

    const { UploadId: gone } = await admin.send(new CreateMultipartUploadCommand({ Bucket: "photos", Key: "big/aborted" }))
    await admin.send(new AbortMultipartUploadCommand({ Bucket: "photos", Key: "big/aborted", UploadId: gone }))
    await expect(
      admin.send(new UploadPartCommand({ Bucket: "photos", Key: "big/aborted", UploadId: gone, PartNumber: 1, Body: "x" })),
    ).rejects.toMatchObject({ name: "NoSuchUpload" })
  })

  test("with the upload helper", async () => {
    const data = randomBytes(11 * 1024 * 1024 + 7)
    const up = new Upload({ client: admin, params: { Bucket: "photos", Key: "big/helper.bin", Body: data }, partSize: 5 * 1024 * 1024, queueSize: 2 })
    const out = await up.done()
    expect(out.ETag).toMatch(/-3"$/)
    const got = await admin.send(new GetObjectCommand({ Bucket: "photos", Key: "big/helper.bin" }))
    expect(createHash("sha256").update(await got.Body!.transformToByteArray()).digest("hex")).toBe(
      createHash("sha256").update(data).digest("hex"),
    )
  })
})

describe("presigned URLs", () => {
  test("GET and PUT with fetch", async () => {
    await admin.send(new PutObjectCommand({ Bucket: "photos", Key: "share/note.txt", Body: "shared" }))
    const getURL = await getSignedUrl(admin, new GetObjectCommand({ Bucket: "photos", Key: "share/note.txt" }), { expiresIn: 300 })
    expect(await (await fetch(getURL)).text()).toBe("shared")
    const putURL = await getSignedUrl(admin, new PutObjectCommand({ Bucket: "photos", Key: "share/upload.txt" }), { expiresIn: 300 })
    expect((await fetch(putURL, { method: "PUT", body: "from a browser" })).status).toBe(200)
    const got = await admin.send(new GetObjectCommand({ Bucket: "photos", Key: "share/upload.txt" }))
    expect(await got.Body!.transformToString()).toBe("from a browser")
    // A URL for one key doesn't open another, and a GET URL doesn't PUT.
    expect((await fetch(getURL.replace("share/note.txt", "share/upload.txt"))).status).toBe(403)
    expect((await fetch(getURL, { method: "PUT", body: "x" })).status).toBe(403)
  })

  test("expires", async () => {
    const url = await getSignedUrl(admin, new GetObjectCommand({ Bucket: "photos", Key: "share/note.txt" }), { expiresIn: 1 })
    await new Promise((r) => setTimeout(r, 2100))
    const res = await fetch(url)
    expect(res.status).toBe(403)
    expect(await res.text()).toContain("AccessDenied")
  })
})

describe("scopes", () => {
  test("a read key for one bucket reads it and nothing else", async () => {
    const reader = await accessKey({ bucket: "photos", access: "read" })
    const got = await reader.send(new GetObjectCommand({ Bucket: "photos", Key: "albums/2026/cat.txt" }))
    expect(await got.Body!.transformToString()).toBe("hello, granary")
    await expect(reader.send(new PutObjectCommand({ Bucket: "photos", Key: "x", Body: "x" }))).rejects.toMatchObject({ name: "AccessDenied" })
    await expect(reader.send(new ListObjectsV2Command({ Bucket: "backups" }))).rejects.toMatchObject({ name: "NoSuchBucket" })
    const { Buckets } = await reader.send(new ListBucketsCommand({}))
    expect(Buckets!.map((b) => b.Name)).toEqual(["photos"])
    reader.destroy()
  })

  test("a wrong secret is refused", async () => {
    const t = (await api("POST", "/v1/tokens", { name: "s3", access_key: true, access: "read" })) as unknown as Created
    const bad = connect(engine.url, t.access_key_id, t.secret_access_key.replace(/.$/, (c) => (c === "a" ? "b" : "a")))
    await expect(bad.send(new ListBucketsCommand({}))).rejects.toMatchObject({ name: "SignatureDoesNotMatch" })
    bad.destroy()
  })
})
