# Lumbung plan

Lumbung is a self-hosted SQLite database platform. One server holds many SQLite databases. Each database has branches. You reach them through an HTTP API, a TypeScript SDK, a CLI, an MCP endpoint and a dashboard.

This file is the source of truth for scope and behavior. Read the section you're working on before writing code. When a decision here turns out wrong, change this file in the same commit as the code and say why.

## 1. Who it's for

- Us first: one instance on the Mac mini, used by our own apps and agents.
- Later, anyone who clones the repo and runs it on their own machine. So nothing in the product may depend on our hostnames, our tunnel or our accounts. Our own deployment notes live in `ops/`, everything else is generic.

No public sign-up, no billing, no multi-org tenancy. One instance is one team.

## 2. Wording rule

Lumbung is described on its own terms. No "clone of", no "alternative to", no comparison to a named hosted database product, anywhere: code, comments, docs, landing page, commit messages, PR text, package descriptions. `scripts/check-wording.sh` enforces this in CI.

Stating a compatibility fact is fine ("works with the libSQL client").

## 3. Decisions already made

| Topic | Decision |
|---|---|
| Name | Lumbung. Repo `zakiego/lumbung`, private for now |
| Engine | Go server `lumbungd`. Owns databases, branches, tokens, REST API, MCP |
| Dashboard | TanStack Start (Nitro) + ui-sdk (ADR 0004), a client of the engine's API |
| Dashboard login | Better Auth, invite-only: email and password plus passkeys, roles owner, member, read-only |
| Site | Astro + Starlight: landing at `/`, docs at `/docs`, static on Cloudflare Pages |
| Client API | Own REST API and own SDK first. libSQL wire protocol is a later milestone |
| CLI | Go binary `lumbung`, same module as the engine |
| Hosting | Engine and dashboard as Docker containers on the Mac mini. Site on Cloudflare Pages |
| Package manager | pnpm workspace for the TS apps, Go module for the engine |

Use palui (`~/Code/JS/Node/2026/palui`) as the reference for repo conventions: pnpm workspace file, TanStack Start app layout, Better Auth setup, Dockerfile, compose, the edge Worker, `ops/`. Copy patterns, not product code.

## 4. Layout

```
apps/engine      Go module. cmd/lumbungd (server), cmd/lumbung (CLI), internal/...
apps/dashboard   TanStack Start: UI under /app, Better Auth, server functions that call the engine
apps/site        Astro + Starlight: landing and docs
apps/edge        Cloudflare Worker for our deployment: one hostname, routes to site, dashboard, engine
packages/client  @lumbung/client: TypeScript SDK and the Drizzle adapter
docs/adr         Short decision records (one file per decision)
ops/             Our own deployment: compose overrides, backup script, notes for the mini
scripts/         CI helpers, worktree setup, wording check, docs tests
```

## 5. Architecture

```
SDK / CLI / MCP client / any HTTP client ──────────────┐
                                                        ▼
browser ─► dashboard (TanStack Start) ──service token─► lumbungd (Go)
           Better Auth, invites, roles                  REST /v1, /mcp, /libsql, /health
           own small SQLite (users, sessions)           meta.db (databases, branches, tokens, audit, stats)
                                                        one SQLite file per branch
```

- The engine works without the dashboard. Everything the dashboard does goes through the public REST API, so the dashboard is also the API's first real user.
- The browser never talks to the engine. Dashboard server functions call it with a service token and pass the acting user for the audit log.
- A read-only member is enforced by the engine opening a read-only connection, never by the dashboard inspecting SQL.
- Zero-config boot: with no settings at all, both containers start. Secrets (service token, auth secret) are generated into the data volume on first start with mode 600. Environment variables only override.

### Storage

