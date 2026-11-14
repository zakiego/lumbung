---
title: Use an S3 client
description: Point the AWS SDK, or any S3 client that takes an endpoint, at a Lumbung instance and work with buckets unchanged.
sidebar:
  order: 11
---

<!-- docs-test: example s3 -->

Lumbung serves an S3-compatible API at `<engine>/v1/s3`. Existing S3 clients work against it with three settings:

- **Endpoint:** your engine's URL plus `/v1/s3`, for example `https://lumbung.example.com/v1/s3`.
- **Path-style addressing** (`forcePathStyle`, `addressing_style = path`): buckets are in the path, not the hostname.
- **Any region.** Lumbung accepts whatever region the client signs with; `us-east-1` is the usual choice.

The client needs an access key. Make one with a token, here for the bucket `photos` with write access:

```sh
lumbung bucket create photos
lumbung token create --name s3 --bucket photos --access write --access-key --json > s3-key.json
```

The id and secret are in `access_key_id` and `secret_access_key`. The secret is shown once. In the dashboard, open Tokens, pick the bucket under Reaches and tick S3 access key.

## The AWS SDK for JavaScript

<!-- docs-test: skip installs packages; the docs test resolves them from examples/s3 -->
```sh
pnpm add @aws-sdk/client-s3 @aws-sdk/lib-storage @aws-sdk/s3-request-presigner
```

```ts
import { readFileSync } from "node:fs"
import { GetObjectCommand, ListObjectsV2Command, PutObjectCommand, S3Client } from "@aws-sdk/client-s3"

const key = JSON.parse(readFileSync("s3-key.json", "utf8"))
const s3 = new S3Client({
  endpoint: "http://127.0.0.1:4870/v1/s3",
  forcePathStyle: true,
  region: "us-east-1",
  credentials: { accessKeyId: key.access_key_id, secretAccessKey: key.secret_access_key },
})

await s3.send(new PutObjectCommand({ Bucket: "photos", Key: "2026/cat.txt", Body: "a very good cat", ContentType: "text/plain" }))

const first = await s3.send(new GetObjectCommand({ Bucket: "photos", Key: "2026/cat.txt", Range: "bytes=0-5" }))
console.log(await first.Body!.transformToString()) // "a very"

const { Contents, CommonPrefixes } = await s3.send(new ListObjectsV2Command({ Bucket: "photos", Delimiter: "/" }))
console.log(Contents ?? [], CommonPrefixes) // [] [{ Prefix: "2026/" }]
```

### Large files

`Upload` from `@aws-sdk/lib-storage` splits a body into parts and uploads them in parallel as a multipart upload. Every part but the last must be at least 5 MiB:

<!-- docs-test: continue -->
```ts
import { Upload } from "@aws-sdk/lib-storage"

const video = new Uint8Array(12 * 1024 * 1024)
const upload = new Upload({ client: s3, params: { Bucket: "photos", Key: "2026/video.bin", Body: video }, partSize: 5 * 1024 * 1024 })
const done = await upload.done()
console.log(done.ETag) // "<md5 of the parts' md5s>-3"
```

Unfinished uploads are aborted after `LUMBUNG_MULTIPART_EXPIRY` (24 hours) and their parts removed. Parts in progress count toward the bucket's size limit.

### Presigned URLs

<!-- docs-test: continue -->
```ts
import { getSignedUrl } from "@aws-sdk/s3-request-presigner"

const url = await getSignedUrl(s3, new GetObjectCommand({ Bucket: "photos", Key: "2026/cat.txt" }), { expiresIn: 3600 })
console.log(await (await fetch(url)).text())
```

A presigned URL lives at most `LUMBUNG_PRESIGN_MAX_EXPIRY` (7 days), and stops working as soon as its token is revoked.

## Other clients

Any client that lets you set an endpoint with a path works the same way. A few settings:

<!-- docs-test: skip a configuration file for another tool, not a program -->
```ini
# ~/.config/rclone/rclone.conf
[lumbung]
type = s3
provider = Other
endpoint = https://lumbung.example.com/v1/s3
access_key_id = LMB...
secret_access_key = ...
force_path_style = true
```

<!-- docs-test: skip needs Python and boto3 -->
```python
import boto3
from botocore.config import Config

s3 = boto3.client(
    "s3",
    endpoint_url="https://lumbung.example.com/v1/s3",
    aws_access_key_id="LMB...",
    aws_secret_access_key="...",
    region_name="us-east-1",
    config=Config(s3={"addressing_style": "path"}),
)
```

Clients that only accept a hostname, without a path, can't reach `/v1/s3` yet.

An access key works in the [workspace](/docs/concepts/workspaces/) of its token, so the endpoint is the same in every workspace and bucket names only need to be unique there. An instance token's key reaches the default workspace; for another one, use `<engine>/v1/workspaces/<workspace>/s3` as the endpoint.

## Behind a proxy

S3 requests are signed, and the signature covers the path and the `Host` header. A proxy in front of the engine must pass both through unchanged: keep the original `Host`, and don't normalize URLs (merging `//` or resolving `..` changes keys like `a//b`, and the signature then fails). See [Reverse proxy and TLS](/docs/self-hosting/reverse-proxy/).

## What's supported

The [S3 API reference](/docs/reference/s3-api/) lists every operation, the error codes and what isn't there (versioning, ACLs, tagging and lifecycle rules among them).
