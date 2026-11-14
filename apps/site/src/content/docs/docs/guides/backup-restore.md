---
title: Back up and restore
description: Snapshots for undo inside Lumbung, and lumbungd backup for copies outside it.
sidebar:
  order: 7
---

There are two tools, for two jobs:

- **Snapshots** live inside Lumbung, next to your data. They are for undo: take one before a risky change, restore it as a new branch if the change goes wrong.
- **Backups** with `lumbungd backup` are copies of the data folder you keep somewhere else, for when the disk or the machine fails.

## Snapshots

```sh
lumbung snapshot create --db app --label "before cleanup"
lumbung query "DELETE FROM users WHERE name IS NULL" --db app
lumbung snapshot list --db app
```

To get the old data back, restore the snapshot into a new branch, check it, then promote it:

```sh
SNAPSHOT=$(lumbung snapshot list --db app --json | jq -r '.[0].id')
lumbung snapshot restore "$SNAPSHOT" --db app --into before-cleanup
lumbung query "SELECT count(*) FROM users" --db app --branch before-cleanup
lumbung branch promote before-cleanup --db app
```

Restoring never overwrites a branch. Set a schedule to take snapshots automatically; see [Snapshots](/docs/concepts/snapshots/).

## Backups

`lumbungd backup` copies `meta.db`, each database's default branch and every object in your [buckets](/docs/concepts/buckets/) into a new timestamped folder, while the engine keeps running. Each SQLite file is copied with SQLite's online backup, so every copy holds whole transactions, and a `manifest.json` lists every file with its SHA-256.

```sh
lumbungd backup --to ./backups --keep 7
ls backups
```

Run it on the machine that holds the data folder (it reads `LUMBUNG_DATA_DIR`), from cron or a systemd timer, then copy the folder off the machine. `--all-branches` includes every branch, not only defaults. `--keep 7` deletes all but the newest seven backups of that kind.

Object files never change once written, so an object the previous backup in the same folder already holds is hard-linked from it instead of copied: a nightly backup only adds the objects that are new. `--skip-objects` leaves object files out entirely, for a quick copy of the databases. The backup also carries `secrets/access-key-encryption-key`, without which a restored instance can't check S3 access keys, so keep backups as private as the data folder. A `--meta-only` backup is named `<time>-meta` and counted on its own, so it never pushes a full backup out.

## Restore a backup

Restore into a new, empty data folder with the engine stopped, then start the engine on it:

```sh
lumbungd restore --from ./backups --to ./restored-data
```

`--from` takes one backup, or a folder of them, in which case the newest full backup is used. Every file is checked against the manifest first. Branches that weren't in the backup, snapshots, databases in the trash, objects whose file isn't in the backup (all of them, after `--skip-objects`) and multipart uploads in progress are dropped from the restored `meta.db`, so nothing points at a missing file. Bucket sizes and counts are recounted. A generated service token comes back with it; one set through `LUMBUNG_SERVICE_TOKEN` comes from the environment as before.

Test a restore before you need one: restore into a scratch folder, start a second engine on it with another `LUMBUNG_ADDR`, and query it. [Backups](/docs/self-hosting/backups/) covers running this under Docker.
