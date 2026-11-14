import type { Diff } from "@lumbung/client"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"
import { DeleteBranch, DiffView } from "./branches"

let approve = false
const agent = createAgent({ allow: (step) => !step.confirm || approve })

const LONG = "feature-add-customer-loyalty-points-and-tier-upgrades-2026-q4-x"

test("deleting a branch needs its exact name", async () => {
  const user = userEvent.setup()
  const onDelete = vi.fn(async () => {})
  render(<DeleteBranch branch={LONG} onDelete={onDelete} />)
  const trigger = screen.getByRole("button", { name: "Delete" })

  await user.click(trigger)
  expect(screen.getByRole("alertdialog", { name: `Delete ${LONG}?` })).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull())
  expect(document.activeElement).toBe(trigger)

  await user.click(trigger)
  const confirm = screen.getByRole("button", { name: "Delete branch" }) as HTMLButtonElement
  expect(confirm.disabled).toBe(true)
  await user.type(screen.getByLabelText(`Type ${LONG} to confirm`), LONG.slice(0, -1))
  expect(confirm.disabled).toBe(true)
  await user.type(screen.getByLabelText(`Type ${LONG} to confirm`), "x")
  await user.click(screen.getByRole("button", { name: "Delete branch" }))
  expect(onDelete).toHaveBeenCalledOnce()
  await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull())
})

test("a protected branch's refusal shows in the dialog", async () => {
  const user = userEvent.setup()
  render(
    <DeleteBranch
      branch="dev"
      onDelete={async () => {
        throw new Error("The branch is protected.")
      }}
    />,
  )
  await user.click(screen.getByRole("button", { name: "Delete" }))
  await user.type(screen.getByLabelText("Type dev to confirm"), "dev")
  await user.click(screen.getByRole("button", { name: "Delete branch" }))
  expect(await screen.findByText("The branch is protected.")).toBeTruthy()
  expect(screen.getByRole("alertdialog", { name: "Delete dev?" })).toBeTruthy()
})

test("an agent needs a person's yes to delete a branch", async () => {
  const onDelete = vi.fn(async () => {})
  render(<DeleteBranch branch="dev" onDelete={onDelete} />)

  const entries = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  expect(entries.map((e) => e.label)).toContain("Delete")

  const steps = [
    { ref: "Delete", action: "press" },
    { ref: "Type dev to confirm", action: "setValue", args: ["dev"] },
    { ref: "Delete dev?", action: "press", args: ["Delete branch"] },
  ]
  approve = false
  expect((await agent.act({ steps })).ok).toBe(false)
  expect(onDelete).not.toHaveBeenCalled()
  approve = true
  const result = await agent.act({ steps: [steps[2]!] })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onDelete).toHaveBeenCalledOnce())
})

test("the diff lists each change with its columns", async () => {
  const user = userEvent.setup()
  const diff: Diff = {
    branch: "dev",
    against: "main",
    branch_ddl: ["CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL, tier TEXT)"],
    against_ddl: ["CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)", "CREATE INDEX users_email ON users (email)"],
    changes: [
      {
        kind: "table",
        name: "users",
        change: "changed",
        before: "CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)",
        after: "CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL, tier TEXT)",
        columns: {
          added: [{ name: "tier", type: "TEXT", not_null: false, pk: 0, hidden: 0, default: null }],
          removed: [],
          changed: [
            {
              name: "email",
              before: { name: "email", type: "TEXT", not_null: false, pk: 0, hidden: 0, default: null },
              after: { name: "email", type: "TEXT", not_null: true, pk: 0, hidden: 0, default: null },
            },
          ],
        },
      },
      { kind: "index", name: "users_email", change: "removed", before: "CREATE INDEX users_email ON users (email)", after: "" },
    ],
  }
  render(<DiffView diff={diff} />)
  const list = screen.getByRole("list", { name: "Schema changes" })
  expect(list.textContent).toContain("Changed")
  expect(list.textContent).toContain("Removed")
  expect(screen.getByText("+ tier TEXT")).toBeTruthy()
  expect(screen.getByText("~ email TEXT → email TEXT NOT NULL")).toBeTruthy()

  await user.click(screen.getByRole("button", { name: "DDL for users_email" }))
  expect(await screen.findByText("CREATE INDEX users_email ON users (email);")).toBeTruthy()
})

test("a diff with no changes says so", () => {
  render(<DiffView diff={{ branch: "dev", against: "main", branch_ddl: [], against_ddl: [], changes: [] }} />)
  expect(screen.getByText("Same schema")).toBeTruthy()
  expect(screen.getByText("dev matches main.")).toBeTruthy()
})
