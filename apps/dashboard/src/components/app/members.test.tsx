import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { describe, expect, test, vi } from "vitest"
import type { Member, MemberAccess } from "@/server/users"
import { lastUiSdkWarning, settle } from "@/test/ui-sdk"
import { InviteDialog, MemberTable, accessSummary } from "./members"

const at = "2026-11-14T08:00:00Z"

const DEFAULT = { id: "ws_aaaaaaaaaaaaaaaaaaaa", name: "default" }
const AGENTS = { id: "ws_bbbbbbbbbbbbbbbbbbbb", name: "agents" }
const STAGING = { id: "ws_cccccccccccccccccccc", name: "staging" }
const workspaces = [DEFAULT, AGENTS, STAGING]
const GONE = "ws_zzzzzzzzzzzzzzzzzzzz"

const ann: Member = { id: "me", name: "Ann", email: "ann@example.com", owner: true, grants: [], createdAt: at }
const bo: Member = { id: "m1", name: "Bo", email: "bo@example.com", owner: false, grants: [{ workspace: DEFAULT.id, role: "readonly" }], createdAt: at }
const cy: Member = {
  id: "m2",
  name: "",
  email: "cy@example.com",
  owner: false,
  grants: [
    { workspace: STAGING.id, role: "admin" },
    { workspace: GONE, role: "admin" },
    { workspace: AGENTS.id, role: "member" },
  ],
  createdAt: at,
}
const dee: Member = { id: "m3", name: "Dee", email: "dee@example.com", owner: false, grants: [], createdAt: at }
const eve: Member = { id: "m4", name: "Eve", email: "eve@example.com", owner: true, grants: [], createdAt: at }

describe("accessSummary", () => {
  test("an owner is an owner, whatever grants ride along", () => {
    expect(accessSummary({ owner: true, grants: [] }, workspaces)).toBe("Owner")
    expect(accessSummary({ owner: true, grants: [{ workspace: AGENTS.id, role: "readonly" }] }, workspaces)).toBe("Owner")
    expect(accessSummary({ owner: true, grants: [] }, [])).toBe("Owner")
  })

  test("a role in a deleted workspace is shown, marked, because a restore gives it back", () => {
    const deleted = [{ id: "ws_dddddddddddddddddddd", name: "old-experiment" }]
    const ben = { owner: false, grants: [{ workspace: deleted[0]!.id, role: "admin" as const }] }
    expect(accessSummary(ben, workspaces)).toBe("No workspace")
    expect(accessSummary(ben, workspaces, deleted)).toBe("old-experiment (deleted) · Admin")
    expect(accessSummary({ owner: false, grants: [{ workspace: DEFAULT.id, role: "member" }, ...ben.grants] }, workspaces, deleted)).toBe(
      "default · Member, old-experiment (deleted) · Admin",
    )
  })

  test("several grants read as workspace and role, in the order they were given", () => {
    expect(accessSummary({ owner: false, grants: [{ workspace: DEFAULT.id, role: "member" }] }, workspaces)).toBe("default · Member")
    expect(
      accessSummary(
        {
          owner: false,
          grants: [
            { workspace: STAGING.id, role: "admin" },
            { workspace: DEFAULT.id, role: "readonly" },
            { workspace: AGENTS.id, role: "member" },
          ],
        },
        workspaces,
      ),
    ).toBe("staging · Admin, default · Read-only, agents · Member")
  })

  test("a grant in a workspace that isn't listed is left out", () => {
    expect(accessSummary(cy, workspaces)).toBe("staging · Admin, agents · Member")
    expect(accessSummary(cy, workspaces)).not.toContain(GONE)
  })

  test("no grants, or only grants in workspaces that are gone, read as no workspace", () => {
    expect(accessSummary({ owner: false, grants: [] }, workspaces)).toBe("No workspace")
    expect(accessSummary({ owner: false, grants: [{ workspace: GONE, role: "admin" }] }, workspaces)).toBe("No workspace")
    expect(accessSummary(bo, [])).toBe("No workspace")
  })
})

