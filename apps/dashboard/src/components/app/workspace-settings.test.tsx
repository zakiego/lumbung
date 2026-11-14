import type { WorkspaceLimitsOut, WorkspaceOut, WorkspaceUsageOut } from "@lumbung/client"
import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { describe, expect, test, vi } from "vitest"
import { lastUiSdkWarning, settle } from "@/test/ui-sdk"
import * as worst from "@/test/worst-case"
import { DeletedWorkspaces, LimitsForm, type LimitsPatch, MoveForm, UsageList, ofLimit } from "./workspace-settings"

const MB = 1024 * 1024

let approve = false
const agent = createAgent({ allow: (step) => !step.confirm || approve })

function named() {
  const entries = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  return entries
}
const NO_LIMITS: WorkspaceLimitsOut = { max_databases: 0, max_buckets: 0, max_database_bytes: 0, max_object_bytes: 0 }

test("ofLimit: the limit when there is one, the bare amount when there isn't", () => {
  expect(ofLimit("3", "20")).toBe("3 of 20")
  expect(ofLimit("1.5 MB", "10.0 MB")).toBe("1.5 MB of 10.0 MB")
  expect(ofLimit("3", null)).toBe("3")
  expect(ofLimit("0", null)).toBe("0")
  expect(ofLimit("3", "")).toBe("3")
})

describe("UsageList", () => {
  const rows = () => Object.fromEntries([...document.querySelectorAll("dt")].map((dt) => [dt.textContent, dt.nextElementSibling?.textContent]))

  test("shows usage against a limit, and a bare number without one", () => {
    const usage: WorkspaceUsageOut = { databases: 2, buckets: 1, database_bytes: 1.5 * MB, object_bytes: 0, tokens: 3 }
    render(<UsageList usage={usage} limits={{ max_databases: 20, max_buckets: 0, max_database_bytes: 10 * MB, max_object_bytes: 0 }} />)
    expect(rows()).toEqual({
      Databases: "2 of 20",
      Buckets: "1",
      "Database storage": "1.5 MB of 10.0 MB",
      "Object storage": "0 B",
      Tokens: "3",
    })
    expect(screen.getByText("2 of 20")).toBeTruthy()
  })

  test("with no limits at all, every row is a bare number", () => {
    render(<UsageList usage={{ databases: 2, buckets: 1, database_bytes: 2048, object_bytes: 5 * 1024 ** 3, tokens: 0 }} limits={NO_LIMITS} />)
    expect(rows()).toEqual({ Databases: "2", Buckets: "1", "Database storage": "2.0 KB", "Object storage": "5.00 GB", Tokens: "0" })
    expect(document.body.textContent).not.toContain(" of ")
  })

  test("big numbers are grouped, and usage over a limit is shown as it is", () => {
    render(
      <UsageList
        usage={{ databases: 1_284_000, buckets: 21, database_bytes: 0, object_bytes: 3 * MB, tokens: 12_345 }}
        limits={{ max_databases: 1_000_000, max_buckets: 20, max_database_bytes: MB, max_object_bytes: 2 * MB }}
      />,
    )
    expect(rows()).toEqual({
      Databases: "1,284,000 of 1,000,000",
      Buckets: "21 of 20",
      "Database storage": "0 B of 1.0 MB",
      "Object storage": "3.0 MB of 2.0 MB",
      Tokens: "12,345",
    })
  })
})

test("UsageList: terabytes and petabytes read in their own units", () => {
  render(
    <UsageList
      usage={{ databases: 1, buckets: 1, database_bytes: 1.5 * 1024 ** 4, object_bytes: 1024 ** 5, tokens: 0 }}
      limits={{ max_databases: 0, max_buckets: 0, max_database_bytes: 2 * 1024 ** 4, max_object_bytes: 8_000_000_000 * MB }}
    />,
  )
  expect(screen.getByText("1.50 TB of 2.00 TB")).toBeTruthy()
  expect(screen.getByText("1 PB of 7.45 PB")).toBeTruthy()
})

