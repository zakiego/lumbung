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

## Roll back

If a new version migrated `meta.db`, an older binary refuses to serve it: `/health` answers 503 with `unavailable`, and every request fails the same way, so a bad rollback is visible at once instead of quietly misreading newer records. To roll back:

1. Stop the engine:

   <!-- docs-test: skip needs Docker Compose -->
   ```sh
   docker compose stop engine
   ```

2. Check out the older version and build it:

   <!-- docs-test: skip builds an image -->
   ```sh
   git checkout v0.1.0
   docker compose build engine
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

Anything created between the upgrade and the rollback (databases, branches, tokens) isn't in the old `meta.db`. Their files stay on disk, and the engine removes ones it doesn't know at start. Records whose files were deleted since the backup are dropped by the restore.

Don't copy `meta.db` over by hand: a leftover `meta.db-wal` from the newer version would be replayed onto the old file.