```
$DATA_DIR/meta.db
$DATA_DIR/dbs/<database_id>/<branch_id>.db        (+ -wal, -shm)
$DATA_DIR/snapshots/<database_id>/<snapshot_id>.db
$DATA_DIR/trash/<database_id>/...                 (soft-deleted, purged after retention)
$DATA_DIR/secrets/
$DATA_DIR/objects/, uploads/                      (buckets, section 16.6)
```

File names are opaque ids, never user-supplied names. That makes renames free and path traversal impossible.

## 6. Engine requirements

### 6.1 SQLite driver

Pick the Go SQLite driver with a spike, not from memory. Candidates: `mattn/go-sqlite3` (cgo) and `ncruces/go-sqlite3` (no cgo). The driver must provide all of these, each proven by a test in the spike:

1. A per-connection authorizer (or equivalent) that can deny statements.
2. Runtime limits (`sqlite3_limit`): attached databases, statement length, value length, and friends.
3. Cancelling a running statement from a context deadline.
4. An online copy of a live database (backup API or an engine-run `VACUUM INTO`).
5. Read-only connections.
6. JSON1 and FTS5 available to users.

Record the choice in `docs/adr/0001-sqlite-driver.md` with the versions tested.

### 6.2 Isolation (the part that must not be wrong)

One database must never read or write another database's file, the meta database, or anything else on disk. Every row below is a named test that runs user SQL through the real query path and asserts it fails cleanly:

| Attack | Expected |
|---|---|
| `ATTACH DATABASE '<other branch file>'` and `DETACH` | denied |
| `VACUUM INTO '<path>'` | denied |
| `load_extension(...)` as SQL function and as API | denied |
| `PRAGMA writable_schema`, `journal_mode`, `temp_store_directory`, `locking_mode`, and any pragma not on the allowlist | denied or ignored without effect |
| Writes with a read-only token (INSERT, DDL, `PRAGMA user_version = 1`) | denied by the read-only connection |
| Infinite recursive CTE | cancelled at the statement timeout, connection still usable after |
| Result with more rows or bytes than the cap | error with a stable code, no partial success reported as full |
| `randomblob(2000000000)` and oversized strings | denied by length limit |
| Filling the disk | stopped by the per-branch size limit, then by the instance quota |
| A user-defined view or trigger that calls a denied function | denied at run time (trusted schema off) |

Add a Go fuzz test that feeds statements through the query path and fails if any file outside that branch's own files is created, opened for write or changed. Run it in CI for a bounded time.

Also: defensive mode on, foreign keys on by default, WAL journal mode, a busy timeout, and one writer connection per branch file with a small pool of readers.

### 6.3 Limits (all configurable, all documented)

Statement timeout (default 30 s), max rows and max bytes per response, max request body, max bound parameters, max size per branch, max total size for the instance, max databases, max branches per database, idle connection close, cap on open files. Before a branch, snapshot or import is created, check free disk on the host and refuse under a threshold with HTTP 507. The Mac mini's disk is tight, so this guard matters.

### 6.4 Values over JSON

Decide the encoding once, document it, and round-trip test it: integers beyond 2^53, blobs, NULL, real versus integer, and column names that repeat. No silent precision loss.

### 6.5 Branches

A branch is a full copy of its parent at the moment it was made, taken with the online copy so writers aren't blocked for long.

- Every database starts with a default branch named `main`.
- Operations: create (from any branch or snapshot), list, rename, delete, reset (copy the parent again), promote (make this branch the default; the old default stays as a normal branch), schema diff against any other branch.
- Schema diff compares tables, columns, indexes, views and triggers from `sqlite_schema` and returns structured changes plus the DDL on both sides.
- Branches do not merge data back. Say so plainly in the docs and the UI. The supported flow is: branch, test a migration on the branch, then apply the same migration to the default branch.
- A protected flag blocks delete and reset. The default branch is always protected.
- Optional expiry (auto-delete after N days) for preview branches.
- A copy taken while writes are running must pass `PRAGMA integrity_check` and contain only committed transactions. Test this under concurrent load.

### 6.6 Snapshots and restore

