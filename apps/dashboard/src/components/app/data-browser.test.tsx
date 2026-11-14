import { act, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"
import type { BrowseResult, FullCell } from "@/fns/data"
import { type Cell, encodeKey } from "@/lib/cells"
import type { GridColumn } from "@/server/grid"
import { type BrowseQuery, type DataActions, DataBrowser } from "./data-browser"
import { Toaster } from "@/components/ui/toast"
import { bigText, worstTableName } from "./grid-fixtures"

let approve = false
const agent = createAgent({ allow: (step) => !step.confirm || approve })

const col = (name: string, type: string, extra: Partial<GridColumn> = {}): GridColumn => ({ name, type, notNull: false, pk: 0, default: null, generated: false, ...extra })
const text = (v: string): Cell => ({ t: "text", v, len: v.length, cut: false })

const columns = [col("id", "INTEGER", { pk: 1 }), col("email", "TEXT", { notNull: true }), col("bio", "TEXT", { default: "'none'" }), col("photo", "BLOB"), col("slug", "TEXT", { generated: true })]

function result(over: Partial<BrowseResult> = {}): BrowseResult {
  return {
    objects: [
      { name: "users", kind: "table" },
      { name: worstTableName, kind: "table" },
      { name: "active_users", kind: "view" },
    ],
    table: { name: "users", kind: "table", columns, readOnly: null, editable: true },
    missing: false,
    rows: [
      { key: [encodeKey(1n)], cells: [{ t: "int", v: "1" }, text("jane@acme.com"), bigText, { t: "blob", size: 2048, hex: "ffd8ffe0" }, text("jane")] },
      { key: [encodeKey(2n)], cells: [{ t: "int", v: "2" }, text("sam@acme.com"), { t: "null" }, { t: "null" }, text("sam")] },
    ],
    total: 1284,
    page: 1,
    pageSize: 50,
    error: null,
    ...over,
  }
}

const query: BrowseQuery = { table: "users", page: 1, sort: null, filters: [] }

function actions(over: Partial<DataActions> = {}): DataActions {
  return {
    loadCell: vi.fn(async () => ({ t: "text" as const, v: "the whole value" })),
    updateCell: vi.fn(async () => {}),
    insertRow: vi.fn(async () => {}),
    deleteRows: vi.fn(async () => {}),
    ...over,
  }
}

const tick = () => act(() => new Promise((r) => setTimeout(r, 0)))

test("an agent can pick a table, select a row and delete it after asking", async () => {
  const onQueryChange = vi.fn()
  const a = actions()
  render(
    <>
      <DataBrowser result={result()} query={query} onQueryChange={onQueryChange} actions={a} />
      <Toaster />
    </>,
  )
  await tick()

  const entries = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  const labels = entries.map((e) => e.label)
  for (const label of ["Table", "Filter", "Insert row", "users", "Rows pages"]) expect(labels, `${label} is missing`).toContain(label)

  let r = await agent.act({ steps: [{ ref: "Table", action: "setValue", args: ["active_users"] }] })
  expect(r.ok, r.error?.message).toBe(true)
  expect(onQueryChange).toHaveBeenCalledWith({ table: "active_users", page: 1, sort: null, filters: [] })

  r = await agent.act({ steps: [{ ref: "users", action: "toggleRow", args: [JSON.stringify(["i:2"])] }] })
  expect(r.ok, r.error?.message).toBe(true)
  await tick()

  const remove = {
    steps: [
      { ref: "Delete row", action: "press" },
      { ref: "Type delete to confirm", action: "setValue", args: ["delete"] },
      { ref: "Delete 1 row?", action: "press", args: ["Delete forever"] },
    ],
  }
  approve = false
  expect((await agent.act(remove)).ok).toBe(false)
  expect(a.deleteRows).not.toHaveBeenCalled()
  approve = true
  const stillOpen = agent.snapshot().some((e) => e.label === "Delete 1 row?")
  r = await agent.act(stillOpen ? { steps: remove.steps.slice(1) } : remove)
  expect(r.ok, r.error?.message).toBe(true)
  await waitFor(() => expect(a.deleteRows).toHaveBeenCalledWith([[{ t: "int", v: "2" }]]))
  approve = false
})

test("a double click edits a cell, and NULL sends null", async () => {
  const user = userEvent.setup()
  const a = actions()
  render(
    <>
      <DataBrowser result={result()} query={query} onQueryChange={() => {}} actions={a} />
      <Toaster />
    </>,
  )
  await tick()

  await user.dblClick(screen.getByRole("gridcell", { name: "sam@acme.com" }))
  const dialog = await screen.findByRole("dialog", { name: /email/ })
  const value = within(dialog).getByLabelText("Value")
  expect((value as HTMLTextAreaElement).value).toBe("sam@acme.com")
  await user.clear(value)
  await user.type(value, "sam@example.com")
  await user.click(within(dialog).getByRole("button", { name: "Save" }))
  expect(a.updateCell).toHaveBeenCalledWith([{ t: "int", v: "2" }], "email", "sam@example.com")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())

  await user.dblClick(screen.getByRole("gridcell", { name: "jane@acme.com" }))
  const again = await screen.findByRole("dialog", { name: /email/ })
  await user.click(within(again).getByRole("switch", { name: "NULL" }))
  expect((within(again).getByLabelText("Value") as HTMLTextAreaElement).disabled).toBe(true)
  await user.click(within(again).getByRole("button", { name: "Save" }))
  expect(a.updateCell).toHaveBeenLastCalledWith([{ t: "int", v: "1" }], "email", null)
})

