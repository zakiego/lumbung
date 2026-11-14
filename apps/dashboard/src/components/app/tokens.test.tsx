import type { TokenOut } from "@lumbung/client"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"
import { type CreatedToken, CreateTokenDialog, type NewToken, TokenTable } from "./tokens"

let approve = false
const agent = createAgent({ allow: (step) => !step.confirm || approve })

const SECRET = "lmb_abc123_s3cr3tvalue"

function Create({ onCreate }: { onCreate: (t: NewToken) => Promise<CreatedToken> }) {
  return <CreateTokenDialog databases={["shop", "blog"]} buckets={["photos"]} loadBranches={async () => ["main", "preview"]} onCreate={onCreate} />
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
  expect(screen.getByRole("checkbox", { name: "S3 access key" })).toBeTruthy()
  await user.click(screen.getByRole("combobox", { name: "Reaches" }))
  await user.click(screen.getByRole("option", { name: "shop" }))
  expect(screen.queryByRole("checkbox", { name: "S3 access key" })).toBeNull()
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

test("a bucket token with an access key shows all three values once", async () => {
  const user = userEvent.setup()
  const created = { token: SECRET, access_key_id: "LMBABC123", secret_access_key: "s3-secret-value" }
  const onCreate = vi.fn(async (_t: NewToken) => created)
  render(<Create onCreate={onCreate} />)

  await user.click(screen.getByRole("button", { name: "New token" }))
  await user.type(screen.getByLabelText("Token name"), "uploads")
  await user.click(screen.getByRole("combobox", { name: "Reaches" }))
  expect(screen.getByRole("group", { name: "Buckets" })).toBeTruthy()
  await user.click(screen.getByRole("option", { name: "photos" }))
  expect(screen.queryByRole("combobox", { name: "Branch" })).toBeNull()
  await user.click(screen.getByRole("checkbox", { name: "S3 access key" }))
  await user.click(screen.getByRole("button", { name: "Create token" }))

  await waitFor(() => expect(onCreate).toHaveBeenCalledOnce())
  expect(onCreate.mock.calls[0]?.[0]).toEqual({ name: "uploads", access: "read", bucket: "photos", accessKey: true })
  for (const value of Object.values(created)) expect(await screen.findByText(value)).toBeTruthy()
  for (const label of ["Copy token", "Copy access key ID", "Copy secret access key"]) expect(screen.getByRole("button", { name: label })).toBeTruthy()
})

test("one token reaches a database and a bucket, each at its own level", async () => {
  const user = userEvent.setup()
  const onCreate = vi.fn(async (_t: NewToken) => ({ token: SECRET }))
  render(<CreateTokenDialog databases={["shop", "blog"]} buckets={["photos"]} mcpUrl="https://db.example.com/mcp" loadBranches={async () => ["main", "preview"]} onCreate={onCreate} />)

  await user.click(screen.getByRole("button", { name: "New token" }))
  await user.type(screen.getByLabelText("Token name"), "agent")
  // Everything already reaches it all, so there is nothing to add.
  expect(screen.queryByRole("button", { name: "Add another" })).toBeNull()
  await user.click(screen.getByRole("combobox", { name: "Reaches" }))
  await user.click(screen.getByRole("option", { name: "shop" }))
  await user.click(screen.getByRole("combobox", { name: "Access" }))
  await user.click(screen.getByRole("option", { name: "Read and write" }))
  expect(screen.queryByRole("button", { name: "Remove reach 1" })).toBeNull()

  await user.click(screen.getByRole("button", { name: "Add another" }))
  // The new row starts on the bucket no row has yet, at read.
  expect(screen.getByRole("combobox", { name: "Reaches 2" }).textContent).toContain("Bucket photos")
  expect(screen.getByRole("combobox", { name: "Access 2" }).textContent).toContain("Read")
  expect(screen.getByRole("checkbox", { name: "S3 access key" })).toBeTruthy()
  // Everything is only offered while it is the one row.
  await user.click(screen.getByRole("combobox", { name: "Reaches" }))
  expect(screen.queryByRole("option", { name: "Everything" })).toBeNull()
  expect(screen.queryByRole("option", { name: "photos" })).toBeNull()
  await user.keyboard("{Escape}")

  await user.click(screen.getByRole("button", { name: "Add another" }))
  await user.click(screen.getByRole("button", { name: "Remove reach 3" }))
  expect(screen.queryByRole("combobox", { name: "Reaches 3" })).toBeNull()
  // The removed row took focus with it, so it moves to the next thing to do.
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Add another" })))

  await user.click(screen.getByRole("button", { name: "Create token" }))
  await waitFor(() => expect(onCreate).toHaveBeenCalledOnce())
  expect(onCreate.mock.calls[0]?.[0]).toEqual({
    name: "agent",
    grants: [
      { access: "write", database: "shop" },
      { access: "read", bucket: "photos" },
    ],
  })
  expect(await screen.findByText(SECRET)).toBeTruthy()
  expect(screen.getByText("https://db.example.com/mcp")).toBeTruthy()
  for (const label of ["Copy token", "Copy MCP URL"]) expect(screen.getByRole("button", { name: label })).toBeTruthy()
})

test("two rows of one database need a branch each", async () => {
  const user = userEvent.setup()
  const onCreate = vi.fn(async (_t: NewToken) => ({ token: SECRET }))
  render(<CreateTokenDialog databases={["shop"]} loadBranches={async () => ["main", "preview"]} onCreate={onCreate} />)

  await user.click(screen.getByRole("button", { name: "New token" }))
  await user.type(screen.getByLabelText("Token name"), "branches")
  await user.click(screen.getByRole("combobox", { name: "Reaches" }))
  await user.click(screen.getByRole("option", { name: "shop" }))
  // With every database taken, the next row offers the first again, for another branch.
  await user.click(screen.getByRole("button", { name: "Add another" }))
  expect(screen.getByRole("combobox", { name: "Reaches 2" }).textContent).toContain("shop")
  await user.click(screen.getByRole("button", { name: "Create token" }))
  expect(await screen.findByText("Pick a different branch for each row of the same database.")).toBeTruthy()
  expect(onCreate).not.toHaveBeenCalled()

  // One row on a branch next to one on every branch still clashes, and so does the same branch twice.
  await user.click(await screen.findByRole("combobox", { name: "Branch" }))
  await user.click(await screen.findByRole("option", { name: "main" }))
  await user.click(screen.getByRole("button", { name: "Create token" }))
  await user.click(screen.getByRole("combobox", { name: "Branch 2" }))
  await user.click(await screen.findByRole("option", { name: "main" }))
  await user.click(screen.getByRole("button", { name: "Create token" }))
  expect(onCreate).not.toHaveBeenCalled()
  expect(screen.getByText("Pick a different branch for each row of the same database.")).toBeTruthy()
  await user.click(screen.getByRole("combobox", { name: "Branch 2" }))
  await user.click(await screen.findByRole("option", { name: "preview" }))
  await user.click(screen.getByRole("combobox", { name: "Access 2" }))
  await user.click(screen.getByRole("option", { name: "Read and write" }))
  await user.click(screen.getByRole("button", { name: "Create token" }))
  await waitFor(() => expect(onCreate).toHaveBeenCalledOnce())
  expect(onCreate.mock.calls[0]?.[0]).toEqual({
    name: "branches",
    grants: [
      { access: "read", database: "shop", branch: "main" },
      { access: "write", database: "shop", branch: "preview" },
    ],
  })
})

test("an agent can give one token a database and a bucket", async () => {
  const onCreate = vi.fn(async (_t: NewToken) => ({ token: SECRET }))
  render(<CreateTokenDialog databases={["shop", "blog"]} buckets={["photos"]} mcpUrl="https://db.example.com/mcp" loadBranches={async () => ["main", "preview"]} onCreate={onCreate} />)

  const first = await agent.act({
    steps: [
      { ref: "New token", action: "press" },
      { ref: "Token name", action: "setValue", args: ["agent-made"] },
      { ref: "Reaches", action: "setValue", args: ["blog"] },
      { ref: "Access", action: "setValue", args: ["Read and write"] },
      { ref: "Add another", action: "press" },
    ],
  })
  expect(first.ok, first.error?.message).toBe(true)
  const open = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of open) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  for (const label of ["Reaches 2", "Access 2", "Remove reach 2", "S3 access key"]) expect(open.map((e) => e.label)).toContain(label)

  const result = await agent.act({
    steps: [
      { ref: "Access 2", action: "setValue", args: ["Admin"] },
      { ref: "Create token", action: "press", args: ["Create token"] },
    ],
  })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onCreate).toHaveBeenCalledOnce())
  expect(onCreate.mock.calls[0]?.[0]).toEqual({
    name: "agent-made",
    grants: [
      { access: "write", database: "blog" },
      { access: "admin", bucket: "photos" },
    ],
  })
  // The person sees the token and the URL to connect to; the agent never reads the token.
  await screen.findByText(SECRET)
  expect(screen.getByText("https://db.example.com/mcp")).toBeTruthy()
  const after = JSON.stringify(agent.snapshot())
  expect(after).not.toContain(SECRET)
})

