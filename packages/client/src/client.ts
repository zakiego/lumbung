import { LumbungError } from "./errors.ts"
import type {
  AuditOut,
  BranchOut,
  BucketOut,
  DatabaseOut,
  Diff,
  ObjectOut,
  Problem,
  ResultOut,
  ScheduleOut,
  Schema,
  SnapshotOut,
  StatsOutBody,
  TokenOut,
  WireParam,
} from "./generated.ts"
import { decodeValue, encodeValue, type InValue, type IntMode, type Value } from "./values.ts"

export interface ClientOptions {
  /** The engine's base URL, e.g. http://127.0.0.1:4870. */
  url: string
  /** A Lumbung token, lmb_<id>_<secret>. */
  token: string
  /** Database name or id for query and batch. Optional for management-only use. */
  database?: string
  /** Branch name or id. Defaults to the database's default branch. */
  branch?: string
  /** How integers come back. Default "number", which throws past 2^53. */
  intMode?: IntMode
  /** A fetch to use instead of the global one. */
  fetch?: typeof fetch
  /** Extra headers on every request. */
  headers?: Record<string, string>
  /** Retries for safe reads (GET, and queries with readOnly). Default 2. */
  retries?: number
}

/** A statement: SQL plus positional args or named params. */
export type Statement =
  | string
  | { sql: string; args?: InValue[] | Record<string, InValue> }

export interface QueryOptions {
  /** Run on a read-only connection even if the token can write. Also makes the query safe to retry. */
  readOnly?: boolean
  /** Cancel the request. */
  signal?: AbortSignal
}

export interface BatchOptions extends QueryOptions {
  /**
   * Schema migration mode: foreign key enforcement is off for the batch and
   * every foreign key is checked once before commit, so rebuilding a table
   * doesn't fire ON DELETE actions. Needs a write token.
   */
  migration?: boolean
}

/** A statement's result. */
export interface Result {
  /** Column names, in order. Repeated names appear more than once. */
  columns: string[]
  /** Declared column types, or "" for expressions. */
  columnTypes: string[]
  /** Rows as objects keyed by column name. With repeated names, the last column wins; use `values` then. */
  rows: Record<string, Value>[]
  /** Rows as arrays in column order. */
  values: Value[][]
  rowsAffected: number
  /** The last inserted rowid, as a bigint (rowids can pass 2^53), or 0n. */
  lastInsertRowid: bigint
}

/** A page of a list. */
export interface Page<T> {
  data: T[]
  nextCursor: string
}

export interface ListOptions {
  limit?: number
  cursor?: string
}

function statementBody(st: Statement): { sql: string; args?: WireParam[]; named?: Record<string, WireParam> } {
  if (typeof st === "string") return { sql: st }
  if (st.args === undefined) return { sql: st.sql }
  if (Array.isArray(st.args)) return { sql: st.sql, args: st.args.map(encodeValue) }
  const named: Record<string, WireParam> = {}
  for (const [k, v] of Object.entries(st.args)) named[k] = encodeValue(v)
  return { sql: st.sql, named }
}

