---
title: Use from an AI agent
description: Connect an MCP client, such as Claude Code, to Lumbung's /mcp endpoint and give the agent a safely scoped token.
sidebar:
  order: 9
---

The engine serves the Model Context Protocol at `/mcp` (streamable HTTP). Agents get tools to list and create databases and branches, read schemas, run read-only queries, run writes, branch, diff and snapshot, and to create buckets and list, read, write and presign objects in them. The [MCP tools reference](/docs/reference/mcp-tools/) lists them all.

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

### A database and a bucket for one agent

An agent that keeps its own rows and its own files needs both, and nothing else. Give it one token with a [grant](/docs/concepts/tokens/#tokens-with-several-grants) for each, at the level each needs:

```sh
lumbung db create agent-notes
lumbung bucket create agent-files
lumbung token create --name agent --grant db=agent-notes:write --grant bucket=agent-files:write
```

Leave out either grant for an agent that only needs a database or only needs files. Other databases and buckets on the instance don't exist as far as this token can tell.

In the dashboard, the same thing is **Tokens**, **New token**, then **Add another** for each extra database or bucket. The new token is shown once, with the MCP URL to paste next to it.

### An agent that makes its own databases and buckets

An agent that should decide for itself what it keeps gets its own [workspace](/docs/concepts/workspaces/). With an admin token for it, the agent creates as many databases and buckets as it needs (`create_database`, `create_bucket`) and uses them, and nothing outside the workspace exists as far as it can tell. The workspace's limits bound how much it can take.

```sh
lumbung workspace create agent --max-databases 20 --max-buckets 5 --max-database-bytes 2GiB --max-object-bytes 5GiB
lumbung token create --name agent --access admin --workspace agent
```

The token works on the same MCP URL as any other. The agent doesn't need to know its workspace's name. `get_workspace` tells it the limits and how much is used, `delete_database` moves a database to the trash (`restore_database` brings it back for 7 days), and `delete_bucket` removes an empty bucket.

## 2. Connect the client

A client asks for two things: the server URL, which is the engine's address followed by `/mcp`, and the token, sent as a bearer token.

Claude Code:

<!-- docs-test: skip needs the Claude Code CLI, which CI does not have -->
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

## 3. The agent reads the playbook

The server's instructions tell the agent to call `get_playbook` before its first tool call. It returns the rules the tool schemas can't carry: what each token level can do, that `query` never writes, that branches never merge back and the safe order for a schema change, what goes to the trash, the result caps, and what each error code means.

The result starts with `Playbook version:` and 8 hex characters, a hash of the rules. The instructions carry the same version, so an agent holding rules from an earlier conversation can tell they're stale without calling the tool. The version changes only when the rules do.

## What the agent can and can't do

- `query` always runs on a read-only connection, whatever the token allows. Writes go through `execute`, which needs a write token.
- Object tools follow the token's level on that bucket: `list_objects`, `get_object` and `presign_object` for a GET need read, and `put_object` and `delete_object` need write.
- Every statement follows the same [security model](/docs/concepts/security-model/) as the REST API: no ATTACH, no VACUUM INTO, no extension loading, no reaching outside the branch.
- Results are capped for a model's context: at most 100 rows by default (`max_rows` up to 1,000) and about 64 KB, measured after long text (2,000 characters shown) and blobs (48 bytes shown) are shortened. A cut result says so and suggests narrowing the query. Rows returned by a write are capped the same way, and the write still applies to every row.
- Lists (databases, branches, schema changes) stop at 200 items. `get_schema` takes a `table` to show one table, and a schema over about 64 KB comes back as names only.
- Tool arguments are JSON numbers, which can't hold every integer past 2^53. Pass those as strings, like `"9007199254740993"`; SQLite still compares them as numbers in an INTEGER column.
- Each tool is annotated as read-only, additive or destructive, so clients can ask before running `execute`, `delete_branch`, `reset_branch`, `delete_database`, `delete_bucket` or `delete_snapshot`.
- Management actions taken through MCP (creating databases, buckets, branches and snapshots, resetting and deleting branches) are in the audit log of the agent's workspace, under its token.
- Creating a database or a bucket needs an admin token for the whole workspace. A token for one database or bucket, or a token with grants, can't.

A good loop for schema changes is the same one people use: `create_branch`, `execute` the migration on it, `diff_branches` against `main`, then run the same SQL on `main`.
