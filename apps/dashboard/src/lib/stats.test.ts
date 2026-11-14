import type { StatsOutBody } from "@lumbung/client"
import { expect, test } from "vitest"
import { errorRate, mergeStats } from "./stats"

function stats(points: [string, number, number][]): StatsOutBody {
  const series = points.map(([at, queries, errors]) => ({ at, queries, errors, rows_read: queries * 2, rows_written: 1 }))
  return {
    queries: series.reduce((n, p) => n + p.queries, 0),
    errors: series.reduce((n, p) => n + p.errors, 0),
    rows_read: 0,
    rows_written: 0,
    p50_ms: 1,
    p95_ms: 1,
    p99_ms: 1,
    series,
    since: "2026-11-14T00:00:00Z",
    size_bytes: 0,
    slow_queries: [],
    step: "1m0s",
  }
}

test("adds up series by bucket, in time order, and lists databases that failed", () => {
  const out = mergeStats([
    { db: "shop", stats: stats([["2026-11-14T00:02:00Z", 5, 1], ["2026-11-14T00:01:00Z", 3, 0]]) },
    { db: "auth", stats: stats([["2026-11-14T00:01:00Z", 2, 2]]) },
    { db: "gone", stats: null },
  ])
  expect(out.queries).toBe(10)
  expect(out.errors).toBe(3)
  expect(out.series.map((p) => [p.at, p.queries, p.errors])).toEqual([
    ["2026-11-14T00:01:00Z", 5, 2],
    ["2026-11-14T00:02:00Z", 5, 1],
  ])
  expect(out.byDb).toEqual({ shop: { queries: 8, errors: 1 }, auth: { queries: 2, errors: 2 } })
  expect(out.failed).toEqual(["gone"])
})

test("merging leaves the inputs alone", () => {
  const a = stats([["2026-11-14T00:01:00Z", 1, 0]])
  mergeStats([{ db: "a", stats: a }, { db: "b", stats: stats([["2026-11-14T00:01:00Z", 1, 0]]) }])
  expect(a.series[0]!.queries).toBe(1)
})

test("error rate stays readable at the extremes", () => {
  expect(errorRate(0, 0)).toBe("0%")
  expect(errorRate(1_284_000, 1)).toBe("<0.1%")
  expect(errorRate(145, 1)).toBe("0.7%")
  expect(errorRate(10, 5)).toBe("50%")
  expect(errorRate(3, 3)).toBe("100%")
})
