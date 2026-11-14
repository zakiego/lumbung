import type { BucketOut, DatabaseOut } from "@lumbung/client"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import type { ReactNode } from "react"
import { afterEach, describe, expect, test, vi } from "vitest"
import { toast } from "@/components/ui/toast"
import { createWorkspace } from "@/fns/workspaces"
import type { Role } from "@/lib/roles"
import type { Person, WorkspaceInfo } from "@/lib/workspace"
import { unnamedSearchDialog, unnamedSidebarScroll } from "@/test/known-bugs"
import { type Place, renderWithRouter } from "@/test/router"
import { lastUiSdkWarning, settle } from "@/test/ui-sdk"
import * as worst from "@/test/worst-case"
import { AppShell, LoneShell, PageHeader, PageTitle } from "./shell"

vi.mock("@/fns/workspaces", () => ({ createWorkspace: vi.fn(async () => ({})) }))
// A toast needs the app's overlay host, which these tests don't mount.
vi.mock("@/components/ui/toast", async (original) => ({ ...(await original<typeof import("@/components/ui/toast")>()), toast: { success: vi.fn(), error: vi.fn() } }))

const agent = createAgent()

afterEach(() => {
  delete document.documentElement.dataset.sidebar
  vi.mocked(createWorkspace).mockClear()
  vi.mocked(toast.success).mockClear()
})

const info = (name: string, id: string, role: Role, isDefault = false): WorkspaceInfo => ({ id, name, default: isDefault, role })

function person(role: Role): Person {
  return { id: "u1", name: "Dev", email: "dev@example.com", owner: role === "owner", role }
}

type Options = {
  databases?: DatabaseOut[] | null
  buckets?: BucketOut[] | null
  workspace?: WorkspaceInfo | null
  workspaces?: WorkspaceInfo[]
  children?: ReactNode
  place?: Place
}

function shell(role: Role, { databases = [], buckets = [], workspace, workspaces, children = <div />, place }: Options = {}) {
  const current = workspace === undefined ? info("default", "ws_aaaaaaaaaaaaaaaaaaaa", role, true) : workspace
  const all = workspaces ?? [...(current ? [current] : []), info("agents", "ws_bbbbbbbbbbbbbbbbbbbb", role), info(worst.LONG_WORKSPACE, "ws_cccccccccccccccccccc", role)]
  return renderWithRouter(
    <AppShell viewer={person(role)} workspace={current} workspaces={all} databases={databases} buckets={buckets}>
      {children}
    </AppShell>,
    current,
    place,
  )
}

// The desktop sidebar. The drawer holds a second copy for small screens.
function sidebar() {
  return within(document.querySelector<HTMLElement>("aside[data-sidebar-rail]")!)
}

function rail() {
  return within(screen.getAllByRole("navigation", { name: "Main" })[0])
}

const hrefs = (scope: ReturnType<typeof rail>) => scope.getAllByRole("link").map((a: HTMLElement) => a.getAttribute("href"))

test("an empty workspace offers to create a database and a bucket from the sidebar", async () => {
  await shell("owner")
  expect(rail().getByRole("button", { name: "New database" })).toBeTruthy()
  expect(rail().getByRole("button", { name: "New bucket" })).toBeTruthy()
})

test("an admin can create databases and buckets, a member neither", async () => {
  const admin = await shell("admin")
  expect(rail().getByRole("button", { name: "New database" })).toBeTruthy()
  expect(rail().getByRole("button", { name: "New bucket" })).toBeTruthy()
  admin.unmount()

  await shell("member")
  expect(rail().queryByRole("heading", { name: "Databases" })).toBeNull()
  expect(rail().queryByRole("button", { name: "New database" })).toBeNull()
  expect(rail().queryByRole("heading", { name: "Buckets" })).toBeNull()
  expect(rail().queryByRole("button", { name: "New bucket" })).toBeNull()
})

test("a read-only person sees no empty groups they can't fill", async () => {
  await shell("readonly")
  expect(rail().queryByRole("heading", { name: "Databases" })).toBeNull()
  expect(rail().queryByRole("heading", { name: "Buckets" })).toBeNull()
  expect(rail().queryByRole("button", { name: "New database" })).toBeNull()
  expect(rail().queryByRole("button", { name: "New bucket" })).toBeNull()
})

test("a failed load still says so, whatever the role", async () => {
  await shell("readonly", { databases: null, buckets: null })
  expect(rail().getByText("Can't load databases")).toBeTruthy()
  expect(rail().getByText("Can't load buckets")).toBeTruthy()
})

