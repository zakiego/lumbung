---
title: Upgrades
description: How lumbungd migrates meta.db, how to upgrade safely, and what a rollback looks like.
sidebar:
  order: 4
---

`meta.db` holds Lumbung's own records: databases, branches, tokens, the audit log. Each version embeds the migrations it needs and applies them when it starts. Migrations only go forward.

## Upgrade

1. Back up `meta.db`:

   <!-- docs-test: skip runs inside the container -->
   ```sh
   docker compose exec -T engine lumbungd backup --to /data/backups --meta-only
   ```

2. Pull the new version and rebuild:

   <!-- docs-test: skip builds an image -->
   ```sh
   git pull
   docker compose up -d --build
   ```

3. Check that it serves:

   ```sh
   curl -s http://127.0.0.1:4870/health
   ```

   `/health` reports the version and how many `meta.db` migrations are applied.

Branch files are plain SQLite and don't change between versions.

## Upgrading to workspaces

The version that brings [workspaces](/docs/concepts/workspaces/) migrates `meta.db` like any other upgrade, and an instance keeps working without a further step:

- Every database, bucket and token lands in a workspace called `default`. Database, bucket and grants tokens keep reaching what they reached. Instance tokens stay instance tokens.
- URLs don't change. A request with no workspace in its URL acts in the token's workspace, or in `default` for an instance token and for an anonymous request, so public links keep working.
- In the dashboard, members and read-only people keep that role, in `default`. Its own database gets three more tables and one more column, and nothing the older dashboard reads is changed.
- Tokens made without a database or a bucket before the upgrade are instance tokens. They reach every workspace, the ones you make later included. Revoke the ones that shouldn't.

The [changelog](/docs/changelog/) lists what behaves differently afterwards. Two things are worth checking in your own scripts: `POST /v1/tokens` without a target now makes a workspace token (instance tokens are made at `POST /v1/instance/tokens`), and `GET /v1/audit` is one workspace's log.

To give an app or an agent a workspace of its own and move a database into it (here one called `notes`, made first so the sample runs):

```sh
lumbung db create notes
lumbung workspace create agent
(umask 077; lumbung token create --name agent --access admin --workspace agent > agent.token)
lumbung db move notes --to agent --yes
```

- A move revokes every token that reached what moved, a grants token included, and moving it back doesn't bring them back. So make the workspace's token first, check it works, and switch the app to it right after the move. A workspace token made before the move reaches whatever is in the workspace later.
- A new workspace has [limits](/docs/concepts/workspaces/#limits) where `default` has none. A move that doesn't fit is refused, and a database that fits today stops taking writes at the limit. Raise it first with `lumbung workspace update`.
- Leave a bucket with public links in `default`: outside it, an anonymous link needs the workspace in its URL.
- Remove the token file once the app has it.

Rolling back past this version follows the steps below, and costs more than usual once workspaces are in use:

- Take a full backup with the newer version first (`lumbungd backup --to <folder> --all-branches`). The older `meta.db` forgets every database, branch, snapshot and bucket made since the upgrade, and the engine deletes their files when it starts. Objects uploaded since are gone too.
- Tokens made in a workspace are gone with it, and tokens a move or a person revoked since work again.
- Roll the dashboard back with the engine: a newer dashboard calls URLs the older engine doesn't have. The older dashboard knows one role for the whole instance, and to it everyone invited or changed since the upgrade is read-only on everything, whichever workspace they were given. Remove the people who shouldn't see everything.

## Roll back

If a new version migrated `meta.db`, an older binary refuses to serve it: `/health` answers 503 with `unavailable`, and every request fails the same way, so a bad rollback is visible at once instead of quietly misreading newer records. To roll back:

1. Stop the engine:

   <!-- docs-test: skip needs Docker Compose -->
   ```sh
   docker compose stop engine
   ```

2. Check out the older version and build it, the dashboard with it:

   <!-- docs-test: skip builds an image -->
   ```sh
   git checkout v0.1.0
   docker compose build engine dashboard
   ```

3. Put back `meta.db` from the backup you took before upgrading. `restore --meta-only` takes the newest meta-only backup in the folder, refuses to run while lumbungd is running, and keeps the current `meta.db` next to it as `meta.db.before-restore-<time>`:

   <!-- docs-test: skip needs Docker Compose -->
   ```sh
   docker compose run --rm engine restore --meta-only --from /data/backups
   ```

4. Start it:

   <!-- docs-test: skip needs Docker Compose -->
   ```sh
   docker compose up -d
   ```

Anything created between the upgrade and the rollback (databases, branches, buckets, objects, tokens) isn't in the old `meta.db`. The engine removes the files it doesn't know when it starts, so take a full backup first if any of it matters. Records whose files were deleted since the backup are dropped by the restore.

Don't copy `meta.db` over by hand: a leftover `meta.db-wal` from the newer version would be replayed onto the old file.
