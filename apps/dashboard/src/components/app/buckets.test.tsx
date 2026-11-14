import type { BucketOut, ObjectOut } from "@lumbung/client"
import { fireEvent, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { type ReactNode, useState } from "react"
import { expect, test, vi } from "vitest"
import { publicObjectURL } from "@/lib/objects"
import type { WorkspaceInfo } from "@/lib/workspace"
import { renderWithRouter } from "@/test/router"
import {
  BucketTable,
  DEFAULT_OBJECT_SORT,
  defaultSortDir,
  type ObjectSort,
  ObjectSortMenu,
  ObjectTable,
  PART_BYTES,
  nextObjectSort,
  objectURL,
  prefixTrail,
  sortFolders,
  sortObjects,
  sortSearch,
  uploadFile,
} from "./buckets"

const noop = async () => {}

const AGENTS: WorkspaceInfo = { id: "ws_bbbbbbbbbbbbbbbbbbbb", name: "agents", default: false, role: "member" }

// Links need a router and the workspace on screen.
async function render(ui: ReactNode, workspace?: WorkspaceInfo) {
  const out = await renderWithRouter(ui, workspace)
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
    workspace_id: "ws1",
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
  expect(screen.getByRole("link", { name: long }).getAttribute("href")).toBe(`/w/default/buckets/${long}`)
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
  expect(link.getAttribute("href")).toBe("/app/api/objects?ws=default&bucket=docs&key=reports%2F%C3%BCn%C3%AF")
  const folder = screen.getByRole("link", { name: "2026/" }) as HTMLAnchorElement
  expect(folder.getAttribute("href")).toBe("/w/default/buckets/docs?prefix=reports%2F2026%2F")
})

test("object table: downloads, folders and settings name the workspace on screen", async () => {
  const access = { publicRead: false, s3Endpoint: "https://h/v1/s3", canAdmin: true }
  await render(<ObjectTable bucket="docs" prefix="" objects={[obj("a.pdf")]} prefixes={["2026/"]} canWrite onDelete={noop} access={access} />, AGENTS)
  expect(screen.getByRole("link", { name: "Download a.pdf" }).getAttribute("href")).toBe("/app/api/objects?ws=agents&bucket=docs&key=a.pdf")
  expect(screen.getByRole("link", { name: "2026/" }).getAttribute("href")).toBe("/w/agents/buckets/docs?prefix=2026%2F")
  fireEvent.click(screen.getByRole("button", { name: "Details of a.pdf" }))
  const sheet = await screen.findByRole("dialog", { name: "a.pdf" })
  expect(within(sheet).getByRole("link", { name: "Download" }).getAttribute("href")).toBe("/app/api/objects?ws=agents&bucket=docs&key=a.pdf")
  expect(within(sheet).getByRole("link", { name: "Bucket settings" }).getAttribute("href")).toBe("/w/agents/buckets/docs/settings")
})

test("object URL: the workspace, the bucket and the key, each encoded", () => {
  expect(objectURL("agents", "docs", "a b/c&d=e?.txt")).toBe("/app/api/objects?ws=agents&bucket=docs&key=a+b%2Fc%26d%3De%3F.txt")
  const q = new URL(objectURL("ws_bbbbbbbbbbbbbbbbbbbb", "docs", "x&ws=default"), "http://h").searchParams
  expect(q.getAll("ws")).toEqual(["ws_bbbbbbbbbbbbbbbbbbbb"])
  expect(q.get("key")).toBe("x&ws=default")
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
  const inWorkspace = () => calls.map((c) => new URL(c.url, "http://h").searchParams.get("ws"))
  await uploadFile("agents", "b", "", new File(["hi"], "small.txt"), fake())
  expect(calls.map((c) => c.method)).toEqual(["PUT"])
  expect(calls[0]!.url).toBe("/app/api/objects?ws=agents&bucket=b&key=small.txt")
  calls.length = 0
  const big = new File([new Uint8Array(PART_BYTES * 2 + 5)], "big.bin")
  await uploadFile("agents", "b", "dir/", big, fake())
  expect(calls.map((c) => c.method)).toEqual(["POST", "PUT", "PUT", "PUT", "POST"])
  expect(calls.slice(1, 4).map((c) => c.size)).toEqual([PART_BYTES, PART_BYTES, 5])
  expect(calls[0]!.url).toContain("key=dir%2Fbig.bin")
  // Every request of the upload names the workspace, the abort too.
  expect(inWorkspace()).toEqual(["agents", "agents", "agents", "agents", "agents"])
  calls.length = 0
  await expect(uploadFile("agents", "b", "", big, fake(2))).rejects.toThrow("disk full")
  expect(calls.at(-1)!.method).toBe("DELETE")
  expect(inWorkspace().every((ws) => ws === "agents")).toBe(true)
})

