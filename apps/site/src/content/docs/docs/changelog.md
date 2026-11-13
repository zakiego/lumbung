---
title: Changelog
description: What changed in each version of Lumbung.
---

## Unreleased

The first version.

- **Engine**: many SQLite databases on one server, each with branches, under a layered security model (authorizer, confined file access, read-only connections, limits) tested against an isolation table and a fuzz test.
- **Branches**: create from a branch or snapshot, rename, reset, promote, protect, expire, schema diff. Copies use SQLite's online backup, so writers keep going.
- **Snapshots**: manual and scheduled, with retention; restore always makes a new branch.
- **Trash**: deleted databases can be restored for 7 days.
- **Import and download**: SQLite files and SQL dumps.
- **REST API** under `/v1` with an OpenAPI document, typed values that never lose precision, problem+json errors with stable codes, cursor pagination, idempotency keys, an audit log and per-branch stats.
- **Tokens**: instance, database and branch scopes with read, write and admin access, expiry and rate limits.
- **MCP** at `/mcp` for AI agents, with annotated tools and results capped for a model's context.
- **TypeScript SDK** `@lumbung/client` with a Drizzle adapter, and the **`lumbung` CLI** with migrations and a SQL shell.
- **Backups**: `lumbungd backup` and `lumbungd restore`.
