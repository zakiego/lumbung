import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { afterEach, beforeEach, expect, test, vi } from "vitest"
import type { RunResult, StatementResult } from "@/fns/sql"
import { bigText, worstColumns, worstRows } from "./grid-fixtures"
import { SqlConsole, planTree, pushHistory, readHistory } from "./sql-console"

// 300-column renders take a few seconds on a shared CI runner.
const WIDE = { timeout: 30_000 }

const agent = createAgent()

const select = (over: Partial<StatementResult> = {}): StatementResult => ({
  columns: ["id", "email"],
  rows: [[{ t: "int", v: "1" }, { t: "text", v: "jane@acme.com", len: 13, cut: false }]],
  rowCount: 1,
  capped: false,
  rowsAffected: 0,
  lastInsertRowid: "0",
  ...over,
})
const write = (n: number): StatementResult => ({ columns: [], rows: [], rowCount: 0, capped: false, rowsAffected: n, lastInsertRowid: "0" })

function setup(run = vi.fn(async (_s: string[]): Promise<RunResult> => ({ results: [select()], ms: 4, readOnly: false })), explain = vi.fn(async () => [{ id: 2, parent: 0, detail: "SCAN users" }])) {
  render(<SqlConsole db="shop" readOnly={false} run={run} explain={explain} />)
  return { run, explain, editor: screen.getByLabelText("SQL") as HTMLTextAreaElement }
}

beforeEach(() => {
  localStorage.clear()
  sessionStorage.clear()
})
afterEach(() => vi.restoreAllMocks())

test("an agent can write SQL and run it", async () => {
  const { run } = setup()
  const entries = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  const labels = entries.map((e) => e.label)
  for (const label of ["SQL", "Run", "Plan", "History"]) expect(labels, `${label} is missing`).toContain(label)

  const r = await agent.act({
    steps: [
      { ref: "SQL", action: "setValue", args: ["SELECT 1; SELECT 2"] },
      { ref: "Run", action: "press" },
    ],
  })
  expect(r.ok, r.error?.message).toBe(true)
  await waitFor(() => expect(run).toHaveBeenCalledWith(["SELECT 1", "SELECT 2"]))
  expect(await screen.findByRole("grid", { name: "Result 1" })).toBeTruthy()
})

test("Cmd or Ctrl+Enter runs the selection when there is one", async () => {
  const user = userEvent.setup()
  const { run, editor } = setup()
  await user.type(editor, "SELECT 1;\nSELECT 2;")
  editor.setSelectionRange(10, 19)
  fireEvent.keyDown(editor, { key: "Enter", metaKey: true, ctrlKey: true })
  await waitFor(() => expect(run).toHaveBeenCalledWith(["SELECT 2"]))

  editor.setSelectionRange(0, 0)
  await user.keyboard("{Control>}{Enter}{/Control}")
  await waitFor(() => expect(run).toHaveBeenLastCalledWith(["SELECT 1", "SELECT 2"]))
})

test("a batch shows each statement's result, the rows changed and the time", async () => {
  const user = userEvent.setup()
  const run = vi.fn(async () => ({ results: [write(3), select({ rows: [], rowCount: 0 }), select({ rowCount: 4210, capped: true })], ms: 1234, readOnly: false }))
  const { editor } = setup(run)
  await user.type(editor, "UPDATE t SET x = 1; SELECT * FROM t WHERE 0; SELECT * FROM t")
  await user.click(screen.getByRole("button", { name: /Run/ }))
  expect(await screen.findByText("3 statements · 1,234 ms")).toBeTruthy()
  expect(screen.getByText("3 rows changed")).toBeTruthy()
  expect(within(screen.getByRole("region", { name: "Result 2" })).getByText("No rows")).toBeTruthy()
  expect(screen.getByText("4,210 rows")).toBeTruthy()
  expect(screen.getByText("First 1 of 4,210 rows")).toBeTruthy()
})

test("an error is shown as the engine wrote it", async () => {
  const user = userEvent.setup()
  const run = vi.fn(async () => Promise.reject(new Error('no such table: "userz"')))
  const { editor } = setup(run)
  await user.type(editor, "SELECT * FROM userz")
  await user.click(screen.getByRole("button", { name: /Run/ }))
  expect((await screen.findByRole("alert")).textContent).toBe('no such table: "userz"')
})

