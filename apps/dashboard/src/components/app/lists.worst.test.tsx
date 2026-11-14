import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import * as worst from "@/test/worst-case"
import { AuditTable, actorOf } from "./audit"
import { InviteTable, MemberTable } from "./members"
import { ScheduleCard, SnapshotTable, scheduleText } from "./snapshots"
import { TokenTable } from "./tokens"

const noop = async () => {}

// Truncated text must stay readable through its title.
function readable(text: string) {
  const el = screen.getAllByText(text)[0]!
  expect(el.closest("[title]")?.getAttribute("title")).toBe(text)
}

// The same for text made of several elements, like a summary with names in mono.
function titled(text: string) {
  const el = [...document.querySelectorAll<HTMLElement>(`[title="${text}"]`)].find((e) => e.textContent === text)
  expect(el, text).toBeTruthy()
  expect(el!.className).toContain("truncate")
}

test("tokens: long names, a deleted database, empty and 500 rows", async () => {
  const { unmount } = render(<TokenTable tokens={worst.tokens(3)} names={worst.tokenNames} onRevoke={noop} />)
  readable(worst.LONG_TOKEN)
  readable(`${worst.tokenNames.databases.db1} / ${worst.LONG_BRANCH}`)
  expect(screen.getByText("gone")).toBeTruthy()
  unmount()
  // 20 grants: three lines, the rest behind a count that names them on hover.
  const grants = render(<TokenTable tokens={worst.tokens(4).slice(3)} names={worst.tokenNames} onRevoke={noop} />)
  const cells = grants.container.querySelectorAll("tbody td")
  expect([...cells[1]!.querySelectorAll("li")].map((li) => li.textContent)).toEqual([
    `${worst.tokenNames.databases.db1} / ${worst.LONG_BRANCH}`,
    `Bucket ${worst.LONG_BUCKET}`,
    "gone",
    "17 more",
  ])
  expect([...cells[2]!.querySelectorAll("li")].map((li) => li.textContent)).toEqual(["Read and write", "Admin", "Read"])
  // All 20 open in a popover, in full, so they can be read by touch and keyboard too.
  await userEvent.setup().click(screen.getByRole("button", { name: "17 more" }))
  const all = await screen.findByRole("dialog", { name: "17 more" })
  expect(all.querySelectorAll("li")).toHaveLength(20)
  expect(all.textContent).toContain(`${worst.tokenNames.databases.db1} / ${worst.LONG_BRANCH}Read and write`)
  expect(all.textContent).toContain("Bucket gone-bucketRead and write")
  grants.unmount()
  render(<TokenTable tokens={[]} names={worst.tokenNames} onRevoke={noop} />)
  expect(screen.getByText("No tokens yet")).toBeTruthy()
  const many = render(<TokenTable tokens={worst.tokens(500)} names={worst.tokenNames} onRevoke={noop} />)
  expect(many.container.querySelectorAll("tbody tr")).toHaveLength(500)
})

test("members: a missing name falls back to the email, and you can't act on yourself", () => {
  render(<MemberTable members={worst.members(3)} me="me" workspaces={worst.workspaces} onAccess={noop} onRemove={noop} />)
  expect(screen.getAllByText(worst.LONG_EMAIL)).toHaveLength(2)
  readable(worst.LONG_EMAIL)
  expect(screen.queryByRole("button", { name: /^Remove Aleksandra/ })).toBeNull()
  expect(screen.queryByRole("button", { name: /Aleksandra/ })).toBeNull()
  expect(screen.queryByRole("combobox")).toBeNull()
  expect(screen.getByRole("button", { name: `Change access for ${worst.LONG_EMAIL}` })).toBeTruthy()
  expect(screen.getByRole("button", { name: "Change access for Jo" })).toBeTruthy()
  expect(screen.getByRole("button", { name: "Remove Jo" })).toBeTruthy()
})

