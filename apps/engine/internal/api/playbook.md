# Lumbung

These tools reach one Lumbung instance: SQLite databases, each with branches, and buckets that
hold files. Apps read and write the same databases and buckets through the REST API and the
libSQL URL, so what you change here is what they see.

The server serves this document itself, through the `get_playbook` tool, so it always matches
the deployed tools. Reading it once per conversation is enough. If you are working from rules
you remember from an earlier conversation, compare the version in the server instructions with
the one those rules came from, and read it again if they differ.

## Hard rules

1. **Look before you name anything.** Start with `list_databases`, and `list_buckets` for
   files. Use the names they return. Never guess a database, branch, bucket or snapshot name,
   and never invent an id. A database the token can't see answers `database_not_found` exactly
   as if it didn't exist, so when a name isn't in the list, say so. Don't probe other
   spellings.
2. **Reads are free, writes are real.** `query` can't change anything. `execute`, `put_object`,
   `delete_object`, `reset_branch` and the `delete_*` tools change data at once, for every app
   that uses it, and only `delete_database` can be taken back. Before one of those on a default
   branch, or any delete, say what you are about to do and get a yes. Skip the question only
   when the user asked for that exact action, or on a branch you made yourself to try things.
3. **Branches never merge back.** A branch is a full copy of another branch at the moment it was
   made. Nothing written on it ever reaches its parent. To change a schema safely:
   `create_branch` from the default branch, `execute` the migration on the new branch, check it
   with `diff_branches` (against the default branch) and `query`, then run the same SQL on the
   default branch with `execute`, and `delete_branch` the test branch. Rows written only on the
   test branch stay there.
4. **One statement per call.** `query` and `execute` each take exactly one SQL statement
   (`multiple_statements` otherwise). `BEGIN`, `COMMIT` and `ROLLBACK` are refused with
   `statement_denied`, and `execute` runs its statement in a transaction of its own, so there
   is no way to make several calls all-or-nothing here. When a change needs several statements
   to land together (a table rebuild, a backfill and a constraint), try the whole sequence on a
   branch first, and tell the user the REST `batch` endpoint or the `lumbung` CLI can run it as
   one transaction.
5. **Values go in `args`, never in the SQL text.** Write `?` placeholders and pass the values in
   order: strings, numbers, booleans or null. Pass an integer past 2^53 as a string, like
   `"9007199254740993"`; SQLite still compares it as a number in an INTEGER column.
6. **A cut result is not the whole answer.** Results stop at `max_rows` (100 by default, at most
   1,000) and about 64 KB, and lists stop at 200 items. A cut result has `truncated: true` and a
   `note`. Narrow the question (a `WHERE`, fewer columns, `COUNT(*)`, `GROUP BY`) instead of
   reporting the first page as everything.
7. **Act on the error code.** A failed call reads `code: message (detail)`. The code is stable;
   the message is for people. The table at the end says what each one means.

## Tokens: what this one can reach

Every call runs as the bearer token the client was set up with. No tool names the token's scope,
so read it from what the lists return and from `forbidden` answers.

- A token works inside one workspace. Nothing outside it exists as far as the token can tell.
- Its scope is the whole workspace, one database (maybe one branch of it), one bucket, or a set
  of grants, each a database or a bucket with its own level.
- Its level is `read`, `write` or `admin`:

| Level | Can |
|---|---|
| `read` | `query`, `get_schema`, `diff_branches`, the list and get tools, `get_object`, a GET `presign_object`. Every statement runs on a read-only connection. |
| `write` | Also `execute`, `create_branch`, `create_snapshot`, `put_object`, `delete_object`, a PUT `presign_object`. `reset_branch` and `restore_snapshot` need write on the whole database, not one branch. |
| `admin` | Also `delete_branch` and `delete_snapshot`. |

Creating and deleting databases and buckets (`create_database`, `create_bucket`,
`delete_database`, `restore_database`, `delete_bucket`) needs an admin token for the whole
workspace. A token for one database or bucket, or a token with grants, gets `forbidden`.

A token limited to one branch sees only that branch. Every other branch answers `forbidden`,
including names that don't exist, and it can't run `list_branches` or `reset_branch`.

When a call is refused for scope, say which token level or scope it needs. Don't retry it
through another tool.

## Databases and branches

- `create_database` makes an empty database with one branch, `main`, which is its default.
  Names are lowercase letters, digits and hyphens, starting with a letter or digit.
- `get_database` shows one database's default branch, branch count and size, and
  `list_branches` marks the default and protected branches.
- Leaving out `branch` on any tool means the database's default branch. Name the branch whenever
  you mean another one.
- `create_branch` copies the default branch unless `from` names another. Give a throwaway branch
  `expires_in_days` so it removes itself.
- `reset_branch` throws a branch's changes away by copying its parent again. `delete_branch`
  removes a branch and its data for good. Neither works on the default branch or a protected one
  (`branch_protected`).
- Making a branch the default (promoting it) isn't a tool here. It is an admin action in the
  REST API, the CLI and the dashboard.
- `diff_branches` compares schema only: tables, columns, indexes, views and triggers. It never
  compares rows. Use `query` on both branches to compare data.
