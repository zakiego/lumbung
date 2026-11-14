# 0005: The libSQL remote protocol

Date: 2026-11-14. Status: accepted.

## Context

PLAN 14, milestone 11: `@libsql/client` and Drizzle's libsql driver must work unchanged against a Lumbung branch, including interactive transactions. Those clients speak the libSQL remote protocol ("Hrana") over HTTP for `http:`, `https:` and `libsql:` URLs.

What the client does, read from the `@libsql/client` 0.18.0 and `@libsql/hrana-client` 0.10.0 sources and `docs/HRANA_3_SPEC.md` in the libsql repository:

- It opens the HTTP client with protocol version 2. It never sends a version check, and every request goes to `v2/pipeline` with a JSON body. The v3 endpoints exist in the spec and in the hrana client, but `@libsql/client` doesn't use them.
- Request URLs are resolved relative to the base URL with `new URL("v2/pipeline", base)`. A base URL without a trailing slash loses its last path segment.
- The auth token goes in `Authorization: Bearer <token>`.
- `execute` pipelines `[execute, close]` on a new stream. `batch` pipelines `store_sql` requests, one `batch` whose steps are `BEGIN DEFERRED`, the statements, `COMMIT` and a conditional `ROLLBACK`, then `close`. `transaction()` keeps a stream open across several pipeline requests, tied together by a baton, and sends `BEGIN IMMEDIATE`, `BEGIN DEFERRED` or `BEGIN TRANSACTION READONLY`, then `COMMIT` or `ROLLBACK`, as SQL. `migrate()` sends `PRAGMA foreign_keys=off` before its batch and `PRAGMA foreign_keys=on` after.
- Errors in an HTTP error response are read as `{"message", "code"}` only when the content type is exactly `application/json`.

## Decision

### Endpoint and branch addressing

The endpoint lives on the engine next to `/v1` and `/mcp`:

```
GET  /libsql/{db}/{branch}/v2              version check (200)
GET  /libsql/{db}/{branch}/v3              version check (200)
POST /libsql/{db}/{branch}/v2/pipeline
POST /libsql/{db}/{branch}/v3/pipeline
POST /libsql/{db}/{branch}/v3/cursor
```

The client URL is `http://host:4870/libsql/<db>/<branch>/`, with the trailing slash. `{db}` and `{branch}` take a name or an id, as on REST. The branch is always explicit: there's no default-branch form, because a URL missing its trailing slash (`/libsql/app/preview`) would then resolve to `/libsql/app/v2/pipeline` and quietly reach the default branch instead of `preview`. That shorter path answers 404 with a message that says to add the branch and the slash.

The version checks need no token and look nothing up, so they reveal nothing. `v3-protobuf` and the v1 endpoints aren't served (404), which makes a client that probes for them fall back to JSON v2.

### Supported requests

`execute`, `batch` (with `ok`, `error`, `not`, `and`, `or` and `is_autocommit` conditions), `sequence`, `describe`, `store_sql`, `close_sql`, `get_autocommit` and `close`, on v2 and v3 alike. The v3 cursor endpoint runs the batch and streams its entries as JSON lines. An unknown request type, a missing or unknown `sql_id`, or both `sql` and `sql_id` is an error result for that request (`bad_request`); the other requests in the pipeline still run, as the spec asks. Every condition in a batch is checked (known type, fields present, step references pointing at earlier steps, nesting at most 64 deep) before any step runs, so a bad one fails the batch with nothing run. When a request fails at the HTTP level after its stream was found (the cursor endpoint), the stream is closed and its transaction rolled back, since the client gets no baton to continue it.

Values use ADR 0003's typed objects, which are the protocol's value objects, through the same `internal/value` code. Results carry `cols`, `rows`, `affected_row_count` and `last_insert_rowid` as on REST; `rows_read` and `rows_written` are the rows returned and the rows changed.

Errors inside a pipeline are `{"message", "code"}` with Lumbung's documented error codes (`constraint_violation`, `statement_denied`, ...), not SQLite's names. Errors for the whole request (auth, an unknown branch, a bad baton, a stream cap) use the same body with `Content-Type: application/json` and the code's HTTP status, so the client surfaces the code.

### Streams and batons

A stream is server-side state: the token, database and branch it belongs to, its stored SQL texts, and its open transaction if any. A stream with no open transaction holds no connection.

