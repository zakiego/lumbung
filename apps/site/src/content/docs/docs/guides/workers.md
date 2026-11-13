---
title: Connect from Workers
description: Query Lumbung from a Cloudflare Worker with @lumbung/client.
sidebar:
  order: 2
---

`@lumbung/client` only needs `fetch`, so it runs in a Worker unchanged. Keep the token in a secret, not in code:

<!-- docs-test: skip needs a Cloudflare account -->
```sh
pnpm add @lumbung/client
npx wrangler secret put LUMBUNG_TOKEN
```

<!-- docs-test: skip runs inside the Workers runtime; the same client calls run on the Connect from Node page -->
```ts
import { createClient } from "@lumbung/client"

interface Env {
  LUMBUNG_URL: string
  LUMBUNG_TOKEN: string
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const db = createClient({ url: env.LUMBUNG_URL, token: env.LUMBUNG_TOKEN, database: "app" })
    const email = new URL(request.url).searchParams.get("email")
    const { rows } = await db.query("SELECT id, name FROM users WHERE email = ?", [email], { readOnly: true })
    return Response.json(rows[0] ?? null)
  },
}
```

Set `LUMBUNG_URL` as a plain variable in your Wrangler config. The engine has to be reachable from Cloudflare's network, so put it behind a [reverse proxy with TLS](/docs/self-hosting/reverse-proxy/).

Create the client inside `fetch` (or once per isolate, as here): it holds no connections, so there's nothing to pool or close. `readOnly: true` uses a read-only connection and lets the client retry the request if the engine is briefly busy.