function toStatement(sql: Statement, args?: InValue[] | Record<string, InValue>): Statement {
  if (typeof sql === "string" && args !== undefined) return { sql, args }
  return sql
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

/** The Lumbung client. Create one with `createClient`. */
export class Client {
  readonly #opts: Required<Pick<ClientOptions, "url" | "token" | "intMode" | "retries">> & ClientOptions
  readonly #fetch: typeof fetch

  constructor(opts: ClientOptions) {
    if (!opts.url) throw new TypeError("createClient: url is required")
    if (!opts.token) throw new TypeError("createClient: token is required")
    this.#opts = { intMode: "number", retries: 2, ...opts, url: opts.url.replace(/\/+$/, "") }
    this.#fetch = opts.fetch ?? globalThis.fetch.bind(globalThis)
  }

  /** A client for another branch of the same database. */
  branch(name: string): Client {
    return new Client({ ...this.#opts, branch: name })
  }

  /** A client for another database (and its default branch). */
  database(name: string, branch?: string): Client {
    return new Client({ ...this.#opts, database: name, branch })
  }

  // ---- transport

  async request<T>(method: string, path: string, opts: { body?: unknown; query?: Record<string, string | number | undefined>; safe?: boolean; signal?: AbortSignal; headers?: Record<string, string> } = {}): Promise<T> {
    const url = new URL(this.#opts.url + path)
    for (const [k, v] of Object.entries(opts.query ?? {})) {
      if (v !== undefined && v !== "") url.searchParams.set(k, String(v))
    }
    const headers: Record<string, string> = {
      authorization: `Bearer ${this.#opts.token}`,
      accept: "application/json",
      ...this.#opts.headers,
      ...opts.headers,
    }
    let body: BodyInit | undefined
    if (opts.body !== undefined) {
      if (opts.body instanceof Uint8Array || opts.body instanceof ArrayBuffer || typeof opts.body === "string") {
        body = opts.body as BodyInit
      } else {
        headers["content-type"] = "application/json"
        body = JSON.stringify(opts.body)
      }
    }
    const safe = opts.safe ?? method === "GET"
    const attempts = safe ? this.#opts.retries + 1 : 1
    let lastError: unknown
    for (let attempt = 0; attempt < attempts; attempt++) {
      if (attempt > 0) await sleep(Math.min(200 * 2 ** (attempt - 1), 2000))
      let res: Response
      try {
        res = await this.#fetch(url, { method, headers, body, signal: opts.signal })
      } catch (err) {
        if (opts.signal?.aborted) throw err
        lastError = new LumbungError("network_error", 0, `request to ${url.origin} failed`, { cause: err })
        continue
      }
      if (res.ok) {
        if (res.status === 204) return undefined as T
        try {
          return (await res.json()) as T
        } catch (err) {
          // A cut-off or non-JSON success body (a proxy in the way).
          lastError = new LumbungError("invalid_response", res.status, "the response body isn't valid JSON", {
            requestId: res.headers.get("x-request-id") ?? undefined,
            cause: err,
          })
          continue
        }
      }
      const error = await toError(res)
      lastError = error
      // Retry only what a retry can fix.
      if (!(safe && (res.status === 503 || res.status === 429 || res.status === 502 || res.status === 504))) throw error
      const retryAfter = Number(res.headers.get("retry-after"))
      if (retryAfter > 0 && retryAfter <= 5) await sleep(retryAfter * 1000)
    }
    throw lastError
  }

  async raw(method: string, path: string, opts: { body?: BodyInit; query?: Record<string, string>; contentType?: string; signal?: AbortSignal } = {}): Promise<Response> {
    const url = new URL(this.#opts.url + path)
    for (const [k, v] of Object.entries(opts.query ?? {})) url.searchParams.set(k, v)
    const headers: Record<string, string> = { authorization: `Bearer ${this.#opts.token}`, ...this.#opts.headers }
    if (opts.contentType) headers["content-type"] = opts.contentType
    let res: Response
    try {
      res = await this.#fetch(url, { method, headers, body: opts.body, signal: opts.signal })
    } catch (err) {
      throw new LumbungError("network_error", 0, `request to ${url.origin} failed`, { cause: err })
    }
    if (!res.ok) throw await toError(res)
    return res
  }

  #target(): string {
    const db = this.#opts.database
    if (!db) throw new TypeError("this client has no database; pass database to createClient or use client.database(name)")
    const base = `/v1/databases/${encodeURIComponent(db)}`
    return this.#opts.branch ? `${base}/branches/${encodeURIComponent(this.#opts.branch)}` : base
  }

  #decode(r: ResultOut): Result {
    const columns = r.columns.map((c) => c.name)
    const values = r.rows.map((row) => row.map((v) => decodeValue(v, this.#opts.intMode)))
    const rows = values.map((row) => {
      const o: Record<string, Value> = {}
      columns.forEach((c, i) => {
        o[c] = row[i] ?? null
      })
      return o
    })
    return {
      columns,
      columnTypes: r.columns.map((c) => c.decltype),
      rows,
      values,
      rowsAffected: r.rows_affected,
      lastInsertRowid: BigInt(r.last_insert_rowid),
    }
  }

  // ---- SQL

  /** Runs one statement in its own transaction. */
  async query(sql: Statement, args?: InValue[] | Record<string, InValue>, opts: QueryOptions = {}): Promise<Result> {
    const body = { ...statementBody(toStatement(sql, args)), read_only: opts.readOnly || undefined }
    const r = await this.request<ResultOut>("POST", `${this.#target()}/query`, { body, safe: opts.readOnly === true, signal: opts.signal })
    return this.#decode(r)
  }

  /** Runs statements in order in one transaction: all take effect or none do. */
  async batch(statements: Statement[], opts: BatchOptions = {}): Promise<Result[]> {
    const body = { statements: statements.map(statementBody), read_only: opts.readOnly || undefined, migration: opts.migration || undefined }
    const r = await this.request<{ results: ResultOut[] }>("POST", `${this.#target()}/batch`, { body, safe: opts.readOnly === true, signal: opts.signal })
    return r.results.map((x) => this.#decode(x))
  }

  // ---- management

  readonly databases = {
    list: (o: ListOptions = {}) => this.#page<DatabaseOut>("/v1/databases", o),
    trash: (o: ListOptions = {}) => this.#page<DatabaseOut>("/v1/trash", o),
    get: (db: string) => this.request<DatabaseOut>("GET", `/v1/databases/${enc(db)}`),
    create: (name: string, o: { idempotencyKey?: string } = {}) =>
      this.request<DatabaseOut>("POST", "/v1/databases", { body: { name }, headers: idem(o.idempotencyKey) }),
    rename: (db: string, name: string) => this.request<DatabaseOut>("PATCH", `/v1/databases/${enc(db)}`, { body: { name } }),
    delete: (db: string) => this.request<DatabaseOut>("DELETE", `/v1/databases/${enc(db)}`),
    restore: (db: string, name?: string) => this.request<DatabaseOut>("POST", `/v1/databases/${enc(db)}/restore`, { body: name ? { name } : {} }),
  }

  readonly branches = {
    list: (db: string, o: ListOptions = {}) => this.#page<BranchOut>(`/v1/databases/${enc(db)}/branches`, o),
    get: (db: string, branch: string) => this.request<BranchOut>("GET", `${branchPath(db, branch)}`),
    create: (
      db: string,
      name: string,
      o: { from?: string; fromSnapshot?: string; protected?: boolean; expiresInDays?: number; idempotencyKey?: string } = {},
    ) =>
      this.request<BranchOut>("POST", `/v1/databases/${enc(db)}/branches`, {
        body: { name, from: o.from, from_snapshot: o.fromSnapshot, protected: o.protected, expires_in_days: o.expiresInDays },
        headers: idem(o.idempotencyKey),
      }),
    update: (db: string, branch: string, o: { name?: string; protected?: boolean; expiresInDays?: number }) =>
      this.request<BranchOut>("PATCH", branchPath(db, branch), { body: { name: o.name, protected: o.protected, expires_in_days: o.expiresInDays } }),
    delete: (db: string, branch: string) => this.request<void>("DELETE", branchPath(db, branch)),
    reset: (db: string, branch: string) => this.request<BranchOut>("POST", `${branchPath(db, branch)}/reset`),
    promote: (db: string, branch: string) => this.request<BranchOut>("POST", `${branchPath(db, branch)}/promote`),
    diff: (db: string, branch: string, against: string) => this.request<Diff>("GET", `${branchPath(db, branch)}/diff`, { query: { against } }),
    schema: (db: string, branch: string) => this.request<Schema>("GET", `${branchPath(db, branch)}/schema`),
    stats: (db: string, branch: string, window: "1h" | "6h" | "24h" | "48h" = "1h") =>
      this.request<StatsOutBody>("GET", `${branchPath(db, branch)}/stats`, { query: { window } }),
  }

  readonly snapshots = {
    list: (db: string, branch: string, o: ListOptions = {}) => this.#page<SnapshotOut>(`${branchPath(db, branch)}/snapshots`, o),
    create: (db: string, branch: string, o: { label?: string; idempotencyKey?: string } = {}) =>
      this.request<SnapshotOut>("POST", `${branchPath(db, branch)}/snapshots`, { body: { label: o.label }, headers: idem(o.idempotencyKey) }),
    restore: (db: string, snapshot: string, branchName: string) =>
      this.request<BranchOut>("POST", `/v1/databases/${enc(db)}/snapshots/${enc(snapshot)}/restore`, { body: { name: branchName } }),
    delete: (db: string, snapshot: string) => this.request<void>("DELETE", `/v1/databases/${enc(db)}/snapshots/${enc(snapshot)}`),
    schedule: {
      get: (db: string, branch: string) => this.request<ScheduleOut>("GET", `${branchPath(db, branch)}/snapshot-schedule`),
      set: (db: string, branch: string, o: { everyMinutes: number; keepCount?: number; keepDays?: number }) =>
        this.request<ScheduleOut>("PUT", `${branchPath(db, branch)}/snapshot-schedule`, {
          body: { every_minutes: o.everyMinutes, keep_count: o.keepCount, keep_days: o.keepDays },
        }),
      delete: (db: string, branch: string) => this.request<void>("DELETE", `${branchPath(db, branch)}/snapshot-schedule`),
    },
  }

  readonly tokens = {
    list: (o: ListOptions = {}) => this.#page<TokenOut>("/v1/tokens", o),
    /**
     * Creates a token. The full token is in `token` and is only shown here.
     * With `accessKey`, the token also gets an S3 access key; its secret is
     * in `secret_access_key`, also shown only here.
     */
    create: (o: {
      name: string
      access: "read" | "write" | "admin"
      database?: string
      branch?: string
      bucket?: string
      accessKey?: boolean
      expiresInDays?: number
    }) =>
      this.request<TokenOut & { token: string; secret_access_key?: string }>("POST", "/v1/tokens", {
        body: {
          name: o.name,
          access: o.access,
          database: o.database,
          branch: o.branch,
          bucket: o.bucket,
          access_key: o.accessKey || undefined,
          expires_in_days: o.expiresInDays,
        },
      }),
    revoke: (id: string) => this.request<void>("DELETE", `/v1/tokens/${enc(id)}`),
  }

  readonly audit = {
    list: (o: ListOptions = {}) => this.#page<AuditOut>("/v1/audit", o),
  }

  readonly buckets = {
    list: (o: ListOptions = {}) => this.#page<BucketOut>("/v1/buckets", o),
    get: (bucket: string) => this.request<BucketOut>("GET", `/v1/buckets/${enc(bucket)}`),
    create: (name: string, o: { idempotencyKey?: string } = {}) =>
      this.request<BucketOut>("POST", "/v1/buckets", { body: { name }, headers: idem(o.idempotencyKey) }),
    update: (bucket: string, o: { name?: string; publicRead?: boolean; corsOrigins?: string[] }) =>
      this.request<BucketOut>("PATCH", `/v1/buckets/${enc(bucket)}`, {
        body: { name: o.name, public_read: o.publicRead, cors_origins: o.corsOrigins },
      }),
    /** Deletes an empty bucket. */
    delete: (bucket: string) => this.request<void>("DELETE", `/v1/buckets/${enc(bucket)}`),
  }

  /** Object operations on one bucket. */
  bucket(name: string): Bucket {
    return new Bucket(this, name, this.#fetch, this.#opts.url, this.#opts.token, this.#opts.headers)
  }

  /** Imports a SQLite file (format "db") or SQL dump (format "sql") into a branch. */
  async import(db: string, branch: string, data: Uint8Array | ArrayBuffer | string, o: { format?: "db" | "sql"; mode?: "empty" | "replace" } = {}): Promise<BranchOut> {
    const format = o.format ?? (typeof data === "string" ? "sql" : "db")
    const res = await this.raw("POST", `${branchPath(db, branch)}/import`, {
      body: data as BodyInit,
      query: { format, mode: o.mode ?? "empty" },
      contentType: format === "sql" ? "application/sql" : "application/octet-stream",
    })
    return (await res.json()) as BranchOut
  }

  /** Downloads a branch as a SQLite file (format "db") or SQL dump ("sql"). */
  async download(db: string, branch: string, format: "db" | "sql" = "db"): Promise<Response> {
    return this.raw("GET", `${branchPath(db, branch)}/export`, { query: { format } })
  }

  async #page<T>(path: string, o: ListOptions): Promise<Page<T>> {
    const r = await this.request<{ data: T[]; next_cursor: string }>("GET", path, { query: { limit: o.limit, cursor: o.cursor } })
    return { data: r.data, nextCursor: r.next_cursor }
  }
}

/** What an upload stores besides the content. */
export interface PutOptions {
  contentType?: string
  cacheControl?: string
  contentDisposition?: string
  contentEncoding?: string
  contentLanguage?: string
  /** Custom metadata, sent as x-amz-meta-* headers. Names are stored lowercase. */
  metadata?: Record<string, string>
  /** Required when body is a ReadableStream. */
  contentLength?: number
  signal?: AbortSignal
}

export interface GetOptions {
  /** Bytes to read, both ends inclusive, or a raw Range header value. */
  range?: { start: number; end?: number } | { suffix: number } | string
  signal?: AbortSignal
}

/** A downloaded object: its record and its content. */
export interface StoredObject {
  key: string
  /** Bytes in this response (the range, when one was asked for). */
  size: number
  /** The whole object's size. */
  totalSize: number
  etag: string
  contentType: string
  lastModified: Date
  metadata: Record<string, string>
  /** The raw response, for streaming its body. */
  response: Response
  text(): Promise<string>
  bytes(): Promise<Uint8Array>
}

export interface ObjectPage {
  objects: ObjectOut[]
  /** Common prefixes under the delimiter, like folders. */
  prefixes: string[]
  nextCursor: string
}

function rangeHeader(r: GetOptions["range"]): string | undefined {
  if (r === undefined) return undefined
  if (typeof r === "string") return r
  if ("suffix" in r) return `bytes=-${r.suffix}`
  return `bytes=${r.start}-${r.end ?? ""}`
}

/** Objects in one bucket. Get one with `client.bucket(name)`. */
export class Bucket {
  readonly #client: Client
  readonly #fetch: typeof fetch
  readonly #url: string
  readonly #token: string
  readonly #headers?: Record<string, string>
  readonly name: string

  constructor(client: Client, name: string, f: typeof fetch, url: string, token: string, headers?: Record<string, string>) {
    this.#client = client
    this.name = name
    this.#fetch = f
    this.#url = url
    this.#token = token
    this.#headers = headers
  }

  #path(sub: string) {
    return `/v1/buckets/${enc(this.name)}/${sub}`
  }

  #objectURL(key: string) {
    const url = new URL(this.#url + this.#path("object"))
    url.searchParams.set("key", key)
    return url
  }

  /** Uploads an object, replacing any with the same key. */
  async put(key: string, body: string | Uint8Array | ArrayBuffer | Blob | ReadableStream<Uint8Array>, o: PutOptions = {}): Promise<ObjectOut> {
    const headers: Record<string, string> = { authorization: `Bearer ${this.#token}`, accept: "application/json", ...this.#headers }
    if (o.contentType) headers["content-type"] = o.contentType
    else if (typeof body === "string") headers["content-type"] = "text/plain; charset=utf-8"
    else if (typeof Blob !== "undefined" && body instanceof Blob && body.type) headers["content-type"] = body.type
    else headers["content-type"] = "application/octet-stream"
    if (o.cacheControl) headers["cache-control"] = o.cacheControl
    if (o.contentDisposition) headers["content-disposition"] = o.contentDisposition
    if (o.contentEncoding) headers["content-encoding"] = o.contentEncoding
    if (o.contentLanguage) headers["content-language"] = o.contentLanguage
    for (const [k, v] of Object.entries(o.metadata ?? {})) headers[`x-amz-meta-${k.toLowerCase()}`] = v
    const init: RequestInit & { duplex?: "half" } = { method: "PUT", headers, body: body as BodyInit, signal: o.signal }
    if (typeof ReadableStream !== "undefined" && body instanceof ReadableStream) {
      if (o.contentLength === undefined) throw new TypeError("bucket.put: a stream body needs contentLength")
      headers["content-length"] = String(o.contentLength)
      init.duplex = "half"
    }
    let res: Response
    try {
      res = await this.#fetch(this.#objectURL(key), init)
    } catch (err) {
      if (o.signal?.aborted) throw err
      throw new LumbungError("network_error", 0, `request to ${new URL(this.#url).origin} failed`, { cause: err })
    }
    if (!res.ok) throw await toError(res)
    return (await res.json()) as ObjectOut
  }

  /** Downloads an object, or a byte range of it. */
  async get(key: string, o: GetOptions = {}): Promise<StoredObject> {
    const headers: Record<string, string> = { authorization: `Bearer ${this.#token}`, ...this.#headers }
    const range = rangeHeader(o.range)
    if (range) headers.range = range
    let res: Response
    try {
      res = await this.#fetch(this.#objectURL(key), { headers, signal: o.signal })
    } catch (err) {
      if (o.signal?.aborted) throw err
      throw new LumbungError("network_error", 0, `request to ${new URL(this.#url).origin} failed`, { cause: err })
    }
    if (!res.ok) throw await toError(res)
    const size = Number(res.headers.get("content-length") ?? "0")
    const total = res.headers.get("content-range")?.split("/")[1]
    return {
      key,
      size,
      totalSize: total ? Number(total) : size,
      etag: (res.headers.get("etag") ?? "").replace(/"/g, ""),
      contentType: res.headers.get("content-type") ?? "",
      lastModified: new Date(res.headers.get("last-modified") ?? 0),
      metadata: metadataOf(res.headers),
      response: res,
      text: () => res.text(),
      bytes: async () => new Uint8Array(await res.arrayBuffer()),
    }
  }

  /** An object's record, or null when there's no object with that key (or no such bucket). */
  async head(key: string, o: { signal?: AbortSignal } = {}): Promise<ObjectOut | null> {
    const res = await this.#fetch(this.#objectURL(key), {
      method: "HEAD",
      headers: { authorization: `Bearer ${this.#token}`, ...this.#headers },
      signal: o.signal,
    })
    if (res.status === 404) return null
    // A HEAD response has no body, so the error carries only its status.
    if (!res.ok) throw await toError(res)
    const headers: Record<string, string> = {}
    for (const h of ["cache-control", "content-disposition", "content-encoding", "content-language", "expires"]) {
      const v = res.headers.get(h)
      if (v) headers[h] = v
    }
    return {
      key,
      size: Number(res.headers.get("content-length") ?? "0"),
      etag: (res.headers.get("etag") ?? "").replace(/"/g, ""),
      content_type: res.headers.get("content-type") ?? "",
      last_modified: new Date(res.headers.get("last-modified") ?? 0).toISOString(),
      headers,
      metadata: metadataOf(res.headers),
    }
  }

  /** Deletes an object. Deleting a key that doesn't exist succeeds. */
  delete(key: string): Promise<void> {
    return this.#client.request<void>("DELETE", this.#path("object"), { query: { key } })
  }

  /** Lists objects in key order. With a delimiter (usually "/"), deeper keys roll up into prefixes. */
  async list(o: { prefix?: string; delimiter?: string; limit?: number; cursor?: string } = {}): Promise<ObjectPage> {
    const r = await this.#client.request<{ data: ObjectOut[]; prefixes: string[]; next_cursor: string }>("GET", this.#path("objects"), {
      query: { prefix: o.prefix, delimiter: o.delimiter, limit: o.limit, cursor: o.cursor },
    })
    return { objects: r.data, prefixes: r.prefixes ?? [], nextCursor: r.next_cursor }
  }

  /** Copies an object into this bucket, from this bucket or another. */
  copy(o: { fromKey: string; key: string; fromBucket?: string; contentType?: string; metadata?: Record<string, string> }): Promise<ObjectOut> {
    return this.#client.request<ObjectOut>("POST", this.#path("copy"), {
      body: { from_bucket: o.fromBucket, from_key: o.fromKey, key: o.key, content_type: o.contentType, metadata: o.metadata },
    })
  }

  /**
   * A presigned S3 URL for one key that anyone can use until it expires,
   * signed with this client's token's access key.
   */
  async presign(key: string, o: { method?: "GET" | "PUT"; expiresIn?: number } = {}): Promise<{ url: string; method: string; expiresAt: Date }> {
    const r = await this.#client.request<{ url: string; method: string; expires_at: string }>("POST", this.#path("presign"), {
      body: { key, method: o.method ?? "GET", expires_in: o.expiresIn },
    })
    return { url: r.url, method: r.method, expiresAt: new Date(r.expires_at) }
  }
}

function metadataOf(h: Headers): Record<string, string> {
  const out: Record<string, string> = {}
  h.forEach((v, k) => {
    if (k.toLowerCase().startsWith("x-amz-meta-")) out[k.toLowerCase().slice("x-amz-meta-".length)] = v
  })
  return out
}

const enc = encodeURIComponent
const branchPath = (db: string, branch: string) => `/v1/databases/${enc(db)}/branches/${enc(branch)}`
const idem = (key?: string): Record<string, string> | undefined => (key ? { "idempotency-key": key } : undefined)

async function toError(res: Response): Promise<LumbungError> {
  const requestId = res.headers.get("x-request-id") ?? undefined
  let problem: Problem | undefined
  try {
    problem = (await res.json()) as Problem
  } catch {
    // not JSON
  }
  if (problem?.code) {
    return new LumbungError(problem.code, res.status, problem.title, { detail: problem.detail, requestId: problem.request_id ?? requestId, problem })
  }
  return new LumbungError("http_error", res.status, `HTTP ${res.status}`, { requestId })
}

/** Creates a client. */
export function createClient(opts: ClientOptions): Client {
  return new Client(opts)
}
