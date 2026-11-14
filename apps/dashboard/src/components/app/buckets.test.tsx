import type { BucketOut, ObjectOut } from "@lumbung/client"
import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from "@tanstack/react-router"
import { render as rtlRender, screen } from "@testing-library/react"
import type { ReactNode } from "react"
import { expect, test } from "vitest"
import { BucketTable, ObjectTable, prefixTrail } from "./buckets"

const noop = async () => {}

// Links need a router; render inside a one-route memory router.
async function render(ui: ReactNode) {
  const router = createRouter({ routeTree: createRootRoute({ component: () => <>{ui}</> }), history: createMemoryHistory({ initialEntries: ["/"] }) })
  const out = rtlRender(<RouterProvider router={router} />)
  await screen.findAllByRole("table").catch(() => screen.findByText(/No objects yet|This folder is empty/))
  return out
}
const at = "2026-11-14T08:00:00Z"
const LONG_KEY = `reports/${"quarterly-revenue-breakdown-by-region-and-product-line-".repeat(12)}final-v2.pdf`

const obj = (key: string, size = 10): ObjectOut => ({ key, size, etag: "x", content_type: "application/pdf", last_modified: at, headers: {}, metadata: {} })

// Truncated text stays reachable: a title on plain text, a tooltip on links.
function readable(text: string) {
  const el = screen.getAllByText(text)[0]!
  const titled = el.closest("[title]")?.getAttribute("title")
  const described = el.closest("a")?.getAttribute("aria-describedby")
  expect(titled === text || !!described).toBe(true)
}

test("bucket table: long names, public badge, many rows", async () => {
  const b = (name: string, i: number): BucketOut => ({
    id: `bk_${i}`,
    name,
    public_read: i % 2 === 0,
    cors_origins: [],
    size_bytes: 10 ** 12 + i,
    object_count: 1234567,
    created_at: at,
    updated_at: at,
  })
  const long = "a" + "-very-long-bucket-name".repeat(2) + "-x"
  await render(<BucketTable buckets={Array.from({ length: 300 }, (_, i) => b(i === 0 ? long : `bucket-${i}`, i))} />)
  readable(long)
  expect(screen.getAllByText("Public read").length).toBe(150)
  expect(screen.getAllByText("1,234,567").length).toBe(300)
})

test("object table: folders first, names relative to the prefix, long and odd keys", async () => {
  const keys = ["reports/a.pdf", LONG_KEY, "reports/🍚 rice\tharvest.txt", "reports/../escape", "reports/ünï"]
  await render(<ObjectTable bucket="docs" prefix="reports/" objects={keys.map((k) => obj(k))} prefixes={["reports/2026/", "reports/ /"]} canWrite onDelete={noop} />)
  const rows = [...document.querySelectorAll("tbody tr")].map((r) => r.textContent)
  expect(rows[0]).toContain("2026/")
  expect(rows[1]).toContain(" /")
  readable(LONG_KEY.slice("reports/".length))
  expect(screen.getByText("../escape")).toBeTruthy()
  expect(screen.getAllByRole("button", { name: /^Delete reports\// })).toHaveLength(5)
  const link = screen.getByRole("link", { name: "Download reports/ünï" }) as HTMLAnchorElement
  expect(link.getAttribute("href")).toBe("/app/api/objects?bucket=docs&key=reports%2F%C3%BCn%C3%AF")
})

test("object table: read-only sees no delete, empty states, 1000 rows", async () => {
  const { unmount } = await render(<ObjectTable bucket="docs" prefix="" objects={[obj("a")]} prefixes={[]} canWrite={false} onDelete={noop} />)
  expect(screen.queryByRole("button", { name: /^Delete/ })).toBeNull()
  expect(screen.getByRole("link", { name: "Download a" })).toBeTruthy()
  unmount()
  const empty = await render(<ObjectTable bucket="docs" prefix="" objects={[]} prefixes={[]} canWrite onDelete={noop} />)
  expect(screen.getByText("No objects yet")).toBeTruthy()
  empty.unmount()
  const folder = await render(<ObjectTable bucket="docs" prefix="a/" objects={[]} prefixes={[]} canWrite onDelete={noop} />)
  expect(screen.getByText("This folder is empty")).toBeTruthy()
  folder.unmount()
  const many = await render(<ObjectTable bucket="docs" prefix="" objects={Array.from({ length: 1000 }, (_, i) => obj(`k${i}`, i))} prefixes={[]} canWrite onDelete={noop} />)
  expect(many.container.querySelectorAll("tbody tr")).toHaveLength(1000)
})

test("object table: a folder marker key isn't listed", async () => {
  await render(<ObjectTable bucket="docs" prefix="reports/" objects={[obj("reports/"), obj("reports/a.pdf")]} prefixes={[]} canWrite onDelete={noop} />)
  expect(document.querySelectorAll("tbody tr")).toHaveLength(1)
})

test("breadcrumb trail", () => {
  expect(prefixTrail("")).toEqual([])
  expect(prefixTrail("a/b/")).toEqual([
    { label: "a", prefix: "a/" },
    { label: "b", prefix: "a/b/" },
  ])
  expect(prefixTrail("/x/")).toEqual([
    { label: "/", prefix: "/" },
    { label: "x", prefix: "/x/" },
  ])
  expect(prefixTrail("a/partial")).toEqual([{ label: "a", prefix: "a/" }])
})
