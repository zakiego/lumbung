---
title: Limits
description: The limits on statements, results and storage, their defaults, and the error each one returns.
sidebar:
  order: 6
---

Every limit has a default that suits a small team on one machine, and every one can be changed with an environment variable. The [Configuration](/docs/reference/configuration/) page lists them all.

## Per statement

| Limit | Default | Setting | Error |
|---|---|---|---|
| Run time of a query or batch | 30 s | `LUMBUNG_STATEMENT_TIMEOUT` | `statement_timeout` |
| Rows one statement returns | 10,000 | `LUMBUNG_MAX_RESULT_ROWS` | `result_too_large` |
| Bytes one request returns | 16 MiB | `LUMBUNG_MAX_RESULT_BYTES` | `result_too_large` |
| One string or blob | 16 MiB | `LUMBUNG_MAX_VALUE_BYTES` | `value_too_large` |
| Statement text | 1 MiB | `LUMBUNG_MAX_SQL_BYTES` | `value_too_large` |
| Bound parameters | 10,000 | `LUMBUNG_MAX_PARAMS` | `sql_error` |
| Memory per connection | 128 MiB | `LUMBUNG_CONN_MAX_MEMORY` | `memory_limit` |
| Waiting for a lock | 5 s | `LUMBUNG_BUSY_TIMEOUT` | `busy` |

When a request hits a limit, nothing partial comes back and anything it wrote is rolled back. A statement cancelled by the timeout leaves its connection ready for the next request.

A statement that hits the memory limit fails with `memory_limit`, and the connection it ran on is closed and replaced, which gives its memory back. The usual cause is sorting large values: `GROUP BY`, `ORDER BY` and `DISTINCT` hold every row they sort in memory, including big text and blob columns the query only measures. Compute the small values first in a materialized CTE and sort those instead. With a large `name` column, this groups lengths rather than whole names:

```sql
WITH t AS MATERIALIZED (
  SELECT email LIKE '%@example.com' AS example, length(name) AS bytes FROM users
)
SELECT example, count(*), sum(bytes) FROM t GROUP BY 1;
```

## Storage

| Limit | Default | Setting | Error |
|---|---|---|---|
| Size of one branch | 1 GiB | `LUMBUNG_MAX_BRANCH_BYTES` | `branch_size_limit` (507) |
| Size of all branches together | 10 GiB | `LUMBUNG_MAX_INSTANCE_BYTES` | `instance_quota_exceeded` (507) |
| Free disk before creating files | 2 GiB | `LUMBUNG_MIN_FREE_DISK` | `insufficient_disk` (507) |
| Databases | 100 | `LUMBUNG_MAX_DATABASES` | `limit_reached` |
| Branches per database | 50 | `LUMBUNG_MAX_BRANCHES` | `limit_reached` |

A full branch, or any branch on an instance at its quota, still serves reads and deletes, and writes that reuse the space deletes freed. SQLite doesn't shrink a file when rows are deleted, so freed space is reused rather than returned.

The branch limit counts the database's pages. The write-ahead log next to it is checkpointed and truncated once it passes a quarter of the branch limit (at most 1 MiB), so it stays small. The instance quota counts both, and a write may only claim half the free room in new pages, since each new page is written to the log first. Creating a branch, resetting one, importing and restoring a database from the trash are checked against the quota before they copy anything.

## Object storage

| Limit | Default | Setting | Error (REST) | Error (S3 API) |
|---|---|---|---|---|
| One object, sent whole or assembled from parts | 5 GiB | `LUMBUNG_MAX_OBJECT_BYTES` | `object_too_large` (413) | `EntityTooLarge` |
| One bucket, counting multipart uploads in progress | 10 GiB | `LUMBUNG_MAX_BUCKET_BYTES` | `bucket_size_limit` (507) | `QuotaExceeded` (403) |
| All buckets together | 20 GiB | `LUMBUNG_MAX_OBJECT_STORAGE_BYTES` | `object_storage_quota_exceeded` (507) | `QuotaExceeded` (403) |
| Free disk before writing | 2 GiB | `LUMBUNG_MIN_FREE_DISK` | `insufficient_disk` (507) | `InsufficientStorage` (507) |
| Buckets | 100 | `LUMBUNG_MAX_BUCKETS` | `limit_reached` | `TooManyBuckets` |
| Life of an unfinished multipart upload | 24 h | `LUMBUNG_MULTIPART_EXPIRY` | | |
| Life of a presigned URL | 7 days | `LUMBUNG_PRESIGN_MAX_EXPIRY` | | |
| Requests without a token, per address | 100/s | `LUMBUNG_PUBLIC_RATE_LIMIT` | | `SlowDown` (503) |

Object storage has its own quotas, apart from the branches'. An upload needs its length up front; it is claimed against the bucket and the instance before the first byte is written, and the write stops at that length, so a full bucket never holds a partial object. Replacing an object only counts the difference in size.

Fixed by the S3 protocol: keys are at most 1024 bytes, custom metadata at most 2 KB, a multipart upload has at most 10,000 parts, and every part but the last is at least 5 MiB. A bucket has at most 1,000 multipart uploads in progress.

## Workspaces

The limits above are for the whole instance. Each [workspace](/docs/concepts/workspaces/#limits) can have lower ones of its own, set when it is made or changed later by an instance admin. `0` means no limit of its own.

| Limit | Default for a new workspace | Setting | Error |
|---|---|---|---|
| Databases in one workspace, counting its trash | 20 | `LUMBUNG_WORKSPACE_MAX_DATABASES` | `limit_reached` |
| Buckets in one workspace | 20 | `LUMBUNG_WORKSPACE_MAX_BUCKETS` | `limit_reached` |
| Size of a workspace's branches together | 2 GiB | `LUMBUNG_WORKSPACE_MAX_DATABASE_BYTES` | `workspace_quota_exceeded` (507) |
| Size of a workspace's buckets together, counting multipart uploads in progress | 5 GiB | `LUMBUNG_WORKSPACE_MAX_OBJECT_BYTES` | `workspace_quota_exceeded` (507), `QuotaExceeded` on the S3 API |
| Live tokens in one workspace | 1,000 | `LUMBUNG_MAX_TOKENS_PER_WORKSPACE` | `limit_reached` |
| Workspaces, counting the trash | 100 | `LUMBUNG_MAX_WORKSPACES` | `limit_reached` |

The default workspace starts with no limits of its own, so an instance that never makes a workspace is bound by the instance limits alone.

The byte limits work like the instance's: a write, a branch copy, an import or an upload that would pass one is refused before anything is kept, and reads and deletes keep working. Revoked tokens are cleared after `LUMBUNG_TRASH_RETENTION`; until then a workspace holds at most ten times its live-token limit in token records, so minting and revoking in a loop hits `limit_reached` too.

## Requests from the dashboard

The dashboard uses one token, the service token, for everyone who signs in. Its requests are counted per person, by the name it sends in `X-Lumbung-Actor`, so one person can't use up the rate limit for the rest. Only the service token is counted this way.

## Connections

Each open branch has one writer connection and up to `LUMBUNG_READERS_PER_BRANCH` (4) read-only ones. Writes to one branch run one at a time; reads run alongside them. At most `LUMBUNG_MAX_OPEN_BRANCHES` (64) branches keep connections open; the least recently used idle one closes to make room, and any branch idle for `LUMBUNG_IDLE_CONN_TIMEOUT` (5 minutes) closes on its own. A closed branch reopens on the next request.
