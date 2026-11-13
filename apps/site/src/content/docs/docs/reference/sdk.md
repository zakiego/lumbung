---
title: TypeScript SDK
description: The @lumbung/client API, its options, errors and value types.
sidebar:
  order: 2
---

`@lumbung/client` has no runtime dependencies and only needs `fetch`, so it runs in Node, Bun, Deno, Workers and the browser.

```ts
import { createClient } from "@lumbung/client"

const db = createClient({
  url: "http://127.0.0.1:4870",
  token: process.env.LUMBUNG_TOKEN!,
  database: "app",
})

const { rows } = await db.query("SELECT id, email FROM users WHERE id = ?", [1])
```

## createClient(options)

| Option | Type | Default | What it does |
|---|---|---|---|
| `url` | string | required | The engine's base URL. |
| `token` | string | required | A Lumbung token. |
| `database` | string | | Database name or id for `query` and `batch`. |
| `branch` | string | the default branch | Branch name or id. |
| `intMode` | `"number"`, `"bigint"`, `"string"` | `"number"` | How integers come back. `"number"` throws a `RangeError` for an integer past 2^53 instead of rounding it. |
| `fetch` | `typeof fetch` | `globalThis.fetch` | A fetch to use, for tests or custom agents. |
| `headers` | object | | Extra headers on every request. |
| `retries` | number | `2` | Retries for safe reads only (see below). |

`client.branch(name)` and `client.database(name, branch?)` return a client bound to another branch or database, sharing the same options.

## Queries

```ts
// Positional parameters
await db.query("INSERT INTO users (email) VALUES (?)", ["ana@example.com"])

// Named parameters (:name, @name or $name)
await db.query("SELECT * FROM users WHERE email = :email", { email: "ana@example.com" })

// One transaction: all or nothing
await db.batch([
  { sql: "INSERT INTO users (email) VALUES (?)", args: ["budi@example.com"] },
  "UPDATE counters SET n = n + 1",
])

// A read-only connection, even with a write token. Also makes it safe to retry.
await db.query("SELECT count(*) AS n FROM users", [], { readOnly: true })

// A schema migration: foreign keys are checked once at the end, so a table
// rebuild doesn't fire ON DELETE CASCADE.
await db.batch(migrationStatements, { migration: true })
```

A `Result` has:

| Field | Type | |
|---|---|---|
| `columns` | `string[]` | Column names in order. Repeated names appear more than once. |
| `columnTypes` | `string[]` | Declared types, `""` for expressions. |
| `rows` | `Record<string, Value>[]` | Rows as objects. With repeated names the last column wins; use `values`. |
| `values` | `Value[][]` | Rows as arrays in column order. |
| `rowsAffected` | `number` | Rows changed by an INSERT, UPDATE or DELETE. |
| `lastInsertRowid` | `bigint` | The last inserted rowid, or `0n`. |

## Values

| SQLite | You get | You can send |
|---|---|---|
| NULL | `null` | `null`, `undefined` |
| INTEGER | `number` (or `bigint` / `string` per `intMode`) | `number` (safe integers), `bigint`, `boolean` (1/0) |
| REAL | `number` | `number` with a fraction, `-0`, `Infinity`, or whole numbers too big for 64-bit integers (`1e21`) |
| TEXT | `string` | `string`, `Date` (as an ISO string) |
| BLOB | `Uint8Array` | `Uint8Array`, `ArrayBuffer` |

Integers travel as decimal strings, so a `bigint` keeps every bit. A whole JS number between 2^53 and 2^63 is refused with a `RangeError`: as a number it may already have been rounded, so pass a `bigint` for an exact integer. See [Value encoding](/docs/reference/value-encoding/).

## Errors

Every failure is a `LumbungError` with a stable `code` (also a success response whose body isn't valid JSON, as `invalid_response`) from the [error codes](/docs/reference/error-codes/), the HTTP `status`, SQLite's message in `detail`, and the `requestId` to find it in the server log. Network failures have code `network_error` and status 0.

```ts
import { LumbungError } from "@lumbung/client"

try {
  await db.query("INSERT INTO users (email) VALUES (?)", ["ana@example.com"])
} catch (err) {
  if (err instanceof LumbungError && err.code === "constraint_violation") {
    // already there
  } else throw err
}
```

## Retries

Only requests that can't change data are retried: GET requests, and queries or batches with `readOnly: true`. They retry on `busy`, `rate_limited` and gateway errors, with a short backoff. Writes are never retried by the SDK; use an `idempotencyKey` on creates if you retry them yourself.

## Management

```ts
const admin = createClient({ url, token })

await admin.databases.create("app")
await admin.branches.create("app", "pr-42", { expiresInDays: 7 })
const diff = await admin.branches.diff("app", "pr-42", "main")
const snap = await admin.snapshots.create("app", "main", { label: "before deploy" })
await admin.snapshots.restore("app", snap.id, "restored")
const { token } = await admin.tokens.create({ name: "ci", database: "app", access: "write" })
```

| Group | Methods |
|---|---|
| `databases` | `list`, `trash`, `get`, `create`, `rename`, `delete`, `restore` |
| `branches` | `list`, `get`, `create`, `update`, `delete`, `reset`, `promote`, `diff`, `schema`, `stats` |
| `snapshots` | `list`, `create`, `restore`, `delete`, `schedule.get`, `schedule.set`, `schedule.delete` |
| `tokens` | `list`, `create`, `revoke` |
| `audit` | `list` |

Lists return `{ data, nextCursor }`; pass `{ cursor: nextCursor }` for the next page. `import(db, branch, data, { format, mode })` and `download(db, branch, format)` move whole databases. The request and response types are generated from the [REST API](/docs/reference/rest-api/)'s OpenAPI document and exported from the package.
