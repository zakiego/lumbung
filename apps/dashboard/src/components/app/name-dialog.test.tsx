import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"
import { CreateDatabaseDialog } from "./name-dialog"

const agent = createAgent()

test("an agent can find the dialog's controls and create a database", async () => {
  const onCreate = vi.fn(async (_name: string) => {})
  render(<CreateDatabaseDialog onCreate={onCreate} />)

  const closed = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of closed) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  expect(closed.map((e) => e.label)).toContain("New database")

  const result = await agent.act({
    steps: [
      { ref: "New database", action: "press" },
      { ref: "Name", action: "setValue", args: ["shop"] },
      { ref: "Create database", action: "press", args: ["Create"] },
    ],
  })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onCreate).toHaveBeenCalledWith("shop"))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
})

test("the open dialog names every control", async () => {
  const user = userEvent.setup()
  render(<CreateDatabaseDialog onCreate={async () => {}} />)
  await user.click(screen.getByRole("button", { name: "New database" }))

  const open = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of open) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  const labels = open.map((e) => e.label)
  for (const label of ["Create database", "Name", "Create"]) expect(labels).toContain(label)
  expect(screen.getByRole("dialog", { name: "Create database" })).toBeTruthy()
  expect(screen.getByRole("button", { name: "Close" })).toBeTruthy()
})

test("a name the engine won't take never reaches the server", async () => {
  const user = userEvent.setup()
  const onCreate = vi.fn(async () => {})
  render(<CreateDatabaseDialog onCreate={onCreate} />)
  await user.click(screen.getByRole("button", { name: "New database" }))

  await user.click(screen.getByRole("button", { name: "Create" }))
  expect(onCreate).not.toHaveBeenCalled()

  await user.type(screen.getByLabelText("Name"), "My Shop")
  await user.click(screen.getByRole("button", { name: "Create" }))
  expect(onCreate).not.toHaveBeenCalled()
  expect(screen.getByText("Use lowercase letters, digits and dashes.")).toBeTruthy()
})

test("a server error stays in the dialog under the field", async () => {
  const user = userEvent.setup()
  render(
    <CreateDatabaseDialog
      onCreate={async () => {
        throw new Error("A database named shop already exists.")
      }}
    />,
  )
  await user.click(screen.getByRole("button", { name: "New database" }))
  await user.type(screen.getByLabelText("Name"), "shop")
  await user.click(screen.getByRole("button", { name: "Create" }))

  expect(await screen.findByText("A database named shop already exists.")).toBeTruthy()
  expect(screen.getByRole("dialog", { name: "Create database" })).toBeTruthy()

  await user.type(screen.getByLabelText("Name"), "-2")
  expect(screen.queryByText("A database named shop already exists.")).toBeNull()
})

test("Escape closes it and focus goes back to the trigger", async () => {
  const user = userEvent.setup()
  render(<CreateDatabaseDialog onCreate={async () => {}} />)
  const trigger = screen.getByRole("button", { name: "New database" })
  await user.click(trigger)
  expect(document.activeElement).toBe(screen.getByLabelText("Name"))
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(trigger)
})
