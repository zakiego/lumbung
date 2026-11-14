---
title: Store files from Node
description: Upload, download, list and share files in a bucket from Node with @lumbung/client.
sidebar:
  order: 10
---

`@lumbung/client` works with [buckets](/docs/concepts/buckets/) through the REST API, with the same token as your queries. It runs anywhere `fetch` does: Node, Bun, Workers and the browser.

<!-- docs-test: skip installs a package; the docs test resolves it from the workspace -->
```sh
pnpm add @lumbung/client
```

Make a bucket and a token for it. The `--access-key` part is only needed for presigned URLs, further down:

```sh
lumbung bucket create photos
lumbung token create --name my-app --bucket photos --access write --access-key --json > token.json
```

## Upload and download

```ts
import { readFileSync } from "node:fs"
import { createClient } from "@lumbung/client"

const client = createClient({
  url: process.env.LUMBUNG_URL!, // e.g. http://127.0.0.1:4870
  token: JSON.parse(readFileSync("token.json", "utf8")).token,
})
const photos = client.bucket("photos")

const saved = await photos.put("notes/hello.txt", "hello from the granary", {
  contentType: "text/plain",
  metadata: { author: "ana" },
})
console.log(saved.etag, saved.size)

const file = await photos.get("notes/hello.txt")
console.log(await file.text(), file.metadata.author)
```

`put` takes a string, a `Uint8Array`, an `ArrayBuffer`, a `Blob` or a `ReadableStream`. A stream needs `contentLength`, since Lumbung checks the size against the bucket's limit before it stores anything. Uploading to a key that exists replaces the object.

Read part of an object with a range. Both ends are inclusive:

<!-- docs-test: continue -->
```ts
const part = await photos.get("notes/hello.txt", { range: { start: 0, end: 4 } })
console.log(await part.text(), part.size, part.totalSize) // "hello" 5 22
```

For a large download, stream `file.response.body` instead of calling `text()` or `bytes()`.

## List

Keys are flat, but a delimiter rolls them up into folders:

<!-- docs-test: continue -->
```ts
await photos.put("notes/2026/june.txt", "june")
const page = await photos.list({ prefix: "notes/", delimiter: "/" })
console.log(page.objects.map((o) => o.key)) // ["notes/hello.txt"]
console.log(page.prefixes)                  // ["notes/2026/"]
```

A page holds at most `limit` entries (default 100, at most 1000). Pass `nextCursor` back as `cursor` until it is empty.

## Copy, check and delete

<!-- docs-test: continue -->
```ts
await photos.copy({ fromKey: "notes/hello.txt", key: "archive/hello.txt" })
console.log(await photos.head("archive/hello.txt"))   // the record
await photos.delete("archive/hello.txt")
console.log(await photos.head("archive/hello.txt"))   // null
```

## Share a file

A presigned URL lets anyone download (or upload) one object until it expires, without a token. It is signed with the token's access key:

<!-- docs-test: continue -->
```ts
const link = await photos.presign("notes/hello.txt", { expiresIn: 600 })
const res = await fetch(link.url)
console.log(res.status, await res.text())
```

Revoking the token ends every URL it signed. To publish a whole bucket instead, turn on public read (`lumbung bucket update photos --public`) and use `<engine>/v1/s3/photos/<key>` as the address.

## Manage buckets

With an instance admin token, `client.buckets` lists, creates, updates and deletes buckets:

```ts
import { createClient } from "@lumbung/client"

const admin = createClient({ url: process.env.LUMBUNG_URL!, token: process.env.LUMBUNG_TOKEN! })
await admin.buckets.create("invoices")
await admin.buckets.update("invoices", { publicRead: false, corsOrigins: ["https://app.example.com"] })
const { data } = await admin.buckets.list()
console.log(data.map((b) => `${b.name}: ${b.object_count} objects, ${b.size_bytes} bytes`))
await admin.buckets.delete("invoices") // only when empty
```

Errors carry the API's [error codes](/docs/reference/error-codes/) in `code`, for example `object_not_found`, `bucket_size_limit` or `no_access_key`.
