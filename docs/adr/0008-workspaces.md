# 0008: Workspaces

Date: 2026-11-14. Status: accepted.

## Context

An app or an AI agent should be able to make as many databases and buckets as it needs, by itself, and reach only its own. Until now it couldn't. Creating a database or a bucket took an instance admin token, and that token reaches everything on the instance. A token with grants (ADR 0007) reaches a fixed list of things somebody else created for it, so it can't grow.

The missing piece is a unit between the instance and a single database: a space that owns databases, buckets and tokens, where a token for the space manages what is inside and can't tell that anything outside exists.

The instance is live with real data: databases, buckets, a public bucket with links in use, an instance admin token, a grants token and the dashboard's service token. `@lumbung/client` is on npm.

## Decision

### Model

A **workspace** owns databases, buckets and tokens. Every database and bucket belongs to exactly one workspace, and so does every token below the instance. Ids are `ws_...`. Names match the database name pattern and are unique on the instance. Database names are unique among the live databases of one workspace, bucket names among the buckets of one workspace, so two workspaces can each have a database called `notes`.

Every instance has one **default workspace**, made by the migration (and on a fresh install), which can be renamed but not deleted. A single-team instance never has to think about workspaces: everything lives in the default one until someone makes another.

Files on disk don't change. They are named by database, branch, bucket and blob ids, which stay unique on the instance, so a workspace is a column in meta.db and not a folder.

Considered: making a workspace a naming prefix on top of grants (a token that may create anything named `palui-*`). Rejected: names would stay global, a failed create would reveal names other tenants took, and quotas, audit and deletion would have nothing to hang on.

Considered: one meta.db per workspace. It isolates further, but backups, the token lookup and the instance limits would all need a view across files, and ADR 0006 already chose one index for the same reason.

### Tokens

Scopes are now `instance`, `workspace`, `database`, `bucket` and `grants`.

- `workspace` is new: read, write or admin on everything in one workspace, including what is created later. read lists and reads. write also changes data: SQL writes, objects, branches and snapshots as a database write token can today. admin also creates, renames, deletes and restores databases and buckets, changes bucket settings, manages the workspace's tokens and reads its audit log.
- `database`, `bucket` and `grants` are what they were, inside one workspace. Every grant of a token names something in the token's own workspace.
- `instance` tokens belong to no workspace. They operate the instance: workspaces and their limits, instance tokens, the instance audit log, moving a database or bucket between workspaces. They can also act inside any workspace, at their level.

A workspace admin can't change its workspace's limits, rename or delete it, or see or make other workspaces. Those are the operator's controls on the tenant, so they stay with instance admins.

Considered: a level between write and admin that may create databases and buckets but not delete them or mint tokens. Rejected for now: an agent's workspace is its sandbox, the quotas bound what it can use, deleting a database is undoable, and three levels with one meaning everywhere are easier to hold than four. It can be added without a migration if a real case shows up.

`POST /v1/tokens` without a target used to make an instance token. It now makes a workspace token in the request's workspace. Instance tokens are made at `POST /v1/instance/tokens`, by an instance admin. This is a breaking change on purpose: the old meaning was "everything that exists", and silently keeping it would hand out reach over every future workspace.

### One place decides

`auth.Principal` gains `WorkspaceID`, and every request principal has exactly one: a workspace-bound token's own, or for an instance token the workspace the request resolved to. Every check takes the record it is about (`meta.Database`, `meta.Bucket`, `meta.Token`) and first compares its workspace with the principal's. So the rule "a principal reaches nothing outside its request's workspace" holds for every scope, instance included, in one file, and a handler that forgets to scope a lookup still fails closed.

Lookups by name or id in meta take the workspace as an argument. A database id from another workspace is not found, the same as an id that doesn't exist.

### Addressing

Two designs were weighed.

**The workspace in every path** (`/v1/workspaces/{ws}/databases/notes`, `/libsql/{ws}/{db}/{branch}`, an S3 endpoint per workspace). URLs describe themselves, and requests with no credential can still say where they are going. But every client must then know its workspace's name next to a token that already determines it. That is a second fact that can disagree with the first, a new place to probe ("does workspace X exist?") that must answer exactly like a missing one, and a rename breaks every URL. It also breaks every URL in use today: SDK and CLI calls, libSQL URLs, S3 endpoints, public object links and the MCP URL an agent was configured with.

**The workspace from the token.** A token belongs to one workspace, so `/v1/databases/notes` resolves there. Nothing a workspace token can send names another workspace, so there is no cross-workspace request to refuse. Existing URLs keep working, and an MCP connector needs a new token and nothing else. What it can't serve by itself: requests without a token (public object reads, CORS preflights) and instance tokens, which belong to no workspace.

Picked: **the token decides, and a qualified URL exists for callers that have no workspace of their own.**

- Short URLs (`/v1/databases/...`, `/v1/buckets/...`, `/v1/tokens`, `/v1/audit`, `/v1/trash`, `/v1/s3/...`, `/libsql/...`, `/mcp`) resolve in the caller's workspace. For a request with no workspace of its own (an instance token, an anonymous request) that is the default workspace.
- Qualified URLs put `/v1/workspaces/{ws}` in front of the same paths, for every door: `/v1/workspaces/acme/databases`, `/v1/workspaces/acme/s3/photos/cat.jpg`, `/v1/workspaces/acme/libsql/app/main/`, `/v1/workspaces/acme/mcp`. `{ws}` is a name or an id. They sit under `/v1`, so no proxy rule changes.
- A workspace-bound token may use a qualified URL only for its own workspace. For any other value it gets `workspace_not_found`, the same answer as a workspace that doesn't exist.

