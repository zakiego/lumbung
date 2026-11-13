---
title: Snapshots
description: Point-in-time copies of a branch, taken by hand or on a schedule, and restored as a new branch.
sidebar:
  order: 3
---

A snapshot is a copy of one branch at one moment, kept as its own file. Taking one doesn't block writers.

## Manual and scheduled

Take a snapshot by hand before anything risky, or set a schedule on a branch (for example every 6 hours). A schedule has retention: keep the newest N scheduled snapshots, drop the ones older than a given age, or both. Manual snapshots are kept until you delete them.

## Restore

Restoring never overwrites a branch. It creates a **new branch** from the snapshot. Check the new branch, then [promote](/docs/concepts/branches/#operations) it if it should become the default. That second step is always explicit.

## Snapshots and deleted branches

A snapshot outlives the branch it came from. You can still restore it after deleting that branch.
