---
title: Workspaces
description: A workspace owns databases, buckets and tokens. A token for a workspace manages what is inside it and can't tell that anything outside exists.
sidebar:
  order: 4.5
---

A workspace is a space that owns databases, buckets and tokens. Give an app or an AI agent a token for a workspace and it can make as many databases and buckets as it needs, by itself, and reach only its own.

Every instance starts with one workspace, named `default`. If you never make another, you never have to think about workspaces: everything lives there.

## Make a workspace and its first token

Making a workspace takes an instance admin token, such as the one you logged in with. So does making the workspace's first token, through a URL that names the workspace:

```sh
curl -s -X POST http://127.0.0.1:4870/v1/workspaces \
  -H "Authorization: Bearer $LUMBUNG_TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "palui"}'

PALUI_TOKEN=$(curl -s -X POST http://127.0.0.1:4870/v1/workspaces/palui/tokens \
  -H "Authorization: Bearer $LUMBUNG_TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "agent", "access": "admin"}' | jq -r .token)
echo "$PALUI_TOKEN" > palui.token
```

From here the workspace runs itself. Its token creates what it needs on the usual URLs, with no workspace in them:

```sh
PALUI_TOKEN=$(cat palui.token)
curl -s -X POST http://127.0.0.1:4870/v1/databases \
  -H "Authorization: Bearer $PALUI_TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "notes"}' | jq -r .name

curl -s -X POST http://127.0.0.1:4870/v1/buckets \
  -H "Authorization: Bearer $PALUI_TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "files"}' | jq -r .name

curl -s http://127.0.0.1:4870/v1/databases -H "Authorization: Bearer $PALUI_TOKEN" | jq -r '.data[].name'
```

The last command prints `notes` and nothing else. The `app` database in the default workspace isn't in the list, and asking for it by name or by id answers `database_not_found`, the same as a database that doesn't exist.

## What a workspace token can do

A workspace token has one level for everything in its workspace, including what is created later.

| Can it... | read | write | admin |
|---|---|---|---|
| List and read every database, branch, snapshot, bucket and object | yes | yes | yes |
| Run writes, upload and delete objects, create branches and snapshots | no | yes | yes |
| Create, rename, delete and restore databases and buckets, change bucket settings | no | no | yes |
| Create and revoke the workspace's tokens, read its audit log, see its trash | no | no | yes |
| Change the workspace's limits, rename or delete it, see or make other workspaces | no | no | no |

The last row is for an instance admin only. The limits are the operator's hold on a workspace, so its own admin can't lift them.

Tokens for [one database, one branch, one bucket or several grants](/docs/concepts/tokens/) work as before, inside one workspace. An admin of a workspace can make them for anything in it.

## Names

Workspace names follow the database rule (`[a-z0-9][a-z0-9-]{0,62}`) and are unique on the instance. A workspace also has an id, like `ws_k3x9q2m7v4p8w1z6c5n0`, which never changes.

Database and bucket names are unique inside a workspace, not across the instance. Two workspaces can each have a database called `notes`. Creating one never fails, or says anything, because of a name another workspace uses.

## Which workspace a request acts in

The token decides.

- A token that belongs to a workspace always acts in that workspace. This is true on every door: REST, the SDK and CLI, the [libSQL endpoint](/docs/guides/libsql/), the [S3 API](/docs/reference/s3-api/) and [MCP](/docs/guides/ai-agents/). URLs don't change: `/v1/databases/notes`, `/libsql/notes/main/`, `/v1/s3/files/a.txt` and `/mcp` each mean "in my workspace".
- An instance token belongs to no workspace. On those URLs it acts in the default workspace.
- A request without a token (reading a [public bucket](/docs/concepts/buckets/)) reaches the default workspace too.

To name a workspace, put `/v1/workspaces/<workspace>` in front. The name or the id both work:

| Short URL | In workspace `palui` |
|---|---|
| `/v1/databases/notes/query` | `/v1/workspaces/palui/databases/notes/query` |
| `/v1/buckets`, `/v1/tokens`, `/v1/audit`, `/v1/trash` | `/v1/workspaces/palui/buckets`, and so on |
| `/v1/s3/files/a.txt` | `/v1/workspaces/palui/s3/files/a.txt` |
| `/libsql/notes/main/` | `/v1/workspaces/palui/libsql/notes/main/` |
| `/mcp` | `/v1/workspaces/palui/mcp` |

An instance token uses these to work inside a workspace. A public link to a bucket outside the default workspace uses the S3 one. A workspace token may use them for its own workspace, where they mean the same as the short URL. For any other workspace it gets `workspace_not_found`, the answer a workspace that doesn't exist gets.

## Limits

Each workspace has its own limits, on top of the [instance limits](/docs/concepts/limits/): how many databases (counting its trash) and buckets it may hold, and how many bytes its databases and its buckets may take. `0` means no limit of its own.

```sh
curl -s -X PATCH http://127.0.0.1:4870/v1/workspaces/palui \
  -H "Authorization: Bearer $LUMBUNG_TOKEN" -H "Content-Type: application/json" \
  -d '{"max_databases": 50, "max_buckets": 10}' | jq .limits
```

A new workspace gets the limits from the request, or from the `LUMBUNG_WORKSPACE_MAX_*` [settings](/docs/reference/configuration/). The default workspace starts with none. A workspace also holds at most `LUMBUNG_MAX_TOKENS_PER_WORKSPACE` live tokens.

`GET /v1/workspace` shows the workspace a token is in, with its limits and what it uses now:

```sh
PALUI_TOKEN=$(cat palui.token)
curl -s http://127.0.0.1:4870/v1/workspace -H "Authorization: Bearer $PALUI_TOKEN" | jq '{name, limits, usage}'
```

## Audit log

Every management action is recorded in the workspace it happened in. `GET /v1/audit` is that workspace's log, for its admins. An instance admin reads everything, with the actions that belong to no workspace (making workspaces and instance tokens), at `GET /v1/instance/audit`.
