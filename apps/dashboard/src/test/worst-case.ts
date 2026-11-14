// Worst-case data for the lists that render user data (break-ui). Lengths are
// the server limits: branch names 63, token names 100, labels 200, emails 200.
import type { AuditOut, DatabaseOut, ScheduleOut, SnapshotOut, TokenOut } from "@lumbung/client"
import type { DbUsage } from "@/lib/stats"
import type { Invite, Member } from "@/server/users"

export const LONG_BRANCH = "feature-add-customer-loyalty-points-and-tier-upgrades-2026-q4-x"
export const LONG_EMAIL = "bartholomew.fitzgerald@northwind-industries-holdings.example.com"
export const LONG_TOKEN = "github-actions-deploy-production-eu-west-1-read-write-rotated-quarterly-by-platform-team-2026-11-14"
export const LONG_LABEL =
  "Before the loyalty points migration: backfill tiers for 1,284 customers, drop the legacy points_ledger table and rebuild the customer_email index with COLLATE NOCASE so lookups stop scanning"

const at = "2026-11-14T08:00:00Z"

const grant = (g: Partial<TokenOut["grants"][number]>): TokenOut["grants"][number] => ({ database_id: "", branch_id: "", bucket_id: "", access: "read", ...g })

// 20 grants is the server limit: long names, a branch, a deleted database and bucket, and every level.
export const MANY_GRANTS: TokenOut["grants"] = [
  grant({ database_id: "db1", branch_id: "b1", access: "write" }),
  grant({ bucket_id: "bk1", access: "admin" }),
  grant({ database_id: "gone" }),
  grant({ bucket_id: "gone-bucket", access: "write" }),
  ...Array.from({ length: 16 }, (_, i) => grant({ database_id: `db-extra-${i}` })),
]

export function tokens(n: number): TokenOut[] {
  const base: TokenOut[] = [
    { id: "svc", name: "dashboard", scope: "instance", workspace_id: "", access: "admin", database_id: "", branch_id: "", bucket_id: "", grants: [], access_key_id: "", created_at: at, expires_at: null, last_used_at: at, service: true },
    { id: "t1", name: LONG_TOKEN, scope: "database", workspace_id: "ws1", access: "write", database_id: "db1", branch_id: "b1", bucket_id: "", grants: [grant({ database_id: "db1", branch_id: "b1", access: "write" })], access_key_id: "", created_at: at, expires_at: "2027-02-12T00:00:00Z", last_used_at: null, service: false },
    { id: "t2", name: "x", scope: "database", workspace_id: "ws1", access: "read", database_id: "gone", branch_id: "", bucket_id: "", grants: [grant({ database_id: "gone" })], access_key_id: "", created_at: at, expires_at: null, last_used_at: null, service: false },
    { id: "t3", name: LONG_TOKEN, scope: "grants", workspace_id: "ws1", access: "admin", database_id: "", branch_id: "", bucket_id: "", grants: MANY_GRANTS, access_key_id: "LMBABC", created_at: at, expires_at: null, last_used_at: null, service: false },
  ]
  return Array.from({ length: n }, (_, i) => ({ ...base[i % base.length]!, id: `t${i}` }))
}

export const LONG_BUCKET = "customer-uploads-and-generated-invoices-production-eu-west-1-01"

export const tokenNames = {
  databases: { db1: "customer-loyalty-and-rewards-production-eu-west-1-primary-store" },
  branches: { b1: LONG_BRANCH },
  buckets: { bk1: LONG_BUCKET },
}

export function members(n: number): Member[] {
  const base: Member[] = [
    { id: "me", name: "Aleksandra Wiśniewska-Kowalczyk", email: "aleksandra@example.com", role: "owner", createdAt: at },
    { id: "m1", name: "", email: LONG_EMAIL, role: "member", createdAt: at },
    { id: "m2", name: "Jo", email: "jo@x.io", role: "readonly", createdAt: at },
  ]
  return Array.from({ length: n }, (_, i) => ({ ...base[i % base.length]!, id: i === 0 ? "me" : `m${i}` }))
}

export const invites: Invite[] = [{ id: "i1", email: LONG_EMAIL, role: "member", createdAt: at, expiresAt: "2026-11-21T08:00:00Z" }]

export function audit(n: number): AuditOut[] {
  const base: AuditOut[] = [
    { id: 1, workspace_id: "ws1", actor: `dashboard:${LONG_EMAIL}`, token_id: "", action: "branch.promote", target: `db_01JCX9/br_01JCXA/${LONG_BRANCH}`, result: "ok", code: "", at, request_id: "req-1" },
    { id: 2, workspace_id: "ws1", actor: "", token_id: "tok_01JCX9ZK4M2", action: "snapshot.restore", target: "db_01JCX9/snap_01JCXB", result: "error", code: "branch_name_taken", at, request_id: "req-2" },
    { id: 3, workspace_id: "ws1", actor: "", token_id: "", action: "x", target: "", result: "ok", code: "", at, request_id: "req-3" },
  ]
  return Array.from({ length: n }, (_, i) => ({ ...base[i % base.length]!, id: i }))
}

export function snapshots(n: number): SnapshotOut[] {
  const base: SnapshotOut[] = [
    { id: "s1", label: LONG_LABEL, kind: "manual", size_bytes: 5_368_709_120, created_at: at, branch_id: "b1", branch_name: LONG_BRANCH, database_id: "db1" },
    { id: "s2", label: "", kind: "scheduled", size_bytes: 0, created_at: at, branch_id: "b1", branch_name: "main", database_id: "db1" },
  ]
  return Array.from({ length: n }, (_, i) => ({ ...base[i % base.length]!, id: `s${i}` }))
}

export const schedule: ScheduleOut = { every_minutes: 1440, keep_count: 1, keep_days: 1, last_run_at: null }

export const LONG_DB = "customer-loyalty-and-rewards-production-eu-west-1-primary-store"

export function databases(n: number): DatabaseOut[] {
  const base: DatabaseOut[] = [
    { id: "db1", workspace_id: "ws1", name: LONG_DB, default_branch: "main", default_branch_id: "b1", deleted_at: null, purge_after: null, branch_count: 63, size_bytes: 5_368_709_120, created_at: at, updated_at: at, last_activity_at: at },
    { id: "db2", workspace_id: "ws1", name: "x", default_branch: "main", default_branch_id: "b2", deleted_at: null, purge_after: null, branch_count: 1, size_bytes: 0, created_at: at, updated_at: at, last_activity_at: null },
    { id: "db3", workspace_id: "ws1", name: "shop", default_branch: "main", default_branch_id: "b3", deleted_at: null, purge_after: null, branch_count: 2, size_bytes: 300_000, created_at: at, updated_at: at, last_activity_at: at },
  ]
  return Array.from({ length: n }, (_, i) => ({ ...base[i % base.length]!, id: `db${i + 1}`, name: i < base.length ? base[i]!.name : `db-${i}` }))
}

// db1 is busy and failing, db2 had no traffic, db3 didn't report.
export const usage: Record<string, DbUsage> = { [LONG_DB]: { queries: 1_284_000, errors: 1 }, x: { queries: 0, errors: 0 } }
