---
title: Changelog
description: What changed in each version of Lumbung.
---

## Unreleased

### MCP playbook

- **`get_playbook`**: a read-only MCP tool that returns the operating rules for agents (token levels, query versus execute, branches, the trash, result caps and error codes), stamped with a version hash. The server instructions are now short and point at it with the same version.

### Workspaces

- **Workspaces** own databases, buckets and tokens. A workspace token manages what is inside its workspace and can't tell that anything outside exists. Database and bucket names are unique per workspace. Every instance has a `default` workspace, and an upgrade puts everything that exists into it.
- **Workspace tokens**: read, write or admin on everything in one workspace. Admin creates and deletes databases and buckets, which used to need an instance admin.
- **Addressing**: URLs don't change for a workspace token. `/v1/workspaces/{workspace}/...` names a workspace on REST, the S3 API, the libSQL endpoint and MCP.
- **Deleting a workspace** moves it to the trash with everything in it, restorable for 7 days. **Moving** a database or a bucket to another workspace takes an instance admin and revokes the tokens that reached it.
- **SDK** `@lumbung/client` 0.2.0: a `workspace` option, `client.workspaces`, `client.instance`, and `move` on databases and buckets. It has breaking changes, listed in the package's `CHANGELOG.md`.
- **CLI**: `lumbung workspace list|show|create|update|delete|restore|trash`, `--workspace` (or `LUMBUNG_WORKSPACE`, or saved by `lumbung login --workspace`), `db move`, `bucket move`, and `--instance` on the token commands.
- **MCP**: `get_workspace`, `delete_database`, `restore_database`, `delete_bucket`, `restore_snapshot` and `delete_snapshot`.
- **Limits per workspace** on databases, buckets, the bytes its databases and its buckets take, and live tokens. An audit log per workspace.
- **Dashboard**: a workspace switcher, a page per workspace for tokens, the audit log, the trash and settings (usage, limits, delete), restoring deleted workspaces, and moving a database or a bucket to another workspace. People get a role per workspace (admin, member or read-only) and see only the workspaces they have one in. The engine holds each person to their role, not only the dashboard.
- The database list no longer tells a token limited to some branches about the branches it can't reach.

Changed behavior to check when upgrading:

- `POST /v1/tokens` (and `lumbung token create`) without a database, a bucket or grants makes a **workspace token** in the request's workspace. It used to make an instance token. Instance tokens are made at `POST /v1/instance/tokens`.
- `GET /v1/audit` is the request's workspace. The log of the whole instance is `GET /v1/instance/audit`, which is also where entries written before the upgrade stay.
- Revoked tokens are removed after `LUMBUNG_TRASH_RETENTION`. They were kept forever before.
- The service token and other instance tokens are listed at `GET /v1/instance/tokens`, not in `GET /v1/tokens`.
- Database, bucket, token and audit bodies have a `workspace_id`, and a token's `scope` can be `workspace`.
- `LUMBUNG_AUDIT_MAX_ROWS` now applies to each workspace's entries.
- Existing tokens keep working. Instance tokens stay instance tokens and act in the default workspace on the usual URLs.
- Dashboard addresses are `/app/w/<workspace>/...`. The old ones open the same page in the default workspace.
- Dashboard members and read-only people keep their role, in the default workspace. Creating, renaming and deleting a database and reading the audit log now take an admin of the workspace, where a member could do them before.

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