function table(members: Member[], onAccess = vi.fn(async (_m: Member, _a: MemberAccess) => {})) {
  render(<MemberTable members={members} me="me" workspaces={workspaces} onAccess={onAccess} onRemove={async () => {}} />)
  return onAccess
}

const agent = createAgent()

// A summary and the title that reads it in full, as { text, title, mono }.
function summaryOf(el: Element) {
  return { text: el.textContent, title: el.getAttribute("title"), mono: [...el.querySelectorAll(".font-mono")].map((m) => m.textContent) }
}

function named() {
  const entries = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  return entries.map((e) => e.label)
}

async function pick(user: ReturnType<typeof userEvent.setup>, select: string, option: string) {
  await user.click(screen.getByRole("combobox", { name: select }))
  await user.click(await screen.findByRole("option", { name: option }))
}

describe("the member table", () => {
  test("shows each person's access in one line, read in full from its title, with workspace names in mono", () => {
    table([ann, bo, cy, dee])
    const cells = [...document.querySelectorAll("tbody tr")].map((r) => r.querySelectorAll("td")[1]!)
    // The cell holds the summary and nothing else.
    for (const c of cells) expect(c.children).toHaveLength(1)
    const summaries = cells.map((c) => c.firstElementChild as HTMLElement)
    expect(summaries.map(summaryOf)).toEqual([
      { text: "Owner", title: "Owner", mono: [] },
      { text: "default · Read-only", title: "default · Read-only", mono: ["default"] },
      { text: "staging · Admin, agents · Member", title: "staging · Admin, agents · Member", mono: ["staging", "agents"] },
      { text: "No workspace", title: "No workspace", mono: [] },
    ])
    for (const s of summaries) {
      expect(s.className).toContain("truncate")
      // Plain text: nothing to do with it, so it isn't a stop for the keyboard.
      expect(s.hasAttribute("tabindex")).toBe(false)
      expect(s.hasAttribute("aria-describedby")).toBe(false)
    }
    expect(screen.getByRole("columnheader", { name: "Access" })).toBeTruthy()
  })

  test("on a narrow screen the Access column goes away and the same summary sits under the email", () => {
    table([ann, bo, cy, dee])
    const header = screen.getByRole("columnheader", { name: "Access" })
    expect(header.className).toContain("hidden")
    expect(header.className).toContain("sm:table-cell")
    const rows = [...document.querySelectorAll("tbody tr")]
    for (const row of rows) {
      const [member, access] = [...row.querySelectorAll("td")]
      expect(access!.className).toContain("hidden")
      expect(access!.className).toContain("sm:table-cell")
      const inColumn = access!.firstElementChild as HTMLElement
      const underEmail = member!.querySelector<HTMLElement>(".sm\\:hidden")!
      expect(inColumn.className).not.toContain("sm:hidden")
      expect(summaryOf(underEmail)).toEqual(summaryOf(inColumn))
      // It comes right after the email.
      expect(underEmail.previousElementSibling?.getAttribute("title")).toMatch(/@example\.com$/)
    }
    // So each summary is in the page twice, once per layout.
    for (const text of ["Owner", "default · Read-only", "staging · Admin, agents · Member", "No workspace"]) {
      expect(document.querySelectorAll(`tbody [title="${text}"]`), text).toHaveLength(2)
    }
  })

  test("the summaries are plain text: no tooltip, no ui-sdk warning, nothing for an agent to press", async () => {
    const before = lastUiSdkWarning()
    table([ann, bo, cy, dee])
    await settle()
    expect(lastUiSdkWarning()).toBe(before)
    expect(document.querySelectorAll("tbody [data-slot='tooltip-trigger'], tbody [role='tooltip']")).toHaveLength(0)
    expect(named().sort()).toEqual(["Change access for Bo", "Change access for Dee", "Change access for cy@example.com", "Remove Bo", "Remove Dee", "Remove cy@example.com"].sort())
  })

  test("you can't change or remove yourself", () => {
    table([ann, bo])
    expect(screen.queryByRole("button", { name: /Ann/ })).toBeNull()
    expect(screen.getByRole("button", { name: "Change access for Bo" })).toBeTruthy()
    expect(screen.getByRole("button", { name: "Remove Bo" })).toBeTruthy()
  })

  test("the access dialog opens on what the person has now", async () => {
    const user = userEvent.setup()
    table([ann, bo])
    await user.click(screen.getByRole("button", { name: "Change access for Bo" }))
    const dialog = screen.getByRole("dialog", { name: "Access for Bo" })
    expect(within(dialog).getByText("Saving signs them out.")).toBeTruthy()
    expect(screen.getByRole("combobox", { name: "Access" }).textContent).toContain("Chosen workspaces")
    const list = within(dialog).getByLabelText("Workspaces")
    expect(list.getAttribute("data-slot")).toBe("scroll-area-viewport")
    expect(within(list).getAllByRole("listitem").map((li) => li.firstElementChild?.textContent)).toEqual(["default", "agents", "staging"])
    expect(within(list).getAllByRole("combobox")).toHaveLength(3)
    expect(screen.getByRole("combobox", { name: "Role in default" }).textContent).toContain("Read-only")
    expect(screen.getByRole("combobox", { name: "Role in agents" }).textContent).toContain("No access")
    expect(screen.getByRole("combobox", { name: "Role in staging" }).textContent).toContain("No access")

    await user.click(screen.getByRole("combobox", { name: "Role in agents" }))
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["No access", "Admin", "Member", "Read-only"])
  })

  test("saving sends the roles picked, one grant per workspace with a role", async () => {
    const user = userEvent.setup()
    const onAccess = table([ann, bo])
    await user.click(screen.getByRole("button", { name: "Change access for Bo" }))
    await pick(user, "Role in default", "Admin")
    await pick(user, "Role in staging", "Member")
    await user.click(screen.getByRole("button", { name: "Save" }))

    await waitFor(() => expect(onAccess).toHaveBeenCalledOnce())
    expect(onAccess).toHaveBeenCalledWith(bo, {
      owner: false,
      grants: [
        { workspace: DEFAULT.id, role: "admin" },
        { workspace: STAGING.id, role: "member" },
      ],
    })
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  })

  test("saving without a change sends what they had", async () => {
    const user = userEvent.setup()
    const onAccess = table([ann, bo])
    await user.click(screen.getByRole("button", { name: "Change access for Bo" }))
    await user.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(onAccess).toHaveBeenCalledWith(bo, { owner: false, grants: [{ workspace: DEFAULT.id, role: "readonly" }] }))
  })

  test("No access takes a workspace away", async () => {
    const user = userEvent.setup()
    const onAccess = table([ann, bo])
    await user.click(screen.getByRole("button", { name: "Change access for Bo" }))
    await pick(user, "Role in default", "No access")
    await pick(user, "Role in agents", "Read-only")
    await user.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(onAccess).toHaveBeenCalledWith(bo, { owner: false, grants: [{ workspace: AGENTS.id, role: "readonly" }] }))
  })

  test("a role in a workspace that is gone isn't sent back", async () => {
    const user = userEvent.setup()
    const onAccess = table([ann, cy])
    await user.click(screen.getByRole("button", { name: "Change access for cy@example.com" }))
    expect(screen.getByRole("dialog", { name: "Access for cy@example.com" })).toBeTruthy()
    await user.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(onAccess).toHaveBeenCalledOnce())
    expect(onAccess.mock.calls[0]![1]).toEqual({
      owner: false,
      grants: [
        { workspace: AGENTS.id, role: "member" },
        { workspace: STAGING.id, role: "admin" },
      ],
    })
  })

  test("picking Owner sends owner alone and hides the workspaces", async () => {
    const user = userEvent.setup()
    const onAccess = table([ann, bo])
    await user.click(screen.getByRole("button", { name: "Change access for Bo" }))
    await pick(user, "Role in agents", "Admin")
    await pick(user, "Access", "Owner, every workspace")
    expect(screen.queryByLabelText("Workspaces")).toBeNull()
    expect(screen.queryByRole("list")).toBeNull()
    expect(screen.queryByRole("combobox", { name: "Role in default" })).toBeNull()
    await user.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(onAccess).toHaveBeenCalledOnce())
    expect(onAccess.mock.calls[0]).toEqual([bo, { owner: true }])
  })

  test("an owner can be given chosen workspaces instead", async () => {
    const user = userEvent.setup()
    const onAccess = table([ann, eve])
    await user.click(screen.getByRole("button", { name: "Change access for Eve" }))
    expect(screen.getByRole("combobox", { name: "Access" }).textContent).toContain("Owner, every workspace")
    expect(screen.queryByLabelText("Workspaces")).toBeNull()
    expect(screen.queryByRole("list")).toBeNull()
    await pick(user, "Access", "Chosen workspaces")
    expect(screen.getByLabelText("Workspaces")).toBeTruthy()
    for (const w of workspaces) expect(screen.getByRole("combobox", { name: `Role in ${w.name}` }).textContent).toContain("No access")
    await pick(user, "Role in agents", "Member")
    await user.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(onAccess).toHaveBeenCalledWith(eve, { owner: false, grants: [{ workspace: AGENTS.id, role: "member" }] }))
  })

  test("the workspace list is hidden while Owner is picked and comes back, with the roles picked, for Chosen workspaces", async () => {
    const user = userEvent.setup()
    const onAccess = table([ann, bo])
    await user.click(screen.getByRole("button", { name: "Change access for Bo" }))
    const dialog = within(screen.getByRole("dialog", { name: "Access for Bo" }))
    expect(dialog.getAllByRole("listitem")).toHaveLength(3)
    await pick(user, "Role in staging", "Admin")

    await pick(user, "Access", "Owner, every workspace")
    // Gone at once, with nothing left behind to animate or to name.
    expect(dialog.queryByLabelText("Workspaces")).toBeNull()
    expect(document.querySelector("[data-slot='collapsible'], [data-slot='scroll-area']")).toBeNull()
    expect(dialog.queryAllByRole("listitem")).toEqual([])
    expect(dialog.queryAllByRole("combobox")).toHaveLength(1)
    for (const w of workspaces) expect(screen.queryByRole("combobox", { name: `Role in ${w.name}` })).toBeNull()

    await pick(user, "Access", "Chosen workspaces")
    expect(dialog.getByLabelText("Workspaces")).toBeTruthy()
    expect(dialog.getAllByRole("listitem")).toHaveLength(3)
    // What was picked before the detour is still there.
    expect(screen.getByRole("combobox", { name: "Role in default" }).textContent).toContain("Read-only")
    expect(screen.getByRole("combobox", { name: "Role in staging" }).textContent).toContain("Admin")
    await user.click(screen.getByRole("button", { name: "Save" }))
    await waitFor(() => expect(onAccess).toHaveBeenCalledOnce())
    expect(onAccess.mock.calls[0]![1]).toEqual({
      owner: false,
      grants: [
        { workspace: DEFAULT.id, role: "readonly" },
        { workspace: STAGING.id, role: "admin" },
      ],
    })
  })

  test("the access button shows a shield, and its dialog is pinned below the top", async () => {
    const user = userEvent.setup()
    table([ann, bo])
    const button = screen.getByRole("button", { name: "Change access for Bo" })
    expect(button.querySelector("svg")?.getAttribute("class")).toContain("lucide-shield")
    await user.click(button)
    expect(screen.getByRole("dialog", { name: "Access for Bo" }).className).toContain("mb-auto")
  })

  test("an agent finds every part of the access editor by name, with and without the workspace list", async () => {
    table([ann, bo])
    const opened = await agent.act({ steps: [{ ref: "Change access for Bo", action: "press" }] })
    expect(opened.ok, opened.error?.message).toBe(true)
    const editor = ["Access for Bo", "Access", "Workspaces", "Role in default", "Role in agents", "Role in staging", "Save"]
    expect(named()).toEqual(expect.arrayContaining(editor))
    expect(agent.snapshot().filter((e) => !e.offPage && e.component === "collapsible")).toEqual([])

    const owner = await agent.act({ steps: [{ ref: "Access", action: "setValue", args: ["Owner, every workspace"] }] })
    expect(owner.ok, owner.error?.message).toBe(true)
    const without = named()
    for (const label of ["Workspaces", "Role in default", "Role in agents", "Role in staging"]) expect(without, label).not.toContain(label)
    for (const label of ["Access for Bo", "Access", "Save"]) expect(without, label).toContain(label)

    const chosen = await agent.act({ steps: [{ ref: "Access", action: "setValue", args: ["Chosen workspaces"] }] })
    expect(chosen.ok, chosen.error?.message).toBe(true)
    expect(named()).toEqual(expect.arrayContaining(editor))
  })

  test("closing without saving changes nothing, and the dialog reopens on what they have", async () => {
    const user = userEvent.setup()
    const onAccess = table([ann, bo])
    await user.click(screen.getByRole("button", { name: "Change access for Bo" }))
    await pick(user, "Role in default", "Admin")
    await pick(user, "Access", "Owner, every workspace")
    await user.click(screen.getByRole("button", { name: "Close" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(onAccess).not.toHaveBeenCalled()

    await user.click(screen.getByRole("button", { name: "Change access for Bo" }))
    expect(screen.getByRole("combobox", { name: "Access" }).textContent).toContain("Chosen workspaces")
    expect(screen.getByRole("combobox", { name: "Role in default" }).textContent).toContain("Read-only")
  })

  test("a refusal stays in the dialog", async () => {
    const user = userEvent.setup()
    const onAccess = vi.fn(async (_m: Member, _a: MemberAccess) => {
      throw new Error("Keep at least one owner.")
    })
    table([ann, eve], onAccess)
    await user.click(screen.getByRole("button", { name: "Change access for Eve" }))
    await pick(user, "Access", "Chosen workspaces")
    await user.click(screen.getByRole("button", { name: "Save" }))
    expect((await screen.findByRole("alert")).textContent).toBe("Keep at least one owner.")
    expect(screen.getByRole("dialog", { name: "Access for Eve" })).toBeTruthy()
  })
})

describe("the invite dialog", () => {
  function invite(current?: string, onInvite = vi.fn(async (_email: string, _a: MemberAccess) => "https://db.example.com/app/invite/tok")) {
    const onDone = vi.fn()
    render(<InviteDialog workspaces={workspaces} current={current} onInvite={onInvite} onDone={onDone} />)
    return { onInvite, onDone }
  }

  test("starts with the workspace on screen as Member, and shows the link once", async () => {
    const user = userEvent.setup()
    const { onInvite, onDone } = invite(AGENTS.id)
    await user.click(screen.getByRole("button", { name: "Invite" }))
    expect(screen.getByRole("dialog", { name: "Invite someone" })).toBeTruthy()
    expect(screen.getByRole("combobox", { name: "Access" }).textContent).toContain("Chosen workspaces")
    expect(screen.getByRole("combobox", { name: "Role in agents" }).textContent).toContain("Member")
    expect(screen.getByRole("combobox", { name: "Role in default" }).textContent).toContain("No access")
    expect(screen.getByRole("combobox", { name: "Role in staging" }).textContent).toContain("No access")

    await user.type(screen.getByLabelText("Email"), "  sam@example.com ")
    await user.click(screen.getByRole("button", { name: "Create invite" }))
    await waitFor(() => expect(onInvite).toHaveBeenCalledOnce())
    expect(onInvite).toHaveBeenCalledWith("sam@example.com", { owner: false, grants: [{ workspace: AGENTS.id, role: "member" }] })
    expect(await screen.findByText("https://db.example.com/app/invite/tok")).toBeTruthy()
    expect(screen.getByRole("button", { name: "Copy invite link" })).toBeTruthy()
    expect(onDone).not.toHaveBeenCalled()
    await user.click(screen.getByRole("button", { name: "Done" }))
    await waitFor(() => expect(onDone).toHaveBeenCalledOnce())
  })

  test("sends the roles picked in other workspaces too", async () => {
    const user = userEvent.setup()
    const { onInvite } = invite(AGENTS.id)
    await user.click(screen.getByRole("button", { name: "Invite" }))
    await user.type(screen.getByLabelText("Email"), "sam@example.com")
    await pick(user, "Role in agents", "Read-only")
    await pick(user, "Role in default", "Admin")
    await user.click(screen.getByRole("button", { name: "Create invite" }))
    await waitFor(() => expect(onInvite).toHaveBeenCalledOnce())
    expect(onInvite.mock.calls[0]![1]).toEqual({
      owner: false,
      grants: [
        { workspace: DEFAULT.id, role: "admin" },
        { workspace: AGENTS.id, role: "readonly" },
      ],
    })
  })

  test("an owner invite sends owner alone", async () => {
    const user = userEvent.setup()
    const { onInvite } = invite(AGENTS.id)
    await user.click(screen.getByRole("button", { name: "Invite" }))
    await user.type(screen.getByLabelText("Email"), "sam@example.com")
    await pick(user, "Access", "Owner, every workspace")
    await user.click(screen.getByRole("button", { name: "Create invite" }))
    await waitFor(() => expect(onInvite).toHaveBeenCalledWith("sam@example.com", { owner: true }))
  })

  test("without a workspace on screen nothing is picked, and the refusal shows", async () => {
    const user = userEvent.setup()
    const onInvite = vi.fn(async (_email: string, _a: MemberAccess): Promise<string> => {
      throw new Error("Pick at least one workspace.")
    })
    invite(undefined, onInvite)
    await user.click(screen.getByRole("button", { name: "Invite" }))
    for (const w of workspaces) expect(screen.getByRole("combobox", { name: `Role in ${w.name}` }).textContent).toContain("No access")
    await user.type(screen.getByLabelText("Email"), "sam@example.com")
    await user.click(screen.getByRole("button", { name: "Create invite" }))
    await waitFor(() => expect(onInvite).toHaveBeenCalledWith("sam@example.com", { owner: false, grants: [] }))
    expect((await screen.findByRole("alert")).textContent).toBe("Pick at least one workspace.")
    expect(screen.getByRole("dialog", { name: "Invite someone" })).toBeTruthy()
  })

  test("needs an email, and starts over each time it opens", async () => {
    const user = userEvent.setup()
    const { onInvite } = invite(AGENTS.id)
    await user.click(screen.getByRole("button", { name: "Invite" }))
    await user.click(screen.getByRole("button", { name: "Create invite" }))
    expect(onInvite).not.toHaveBeenCalled()

    await user.type(screen.getByLabelText("Email"), "sam@example.com")
    await pick(user, "Role in agents", "Admin")
    await pick(user, "Role in staging", "Admin")
    await user.click(screen.getByRole("button", { name: "Close" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())

    await user.click(screen.getByRole("button", { name: "Invite" }))
    expect((screen.getByLabelText("Email") as HTMLInputElement).value).toBe("")
    expect(screen.getByRole("combobox", { name: "Role in agents" }).textContent).toContain("Member")
    expect(screen.getByRole("combobox", { name: "Role in staging" }).textContent).toContain("No access")
    expect(onInvite).not.toHaveBeenCalled()
  })
})
