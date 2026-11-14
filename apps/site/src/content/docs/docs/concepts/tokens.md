---
title: Tokens and scopes
description: How API tokens look, what each scope and access level allows, and how the dashboard's service token works.
sidebar:
  order: 5
---

Every request carries a token:

```sh
curl -H "Authorization: Bearer $LUMBUNG_TOKEN" http://127.0.0.1:4870/v1/databases
```

A token looks like `lmb_<id>_<secret>`. Lumbung stores only a SHA-256 hash of it and shows the full token once, when it is created. The id part is public: it names the token in lists and the audit log.

## Scope and access

A token has a **scope**, the whole instance, one database (optionally one branch of it) or one bucket, and an **access** level: `read`, `write` or `admin`.

| Can it... | read | write | admin |
|---|---|---|---|
| Run SELECT, read schema, diff, stats, snapshots, download | yes | yes | yes |
| Run writes (on the branch's writer connection) | no | yes | yes |
| Create branches and snapshots, reset branches (needs the whole database), import into an empty branch, restore a snapshot into a new branch | no | yes | yes |
| Delete, rename, protect or promote branches, set snapshot schedules, delete snapshots, import with `mode=replace` | no | no | yes |
| Create and revoke tokens within its own scope | no | no | yes |

On top of that, only an **instance admin** token (instance scope, admin access) can create, rename, delete and restore databases, list the trash and read the audit log.

A read token runs every statement on a read-only connection, so SQLite itself refuses writes. A write token can ask for the same with `"read_only": true` in a query.

### Database and branch tokens

A database token only sees its database: other databases answer `database_not_found`, as if they didn't exist. A token limited to one branch can query, download, snapshot and read the schema of that branch only. Every other branch, and any branch name that doesn't exist, answers `forbidden`, so it can't probe for names. It can't list branches, read the database's details, reset its branch (a reset copies the parent's data in), or reach the default branch through `/v1/databases/{db}/query` unless that branch is the default.

When a branch is deleted, tokens limited to it are revoked. When a database is purged from the trash, its tokens are removed.

### Bucket tokens

A bucket token reaches one [bucket](/docs/concepts/buckets/) and nothing else: other buckets answer `bucket_not_found` (`NoSuchBucket` on the S3 API), and it sees no databases. A database token sees no buckets. Instance tokens reach every bucket at their access level.

| On a bucket, can it... | read | write | admin |
|---|---|---|---|
| List, download, read metadata, presign a GET | yes | yes | yes |
| Upload, delete, copy into the bucket, multipart uploads, presign a PUT | no | yes | yes |
| Rename, set public read and CORS origins | no | no | yes |
| Create and revoke tokens for the bucket | no | no | yes |

Creating and deleting buckets needs an instance admin token. Copying from another bucket needs read on that bucket too, in the same token. When a bucket is deleted, its tokens are revoked.

## Access keys

S3 clients don't send a token; they sign each request with an access key id and a secret. Ask for an access key when you create a token, and both come back once, next to the token:

```sh
lumbung bucket create photos
lumbung token create --name uploads --bucket photos --access write --access-key
```

The access key id is `LMB` followed by the token's id in capitals. The key has the token's scope, access, expiry and rate limit, and revoking the token ends the key and every presigned URL it signed. Lumbung needs the secret to check signatures, so unlike tokens it is stored, encrypted with a key kept in `secrets/access-key-encryption-key` in the data folder. A copy of `meta.db` alone doesn't reveal any secret.

## Expiry and limits

A token can expire after a number of days. A token that expires can't create one that outlives it: the new token's expiry is capped at its creator's. Revoking a token doesn't revoke the tokens it created; revoke those too.

Each token is rate limited (`LUMBUNG_RATE_LIMIT`, `LUMBUNG_RATE_BURST`). Failed authentications are limited per client address (`LUMBUNG_AUTH_FAILURES_PER_MINUTE`): past the limit, bad tokens from that address get `rate_limited`, while valid tokens keep working, so a flood of bad requests through a shared proxy can't lock real clients out. Both answer with a `Retry-After` header. Behind a proxy, set `LUMBUNG_CLIENT_IP_HEADER` and `LUMBUNG_TRUSTED_PROXIES` so each client gets its own limit; `X-Forwarded-For` is read from the right, skipping trusted proxies.

Creating a token takes no `Idempotency-Key`: replaying the response would mean storing the secret, which is only ever shown once.

## The service token

On first start lumbungd creates one instance admin token for the dashboard and writes it to `secrets/service-token` in the data folder, readable only by its owner. Set `LUMBUNG_SERVICE_TOKEN` to use your own instead. The service token can't be revoked through the API; replace it by changing the variable or the file and restarting.

The dashboard sends `X-Lumbung-Actor` with the signed-in user, so the [audit log](/docs/reference/rest-api/#read-the-audit-log) records who acted. The header is ignored on every other token.
