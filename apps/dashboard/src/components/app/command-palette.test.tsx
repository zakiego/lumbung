import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"
import { createDialogController } from "@/components/ui/dialog"
import type { Role } from "@/lib/roles"
import * as worst from "@/test/worst-case"
import { CommandPalette } from "./command-palette"

const DEFAULT = { id: "ws_aaaaaaaaaaaaaaaaaaaa", name: "default" }
const AGENTS = { id: "ws_bbbbbbbbbbbbbbbbbbbb", name: "agents" }

function open(role: Role, workspace: typeof DEFAULT | null = DEFAULT) {
  const controller = createDialogController<unknown, unknown>()
  const navigate = vi.fn()
  render(
    <CommandPalette
      controller={controller}
      viewer={{ role, owner: role === "owner" }}
      workspace={workspace}
      workspaces={[DEFAULT, AGENTS]}
      databases={workspace ? worst.databases(3) : []}
      navigate={navigate}
    />,
  )
  controller.open()
  return { controller, navigate }
}

const options = () => screen.getAllByRole("option").map((o) => o.textContent)

const ADMIN_PAGES = ["Tokens", "Audit log", "Trash", "Workspace settings"]
const OWNER_PAGES = ["Members", "Instance settings"]

test("typing filters and Enter opens the database", async () => {
  const { navigate } = open("owner")
  const input = await screen.findByRole("combobox", { name: "Search" })
  await userEvent.type(input, "shop")
  await userEvent.keyboard("{Enter}")
  expect(navigate).toHaveBeenCalledWith("/w/$ws/db/$db", { ws: "default", db: "shop" })
  await waitFor(() => expect(screen.queryByRole("combobox", { name: "Search" })).toBeNull())
})

test.each<Role>(["readonly", "member"])("a %s person doesn't see admin or owner pages", async (role) => {
  open(role)
  await screen.findByRole("combobox", { name: "Search" })
  for (const name of [...ADMIN_PAGES, ...OWNER_PAGES]) expect(screen.queryByRole("option", { name }), name).toBeNull()
  expect(screen.getByRole("option", { name: "Overview" })).toBeTruthy()
  expect(screen.getByRole("option", { name: "Account" })).toBeTruthy()
  expect(screen.getByRole("option", { name: worst.LONG_DB })).toBeTruthy()
})

test("an admin sees the workspace's pages and not the instance's", async () => {
  const { navigate } = open("admin")
  await screen.findByRole("combobox", { name: "Search" })
  for (const name of ADMIN_PAGES) expect(screen.getByRole("option", { name }), name).toBeTruthy()
  for (const name of OWNER_PAGES) expect(screen.queryByRole("option", { name }), name).toBeNull()
  await userEvent.click(screen.getByRole("option", { name: "Trash" }))
  expect(navigate).toHaveBeenCalledWith("/w/$ws/trash", { ws: "default" })
})

test("an owner sees every page, each under the workspace or the instance", async () => {
  const pages: [string, string, Record<string, string> | undefined][] = [
    ["Overview", "/w/$ws", { ws: "default" }],
    ["Tokens", "/w/$ws/tokens", { ws: "default" }],
    ["Audit log", "/w/$ws/audit", { ws: "default" }],
    ["Trash", "/w/$ws/trash", { ws: "default" }],
    ["Workspace settings", "/w/$ws/settings", { ws: "default" }],
    ["Members", "/members", undefined],
    ["Instance settings", "/settings", undefined],
    ["Account", "/account", undefined],
  ]
  const { controller, navigate } = open("owner")
  for (const [name, to, params] of pages) {
    controller.open()
    await userEvent.click(await screen.findByRole("option", { name }))
    expect(navigate, name).toHaveBeenLastCalledWith(to, params)
    await waitFor(() => expect(screen.queryByRole("combobox", { name: "Search" })).toBeNull())
  }
  expect(navigate).toHaveBeenCalledTimes(pages.length)
})

test("the other workspaces are offered, never the one on screen", async () => {
  const { navigate } = open("member")
  await screen.findByRole("combobox", { name: "Search" })
  expect(screen.getByRole("option", { name: "agents" })).toBeTruthy()
  expect(screen.queryByRole("option", { name: "default" })).toBeNull()
  await userEvent.type(screen.getByRole("combobox", { name: "Search" }), "switch")
  expect(options()).toEqual(["agents"])
  await userEvent.keyboard("{Enter}")
  expect(navigate).toHaveBeenCalledWith("/w/$ws", { ws: "agents" })
})

test("without a workspace there are no workspace pages, whatever the role", async () => {
  open("admin", null)
  await screen.findByRole("combobox", { name: "Search" })
  for (const name of ["Overview", ...ADMIN_PAGES, ...OWNER_PAGES]) expect(screen.queryByRole("option", { name }), name).toBeNull()
  expect(options()).toEqual(["default", "agents", "Account", "Light theme", "Dark theme", "System theme"])
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