test("Plan shows the plan as a tree and wants one statement", async () => {
  const user = userEvent.setup()
  const explain = vi.fn(async () => [
    { id: 2, parent: 0, detail: "SCAN users" },
    { id: 5, parent: 2, detail: "SEARCH orders USING INDEX orders_user (user_id=?)" },
  ])
  const { editor } = setup(undefined, explain)
  await user.type(editor, "SELECT * FROM users JOIN orders ON orders.user_id = users.id;")
  await user.click(screen.getByRole("button", { name: "Plan" }))
  const plan = await screen.findByRole("region", { name: "Query plan" })
  expect(explain).toHaveBeenCalledWith("SELECT * FROM users JOIN orders ON orders.user_id = users.id")
  const nested = within(plan).getByText(/SEARCH orders/).closest("ul")!
  expect(nested.parentElement!.textContent).toContain("SCAN users")

  await user.type(editor, " SELECT 2")
  await user.click(screen.getByRole("button", { name: "Plan" }))
  expect((await screen.findByRole("alert")).textContent).toMatch(/one statement at a time/)
  expect(explain).toHaveBeenCalledOnce()
})

test("history keeps the last 50 per database and loads an entry back", async () => {
  const user = userEvent.setup()
  let list = readHistory("shop")
  for (let i = 0; i < 60; i++) list = pushHistory("shop", `SELECT ${i}`, list, i)
  expect(readHistory("shop")).toHaveLength(50)
  expect(readHistory("shop")[0]!.sql).toBe("SELECT 59")
  expect(readHistory("other")).toEqual([])

  const { editor } = setup()
  await act(() => new Promise((r) => setTimeout(r, 0)))
  await user.click(screen.getByRole("button", { name: "History" }))
  await user.click(await screen.findByRole("menuitem", { name: /SELECT 58/ }))
  expect(editor.value).toBe("SELECT 58")
})

test("history survives storage that throws", () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new Error("blocked")
  })
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("full")
  })
  expect(readHistory("shop")).toEqual([])
  expect(pushHistory("shop", "SELECT 1", [])).toHaveLength(1)
})

test("the draft survives leaving the tab, per database", async () => {
  const user = userEvent.setup()
  const { editor } = setup()
  await user.type(editor, "SELECT 42")
  cleanup()
  render(<SqlConsole db="shop" readOnly={false} run={vi.fn()} explain={vi.fn()} />)
  expect((screen.getByLabelText("SQL") as HTMLTextAreaElement).value).toBe("SELECT 42")
  cleanup()
  render(<SqlConsole db="blog" readOnly={false} run={vi.fn()} explain={vi.fn()} />)
  expect((screen.getByLabelText("SQL") as HTMLTextAreaElement).value).toBe("")
})

test("a read-only role sees it", () => {
  render(<SqlConsole db="shop" readOnly run={vi.fn()} explain={vi.fn()} />)
  expect(screen.getByText("Read-only")).toBeTruthy()
})

test("worst case: 300 columns and a 5 MB value open without the full value", WIDE, async () => {
  const user = userEvent.setup()
  const wide = select({ columns: worstColumns.map((c) => c.name), rows: worstRows.map((r) => r.cells), rowCount: 50 })
  const { editor } = setup(vi.fn(async () => ({ results: [wide], ms: 9, readOnly: false })))
  await user.type(editor, "SELECT * FROM wide")
  await user.click(screen.getByRole("button", { name: /Run/ }))
  const grid = await screen.findByRole("grid", { name: "Result 1" })
  expect(within(grid).getAllByRole("columnheader")).toHaveLength(300)
  const cell = within(within(grid).getAllByRole("row")[1]!).getAllByRole("gridcell")[2]!
  await user.dblClick(cell)
  const dialog = await screen.findByRole("dialog", { name: /notes/ })
  expect(within(dialog).getByLabelText("Value").textContent).toBe(bigText.t === "text" ? bigText.v : "")
  expect(within(dialog).getByText("First 1,000 of 5,242,880 characters")).toBeTruthy()
})

test("a wide result draws a few rows at a time", WIDE, async () => {
  const user = userEvent.setup()
  const rows = Array.from({ length: 120 }, (_, i) => worstRows[i % worstRows.length]!.cells)
  const wide = select({ columns: worstColumns.map((c) => c.name), rows, rowCount: 120 })
  const { editor } = setup(vi.fn(async () => ({ results: [wide], ms: 9, readOnly: false })))
  await user.type(editor, "SELECT * FROM wide")
  await user.click(screen.getByRole("button", { name: /Run/ }))
  const grid = await screen.findByRole("grid", { name: "Result 1" })
  expect(within(grid).getAllByRole("row")).toHaveLength(51)
  expect(screen.getByText("First 50 of 120 rows")).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "Show more rows of Result 1" }))
  expect(within(grid).getAllByRole("row")).toHaveLength(101)
})

test("planTree nests by parent", () => {
  expect(planTree([{ id: 1, parent: 0, detail: "a" }, { id: 2, parent: 1, detail: "b" }, { id: 3, parent: 0, detail: "c" }]).map((n) => [n.detail, n.children.map((c) => c.detail)])).toEqual([
    ["a", ["b"]],
    ["c", []],
  ])
})