test("a member sees the databases and buckets that exist, with no way to add one", async () => {
  const bucket: BucketOut = { id: "bk_1", workspace_id: "ws1", name: "photos", public_read: false, cors_origins: [], size_bytes: 0, object_count: 0, created_at: "2026-11-14T08:00:00Z", updated_at: "2026-11-14T08:00:00Z" }
  await shell("member", { databases: worst.databases(3), buckets: [bucket] })
  expect(rail().getByRole("heading", { name: "Databases" })).toBeTruthy()
  expect(rail().getByRole("link", { name: "shop" }).getAttribute("href")).toBe("/w/default/db/shop")
  expect(rail().getByRole("link", { name: worst.LONG_DB }).getAttribute("href")).toBe(`/w/default/db/${worst.LONG_DB}`)
  expect(rail().getByRole("link", { name: "photos" }).getAttribute("href")).toBe("/w/default/buckets/photos")
  expect(rail().getByRole("link", { name: "Buckets" }).getAttribute("href")).toBe("/w/default/buckets")
  expect(rail().queryByRole("button", { name: "New database" })).toBeNull()
  expect(rail().queryByRole("button", { name: "New bucket" })).toBeNull()
})

test("every link inside a workspace is under its name", async () => {
  const agents = info("agents", "ws_bbbbbbbbbbbbbbbbbbbb", "owner")
  await shell("owner", { workspace: agents, workspaces: [info("default", "ws_aaaaaaaaaaaaaaaaaaaa", "owner", true), agents], databases: worst.databases(3) })
  expect(rail().getByRole("link", { name: "Overview" }).getAttribute("href")).toBe("/w/agents")
  expect(rail().getByRole("link", { name: "shop" }).getAttribute("href")).toBe("/w/agents/db/shop")
  const workspace = within(rail().getByRole("heading", { name: "Workspace" }).parentElement!)
  expect(hrefs(workspace)).toEqual(["/w/agents/tokens", "/w/agents/audit", "/w/agents/trash", "/w/agents/settings"])
  // Only the instance's own pages sit outside a workspace.
  const outside = hrefs(rail()).filter((h) => !h?.startsWith("/w/agents"))
  expect(outside).toEqual(["/members", "/settings"])
})

test("the two Settings links have names of their own", async () => {
  await shell("owner")
  const workspace = rail().getByRole("link", { name: "Workspace settings" })
  const instance = rail().getByRole("link", { name: "Instance settings" })
  expect(workspace.getAttribute("href")).toBe("/w/default/settings")
  expect(instance.getAttribute("href")).toBe("/settings")
  expect(rail().queryByRole("link", { name: "Settings" })).toBeNull()
  // Both still read "Settings" under their group's heading.
  expect(workspace.textContent).toBe("Settings")
  expect(instance.textContent).toBe("Settings")
})

test("the switcher names the workspace on screen, lists the others and marks the current one", async () => {
  const user = userEvent.setup()
  await shell("owner")
  const trigger = sidebar().getByRole("button", { name: "Workspace default. Switch workspace" })
  expect(trigger.getAttribute("aria-haspopup")).toBe("menu")
  expect(trigger.getAttribute("title")).toBe("default")
  const name = within(trigger).getByText("default")
  expect(name.hasAttribute("data-truncate")).toBe(true)
  expect(name.className).toContain("font-mono")

  await user.click(trigger)
  const menu = await screen.findByRole("menu")
  const items = within(menu).getAllByRole("menuitemradio")
  expect(items.map((i) => i.textContent)).toEqual(["default", "agents", worst.LONG_WORKSPACE])
  expect(items.map((i) => i.getAttribute("aria-checked"))).toEqual(["true", "false", "false"])
  expect(within(menu).getByText("Workspaces")).toBeTruthy()
})

test("a long list of workspaces scrolls inside the menu, and a long name wraps", async () => {
  const user = userEvent.setup()
  const many = Array.from({ length: 60 }, (_, i) => info(i === 1 ? worst.LONG_WORKSPACE : `team-${i}`, `ws_${String(i).padStart(20, "0")}`, "member", i === 0))
  await shell("member", { workspace: many[0], workspaces: many })
  await user.click(sidebar().getByRole("button", { name: "Workspace team-0. Switch workspace" }))
  const menu = await screen.findByRole("menu")
  const items = within(menu).getAllByRole("menuitemradio")
  expect(items).toHaveLength(60)
  // The menu is as tall as there is room for, and the list of workspaces is the part that scrolls.
  for (const cls of ["max-h-(--available-height)", "flex", "flex-col"]) expect(menu.className, cls).toContain(cls)
  expect(menu.className).not.toContain("overflow-y-auto")
  const scrolls = items[0]!.closest<HTMLElement>(".overflow-y-auto")!
  expect(scrolls).not.toBe(menu)
  expect(menu.contains(scrolls)).toBe(true)
  expect(scrolls.className).toContain("min-h-0")
  for (const item of items) expect(scrolls.contains(item)).toBe(true)
  expect(within(scrolls).getByText("Workspaces")).toBeTruthy()
  const long = within(menu).getByText(worst.LONG_WORKSPACE)
  expect(long.className).toContain("[overflow-wrap:anywhere]")
  expect(long.className).not.toContain("truncate")
})

