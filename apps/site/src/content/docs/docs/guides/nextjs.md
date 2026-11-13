---
title: Connect from Next.js
description: Query Lumbung from Next.js server code with @lumbung/client.
sidebar:
  order: 3
---

Call Lumbung only from server code: route handlers, server components and server actions. The token must never reach the browser.

<!-- docs-test: skip installs a package -->
```sh
pnpm add @lumbung/client
```

Make one client for the app:

```ts title="lib/db.ts"
import { createClient } from "@lumbung/client"

export const db = createClient({
  url: process.env.LUMBUNG_URL!,
  token: process.env.LUMBUNG_TOKEN!,
  database: "app",
})
```

Use it in a route handler:

<!-- docs-test: skip runs inside Next.js; lib/db.ts above is checked, and the same calls run on the Connect from Node page -->
```ts title="app/api/users/route.ts"
import { db } from "@/lib/db"

export async function GET() {
  const { rows } = await db.query("SELECT id, email, name FROM users ORDER BY id", [], { readOnly: true })
  return Response.json(rows)
}
```

Set `LUMBUNG_URL` and `LUMBUNG_TOKEN` in `.env.local` (and in your host's settings). Results are plain objects, so they serialize to server components' props as they are; integers are numbers unless you set `intMode` (see the [SDK reference](/docs/reference/sdk/)).

For a preview deployment per pull request, point it at a branch: set `branch` in `createClient` from an environment variable. See [A branch per pull request](/docs/guides/branch-per-pr/).