test("members: a role in every workspace, a 63 character workspace name, no workspace at all, and 500 rows", () => {
  const { unmount } = render(<MemberTable members={worst.members(3)} me="me" workspaces={worst.workspaces} onAccess={noop} onRemove={noop} />)
  const cells = [...document.querySelectorAll("tbody tr")].map((r) => r.querySelectorAll("td")[1]!)
  expect(cells.map((c) => c.textContent)).toEqual(["Owner", worst.MANY_ROLES_SUMMARY, "No workspace"])
  // The summary is one truncated line, read in full through its title. The role in a workspace that is gone isn't in it.
  for (const c of cells) expect(c.firstElementChild?.getAttribute("title")).toBe(c.textContent)
  titled(worst.MANY_ROLES_SUMMARY)
  expect(worst.MANY_ROLES_SUMMARY).not.toContain("ws_")
  expect([...cells[1]!.querySelectorAll(".font-mono")].map((m) => m.textContent)).toEqual(["default", worst.LONG_WORKSPACE, "x"])
  // On a narrow screen the email wraps and the summary sits under it.
  const email = screen.getAllByText(worst.LONG_EMAIL).find((e) => e.getAttribute("title") === worst.LONG_EMAIL && e.className.includes("text-xs"))!
  expect(email.className).toContain("[overflow-wrap:anywhere]")
  expect(email.nextElementSibling?.textContent).toBe(worst.MANY_ROLES_SUMMARY)
  expect(email.nextElementSibling?.className).toContain("sm:hidden")
  unmount()
  const many = render(<MemberTable members={worst.members(500)} me="me" workspaces={worst.workspaces} onAccess={noop} onRemove={noop} />)
  expect(many.container.querySelectorAll("tbody tr")).toHaveLength(500)
  expect(screen.getAllByRole("button", { name: /^Change access for / })).toHaveLength(499)
})

test("invites: long email, long access and the empty state", () => {
  const { unmount } = render(<InviteTable invites={worst.invites} workspaces={worst.workspaces} onResend={async () => ""} onCancel={noop} onChanged={() => {}} />)
  readable(worst.LONG_EMAIL)
  titled(worst.MANY_ROLES_SUMMARY)
  unmount()
  const owner = render(<InviteTable invites={[{ ...worst.invites[0]!, owner: true, grants: [] }]} workspaces={worst.workspaces} onResend={async () => ""} onCancel={noop} onChanged={() => {}} />)
  readable("Owner")
  owner.unmount()
  render(<InviteTable invites={[]} workspaces={worst.workspaces} onResend={async () => ""} onCancel={noop} onChanged={() => {}} />)
  expect(screen.getByText("No open invites")).toBeTruthy()
})

test("audit: token actors, long targets, errors and the empty state", () => {
  const { unmount } = render(<AuditTable entries={worst.audit(3)} />)
  readable(worst.LONG_EMAIL)
  readable(`db_01JCX9/br_01JCXA/${worst.LONG_BRANCH}`)
  expect(screen.getByText("tok_01JCX9ZK4M2")).toBeTruthy()
  expect(screen.getByText("branch_name_taken")).toBeTruthy()
  expect(screen.getByText("unknown")).toBeTruthy()
  expect(actorOf(worst.audit(1)[0]!)).toEqual({ text: worst.LONG_EMAIL, token: false })
  unmount()
  render(<AuditTable entries={[]} />)
  expect(screen.getByText("Nothing logged yet")).toBeTruthy()
})

test("snapshots: a 200 character label, no label, and counts of one", () => {
  render(<SnapshotTable snapshots={worst.snapshots(2)} canWrite onRestore={noop} onDelete={noop} />)
  readable(worst.LONG_LABEL)
  expect(screen.getByText("5.00 GB")).toBeTruthy()
  expect(screen.getByText(/^Scheduled /)).toBeTruthy()
  expect(scheduleText(worst.schedule)).toBe("Every day, keeps last 1 and 1 day")
  expect(scheduleText({ ...worst.schedule, every_minutes: 90, keep_count: 0, keep_days: 0 })).toBe("Every 90 min")
  render(<ScheduleCard schedule={worst.schedule} canWrite onSave={noop} onRemove={noop} />)
  expect(screen.getByText("Hasn't run yet")).toBeTruthy()
})
