---
name: lumbung
description: Connect an app, script or agent to a Lumbung instance (self-hosted SQLite databases with branches and buckets). Covers the HTTP API, the libSQL URL for @libsql/client and Drizzle, the @lumbung/client SDK, the MCP endpoint, the lumbung CLI and the S3-compatible object API, plus tokens and scopes. Use when the user says "connect to lumbung", "query my lumbung database", "add lumbung to this app", "lumbung MCP", "lumbung token", "lumbung branch", or wires LUMBUNG_URL / LUMBUNG_TOKEN into code.
---

# Connect to Lumbung

Lumbung is one engine (`lumbungd`) that serves every interface on the same origin. Pick the interface, get a token, plug in the URL.

```
<engine>/v1/...                       REST API (JSON)
<engine>/libsql/<db>/<branch>/        libSQL protocol (@libsql/client, drizzle-orm/libsql)
<engine>/mcp                          MCP, streamable HTTP
<engine>/v1/s3                        S3-compatible object API (path-style)
<engine>/health                       liveness, no token
```

A local engine listens on `http://127.0.0.1:4870`. Over TLS use the instance's public origin, for example `https://db.example.com`.

## 1. Get the connection settings

Three values, always from the user or the environment:

| Env var | What |
|---|---|
| `LUMBUNG_URL` | Engine origin, no trailing path. |
| `LUMBUNG_TOKEN` | A token, `lmb_<id>_<secret>`. |
| `LUMBUNG_DATABASE` | Default database name (optional). |

Rules for tokens:

- Never invent, print, log or commit a token. Read it from env or a gitignored file. In this machine's shell, any command whose text contains `.env` may be blocked, so write env files with the editor tools.
- Ask for the narrowest token that does the job. The full table is in `/docs/concepts/tokens/`.
- Scope is the whole instance, one database (optionally one branch), or one bucket. Access is `read`, `write` or `admin`.
- Only an instance admin token can create, rename, delete or restore databases, create buckets, list the trash or read the audit log.
- A read token runs every statement on a read-only connection.

If the user has an admin token and wants a narrower one:

```sh
lumbung token create --name app --db app --access write --expires-in-days 90
lumbung token create --name agent --db app --branch agent-sandbox --access write
lumbung token create --name uploads --bucket photos --access write --access-key --json
```

The full token is shown once. The dashboard (`<origin>/app`, Tokens page) does the same. On the server host, `lumbungd service-token` prints the instance admin token: use it only to bootstrap.

Check the settings before writing code:

```sh
curl -s "$LUMBUNG_URL/health"
curl -s "$LUMBUNG_URL/v1/databases" -H "Authorization: Bearer $LUMBUNG_TOKEN"
```

## 2. Pick an interface

| Situation | Use |
|---|---|
| TypeScript app inside this repo or its workspace | `@lumbung/client` (and `@lumbung/client/drizzle`) |
| TypeScript app anywhere else, or existing libSQL / Drizzle libsql code | `@libsql/client` with the libSQL URL |
| Need interactive transactions (BEGIN ... COMMIT across round trips) | libSQL URL |
| Shell, CI, migrations, quick checks | `lumbung` CLI or curl |
| AI agent or MCP client | `/mcp` |
| Files, uploads, any S3 tool | `/v1/s3` |

`@lumbung/client` is a private workspace package (`"workspace:*"`), not on a public registry. Outside this monorepo, don't `npm install` it: use `@libsql/client`, plain `fetch` against the REST API, or vendor the SDK.

## REST API

Auth header on every call: `Authorization: Bearer <token>`. Errors are `application/problem+json` with a stable `code`. Match on `code`, never on the message.

```sh
# One statement on the default branch
curl -s "$LUMBUNG_URL/v1/databases/app/query" \
  -H "Authorization: Bearer $LUMBUNG_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"sql": "SELECT id, email FROM users WHERE email = ?", "args": ["ana@example.com"]}'

# Several statements in one transaction, on a branch
curl -s "$LUMBUNG_URL/v1/databases/app/branches/pr-42/batch" \
  -H "Authorization: Bearer $LUMBUNG_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"statements": [{"sql": "INSERT INTO users (email) VALUES (?)", "args": ["budi@example.com"]}, {"sql": "UPDATE users SET name = ? WHERE email = ?", "args": ["Budi", "budi@example.com"]}]}'
```

