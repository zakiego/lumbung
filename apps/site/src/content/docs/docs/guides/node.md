---
title: Connect from Node
description: Query a Lumbung database from a Node.js app with @lumbung/client.
sidebar:
  order: 1
---

```sh
pnpm add @lumbung/client
```

Create a token for the app, scoped to its database:

```sh
lumbung token create --name my-app --db app --access write
```

Then connect:

```ts
import { createClient } from "@lumbung/client"

const db = createClient({
  url: process.env.LUMBUNG_URL!,     // e.g. http://127.0.0.1:4870
  token: process.env.LUMBUNG_TOKEN!,
  database: "app",
})

await db.query("CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)")
await db.query("INSERT INTO notes (body) VALUES (?)", ["hello"])

const { rows } = await db.query("SELECT id, body FROM notes ORDER BY id DESC LIMIT 10")
console.log(rows)
```

The client is a thin wrapper over `fetch`: create one per process and share it. Each `query` runs in its own transaction; use `batch` for several statements that must succeed together. See the [SDK reference](/docs/reference/sdk/) for every option.
