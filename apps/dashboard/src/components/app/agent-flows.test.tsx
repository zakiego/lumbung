import type { BranchOut, SnapshotOut } from "@lumbung/client"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"
import type { Member, MemberAccess } from "@/server/users"
import { PromoteBranch, ResetBranch } from "./branches"
import { BranchPicker } from "./db-frame"
import { ImportForm } from "./db-settings"
import { MemberTable } from "./members"
import { SnapshotTable } from "./snapshots"

let approve = false
const agent = createAgent({ allow: (step) => !step.confirm || approve })

const at = "2026-11-14T08:00:00Z"

function named() {
  const entries = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  return entries.map((e) => e.label)
}

test("promoting a branch is a plain yes with a person's approval", async () => {
  const onPromote = vi.fn(async () => {})
  render(<PromoteBranch branch="dev" onPromote={onPromote} />)
  expect(named()).toContain("Promote")

  const steps = [
    { ref: "Promote", action: "press" },
    { ref: "Make dev the default?", action: "press", args: ["Make default"] },
  ]
  approve = false
  expect((await agent.act({ steps })).ok).toBe(false)
  expect(onPromote).not.toHaveBeenCalled()
  approve = true
  const result = await agent.act({ steps: [steps.at(-1)!] })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onPromote).toHaveBeenCalledOnce())
})

test("the promote confirm isn't styled as destructive and has no typed name", async () => {
  const user = userEvent.setup()
  render(<PromoteBranch branch="dev" onPromote={async () => {}} />)
  await user.click(screen.getByRole("button", { name: "Promote" }))
  expect(screen.queryByLabelText("Type dev to confirm")).toBeNull()
  expect(screen.getByRole("button", { name: "Make default" }).className).not.toContain("bg-destructive")
})

test("resetting a branch needs its name and a person's approval", async () => {
  const onReset = vi.fn(async () => {})
  render(<ResetBranch branch="dev" parent="main" onReset={onReset} />)
  expect(named()).toContain("Reset")

  const steps = [
    { ref: "Reset", action: "press" },
    { ref: "Type dev to confirm", action: "setValue", args: ["dev"] },
    { ref: "Reset dev to main?", action: "press", args: ["Reset branch"] },
  ]
  approve = false
  expect((await agent.act({ steps })).ok).toBe(false)
  expect(onReset).not.toHaveBeenCalled()
  approve = true
  const result = await agent.act({ steps: [steps.at(-1)!] })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onReset).toHaveBeenCalledOnce())
})

test("Enter in the typed field confirms once the name matches", async () => {
  const user = userEvent.setup()
  const onReset = vi.fn(async () => {})
  render(<ResetBranch branch="dev" parent="main" onReset={onReset} />)
  await user.click(screen.getByRole("button", { name: "Reset" }))
  const field = screen.getByLabelText("Type dev to confirm")
  await user.type(field, "de{Enter}")
  expect(onReset).not.toHaveBeenCalled()
  await user.type(field, "v{Enter}")
  await waitFor(() => expect(onReset).toHaveBeenCalledOnce())
})

test("an agent changes a member's role in a workspace", async () => {
  const workspaces = [
    { id: "ws_aaaaaaaaaaaaaaaaaaaa", name: "default" },
    { id: "ws_bbbbbbbbbbbbbbbbbbbb", name: "agents" },
  ]
  const members: Member[] = [
    { id: "me", name: "Ann", email: "ann@example.com", owner: true, grants: [], createdAt: at },
    { id: "m1", name: "Bo", email: "bo@example.com", owner: false, grants: [{ workspace: "ws_aaaaaaaaaaaaaaaaaaaa", role: "readonly" }], createdAt: at },
  ]
  const onAccess = vi.fn(async (_m: Member, _a: MemberAccess) => {})
  render(<MemberTable members={members} me="me" workspaces={workspaces} onAccess={onAccess} onRemove={async () => {}} />)
  expect(named()).toContain("Change access for Bo")

  const opened = await agent.act({ steps: [{ ref: "Change access for Bo", action: "press" }] })
  expect(opened.ok, opened.error?.message).toBe(true)
  const open = named()
  for (const label of ["Access for Bo", "Access", "Workspaces", "Role in default", "Role in agents", "Save"]) expect(open).toContain(label)

  const result = await agent.act({
    steps: [
      { ref: "Role in default", action: "setValue", args: ["member"] },
      { ref: "Role in agents", action: "setValue", args: ["admin"] },
      { ref: "Access for Bo", action: "press", args: ["Save"] },
    ],
  })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onAccess).toHaveBeenCalledOnce())
  expect(onAccess).toHaveBeenCalledWith(members[1], {
    owner: false,
    grants: [
      { workspace: "ws_aaaaaaaaaaaaaaaaaaaa", role: "member" },
      { workspace: "ws_bbbbbbbbbbbbbbbbbbbb", role: "admin" },
    ],
  })
})

