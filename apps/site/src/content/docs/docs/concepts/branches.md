---
title: Branches
description: Branches are full copies of a database you can change without touching the default branch. They never merge data back.
sidebar:
  order: 2
---

A branch is a full copy of another branch, or of a snapshot, at the moment you create it. Changes on a branch stay on that branch.

Branches **don't merge data back**. There is no merge. The flow Lumbung supports is:

1. Create a branch from the default branch.
2. Run your migration on the branch and test your app against it.
3. Run the same migration on the default branch.
4. Delete the branch.

Use the [schema diff](#schema-diff) between the two branches to confirm the migration did what you expected before step 3.

## Creating a branch

A new branch copies its source with SQLite's online backup, inside one read transaction. Writers on the source keep going while the copy runs, and the copy holds exactly the transactions committed when it started: never half of one.

A branch can start from:

- any branch of the same database (the default when you name none), or
- a snapshot, which is how [restore](/docs/concepts/snapshots/#restore) works.

## Operations

| Operation | What it does |
|---|---|
| Rename | Changes the name. The id stays. |
| Reset | Copies the branch's parent again, throwing away the branch's changes. |
| Promote | Makes the branch the database's default. The old default stays as an ordinary branch, still protected until you change that. |
| Delete | Removes the branch and its file. |
| Schema diff | Compares tables, columns, indexes, views and triggers with another branch. |

## Protected branches

A protected branch can't be deleted or reset. The default branch is always protected. Turn protection on for any branch you want to keep safe.

## Expiry

A branch can expire after a number of days, which suits a branch per pull request. An expired branch is deleted automatically unless it is protected.

## Schema diff

The diff reads each branch's schema and reports every table, index, view and trigger that was added, removed or changed. Changed tables list the columns added, removed or altered. Each change carries the DDL from both sides.