test("New workspace stays in reach under a long list: it is outside the part that scrolls", async () => {
  const user = userEvent.setup()
  const many = Array.from({ length: 60 }, (_, i) => info(`team-${i}`, `ws_${String(i).padStart(20, "0")}`, "owner", i === 0))
  await shell("owner", { workspace: many[0], workspaces: many })
  await user.click(sidebar().getByRole("button", { name: "Workspace team-0. Switch workspace" }))
  const menu = await screen.findByRole("menu")
  const scrolls = within(menu).getAllByRole("menuitemradio")[0]!.closest<HTMLElement>(".overflow-y-auto")!
  const create = within(menu).getByRole("menuitem", { name: "New workspace" })
  expect(scrolls.contains(create)).toBe(false)
  expect(scrolls.compareDocumentPosition(create) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(create.closest(".overflow-y-auto")).toBeNull()
})

test("picking another workspace opens it, picking the current one goes nowhere", async () => {
  const user = userEvent.setup()
  const same = await shell("admin")
  await user.click(sidebar().getByRole("button", { name: "Workspace default. Switch workspace" }))
  await user.click(await screen.findByRole("menuitemradio", { name: "default" }))
  await new Promise((r) => setTimeout(r, 20))
  expect(same.router.state.location.pathname).toBe("/")
  same.unmount()

  const { router } = await shell("admin")
  await user.click(sidebar().getByRole("button", { name: "Workspace default. Switch workspace" }))
  await user.click(await screen.findByRole("menuitemradio", { name: "agents" }))
  await waitFor(() => expect(router.state.location.pathname).toBe("/w/agents"))
})

test("New workspace is there for an owner and not for an admin", async () => {
  const user = userEvent.setup()
  const owner = await shell("owner")
  await user.click(sidebar().getByRole("button", { name: "Workspace default. Switch workspace" }))
  await user.click(await screen.findByRole("menuitem", { name: "New workspace" }))
  const dialog = await screen.findByRole("dialog", { name: "Create workspace" })
  expect(within(dialog).getByLabelText("Name")).toBeTruthy()
  owner.unmount()

  await shell("admin")
  await user.click(sidebar().getByRole("button", { name: "Workspace default. Switch workspace" }))
  expect(await screen.findAllByRole("menuitemradio")).toHaveLength(3)
  expect(screen.queryByRole("menuitem", { name: "New workspace" })).toBeNull()
})

test("an owner creates a workspace and lands in it, and the dialog is empty the next time", async () => {
  const user = userEvent.setup()
  const { router } = await shell("owner")
  const open = async () => {
    await user.click(sidebar().getByRole("button", { name: "Workspace default. Switch workspace" }))
    await user.click(await screen.findByRole("menuitem", { name: "New workspace" }))
    return within(await screen.findByRole("dialog", { name: "Create workspace" }))
  }
  const first = await open()
  await user.type(first.getByLabelText("Name"), "half-typed")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "Create workspace" })).toBeNull())
  expect(createWorkspace).not.toHaveBeenCalled()

  const second = await open()
  expect((second.getByLabelText("Name") as HTMLInputElement).value).toBe("")
  await user.type(second.getByLabelText("Name"), "team-x")
  await user.click(second.getByRole("button", { name: "Create" }))
  await waitFor(() => expect(router.state.location.pathname).toBe("/w/team-x"))
  expect(vi.mocked(createWorkspace).mock.calls).toEqual([[{ data: { name: "team-x" } }]])
  expect(toast.success).toHaveBeenCalledWith("Created team-x")
  await waitFor(() => expect(screen.queryByRole("dialog", { name: "Create workspace" })).toBeNull())
})

test("a refused workspace name stays in the dialog, and nothing is opened", async () => {
  const user = userEvent.setup()
  vi.mocked(createWorkspace).mockRejectedValueOnce(new Error("A workspace named agents already exists."))
  const { router } = await shell("owner")
  await user.click(sidebar().getByRole("button", { name: "Workspace default. Switch workspace" }))
  await user.click(await screen.findByRole("menuitem", { name: "New workspace" }))
  const dialog = within(await screen.findByRole("dialog", { name: "Create workspace" }))
  await user.type(dialog.getByLabelText("Name"), "agents")
  await user.click(dialog.getByRole("button", { name: "Create" }))
  expect(await dialog.findByText("A workspace named agents already exists.")).toBeTruthy()
  expect(router.state.location.pathname).toBe("/")
  expect(toast.success).not.toHaveBeenCalled()
})