test("an agent makes a member an owner, and the workspace roles leave the page", async () => {
  const workspaces = [{ id: "ws_aaaaaaaaaaaaaaaaaaaa", name: "default" }]
  const members: Member[] = [
    { id: "me", name: "Ann", email: "ann@example.com", owner: true, grants: [], createdAt: at },
    { id: "m1", name: "Bo", email: "bo@example.com", owner: false, grants: [{ workspace: "ws_aaaaaaaaaaaaaaaaaaaa", role: "admin" }], createdAt: at },
  ]
  const onAccess = vi.fn(async (_m: Member, _a: MemberAccess) => {})
  render(<MemberTable members={members} me="me" workspaces={workspaces} onAccess={onAccess} onRemove={async () => {}} />)
  const result = await agent.act({
    steps: [
      { ref: "Change access for Bo", action: "press" },
      { ref: "Access", action: "setValue", args: ["Owner, every workspace"] },
    ],
  })
  expect(result.ok, result.error?.message).toBe(true)
  expect(named()).not.toContain("Role in default")
  expect(named()).not.toContain("Workspaces")
  const saved = await agent.act({ steps: [{ ref: "Access for Bo", action: "press", args: ["Save"] }] })
  expect(saved.ok, saved.error?.message).toBe(true)
  await waitFor(() => expect(onAccess).toHaveBeenCalledWith(members[1], { owner: true }))
})

const snapshot: SnapshotOut = {
  id: "s1",
  label: "before-migration",
  kind: "manual",
  branch_id: "b1",
  branch_name: "main",
  database_id: "db1",
  created_at: at,
  size_bytes: 2048,
}

test("an agent restores a snapshot to a new branch", async () => {
  const onRestore = vi.fn(async (_s: SnapshotOut, _name: string) => {})
  render(<SnapshotTable snapshots={[snapshot]} canWrite onRestore={onRestore} onDelete={async () => {}} />)
  expect(named()).toContain("Restore before-migration")

  const result = await agent.act({
    steps: [
      { ref: "Restore before-migration", action: "press" },
      { ref: "New branch name", action: "setValue", args: ["main-fix"] },
      { ref: "Restore before-migration", action: "press", args: ["Create branch"] },
    ],
  })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onRestore).toHaveBeenCalledWith(snapshot, "main-fix"))
})

test("deleting a snapshot needs a person's approval", async () => {
  const onDelete = vi.fn(async (_s: SnapshotOut) => {})
  render(<SnapshotTable snapshots={[snapshot]} canWrite onRestore={async () => {}} onDelete={onDelete} />)
  expect(named()).toContain("Delete before-migration")

  const steps = [
    { ref: "Delete before-migration", action: "press" },
    { ref: "Delete before-migration?", action: "press", args: ["Delete snapshot"] },
  ]
  approve = false
  expect((await agent.act({ steps })).ok).toBe(false)
  expect(onDelete).not.toHaveBeenCalled()
  approve = true
  const result = await agent.act({ steps: [steps.at(-1)!] })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onDelete).toHaveBeenCalledOnce())
})

test("replacing a branch's data on import needs its name and a person's approval", async () => {
  const user = userEvent.setup({ pointerEventsCheck: 0 })
  const onImport = vi.fn(async (_f: File, _mode: string) => {})
  const { container } = render(<ImportForm branch="main" onImport={onImport} />)
  const input = container.querySelector<HTMLInputElement>('input[type="file"]')!
  await user.upload(input, new File(["CREATE TABLE t (a);"], "dump.sql", { type: "application/sql" }))
  expect(named()).toContain("Mode")

  const mode = await agent.act({ steps: [{ ref: "Mode", action: "setValue", args: ["replace"] }] })
  expect(mode.ok, mode.error?.message).toBe(true)

  const steps = [
    { ref: "Import", action: "press" },
    { ref: "Type main to confirm", action: "setValue", args: ["main"] },
    { ref: "Replace main?", action: "press", args: ["Replace data"] },
  ]
  approve = false
  expect((await agent.act({ steps })).ok).toBe(false)
  expect(onImport).not.toHaveBeenCalled()
  approve = true
  const result = await agent.act({ steps: [steps.at(-1)!] })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onImport).toHaveBeenCalledWith(expect.any(File), "replace"))
})

test("an agent switches the branch in the frame", async () => {
  const branch = (name: string, isDefault = false): BranchOut => ({
    id: name,
    name,
    default: isDefault,
    protected: isDefault,
    parent_id: "",
    database_id: "db1",
    created_at: at,
    expires_at: null,
    size_bytes: 0,
    source_snapshot_id: "",
    updated_at: at,
  })
  const onChange = vi.fn()
  render(<BranchPicker branches={[branch("main", true), branch("dev")]} value="main" onChange={onChange} />)
  expect(named()).toContain("Branch")

  const result = await agent.act({ steps: [{ ref: "Branch", action: "setValue", args: ["dev"] }] })
  expect(result.ok, result.error?.message).toBe(true)
  expect(onChange).toHaveBeenCalledWith("dev")
})
