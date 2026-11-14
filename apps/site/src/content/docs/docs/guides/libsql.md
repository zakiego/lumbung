---
title: Use the libSQL client
description: Connect @libsql/client or Drizzle's libsql driver to a Lumbung branch by URL, with interactive transactions.
sidebar:
  order: 4
---

<!-- docs-test: example libsql -->

Lumbung speaks the libSQL remote protocol over HTTP, so `@libsql/client` and Drizzle's `drizzle-orm/libsql` driver work against a branch without changes. Point the client at the branch's URL and pass a Lumbung token. Interactive transactions work too.

<!-- docs-test: skip installs packages; the docs test resolves them from examples/libsql -->
```sh
pnpm add @libsql/client
```

## Connect

```ts
import { createClient } from "@libsql/client"

const client = createClient({
  url: "http://127.0.0.1:4870/libsql/app/main/",
  authToken: process.env.LUMBUNG_TOKEN,
})

const rs = await client.execute({ sql: "SELECT id, name FROM users WHERE email = ?", args: ["ana@example.com"] })
console.log(rs.rows[0]?.name)
```

The URL is `<engine>/libsql/<database>/<branch>/`. Both names can also be ids. Keep the trailing slash: the client resolves its request paths against the URL, and without the slash it drops the branch. Lumbung answers that mistake with `not_found` instead of guessing a branch.

Over TLS, use `https://` or `libsql://` with your public hostname, for example `libsql://db.example.com/libsql/app/main/`.

## Batches

`client.batch` runs the statements in one transaction, all or nothing:

<!-- docs-test: continue -->
```ts
await client.batch(
  [
    { sql: "INSERT INTO users (email, name) VALUES (?, ?)", args: ["budi@example.com", "Budi"] },
    { sql: "UPDATE users SET name = ? WHERE email = ?", args: ["Ana S.", "ana@example.com"] },
  ],
  "write",
)
```

## Interactive transactions

`client.transaction()` keeps a transaction open across requests, so you can read, decide, then write:

<!-- docs-test: continue -->
```ts
const tx = await client.transaction("write")
try {
  const { rows } = await tx.execute("SELECT count(*) AS n FROM users")
  await tx.execute({ sql: "INSERT INTO users (email, name) VALUES (?, ?)", args: [`user${rows[0]!.n}@example.com`, "New"] })
  await tx.commit()
} finally {
  tx.close() // rolls back if commit wasn't reached
}
```

Things to know:

- A `"write"` or `"deferred"` transaction takes the branch's writer at its first statement. There's one writer per branch, so other writes (from this client, other clients, REST or MCP) wait until you commit or roll back. Reads don't wait.
- A transaction open longer than `LUMBUNG_LIBSQL_TX_TIMEOUT` (60 s), or idle longer than `LUMBUNG_LIBSQL_STREAM_IDLE_TIMEOUT` (10 s), is rolled back. The next call fails with `invalid_baton`.
- A `"read"` transaction, and any transaction from a read token, runs on a read-only connection.
- If the transaction ends without you asking (the transaction timeout, the statement timeout on a write, a full branch, or a write whose result passes the caps), later statements fail with `transaction_aborted` until you roll back, so nothing runs outside the transaction you think you have.

## Errors

Errors carry Lumbung's [error codes](/docs/reference/error-codes/) in `code`, not SQLite's names:

<!-- docs-test: continue -->
```ts
try {
  await client.execute({ sql: "INSERT INTO users (email, name) VALUES (?, ?)", args: ["ana@example.com", "Again"] })
} catch (err) {
  if ((err as { code?: string }).code !== "constraint_violation") throw err
}
```

Integers past 2^53 arrive intact. The client's default `intMode: "number"` refuses them, so pass `intMode: "bigint"` to `createClient` if your data has them.

## Drizzle

Drizzle's libsql driver takes the same client. `db.transaction()` and nested transactions (savepoints) work:

```ts
// src/schema.ts
import { integer, sqliteTable, text } from "drizzle-orm/sqlite-core"

export const users = sqliteTable("users", {
  id: integer("id").primaryKey(),
  email: text("email").notNull().unique(),
  name: text("name"),
})
```

```ts
import { createClient } from "@libsql/client"
import { eq } from "drizzle-orm"
import { drizzle } from "drizzle-orm/libsql"
import * as schema from "./schema.ts"

const client = createClient({ url: "http://127.0.0.1:4870/libsql/app/main/", authToken: process.env.LUMBUNG_TOKEN })
const db = drizzle(client, { schema })

await db.transaction(async (tx) => {
  const ana = await tx.select().from(schema.users).where(eq(schema.users.email, "ana@example.com")).get()
  if (!ana) tx.rollback()
  await tx.update(schema.users).set({ name: "Ana" }).where(eq(schema.users.id, ana!.id))
})
```

`migrate()` from `drizzle-orm/libsql/migrator` works as well. It turns foreign keys off for the migration, and Lumbung checks every foreign key once before committing, as with the REST `migration` batch option. If you run a table rebuild yourself with `executeMultiple`, put it inside `BEGIN` and `COMMIT`: after `PRAGMA foreign_keys = OFF`, Lumbung refuses writes outside a transaction rather than let a `DROP TABLE` cascade.

## Branches and tokens

Each branch has its own URL, so a preview deploy only changes the branch name:

```text
http://127.0.0.1:4870/libsql/app/main/
http://127.0.0.1:4870/libsql/app/pr-42/
```

The client sends the token as `Authorization: Bearer`, with the same scopes as the REST API. A branch-limited token gets `forbidden` on any other branch's URL. See [the protocol reference](/docs/reference/libsql-protocol/) for what the endpoint supports.
