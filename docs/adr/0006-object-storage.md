# 0006: Object storage

Date: 2026-11-14. Status: accepted.

## Context

PLAN 16 adds buckets of files to a shipped instance. Existing S3 clients (SDKs, CLIs, sync tools) must work unchanged, and the feature must reuse what the engine already has: tokens and scopes, rate limits, the audit log, limits, backups, the dashboard, the CLI, the SDK and MCP. Our public hostname forwards only `/app`, `/v1`, `/mcp`, `/libsql` and `/health` to the engine, and the Mac mini's disk is tight.

## Decision

### Protocol and path

The S3 REST protocol, path-style, at `/v1/s3/<bucket>/<key>`. Path-style is the only form that works on one hostname without wildcard DNS. Putting it under `/v1` means no edge Worker or tunnel change, and clients that take an endpoint with a path (the AWS SDKs, boto3, rclone) work with `https://<host>/v1/s3`. Clients that only accept a bare host can't use this prefix; that's the cost, and a separate listener can be added later if it matters.

The S3 handler runs before the router. Go's `ServeMux` cleans paths (`a//b` and `./x` are redirected), which would change keys and break signatures, so `/v1/s3/` is matched on the raw path first.

The REST API also gets JSON endpoints for buckets and objects (`/v1/buckets/...`, key in a query parameter so it never meets path cleaning). The SDK, CLI, dashboard and MCP use those with bearer tokens, so they need no XML or SigV4. Both front ends call one object store in the engine.

Region: the engine accepts any region in a signature's credential scope and reports the empty location (the protocol's default) for every bucket.

### Auth

SigV4 only on the S3 API: header auth and presigned URLs, signed or unsigned payloads, and `aws-chunked` bodies (signed chunks, signed or unsigned trailers). Current AWS SDKs send CRC32 trailers by default, so chunked bodies and the checksum headers are verified, not ignored. The clock skew limit is 15 minutes and presigned URLs live at most 7 days, as in the protocol.

An access key belongs to a token. Its id is `LMB` + the token's 20 character id in capitals, so lookup needs no new index and the audit log names the same token. SigV4 is an HMAC, so the server needs the secret: it is stored AES-256-GCM encrypted (the token id as associated data) with a 32 byte key in `secrets/access-key-encryption-key`, generated on first start with mode 600. A meta.db copy alone doesn't reveal secrets. Revoking or expiring the token ends the key and every URL it signed, since presigned URLs are checked against the live token on use.

Considered: deriving the secret from the instance key and the token id (no storage). Rejected because the secret could then be shown again later, and the "shown once" rule for credentials would rest on convention.

Tokens gain the scope `bucket` with a `bucket_id`. The tokens table's CHECK constraints name the scopes, so migration 4 rebuilds the table. Bucket tokens can't create database tokens and the reverse (`Covers`), and database checks (`AtLeast`, `SeesDatabase`) already fail for a token with no database id.

Public read is per bucket and covers only GET and HEAD of objects. Anonymous requests to a private bucket and to a missing one get the same `AccessDenied`, and anonymous traffic has its own per-address rate limit.

### Storage layout

The index lives in meta.db: `buckets` (with running size and count), `objects` (primary key bucket id and key, binary collation, which is the protocol's UTF-8 byte order), `uploads` and `upload_parts`, and `blob_gc`. Content lives in files named by random ids:

```
objects/<bucket id>/<first 2 chars of blob id>/<blob id>
uploads/<upload id>/<part number>-<random>
```

A put streams to `tmp/`, hashing as it goes (MD5 for the ETag, SHA-256 when the payload is signed, the declared checksum), fsyncs, renames into `objects/`, and records the row in one meta transaction that also queues the old blob for removal. Files are never modified after the rename, so a reader holding one sees a whole object, and a copy can hard-link instead of copying.

Considered: one SQLite index file per bucket. It isolates buckets further and spreads writes, but every backup would then need a consistent cut across many files and meta.db. meta.db already holds tokens and is copied with the online backup API, so one copy gives a consistent index. A single team's instance doesn't need more write throughput than meta.db's WAL gives.

Removal of replaced or deleted blobs waits `LUMBUNG_OBJECT_GC_DELAY` (1 hour), so a backup that copied meta.db can still copy every file it lists. Startup and housekeeping remove files with no row older than the delay, and unfinished uploads past `LUMBUNG_MULTIPART_EXPIRY`.

### Limits

All settings, documented with defaults: `LUMBUNG_MAX_BUCKETS` (100), `LUMBUNG_MAX_OBJECT_BYTES` (5GiB), `LUMBUNG_MAX_BUCKET_BYTES` (10GiB), `LUMBUNG_MAX_OBJECT_STORAGE_BYTES` (20GiB, objects plus parts in progress), `LUMBUNG_MULTIPART_EXPIRY` (24h), `LUMBUNG_PRESIGN_MAX_EXPIRY` (168h), `LUMBUNG_PUBLIC_RATE_LIMIT` (100 per second per address), `LUMBUNG_OBJECT_GC_DELAY` (1h). Puts need a length (`Content-Length`, or `x-amz-decoded-content-length` for chunked bodies), which is reserved against the bucket and instance limits before the first byte is written; the write also stops at the declared length. `LUMBUNG_MIN_FREE_DISK` refuses writes with 507. Multipart follows the protocol: parts 1 to 10000, at least 5 MiB except the last.

Keys: 1 to 1024 bytes of valid UTF-8 without control characters (they can't appear in XML 1.0 responses). User metadata: 2 KB in total.

## Consequences

- The AWS SDK for JavaScript and other path-style clients work by endpoint alone. `examples/s3` proves it in CI.
- Proxies that normalize URLs (resolving `..`, merging slashes) change keys with such segments and break their signatures. The docs say so.
- Clients that only accept a host, not an endpoint path, can't connect yet.
- meta.db grows by one row per object, and every write takes its write lock briefly.
- Backups grow with the buckets; unchanged files are hard-linked between backups in the same folder, and `--skip-objects` leaves them out.
