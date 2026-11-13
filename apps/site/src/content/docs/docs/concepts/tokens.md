---
title: Tokens and scopes
description: How API tokens look, what each scope and access level allows, and how the dashboard's service token works.
sidebar:
  order: 4
---

Every request carries a token:

```sh
curl -H "Authorization: Bearer $LUMBUNG_TOKEN" http://127.0.0.1:4870/v1/databases
```

A token looks like `lmb_<id>_<secret>`. Lumbung stores only a SHA-256 hash of it and shows the full token once, when it is created. The id part is public: it names the token in lists and the audit log.

## Scope and access

A token has a **scope**, the whole instance or one database (optionally one branch of it), and an **access** level: `read`, `write` or `admin`.

| Can it... | read | write | admin |
|---|---|---|---|
| Run SELECT, read schema, diff, stats, snapshots, download | yes | yes | yes |
| Run writes (on the branch's writer connection) | no | yes | yes |
| Create branches and snapshots, reset branches, import into an empty branch, restore a snapshot into a new branch | no | yes | yes |
| Delete, rename, protect or promote branches, set snapshot schedules, delete snapshots, import with `mode=replace` | no | no | yes |
| Create and revoke tokens within its own scope | no | no | yes |

On top of that, only an **instance admin** token (instance scope, admin access) can create, rename, delete and restore databases, list the trash and read the audit log.

A read token runs every statement on a read-only connection, so SQLite itself refuses writes. A write token can ask for the same with `"read_only": true` in a query.

### Database and branch tokens

A database token only sees its database: other databases answer `database_not_found`, as if they didn't exist. A token limited to one branch can query, download, snapshot and read the schema of that branch only. It can't list branches or reach the default branch through `/v1/databases/{db}/query` unless that branch is the default.

When a branch is deleted, tokens limited to it are revoked. When a database is purged from the trash, its tokens are removed.

## Expiry and limits

A token can expire after a number of days. Each token is rate limited (`LUMBUNG_RATE_LIMIT`, `LUMBUNG_RATE_BURST`), and failed authentications are limited per client address (`LUMBUNG_AUTH_FAILURES_PER_MINUTE`). Both answer `rate_limited` with a `Retry-After` header.

## The service token

On first start lumbungd creates one instance admin token for the dashboard and writes it to `secrets/service-token` in the data folder, readable only by its owner. Set `LUMBUNG_SERVICE_TOKEN` to use your own instead. The service token can't be revoked through the API; replace it by changing the variable or the file and restarting.

The dashboard sends `X-Lumbung-Actor` with the signed-in user, so the [audit log](/docs/reference/rest-api/#read-the-audit-log) records who acted. The header is ignored on every other token.
