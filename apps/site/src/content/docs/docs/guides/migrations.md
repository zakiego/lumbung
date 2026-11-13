---
title: Run migrations
description: Apply a folder of .sql migrations to a branch with lumbung migrate, and test them on a branch first.
sidebar:
  order: 5
---

`lumbung migrate apply` runs every `.sql` file in a folder, in name order, that hasn't run on the branch yet. Each file runs in its own transaction, so a failing file changes nothing and the ones before it stay applied.

```sh
lumbung migrate status --db app --dir migrations
lumbung migrate apply  --db app --dir migrations
```

## How it tracks what ran

Applied files are recorded in a `_lumbung_migrations` table on the branch: the file name, a SHA-256 checksum of its content and when it ran. If an applied file changes later, `apply` refuses to continue. Add a new migration instead of editing one that already ran.

## File format

- One or more statements per file, separated by semicolons. `CREATE TRIGGER ... BEGIN ... END;` bodies are kept whole.
- drizzle-kit's `--> statement-breakpoint` lines are ordinary comments, so its output works as is. Its `meta` folder is ignored.
- `PRAGMA foreign_keys = OFF` in a migration is accepted but has no effect: foreign keys stay enforced inside the migration's transaction.

## Test on a branch first

Branches make it safe to try a migration on real data:

```sh
lumbung branch create try-0007 --db app
lumbung migrate apply --db app --branch try-0007 --dir migrations
lumbung branch diff try-0007 --db app --against main
lumbung migrate apply --db app --dir migrations
lumbung branch delete try-0007 --db app --yes
```

Branches never merge data back, so the last `apply` runs the same files on `main`. See [Branches](/docs/concepts/branches/).
