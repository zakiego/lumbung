---
title: Run migrations
description: Apply a folder of .sql migrations to a branch with lumbung migrate, and test them on a branch first.
sidebar:
  order: 5
---

`lumbung migrate apply` runs every `.sql` file in a folder, in name order, that hasn't run on the branch yet. Each file runs in its own transaction, so a failing file changes nothing and the ones before it stay applied. `migrate status` only reads, so a read-only token can check what's pending.

```sh
lumbung migrate status --db app --dir migrations
lumbung migrate apply  --db app --dir migrations
```

## How it tracks what ran

Applied files are recorded in a `_lumbung_migrations` table on the branch: the file name, a SHA-256 checksum of its content and when it ran. If an applied file changes later, `apply` refuses to continue. Add a new migration instead of editing one that already ran.

## File format

- One or more statements per file, separated by semicolons. `CREATE TRIGGER ... BEGIN ... END;` bodies are kept whole.
- drizzle-kit's `--> statement-breakpoint` lines are ordinary comments, so its output works as is. Its `meta` folder is ignored.
- Each file runs in migration mode: foreign key enforcement is off while the file runs, and every foreign key is checked once before it commits. Schema tools rebuild a table to change a column (create a new table, copy the rows, drop the old one, rename), and with enforcement on, the drop would fire `ON DELETE CASCADE` and delete child rows. If the file leaves a row pointing at nothing, the whole file is rolled back. `PRAGMA foreign_keys` lines in the file change nothing either way.
- A file can hold up to 998 statements (one batch's limit, minus the two bookkeeping statements). Split larger ones.

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