describe("LimitsForm", () => {
  const limits: WorkspaceLimitsOut = { max_databases: 20, max_buckets: 0, max_database_bytes: 512 * MB, max_object_bytes: 0 }

  function form(start: WorkspaceLimitsOut = limits, onSave = vi.fn(async (_patch: LimitsPatch) => {})) {
    render(<LimitsForm limits={start} onSave={onSave} />)
    const field = (label: string) => screen.getByLabelText(label) as HTMLInputElement
    const save = () => screen.getByRole("button", { name: "Save limits" }) as HTMLButtonElement
    return { onSave, field, save }
  }

  test("starts on the current limits, megabytes for bytes and empty for none, with nothing to save", async () => {
    const user = userEvent.setup()
    const { onSave, field, save } = form()
    expect(field("Databases").value).toBe("20")
    expect(field("Buckets").value).toBe("")
    expect(field("Database storage (MB)").value).toBe("512")
    expect(field("Object storage (MB)").value).toBe("")
    expect(field("Buckets").placeholder).toBe("No limit")
    expect(save().disabled).toBe(true)
    await user.click(save())
    await user.type(field("Databases"), "{Enter}")
    expect(onSave).not.toHaveBeenCalled()
  })

  test("sends only the field that changed", async () => {
    const user = userEvent.setup()
    const { onSave, field, save } = form()
    await user.type(field("Buckets"), "5")
    await user.tab()
    expect(save().disabled).toBe(false)
    await user.click(save())
    await waitFor(() => expect(onSave).toHaveBeenCalledOnce())
    expect(onSave.mock.calls[0]![0]).toEqual({ maxBuckets: 5 })
  })

  test("an emptied field is sent as 0, which is no limit", async () => {
    const user = userEvent.setup()
    const { onSave, field, save } = form()
    await user.clear(field("Databases"))
    await user.clear(field("Database storage (MB)"))
    await user.tab()
    await user.click(save())
    await waitFor(() => expect(onSave).toHaveBeenCalledOnce())
    expect(onSave.mock.calls[0]![0]).toEqual({ maxDatabases: 0, maxDatabaseBytes: 0 })
  })

  test("megabytes are sent as bytes", async () => {
    const user = userEvent.setup()
    const { onSave, field, save } = form()
    await user.clear(field("Database storage (MB)"))
    await user.type(field("Database storage (MB)"), "1024")
    await user.type(field("Object storage (MB)"), "2")
    await user.tab()
    await user.click(save())
    await waitFor(() => expect(onSave).toHaveBeenCalledOnce())
    expect(onSave.mock.calls[0]![0]).toEqual({ maxDatabaseBytes: 1024 * MB, maxObjectBytes: 2 * MB })
  })

  test("all four at once", async () => {
    const user = userEvent.setup()
    const { onSave, field, save } = form(NO_LIMITS)
    await user.type(field("Databases"), "3")
    await user.type(field("Buckets"), "4")
    await user.type(field("Database storage (MB)"), "5")
    await user.type(field("Object storage (MB)"), "6")
    await user.tab()
    await user.click(save())
    await waitFor(() => expect(onSave).toHaveBeenCalledOnce())
    expect(onSave.mock.calls[0]![0]).toEqual({ maxDatabases: 3, maxBuckets: 4, maxDatabaseBytes: 5 * MB, maxObjectBytes: 6 * MB })
  })

  test("a field put back to what it was is no change", async () => {
    const user = userEvent.setup()
    const { onSave, field, save } = form()
    await user.clear(field("Databases"))
    await user.type(field("Databases"), "21")
    await user.tab()
    expect(save().disabled).toBe(false)
    await user.clear(field("Databases"))
    await user.type(field("Databases"), "20")
    await user.tab()
    expect(save().disabled).toBe(true)
    expect(onSave).not.toHaveBeenCalled()
  })

  test("a byte limit that isn't a whole megabyte survives a save that didn't touch it", async () => {
    const user = userEvent.setup()
    const { onSave, field, save } = form({ max_databases: 0, max_buckets: 0, max_database_bytes: 1_500_000, max_object_bytes: 100 })
    expect(field("Database storage (MB)").value).toBe("1")
    expect(field("Object storage (MB)").value).toBe("1")
    expect(save().disabled).toBe(true)
    await user.type(field("Databases"), "7")
    await user.tab()
    await user.click(save())
    await waitFor(() => expect(onSave).toHaveBeenCalledOnce())
    expect(onSave.mock.calls[0]![0]).toEqual({ maxDatabases: 7 })
  })

  test("a save in flight can't be sent twice, and a refusal is shown", async () => {
    const user = userEvent.setup()
    let fail!: (e: Error) => void
    const onSave = vi.fn((_patch: LimitsPatch) => new Promise<void>((_, reject) => (fail = reject)))
    const { field, save } = form(limits, onSave)
    await user.type(field("Buckets"), "5")
    await user.tab()
    await user.click(save())
    const busy = await screen.findByRole("button", { name: "Saving…" })
    expect((busy as HTMLButtonElement).disabled).toBe(true)
    await user.click(busy)
    expect(onSave).toHaveBeenCalledOnce()

    fail(new Error("the workspace already has 6 buckets"))
    expect((await screen.findByRole("alert")).textContent).toBe("the workspace already has 6 buckets")
    expect(save().disabled).toBe(false)
    expect(field("Buckets").value).toBe("5")
    // The refusal is about the form, so no one field is marked as the wrong one.
    expect(document.querySelectorAll("[data-invalid], [aria-invalid='true']")).toHaveLength(0)
    expect(screen.getAllByRole("alert")).toHaveLength(1)
  })

  test("a refusal clears when the next save goes through", async () => {
    const user = userEvent.setup()
    const onSave = vi.fn(async (patch: LimitsPatch) => {
      if (patch.maxBuckets === 5) throw new Error("the workspace already has 6 buckets")
    })
    const { field, save } = form(limits, onSave)
    await user.type(field("Buckets"), "5")
    await user.tab()
    await user.click(save())
    expect(await screen.findByRole("alert")).toBeTruthy()
    await user.clear(field("Buckets"))
    await user.type(field("Buckets"), "7")
    await user.tab()
    await user.click(save())
    await waitFor(() => expect(onSave).toHaveBeenLastCalledWith({ maxBuckets: 7 }))
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull())
  })

  test("a byte limit goes up to 8,000,000,000 MB, which still fits a whole number of bytes", async () => {
    const user = userEvent.setup()
    const { onSave, field, save } = form(NO_LIMITS)
    await user.type(field("Object storage (MB)"), "8000000000")
    await user.tab()
    expect(field("Object storage (MB)").value).toBe("8000000000")
    await user.click(save())
    await waitFor(() => expect(onSave).toHaveBeenCalledOnce())
    expect(onSave.mock.calls[0]![0]).toEqual({ maxObjectBytes: 8_000_000_000 * MB })
    expect(Number.isSafeInteger(8_000_000_000 * MB)).toBe(true)
  })

  test("more than that is brought back to the most the field takes", async () => {
    const user = userEvent.setup()
    const { onSave, field, save } = form(NO_LIMITS)
    await user.type(field("Database storage (MB)"), "9000000000")
    await user.type(field("Databases"), "2000000")
    await user.tab()
    expect(field("Database storage (MB)").value).toBe("8000000000")
    expect(field("Databases").value).toBe("1000000")
    await user.click(save())
    await waitFor(() => expect(onSave).toHaveBeenCalledOnce())
    expect(onSave.mock.calls[0]![0]).toEqual({ maxDatabases: 1_000_000, maxDatabaseBytes: 8_000_000_000 * MB })
  })

  test("an agent saves limits: one it sets, one it clears, megabytes as bytes", async () => {
    const onSave = vi.fn(async (_patch: LimitsPatch) => {})
    render(<LimitsForm limits={limits} onSave={onSave} />)
    const entries = named()
    for (const label of ["Databases", "Buckets", "Database storage (MB)", "Object storage (MB)", "Save limits"]) expect(entries.map((e) => e.label), label).toContain(label)
    // Nothing changed yet, so there is nothing to save.
    expect(entries.find((e) => e.label === "Save limits")!.actions).toEqual([])

    const result = await agent.act({
      steps: [
        { ref: "Buckets", action: "setValue", args: [5] },
        { ref: "Databases", action: "setValue", args: [null] },
        { ref: "Object storage (MB)", action: "setValue", args: ["2048"] },
        { ref: "Save limits", action: "press" },
      ],
    })
    expect(result.ok, result.error?.message).toBe(true)
    await waitFor(() => expect(onSave).toHaveBeenCalledOnce())
    expect(onSave.mock.calls[0]![0]).toEqual({ maxDatabases: 0, maxBuckets: 5, maxObjectBytes: 2048 * MB })
    named()
  })
})

