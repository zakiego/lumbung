---
title: Workspaces
description: A workspace owns databases, buckets and tokens. A token for a workspace manages what is inside it and can't tell that anything outside exists.
sidebar:
  order: 4.5
---

A workspace is a space that owns databases, buckets and tokens. Give an app or an AI agent a token for a workspace and it can make as many databases and buckets as it needs, by itself, and reach only its own.

Every instance starts with one workspace, named `default`. If you never make another, you never have to think about workspaces: everything lives there.

## Make a workspace and its first token

Making a workspace takes an instance admin token, such as the one you logged in with. So does making the workspace's first token, with `--workspace` to say where:

```sh
lumbung workspace create agent
(umask 077; lumbung token create --name agent --access admin --workspace agent > agent.token)
```

From here the workspace runs itself. Its token creates what it needs with the usual commands and URLs, with no workspace in them:

```sh
export LUMBUNG_TOKEN=$(cat agent.token)
lumbung db create notes
lumbung bucket create files
lumbung db list
```

The list holds `notes` and nothing else. The `app` database in the default workspace isn't in it, and asking for it by name or by id answers `database_not_found`, the same as a database that doesn't exist.

The same token works unchanged with the SDK, an S3 client, the libSQL client and MCP. Over REST:

```sh
curl -s http://127.0.0.1:4870/v1/databases -H "Authorization: Bearer $(cat agent.token)" | jq -r '.data[].name'
```

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

| Short URL | In workspace `agent` |
|---|---|
| `/v1/databases/notes/query` | `/v1/workspaces/agent/databases/notes/query` |
| `/v1/buckets`, `/v1/tokens`, `/v1/audit`, `/v1/trash`, `/v1/workspace` | `/v1/workspaces/agent/buckets`, and so on |
| `/v1/s3/files/a.txt` | `/v1/workspaces/agent/s3/files/a.txt` |
| `/libsql/notes/main/` | `/v1/workspaces/agent/libsql/notes/main/` |
| `/mcp` | `/v1/workspaces/agent/mcp` |

An instance token uses these to work inside a workspace. The CLI's `--workspace` flag (or `LUMBUNG_WORKSPACE`) and the SDK's `workspace` option add the prefix for you. `lumbung login --workspace` saves it in the profile, where it goes with the saved token only: a command run with another `--token` doesn't inherit it. Before a delete or a move, the CLI's prompt names the workspace it acts in. A public link to a bucket outside the default workspace uses the S3 one. A workspace token may use them for its own workspace, where they mean the same as the short URL. For any other workspace it gets `workspace_not_found`, the answer a workspace that doesn't exist gets.

## Limits

Each workspace has its own limits, on top of the [instance limits](/docs/concepts/limits/): how many databases (counting its trash) and buckets it may hold, and how many bytes its databases and its buckets may take. `0` means no limit of its own.

```sh
lumbung workspace create reports --max-databases 5 --max-object-bytes 1GiB
lumbung workspace update agent --max-databases 50 --max-buckets 10
```

A new workspace gets the limits from the request, or from the `LUMBUNG_WORKSPACE_MAX_*` [settings](/docs/reference/configuration/). The default workspace starts with none. A workspace also holds at most `LUMBUNG_MAX_TOKENS_PER_WORKSPACE` live tokens. A write past a byte limit fails with `workspace_quota_exceeded`, and nothing of it is kept.

`lumbung workspace show` (`GET /v1/workspace`) shows the workspace a token is in. A token for the whole workspace also gets its limits and what it uses now. A token for one database or bucket doesn't: the totals would tell it what else the workspace holds.

```sh
export LUMBUNG_TOKEN=$(cat agent.token)
lumbung workspace show
```

## Deleting and restoring

Deleting a workspace takes an instance admin and moves it to the trash:

```sh
lumbung workspace create scratch
lumbung workspace delete scratch --yes
```

At once its tokens stop working, its databases and buckets stop answering (public links and presigned URLs too), and open connections are closed. A token of a deleted workspace gets the answer a token that doesn't exist gets. The name is free to reuse.

Until the trash retention passes (`LUMBUNG_TRASH_RETENTION`, 7 days by default) it can be restored with everything in it, tokens included. Pass `--name` if the old one was taken in the meantime:

```sh
lumbung workspace trash
lumbung workspace restore scratch
```

After the retention the workspace is removed for good: its databases, snapshots, objects and tokens. Its audit entries stay in the instance log. The default workspace can't be deleted.

## Moving a database or a bucket

An instance admin can move a database or a bucket into another workspace, for example to give something that started in the default workspace a workspace of its own:

```sh
lumbung db move app --to agent --yes
```

The command acts where the database is now (here the default workspace; add `--workspace` for another). `lumbung bucket move` does the same for a bucket. Over REST they are `POST /v1/databases/<db>/move` and `POST /v1/buckets/<bucket>/move` with `{"workspace": "agent"}`.

- The move fails if the name is taken in the new workspace, or one of its limits would be passed.
- Every token that reached the database or bucket is revoked, whole: a token of the old workspace can't reach across. Make new ones in the new workspace.
- Branches, snapshots, objects and unfinished uploads move with it. Nothing is copied.

## Audit log

Every management action is recorded in the workspace it happened in. `GET /v1/audit` is that workspace's log, for its admins. An instance admin reads everything, with the actions that belong to no workspace (making workspaces and instance tokens), at `GET /v1/instance/audit`.
