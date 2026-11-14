import { screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import { renderWithRouter } from "@/test/router"
import { AppShell } from "./shell"

function shell(role: "owner" | "member" | "readonly", databases: [] | null = [], buckets: [] | null = []) {
  const viewer = { id: "u1", name: "Dev", email: "dev@example.com", role } as never
  return renderWithRouter(
    <AppShell viewer={viewer} databases={databases} buckets={buckets}>
      <div />
    </AppShell>,
  )
}

function rail() {
  return within(screen.getAllByRole("navigation", { name: "Main" })[0])
}

test("an empty instance offers to create a database and a bucket from the sidebar", async () => {
  await shell("owner")
  expect(rail().getByRole("button", { name: "New database" })).toBeTruthy()
  expect(rail().getByRole("button", { name: "New bucket" })).toBeTruthy()
})

test("a member can create databases but not buckets", async () => {
  await shell("member")
  expect(rail().getByRole("button", { name: "New database" })).toBeTruthy()
  expect(rail().queryByRole("heading", { name: "Buckets" })).toBeNull()
  expect(rail().queryByRole("button", { name: "New bucket" })).toBeNull()
})

test("a read-only person sees no empty groups they can't fill", async () => {
  await shell("readonly")
  expect(rail().queryByRole("heading", { name: "Databases" })).toBeNull()
  expect(rail().queryByRole("heading", { name: "Buckets" })).toBeNull()
  expect(rail().queryByRole("button", { name: "New database" })).toBeNull()
})

test("a failed load still says so, whatever the role", async () => {
  await shell("readonly", null, null)
  expect(rail().getByText("Can't load databases")).toBeTruthy()
  expect(rail().getByText("Can't load buckets")).toBeTruthy()
})