- Query body: `sql`, `args` (positional) or `named`, `read_only`.
- Batch body: `statements`, `read_only`, `migration` (checks foreign keys once at the end, use it for table rebuilds).
- `query` takes exactly one statement (`multiple_statements` otherwise). BEGIN/COMMIT are refused (`statement_denied`): use `batch`.
- Values come back typed: `{"type": "integer", "value": "1"}`. Integers are decimal strings so 64-bit values survive. Blobs are base64.
- Lists: `GET /v1/databases`, `GET /v1/databases/{db}/branches`, `GET /v1/buckets`, `GET /v1/tokens`.
- Send `Idempotency-Key` on creates you might retry. Quote `X-Request-Id` from the response when reporting a failure.
- To react to writes instead of polling, use `GET /v1/databases/{db}/wait` (or `.../branches/{branch}/wait`): call it without `cursor`, then pass the `cursor` from each answer with `timeout` up to 60. It answers `{"cursor", "changed"}` as soon as a write changes rows or the schema. Waits don't count as queries.

## @lumbung/client (TypeScript SDK)

No runtime dependencies, only `fetch`. Runs in Node, Bun, Deno, Workers and browsers.

```ts
import { createClient, LumbungError } from "@lumbung/client"

const db = createClient({
  url: process.env.LUMBUNG_URL!,
  token: process.env.LUMBUNG_TOKEN!,
  database: "app",
  // branch: "pr-42", intMode: "number" | "bigint" | "string", retries: 2
})

const { rows } = await db.query("SELECT id, email FROM users WHERE id = ?", [1])
await db.query("SELECT * FROM users WHERE email = :email", { email: "ana@example.com" })
await db.batch([{ sql: "INSERT INTO users (email) VALUES (?)", args: ["cici@example.com"] }, "UPDATE users SET name = 'Cici'"])

try {
  await db.query("INSERT INTO users (email) VALUES (?)", ["ana@example.com"])
} catch (err) {
  if (!(err instanceof LumbungError && err.code === "constraint_violation")) throw err
}
```

- `db.branch("pr-42")` and `db.database("other")` return bound clients.
- `await db.wait(cursor, { timeout: 55, signal })` holds until the branch changes. Start with `db.wait()` for the first cursor.
- Writes are never retried by the SDK. Reads retry only when `readOnly: true` or GET.
- Management groups: `databases`, `branches`, `snapshots`, `tokens`, `audit`, `buckets`. Objects: `client.bucket("photos").put/get/list`.
- Drizzle: `import { drizzle } from "@lumbung/client/drizzle"`, then `drizzle({ url, token, database, branch }, { schema })`. `db.transaction()` throws `unsupported`. Use `db.batch([...])`.

## libSQL URL (@libsql/client, drizzle-orm/libsql)

```ts
import { createClient } from "@libsql/client"

const client = createClient({
  url: `${process.env.LUMBUNG_URL}/libsql/app/main/`,
  authToken: process.env.LUMBUNG_TOKEN,
})
const rs = await client.execute({ sql: "SELECT id FROM users WHERE email = ?", args: ["ana@example.com"] })
```

- The URL is `<engine>/libsql/<database>/<branch>/`. The branch is required and so is the trailing slash. Without it the client drops the branch and the engine answers `not_found`.
- Over TLS use `https://` or `libsql://`, for example `libsql://db.example.com/libsql/app/main/`.
- Interactive transactions work (`client.transaction()`), and so does `drizzle-orm/libsql` with the same client.
- Served over HTTP with JSON: v2/v3 `pipeline` and v3 `cursor`. The protobuf encoding and v1 endpoints answer 404, so clients fall back to JSON.

## MCP

Endpoint `<engine>/mcp`, streamable HTTP, same bearer token and scopes as REST.

```sh
claude mcp add --transport http lumbung "$LUMBUNG_URL/mcp" --header "Authorization: Bearer $LUMBUNG_TOKEN"
```

