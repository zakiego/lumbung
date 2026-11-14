import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"
import { createDialogController } from "@/components/ui/dialog"
import * as worst from "@/test/worst-case"
import { CommandPalette } from "./command-palette"

function open(role: "owner" | "readonly") {
  const controller = createDialogController<unknown, unknown>()
  const navigate = vi.fn()
  render(<CommandPalette controller={controller} viewer={{ role }} databases={worst.databases(3)} navigate={navigate} />)
  controller.open()
  return { controller, navigate }
}

test("typing filters and Enter opens the database", async () => {
  const { navigate } = open("owner")
  const input = await screen.findByRole("combobox", { name: "Search" })
  await userEvent.type(input, "shop")
  await userEvent.keyboard("{Enter}")
  expect(navigate).toHaveBeenCalledWith("/db/$db", { db: "shop" })
  await waitFor(() => expect(screen.queryByRole("combobox", { name: "Search" })).toBeNull())
})

test("a read-only person doesn't see admin pages", async () => {
  open("readonly")
  await screen.findByRole("combobox", { name: "Search" })
  expect(screen.queryByRole("option", { name: "Tokens" })).toBeNull()
  expect(screen.queryByRole("option", { name: "Audit log" })).toBeNull()
  expect(screen.getByRole("option", { name: "Trash" })).toBeTruthy()
  expect(screen.getByRole("option", { name: worst.LONG_DB })).toBeTruthy()
})

test("an agent can find every command by name and run one", async () => {
  const { navigate } = open("owner")
  await screen.findByRole("combobox", { name: "Search" })
  const agent = createAgent()
  const labels = agent.snapshot().filter((e) => !e.offPage).map((e) => e.label)
  expect(labels).toContain("Search")
  for (const label of labels) expect(label).toBeTruthy()
  await userEvent.click(screen.getByRole("option", { name: "Members" }))
  expect(navigate).toHaveBeenCalledWith("/members", undefined)
})
