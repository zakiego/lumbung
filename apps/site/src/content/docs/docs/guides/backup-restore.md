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

`lumbungd backup` copies `meta.db` and each database's default branch into a new timestamped folder, while the engine keeps running. Each file is copied with SQLite's online backup, so every copy holds whole transactions, and a `manifest.json` lists every file with its SHA-256.

```sh
lumbungd backup --to ./backups --keep 7
ls backups
```

Run it on the machine that holds the data folder (it reads `LUMBUNG_DATA_DIR`), from cron or a systemd timer, then copy the folder off the machine. `--all-branches` includes every branch, not only defaults. `--keep 7` deletes all but the newest seven backups.

## Restore a backup

Restore into a new, empty data folder with the engine stopped, then start the engine on it:

```sh
lumbungd restore --from ./backups --to ./restored-data
```

`--from` takes one backup, or a folder of them, in which case the newest is used. Every file is checked against the manifest first. Branches that weren't in the backup, snapshots and databases in the trash are dropped from the restored `meta.db`, so nothing points at a missing file. The service token comes back with it.

Test a restore before you need one: restore into a scratch folder, start a second engine on it with another `LUMBUNG_ADDR`, and query it. [Backups](/docs/self-hosting/backups/) covers running this under Docker.
