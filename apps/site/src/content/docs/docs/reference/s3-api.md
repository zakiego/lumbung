---
title: S3 API
description: The engine's S3-compatible API at /v1/s3, its operations, authentication, limits and error codes.
sidebar:
  order: 9
---

The engine serves an S3-compatible API at `/v1/s3`, path-style: `/v1/s3/<bucket>/<key>`. It's what the AWS SDKs, boto3, rclone and other S3 clients speak. For using it from code, see [Use an S3 client](/docs/guides/s3-clients/).

A Lumbung bearer token works on it too, which is handy with curl. With a bearer token, `x-amz-content-sha256` may still carry the body's SHA-256 or `STREAMING-UNSIGNED-PAYLOAD-TRAILER`; signed chunks need a signature:

```sh
lumbung bucket create docs
printf 'plain text\n' > readme.txt
lumbung object put readme.txt docs/
curl -s -H "Authorization: Bearer <token>" http://127.0.0.1:4870/v1/s3/docs/readme.txt
curl -s -H "Authorization: Bearer <token>" "http://127.0.0.1:4870/v1/s3/docs?list-type=2"
```

## Authentication

| How | When |
|---|---|
| `Authorization: AWS4-HMAC-SHA256 ...` | S3 clients: Signature Version 4 with an [access key](/docs/concepts/tokens/#access-keys) |
| `?X-Amz-Algorithm=AWS4-HMAC-SHA256&...` | Presigned URLs, valid up to `LUMBUNG_PRESIGN_MAX_EXPIRY` (7 days) |
| `Authorization: Bearer lmb_...` | Lumbung tokens, for curl and scripts |
| No credentials | Only GET and HEAD of objects in a bucket with public read |

- Any region in the credential scope is accepted; the service must be `s3`.
- The request time must be within 15 minutes of the server's.
- With header signing, every `x-amz-*` header must be signed. A presigned URL may carry unsigned `x-amz-checksum-*` headers, which only add checks.
- The payload may be signed (`x-amz-content-sha256` is the body's SHA-256), `UNSIGNED-PAYLOAD`, or `aws-chunked` with signed chunks (`STREAMING-AWS4-HMAC-SHA256-PAYLOAD`, with or without `-TRAILER`) or unsigned chunks with a checksum trailer (`STREAMING-UNSIGNED-PAYLOAD-TRAILER`). Chunked uploads need `x-amz-decoded-content-length`.
- Checksums in `x-amz-checksum-crc32`, `-crc32c`, `-crc64nvme`, `-sha1`, `-sha256` (as headers or trailers) and `Content-MD5` are checked. A mismatch fails the request and nothing is stored.
- The token behind an access key is read on every request: revoking or expiring it stops the key and its presigned URLs at once.
- Failed signatures count toward `LUMBUNG_AUTH_FAILURES_PER_MINUTE` for the client's address; requests without credentials toward `LUMBUNG_PUBLIC_RATE_LIMIT`. Past either, the answer is `SlowDown`.
- Signature Version 2 and Signature Version 4A aren't supported.

## Operations

| Operation | Request | Needs |
|---|---|---|
| ListBuckets | `GET /v1/s3` | any token (lists the buckets it can see) |
| CreateBucket | `PUT /v1/s3/{bucket}` | instance admin |
| HeadBucket | `HEAD /v1/s3/{bucket}` | read |
| DeleteBucket | `DELETE /v1/s3/{bucket}` | instance admin; the bucket must be empty |
| GetBucketLocation | `GET /v1/s3/{bucket}?location` | read; always the default (empty) location |
| GetBucketVersioning | `GET /v1/s3/{bucket}?versioning` | read; always unversioned |
| ListObjectsV2 | `GET /v1/s3/{bucket}?list-type=2` | read |
| ListObjects | `GET /v1/s3/{bucket}` | read |
| PutObject | `PUT /v1/s3/{bucket}/{key}` | write |
| GetObject | `GET /v1/s3/{bucket}/{key}` | read, or none on a public bucket |
| HeadObject | `HEAD /v1/s3/{bucket}/{key}` | read, or none on a public bucket |
| DeleteObject | `DELETE /v1/s3/{bucket}/{key}` | write |
| DeleteObjects | `POST /v1/s3/{bucket}?delete` | write; up to 1000 keys |
| CopyObject | `PUT /v1/s3/{bucket}/{key}` with `x-amz-copy-source` | write here, read on the source |
| CreateMultipartUpload | `POST /v1/s3/{bucket}/{key}?uploads` | write |
| UploadPart | `PUT /v1/s3/{bucket}/{key}?partNumber=N&uploadId=ID` | write |
| CompleteMultipartUpload | `POST /v1/s3/{bucket}/{key}?uploadId=ID` | write |
| AbortMultipartUpload | `DELETE /v1/s3/{bucket}/{key}?uploadId=ID` | write |
| ListParts | `GET /v1/s3/{bucket}/{key}?uploadId=ID` | read |
| ListMultipartUploads | `GET /v1/s3/{bucket}?uploads` | read |
| CORS preflight | `OPTIONS /v1/s3/{bucket}/...` | none; answered from the bucket's CORS origins |

Details:

- **GetObject** takes one `Range` (`bytes=a-b`, `bytes=a-`, `bytes=-n`); several ranges or another unit return the whole object. `If-Match`, `If-None-Match`, `If-Modified-Since` and `If-Unmodified-Since` work as in HTTP. Signed requests may override response headers with `response-content-type`, `response-content-disposition`, `response-cache-control`, `response-content-encoding`, `response-content-language` and `response-expires`.
- **PutObject** stores `Content-Type`, `Cache-Control`, `Content-Disposition`, `Content-Encoding` (without `aws-chunked`), `Content-Language`, `Expires` and `x-amz-meta-*`. `If-None-Match: *` refuses to overwrite and `If-Match` requires the current ETag; both are checked in the same step that records the object, so of two writers racing with `If-None-Match: *`, exactly one wins. CompleteMultipartUpload takes the same two headers.
- **Responses** to GetObject and HeadObject always carry `Content-Security-Policy: sandbox` and `X-Content-Type-Options: nosniff`, so stored HTML or SVG can't run scripts in a browser.
- **CopyObject** takes `x-amz-metadata-directive` `COPY` (default) or `REPLACE`, and the `x-amz-copy-source-if-*` conditions; any that doesn't hold answers `PreconditionFailed`. Copying an object onto itself needs `REPLACE`.
- **ListObjectsV2** takes `prefix`, `delimiter`, `max-keys` (at most 1000), `continuation-token`, `start-after`, `fetch-owner` and `encoding-type=url`. Keys are listed in UTF-8 byte order.
- **Multipart:** part numbers run from 1 to 10,000, every part but the last is at least 5 MiB, and the ETag of the result is the MD5 of the parts' MD5s with `-<parts>`. Parts not named in the completion are discarded. Unfinished uploads are aborted after `LUMBUNG_MULTIPART_EXPIRY`.

Not supported, answering `NotImplemented`: changing bucket settings over the S3 API (use the REST API, the CLI or the dashboard), versioning and object versions, ACLs, bucket policies, tagging, lifecycle rules, object lock, website hosting, logging, replication, encryption settings, S3 Select, UploadPartCopy and reading one part with `partNumber`. Storage class is always `STANDARD`.

## Errors

Errors are XML with an S3 code, and every response has `x-amz-request-id` (the same id as `X-Request-Id`):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>NoSuchKey</Code><Message>no object with that key</Message><BucketName>docs</BucketName><Key>missing.txt</Key><Resource>/v1/s3/docs/missing.txt</Resource><RequestId>4f1c...</RequestId></Error>
```

| Code | Status | When |
|---|---|---|
| `AccessDenied` | 403 | The token's scope or access doesn't allow it, an unsigned `x-amz-*` header, an expired URL, or no credentials on a private (or missing) bucket |
| `InvalidAccessKeyId` | 403 | No such access key, or its token is revoked or expired |
| `SignatureDoesNotMatch` | 403 | Wrong secret, or the request changed after signing (including a chunk) |
| `RequestTimeTooSkewed` | 403 | The request time is more than 15 minutes off |
| `AuthorizationHeaderMalformed`, `AuthorizationQueryParametersError` | 400 | The signature can't be read |
| `NoSuchBucket` | 404 | No bucket by that name, or one the token can't see |
| `NoSuchKey` | 404 | No object with that key |
| `NoSuchUpload` | 404 | No such multipart upload for this bucket and key |
| `BucketNotEmpty` | 409 | Deleting a bucket that holds objects |
| `BucketAlreadyOwnedByYou` | 409 | Creating a bucket that exists |
| `InvalidBucketName` | 400 | The name isn't 3 to 63 lowercase letters, digits and hyphens |
| `KeyTooLongError` | 400 | The key is over 1024 bytes, or has control characters |
| `EntityTooLarge` | 400 | The object or part is over `LUMBUNG_MAX_OBJECT_BYTES` |
| `EntityTooSmall` | 400 | A part before the last is under 5 MiB |
| `InvalidPart`, `InvalidPartOrder` | 400 | A completion names a missing part, a wrong ETag, or parts out of order |
| `QuotaExceeded` | 403 | The bucket or all buckets would pass their size limit |
| `InsufficientStorage` | 507 | The disk has less free space than `LUMBUNG_MIN_FREE_DISK` |
| `BadDigest`, `XAmzContentSHA256Mismatch` | 400 | The body doesn't match its checksum or signed hash |
| `IncompleteBody` | 400 | The body is shorter or longer than declared |
| `MissingContentLength` | 411 | An upload without a length |
| `InvalidRange` | 416 | The range starts past the end |
| `PreconditionFailed` | 412 | A condition doesn't hold |
| `TooManyBuckets` | 400 | The instance has `LUMBUNG_MAX_BUCKETS` buckets |
| `SlowDown` | 503 | Rate limited; `Retry-After` says when to try again |
| `NotImplemented` | 501 | An operation from the list above that isn't supported |
| `InternalError` | 500 | Something failed inside the engine; the log has the request id |

## Paths and proxies

The engine matches `/v1/s3/` on the raw request path before any other routing, so keys with `//`, `.` or `..` segments arrive as sent, and stay opaque names: they never become file paths. Browsers and `fetch` resolve `.` and `..` segments in a URL, even percent-encoded, so presigning refuses a key with such a segment rather than handing out a URL that names another key, and the dashboard uploads such keys in one piece. A proxy in front must pass the path and the `Host` header through unchanged, or signatures fail.