describe("MoveForm", () => {
  const targets = [
    { id: "ws_bbbbbbbbbbbbbbbbbbbb", name: "agents" },
    { id: "ws_cccccccccccccccccccc", name: "staging" },
  ]

  test("asks before moving, to the first workspace unless another is picked", async () => {
    const user = userEvent.setup()
    const onMove = vi.fn(async (_to: { id: string; name: string }) => {})
    render(<MoveForm what="database" name="shop" targets={targets} onMove={onMove} />)
    const select = screen.getByRole("combobox", { name: "Workspace" })
    expect(select.textContent).toContain("agents")
    expect(screen.getByText("Workspace", { selector: "label" })).toBeTruthy()
    // The button comes after the field it acts on.
    expect(select.compareDocumentPosition(screen.getByRole("button", { name: "Move" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

    await user.click(screen.getByRole("button", { name: "Move" }))
    const ask = screen.getByRole("alertdialog", { name: "Move shop to agents?" })
    expect(within(ask).getByText("Tokens that reach this database stop working. Make new ones in agents.")).toBeTruthy()
    expect(onMove).not.toHaveBeenCalled()
    await user.click(within(ask).getByRole("button", { name: "Cancel" }))
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull())
    expect(onMove).not.toHaveBeenCalled()

    await user.click(screen.getByRole("button", { name: "Move" }))
    await user.click(screen.getByRole("button", { name: "Move database" }))
    await waitFor(() => expect(onMove).toHaveBeenCalledOnce())
    expect(onMove).toHaveBeenCalledWith(targets[0])
  })

  test("passes the workspace picked", async () => {
    const user = userEvent.setup()
    const onMove = vi.fn(async (_to: { id: string; name: string }) => {})
    render(<MoveForm what="bucket" name="photos" targets={targets} onMove={onMove} />)
    await user.click(screen.getByRole("combobox", { name: "Workspace" }))
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["agents", "staging"])
    await user.click(screen.getByRole("option", { name: "staging" }))

    await user.click(screen.getByRole("button", { name: "Move" }))
    const ask = screen.getByRole("alertdialog", { name: "Move photos to staging?" })
    expect(within(ask).getByText("Tokens that reach this bucket stop working. Make new ones in staging.")).toBeTruthy()
    await user.click(within(ask).getByRole("button", { name: "Move bucket" }))
    await waitFor(() => expect(onMove).toHaveBeenCalledOnce())
    expect(onMove).toHaveBeenCalledWith(targets[1])
  })

  test("a refusal shows in the question", async () => {
    const user = userEvent.setup()
    render(
      <MoveForm
        what="database"
        name="shop"
        targets={targets}
        onMove={async () => {
          throw new Error("agents already has a database named shop")
        }}
      />,
    )
    await user.click(screen.getByRole("button", { name: "Move" }))
    await user.click(screen.getByRole("button", { name: "Move database" }))
    expect((await screen.findByRole("alert")).textContent).toBe("agents already has a database named shop")
    expect(screen.getByRole("alertdialog", { name: "Move shop to agents?" })).toBeTruthy()
  })

  test("with nowhere to move to there is no Move button", () => {
    render(<MoveForm what="database" name="shop" targets={[]} onMove={async () => {}} />)
    expect(screen.queryByRole("button", { name: "Move" })).toBeNull()
  })

  test("an agent needs a person's yes to move", async () => {
    approve = false
    const onMove = vi.fn(async (_to: { id: string; name: string }) => {})
    render(<MoveForm what="database" name="shop" targets={targets} onMove={onMove} />)
    expect(named().map((e) => e.label)).toEqual(["Workspace", "Move"])

    const picked = await agent.act({ steps: [{ ref: "Workspace", action: "setValue", args: ["staging"] }] })
    expect(picked.ok, picked.error?.message).toBe(true)
    const steps = [
      { ref: "Move", action: "press" },
      { ref: "Move shop to staging?", action: "press", args: ["Move database"] },
    ]
    expect((await agent.act({ steps })).ok).toBe(false)
    expect(onMove).not.toHaveBeenCalled()
    approve = true
    const result = await agent.act({ steps: [steps.at(-1)!] })
    expect(result.ok, result.error?.message).toBe(true)
    await waitFor(() => expect(onMove).toHaveBeenCalledWith(targets[1]))
  })
})

describe("DeletedWorkspaces", () => {
  const at = "2026-11-14T08:00:00Z"
  const deleted = (name: string, id: string, o: Partial<WorkspaceOut> = {}): WorkspaceOut => ({
    id,
    name,
    default: false,
    created_at: "2026-11-01T08:00:00Z",
    updated_at: at,
    deleted_at: at,
    purge_after: "2026-11-21T08:00:00Z",
    ...o,
  })
  const agents = deleted("agents", "ws_bbbbbbbbbbbbbbbbbbbb")
  const staging = deleted("staging", "ws_cccccccccccccccccccc")

  function list(workspaces: WorkspaceOut[], onRestore = vi.fn(async (_w: WorkspaceOut, _name?: string) => {})) {
    render(<DeletedWorkspaces workspaces={workspaces} onRestore={onRestore} />)
    return onRestore
  }

  async function openRestore(user: ReturnType<typeof userEvent.setup>, name: string) {
    await user.click(screen.getByRole("button", { name: `Restore ${name}` }))
    const dialog = screen.getByRole("dialog", { name: `Restore ${name}` })
    return { dialog, field: within(dialog).getByLabelText("Name") as HTMLInputElement, submit: within(dialog).getByRole("button", { name: "Restore" }) }
  }

  test("an empty trash says so", () => {
    list([])
    expect(screen.getByText("No deleted workspaces")).toBeTruthy()
    expect(screen.queryByRole("table")).toBeNull()
  })

  // One row as a person reads it: the name, its title, the line under it, the two dates and the button.
  function rows() {
    return [...document.querySelectorAll("tbody tr")].map((r) => {
      const [first, gone, purged, actions] = [...r.querySelectorAll("td")]
      const name = first!.firstElementChild as HTMLElement
      return {
        name: name.textContent,
        title: name.getAttribute("title"),
        under: first!.querySelector(".sm\\:hidden")?.textContent ?? null,
        deleted: gone!.textContent,
        purged: purged!.textContent,
        button: within(actions as HTMLElement).getByRole("button").getAttribute("aria-label"),
      }
    })
  }

  test("lists each workspace with when it went and when it's removed for good", () => {
    list([agents, deleted(worst.LONG_WORKSPACE, "ws_dddddddddddddddddddd", { deleted_at: null, purge_after: null })])
    expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Workspace", "Deleted", "Removed for good", "Actions"])
    expect(screen.queryByText("Gone for good")).toBeNull()
    expect(rows()).toEqual([
      { name: "agents", title: "agents", under: "Removed for good Nov 21, 2026", deleted: "Nov 14, 2026", purged: "Nov 21, 2026", button: "Restore agents" },
      { name: worst.LONG_WORKSPACE, title: worst.LONG_WORKSPACE, under: null, deleted: "-", purged: "-", button: `Restore ${worst.LONG_WORKSPACE}` },
    ])
    // Each Restore button says which workspace it restores.
    expect(screen.getByRole("button", { name: "Restore agents" }).textContent).toBe("Restore")
    expect(screen.getByRole("button", { name: `Restore ${worst.LONG_WORKSPACE}` })).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Restore" })).toBeNull()
  })

  test("on a narrow screen the dates' columns go away, the date sits under the name and the name wraps", () => {
    list([agents, deleted(worst.LONG_WORKSPACE, "ws_dddddddddddddddddddd")])
    for (const name of ["Deleted", "Removed for good"]) {
      const header = screen.getByRole("columnheader", { name })
      expect(header.className).toContain("hidden")
      expect(header.className).toContain("sm:table-cell")
    }
    for (const row of document.querySelectorAll("tbody tr")) {
      const [first, gone, purged] = [...row.querySelectorAll("td")]
      for (const cell of [gone!, purged!]) {
        expect(cell.className).toContain("hidden")
        expect(cell.className).toContain("sm:table-cell")
      }
      const name = first!.firstElementChild as HTMLElement
      expect(name.className).toContain("[overflow-wrap:anywhere]")
      expect(name.className).toContain("sm:truncate")
      expect(name.className).toContain("font-mono")
      // Plain text: nothing to do with it, so it isn't a stop for the keyboard.
      expect(name.hasAttribute("tabindex")).toBe(false)
      expect(name.hasAttribute("aria-describedby")).toBe(false)
    }
    // So the date is in the page twice per row, once per layout.
    expect(screen.getAllByText("Removed for good Nov 21, 2026")).toHaveLength(2)
    expect(screen.getAllByText("Nov 21, 2026")).toHaveLength(2)
  })

  test("the names are plain text: no tooltip, no ui-sdk warning, and an agent sees only the Restore buttons", async () => {
    const before = lastUiSdkWarning()
    list([agents, staging])
    await settle()
    expect(lastUiSdkWarning()).toBe(before)
    expect(document.querySelectorAll("[data-slot='tooltip-trigger'], [role='tooltip']")).toHaveLength(0)
    expect(named().map((e) => e.label)).toEqual(["Restore agents", "Restore staging"])
  })

  test("an agent restores a deleted workspace, under its own name or a new one", async () => {
    const onRestore = list([agents, staging])
    const labels = named().map((e) => e.label)
    for (const label of ["Restore agents", "Restore staging"]) expect(labels, label).toContain(label)

    const same = await agent.act({
      steps: [
        { ref: "Restore staging", action: "press" },
        { ref: "Restore staging", action: "press", args: ["Restore"] },
      ],
    })
    expect(same.ok, same.error?.message).toBe(true)
    await waitFor(() => expect(onRestore).toHaveBeenCalledOnce())
    expect(onRestore.mock.calls[0]).toEqual([staging, undefined])
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())

    const opened = await agent.act({ steps: [{ ref: "Restore agents", action: "press" }] })
    expect(opened.ok, opened.error?.message).toBe(true)
    const open = named().map((e) => e.label)
    for (const label of ["Restore agents", "Name", "Restore"]) expect(open, label).toContain(label)
    const renamed = await agent.act({
      steps: [
        { ref: "Name", action: "setValue", args: ["agents-2"] },
        { ref: "Restore agents", action: "press", args: ["Restore"] },
      ],
    })
    expect(renamed.ok, renamed.error?.message).toBe(true)
    await waitFor(() => expect(onRestore).toHaveBeenCalledTimes(2))
    expect(onRestore.mock.calls[1]).toEqual([agents, "agents-2"])
  })

  test("restoring under the same name sends no name", async () => {
    const user = userEvent.setup()
    const onRestore = list([agents, staging])
    const { dialog, field, submit } = await openRestore(user, "staging")
    expect(field.value).toBe("staging")
    expect(within(dialog).getByText("Its databases, buckets and tokens come back with it.")).toBeTruthy()
    await user.click(submit)
    await waitFor(() => expect(onRestore).toHaveBeenCalledOnce())
    expect(onRestore.mock.calls[0]).toEqual([staging, undefined])
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  })

  test("restoring under a new name sends it", async () => {
    const user = userEvent.setup()
    const onRestore = list([agents, staging])
    const { field, submit } = await openRestore(user, "agents")
    await user.type(field, "-2")
    await user.click(submit)
    await waitFor(() => expect(onRestore).toHaveBeenCalledOnce())
    expect(onRestore.mock.calls[0]).toEqual([agents, "agents-2"])
  })

  test("a name typed and then put back is the same name", async () => {
    const user = userEvent.setup()
    const onRestore = list([agents])
    const { field, submit } = await openRestore(user, "agents")
    await user.clear(field)
    await user.type(field, "agents")
    await user.click(submit)
    await waitFor(() => expect(onRestore).toHaveBeenCalledOnce())
    expect(onRestore.mock.calls[0]).toEqual([agents, undefined])
  })

  test("a name the engine wouldn't take isn't sent", async () => {
    const user = userEvent.setup()
    const onRestore = list([agents])
    const { field, submit } = await openRestore(user, "agents")
    await user.clear(field)
    await user.click(submit)
    await user.type(field, "Not A Name")
    await user.click(submit)
    expect(onRestore).not.toHaveBeenCalled()
    expect(screen.getByRole("dialog", { name: "Restore agents" })).toBeTruthy()
  })

  test("a taken name shows the refusal, and the dialog reopens on the old name", async () => {
    const user = userEvent.setup()
    const onRestore = vi.fn(async (_w: WorkspaceOut, name?: string) => {
      if (!name) throw new Error("A workspace named agents already exists.")
    })
    list([agents], onRestore)
    const first = await openRestore(user, "agents")
    await user.click(first.submit)
    expect((await screen.findByRole("alert")).textContent).toBe("A workspace named agents already exists.")
    await user.type(first.field, "-old")
    await user.click(first.submit)
    await waitFor(() => expect(onRestore).toHaveBeenLastCalledWith(agents, "agents-old"))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())

    const again = await openRestore(user, "agents")
    expect(again.field.value).toBe("agents")
    expect(screen.queryByRole("alert")).toBeNull()
  })
})
