import type { Schema } from "@lumbung/client"
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test } from "vitest"
import { worstTableName } from "./grid-fixtures"
import { SchemaView } from "./schema-view"

// 300-column renders take a few seconds on a shared CI runner.
const WIDE = { timeout: 30_000 }

const agent = createAgent()

const wideColumns = Array.from({ length: 300 }, (_, i) => ({ name: `attribute_${i}`, type: "TEXT", not_null: false, pk: 0, default: null, hidden: 0 }))

const schema: Schema = {
  tables: [
    {
      name: "users",
      virtual: false,
      sql: "CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL DEFAULT '')",
      columns: [
        { name: "id", type: "INTEGER", not_null: false, pk: 1, default: null, hidden: 0 },
        { name: "email", type: "TEXT", not_null: true, pk: 0, default: "''", hidden: 0 },
      ],
    },
    { name: worstTableName, virtual: false, sql: `CREATE TABLE ${worstTableName} (${wideColumns.map((c) => `${c.name} TEXT`).join(", ")})`, columns: wideColumns },
    { name: "sqlite_sequence", virtual: false, sql: "", columns: [] },
  ],
  views: [{ name: "active_users", sql: "CREATE VIEW active_users AS SELECT * FROM users" }],
  indexes: [{ name: "users_email", table: "users", unique: true, columns: ["email"], sql: "CREATE UNIQUE INDEX users_email ON users(email)" }],
  triggers: [{ name: "users_touch", table: "users", sql: "CREATE TRIGGER users_touch AFTER UPDATE ON users BEGIN SELECT 1; END" }],
}

test("an agent can search the schema and open a table", async () => {
  render(<SchemaView schema={schema} />)
  const entries = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  expect(entries.map((e) => e.label)).toContain("Search schema")

  const r = await agent.act({ steps: [{ ref: "Search schema", action: "setValue", args: ["users"] }] })
  expect(r.ok, r.error?.message).toBe(true)
  expect(await screen.findByRole("button", { name: /^active_users/ })).toBeTruthy()
  await waitFor(() => expect(screen.queryByRole("button", { name: new RegExp(`^${worstTableName}`) })).toBeNull())
  expect(screen.queryByText("sqlite_sequence")).toBeNull()
})

test("a small schema starts open: columns, indexes, triggers and DDL with copy buttons", async () => {
  const user = userEvent.setup()
  render(<SchemaView schema={schema} />)
  expect(screen.getByLabelText("Primary key")).toBeTruthy()
  expect(screen.getByText("users_email")).toBeTruthy()
  expect(screen.getByText("users_touch")).toBeTruthy()
  for (const name of ["Copy users DDL", "Copy users_email DDL", "Copy users_touch DDL"]) expect(screen.getByRole("button", { name })).toBeTruthy()
  await user.click(screen.getByRole("button", { name: /^users/ }))
  await waitFor(() => expect(screen.queryByText("users_email")).toBeNull())
})

test("a 300-column table opens into a scrolling list", WIDE, async () => {
  render(<SchemaView schema={schema} />)
  const trigger = screen.getByRole("button", { name: new RegExp(`^${worstTableName}`) })
  expect(trigger.textContent).toContain("300 columns")
  expect(trigger.getAttribute("aria-expanded")).toBe("true")
  const list = screen.getByLabelText(`Columns of ${worstTableName}`)
  expect(within(list).getAllByRole("row")).toHaveLength(301)
})

test("empty states", async () => {
  const user = userEvent.setup()
  const { rerender } = render(<SchemaView schema={{ tables: [], views: [], indexes: [], triggers: [] }} />)
  expect(screen.getByText("No tables yet")).toBeTruthy()
  rerender(<SchemaView schema={schema} />)
  await user.type(screen.getByLabelText("Search schema"), "zzz")
  expect(await screen.findByText("No matches")).toBeTruthy()
})