test("removing a row goes back to a plain token", async () => {
  const user = userEvent.setup()
  const onCreate = vi.fn(async (_t: NewToken) => ({ token: SECRET }))
  render(<Create onCreate={onCreate} />)

  await user.click(screen.getByRole("button", { name: "New token" }))
  await user.type(screen.getByLabelText("Token name"), "files")
  await user.click(screen.getByRole("combobox", { name: "Reaches" }))
  await user.click(screen.getByRole("option", { name: "blog" }))
  await user.click(screen.getByRole("button", { name: "Add another" }))
  await user.click(screen.getByRole("button", { name: "Remove reach 1" }))
  expect(screen.getByRole("combobox", { name: "Reaches" }).textContent).toContain("Bucket photos")
  expect(screen.queryByRole("button", { name: "Remove reach 1" })).toBeNull()
  await user.click(screen.getByRole("button", { name: "Create token" }))

  await waitFor(() => expect(onCreate).toHaveBeenCalledOnce())
  expect(onCreate.mock.calls[0]?.[0]).toEqual({ name: "files", access: "read", bucket: "photos" })
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
  for (const label of ["Create token", "Token name", "Reaches", "Access", "Expires in (days)", "S3 access key"]) expect(labels).toContain(label)

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
  bucket_id: "",
  grants: [],
  access_key_id: "",
  created_at: "2026-11-01T00:00:00Z",
  expires_at: null,
  last_used_at: null,
  service: false,
  ...t,
})

