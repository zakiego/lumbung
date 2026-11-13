---
title: Databases
description: What a database is in Lumbung, how it's named, and what happens when you delete one.
sidebar:
  order: 1
---

A database is a set of branches that share a name. Every database starts with one branch, `main`, which is its default: requests that don't name a branch go there.

Each branch is a single SQLite file on the server. Clients never see file paths. They address a database by its name or its id:

- **Name**: lowercase letters, digits and hyphens, starting with a letter or digit, up to 63 characters (`[a-z0-9][a-z0-9-]{0,62}`). Names can change.
- **Id**: assigned by Lumbung, like `db_k3x9q2m7v4p8w1z6c5n0`. Ids never change.

## Deleting and restoring

Deleting a database moves it to the trash. Its branches and snapshots stop answering at once, and its name is free to reuse. Until the trash retention passes (`LUMBUNG_TRASH_RETENTION`, 7 days by default) you can restore it, with its branches and snapshots as they were. If its old name was taken in the meantime, restore it under a new name. After the retention, the files are removed for good.

## Limits

An instance holds at most `LUMBUNG_MAX_DATABASES` databases (100 by default), counting the ones in the trash. See [Limits](/docs/concepts/limits/).
