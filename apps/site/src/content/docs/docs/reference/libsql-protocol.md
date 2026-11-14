---
title: libSQL protocol
description: The engine's libSQL remote protocol endpoint, what it supports, and how streams, transactions and limits work.
sidebar:
  order: 8
---

The engine serves the libSQL remote protocol over HTTP ("Hrana", versions 2 and 3, JSON) next to the REST API. It's what `@libsql/client` and `drizzle-orm/libsql` speak for `http:`, `https:` and `libsql:` URLs. For using it from code, see [Use the libSQL client](/docs/guides/libsql/).

```sh
curl -s http://127.0.0.1:4870/libsql/app/main/v2/pipeline \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"baton": null, "requests": [{"type": "execute", "stmt": {"sql": "SELECT count(*) FROM users"}}, {"type": "close"}]}'
```

## Endpoints

| Method and path | What it does |
|---|---|
| `GET /libsql/{db}/{branch}/v2` | Version check. 200, no token needed. |
| `GET /libsql/{db}/{branch}/v3` | Version check. 200, no token needed. |
| `POST /libsql/{db}/{branch}/v2/pipeline` | Runs a pipeline of requests on a stream. |
| `POST /libsql/{db}/{branch}/v3/pipeline` | The same, version 3. |
| `POST /libsql/{db}/{branch}/v3/cursor` | Runs a batch and streams its results as JSON lines. |

`{db}` and `{branch}` take a name or an id. The branch is always in the path; there's no default-branch form. The client URL is the path up to the branch, with a trailing slash: `http://127.0.0.1:4870/libsql/app/main/`. A path missing the branch answers 404 `not_found`.

The Protobuf encoding (`v3-protobuf`) and the version 1 endpoints aren't served. They answer 404, which makes a client that probes for them fall back to JSON.

## Requests

| Request | Notes |
|---|---|
| `execute` | One statement, positional `args` or `named_args` (not both), `want_rows`. |
| `batch` | Steps run in order. Conditions: `ok`, `error`, `not`, `and`, `or`, `is_autocommit`. A condition may only name an earlier step. At most 1000 steps. |
| `sequence` | Statements separated by semicolons, rows ignored, stops at the first error. |
| `describe` | Parameters, columns, `is_explain` and `is_readonly`, without running the statement. |
| `store_sql`, `close_sql` | At most 128 stored texts per stream, each within `LUMBUNG_MAX_SQL_BYTES`. |
| `get_autocommit` | Whether a transaction is open on the stream. |
| `close` | Closes the stream and rolls back its open transaction. |

A pipeline holds at most 1000 requests, and its body is capped by `LUMBUNG_MAX_REQUEST_BYTES`. Every request runs, even after an earlier one failed, as the protocol asks.

Values use the same typed objects as the REST API ([Value encoding](/docs/reference/value-encoding/)), which are the protocol's own. Statement results carry `cols`, `rows`, `affected_row_count` and `last_insert_rowid`; `rows_read` and `rows_written` are the rows returned and the rows changed.

## Streams and batons

A pipeline with `"baton": null` opens a stream. Each response carries a new baton for the next request on that stream, and the old one stops working, so a stream's requests are serial. A baton that's stale, made up, or used with another token or on another branch's URL gets `invalid_baton` (400). A response's baton is `null` once the stream is closed. `base_url` is always `null`.

A stream with no request for `LUMBUNG_LIBSQL_STREAM_IDLE_TIMEOUT` (10 s) is closed. At most `LUMBUNG_LIBSQL_MAX_STREAMS` (128) streams are open on the instance and `LUMBUNG_LIBSQL_MAX_STREAMS_PER_TOKEN` (16) per token; past either, `too_many_streams` (429). A stream with no open transaction holds no connection.

## Transactions

The engine owns transaction control. It recognizes these statements itself and never passes them to SQLite:

| Statement | Effect |
|---|---|
| `BEGIN [DEFERRED \| IMMEDIATE \| EXCLUSIVE] [TRANSACTION]` | Opens a transaction that stays open across pipeline requests. A token that can write gets the branch's writer, whatever the mode. |
| `BEGIN TRANSACTION READONLY` | Opens it on a read-only connection. |
| `COMMIT`, `END` | Commits. |
| `ROLLBACK` | Rolls back. |

Any other form of transaction control (a named transaction, `BEGIN` inside a longer statement) is denied with `statement_denied`, as on REST. `SAVEPOINT`, `RELEASE` and `ROLLBACK TO` work inside an open transaction and are denied outside one. A statement outside a transaction runs in its own; one that only reads runs on a read-only connection even for a write token, so it doesn't wait for an open transaction.

An open transaction ends:

- at `COMMIT`, `ROLLBACK` or `close`;
- when the stream goes idle, or the transaction passes `LUMBUNG_LIBSQL_TX_TIMEOUT` (60 s): it's rolled back and the next request gets `invalid_baton`;
- when SQLite rolls it back after an error (the statement timeout, a full branch), or a statement fails with `result_too_large`: later statements get `transaction_aborted` until `ROLLBACK`;
- when the branch is reset, deleted or imported into, or the engine shuts down: it's rolled back first.

`PRAGMA foreign_keys = OFF` outside a transaction applies to the next one, as the client's `migrate()` expects: foreign keys are off for it, and every foreign key is checked once before COMMIT. `PRAGMA foreign_keys = ON` clears it.

## Auth and limits

Requests need `Authorization: Bearer <token>` with the same [scopes](/docs/concepts/tokens/) as the REST API. A read token gets read-only connections. A branch-limited token gets `forbidden` on another branch. Failed authentication and each token's requests are rate limited as on REST.

Every statement runs through the same engine path as REST, so the [limits](/docs/concepts/limits/) and the [security model](/docs/concepts/security-model/) apply unchanged: statement timeout, row and byte caps (per pipeline request), value, statement and parameter limits, branch and instance size limits, and the authorizer.

## Errors

Errors for one request in a pipeline are `{"message", "code"}` in its result. Errors for the whole HTTP request (authentication, an unknown database or branch, a bad baton, a stream cap, a malformed body) use the same shape with the code's HTTP status and `Content-Type: application/json`. Codes are Lumbung's [error codes](/docs/reference/error-codes/), not SQLite's names.