test("the service token can't be revoked", () => {
  render(
    <TokenTable
      tokens={[token({ id: "svc", name: "dashboard", scope: "instance", access: "admin", service: true }), token({ id: "t2", name: "ci", branch_id: "b1" }), token({ id: "t3", name: "uploads", scope: "bucket", database_id: "", bucket_id: "bk1" })]}
      names={{ databases: { db1: "shop" }, branches: { b1: "preview" }, buckets: { bk1: "photos" } }}
      onRevoke={async () => {}}
    />,
  )
  expect(screen.getByText("service")).toBeTruthy()
  expect(screen.queryByRole("button", { name: "Revoke dashboard" })).toBeNull()
  expect(screen.getByText("shop / preview")).toBeTruthy()
  expect(screen.getByText("Bucket photos")).toBeTruthy()
})

test("a token with several grants lists each reach with its own level", () => {
  const grants = [
    { database_id: "db1", branch_id: "", bucket_id: "", access: "write" as const },
    { database_id: "", branch_id: "", bucket_id: "bk1", access: "read" as const },
  ]
  render(<TokenTable tokens={[token({ id: "t4", name: "agent", scope: "grants", access: "write", database_id: "", grants })]} names={{ databases: { db1: "shop" }, branches: {}, buckets: { bk1: "photos" } }} onRevoke={async () => {}} />)
  const row = screen.getByText("agent").closest("tr")!
  const lines = (cell: number) => [...row.querySelectorAll("td")[cell]!.querySelectorAll("li")].map((li) => li.textContent)
  expect(lines(1)).toEqual(["shop", "Bucket photos"])
  expect(lines(2)).toEqual(["Read and write", "Read"])
})

test("revoking needs a person's yes", async () => {
  const onRevoke = vi.fn(async () => {})
  render(
    <TokenTable
      tokens={[token({ id: "t2", name: "ci", branch_id: "b1" })]}
      names={{ databases: { db1: "shop" }, branches: { b1: "preview" }, buckets: { bk1: "photos" } }}
      onRevoke={onRevoke}
    />,
  )

  const revoke = { steps: [{ ref: "Revoke ci", action: "press", args: ["Revoke token"] }] }
  approve = false
  expect((await agent.act(revoke)).ok).toBe(false)
  expect(onRevoke).not.toHaveBeenCalled()
  approve = true
  const result = await agent.act(revoke)
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onRevoke).toHaveBeenCalledOnce())
})
