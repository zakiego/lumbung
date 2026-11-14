import type { BucketOut, ObjectOut } from "@lumbung/client"
import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from "@tanstack/react-router"
import { fireEvent, render as rtlRender, screen, waitFor, within } from "@testing-library/react"
import { createAgent } from "@ui-sdk/react/agent"
import type { ReactNode } from "react"
import { expect, test, vi } from "vitest"
import { BucketTable, ObjectTable, PART_BYTES, objectURL, prefixTrail, uploadFile } from "./buckets"
import { publicObjectURL } from "@/lib/objects"

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
  expect(within(sheet).getByRole("link", { name: "Download" }).getAttribute("href")).toBe(objectURL("docs", photo.key))
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
