import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { LumbungError } from "@lumbung/client"
import { afterEach, beforeEach, expect, test, vi } from "vitest"
import { resetAccess, setGrants } from "./access"
import { resetAuth } from "./auth"
import { resetConfig } from "./config"
import { getDb, setDb } from "./db"
import { forgetMinted, recordMinted, revokeOutlived } from "./minted"
import { acceptInvite, createInvite, createOwner, removeMember, setAccess } from "./users"

// What the engine was asked to revoke, and what it refuses.
const engine = vi.hoisted(() => ({ revoked: [] as string[], missing: new Set<string>(), trashed: new Set<string>() }))

vi.mock("./engine", async (original) => ({
  ...(await original<typeof import("./engine")>()),
  engineFor: (actor: { owner: boolean }) => {
    if (!actor.owner) throw new Error("engineFor: only an owner acts on the instance")
    const revoke = (where: string) => async (id: string) => {
      if (engine.trashed.has(where)) throw new LumbungError("workspace_not_found", 404, "no such workspace")
      if (engine.missing.has(id)) throw new LumbungError("token_not_found", 404, "no such token")
      engine.revoked.push(`${where}:${id}`)
    }
    return {
      instance: { tokens: { revoke: revoke("instance") } },
      workspace: (ws: string) => ({ tokens: { revoke: revoke(ws) } }),
    }
  },
}))

const A = "ws_aaaaaaaaaaaaaaaaaaaa"
const B = "ws_bbbbbbbbbbbbbbbbbbbb"
const password = "correct horse battery"
let dir = ""

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), "lumbung-dashboard-"))
  process.env.LUMBUNG_DASHBOARD_DATA_DIR = dir
  process.env.LUMBUNG_AUTH_SECRET = "test-secret-that-is-long-enough-for-auth"
  process.env.LUMBUNG_DASHBOARD_URL = "http://localhost:4871"
  process.env.LUMBUNG_SERVICE_TOKEN = "lmb_service_testsecret"
  resetConfig()
  resetAuth()
  resetAccess()
  setDb(undefined)
  engine.revoked = []
  engine.missing.clear()
  engine.trashed.clear()
})

afterEach(() => {
  getDb().close()
  setDb(undefined)
  resetAuth()
  rmSync(dir, { recursive: true, force: true })
})

async function people() {
  const o = await createOwner({ email: "owner@example.com", name: "Ana", password })
  const join = async (email: string, grants: { workspace: string; role: "admin" | "member" | "readonly" }[]) => {
    const { token } = await createInvite(o.id, email, { owner: false, grants })
    return (await acceptInvite(token, { name: email, password })).user.id
  }
  return { owner: { id: o.id, email: "owner@example.com", owner: true }, o: o.id, admin: await join("admin@example.com", [{ workspace: A, role: "admin" }, { workspace: B, role: "admin" }]) }
}

const recorded = () => (getDb().prepare(`SELECT token_id FROM minted_token ORDER BY token_id`).all() as { token_id: string }[]).map((r) => r.token_id)

test("tokens stay while their maker is still an admin of the workspace, or an owner", async () => {
  const { owner, o, admin } = await people()
  recordMinted("tok_a", A, admin)
  recordMinted("tok_owner", A, o)
  recordMinted("tok_instance", null, o)
  expect(await revokeOutlived(owner)).toBe(0)
  expect(engine.revoked).toEqual([])
  expect(recorded()).toEqual(["tok_a", "tok_instance", "tok_owner"])
})

test("lowering someone in one workspace revokes what they made there and nothing else", async () => {
  const { owner, o, admin } = await people()
  recordMinted("tok_a", A, admin)
  recordMinted("tok_b", B, admin)
  await setAccess(o, admin, { owner: false, grants: [{ workspace: A, role: "member" }, { workspace: B, role: "admin" }] })
  expect(await revokeOutlived(owner)).toBe(1)
  expect(engine.revoked).toEqual([`${A}:tok_a`])
  expect(recorded()).toEqual(["tok_b"])
})

test("removing someone revokes everything they made", async () => {
  const { owner, o, admin } = await people()
  recordMinted("tok_a", A, admin)
  recordMinted("tok_b", B, admin)
  await removeMember(o, admin)
  expect(await revokeOutlived(owner)).toBe(2)
  expect(engine.revoked.sort()).toEqual([`${A}:tok_a`, `${B}:tok_b`])
  expect(recorded()).toEqual([])
})

test("an owner who stops being one loses their instance tokens, and keeps what they are still an admin for", async () => {
  const { owner, o } = await people()
  const second = await createInvite(o, "second@example.com", { owner: true })
  const two = (await acceptInvite(second.token, { name: "Second", password })).user.id
  recordMinted("tok_instance", null, two)
  recordMinted("tok_a", A, two)
  recordMinted("tok_b", B, two)
  await setAccess(o, two, { owner: false, grants: [{ workspace: A, role: "admin" }] })
  expect(await revokeOutlived(owner)).toBe(2)
  expect(engine.revoked.sort()).toEqual([`${B}:tok_b`, "instance:tok_instance"].sort())
  expect(recorded()).toEqual(["tok_a"])
})

test("a token already gone is forgotten, and one in a deleted workspace waits for the restore", async () => {
  const { owner, o, admin } = await people()
  recordMinted("tok_gone", A, admin)
  recordMinted("tok_trashed", B, admin)
  engine.missing.add("tok_gone")
  engine.trashed.add(B)
  await removeMember(o, admin)
  expect(await revokeOutlived(owner)).toBe(0)
  expect(recorded()).toEqual(["tok_trashed"])

  // The workspace is restored, with its tokens: this one doesn't come back.
  engine.trashed.delete(B)
  expect(await revokeOutlived(owner)).toBe(1)
  expect(engine.revoked).toEqual([`${B}:tok_trashed`])
  expect(recorded()).toEqual([])
})

test("only an owner can run it, and a token revoked by hand is forgotten", async () => {
  const { admin } = await people()
  recordMinted("tok_a", A, admin)
  await expect(revokeOutlived({ email: "admin@example.com", owner: false })).rejects.toThrow(/only an owner/)
  forgetMinted("tok_a")
  expect(recorded()).toEqual([])
  setGrants(admin, [])
})
