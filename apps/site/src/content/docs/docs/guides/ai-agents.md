---
title: Use from an AI agent
description: Connect an MCP client, such as Claude Code, to Lumbung's /mcp endpoint and give the agent a safely scoped token.
sidebar:
  order: 9
---

The engine serves the Model Context Protocol at `/mcp` (streamable HTTP). Agents get tools to list databases and branches, read schemas, run read-only queries, run writes, branch, diff and snapshot. The [MCP tools reference](/docs/reference/mcp-tools/) lists them all.

## 1. Make a token for the agent

Give the agent the least it needs. A read-only token on one database is a good start:

```sh
lumbung token create --name agent --db app --access read --expires-in-days 30
```

For an agent that may change data, use `--access write`, and consider limiting it to a branch with `--branch` so it can't touch the default branch:

```sh
lumbung branch create agent-sandbox --db app
lumbung token create --name agent --db app --branch agent-sandbox --access write
```

## 2. Connect the client

Claude Code:

```sh
claude mcp add --transport http lumbung http://127.0.0.1:4870/mcp --header "Authorization: Bearer <token>"
```

Any client that reads an `mcpServers` config:

```json
{
  "mcpServers": {
    "lumbung": {
      "type": "http",
      "url": "http://127.0.0.1:4870/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

## What the agent can and can't do

- `query` always runs on a read-only connection, whatever the token allows. Writes go through `execute`, which needs a write token.
- Every statement follows the same [security model](/docs/concepts/security-model/) as the REST API: no ATTACH, no VACUUM INTO, no extension loading, no reaching outside the branch.
- Results are capped for a model's context: at most 100 rows by default (`max_rows` up to 1,000) and about 64 KB, with long text and blobs shortened. A cut result says so and suggests narrowing the query.
- Each tool is annotated as read-only, additive or destructive, so clients can ask before running `execute`, `delete_branch` or `reset_branch`.
- Management actions taken through MCP (creating databases, branches and snapshots, resetting and deleting branches) are in the audit log under the agent's token.

A good loop for schema changes is the same one people use: `create_branch`, `execute` the migration on it, `diff_branches` against `main`, then run the same SQL on `main`.
