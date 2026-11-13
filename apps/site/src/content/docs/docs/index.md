---
title: What is Lumbung
description: A self-hosted server for many SQLite databases, each with branches.
---

Lumbung runs on your own machine and holds many SQLite databases. Each database has branches: full copies you can change without touching the default branch.

You reach every database through:

- an HTTP API under `/v1`
- the `@lumbung/client` TypeScript SDK
- the `lumbung` CLI
- an MCP endpoint at `/mcp` for AI agents
- a web dashboard

One instance is one team. There's no public sign-up and no billing.

This documentation grows with each release. The quickstart, concepts and reference pages arrive as the features they describe ship.