- Manual and scheduled snapshots per branch, with retention by count and age.
- Restore never overwrites in place. It creates a new branch from the snapshot. Promoting it is a separate, explicit step.
- Deleting a database is a soft delete into `trash/` with a retention period, and can be undone until purge.

### 6.7 Import and export

- Import a `.db` file (check the header, run `integrity_check`, open it under the same isolation rules) or a `.sql` dump.
- Export a consistent `.db` copy or a SQL dump.

### 6.8 Tokens

- Format `lmb_<id>_<secret>`. Only a SHA-256 hash is stored. The full token is shown once.
- Scopes: instance admin, per database with read, write or admin (optionally limited to one branch), or per bucket (section 16.4). Optional expiry and S3 access key. Last-used time recorded.
- Constant-time comparison. Rate limit per token. Failed auth is rate limited by address.

### 6.9 Audit and stats

- Audit log for every management action: who (token or dashboard user), what, when, result.
- Per-branch counters in rolling buckets: queries, rows read and written, errors, duration percentiles. Bounded slow-query log. All of it capped in size.

### 6.10 Health and upgrades

- `GET /health` returns 200 only when the engine can serve. It fails when `meta.db` is newer than the binary, so a bad rollback shows up as failed.
- Meta migrations are embedded, forward-only, and run at start. `scripts/deploy-up.sh` backs up `meta.db` right before the swap.

## 7. REST API

Base path `/v1`, `Authorization: Bearer <token>`. `{db}` and `{branch}` accept a name or an id. Names match `[a-z0-9][a-z0-9-]{0,62}`.

```
GET  POST            /v1/databases
GET  PATCH  DELETE   /v1/databases/{db}
POST                 /v1/databases/{db}/restore            (undo a soft delete)
GET  POST            /v1/databases/{db}/branches
GET  PATCH  DELETE   /v1/databases/{db}/branches/{branch}
POST                 /v1/databases/{db}/branches/{branch}/reset
POST                 /v1/databases/{db}/branches/{branch}/promote
GET                  /v1/databases/{db}/branches/{branch}/diff?against={branch}
GET                  /v1/databases/{db}/branches/{branch}/schema
POST                 /v1/databases/{db}/branches/{branch}/query
POST                 /v1/databases/{db}/branches/{branch}/batch
POST                 /v1/databases/{db}/query               (default branch)
POST                 /v1/databases/{db}/batch               (default branch)
GET  POST            /v1/databases/{db}/branches/{branch}/snapshots
POST                 /v1/databases/{db}/snapshots/{snapshot}/restore
POST                 /v1/databases/{db}/branches/{branch}/import
GET                  /v1/databases/{db}/branches/{branch}/export
GET                  /v1/databases/{db}/branches/{branch}/stats
GET  POST            /v1/tokens
DELETE               /v1/tokens/{token_id}
GET                  /v1/audit
GET                  /v1/openapi.json
GET                  /health
```

Added while building milestone 3, because the dashboard and the retention rules need them: `GET /v1/trash` (the trash list), `DELETE /v1/databases/{db}/snapshots/{snapshot}` (manual snapshots are never removed by retention, so they need a delete), and `GET PUT DELETE /v1/databases/{db}/branches/{branch}/snapshot-schedule` (scheduled snapshots). A query or batch body may set `"read_only": true` to use a read-only connection with a write token, which MCP's `query` tool relies on.

Rules:

- `query` runs one statement with bound parameters. `batch` runs a list of statements in one transaction, all or nothing. Interactive transactions are on the libSQL protocol endpoint (7.1).
- Errors use `application/problem+json` with a stable machine-readable `code`. The list of codes is part of the docs.
- Create endpoints accept `Idempotency-Key`, except token creation: replaying it would mean storing the secret. Lists are cursor-paginated.
- The OpenAPI document is generated from the Go handlers and committed as `apps/engine/openapi.json`. CI fails when the committed file differs from a fresh generation. The SDK types and the docs API reference are both generated from it, so the three can't drift.