```json
{
  "mcpServers": {
    "lumbung": {
      "type": "http",
      "url": "http://127.0.0.1:4870/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

- The server instructions say to call `get_playbook` first. It returns the operating rules (token levels, query versus execute, branches, the trash, result caps, error codes) with a version that matches the instructions. Once per conversation is enough.
- Tools: `get_playbook`, `list_databases`, `get_database`, `create_database`, `list_branches`, `create_branch`, `delete_branch`, `reset_branch`, `diff_branches`, `get_schema`, `query`, `execute`, `list_snapshots`, `create_snapshot`, and bucket tools `list_buckets`, `create_bucket`, `list_objects`, `get_object`, `put_object`, `delete_object`, `presign_object`.
- `query` is always read-only. `execute` needs a write token.
- Results are capped (100 rows by default, `max_rows` up to 1,000, about 64 KB). Narrow the query when a result says it was cut.
- Pass integers past 2^53 as strings.
- Give an agent a read token, or a write token limited to a sandbox branch. Safe schema change loop: `create_branch`, `execute` the migration there, `diff_branches` against `main`, then run the same SQL on `main`.

## lumbung CLI

Go binary. Build it from the repo with `(cd apps/engine && go install ./cmd/lumbung)`.

```sh
echo "$LUMBUNG_TOKEN" | lumbung login --url "$LUMBUNG_URL" --db app
lumbung db list
lumbung query "SELECT id, email FROM users" --db app
lumbung query "INSERT INTO users (email) VALUES (?)" --db app --arg ana@example.com
lumbung branch create pr-42 --db app --expires-in-days 7
lumbung branch diff pr-42 --db app --against main
lumbung shell --db app
```

- Settings come from flags (`--url`, `--token`, `--profile`), then `LUMBUNG_URL` / `LUMBUNG_TOKEN` / `LUMBUNG_DATABASE`, then the saved profile.
- The profile file is `lumbung/config.json` in the OS config folder (mode 0600). `LUMBUNG_CONFIG` points it elsewhere.
- A `--url` that differs from the saved profile's URL is refused unless a token is passed too, so a saved token never leaks to another host.
- Add `--json` for machine-readable output and `-y` to skip confirmations. Only use `-y` on destructive commands when the user asked for that action.

## S3-compatible objects

```ts
import { S3Client, PutObjectCommand } from "@aws-sdk/client-s3"

const s3 = new S3Client({
  endpoint: `${process.env.LUMBUNG_URL}/v1/s3`,
  forcePathStyle: true,
  region: "us-east-1",
  credentials: { accessKeyId: process.env.LUMBUNG_S3_KEY_ID!, secretAccessKey: process.env.LUMBUNG_S3_SECRET! },
})
await s3.send(new PutObjectCommand({ Bucket: "photos", Key: "2026/cat.txt", Body: "hello" }))
```

- Credentials come from `lumbung token create ... --bucket <name> --access-key --json`: `access_key_id` (starts with `LMB`) and `secret_access_key`, shown once.
- Path-style only, any region. Signature V4 only.
- A bearer token also works on `/v1/s3` for curl.
- Presigned URLs live at most 7 days and die when the token is revoked.
- Not there: versioning, ACLs, bucket policies, tagging, lifecycle.

## Errors worth knowing

| Code | Fix |
|---|---|
| `unauthorized` (401) | Token missing, malformed, revoked or expired. Ask for a new one. |
| `forbidden` (403) | Token scope doesn't cover this. A branch-limited token gets this on any other branch. |
| `database_not_found` (404) | Wrong name, or a database token pointed at another database. |
| `read_only` (403) | Read token, or `read_only: true`, on a write. |
| `statement_denied` (403) | ATTACH, VACUUM INTO, BEGIN/COMMIT over REST, temp objects, a denied pragma or function. |
| `not_found` (404) on `/libsql` | Missing branch or trailing slash in the URL. |
| `busy` / `rate_limited` | Retry with backoff. `Retry-After` says when. |

## Docs

Every docs page is also served as Markdown at `<site>/docs/<page>.md`, and `<site>/llms.txt` lists them. In this repo they live in `apps/site/src/content/docs/docs/`. The most useful ones: `reference/rest-api.mdx`, `reference/sdk.md`, `guides/libsql.md`, `guides/ai-agents.md`, `reference/cli.mdx`, `guides/s3-clients.md`, `concepts/tokens.md`, `reference/error-codes.mdx`. Working examples are in `examples/libsql`, `examples/drizzle` and `examples/s3`.
