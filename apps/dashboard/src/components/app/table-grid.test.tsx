import { act, render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { useState } from "react"
import { expect, test, vi } from "vitest"
import type { GridSort } from "@/components/ui/data-grid"
import { bigText, demoColumns, demoRows, worstColumns, worstRows } from "./grid-fixtures"
import { TableGrid } from "./table-grid"

const agent = createAgent()

function Editable({ onOpenCell = () => {} }: { onOpenCell?: (rowId: string, col: number) => void }) {
  const [sort, setSort] = useState<GridSort>(null)
  const [selected, setSelected] = useState<string[]>([])
  return (
    <>
      <TableGrid label="users" columns={demoColumns} rows={demoRows} sort={sort} onSortChange={setSort} selected={selected} onSelectedChange={setSelected} onOpenCell={onOpenCell} />
      <output aria-label="Selection">{selected.join(",")}</output>
      <output aria-label="Sort">{sort ? `${sort.column} ${sort.dir}` : ""}</output>
    </>
  )
}

const tick = () => act(() => new Promise((r) => setTimeout(r, 0)))

test("an agent can find the grid, sort it, select rows and open a cell", async () => {
  const onOpenCell = vi.fn()
  render(<Editable onOpenCell={onOpenCell} />)
  await tick()

  const entries = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  const grid = entries.find((e) => e.label === "users")
  expect(grid, "the grid is missing from the snapshot").toBeTruthy()
  expect(grid!.actions).toEqual(expect.arrayContaining(["sortBy", "toggleRow", "toggleAll", "openCell"]))

  const result = await agent.act({
    steps: [
      { ref: "users", action: "sortBy", args: ["email", "desc"] },
      { ref: "users", action: "toggleRow", args: ["2"] },
      { ref: "users", action: "openCell", args: ["3", "name"] },
    ],
  })
  expect(result.ok, result.error?.message).toBe(true)
  expect(screen.getByLabelText("Sort").textContent).toBe("email desc")
  expect(screen.getByLabelText("Selection").textContent).toBe("2")
  expect(onOpenCell).toHaveBeenCalledWith("3", 2)
})

test("one tab stop, arrows move, Enter opens, Space selects", async () => {
  const user = userEvent.setup()
  const onOpenCell = vi.fn()
  render(<Editable onOpenCell={onOpenCell} />)
  await tick()

  const grid = screen.getByRole("grid", { name: "users" })
  const tabbable = grid.querySelectorAll('[tabindex="0"]')
  expect(tabbable).toHaveLength(1)

  await user.tab()
  expect(document.activeElement).toBe(screen.getByRole("columnheader", { name: "Select all rows" }))
  await user.keyboard("{ArrowDown}{ArrowRight}{ArrowRight}")
  const email = within(grid).getByRole("gridcell", { name: "jane@acme.com" })
  expect(document.activeElement).toBe(email)

  await user.keyboard("{Enter}")
  expect(onOpenCell).toHaveBeenCalledWith("1", 1)

  await user.keyboard(" ")
  expect(screen.getByLabelText("Selection").textContent).toBe("1")
  expect(within(grid).getAllByRole("row")[1]!.getAttribute("aria-selected")).toBe("true")

  await user.keyboard("{ArrowUp}{Enter}")
  expect(screen.getByLabelText("Sort").textContent).toBe("email asc")
  expect(screen.getByRole("columnheader", { name: /email/ }).getAttribute("aria-sort")).toBe("ascending")
  await user.keyboard("{Enter}")
  expect(screen.getByLabelText("Sort").textContent).toBe("email desc")

  await user.keyboard("{Home}{Enter}")
  expect(screen.getByLabelText("Selection").textContent).toBe("1,2,3")
  await user.keyboard("{Enter}")
  expect(screen.getByLabelText("Selection").textContent).toBe("")
})

test("a click selects with the checkbox column and a double click opens a cell", async () => {
  const user = userEvent.setup()
  const onOpenCell = vi.fn()
  render(<Editable onOpenCell={onOpenCell} />)
  await tick()
  const rows = screen.getAllByRole("row")
  await user.click(within(rows[2]!).getByRole("gridcell", { name: "Select row" }))
  expect(screen.getByLabelText("Selection").textContent).toBe("2")
  await user.click(screen.getByRole("gridcell", { name: "Sam Lee" }))
  expect(onOpenCell).not.toHaveBeenCalled()
  await user.dblClick(screen.getByRole("gridcell", { name: "Sam Lee" }))
  expect(onOpenCell).toHaveBeenCalledWith("2", 2)
})

test("a read-only grid has no selection column and headers that don't sort", async () => {
  render(<TableGrid label="Result 1" columns={demoColumns} rows={demoRows} />)
  await tick()
  const grid = screen.getByRole("grid", { name: "Result 1" })
  expect(grid.getAttribute("aria-multiselectable")).toBeNull()
  expect(screen.queryByRole("columnheader", { name: "Select all rows" })).toBeNull()
  for (const h of screen.getAllByRole("columnheader")) expect(h.getAttribute("aria-sort")).toBeNull()
})

test("worst case: 300 columns, a 5 MB value, blobs, NULLs and huge numbers", async () => {
  render(<TableGrid label="wide" columns={worstColumns} rows={worstRows} onSelectedChange={() => {}} selected={[]} />)
  await tick()
  const grid = screen.getByRole("grid", { name: "wide" })
  expect(grid.getAttribute("aria-colcount")).toBe("301")
  expect(within(grid).getAllByRole("columnheader")).toHaveLength(301)
  expect(within(grid).getAllByRole("row")).toHaveLength(51)

  const first = within(grid).getAllByRole("row")[1]!
  const cells = within(first).getAllByRole("gridcell")
  expect(cells[1]!.textContent).toBe("9223372036854775807")
  expect(cells[1]!.className).toContain("text-right")
  expect(cells[3]!.textContent).toBe(bigText.t === "text" ? bigText.v : "")
  expect(cells[3]!.textContent!.length).toBe(1000)
  expect(cells[4]!.textContent).toBe("BLOB 2.3 MB")
  expect(cells[6]!.textContent).toBe("NULL")
  expect(grid.querySelectorAll('[tabindex="0"]')).toHaveLength(1)
})

test("repeated column names stay apart", async () => {
  const onOpenCell = vi.fn()
  render(
    <TableGrid
      label="Result 1"
      columns={[{ name: "a" }, { name: "a" }]}
      rows={[{ id: "0", cells: [{ t: "int", v: "1" }, { t: "int", v: "2" }] }]}
      onOpenCell={onOpenCell}
    />,
  )
  await tick()
  await userEvent.setup().dblClick(screen.getByRole("gridcell", { name: "2" }))
  expect(onOpenCell).toHaveBeenCalledWith("0", 1)
})

test("zero rows shows the header and the empty message", async () => {
  render(<TableGrid label="empty" columns={demoColumns} rows={[]} empty="No rows" />)
  await tick()
  expect(screen.getAllByRole("columnheader")).toHaveLength(4)
  expect(screen.getByText("No rows")).toBeTruthy()
})
