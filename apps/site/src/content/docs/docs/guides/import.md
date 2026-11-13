---
title: Import an existing SQLite file
description: Bring a SQLite database file or a SQL dump into Lumbung, and download one back out.
sidebar:
  order: 8
---

Any SQLite database file can become a Lumbung database. Create an empty database and import the file into its default branch:

```sh
lumbung export --db app -o existing.db
lumbung db create imported
lumbung import existing.db --db imported
lumbung query "SELECT count(*) FROM users" --db imported
```

(The first line just makes a sample file; use your own.)

A SQL dump works the same way. The CLI tells the two apart by the file's first bytes:

```sh
lumbung export --db app --format sql > dump.sql
lumbung db create from-dump
lumbung import dump.sql --db from-dump
```

## What's checked

Lumbung builds the import into a new file and only swaps it in when every check passes:

- the file starts with the SQLite header, and passes `PRAGMA integrity_check`;
- it fits the branch size limit (`LUMBUNG_MAX_BRANCH_BYTES`) and the import limit (`LUMBUNG_MAX_IMPORT_BYTES`, 1 GiB by default);
- it is opened under the same [security model](/docs/concepts/security-model/) as any query, so a view or trigger inside it can't call a function Lumbung blocks;
- a SQL dump runs as one transaction with foreign keys checked once at the end, so dumps that write tables in name order still load.

## Into a branch that has data

By default the target branch must have no tables, so an import never overwrites data by accident. `--mode replace` throws the branch's content away first. It isn't allowed on a protected branch, including the default branch: import into a new branch, check it, then promote it.

```sh
lumbung branch create staging --db imported
lumbung import dump.sql --db imported --branch staging --mode replace --yes
```

## Download

`lumbung export` writes a consistent copy of a branch as a SQLite file (`--format db`, the default) or a SQL dump (`--format sql`). Contentless FTS5 tables can't be rebuilt from SQL, so branches with one only download as a file.