### 7.1 libSQL protocol

Added in milestone 11 (ADR 0005). The libSQL remote protocol over HTTP (Hrana v2 and v3, JSON) at `/libsql/{db}/{branch}/`, so `@libsql/client` and `drizzle-orm/libsql` work against a branch by URL alone. Same bearer tokens, scopes, limits and isolation as REST. Streams with batons carry interactive transactions: the engine recognizes BEGIN, COMMIT and ROLLBACK itself and runs its own, and savepoints are allowed inside an open transaction. Streams have an idle timeout, transactions a lifetime, and both are capped per token and per instance.

## 8. MCP

- Streamable HTTP endpoint at `/mcp` on the engine, same bearer tokens and scopes as REST.
- Tools: `list_databases`, `get_database`, `create_database`, `list_branches`, `create_branch`, `delete_branch`, `reset_branch`, `diff_branches`, `get_schema`, `query` (always on a read-only connection), `execute` (needs a write scope), `list_snapshots`, `create_snapshot`.
- Every tool has correct read-only and destructive annotations, a tight input schema, and capped output sized for a model's context (row and byte caps, with a note when truncated).
- Check the current MCP spec and the Go SDK docs before writing it. Test with a real client: `mcporter` against a local engine.
- Later milestone: OAuth so it can be added as a connector in Claude on the web.

## 9. SDK and CLI

### `@lumbung/client` (TypeScript)

