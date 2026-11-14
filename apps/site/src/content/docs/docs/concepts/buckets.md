---
title: Buckets and objects
description: Files next to your databases, in buckets, reachable over an S3-compatible API, the REST API, the SDK, the CLI and MCP.
sidebar:
  order: 4
---

A bucket holds files, called objects. Buckets live on the same instance as your databases, behind the same tokens, and show up in the dashboard, the CLI, the SDK and MCP.

```sh
echo "hello from the granary" > note.txt
lumbung bucket create photos
lumbung object put note.txt photos/notes/
lumbung object ls photos/notes/
lumbung object get photos/notes/note.txt
```

## Buckets

A bucket name is 3 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit, so every S3 client accepts it. Only an instance admin token creates and deletes buckets. A bucket can only be deleted when it holds no objects.

Each bucket has two settings:

- **Public read.** Anyone can GET and HEAD objects without a token. Listing, writing and deleting still need one. Off by default.
- **CORS origins.** Origins a browser may call the S3 API from, for [uploads straight from the browser](/docs/guides/browser-uploads/). Empty by default.

```sh
lumbung bucket update photos --public
lumbung bucket update photos --private --cors https://app.example.com
```

## Objects

An object has a key, its content, a size, an ETag, a content type, the standard headers `Cache-Control`, `Content-Disposition`, `Content-Encoding`, `Content-Language` and `Expires`, and custom metadata.

- A key is 1 to 1024 bytes of UTF-8 without control characters. Slashes have no special meaning to storage; listings can roll keys up into folders at a delimiter, usually `/`. Keys are opaque: `../x` or `a//b` are just names.
- The ETag is the MD5 of the content. An object assembled from a multipart upload has the MD5 of its parts' MD5s with `-<parts>`, as S3 clients expect.
- Custom metadata travels as `x-amz-meta-*` headers, at most 2 KB in total. Names are stored lowercase.
- A write is atomic. Readers see the old object or the new one, never a partial file. The last write wins.

There is no versioning: replacing or deleting an object is final. Lumbung keeps the old file for `LUMBUNG_OBJECT_GC_DELAY` (an hour) only so a backup running at that moment can still copy it.

## Ways in

| From | Use |
|---|---|
| Any S3 client (AWS SDKs, boto3, rclone) | The [S3-compatible API](/docs/reference/s3-api/) at `<engine>/v1/s3`, with an access key |
| Your code | [`@lumbung/client`](/docs/guides/files-node/): `client.bucket("photos").put(...)` |
| Scripts | `lumbung bucket` and `lumbung object` in the [CLI](/docs/reference/cli/) |
| Anything that speaks HTTP | `/v1/buckets` in the [REST API](/docs/reference/rest-api/) |
| AI agents | `list_objects`, `get_object`, `put_object` and friends in [MCP](/docs/reference/mcp-tools/) |
| People | Buckets in the dashboard |

## Tokens and access keys

Bucket access uses the same [tokens](/docs/concepts/tokens/): an instance token reaches every bucket at its access level, a bucket token reaches one bucket, and a database token reaches none. S3 clients sign requests instead of sending a token, so a token can carry an **access key**: an id and a secret made with the token and shown once.

```sh
lumbung token create --name uploads --bucket photos --access write --access-key
```

## Storage

Objects are files in the data folder under `objects/`, one file per object, named by a random id. The index (keys, sizes, metadata) is in `meta.db`. Both go into [backups](/docs/self-hosting/backups/). [Limits](/docs/concepts/limits/#object-storage) cap the size of one object, one bucket and all buckets together, separately from the databases' limits.