test("the Instance group is only there for an owner", async () => {
  const owner = await shell("owner")
  const group = within(rail().getByRole("heading", { name: "Instance" }).parentElement!)
  expect(group.getByRole("link", { name: "Members" }).getAttribute("href")).toBe("/members")
  expect(group.getByRole("link", { name: "Instance settings" }).getAttribute("href")).toBe("/settings")
  owner.unmount()

  for (const role of ["admin", "member", "readonly"] as const) {
    const { unmount } = await shell(role)
    expect(rail().queryByRole("heading", { name: "Instance" }), role).toBeNull()
    expect(rail().queryByRole("link", { name: "Members" }), role).toBeNull()
    expect(hrefs(rail()), role).not.toContain("/members")
    expect(hrefs(rail()), role).not.toContain("/settings")
    unmount()
  }
})

test("the Workspace group is there for an admin and not for a member or a read-only person", async () => {
  const admin = await shell("admin")
  const group = within(rail().getByRole("heading", { name: "Workspace" }).parentElement!)
  expect(group.getAllByRole("link").map((a) => a.textContent)).toEqual(["Tokens", "Audit log", "Trash", "Settings"])
  expect(hrefs(group)).toEqual(["/w/default/tokens", "/w/default/audit", "/w/default/trash", "/w/default/settings"])
  admin.unmount()

  for (const role of ["member", "readonly"] as const) {
    const { unmount } = await shell(role)
    expect(rail().queryByRole("heading", { name: "Workspace" }), role).toBeNull()
    for (const name of ["Tokens", "Audit log", "Trash", "Workspace settings"]) expect(rail().queryByRole("link", { name }), `${role} ${name}`).toBeNull()
    // Overview is all that is left of an empty workspace for them.
    expect(hrefs(rail()), role).toEqual(["/w/default"])
    unmount()
  }
})

test("without a workspace there is no workspace nav and nothing to switch", async () => {
  const none = await shell("member", { workspace: null, workspaces: [] })
  expect(sidebar().queryByRole("button", { name: /Switch workspace/ })).toBeNull()
  expect(sidebar().getByText("Lumbung")).toBeTruthy()
  expect(rail().queryAllByRole("link")).toEqual([])
  expect(rail().queryByRole("heading")).toBeNull()
  expect(rail().queryByRole("button", { name: "New database" })).toBeNull()
  expect(screen.queryByRole("menu")).toBeNull()
  // The account menu is still there, so they can sign out.
  expect(sidebar().getByRole("button", { name: /Dev/ })).toBeTruthy()
  none.unmount()

  // An owner with no workspace still reaches the instance's own pages.
  await shell("owner", { workspace: null, workspaces: [] })
  expect(sidebar().queryByRole("button", { name: /Switch workspace/ })).toBeNull()
  expect(hrefs(rail())).toEqual(["/members", "/settings"])
})

test("a 63 character workspace name stays readable in the switcher", async () => {
  const user = userEvent.setup()
  const long = info(worst.LONG_WORKSPACE, "ws_cccccccccccccccccccc", "member")
  await shell("member", { workspace: long, workspaces: [long, info("agents", "ws_bbbbbbbbbbbbbbbbbbbb", "member")] })
  const trigger = sidebar().getByRole("button", { name: `Workspace ${worst.LONG_WORKSPACE}. Switch workspace` })
  expect(trigger.getAttribute("title")).toBe(worst.LONG_WORKSPACE)
  expect(rail().getByRole("link", { name: "Overview" }).getAttribute("href")).toBe(`/w/${worst.LONG_WORKSPACE}`)
  await user.click(trigger)
  expect((await screen.findByRole("menuitemradio", { name: worst.LONG_WORKSPACE })).getAttribute("aria-checked")).toBe("true")
})

