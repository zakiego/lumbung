---
title: Backups
description: Nightly backups with lumbungd backup under Docker or systemd, keeping copies off the machine, and testing a restore.
sidebar:
  order: 3
---

`lumbungd backup` makes a consistent copy while the engine runs; [Back up and restore](/docs/guides/backup-restore/) explains what it copies and how restore works. This page is about running it every night.

## Under Docker

Write backups to a folder on the host, mounted into the container:

```yaml
# compose.override.yaml
services:
  engine:
    volumes:
      - ./backups:/backups
```

<!-- docs-test: skip runs inside the container -->
```sh
docker compose exec -T engine lumbungd backup --to /backups --keep 14
```

Run that from cron on the host, for example at 03:00:

```text
0 3 * * * cd /srv/lumbung && docker compose exec -T engine lumbungd backup --to /backups --keep 14 >> /var/log/lumbung-backup.log 2>&1
```

The host folder must be writable by uid 65532, the user the image runs as.

## Off the machine

A backup on the same disk doesn't survive the disk. Copy each new backup somewhere else after it's made, with whatever you already use: `rclone`, `restic`, `rsync` to another machine. Backups hold your data and the service token, so encrypt them in transit and at rest.

## Before an upgrade

```sh
lumbungd backup --to ./backups --meta-only
```

copies only `meta.db`, in a second. `meta.db` changes shape when a new version migrates it; a copy lets you roll back. See [Upgrades](/docs/self-hosting/upgrades/).

## Test a restore

A backup you haven't restored is a guess. Once after setting this up, and after big changes:

<!-- docs-test: skip restores into a second engine; the Back up and restore page runs the restore itself -->
```sh
lumbungd restore --from /backups --to /tmp/restore-check
LUMBUNG_DATA_DIR=/tmp/restore-check LUMBUNG_ADDR=127.0.0.1:4871 lumbungd &
curl -s http://127.0.0.1:4871/health
```

Then query a few tables against port 4871, stop that engine and delete the folder.
