---
title: What is Lumbung
description: A self-hosted server for many SQLite databases, each with branches.
---

Lumbung runs on your own machine and holds many SQLite databases. Each database has branches: full copies you can change without touching the default branch.

You reach every database through:

- an [HTTP API](/docs/reference/rest-api/) under `/v1`
- the `@lumbung/client` [SDK](/docs/reference/sdk/) for TypeScript
- the [libSQL protocol](/docs/guides/libsql/), for `@libsql/client` and Drizzle
- the `lumbung` [CLI](/docs/reference/cli/)
- an [MCP endpoint](/docs/guides/ai-agents/) at `/mcp` for AI agents
- a [web dashboard](/docs/guides/dashboard/)

Files go in [buckets](/docs/concepts/buckets/), with an [S3-compatible API](/docs/reference/s3-api/).

One instance is one team. There's no public sign-up and no billing.