describe("someone with one workspace and no way to make another", () => {
  function row(name: string) {
    const text = sidebar().getByText(name, { selector: "[data-truncate]" })
    return { text, row: text.parentElement! }
  }

  test.each<Role>(["admin", "member", "readonly"])("%s: the name is a link to the workspace's overview, with no menu to open", async (role) => {
    const user = userEvent.setup()
    const only = info("agents", "ws_bbbbbbbbbbbbbbbbbbbb", role)
    const { router } = await shell(role, { workspace: only, workspaces: [only] })
    expect(sidebar().queryByRole("button", { name: /Switch workspace/ })).toBeNull()
    expect(sidebar().queryByRole("button", { name: /agents/ })).toBeNull()
    const { text, row: el } = row("agents")
    expect(text.className).toContain("font-mono")
    // A link named by the workspace's name, and nothing that opens a popup.
    expect(el).toBe(sidebar().getByRole("link", { name: "agents" }))
    expect(el.getAttribute("href")).toBe("/w/agents")
    expect(el.hasAttribute("aria-haspopup")).toBe(false)
    expect(el.hasAttribute("aria-label")).toBe(false)
    await user.click(el)
    await waitFor(() => expect(router.state.location.pathname).toBe("/w/agents"))
    expect(screen.queryByRole("menu")).toBeNull()
    expect(screen.queryByRole("menuitemradio")).toBeNull()
    expect(screen.queryByRole("menuitem", { name: "New workspace" })).toBeNull()
    // The rest of the sidebar is as it was.
    expect(rail().getByRole("link", { name: "Overview" }).getAttribute("href")).toBe("/w/agents")
  })

  test("a 63 character name is cut off in the row and read in full from its tip, by keyboard too", async () => {
    const long = info(worst.LONG_WORKSPACE, "ws_cccccccccccccccccccc", "member")
    await shell("member", { workspace: long, workspaces: [long] })
    const { text, row: el } = row(worst.LONG_WORKSPACE)
    expect(text.className).toContain("truncate")
    expect(el).toBe(sidebar().getByRole("link", { name: worst.LONG_WORKSPACE }))
    expect(el.getAttribute("href")).toBe(`/w/${worst.LONG_WORKSPACE}`)
    // A link is a stop for the keyboard as it is, and the tip describes it.
    expect(el.hasAttribute("title")).toBe(false)
    expect(el.hasAttribute("tabindex")).toBe(false)
    const tip = document.getElementById(el.getAttribute("aria-describedby")!)!
    expect(tip.getAttribute("role")).toBe("tooltip")
    expect(tip.textContent).toBe(worst.LONG_WORKSPACE)
  })

  test("an owner with one workspace keeps the menu, to make another", async () => {
    const user = userEvent.setup()
    const only = info("default", "ws_aaaaaaaaaaaaaaaaaaaa", "owner", true)
    await shell("owner", { workspace: only, workspaces: [only] })
    await user.click(sidebar().getByRole("button", { name: "Workspace default. Switch workspace" }))
    expect(await screen.findAllByRole("menuitemradio")).toHaveLength(1)
    expect(screen.getByRole("menuitem", { name: "New workspace" })).toBeTruthy()
  })

  test("a member with two workspaces gets the menu", async () => {
    const two = [info("agents", "ws_bbbbbbbbbbbbbbbbbbbb", "member"), info("staging", "ws_cccccccccccccccccccc", "readonly")]
    await shell("member", { workspace: two[0], workspaces: two })
    expect(sidebar().getByRole("button", { name: "Workspace agents. Switch workspace" })).toBeTruthy()
  })

  test("the link raises no ui-sdk warning, and an agent sees a named tip and no menu in its place", async () => {
    const before = lastUiSdkWarning()
    const only = info("agents", "ws_bbbbbbbbbbbbbbbbbbbb", "member")
    await shell("member", { workspace: only, workspaces: [only] })
    await settle()
    expect(lastUiSdkWarning()).toBe(before)
    const entries = agent.snapshot().filter((e) => !e.offPage)
    expect(entries.filter((e) => e.label === "agents").map((e) => e.component)).toEqual(["tooltip"])
    expect(entries.filter((e) => e.label?.includes("Switch workspace"))).toEqual([])
    expect(entries.filter((e) => e.component === "menu").map((e) => e.label)).toEqual([expect.stringContaining("Dev")])
  })
})

