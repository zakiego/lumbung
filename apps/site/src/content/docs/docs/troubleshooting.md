---
title: Troubleshooting
description: What common errors mean and what to do about them.
---

Every error has a stable `code` (see [Error codes](/docs/reference/error-codes/)) and a `request_id` that matches a line in the engine's log.

| You see | It means | Do this |
|---|---|---|
| `unauthorized` | The token is missing, mistyped, revoked or expired. | Check `Authorization: Bearer <token>`. `lumbung token list` shows expiry and last use. |
| `rate_limited` | Too many requests for this token, or too many failed logins from this address. | Wait for `Retry-After`. Behind a proxy, set `LUMBUNG_CLIENT_IP_HEADER` so clients don't share one limit. |
| `forbidden` | The token is valid but its scope doesn't cover this. | Use a token with more access, or a database token instead of a branch token. See [Tokens and scopes](/docs/concepts/tokens/). |
| `database_not_found` for a database that exists | The token is scoped to another database; other databases look like they don't exist. | Use a token for this database. |
| `read_only` | A read token, or `read_only: true`, tried to write. | Use a write token. |
| `statement_denied` | The statement uses something Lumbung blocks: ATTACH, VACUUM, BEGIN/COMMIT, temp tables, a pragma off the allowlist. | Drop it. Each request is already a transaction; use a batch for several statements. See [Security model](/docs/concepts/security-model/). |
| `multiple_statements` | `query` got more than one statement. | Use `batch`, or `lumbung migrate` for a file. |
| `statement_timeout` | The statement ran past `LUMBUNG_STATEMENT_TIMEOUT`. | Add an index or a `LIMIT`; check the branch's slow-query log. |
| `result_too_large` | The result is past the row or byte cap. | Page with `LIMIT` and `OFFSET` or a keyset, or select fewer columns. |
| `branch_size_limit`, `instance_quota_exceeded` | The write would grow past a size limit. | Delete data (freed pages are reused), or raise the limit. |
| `insufficient_disk` | The disk has less free space than `LUMBUNG_MIN_FREE_DISK`. | Free disk space; delete old snapshots or empty the trash. |
| `busy` | The branch stayed locked past the busy timeout, or a reset or import is waiting on it. | Retry. Long-running writes on the same branch queue behind each other. |
| `unavailable` from `/health` | The engine can't serve, usually because `meta.db` is newer than the binary after a rollback. | Run the newer version, or restore the `meta.db` backup. See [Upgrades](/docs/self-hosting/upgrades/). |
| `branch_protected` | Deleting, resetting or replacing a protected branch. | Unprotect it first, or import into a new branch and promote it. |

## The CLI says "not logged in"

Run `lumbung login --url <engine URL>`, or set `LUMBUNG_URL` and `LUMBUNG_TOKEN`. If you pass `--url` for a different instance, pass its token too: the saved token only goes to the saved URL.

## A migration failed halfway

It didn't: each migration file runs in one transaction, so a failing file changes nothing. Fix the file and run `lumbung migrate apply` again. Files that ran before it stay applied.

## Deleted a database by mistake

It's in the trash until `LUMBUNG_TRASH_RETENTION` (7 days) passes:

```sh
lumbung db trash
```

Then `lumbung db restore <name or id>`.
