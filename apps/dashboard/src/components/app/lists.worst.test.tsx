import { render, screen } from "@testing-library/react"
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

test("tokens: long names, a deleted database, empty and 500 rows", () => {
  const { unmount } = render(<TokenTable tokens={worst.tokens(3)} names={worst.tokenNames} onRevoke={noop} />)
  readable(worst.LONG_TOKEN)
  readable(`${worst.tokenNames.databases.db1} / ${worst.LONG_BRANCH}`)
  expect(screen.getByText("gone")).toBeTruthy()
  unmount()
  render(<TokenTable tokens={[]} names={worst.tokenNames} onRevoke={noop} />)
  expect(screen.getByText("No tokens yet")).toBeTruthy()
  const many = render(<TokenTable tokens={worst.tokens(500)} names={worst.tokenNames} onRevoke={noop} />)
  expect(many.container.querySelectorAll("tbody tr")).toHaveLength(500)
})

test("members: a missing name falls back to the email, and you can't act on yourself", () => {
  render(<MemberTable members={worst.members(3)} me="me" onRole={noop} onRemove={noop} />)
  expect(screen.getAllByText(worst.LONG_EMAIL)).toHaveLength(2)
  readable(worst.LONG_EMAIL)
  expect(screen.queryByRole("button", { name: /^Remove Aleksandra/ })).toBeNull()
  expect(screen.queryByRole("combobox", { name: /Aleksandra/ })).toBeNull()
  expect(screen.getByRole("combobox", { name: `Role for ${worst.LONG_EMAIL}` })).toBeTruthy()
  expect(screen.getByRole("button", { name: "Remove Jo" })).toBeTruthy()
})

test("invites: long email and the empty state", () => {
  const { unmount } = render(<InviteTable invites={worst.invites} onResend={async () => ""} onCancel={noop} onChanged={() => {}} />)
  readable(worst.LONG_EMAIL)
  unmount()
  render(<InviteTable invites={[]} onResend={async () => ""} onCancel={noop} onChanged={() => {}} />)
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