- `createClient({ url, token, database, branch? })`, then `query`, `batch`, and typed management calls.
- No runtime dependencies, uses `fetch`, works in Node, Bun, Workers and the browser.
- Typed errors that carry the API's `code`. Retries only on safe reads.
- `@lumbung/client/drizzle`: adapter on Drizzle's `sqlite-proxy` driver, with a tested example.
- Tests run against a real engine binary, not mocks.
- Published to GitHub Packages (the repository owner's scope, `UNLICENSED`) by `.github/workflows/release-client.yml` on a `client-vX.Y.Z` tag. Release steps are in `ops/README.md`.

### `lumbung` (CLI)

`login`, `db list|create|delete|restore`, `branch list|create|delete|reset|promote|diff`, `query`, `shell` (REPL), `migrate apply|status`, `snapshot list|create|restore`, `token list|create|revoke`, `import`, `export`.

- `migrate apply` runs a folder of `.sql` files in order, each in its own transaction, and records them in a `_lumbung_migrations` table. Works with the SQL files drizzle-kit generates.
- `--json` on every command for scripts. Destructive commands ask for confirmation unless `--yes`.
- The CLI reference page in the docs is generated from the CLI's own help output.

## 10. Dashboard

Built with `/zaki-design-ui` on ui-sdk components (ADR 0004; this said shadcn until milestone 8). Screens:

- First-run setup (creates the owner), login, accept invite.
- Databases: list with size, branch count, last activity. Create. Trash with restore.
- Database, with a branch switcher in the header:
  - Overview: size, table count, request chart, connect snippets (SDK, CLI, curl, MCP) with copy buttons.
  - Data: browse a table, filter, sort, paginate, edit a cell, insert and delete rows.
  - SQL: editor, results, history, the plan for a query.
  - Schema: tables, columns, indexes, DDL.
  - Branches: list, create, diff against parent, reset, promote, delete.
  - Snapshots: list, create, schedule, restore to a new branch.
  - Settings: rename, limits, import, export, delete.
- Tokens (for a database, a branch, a bucket or everything, with an optional S3 access key), Members and invites, Audit log, Instance settings.

Destructive actions need a typed confirmation. Every list has empty, loading and error states. Run `break-ui` on anything that renders user data: long table names, a 300-column table, a 5 MB text cell, blobs, NULLs, zero rows.

## 11. Site and docs

One Astro app: landing page at `/`, Starlight docs at `/docs`. Docs are written with each milestone, not at the end. A milestone isn't done until its pages exist.

Structure:

- Start: What is Lumbung, Quickstart (running instance to first query in under 5 minutes), Install.
- Concepts: Databases, Branches, Snapshots, Tokens and scopes, Limits, Security model.
- Guides: Connect from Node, from Workers, from Next.js. Use with Drizzle. Run migrations. A branch per pull request. Back up and restore. Import an existing SQLite file. Use from an AI agent.
- Reference: REST API (generated from OpenAPI), SDK, CLI (generated), MCP tools, Error codes, Configuration (every setting with default), Value encoding.
- Self-hosting: Docker Compose, Reverse proxy and TLS, Backups, Upgrades, Monitoring, Hardening.
- Troubleshooting, Changelog.

Rules that keep the docs correct:

1. Every code sample in the docs is extracted and run against a live engine in CI (`scripts/docs-test`). A sample that can't run is marked and explained.
2. API, CLI and configuration reference pages are generated from the code, never typed by hand.
3. A link checker and the wording check run in CI.
4. `llms.txt` and per-page Markdown are served for agents.
5. Each page answers one question, leads with the working example, and uses real values that work against the quickstart instance.

The landing page goes through `/zaki-design-ui` including its Ship phase (metadata, OG image, favicon set, sitemap, robots).

## 12. Deployment

Generic (documented for everyone): one `compose.yaml` with `engine` and `dashboard` services and one data volume, both bound to localhost, healthchecks on both.

Ours (in `ops/`, done with the `zaki-selfhost` skill):

- Checkout at `~/selfhost/lumbung`, two ports from `free-port.sh`, `mem_limit` on both services.
- One public hostname, `lumbung.zakiego.com`, through an edge Worker like palui's: `/` and `/docs` to Pages, `/app` to the dashboard, `/v1`, `/mcp`, `/libsql` and `/health` to the engine.
- `scripts/deploy-up.sh` backs up `meta.db` right before the swap. A nightly job copies every default branch and the meta database to a backup folder with retention. Test a restore from that backup once before calling it done.
- Add the health URL to `m4-alerts`, the row to the Apps table in the `zaki-selfhost` skill, and the repo to the auto-deploy allowlist.
- Docker build memory: the OrbStack VM has 3 GB. Keep the Go build and the Nitro build in separate stages and never run them in parallel.

Steps only Zaki can do (ask once, together): restart the tunnel after the ingress change.

## 13. Testing bar

- Go: unit and integration tests on real SQLite files in temp dirs, `go test -race`, `go vet`, staticcheck, govulncheck, the isolation table in 6.2, the fuzz test, concurrency tests (parallel writers, branch during writes), and a crash test (kill the process mid-write, reopen, `integrity_check` passes).
- API: contract tests from the OpenAPI document, plus golden responses for errors.
- SDK and CLI: tests against a real engine binary.
- Dashboard: typecheck, component tests for the data grid and SQL console, one browser end-to-end run.
- One end-to-end script, run in CI: boot engine and dashboard, set up the owner, create a database, create a table, insert, branch, change the schema on the branch, diff, promote, snapshot, restore, create a token, query through the SDK, call MCP `tools/list` and `query`.
- CI on every push: lint, typecheck, Go tests, TS tests, OpenAPI drift check, docs tests, link check, wording check, site build.
- Each milestone ends with a cold review by a fresh subagent (code review at high effort) and every HIGH and MEDIUM finding fixed before the next milestone starts.

## 14. Milestones

Work top to bottom. Each one ends green in CI, with its docs pages written.

| # | Milestone | Done when |
|---|---|---|
| 0 | Scaffold | Workspace, Go module, CI, wording check, `t3.json` actions, Starlight skeleton building, AGENTS.md commands filled in |
| 1 | Engine core | Driver ADR, storage layout, connection manager, query and batch, limits, every row of 6.2 passing, fuzz test in CI |
| 2 | Branches and snapshots | 6.5, 6.6, 6.7 with the concurrency and crash tests |
| 3 | REST API | Section 7 complete, tokens, audit, stats, OpenAPI generated and drift-checked |
| 4 | SDK and CLI | Section 9, Drizzle example, `migrate` |
| 5 | MCP | Section 8, verified with mcporter |
| 6 | Docs pass | Every page in section 11 exists, docs tests green |
| 7 | Design stop | Explore variants for the dashboard's main screen and the landing hero are ready. Ask Zaki to pick both at once, and for the tunnel restart later |
| 8 | Dashboard | Section 10, reviewed per `/zaki-design-ui` |
| 9 | Landing | Landing page shipped per `/zaki-design-ui` Ship |
| 10 | Deploy | Section 12, live at the public hostname, backup and restore tested |
| 11 | libSQL protocol | `@libsql/client` and Drizzle's libsql driver work unchanged against a branch, including interactive transactions |

Object storage continues with milestones 12 to 17 in section 16.9.

Later, not scheduled: MCP OAuth for Claude on the web, publishing the CLI, publishing the SDK to public npm, the open-source checklist (license, security policy, contribution guide), read replicas.

## 15. Open questions for Zaki

- License for when the repo goes public.
- Whether the public hostname stays `lumbung.zakiego.com` or moves to its own domain.

## 16. Object storage

Added after milestone 11, on top of the shipped product. Buckets of files live next to the databases, in the same engine, behind the same tokens, dashboard, CLI, SDK and MCP. ADR 0006 records the protocol, layout, auth and limits.

### 16.1 Model

- A bucket belongs to the instance. Names match `[a-z0-9][a-z0-9-]{1,61}[a-z0-9]` (3 to 63 characters, so every S3 client accepts them). Bucket ids are `bk_...`.
- An object has a key (1 to 1024 bytes of UTF-8, no control characters), a size, an ETag, a content type, the standard headers (`Cache-Control`, `Content-Disposition`, `Content-Encoding`, `Content-Language`) and custom metadata (`x-amz-meta-*`, 2 KB in total). Keys are opaque strings: they never become file paths.
- The ETag is the MD5 of the content, or for a multipart object the MD5 of the parts' MD5s with a `-<parts>` suffix, as S3 clients expect.
- Writes are atomic: a reader sees the old object or the new one, never a partial file. The last write wins.
- No versioning, no lifecycle rules, no object locks. A bucket can only be deleted when it's empty.
- Per bucket: `public_read` (anyone may GET and HEAD objects without a token; listing still needs one) and `cors_origins` (origins a browser may call the S3 API from, for presigned uploads).

### 16.2 S3-compatible API

At `/v1/s3/`, path-style: `/v1/s3/<bucket>/<key>`. It sits under `/v1`, so our edge Worker and tunnel already forward it. Clients set the endpoint to `https://<host>/v1/s3` with path-style addressing and any region.

Operations: ListBuckets, CreateBucket, HeadBucket, DeleteBucket, GetBucketLocation, GetBucketVersioning (always unversioned), PutObject, GetObject (one `Range`, conditional headers, `response-*` overrides), HeadObject, DeleteObject, DeleteObjects, CopyObject (within and across buckets, `COPY` or `REPLACE` metadata), ListObjectsV2 and ListObjects (prefix, delimiter, pagination, `encoding-type=url`), CreateMultipartUpload, UploadPart, CompleteMultipartUpload, AbortMultipartUpload, ListParts, ListMultipartUploads. Anything else answers `NotImplemented`.

- Auth is AWS Signature Version 4, in the `Authorization` header or as a presigned URL (query string, at most `LUMBUNG_PRESIGN_MAX_EXPIRY`). Payloads may be signed, `UNSIGNED-PAYLOAD`, or `aws-chunked` with signed chunks or trailing checksums; every chunk signature and declared checksum (CRC32, CRC32C, CRC64NVME, SHA-1, SHA-256, Content-MD5) is checked before the object is committed.
- Errors are S3 XML with S3 error codes. Every request gets `x-amz-request-id`.
- The engine routes `/v1/s3/` before the general router, so keys with `//`, `.` or `..` segments reach it unchanged.

### 16.3 REST, SDK, CLI, MCP

REST under `/v1` (JSON, problem+json errors, in the OpenAPI document):

```
GET  POST            /v1/buckets
GET  PATCH  DELETE   /v1/buckets/{bucket}
GET                  /v1/buckets/{bucket}/objects?prefix=&delimiter=&cursor=&limit=
PUT  GET  HEAD DELETE /v1/buckets/{bucket}/object?key=     (raw body in and out; GET takes Range)
POST                 /v1/buckets/{bucket}/copy             (from another bucket or key)
POST                 /v1/buckets/{bucket}/presign          (a presigned URL, signed with the token's access key)
```

`POST /v1/tokens` takes `bucket` (instead of `database`) and `access_key: true`, which returns an S3 access key id and secret once.

- SDK: `client.buckets` (list, create, get, update, delete) and `client.bucket(name)` (put, get with range, head, delete, list, copy, presign). Same rules as section 9: no runtime dependencies, tests against a real engine.
- CLI: `bucket list|create|delete|update`, `object ls|put|get|rm|cp|presign`, and `token create --bucket --access-key`.
- MCP tools: `list_buckets`, `create_bucket`, `list_objects`, `get_object` (text up to the context cap, metadata only for binary), `put_object` (text or base64, capped), `delete_object`, `presign_object`. Same annotations and caps as section 8.

### 16.4 Tokens and access keys

- New token scope `bucket`: read, write or admin on one bucket. Instance tokens reach every bucket at their level. Database tokens never reach a bucket, and bucket tokens never reach a database.
- read: list, GET, HEAD, presign GET. write: also PUT, DELETE, copy into the bucket, multipart, presign PUT. admin: also bucket settings and tokens for the bucket. Creating and deleting buckets needs an instance admin.
- An access key belongs to one token: id `LMB` + the token's id in capitals, secret 40 random characters. It has the token's scope, expiry, rate limit and audit identity, and revoking the token revokes the key and every URL it signed. SigV4 needs the secret on the server, so it is stored encrypted (AES-256-GCM, key in `secrets/access-key-encryption-key`, mode 600, made on first start).
- Failed signatures count against the per-address failed-auth limit. Anonymous public reads have their own per-address limit.

### 16.5 Isolation (the part that must not be wrong)

Each row is a named test through the real HTTP handlers, for both the S3 API and REST:

| Attack | Expected |
|---|---|
| A token for bucket A reads, lists, heads, writes, deletes, copies or presigns in bucket B | denied, the same answer as a bucket that doesn't exist |
| CopyObject into A from B with a token for A | denied (the source needs read) |
| Read token: PUT, DELETE, DeleteObjects, copy, any multipart step, presign PUT, bucket settings | denied |
| Write token: bucket settings, delete bucket, create bucket, tokens | denied |
| Database token on any bucket operation; bucket token on databases, `/libsql`, SQL MCP tools | denied |
| A multipart upload id from bucket B used under bucket A, or by another bucket's token | `NoSuchUpload` |
| Presigned URL with its path, method, expiry or headers changed; expired; signed by a revoked token | denied |
| Signed payload hash or chunk signature that doesn't match the body; wrong secret; clock skew past 15 minutes | denied, nothing stored |
| Anonymous PUT, DELETE or list on a public bucket; anonymous GET on a private or missing bucket | denied, private and missing look the same |
| Keys like `../../meta.db`, `%2e%2e/x`, `/abs`, `a//b`, 1025 bytes, control characters | stored as opaque keys or refused; no file outside the bucket's blob folder is touched |
| Object, bucket or instance storage past its limit; disk under `LUMBUNG_MIN_FREE_DISK` | refused before or while writing, nothing partial kept |
| The access key secret in meta.db or a meta-only backup | never in plain text |

A Go fuzz test sends random methods, paths, keys and headers through the S3 handler and fails if any file outside `objects/`, `uploads/` and `tmp/` is created or changed. CI runs it for a bounded time. Section 6.2's tests stay as they are.

### 16.6 Storage

```
$DATA_DIR/objects/<bucket id>/<2 chars>/<blob id>    one immutable file per object version
$DATA_DIR/uploads/<upload id>/<part>                 multipart parts in progress
$DATA_DIR/secrets/access-key-encryption-key
```

The index (buckets, objects, uploads, parts) lives in meta.db. A write streams to `tmp/`, is checked (size, hashes, checksums), renamed into `objects/`, then recorded in one meta transaction. A replaced or deleted object's file is removed after `LUMBUNG_OBJECT_GC_DELAY`, so a backup running meanwhile can still copy it. Files meta.db doesn't know are swept at start and by housekeeping. Unfinished multipart uploads are aborted after `LUMBUNG_MULTIPART_EXPIRY`.

Limits (section 6.3 style, all settings): object size, bucket size, total object storage (objects plus parts in progress), bucket count, presign expiry, anonymous read rate. Writes are refused with HTTP 507 under the free-disk threshold.

### 16.7 Backup and restore

`lumbungd backup` copies every live object's file and the encryption key with meta.db, listed in the manifest with SHA-256 sums. Files are immutable, so a file already in the previous backup is hard-linked instead of copied, with its sum reused. `--skip-objects` leaves them out. Restore copies them back, and reconcile drops object rows whose file isn't in the backup (fixing bucket totals) and every unfinished upload. `ops/restore-drill.sh` also lists every bucket and downloads objects to check their size and ETag. A `--skip-objects` backup is a kind of its own (`<time>-no-objects`), so it is never restored by default and never pushes full backups out of retention. Object files are copied right after meta.db, before branches, so the GC delay covers them; a file linked from the previous backup is checked against its sum first.

### 16.8 Dashboard and docs

- Dashboard: Buckets in the sidebar. List with size and object count, create. Bucket browser with folders from `/`, breadcrumbs, upload (streamed through the dashboard, not buffered), download, delete, empty and loading and error states. Settings: public read, CORS origins, delete with a typed confirmation. Read-only members browse and download; members upload and delete; owners change settings and create or delete buckets.
- Docs: Concepts: Buckets and objects; Tokens and scopes and Limits updated. Guides: Use an S3 client, Store files from Node, Browser uploads with presigned URLs. Reference: S3 API (operations, auth, error codes). Self-hosting: Backups updated. Samples run in `scripts/docs-test`; S3 samples run inside `examples/s3`.
- `examples/s3`: the AWS SDK for JavaScript, unchanged, against a real engine in CI: put, get with a range, head, list with prefix and delimiter, copy, delete, multipart (by hand and with its upload helper), presigned GET and PUT.

### 16.9 Milestones

| # | Milestone | Done when |
|---|---|---|
| 12 | Object store core | meta migration, bucket and object store in the engine, multipart, quotas, GC and orphan sweep, bucket token scope and access keys, settings and error codes, unit tests |
| 13 | S3 API | 16.2 complete, every row of 16.5 passing, fuzz test in CI, `examples/s3` green in CI |
| 14 | REST, SDK, CLI | 16.3 REST, SDK and CLI with tests against a real engine, OpenAPI and generated docs current |
| 15 | MCP and docs | MCP tools verified with mcporter, every page in 16.8 with samples passing |
| 16 | Backup and restore | 16.7 with tests, restore drill updated and run once against a local backup |
| 17 | Dashboard | 16.8 screens, `break-ui` on the browser, end-to-end script walks a bucket |

Each ends with a cold review by a fresh subagent and every HIGH and MEDIUM finding fixed before the next starts.
