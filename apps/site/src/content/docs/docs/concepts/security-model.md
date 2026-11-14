---
title: Security model
description: How Lumbung keeps one database from reaching another database's files, the engine's own records, or the host.
sidebar:
  order: 7
---

One database can never read or write another database's file, `meta.db`, or anything else on disk. Every statement a client sends runs under these rules, whichever way it arrives: REST, SDK, CLI or MCP.

## What a statement can't do

| Attempt | Result |
|---|---|
| `ATTACH DATABASE` or `DETACH` | `statement_denied` |
| `VACUUM` or `VACUUM INTO` | `statement_denied` |
| `load_extension()` | not available: it isn't compiled in |
| `BEGIN`, `COMMIT`, `SAVEPOINT` | `statement_denied`; each request runs in its own transaction. On the [libSQL protocol](/docs/reference/libsql-protocol/) the engine recognizes BEGIN, COMMIT and ROLLBACK and runs its own, and savepoints work inside that transaction |
| Any temp object: `CREATE TEMP ...` or `CREATE ... temp.name` | `statement_denied`; temp objects would outlive the request on a shared connection |
| A pragma not on the allowlist below | `statement_denied` |
| A write with read-only access | `read_only`, refused by SQLite itself |
| A statement that runs too long | `statement_timeout`; the connection stays usable |
| A result past the row or byte cap | `result_too_large`; nothing partial is returned |
| A string or blob past the value limit | `value_too_large` |
| Growing a branch past its size limit, or the instance past its quota | `branch_size_limit` or `instance_quota_exceeded` |

## Allowed pragmas

Pragmas work both as `PRAGMA name` statements and as `pragma_name(...)` table-valued functions.

- Schema and checks, with or without an argument: `table_info`, `table_xinfo`, `table_list`, `index_list`, `index_info`, `index_xinfo`, `foreign_key_list`, `foreign_key_check`, `integrity_check`, `quick_check`, `collation_list`, `function_list`, `module_list`, `pragma_list`, `compile_options`.
- Read only: `schema_version`, `data_version`, `page_count`, `page_size`, `freelist_count`, `max_page_count`, `journal_mode`, `encoding`, `auto_vacuum`.
- Read and set: `user_version`, `application_id`. Setting them needs write access.
- Accepted with no lasting effect: `foreign_keys`, `defer_foreign_keys`. Every request runs inside a transaction, where SQLite ignores `foreign_keys`, and the connection is reset afterwards. Migration files that toggle them still run. Foreign keys stay enforced.

## How it's enforced

The rules sit in layers, so no single check has to be perfect:

1. **An authorizer** inspects every statement when SQLite prepares it, including the statements inside views and triggers, and refuses the actions above.
2. **The attached-database limit is zero**, so even an attach that slipped past the authorizer would fail.
3. **Each connection's file layer only opens that branch's own files**: the database file and its `-wal` and `-journal` companions. Any other path, including a temp file, can't be opened.
4. **Read-only access uses a connection opened read-only.** Lumbung never decides whether SQL writes by reading it; SQLite refuses the write.
5. **Defensive mode is on and trusted schema is off.** A view or trigger can't call a function that isn't marked safe, and the schema can't be edited directly.
6. **Temp storage is in memory**, so sorting and temporary results never create files.
7. **Each connection has a memory ceiling** and limits on value size, statement length and bound parameters.
8. **Files are named by opaque ids**, never by the names you choose, so there is no path to traverse.

A fuzz test feeds random statements through the query path and fails if any file outside the attacking branch is created or changed. It runs in CI on every push, next to one named test for each row of the table above.

## What this doesn't cover

- Anyone with write access to a branch can change or delete all of its data. Scope tokens to the databases and branches they need.
- The host itself: run lumbungd as an unprivileged user (the Docker image does) and keep the data directory private. It is created with mode 700.