test("object details: a row opens its details, with links only on a public bucket", async () => {
  const photo: ObjectOut = {
    key: "trips/🍚 rice.png",
    size: 2048,
    etag: "6f53033011e5582c3b06a3969cc0675a",
    content_type: "image/png",
    last_modified: at,
    headers: { "cache-control": "max-age=3600" },
    metadata: { camera: "x".repeat(300) },
  }
  const access = { publicRead: true, s3Endpoint: "https://db.example.com/v1/s3", canAdmin: true }
  const { unmount } = await render(<ObjectTable bucket="docs" prefix="trips/" objects={[photo]} prefixes={[]} canWrite onDelete={noop} access={access} />)
  fireEvent.click(screen.getByRole("button", { name: "Details of trips/🍚 rice.png" }))
  const sheet = await screen.findByRole("dialog", { name: "🍚 rice.png" })
  expect(within(sheet).getByText("https://db.example.com/v1/s3/docs/trips/%F0%9F%8D%9A%20rice.png")).toBeTruthy()
  expect(within(sheet).getByText("max-age=3600")).toBeTruthy()
  expect(within(sheet).getByText("x".repeat(300))).toBeTruthy()
  expect(within(sheet).getByRole("link", { name: "Download" }).getAttribute("href")).toBe(objectURL("default", "docs", photo.key))
  expect(within(sheet).getByRole("button", { name: "Delete" })).toBeTruthy()
  unmount()

  await render(<ObjectTable bucket="docs" prefix="" objects={[obj("a.pdf")]} prefixes={[]} canWrite={false} onDelete={noop} access={{ ...access, publicRead: false }} />)
  fireEvent.click(screen.getByRole("button", { name: "Details of a.pdf" }))
  const priv = await screen.findByRole("dialog", { name: "a.pdf" })
  expect(within(priv).queryByRole("button", { name: "Copy public link" })).toBeNull()
  expect(within(priv).getByRole("link", { name: "Bucket settings" })).toBeTruthy()
  expect(within(priv).queryByRole("button", { name: "Delete" })).toBeNull()
})

test("object details: no settings link for non-admins, dot keys aren't linked", async () => {
  const access = { publicRead: false, s3Endpoint: "https://h/v1/s3", canAdmin: false }
  const { unmount } = await render(<ObjectTable bucket="docs" prefix="" objects={[obj("a.pdf")]} prefixes={[]} canWrite={false} onDelete={noop} access={access} />)
  fireEvent.click(screen.getByRole("button", { name: "Details of a.pdf" }))
  const priv = await screen.findByRole("dialog", { name: "a.pdf" })
  expect(within(priv).queryByRole("link", { name: "Bucket settings" })).toBeNull()
  unmount()
  await render(<ObjectTable bucket="docs" prefix="" objects={[obj("x/../y")]} prefixes={[]} canWrite={false} onDelete={noop} access={{ ...access, publicRead: true }} />)
  fireEvent.click(screen.getByRole("button", { name: "Details of x/../y" }))
  const dotted = await screen.findByRole("dialog", { name: "x/../y" })
  expect(within(dotted).getByText("This key can't be linked.")).toBeTruthy()
})

test("public object URL: segments encoded, dot segments refused", () => {
  expect(publicObjectURL("https://h/v1/s3", "b", "a b/c#d?.txt")).toBe("https://h/v1/s3/b/a%20b/c%23d%3F.txt")
  expect(publicObjectURL("https://h/v1/s3", "b", "a/../c")).toBeNull()
})

test("an agent opens an object's details, and deleting it needs a person's approval", async () => {
  let approve = false
  const agent = createAgent({ allow: (step) => !step.confirm || approve })
  const onDelete = vi.fn(async () => {})
  await render(<ObjectTable bucket="docs" prefix="" objects={[obj("a.pdf")]} prefixes={[]} canWrite onDelete={onDelete} access={{ publicRead: false, s3Endpoint: "https://h/v1/s3", canAdmin: false }} />)
  const opened = await agent.act({ steps: [{ ref: "Details of a.pdf", action: "press" }] })
  expect(opened.ok, opened.error?.message).toBe(true)
  const sheet = await screen.findByRole("dialog", { name: "a.pdf" })
  for (const e of agent.snapshot().filter((e) => !e.offPage && e.component !== "more")) expect(e.label, `${e.component} ${e.ref} has no name`).toBeTruthy()
  fireEvent.click(within(sheet).getByRole("button", { name: "Delete" }))
  const confirm = [{ ref: "Delete a.pdf?", action: "press", args: ["Delete object"] }]
  expect((await agent.act({ steps: confirm })).ok).toBe(false)
  expect(onDelete).not.toHaveBeenCalled()
  approve = true
  const done = await agent.act({ steps: confirm })
  expect(done.ok, done.error?.message).toBe(true)
  await waitFor(() => expect(onDelete).toHaveBeenCalledWith("a.pdf"))
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
