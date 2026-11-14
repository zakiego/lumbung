// Worst-case data for the lists that render user data (break-ui). Lengths are
// the server limits: branch names 63, token names 100, labels 200, emails 200.
import type { AuditOut, ScheduleOut, SnapshotOut, TokenOut } from "@lumbung/client"
import type { Invite, Member } from "@/server/users"

export const LONG_BRANCH = "feature-add-customer-loyalty-points-and-tier-upgrades-2026-q4-x"
export const LONG_EMAIL = "bartholomew.fitzgerald@northwind-industries-holdings.example.com"
export const LONG_TOKEN = "github-actions-deploy-production-eu-west-1-read-write-rotated-quarterly-by-platform-team-2026-11-14"
export const LONG_LABEL =
  "Before the loyalty points migration: backfill tiers for 1,284 customers, drop the legacy points_ledger table and rebuild the customer_email index with COLLATE NOCASE so lookups stop scanning"

const at = "2026-11-14T08:00:00Z"

export function tokens(n: number): TokenOut[] {
  const base: TokenOut[] = [
    { id: "svc", name: "dashboard", scope: "instance", access: "admin", database_id: "", branch_id: "", created_at: at, expires_at: null, last_used_at: at, service: true },
    { id: "t1", name: LONG_TOKEN, scope: "database", access: "write", database_id: "db1", branch_id: "b1", created_at: at, expires_at: "2027-02-12T00:00:00Z", last_used_at: null, service: false },
    { id: "t2", name: "x", scope: "database", access: "read", database_id: "gone", branch_id: "", created_at: at, expires_at: null, last_used_at: null, service: false },
  ]
  return Array.from({ length: n }, (_, i) => ({ ...base[i % base.length]!, id: `t${i}` }))
}

export const tokenNames = { databases: { db1: "customer-loyalty-and-rewards-production-eu-west-1-primary-store" }, branches: { b1: LONG_BRANCH } }

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
    { id: 1, actor: `dashboard:${LONG_EMAIL}`, token_id: "", action: "branch.promote", target: `db_01JCX9/br_01JCXA/${LONG_BRANCH}`, result: "ok", code: "", at, request_id: "r1" },
    { id: 2, actor: "", token_id: "tok_01JCX9ZK4M2", action: "snapshot.restore", target: "db_01JCX9/snap_01JCXB", result: "error", code: "branch_name_taken", at, request_id: "r2" },
    { id: 3, actor: "", token_id: "", action: "x", target: "", result: "ok", code: "", at, request_id: "r3" },
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
