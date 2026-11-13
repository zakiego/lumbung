---
title: Monitoring
description: The health check, per-branch stats, the slow-query log, the audit log and the engine's logs.
sidebar:
  order: 5
---

## Health

`GET /health` needs no token. It answers 200 only when the engine can serve, and 503 otherwise, for example when `meta.db` is newer than the binary.

```sh
curl -s http://127.0.0.1:4870/health
```

Point your uptime checker at it. Under Docker, the image's health check uses the same endpoint.

## Branch stats

Each branch counts queries, rows read and written, errors and query durations in one-minute buckets, kept for `LUMBUNG_STATS_RETENTION` (48 hours).

```sh
curl -s "http://127.0.0.1:4870/v1/databases/app/branches/main/stats?window=1h" \
  -H "Authorization: Bearer $LUMBUNG_TOKEN"
```

The answer has totals, p50, p95 and p99 durations, a series per minute (per 15 minutes for longer windows), the branch's size on disk, and its slow-query log: the newest 50 queries slower than `LUMBUNG_SLOW_QUERY` (1 second).

## Audit log

Every management action (creating, renaming, deleting databases, branches, snapshots and tokens) is recorded with the token, the dashboard user when there is one, the result and the error code. Instance admins can read it:

```sh
curl -s "http://127.0.0.1:4870/v1/audit?limit=20" -H "Authorization: Bearer $LUMBUNG_TOKEN"
```

The newest `LUMBUNG_AUDIT_MAX_ROWS` (100,000) entries are kept.

## Logs

lumbungd writes JSON lines to standard error. Errors carry the request id that clients also get in `X-Request-Id` and in every error body, so a user's report leads straight to the log line.

## Disk

The engine refuses to create databases, branches, snapshots and imports when the disk has less than `LUMBUNG_MIN_FREE_DISK` (2 GiB) free, and stops writes past `LUMBUNG_MAX_INSTANCE_BYTES`. Alert on free disk well above that threshold.