test("a cut value loads in full before it can be edited", async () => {
  const user = userEvent.setup()
  let resolve: (v: { t: "text"; v: string }) => void = () => {}
  const a = actions({ loadCell: vi.fn(() => new Promise<FullCell>((r) => (resolve = r))) })
  render(<DataBrowser result={result()} query={query} onQueryChange={() => {}} actions={a} />)
  await tick()

  const cells = within(screen.getAllByRole("row")[1]!).getAllByRole("gridcell")
  await user.dblClick(cells[3]!)
  const dialog = await screen.findByRole("dialog", { name: /bio/ })
  expect(a.loadCell).toHaveBeenCalledWith([{ t: "int", v: "1" }], "bio")
  expect(within(dialog).getByRole("button", { name: "Save" }).hasAttribute("disabled")).toBe(true)
  await act(async () => resolve({ t: "text", v: "the whole value" }))
  expect((within(dialog).getByLabelText("Value") as HTMLTextAreaElement).value).toBe("the whole value")
})

test("a value over 1 MB says so and can't be saved", async () => {
  const user = userEvent.setup()
  const a = actions({ loadCell: vi.fn(async () => Promise.reject(new Error("This value is over 1 MB. Change it with SQL."))) })
  render(<DataBrowser result={result()} query={query} onQueryChange={() => {}} actions={a} />)
  await tick()
  await user.dblClick(within(screen.getAllByRole("row")[1]!).getAllByRole("gridcell")[3]!)
  const dialog = await screen.findByRole("dialog", { name: /bio/ })
  expect(await within(dialog).findByText("This value is over 1 MB. Change it with SQL.")).toBeTruthy()
  expect(within(dialog).getByRole("button", { name: "Save" }).hasAttribute("disabled")).toBe(true)
})

test("blobs open as their size, not an editor", async () => {
  const user = userEvent.setup()
  render(<DataBrowser result={result()} query={query} onQueryChange={() => {}} actions={actions()} />)
  await tick()
  await user.dblClick(screen.getByRole("gridcell", { name: "BLOB 2.0 KB" }))
  const dialog = await screen.findByRole("dialog", { name: /photo/ })
  expect(within(dialog).getByText("Blobs can't be edited here.")).toBeTruthy()
  expect(within(dialog).queryByRole("button", { name: "Save" })).toBeNull()
})

test("a read-only table shows values but no edit controls", async () => {
  const user = userEvent.setup()
  const ro = result({ table: { name: "active_users", kind: "view", columns: columns.slice(0, 2), readOnly: "Views are read-only.", editable: false }, rows: [{ key: null, cells: [{ t: "int", v: "1" }, text("jane@acme.com")] }] })
  render(<DataBrowser result={ro} query={{ ...query, table: "active_users" }} onQueryChange={() => {}} actions={actions()} />)
  await tick()
  expect(screen.getByText("Read-only")).toBeTruthy()
  expect(screen.queryByRole("button", { name: "Insert row" })).toBeNull()
  expect(screen.getByRole("grid", { name: "active_users" }).getAttribute("aria-multiselectable")).toBeNull()
  await user.dblClick(screen.getByRole("gridcell", { name: "jane@acme.com" }))
  const dialog = await screen.findByRole("dialog", { name: /email/ })
  const value = within(dialog).getByLabelText("Value")
  expect(value.tagName).toBe("PRE")
  expect(value.textContent).toBe("jane@acme.com")
  expect(within(dialog).queryByRole("textbox")).toBeNull()
  expect(within(dialog).queryByRole("button", { name: "Save" })).toBeNull()
})

test("a filter is added from the popover and resets the page", async () => {
  const user = userEvent.setup()
  const onQueryChange = vi.fn()
  render(<DataBrowser result={result()} query={query} onQueryChange={onQueryChange} actions={actions()} />)
  await tick()
  const r = await agent.act({
    steps: [
      { ref: "Filter", action: "press" },
      { ref: "Column", action: "setValue", args: ["email"] },
      { ref: "Operator", action: "setValue", args: ["contains"] },
      { ref: "Value", action: "setValue", args: ["50%"] },
    ],
  })
  expect(r.ok, r.error?.message).toBe(true)
  await user.click(screen.getByRole("button", { name: "Apply filter" }))
  expect(onQueryChange).toHaveBeenCalledWith({ filters: [{ column: "email", op: "contains", value: "50%" }], page: 1 })
})

