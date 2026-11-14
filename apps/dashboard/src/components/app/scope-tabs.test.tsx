import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test } from "vitest"
import type { WorkspaceInfo } from "@/lib/workspace"
import { unnamedSearchDialog, unnamedSidebarScroll } from "@/test/known-bugs"
import { renderWithRouter } from "@/test/router"
import { type Scope, ScopeFrame } from "./scope-tabs"
import { AppShell } from "./shell"

const agent = createAgent()

// The frame's header needs the shell around it, and the page sits on its real route.
function frame(owner: boolean, scope: Scope = "workspace", title = "Tokens", to: "/w/$ws/tokens" | "/w/$ws/audit" = "/w/$ws/tokens") {
  const at = to.replace("$ws", "agents") + (scope === "instance" ? "?scope=instance" : "")
  const role = owner ? "owner" : "admin"
  const workspace: WorkspaceInfo = { id: "ws_bbbbbbbbbbbbbbbbbbbb", name: "agents", default: false, role }
  const other: WorkspaceInfo = { id: "ws_aaaaaaaaaaaaaaaaaaaa", name: "default", default: true, role }
  return renderWithRouter(
    <AppShell viewer={{ id: "u1", name: "Dev", email: "dev@example.com", owner, role }} workspace={workspace} workspaces={[workspace, other]} databases={[]} buckets={[]}>
      <ScopeFrame title={title} to={to} scope={scope} owner={owner}>
        <p>the {title.toLowerCase()}</p>
      </ScopeFrame>
    </AppShell>,
    workspace,
    { route: to, at },
  )
}

test("an admin gets the workspace's page alone, with no tabs", async () => {
  await frame(false)
  expect(screen.getByRole("heading", { level: 1, name: "Tokens" })).toBeTruthy()
  expect(screen.getByText("the tokens")).toBeTruthy()
  expect(screen.queryByRole("tablist")).toBeNull()
  expect(screen.queryByRole("tab")).toBeNull()
})

test("an owner can switch between the workspace and the instance", async () => {
  const first = await frame(true)
  const tabs = within(screen.getByRole("tablist", { name: "Tokens scope" }))
  const workspace = tabs.getByRole("tab", { name: "Workspace" })
  const instance = tabs.getByRole("tab", { name: "Instance" })
  expect(workspace.getAttribute("href")).toBe("/w/agents/tokens")
  expect(instance.getAttribute("href")).toBe("/w/agents/tokens?scope=instance")
  expect(workspace.getAttribute("aria-selected")).toBe("true")
  expect(instance.getAttribute("aria-selected")).toBe("false")
  expect(screen.getByText("the tokens")).toBeTruthy()
  first.unmount()

  await frame(true, "instance")
  expect(screen.getByRole("tab", { name: "Instance" }).getAttribute("aria-selected")).toBe("true")
  expect(screen.getByRole("tab", { name: "Workspace" }).getAttribute("aria-selected")).toBe("false")
  expect(screen.getByText("the tokens")).toBeTruthy()
})

test("the tab list is named for its page, and the tabs lead to that page", async () => {
  await frame(true, "workspace", "Audit log", "/w/$ws/audit")
  const tabs = within(screen.getByRole("tablist", { name: "Audit log scope" }))
  expect(tabs.getByRole("tab", { name: "Workspace" }).getAttribute("href")).toBe("/w/agents/audit")
  expect(tabs.getByRole("tab", { name: "Instance" }).getAttribute("href")).toBe("/w/agents/audit?scope=instance")
  expect(screen.queryByRole("tablist", { name: "Tokens scope" })).toBeNull()
})

test("clicking a tab changes the URL, and the page follows the scope it is given", async () => {
  const user = userEvent.setup()
  const { router } = await frame(true)
  await user.click(screen.getByRole("tab", { name: "Instance" }))
  await waitFor(() => expect(router.state.location.href).toBe("/w/agents/tokens?scope=instance"))
  // The route decides the scope: until it passes "instance" back, Workspace stays selected.
  expect(screen.getByRole("tab", { name: "Workspace" }).getAttribute("aria-selected")).toBe("true")
})

const onPage = () => agent.snapshot().filter((e) => !e.offPage && e.component !== "more")

test("an agent finds the scope tabs by name and sees both scopes", async () => {
  await frame(true)
  // Every control has a name, but for two in the shell around the page: see the test.fails in shell.test.tsx.
  const entries = onPage().filter((e) => !unnamedSidebarScroll(e) && !unnamedSearchDialog(e))
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  const tabs = entries.find((e) => e.label === "Tokens scope")!
  expect(tabs.component).toBe("tabs")
  expect(tabs.actions).toEqual(["setValue"])
  expect(tabs.value).toBe("workspace")
  expect(tabs.items).toEqual([
    { value: "workspace", label: "Workspace" },
    { value: "instance", label: "Instance" },
  ])
})

test("an agent switches the scope tabs to the instance", async () => {
  const { router } = await frame(true)
  expect(router.state.location.href).toBe("/w/agents/tokens")
  const result = await agent.act({ steps: [{ ref: "Tokens scope", action: "setValue", args: ["Instance"] }] })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(router.state.location.href).toBe("/w/agents/tokens?scope=instance"))
})

test("an agent switches the scope tabs back to the workspace, and the scope leaves the URL", async () => {
  const { router } = await frame(true, "instance", "Audit log", "/w/$ws/audit")
  expect(router.state.location.href).toBe("/w/agents/audit?scope=instance")
  const result = await agent.act({ steps: [{ ref: "Audit log scope", action: "setValue", args: ["workspace"] }] })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(router.state.location.href).toBe("/w/agents/audit"))
})

test("an agent picking the scope already on screen goes nowhere", async () => {
  const { router } = await frame(true)
  const result = await agent.act({ steps: [{ ref: "Tokens scope", action: "setValue", args: ["Workspace"] }] })
  expect(result.ok, result.error?.message).toBe(true)
  await new Promise((r) => setTimeout(r, 20))
  expect(router.state.location.href).toBe("/w/agents/tokens")
})

test("the arrow keys switch the scope too", async () => {
  const user = userEvent.setup()
  const { router } = await frame(true)
  screen.getByRole("tab", { name: "Workspace" }).focus()
  await user.keyboard("{ArrowRight}")
  await waitFor(() => expect(router.state.location.href).toBe("/w/agents/tokens?scope=instance"))
})