- `get_schema` returns the `CREATE` statements. Pass `table` for one table. A schema over about
  64 KB comes back as names only (`names_only: true`), and then `table` gets you the rest.

## Snapshots

- `list_snapshots` lists a branch's snapshots, newest first, with the ids the other snapshot
  tools take.
- `create_snapshot` saves a branch as it is now. Take one before a risky change on a default
  branch, with a `label` that says why.
- `restore_snapshot` never overwrites anything. It makes a new branch from the snapshot. Check
  that branch, then tell the user how to use it.
- `delete_snapshot` needs admin on the database and can't be undone. Branches made from the
  snapshot keep working.

## The trash

`delete_database` moves a database to the trash. Its branches stop answering at once and its
name is free again. `restore_database` brings it back, with its branches and snapshots, until the
trash retention passes (7 days unless the instance says otherwise); after that it is gone. If
another database took the name meanwhile, pass `name` to restore it under a new one. Buckets,
branches, snapshots and objects have no trash.

## Buckets and objects

- `list_objects` lists keys in order. `prefix` narrows it, `delimiter: "/"` rolls deeper keys up
  into folder-like prefixes, and `cursor` from the last call gets the next page.
- `get_object` returns text as text, up to about 64 KB from `offset`. For a longer text object,
  call it again with a larger `offset`. For binary content only the metadata comes back; make a
  GET URL with `presign_object` instead.
- `put_object` takes `text` or `content_base64`, never both, at most 1 MB. It replaces an object
  with the same key without asking. For larger files use the S3 API or a PUT URL from
  `presign_object`.
- `delete_object` can't be undone, and deleting a key that isn't there succeeds.
- `presign_object` makes a URL that anyone holding it can use until it expires (an hour by
  default, at most 7 days or less if the instance is set lower), with no token. Make one only when the user wants to share or upload
  a file, and hand it to them rather than pasting it somewhere else. It needs a token created with
  an access key (`no_access_key` otherwise).
- `create_bucket` names are 3 to 63 lowercase letters, digits and hyphens, starting and ending
  with a letter or digit. `delete_bucket` only works on an empty bucket (`bucket_not_empty`).

## Limits

`get_workspace` shows the workspace's limits (most databases, buckets and bytes; 0 means no
limit of its own) and how much is used. Check it before creating databases or buckets. For a
token that reaches only part of the workspace, the limits aren't shown.

## SQL

- This is SQLite. Look at a table with `get_schema` or `query` with `PRAGMA table_info(name)`.
- Rows come back as arrays in column order. Integers past 2^53 and blobs come back as strings.
  Long text shows its first 2,000 characters and a blob its first 48 bytes.
- `statement_denied` covers `ATTACH`, `VACUUM INTO`, transaction control, temporary tables and
  views, extension loading, and pragmas or functions off the allowlist. There is no way around
  it, so don't look for one.
- A statement that runs past the statement timeout is cancelled and keeps nothing it wrote
  (`statement_timeout`). Make it smaller, or add the index it needs on a branch first.
- `GROUP BY`, `ORDER BY` and `DISTINCT` keep every row they sort in memory, large columns
  included. Over big text or blobs that hits `memory_limit`. Shrink the values first in a
  `WITH t AS MATERIALIZED (SELECT ..., length(payload) AS n FROM ...)` and sort `t` instead.

## Error codes

| Code | What to do |
|---|---|
| `unauthorized` | The token is missing, revoked or expired. Tell the user; you can't fix it. |
| `forbidden` | The token's scope or level doesn't cover this. Say what it would need. |
| `database_not_found`, `branch_not_found`, `bucket_not_found`, `object_not_found`, `snapshot_not_found` | Wrong name, or out of this token's sight. List again and use a returned name. |
| `read_only` | A write through `query`, or with a read token. Use `execute`, or ask for a write token. |
| `statement_denied` | Not allowed in Lumbung. Rewrite without it. |
| `multiple_statements` | Split it into one call per statement. |
| `invalid_params` | The `args` don't match the `?` placeholders. Count them again. |
| `sql_error` | SQLite refused the statement; `detail` has its message. Fix the SQL. |
| `constraint_violation` | A UNIQUE, NOT NULL, CHECK or foreign key failed. Nothing was written. |
| `branch_protected` | The default or a protected branch can't be deleted or reset. |
| `parent_gone` | The branch's parent is gone, so it can't be reset. |
| `name_taken` | Pick another name, or ask the user which they meant. |
| `invalid_name` | Lowercase letters, digits and hyphens only (see the rules above). |
| `limit_reached`, `workspace_quota_exceeded`, `branch_size_limit` | Out of room. Report the limit from `get_workspace`; don't delete things to make room unless asked. |
| `value_too_large`, `object_too_large` | Too big for one call. Split the data, or use the S3 API for files. |
| `memory_limit` | The statement needed more memory than a connection may use. Sort or group small values (see SQL above), or read fewer rows at a time. |
| `busy`, `rate_limited` | Wait a moment and retry the same call once or twice. |
| `internal` | Something failed inside the engine. Report it with the request id it gives. |