- A baton is `<stream id>.<secret>`, both random. Every response carries a fresh secret and the old one stops working, so requests on a stream are serial and a stale or replayed baton is refused with `invalid_baton` (HTTP 400). A baton presented with a different token or on a different branch's URL is refused the same way, so it can't be used to learn anything.
- A stream with no request for `LUMBUNG_LIBSQL_STREAM_IDLE_TIMEOUT` (10 s) is closed. An open transaction may live at most `LUMBUNG_LIBSQL_TX_TIMEOUT` (60 s) from its BEGIN. Either way the transaction is rolled back and its connection returned, and the next request with the old baton gets `invalid_baton`.
- At most `LUMBUNG_LIBSQL_MAX_STREAMS` (128) streams are open on the instance and `LUMBUNG_LIBSQL_MAX_STREAMS_PER_TOKEN` (16) per token. Past either, opening a stream fails with `too_many_streams` (429).
- A stream stores at most 128 SQL texts, each within the statement length limit, and together within the request body limit.
- Closing the engine, and a reset, delete or import of the branch, roll back open transactions on it, before every attempt at the branch's gate, so one that began during a priority break doesn't hold the maintenance up.
- Statements that reach the engine count in the branch's stats (BEGIN, COMMIT, ROLLBACK and the foreign keys pragma don't), `sequence` statements included. Slow-query rows are written by a background worker, since an open transaction may hold the writer meanwhile.

### Transactions

The engine owns transaction control, as on REST: the authorizer still denies `BEGIN`, `COMMIT` and `ROLLBACK` in user SQL. On this endpoint the engine recognizes those statements itself, by their keywords, and runs its own trusted `BEGIN`, `COMMIT` and `ROLLBACK`:

- `BEGIN [DEFERRED | IMMEDIATE | EXCLUSIVE] [TRANSACTION]` starts a transaction that stays open across pipeline requests. A token that can write gets the branch's writer, with `BEGIN IMMEDIATE`, whatever mode was asked for: writes need the writer, and there's one per branch. `BEGIN TRANSACTION READONLY`, and any BEGIN from a read token, gets a read-only connection.
- `COMMIT` and `END` commit, `ROLLBACK` rolls back, and both return the connection.
- `SAVEPOINT`, `RELEASE` and `ROLLBACK TO` are allowed inside an open transaction, so Drizzle's nested transactions work. Outside one they stay denied.
- A statement outside a transaction runs in its own, exactly like a REST query.
- When a transaction ends without the client asking (SQLite rolls it back after a timeout or a full branch, it passes the transaction timeout, or maintenance aborts it), the stream remembers it: later statements fail with `transaction_aborted` until `ROLLBACK`, so they can't run outside the transaction the client thinks it has. `get_autocommit` and the `is_autocommit` condition report false meanwhile, matching what the stream accepts. A statement that changed rows and then fails with `result_too_large` rolls the transaction back too, since part of it ran; a read past the caps fails alone.
- The transaction timeout is a deadline on the transaction itself: a statement running when it passes is interrupted, and the engine rolls back at the deadline even with no request in flight.
- `last_insert_rowid()` sees the last insert of the session or transaction, as in SQLite; the engine resets it only when a pooled connection starts a new session. Results report a rowid only for a statement that set one.
- `PRAGMA foreign_keys = OFF` outside a transaction, as `migrate()` sends, applies to the next transaction the way REST's `migration` batch option does: foreign keys are off for it and every foreign key is checked once before COMMIT. `PRAGMA foreign_keys = ON` clears it. While it's set, a write outside a transaction is refused with `bad_request` saying to send BEGIN first; reads still run. Honoring it for single statements was considered and rejected: without a check, a table rebuild's DROP leaves dangling rows behind; with the check before each implicit commit, that same DROP fails, because the parent is gone until the RENAME two statements later. A rebuild only works as one transaction, which is what `migrate()` sends, and an `executeMultiple` rebuild works once it's wrapped in BEGIN and COMMIT.

An open write transaction holds the branch's only writer, so other writes on the branch (REST, MCP, other streams) wait for it, up to the statement timeout. The transaction timeout bounds that wait.

### Auth and limits

The same bearer tokens and scopes as REST, through the same checks: failed authentication is rate limited per address, each request counts against the token's rate limit, a read token gets read-only connections, and a branch-limited token reaches only its branch (another branch is `forbidden`). The token is checked on every request, so revoking it ends its streams' access at once.

Every statement runs through the same engine path as REST, so the statement timeout, row and byte caps (per pipeline request), parameter, value and SQL length limits, and the branch and instance size limits all apply unchanged. A pipeline holds at most 1000 requests and a batch at most 1000 steps; the body is capped by `LUMBUNG_MAX_REQUEST_BYTES`.

### Isolation

PLAN 6.2 applies unchanged: every user statement is prepared on the same hardened connections, under the same authorizer, with ATTACH, VACUUM INTO, extensions, temp objects and off-list pragmas denied, the attached-database limit at zero, and the confined VFS. The only authorizer change is that savepoints are allowed while an interactive transaction is open. The engine's own BEGIN, COMMIT and ROLLBACK never include user text. The isolation table is tested again through this endpoint.

## Consequences

- `@libsql/client` and `drizzle-orm/libsql` work by URL alone, including `transaction()` and `db.transaction()`. `examples/libsql` tests that against a real engine.
- Code that compares `err.code` with SQLite names (`SQLITE_CONSTRAINT`) needs Lumbung's codes instead.
- A forgotten transaction blocks the branch's writers for up to the transaction timeout. That's the price of interactive transactions, and the setting is documented with it.
- `/libsql` is a new public path prefix. Our edge Worker sends it to the engine.