The qualified form is one mechanism instead of a header because several clients can only be given a URL: a browser following a public link, `@libsql/client`, an S3 client's endpoint, an MCP connector. Every one of them already takes a base URL.

The default-workspace rule for callers without a workspace keeps the instance admin token and every public link working unchanged, and keeps the quickstart at "make a token, create a database". Its cost: an operator who forgets the workspace acts in the default one. That is the same trade a default namespace makes elsewhere, and only instance tokens, which may act anywhere anyway, are exposed to it.

OpenAPI lists each operation once, at its short path, and states the qualified form as a rule. The SDK and CLI take a `workspace` option that adds the prefix.

### Limits and quotas

A workspace has `max_databases`, `max_buckets`, `max_database_bytes` (the branch files of its live databases) and `max_object_bytes` (its objects plus multipart parts in progress). 0 means no limit of its own. The instance limits still apply on top. A new workspace takes its limits from the request or from `LUMBUNG_WORKSPACE_MAX_*` settings; the default workspace starts with none, so an upgraded instance behaves as before. `LUMBUNG_MAX_WORKSPACES` caps workspaces and `LUMBUNG_MAX_TOKENS_PER_WORKSPACE` caps live tokens, so an agent with an admin token can't fill meta.db.

Counts are checked in the transaction that inserts. Bytes are reserved before a write, the way the instance quota already works.

Considered: per-workspace rate limits and stream caps. Left out: the per-token rate limit and the token cap already bound a workspace, and a second limiter is easy to add later.

### Deleting a workspace

Deleting moves the workspace to the trash for `LUMBUNG_TRASH_RETENTION`, like a database. At once: its tokens stop authenticating, its databases and buckets stop resolving (public links and presigned URLs included), open connections and streams are closed, scheduled snapshots pause. Restoring brings all of it back, tokens too. After retention the engine purges it: databases, snapshots, objects, uploads, tokens. The name is free again as soon as it is in the trash; a restore under a taken name needs a new one.

Considered: only allowing an empty workspace to be deleted, like a bucket. Rejected: an agent's workspace may hold dozens of databases, and making an operator delete each first turns one decision into a chore and leaves half-deleted states. Considered: revoking tokens on delete. Rejected for the same reason ADR 0007 keeps a trashed database's tokens: an undo that doesn't bring the credentials back isn't an undo.

### Moving between workspaces

An instance admin can move a database or a bucket to another workspace. The live instance needs this to give an existing agent its own workspace. The move fails if the name is taken there or a limit would be passed. Every token that reached the moved thing is revoked, whole, as when something a grant names is deleted: a token bound to the old workspace can't hold a grant across the boundary.

### Audit

Every entry records the workspace it happened in. `GET /v1/audit` is the request's workspace, for a workspace admin. `GET /v1/instance/audit` is everything, including instance actions (workspaces, instance tokens, moves), for an instance admin. The row cap applies per workspace, so one busy workspace can't push another's history out.

### Dashboard

The dashboard keeps one engine credential, the service token, and uses qualified URLs.

Members get a role per workspace. An instance `owner` sees and manages every workspace, the members and the instance. Everyone else is a plain user whose access is a list of (workspace, role) with roles admin, member and read-only; a workspace they have no role in doesn't exist for them. Invites carry the workspaces and roles to grant. On upgrade, each existing member and read-only user gets that role in the default workspace.

Considered: keeping instance-wide roles and showing every workspace to everyone. Rejected: "who may see which" would then stop at the engine's door, and the dashboard would be the one place a workspace boundary didn't apply.

The engine enforces the role, not only the dashboard. With the service token, the dashboard sends `X-Lumbung-Actor-Access: read|write|admin` and the engine narrows the principal to a workspace principal at that level for the request's workspace. A server function that forgets a role check then still can't pass the member's level. Only the service token is narrowed this way, and the header can only lower what the request may do.

The workspace is part of the dashboard URL (`/app/w/{workspace}/...`), so links can be shared and two tabs can show two workspaces.

### Backups and restore

Nothing changes in the format: workspaces are rows in meta.db, which every backup holds. Restore and its reconcile work on ids as before. The restore drill walks every workspace.

### Upgrade path

Migration 6 is forward-only. It creates the default workspace, rebuilds `databases`, `buckets` and `tokens` with a `workspace_id`, carries `token_grants` across the rebuild (migration 5's comment), replaces the two name indexes with per-workspace ones, and stamps existing audit rows with the default workspace.

- Database, bucket and grants tokens land in the default workspace and work unchanged.
- Instance tokens stay instance tokens, the service token included. On short URLs they act in the default workspace, which holds everything they could reach before.
- Public links and S3 endpoints keep working: unqualified anonymous requests resolve in the default workspace.

A test starts from a version 5 `meta.db` holding every kind of token, a grants token included, and checks each one after the migration. Rolling back the binary needs the `meta.db` copy that `deploy-up.sh` takes before the swap, as for any migration.

## Consequences

- PLAN gains section 17, with its own isolation table. Each row is a named test through the real handlers for every door it applies to. Sections 6.2 and 16.5 keep their tests unchanged.
- `@lumbung/client` 0.2.0: `workspace` option, workspace calls, and token creation without a target now makes a workspace token. Documented in the changelog as breaking.
- A client that switches on a token's `scope` sees a new value, `workspace`. Token, database and bucket bodies gain `workspace_id`.
- Engine functions that took a database or bucket name now take a workspace too. Nothing resolves a name without one.
- The database list no longer shows a branch-limited token counts, sizes or the default branch for branches it can't reach. `GET /v1/databases/{db}` keeps refusing such a token: opening it up would have meant changing an existing isolation assertion, and the list was the only place that told it more than it could open.
- A later migration that rebuilds `tokens` must still carry `token_grants` across.
