---
title: Use with Drizzle
description: Define a schema in Drizzle, generate migrations with drizzle-kit, apply them with lumbung migrate, and query through @lumbung/client/drizzle.
sidebar:
  order: 4
---

`@lumbung/client/drizzle` runs Drizzle on a Lumbung branch through Drizzle's `sqlite-proxy` driver. The full example lives in `examples/drizzle` in the repository, and CI runs it against a real engine.

## 1. Install

```sh
pnpm add @lumbung/client drizzle-orm
pnpm add -D drizzle-kit
```

## 2. Define the schema

```ts
// src/schema.ts
import { integer, sqliteTable, text } from "drizzle-orm/sqlite-core"

export const users = sqliteTable("users", {
  id: integer("id").primaryKey({ autoIncrement: true }),
  email: text("email").notNull().unique(),
  name: text("name").notNull(),
})
```

## 3. Generate and apply migrations

drizzle-kit only writes the SQL files; `lumbung migrate apply` runs them against a branch, each file in its own transaction.

```ts
// drizzle.config.ts
import { defineConfig } from "drizzle-kit"

export default defineConfig({ dialect: "sqlite", schema: "./src/schema.ts", out: "./drizzle" })
```

```sh
pnpm drizzle-kit generate
lumbung migrate apply --db app --dir drizzle
```

See [Run migrations](/docs/guides/migrations/) for how applied files are tracked.

## 4. Query

```ts
import { drizzle } from "@lumbung/client/drizzle"
import { eq } from "drizzle-orm"
import * as schema from "./schema.ts"

const db = drizzle({ url: "http://127.0.0.1:4870", token: process.env.LUMBUNG_TOKEN!, database: "app" }, { schema })

await db.insert(schema.users).values({ email: "ana@example.com", name: "Ana" })
const ana = await db.select().from(schema.users).where(eq(schema.users.email, "ana@example.com")).get()
```

`db.batch([...])` runs as one Lumbung batch: one transaction, all or nothing.

## Errors

Drizzle wraps driver errors in its own `DrizzleQueryError`. The `LumbungError`, with its stable `code`, is the `cause`:

```ts
try {
  await db.insert(schema.users).values({ email: "ana@example.com", name: "Again" })
} catch (err) {
  if ((err as { cause?: { code?: string } }).cause?.code === "constraint_violation") {
    // duplicate email
  }
}
```

## Branches

Pass `branch` to work on a branch, for example to try a migration first:

```ts
const preview = drizzle({ url, token, database: "app", branch: "pr-42" }, { schema })
```
