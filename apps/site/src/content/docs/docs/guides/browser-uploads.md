---
title: Upload from the browser
description: Let users upload files straight to a bucket with presigned PUT URLs and CORS, without passing them through your server.
sidebar:
  order: 12
---

Your server keeps the token. For each upload it asks Lumbung for a presigned PUT URL for one key, and the browser sends the file straight to that URL. The file never passes through your server, and the URL can't be used for any other key or after it expires.

## 1. Allow your site's origin

Browsers only call another origin when it allows them. Add your site to the bucket's CORS origins:

```sh
lumbung bucket create uploads
lumbung bucket update uploads --cors https://app.example.com,http://localhost:5173
```

Each origin is `scheme://host[:port]`, or `*` for any. The engine then answers preflight requests for that bucket and adds `Access-Control-Allow-Origin` to its responses.

## 2. Presign on the server

The token needs write access to the bucket and an access key:

```sh
lumbung token create --name upload-signer --bucket uploads --access write --access-key --json > signer.json
```

```ts
// src/presign.ts
import { readFileSync } from "node:fs"
import { createClient } from "@lumbung/client"

const lumbung = createClient({
  url: process.env.LUMBUNG_URL!,
  token: JSON.parse(readFileSync("signer.json", "utf8")).token,
})

// In your API route: pick the key yourself, never from user input as is.
export async function uploadURL(userId: string, fileName: string) {
  const key = `users/${userId}/${crypto.randomUUID()}-${fileName.replace(/[^\w.-]/g, "_")}`
  const { url, expiresAt } = await lumbung.bucket("uploads").presign(key, { method: "PUT", expiresIn: 300 })
  return { key, url, expiresAt }
}
```

## 3. Upload from the page

<!-- docs-test: skip runs in a browser -->
```js
const { url, key } = await (await fetch("/api/upload-url", { method: "POST", body: JSON.stringify({ name: file.name }) })).json()
const res = await fetch(url, { method: "PUT", body: file })
if (!res.ok) throw new Error(`upload failed: ${res.status}`)
```

Here is the same round trip from Node, to check the setup:

```ts
import { uploadURL } from "./presign.ts"

const { url, key } = await uploadURL("ana", "avatar.png")
const res = await fetch(url, { method: "PUT", body: new Uint8Array([137, 80, 78, 71]) })
console.log(res.status, key)
```

The object gets the content type the browser sends. Size limits still apply: an upload past `LUMBUNG_MAX_OBJECT_BYTES` or the bucket's limit fails with `EntityTooLarge` or `QuotaExceeded`.

## Showing files

For private files, presign a GET per view, the same way. For files anyone may see, such as avatars, make the bucket public instead and link to `<engine>/v1/s3/uploads/<key>`:

```sh
lumbung bucket update uploads --public
```

Public read covers GET and HEAD of objects only. Listing and writing still need a token.
