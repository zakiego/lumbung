---
title: Changelog
description: What changed in each version of Lumbung.
---

## Unreleased

### Workspaces

- **Workspaces** own databases, buckets and tokens. A workspace token manages what is inside its workspace and can't tell that anything outside exists. Database and bucket names are unique per workspace. Every instance has a `default` workspace, and an upgrade puts everything that exists into it.
- **Workspace tokens**: read, write or admin on everything in one workspace. Admin creates and deletes databases and buckets, which used to need an instance admin.
- **Addressing**: URLs don't change for a workspace token. `/v1/workspaces/{workspace}/...` names a workspace on REST, the S3 API, the libSQL endpoint and MCP.
- **Limits per workspace** on databases, buckets and live tokens, and an audit log per workspace.
- The database list no longer tells a token limited to some branches about the branches it can't reach.

Changed behavior to check when upgrading:

- `POST /v1/tokens` (and `lumbung token create`) without a database, a bucket or grants makes a **workspace token** in the request's workspace. It used to make an instance token. Instance tokens are made at `POST /v1/instance/tokens`.
- `GET /v1/audit` is the request's workspace. The log of the whole instance is `GET /v1/instance/audit`.
- The service token and other instance tokens are listed at `GET /v1/instance/tokens`, not in `GET /v1/tokens`.
- Database, bucket, token and audit bodies have a `workspace_id`, and a token's `scope` can be `workspace`.
- `LUMBUNG_AUDIT_MAX_ROWS` now applies to each workspace's entries.
- Existing tokens keep working. Instance tokens stay instance tokens and act in the default workspace on the usual URLs.

### The first version

- **Engine**: many SQLite databases on one server, each with branches, under a layered security model (authorizer, confined file access, read-only connections, limits) tested against an isolation table and a fuzz test.
- **Branches**: create from a branch or snapshot, rename, reset, promote, protect, expire, schema diff. Copies use SQLite's online backup, so writers keep going.
- **Snapshots**: manual and scheduled, with retention; restore always makes a new branch.
- **Trash**: deleted databases can be restored for 7 days.
- **Import and download**: SQLite files and SQL dumps.
- **REST API** under `/v1` with an OpenAPI document, typed values that never lose precision, problem+json errors with stable codes, cursor pagination, idempotency keys, an audit log and per-branch stats.
- **Tokens**: instance, database and branch scopes with read, write and admin access, expiry and rate limits.
- **MCP** at `/mcp` for AI agents, with annotated tools and results capped for a model's context.
- **libSQL protocol** at `/libsql/{db}/{branch}/`: `@libsql/client` and Drizzle's libsql driver work against a branch by URL, including interactive transactions.
- **TypeScript SDK** `@lumbung/client` with a Drizzle adapter, and the **`lumbung` CLI** with migrations and a SQL shell.
- **Backups**: `lumbungd backup` and `lumbungd restore`.
