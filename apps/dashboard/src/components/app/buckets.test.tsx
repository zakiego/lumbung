import type { BucketOut, ObjectOut } from "@lumbung/client"
import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from "@tanstack/react-router"
import { render as rtlRender, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { type ReactNode, useState } from "react"
import { expect, test } from "vitest"
import {
  BucketTable,
  DEFAULT_OBJECT_SORT,
  dateGroup,
  defaultSortDir,
  groupObjects,
  groupSearch,
  matchesQuery,
  type ObjectSort,
  ObjectSortMenu,
  ObjectTable,
  PART_BYTES,
  nextObjectSort,
  prefixTrail,
  sortFolders,
  sortObjects,
  sortSearch,
  typeGroup,
  uploadFile,
} from "./buckets"

const noop = async () => {}

// Links need a router; render inside a one-route memory router.
async function render(ui: ReactNode) {
  const router = createRouter({ routeTree: createRootRoute({ component: () => <>{ui}</> }), history: createMemoryHistory({ initialEntries: ["/"] }) })
  const out = rtlRender(<RouterProvider router={router} />)
  await screen.findAllByRole("table").catch(() => screen.findByText(/No objects yet|This folder is empty|No matches/))
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
  expect(rows[0]).toContain(" /")
  expect(rows[1]).toContain("2026/")
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

test("uploadFile: small files in one request, large ones in parts, aborted on failure", async () => {
  const calls: { url: string; method: string; size?: number }[] = []
  const ok = (body: unknown) => new Response(JSON.stringify(body), { status: 200 })
  const fake = (failAt?: number) =>
    (async (u: RequestInfo | URL, init?: RequestInit) => {
      const url = String(u)
      const size = init?.body instanceof Blob ? init.body.size : undefined
      calls.push({ url, method: init?.method ?? "GET", size })
      if (url.includes("&uploads")) return ok({ uploadId: "up_aaaaaaaaaaaaaaaaaaaa" })
      if (url.includes("partNumber=")) {
        if (failAt && url.includes(`partNumber=${failAt}`)) return new Response(JSON.stringify({ error: "disk full" }), { status: 507 })
        return ok({ etag: "a".repeat(32) })
      }
      return ok({ key: "k", etag: "e" })
    }) as typeof fetch
  await uploadFile("b", "", new File(["hi"], "small.txt"), fake())
  expect(calls.map((c) => c.method)).toEqual(["PUT"])
  calls.length = 0
  const big = new File([new Uint8Array(PART_BYTES * 2 + 5)], "big.bin")
  await uploadFile("b", "dir/", big, fake())
  expect(calls.map((c) => c.method)).toEqual(["POST", "PUT", "PUT", "PUT", "POST"])
  expect(calls.slice(1, 4).map((c) => c.size)).toEqual([PART_BYTES, PART_BYTES, 5])
  expect(calls[0]!.url).toContain("key=dir%2Fbig.bin")
  calls.length = 0
  await expect(uploadFile("b", "", big, fake(2))).rejects.toThrow("disk full")
  expect(calls.at(-1)!.method).toBe("DELETE")
})

const sized = (key: string, size: number, type: string, modified: string): ObjectOut => ({ ...obj(key, size), content_type: type, last_modified: modified })
const files = [
  sized("IMG_10.png", 300, "image/png", "2026-11-12T00:00:00Z"),
  sized("IMG_9.jpeg", 6, "image/jpeg", "2026-11-14T00:00:00Z"),
  sized("b.txt", 300, "text/plain", "2026-11-13T00:00:00Z"),
]
const keys = (sort: ObjectSort) => sortObjects(files, sort).map((o) => o.key)

test("sort: natural names, numbers and dates, ties by name", () => {
  expect(keys(DEFAULT_OBJECT_SORT)).toEqual(["b.txt", "IMG_9.jpeg", "IMG_10.png"])
  expect(keys({ key: "name", dir: "desc" })).toEqual(["IMG_10.png", "IMG_9.jpeg", "b.txt"])
  expect(keys({ key: "size", dir: "desc" })).toEqual(["b.txt", "IMG_10.png", "IMG_9.jpeg"])
  expect(keys({ key: "size", dir: "asc" })).toEqual(["IMG_9.jpeg", "b.txt", "IMG_10.png"])
  expect(keys({ key: "modified", dir: "desc" })).toEqual(["IMG_9.jpeg", "b.txt", "IMG_10.png"])
  expect(keys({ key: "type", dir: "asc" })).toEqual(["IMG_9.jpeg", "IMG_10.png", "b.txt"])
  expect(sortFolders(["b/", "a10/", "a9/"], { key: "size", dir: "desc" })).toEqual(["a9/", "a10/", "b/"])
  expect(sortFolders(["b/", "a10/", "a9/"], { key: "name", dir: "desc" })).toEqual(["b/", "a10/", "a9/"])
})

test("sort: a new column starts where people look first, the same one flips", () => {
  expect(nextObjectSort(DEFAULT_OBJECT_SORT, "name")).toEqual({ key: "name", dir: "desc" })
  expect(nextObjectSort(DEFAULT_OBJECT_SORT, "size")).toEqual({ key: "size", dir: "desc" })
  expect(nextObjectSort(DEFAULT_OBJECT_SORT, "modified")).toEqual({ key: "modified", dir: "desc" })
  expect(nextObjectSort({ key: "size", dir: "desc" }, "type")).toEqual({ key: "type", dir: "asc" })
  expect(defaultSortDir("name")).toBe("asc")
  expect(defaultSortDir("size")).toBe("desc")
  expect(sortSearch(DEFAULT_OBJECT_SORT)).toEqual({})
  expect(sortSearch({ key: "size", dir: "desc" })).toEqual({ sort: "size", dir: "desc" })
})

// The route hands in a navigate call, which returns a Promise.
function Sortable() {
  const [sort, set] = useState<ObjectSort>(DEFAULT_OBJECT_SORT)
  const setSort = (s: ObjectSort) => {
    set(s)
    return new Promise<void>(() => {})
  }
  return (
    <>
      <ObjectSortMenu sort={sort} onSortChange={setSort} />
      <ObjectTable bucket="docs" prefix="" objects={files} prefixes={["z/"]} canWrite onDelete={noop} sort={sort} onSortChange={setSort} />
    </>
  )
}
const order = () => [...document.querySelectorAll("tbody tr")].map((r) => r.querySelector("td .truncate")!.textContent)

test("object table: headers sort, folders stay on top, the menu agrees", async () => {
  const user = userEvent.setup()
  await render(<Sortable />)
  expect(screen.getByRole("columnheader", { name: "Name" }).getAttribute("aria-sort")).toBe("ascending")
  await user.click(screen.getByRole("button", { name: "Size" }))
  expect(order()).toEqual(["z/", "b.txt", "IMG_10.png", "IMG_9.jpeg"])
  const size = screen.getByRole("columnheader", { name: "Size" })
  expect(size.getAttribute("aria-sort")).toBe("descending")
  expect(screen.getByRole("columnheader", { name: "Name" }).getAttribute("aria-sort")).toBeNull()
  // A slow navigation never leaves the header busy, so a second press lands.
  expect(screen.getByRole("button", { name: "Size" }).getAttribute("aria-busy")).toBeNull()
  await user.click(screen.getByRole("button", { name: "Size" }))
  expect(size.getAttribute("aria-sort")).toBe("ascending")
  await user.click(screen.getByRole("button", { name: "Sort by Size, ascending" }))
  await user.click(await screen.findByRole("menuitemradio", { name: "Modified" }))
  expect(order()).toEqual(["z/", "IMG_9.jpeg", "b.txt", "IMG_10.png"])
  expect(screen.getByRole("button", { name: "Sort by Modified, descending" })).toBeTruthy()
})

test("object table: without onSortChange the headers are plain text", async () => {
  await render(<ObjectTable bucket="docs" prefix="" objects={files} prefixes={[]} canWrite onDelete={noop} />)
  expect(screen.queryByRole("button", { name: "Size" })).toBeNull()
  expect(order()).toEqual(["b.txt", "IMG_9.jpeg", "IMG_10.png"])
})

test("search: a name match ignores case and surrounding spaces", () => {
  expect(matchesQuery("IMG_10.png", "img")).toBe(true)
  expect(matchesQuery("IMG_10.png", "  10.P ")).toBe(true)
  expect(matchesQuery("b.txt", "img")).toBe(false)
  expect(matchesQuery("b.txt", "   ")).toBe(true)
})

test("group: types fall into families, dates into calendar days", () => {
  expect(typeGroup("image/png")).toBe("Images")
  expect(typeGroup("Video/MP4")).toBe("Videos")
  expect(typeGroup("application/pdf")).toBe("Documents")
  expect(typeGroup("text/plain; charset=utf-8")).toBe("Documents")
  expect(typeGroup("application/zip")).toBe("Archives")
  expect(typeGroup("application/octet-stream")).toBe("Other")
  const now = new Date(2026, 10, 14, 9, 0)
  expect(dateGroup(new Date(2026, 10, 14, 0, 30).toISOString(), now)).toBe("Today")
  expect(dateGroup(new Date(2026, 10, 13, 23, 0).toISOString(), now)).toBe("Yesterday")
  expect(dateGroup(new Date(2026, 10, 9).toISOString(), now)).toBe("Previous 7 days")
  expect(dateGroup(new Date(2026, 9, 20).toISOString(), now)).toBe("Previous 30 days")
  expect(dateGroup(new Date(2025, 0, 1).toISOString(), now)).toBe("Older")
  expect(groupSearch("none")).toEqual({})
  expect(groupSearch("type")).toEqual({ group: "type" })
})

test("group: sections keep the sort inside, drop empty ones, flip dates for oldest first", () => {
  const now = new Date("2026-11-14T12:00:00Z")
  const byType = groupObjects(files, "type", DEFAULT_OBJECT_SORT, now)
  expect(byType.map((g) => [g.label, g.objects.map((o) => o.key)])).toEqual([
    ["Images", ["IMG_9.jpeg", "IMG_10.png"]],
    ["Documents", ["b.txt"]],
  ])
  const newest = groupObjects(files, "modified", { key: "modified", dir: "desc" }, now).map((g) => g.objects[0]!.key)
  const oldest = groupObjects(files, "modified", { key: "modified", dir: "asc" }, now).map((g) => g.objects[0]!.key)
  expect(oldest).toEqual([...newest].reverse())
  expect(groupObjects(files, "none", DEFAULT_OBJECT_SORT)).toHaveLength(1)
})

test("object table: search filters folders and files, a group adds labeled sections", async () => {
  const { unmount } = await render(<ObjectTable bucket="docs" prefix="" objects={files} prefixes={["img-raw/", "z/"]} canWrite onDelete={noop} query="img" />)
  expect(order()).toEqual(["img-raw/", "IMG_9.jpeg", "IMG_10.png"])
  unmount()
  await render(<ObjectTable bucket="docs" prefix="" objects={files} prefixes={["z/"]} canWrite onDelete={noop} group="type" />)
  expect(screen.getByRole("rowheader", { name: /Folders/ })).toBeTruthy()
  expect(screen.getByRole("rowheader", { name: /Images/ })).toBeTruthy()
  expect(screen.getByRole("rowheader", { name: /Documents/ })).toBeTruthy()
})

test("object table: a search with no hits says so and keeps the footer", async () => {
  await render(<ObjectTable bucket="docs" prefix="" objects={files} prefixes={[]} canWrite onDelete={noop} query="nope" footer={<button type="button">Load more</button>} />)
  expect(screen.getByText("No matches")).toBeTruthy()
  expect(screen.getByRole("button", { name: "Load more" })).toBeTruthy()
})