describe("the page header", () => {
  const agents = info("agents", "ws_bbbbbbbbbbbbbbbbbbbb", "admin")
  const page = (title: string) => (
    <PageHeader actions={<span>an action</span>}>
      <PageTitle>{title}</PageTitle>
    </PageHeader>
  )
  const header = () => within(document.querySelector<HTMLElement>("header")!)

  test("inside a workspace, the workspace's name comes before the title", async () => {
    await shell("admin", { workspace: agents, children: page("Tokens"), place: { route: "/w/$ws/tokens", at: "/w/agents/tokens" } })
    const crumb = header().getByRole("link", { name: "agents" })
    expect(crumb.getAttribute("href")).toBe("/w/agents")
    expect(crumb.className).toContain("font-mono")
    expect(crumb.className).toContain("truncate")
    // The full name is in its tip, not a title.
    expect(crumb.hasAttribute("title")).toBe(false)
    expect(document.getElementById(crumb.getAttribute("aria-describedby")!)?.textContent).toBe("agents")
    const slash = header().getByText("/")
    expect(slash.getAttribute("aria-hidden")).toBe("true")
    const title = header().getByRole("heading", { level: 1, name: "Tokens" })
    expect(crumb.compareDocumentPosition(slash) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(slash.compareDocumentPosition(title) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(header().getByText("an action")).toBeTruthy()
  })

  test("when there isn't room for both, the crumb gives way before the title", async () => {
    await shell("admin", { workspace: agents, children: page("Tokens"), place: { route: "/w/$ws/tokens", at: "/w/agents/tokens" } })
    const crumb = header().getByRole("link", { name: "agents" }).parentElement!
    const title = header().getByRole("heading", { level: 1, name: "Tokens" }).parentElement!
    // The crumb starts at no width and takes only what the title leaves: it can shrink to nothing, the slash can't.
    for (const cls of ["min-w-0", "basis-0", "max-w-fit", "overflow-hidden"]) expect(crumb.className, cls).toContain(cls)
    expect(crumb.className).not.toContain("shrink-0")
    expect(header().getByRole("link", { name: "agents" }).className).toContain("min-w-0")
    expect(header().getByText("/").className).toContain("shrink-0")
    // The title takes its own width first.
    expect(title.className).toContain("flex-auto")
    expect(title.className).not.toContain("basis-0")
  })

  test("the crumb goes to the workspace's overview", async () => {
    const user = userEvent.setup()
    const { router } = await shell("admin", { workspace: agents, children: page("Tokens"), place: { route: "/w/$ws/tokens", at: "/w/agents/tokens" } })
    expect(router.state.location.pathname).toBe("/w/agents/tokens")
    await user.click(header().getByRole("link", { name: "agents" }))
    await waitFor(() => expect(router.state.location.pathname).toBe("/w/agents"))
  })

  test("on a desktop the crumb only shows while the sidebar is collapsed, and the switcher keeps its title either way", async () => {
    const user = userEvent.setup()
    await shell("admin", { workspace: agents, children: page("Tokens"), place: { route: "/w/$ws/tokens", at: "/w/agents/tokens" } })
    const crumb = () => header().getByRole("link", { name: "agents" }).parentElement!
    const trigger = () => sidebar().getByRole("button", { name: "Workspace agents. Switch workspace" })
    const toggle = header().getByRole("button", { name: "Toggle sidebar" })
    expect(toggle.getAttribute("aria-expanded")).toBe("true")
    expect(crumb().className).toContain("md:hidden")
    expect(trigger().getAttribute("title")).toBe("agents")

    await user.click(toggle)
    expect(toggle.getAttribute("aria-expanded")).toBe("false")
    expect(crumb().className).not.toContain("md:hidden")
    // Collapsed, the name is only there for screen readers, so the title is what a mouse reads.
    expect(trigger().getAttribute("title")).toBe("agents")
    expect(trigger().hasAttribute("aria-describedby")).toBe(false)

    await user.click(toggle)
    expect(crumb().className).toContain("md:hidden")
    expect(trigger().getAttribute("title")).toBe("agents")
  })

  test("a long workspace name in the crumb is cut off and read in full from its tip", async () => {
    const long = info(worst.LONG_WORKSPACE, "ws_cccccccccccccccccccc", "member")
    await shell("member", { workspace: long, workspaces: [long, agents], children: page("Overview"), place: { route: "/w/$ws", at: `/w/${worst.LONG_WORKSPACE}` } })
    const crumb = header().getByRole("link", { name: worst.LONG_WORKSPACE })
    expect(crumb.className).toContain("truncate")
    const tip = document.getElementById(crumb.getAttribute("aria-describedby")!)!
    expect(tip.getAttribute("role")).toBe("tooltip")
    expect(tip.textContent).toBe(worst.LONG_WORKSPACE)
  })

  test("the crumb raises no ui-sdk warning", async () => {
    const before = lastUiSdkWarning()
    await shell("admin", { workspace: agents, children: page("Tokens"), place: { route: "/w/$ws/tokens", at: "/w/agents/tokens" } })
    await settle()
    expect(lastUiSdkWarning()).toBe(before)
  })

  test("a page outside a workspace has no crumb, though the sidebar still shows the last workspace", async () => {
    await shell("owner", { workspace: info("agents", "ws_bbbbbbbbbbbbbbbbbbbb", "owner"), children: page("Members"), place: { route: "/members", at: "/members" } })
    expect(header().getByRole("heading", { level: 1, name: "Members" })).toBeTruthy()
    expect(header().queryByText("agents")).toBeNull()
    expect(header().queryByRole("link")).toBeNull()
    expect(header().queryByText("/")).toBeNull()
    expect(sidebar().getByRole("button", { name: "Workspace agents. Switch workspace" })).toBeTruthy()
  })

  test("with no workspace on screen there is no crumb, whatever the URL", async () => {
    await shell("member", { workspace: null, workspaces: [], children: page("Lumbung"), place: { route: "/w/$ws", at: "/w/nope" } })
    expect(header().getByRole("heading", { level: 1, name: "Lumbung" })).toBeTruthy()
    expect(header().queryByText("nope")).toBeNull()
    expect(header().queryByRole("link")).toBeNull()
    expect(header().queryByText("/")).toBeNull()
  })
})

describe("LoneShell", () => {
  const account = { id: "u1", name: "Dev", email: "dev@example.com", owner: false }

  test("shows the title, the body and the account menu, with no workspace nav and nothing to switch", async () => {
    const user = userEvent.setup()
    await renderWithRouter(
      <LoneShell account={account} title="Lumbung">
        <p>No workspace yet</p>
      </LoneShell>,
      null,
    )
    expect(screen.getByRole("heading", { level: 1, name: "Lumbung" })).toBeTruthy()
    expect(within(screen.getByRole("main")).getByText("No workspace yet")).toBeTruthy()
    expect(sidebar().getByText("Lumbung")).toBeTruthy()
    expect(sidebar().queryByRole("button", { name: /Switch workspace/ })).toBeNull()
    expect(rail().queryAllByRole("link")).toEqual([])
    expect(rail().queryByRole("heading")).toBeNull()
    expect(rail().queryByRole("button")).toBeNull()

    // Nobody without a workspace has a role to show.
    const menu = sidebar().getByRole("button", { name: /Dev/ })
    for (const label of ["Owner", "Admin", "Member", "Read-only"]) expect(within(menu).queryByText(label)).toBeNull()
    await user.click(menu)
    expect(await screen.findByRole("menuitem", { name: "Sign out" })).toBeTruthy()
    expect(screen.getByRole("menuitem", { name: "Account" })).toBeTruthy()
    expect(screen.queryByRole("menuitemradio", { name: /default|agents/ })).toBeNull()
    expect(screen.queryByRole("menuitem", { name: "New workspace" })).toBeNull()
  })

  test("stays without a workspace on a workspace URL: no crumb and no nav for the name in it", async () => {
    await renderWithRouter(
      <LoneShell account={account} title="That workspace doesn't exist">
        <p>gone</p>
      </LoneShell>,
      null,
      { route: "/w/$ws", at: "/w/secret" },
    )
    expect(screen.getByRole("heading", { level: 1, name: "That workspace doesn't exist" })).toBeTruthy()
    expect(screen.queryByText("secret")).toBeNull()
    expect(rail().queryAllByRole("link")).toEqual([])
  })

  test("search still offers the workspaces the person may use, when it is given them", async () => {
    const user = userEvent.setup()
    const mine = [info("default", "ws_aaaaaaaaaaaaaaaaaaaa", "member", true), info("agents", "ws_bbbbbbbbbbbbbbbbbbbb", "readonly")]
    const { router } = await renderWithRouter(
      <LoneShell account={account} title="That workspace doesn't exist" workspaces={mine}>
        <p>gone</p>
      </LoneShell>,
      null,
      { route: "/w/$ws", at: "/w/nope" },
    )
    // The sidebar is still the one for nobody's workspace.
    expect(sidebar().queryByRole("button", { name: /Switch workspace/ })).toBeNull()
    expect(sidebar().getByText("Lumbung")).toBeTruthy()
    expect(rail().queryAllByRole("link")).toEqual([])

    await user.click(sidebar().getByRole("button", { name: /Search/ }))
    await screen.findByRole("combobox", { name: "Search" })
    expect(screen.getByRole("option", { name: "default" })).toBeTruthy()
    expect(screen.queryByRole("option", { name: "Overview" })).toBeNull()
    await user.click(screen.getByRole("option", { name: "agents" }))
    await waitFor(() => expect(router.state.location.pathname).toBe("/w/agents"))
  })

  test("without them, search offers no workspace", async () => {
    const user = userEvent.setup()
    await renderWithRouter(
      <LoneShell account={account} title="Lumbung">
        <p>No workspace yet</p>
      </LoneShell>,
      null,
    )
    await user.click(sidebar().getByRole("button", { name: /Search/ }))
    await screen.findByRole("combobox", { name: "Search" })
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["Account", "Light theme", "Dark theme", "System theme"])
  })

  test("an owner keeps the instance's pages and is shown as Owner", async () => {
    await renderWithRouter(
      <LoneShell account={{ ...account, owner: true }} title="Couldn't load this workspace">
        <p>engine down</p>
      </LoneShell>,
      null,
    )
    expect(hrefs(rail())).toEqual(["/members", "/settings"])
    expect(within(sidebar().getByRole("button", { name: /Dev/ })).getByText("Owner")).toBeTruthy()
    expect(sidebar().queryByRole("button", { name: /Switch workspace/ })).toBeNull()
  })
})

describe("an agent", () => {
  const onPage = () => agent.snapshot().filter((e) => !e.offPage && e.component !== "more")

  // Every control has a name, but for the sidebar's scroll area: see the test.fails below.
  function named() {
    const entries = onPage().filter((e) => !unnamedSidebarScroll(e))
    for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
    return entries
  }

  const SWITCHER = "Workspace default. Switch workspace"
  const switcher = () => onPage().find((e) => e.label === SWITCHER)!

  test("switches workspace through the switcher menu", async () => {
    const { router } = await shell("admin")
    const labels = named().map((e) => e.label)
    for (const label of [SWITCHER, "Overview", "Tokens", "Audit log", "Trash", "Workspace settings", "New database", "New bucket"]) expect(labels, label).toContain(label)

    const result = await agent.act({ steps: [{ ref: SWITCHER, action: "press", args: ["agents"] }] })
    expect(result.ok, result.error?.message).toBe(true)
    await waitFor(() => expect(router.state.location.pathname).toBe("/w/agents"))
    // The open menu names every workspace the person may use, and nothing an admin can't do.
    expect(switcher().buttons).toEqual(["default", "agents", worst.LONG_WORKSPACE])
    named()
  })

  test("pressing the workspace already on screen goes nowhere", async () => {
    const { router } = await shell("member")
    const result = await agent.act({ steps: [{ ref: SWITCHER, action: "press", args: ["default"] }] })
    expect(result.ok, result.error?.message).toBe(true)
    await settle()
    expect(router.state.location.pathname).toBe("/")
  })

  test("creates a workspace through New workspace", async () => {
    const { router } = await shell("owner")
    named()
    const opened = await agent.act({ steps: [{ ref: SWITCHER, action: "press", args: ["New workspace"] }] })
    expect(opened.ok, opened.error?.message).toBe(true)
    await screen.findByRole("dialog", { name: "Create workspace" })
    const labels = named().map((e) => e.label)
    for (const label of ["Create workspace", "Name", "Create"]) expect(labels, label).toContain(label)

    const result = await agent.act({
      steps: [
        { ref: "Name", action: "setValue", args: ["team-x"] },
        { ref: "Create workspace", action: "press", args: ["Create"] },
      ],
    })
    expect(result.ok, result.error?.message).toBe(true)
    await waitFor(() => expect(router.state.location.pathname).toBe("/w/team-x"))
    expect(vi.mocked(createWorkspace).mock.calls).toEqual([[{ data: { name: "team-x" } }]])
    expect(toast.success).toHaveBeenCalledWith("Created team-x")
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Create workspace" })).toBeNull())
  })

  test("can't create a workspace as an admin: the menu has no such button", async () => {
    await shell("admin")
    const result = await agent.act({ steps: [{ ref: SWITCHER, action: "press", args: ["New workspace"] }] })
    expect(result.ok).toBe(false)
    expect(createWorkspace).not.toHaveBeenCalled()
    expect(screen.queryByRole("dialog", { name: "Create workspace" })).toBeNull()
  })

  test("presses the switcher by its name: one component has it, the menu", async () => {
    const { router } = await shell("admin")
    const withName = onPage().filter((e) => e.label?.includes("Switch workspace"))
    expect(withName.map((e) => e.component)).toEqual(["menu"])
    // A few of its words are enough, as a person would say it.
    const result = await agent.act({ steps: [{ ref: "Switch workspace", action: "press", args: ["agents"] }] })
    expect(result.ok, result.error?.message).toBe(true)
    await waitFor(() => expect(router.state.location.pathname).toBe("/w/agents"))
  })

  test.fails("finds every component in the sidebar by name (source bug: the sidebar's ScrollArea at shell.tsx:126 has no label)", async () => {
    await shell("owner")
    for (const entry of onPage().filter((e) => e.component === "scroll-area")) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  })

  test.fails("finds the search dialog by name on a page with a header (source bug: with the header's second trigger the closed dialog has no name)", async () => {
    await shell("owner", {
      children: (
        <PageHeader>
          <PageTitle>Tokens</PageTitle>
        </PageHeader>
      ),
    })
    expect(onPage().filter(unnamedSearchDialog)).toEqual([])
  })
})
