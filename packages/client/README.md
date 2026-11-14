# @lumbung/client

TypeScript client for [Lumbung](https://lumbung.zakiego.com/docs/), a self-hosted SQLite database platform with branches. Query, batch and manage databases, branches and buckets over the Lumbung HTTP API.

It has no runtime dependencies and only needs `fetch`, so it runs in Node, Bun, Workers and the browser.

```sh
npm install @lumbung/client
```

## Query

```ts
import { createClient } from "@lumbung/client"

const db = createClient({
  url: process.env.LUMBUNG_URL!, // e.g. http://127.0.0.1:4870
  token: process.env.LUMBUNG_TOKEN!,
  database: "app",
})

await db.query("CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)")
await db.query("INSERT INTO notes (body) VALUES (?)", ["hello"])

const { rows } = await db.query("SELECT id, body FROM notes ORDER BY id DESC LIMIT 10")
```

Each `query` runs in its own transaction. Use `batch` for statements that must succeed together. Pass `branch` to `createClient` to work on a branch instead of `main`.

Errors are thrown as `LumbungError`, which carries the API's error `code`.

## Drizzle

`@lumbung/client/drizzle` runs Drizzle on a Lumbung branch. Install `drizzle-orm` next to it.

```ts
import { drizzle } from "@lumbung/client/drizzle"
import * as schema from "./schema.ts"

const db = drizzle({ url: "http://127.0.0.1:4870", token: process.env.LUMBUNG_TOKEN!, database: "app" }, { schema })
```

## License

MIT
