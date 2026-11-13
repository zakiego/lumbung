---
title: Limits
description: The limits on statements, results and storage, their defaults, and the error each one returns.
sidebar:
  order: 5
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

## Storage

| Limit | Default | Setting | Error |
|---|---|---|---|
| Size of one branch | 1 GiB | `LUMBUNG_MAX_BRANCH_BYTES` | `branch_size_limit` (507) |
| Size of all branches together | 10 GiB | `LUMBUNG_MAX_INSTANCE_BYTES` | `instance_quota_exceeded` (507) |
| Free disk before creating files | 2 GiB | `LUMBUNG_MIN_FREE_DISK` | `insufficient_disk` (507) |
| Databases | 100 | `LUMBUNG_MAX_DATABASES` | `limit_reached` |
| Branches per database | 50 | `LUMBUNG_MAX_BRANCHES` | `limit_reached` |

A full branch still serves reads and deletes, so you can make room.

## Connections

Each open branch has one writer connection and up to `LUMBUNG_READERS_PER_BRANCH` (4) read-only ones. Writes to one branch run one at a time; reads run alongside them. At most `LUMBUNG_MAX_OPEN_BRANCHES` (64) branches keep connections open; the least recently used idle one closes to make room, and any branch idle for `LUMBUNG_IDLE_CONN_TIMEOUT` (5 minutes) closes on its own. A closed branch reopens on the next request.
