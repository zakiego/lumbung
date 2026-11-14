import type { TokenOut } from "@lumbung/client"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"
import { CreateTokenDialog, type NewToken, TokenTable } from "./tokens"

let approve = false
const agent = createAgent({ allow: (step) => !step.confirm || approve })

const SECRET = "lmb_abc123_s3cr3tvalue"

function Create({ onCreate }: { onCreate: (t: NewToken) => Promise<{ token: string }> }) {
  return <CreateTokenDialog databases={["shop", "blog"]} loadBranches={async () => ["main", "preview"]} onCreate={onCreate} />
}

test("a person creates a branch-limited token and sees it once", async () => {
  const user = userEvent.setup()
  const onCreate = vi.fn(async (_t: NewToken) => ({ token: SECRET }))
  render(<Create onCreate={onCreate} />)
  const trigger = screen.getByRole("button", { name: "New token" })

  await user.click(trigger)
  expect(screen.getByRole("dialog", { name: "Create token" })).toBeTruthy()
  expect(screen.queryByRole("combobox", { name: "Branch" })).toBeNull()

  await user.click(screen.getByRole("button", { name: "Create token" }))
  expect(onCreate).not.toHaveBeenCalled()

  await user.type(screen.getByLabelText("Token name"), "ci deploy")
  await user.click(screen.getByRole("combobox", { name: "Reaches" }))
  await user.click(screen.getByRole("option", { name: "shop" }))
  await user.click(await screen.findByRole("combobox", { name: "Branch" }))
  await user.click(await screen.findByRole("option", { name: "preview" }))
  await user.click(screen.getByRole("combobox", { name: "Access" }))
  await user.click(screen.getByRole("option", { name: "Read and write" }))
  await user.type(screen.getByLabelText("Expires in (days)"), "30")
  await user.click(screen.getByRole("button", { name: "Create token" }))

  await waitFor(() => expect(onCreate).toHaveBeenCalledOnce())
  expect(onCreate.mock.calls[0]?.[0]).toEqual({ name: "ci deploy", access: "write", database: "shop", branch: "preview", expiresInDays: 30 })
  expect(await screen.findByText(SECRET)).toBeTruthy()
  expect(screen.getByText("Copy it now. It won't be shown again.")).toBeTruthy()
  expect(screen.getByRole("button", { name: "Copy token" })).toBeTruthy()

  await user.click(screen.getByRole("button", { name: "Done" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(trigger)

  await user.click(trigger)
  expect(screen.queryByText(SECRET)).toBeNull()
  expect((screen.getByLabelText("Token name") as HTMLInputElement).value).toBe("")
})

test("a server error stays in the dialog", async () => {
  const user = userEvent.setup()
  render(
    <Create
      onCreate={async () => {
        throw new Error("A token named ci already exists.")
      }}
    />,
  )
  await user.click(screen.getByRole("button", { name: "New token" }))
  await user.type(screen.getByLabelText("Token name"), "ci")
  await user.click(screen.getByRole("button", { name: "Create token" }))
  expect(await screen.findByText("A token named ci already exists.")).toBeTruthy()
  expect(screen.getByRole("dialog", { name: "Create token" })).toBeTruthy()
})

test("an agent can create a token but never read it", async () => {
  const onCreate = vi.fn(async (_t: NewToken) => ({ token: SECRET }))
  render(<Create onCreate={onCreate} />)

  const closed = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of closed) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  expect(closed.map((e) => e.label)).toContain("New token")

  const opened = await agent.act({ steps: [{ ref: "New token", action: "press" }] })
  expect(opened.ok, opened.error?.message).toBe(true)
  const open = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of open) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  const labels = open.map((e) => e.label)
  for (const label of ["Create token", "Token name", "Reaches", "Access", "Expires in (days)"]) expect(labels).toContain(label)

  const result = await agent.act({
    steps: [
      { ref: "Token name", action: "setValue", args: ["agent-made"] },
      { ref: "Reaches", action: "setValue", args: ["blog"] },
      { ref: "Access", action: "setValue", args: ["Admin"] },
      { ref: "Create token", action: "press", args: ["Create token"] },
    ],
  })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onCreate).toHaveBeenCalledOnce())
  expect(onCreate.mock.calls[0]?.[0]).toMatchObject({ name: "agent-made", access: "admin", database: "blog" })
  expect(await screen.findByText(SECRET)).toBeTruthy()
  expect(JSON.stringify(agent.snapshot())).not.toContain(SECRET)
})

const token = (t: Partial<TokenOut>): TokenOut => ({
  id: "t1",
  name: "ci",
  scope: "database",
  access: "read",
  database_id: "db1",
  branch_id: "",
  created_at: "2026-11-01T00:00:00Z",
  expires_at: null,
  last_used_at: null,
  service: false,
  ...t,
})

test("the service token can't be revoked, and revoking needs a person's yes", async () => {
  const onRevoke = vi.fn(async () => {})
  render(
    <TokenTable
      tokens={[token({ id: "svc", name: "dashboard", scope: "instance", access: "admin", service: true }), token({ id: "t2", name: "ci", branch_id: "b1" })]}
      names={{ databases: { db1: "shop" }, branches: { b1: "preview" } }}
      onRevoke={onRevoke}
    />,
  )
  expect(screen.getByText("service")).toBeTruthy()
  expect(screen.queryByRole("button", { name: "Revoke dashboard" })).toBeNull()
  expect(screen.getByText("shop / preview")).toBeTruthy()

  const revoke = { steps: [{ ref: "Revoke ci", action: "press", args: ["Revoke token"] }] }
  approve = false
  expect((await agent.act(revoke)).ok).toBe(false)
  expect(onRevoke).not.toHaveBeenCalled()
  approve = true
  const result = await agent.act(revoke)
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onRevoke).toHaveBeenCalledOnce())
})