test("filters show as chips that can be removed", async () => {
  const user = userEvent.setup()
  const onQueryChange = vi.fn()
  const filters = [
    { column: "email", op: "is null" as const },
    { column: "id", op: ">=" as const, value: "10" },
  ]
  render(<DataBrowser result={result({ rows: [], total: 0 })} query={{ ...query, filters }} onQueryChange={onQueryChange} actions={actions()} />)
  await tick()
  expect(screen.getByText("No rows match")).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "Remove filter email is null" }))
  expect(onQueryChange).toHaveBeenCalledWith({ filters: [filters[1]], page: 1 })
})

test("insert sends only the filled fields and skips generated columns", async () => {
  const user = userEvent.setup()
  const a = actions()
  render(
    <>
      <DataBrowser result={result()} query={query} onQueryChange={() => {}} actions={a} />
      <Toaster />
    </>,
  )
  await tick()
  await user.click(screen.getByRole("button", { name: "Insert row" }))
  const dialog = await screen.findByRole("dialog", { name: "Insert into users" })
  expect(within(dialog).queryByLabelText(/slug/)).toBeNull()
  expect(within(dialog).getByLabelText(/^id/).getAttribute("placeholder")).toBe("Auto")
  expect(within(dialog).getByLabelText(/^bio/).getAttribute("placeholder")).toBe("'none'")
  await user.type(within(dialog).getByLabelText(/^email/), "kai@acme.com")
  await user.click(within(dialog).getByRole("button", { name: "Insert" }))
  expect(a.insertRow).toHaveBeenCalledWith({ email: "kai@acme.com" })
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
})

test("an insert error stays in the dialog", async () => {
  const user = userEvent.setup()
  const a = actions({ insertRow: vi.fn(async () => Promise.reject(new Error("NOT NULL constraint failed: users.email"))) })
  render(<DataBrowser result={result()} query={query} onQueryChange={() => {}} actions={a} />)
  await tick()
  await user.click(screen.getByRole("button", { name: "Insert row" }))
  await user.click(await screen.findByRole("button", { name: "Insert" }))
  expect(await screen.findByText("NOT NULL constraint failed: users.email")).toBeTruthy()
  expect(screen.getByRole("dialog", { name: "Insert into users" })).toBeTruthy()
})

test("pages: the range, and Next asks for page 2", async () => {
  const user = userEvent.setup()
  const onQueryChange = vi.fn()
  render(<DataBrowser result={result()} query={query} onQueryChange={onQueryChange} actions={actions()} />)
  await tick()
  expect(screen.getByText("1–50 of 1,284")).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "Next page" }))
  expect(onQueryChange).toHaveBeenCalledWith({ page: 2 })
})

test("states: no tables, a missing table, and an engine error", async () => {
  const { rerender } = render(<DataBrowser result={result({ objects: [], table: null, rows: [], total: 0 })} query={query} onQueryChange={() => {}} actions={actions()} />)
  expect(screen.getByText("No tables yet")).toBeTruthy()
  rerender(<DataBrowser result={result({ table: null, missing: true, rows: [], total: 0 })} query={{ ...query, table: "gone" }} onQueryChange={() => {}} actions={actions()} />)
  expect(screen.getByText("This table doesn't exist on this branch")).toBeTruthy()
  rerender(<DataBrowser result={result({ rows: [], total: 0, error: "Can't reach the engine." })} query={query} onQueryChange={() => {}} actions={actions()} />)
  expect(screen.getByText("Can't reach the engine.")).toBeTruthy()
})

test("a read-only role sees the same Read-only status on a writable table", async () => {
  render(<DataBrowser result={result({ table: { name: "users", kind: "table", columns, readOnly: null, editable: false } })} query={query} onQueryChange={() => {}} actions={actions()} />)
  await tick()
  expect(screen.getByText("Read-only")).toBeTruthy()
  expect(screen.queryByRole("button", { name: "Insert row" })).toBeNull()
})

test("a value cut without a known length opens in full before editing", async () => {
  const user = userEvent.setup()
  const cut: Cell = { t: "text", v: "x".repeat(1000), len: null, cut: true }
  const loadCell = vi.fn(async (): Promise<FullCell> => ({ t: "text", v: "x".repeat(1500) }))
  const rows = [{ key: [encodeKey(1n)], cells: [{ t: "int", v: "1" } as Cell, cut, text("b"), { t: "null" } as Cell, text("s")] }]
  render(<DataBrowser result={result({ rows })} query={query} onQueryChange={() => {}} actions={actions({ loadCell })} />)
  await tick()
  await user.dblClick(screen.getAllByRole("gridcell")[2]!)
  const dialog = await screen.findByRole("dialog", { name: /email/ })
  await waitFor(() => expect((within(dialog).getByLabelText("Value") as HTMLTextAreaElement).value).toHaveLength(1500))
  expect(loadCell).toHaveBeenCalledOnce()
})
