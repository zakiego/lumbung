import type { Point, StatsOutBody } from "@lumbung/client"
import { z } from "zod"

export const WINDOWS = ["1h", "6h", "24h", "48h"] as const
export type StatsWindow = (typeof WINDOWS)[number]
export const DEFAULT_WINDOW: StatsWindow = "24h"

export const WINDOW_LABEL: Record<StatsWindow, string> = {
  "1h": "Last hour",
  "6h": "Last 6 hours",
  "24h": "Last 24 hours",
  "48h": "Last 48 hours",
}

// A bad ?window= falls back to the default instead of failing the page.
export const windowSearch = z.object({ window: z.enum(WINDOWS).optional().catch(undefined) })

export type DbUsage = { queries: number; errors: number }

export type WorkspaceStats = {
  queries: number
  errors: number
  rows_read: number
  rows_written: number
  series: Point[]
  byDb: Record<string, DbUsage>
  failed: string[]
}

// Every branch in one window shares the same buckets, so points add up by time.
export function mergeStats(results: { db: string; stats: StatsOutBody | null }[]): WorkspaceStats {
  const out: WorkspaceStats = { queries: 0, errors: 0, rows_read: 0, rows_written: 0, series: [], byDb: {}, failed: [] }
  const buckets = new Map<string, Point>()
  for (const { db, stats } of results) {
    if (!stats) {
      out.failed.push(db)
      continue
    }
    out.queries += stats.queries
    out.errors += stats.errors
    out.rows_read += stats.rows_read
    out.rows_written += stats.rows_written
    out.byDb[db] = { queries: stats.queries, errors: stats.errors }
    for (const p of stats.series) {
      const b = buckets.get(p.at)
      if (b) {
        b.queries += p.queries
        b.errors += p.errors
        b.rows_read += p.rows_read
        b.rows_written += p.rows_written
      } else {
        buckets.set(p.at, { ...p })
      }
    }
  }
  out.series = [...buckets.values()].sort((a, b) => (a.at < b.at ? -1 : a.at > b.at ? 1 : 0))
  return out
}

export function errorRate(queries: number, errors: number): string {
  if (queries === 0) return "0%"
  const pct = (errors / queries) * 100
  if (pct === 0) return "0%"
  if (pct < 0.1) return "<0.1%"
  return `${pct < 10 ? pct.toFixed(1) : Math.round(pct)}%`
}
