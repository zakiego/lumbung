// Buckets and objects, through the engine's REST API.
import type { BucketOut, ObjectOut } from "@lumbung/client"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { s3Endpoint } from "@/lib/snippets"
import { config } from "@/server/config"
import { allPages, call } from "@/server/engine"
import { requireIn } from "@/server/session"

const bucketName = z.string().regex(/^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$/, "Use 3 to 63 lowercase letters, digits and dashes, starting and ending with a letter or digit.")
const ref = z.string().min(1).max(100)
// The workspace a call acts in, by name or id. Every call names one.
const ws = z.string().min(1).max(100)
const key = z.string().min(1).max(1024)

export const listBuckets = createServerFn({ method: "GET" })
  .validator(z.object({ ws }))
  .handler(async ({ data }): Promise<BucketOut[]> => {
  const access = await requireIn(data.ws, "read")
  const e = access.engine
  return call(() => allPages((cursor) => e.buckets.list({ limit: 200, cursor })))
})

export type BucketPage = { bucket: BucketOut; objects: ObjectOut[]; prefixes: string[]; nextCursor: string; s3Endpoint: string }

export const getBucketPage = createServerFn({ method: "GET" })
  .validator(z.object({ ws, bucket: ref, prefix: z.string().max(1024).default(""), cursor: z.string().max(4096).optional() }))
  .handler(async ({ data }): Promise<BucketPage> => {
    const access = await requireIn(data.ws, "read")
    const e = access.engine
    return call(async () => {
      const [bucket, page] = await Promise.all([
        e.buckets.get(data.bucket),
        e.bucket(data.bucket).list({ prefix: data.prefix, delimiter: "/", limit: 200, cursor: data.cursor }),
      ])
      return { bucket, objects: page.objects, prefixes: page.prefixes, nextCursor: page.nextCursor, s3Endpoint: s3Endpoint(config().publicEngineUrl, access.workspace) }
    })
  })

export const getBucket = createServerFn({ method: "GET" })
  .validator(z.object({ ws, bucket: ref }))
  .handler(async ({ data }): Promise<{ bucket: BucketOut; s3Endpoint: string }> => {
    const access = await requireIn(data.ws, "admin")
    return call(async () => ({ bucket: await access.engine.buckets.get(data.bucket), s3Endpoint: s3Endpoint(config().publicEngineUrl, access.workspace) }))
  })

// What the Connect page shows: the bucket and the public engine URL. Tokens never leave the server.
export const getBucketConnect = createServerFn({ method: "GET" })
  .validator(z.object({ ws, bucket: ref }))
  .handler(async ({ data }): Promise<{ bucket: BucketOut; engineUrl: string }> => {
    const access = await requireIn(data.ws, "read")
    return call(async () => ({ bucket: await access.engine.buckets.get(data.bucket), engineUrl: config().publicEngineUrl }))
  })

export const createBucket = createServerFn({ method: "POST" })
  .validator(z.object({ ws, name: bucketName }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "admin")
    return call(() => access.engine.buckets.create(data.name))
  })

export const updateBucket = createServerFn({ method: "POST" })
  .validator(
    z.object({
      ws,
      bucket: ref,
      name: bucketName.optional(),
      publicRead: z.boolean().optional(),
      corsOrigins: z.array(z.string().min(1).max(200)).max(20).optional(),
    }),
  )
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "admin")
    const { bucket, ...patch } = data
    return call(() => access.engine.buckets.update(bucket, patch))
  })

export const deleteBucket = createServerFn({ method: "POST" })
  .validator(z.object({ ws, bucket: ref }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "admin")
    await call(() => access.engine.buckets.delete(data.bucket))
    return { ok: true }
  })

export const deleteObject = createServerFn({ method: "POST" })
  .validator(z.object({ ws, bucket: ref, key }))
  .handler(async ({ data }) => {
    const access = await requireIn(data.ws, "write")
    await call(() => access.engine.bucket(data.bucket).delete(data.key))
    return { ok: true }
  })
