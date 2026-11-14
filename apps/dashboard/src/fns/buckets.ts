// Buckets and objects, through the engine's REST API.
import type { BucketOut, ObjectOut } from "@lumbung/client"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { config } from "@/server/config"
import { allPages, call, engineFor } from "@/server/engine"
import { requireCan } from "@/server/session"

const bucketName = z.string().regex(/^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$/, "Use 3 to 63 lowercase letters, digits and dashes, starting and ending with a letter or digit.")
const ref = z.string().min(1).max(100)
const key = z.string().min(1).max(1024)

export const listBuckets = createServerFn({ method: "GET" }).handler(async (): Promise<BucketOut[]> => {
  const viewer = await requireCan("read")
  const e = engineFor(viewer)
  return call(() => allPages((cursor) => e.buckets.list({ limit: 200, cursor })))
})

export type BucketPage = { bucket: BucketOut; objects: ObjectOut[]; prefixes: string[]; nextCursor: string; s3Endpoint: string }

export const getBucketPage = createServerFn({ method: "GET" })
  .validator(z.object({ bucket: ref, prefix: z.string().max(1024).default(""), cursor: z.string().max(4096).optional() }))
  .handler(async ({ data }): Promise<BucketPage> => {
    const viewer = await requireCan("read")
    const e = engineFor(viewer)
    return call(async () => {
      const [bucket, page] = await Promise.all([
        e.buckets.get(data.bucket),
        e.bucket(data.bucket).list({ prefix: data.prefix, delimiter: "/", limit: 200, cursor: data.cursor }),
      ])
      return { bucket, objects: page.objects, prefixes: page.prefixes, nextCursor: page.nextCursor, s3Endpoint: `${config().publicEngineUrl}/v1/s3` }
    })
  })

export const getBucket = createServerFn({ method: "GET" })
  .validator(z.object({ bucket: ref }))
  .handler(async ({ data }): Promise<{ bucket: BucketOut; s3Endpoint: string }> => {
    const viewer = await requireCan("admin")
    return call(async () => ({ bucket: await engineFor(viewer).buckets.get(data.bucket), s3Endpoint: `${config().publicEngineUrl}/v1/s3` }))
  })

export const createBucket = createServerFn({ method: "POST" })
  .validator(z.object({ name: bucketName }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("admin")
    return call(() => engineFor(viewer).buckets.create(data.name))
  })

export const updateBucket = createServerFn({ method: "POST" })
  .validator(
    z.object({
      bucket: ref,
      name: bucketName.optional(),
      publicRead: z.boolean().optional(),
      corsOrigins: z.array(z.string().min(1).max(200)).max(20).optional(),
    }),
  )
  .handler(async ({ data }) => {
    const viewer = await requireCan("admin")
    const { bucket, ...patch } = data
    return call(() => engineFor(viewer).buckets.update(bucket, patch))
  })

export const deleteBucket = createServerFn({ method: "POST" })
  .validator(z.object({ bucket: ref }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("admin")
    await call(() => engineFor(viewer).buckets.delete(data.bucket))
    return { ok: true }
  })

export const deleteObject = createServerFn({ method: "POST" })
  .validator(z.object({ bucket: ref, key }))
  .handler(async ({ data }) => {
    const viewer = await requireCan("write")
    await call(() => engineFor(viewer).bucket(data.bucket).delete(data.key))
    return { ok: true }
  })
