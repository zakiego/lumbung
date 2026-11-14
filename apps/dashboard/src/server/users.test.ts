import { createHash } from "node:crypto"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, test } from "vitest"
import type { WorkspaceGrant } from "@/lib/roles"
import { grantsOf } from "./access"
import { getAuth, resetAuth } from "./auth"
import { resetConfig } from "./config"
import { getDb, migrateOwn, setDb } from "./db"
import {
  type MemberAccess,
  UserError,
  acceptInvite,
  createInvite,
  createOwner,
  findInvite,
  listInvites,
  listMembers,
  removeMember,
  setAccess,
  userCount,
} from "./users"

let dir = ""

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), "lumbung-dashboard-"))
  process.env.LUMBUNG_DASHBOARD_DATA_DIR = dir
  process.env.LUMBUNG_AUTH_SECRET = "test-secret-that-is-long-enough-for-auth"
  process.env.LUMBUNG_DASHBOARD_URL = "http://localhost:4871"
  resetConfig()
  resetAuth()
  setDb(undefined)
})

afterEach(() => {
  getDb().close()
  setDb(undefined)
  resetAuth()
  rmSync(dir, { recursive: true, force: true })
})

const password = "correct horse battery"

const WS_A = "ws_aaaaaaaaaaaaaaaaaaaa"
const WS_B = "ws_bbbbbbbbbbbbbbbbbbbb"
const WS_C = "ws_cccccccccccccccccccc"

const OWNER: MemberAccess = { owner: true }
const inA = (role: WorkspaceGrant["role"] = "member"): MemberAccess => ({ owner: false, grants: [{ workspace: WS_A, role }] })

async function owner() {
  return createOwner({ email: "Owner@Example.com", name: "Ana", password })
}

async function member(ownerId: string, email = "ben@example.com", access: MemberAccess = inA()) {
  const { token } = await createInvite(ownerId, email, access)
  const { user } = await acceptInvite(token, { name: "Ben", password })
  return user
}

async function sessionCount(userId: string) {
  const row = getDb().prepare(`SELECT COUNT(*) AS n FROM session WHERE userId = ?`).get(userId) as { n: number }
  return Number(row.n)
}

function roleOf(userId: string) {
  return (getDb().prepare(`SELECT role FROM "user" WHERE id = ?`).get(userId) as { role: string }).role
}

function grantRows(userId: string) {
  const row = getDb().prepare(`SELECT COUNT(*) AS n FROM workspace_member WHERE user_id = ?`).get(userId) as { n: number }
  return Number(row.n)
}

describe("first-run setup", () => {
  test("creates one owner, and only while there are no users", async () => {
    const user = await owner()
    expect(user.email).toBe("owner@example.com")
    expect(await userCount()).toBe(1)
    await expect(createOwner({ email: "eve@example.com", name: "Eve", password })).rejects.toThrow(UserError)
    expect(await userCount()).toBe(1)
  })

  test("two setups at once still make one owner", async () => {
    const results = await Promise.allSettled([
      createOwner({ email: "a@example.com", name: "A", password }),
      createOwner({ email: "b@example.com", name: "B", password }),
    ])
    expect(results.filter((r) => r.status === "fulfilled")).toHaveLength(1)
    expect(await userCount()).toBe(1)
  })

  test("rejects a short password", async () => {
    await expect(createOwner({ email: "a@example.com", name: "A", password: "short" })).rejects.toThrow("at least 10")
  })
})

describe("invites", () => {
  test("work once", async () => {
    const o = await owner()
    const { token, invite } = await createInvite(o.id, "Ben@Example.com", inA())
    expect(invite.email).toBe("ben@example.com")
    expect(await findInvite(token)).toMatchObject({ email: "ben@example.com", role: "readonly", owner: false, grants: [{ workspace: WS_A, role: "member" }] })

    const { user } = await acceptInvite(token, { name: "Ben", password })
    expect(user.email).toBe("ben@example.com")
    expect(await findInvite(token)).toBeUndefined()
    await expect(acceptInvite(token, { name: "Ben again", password })).rejects.toThrow("expired or was already used")
    expect(await userCount()).toBe(2)
  })

  test("stop working once they expire", async () => {
    const o = await owner()
    const { token, invite } = await createInvite(o.id, "ben@example.com", inA())
    getDb().prepare(`UPDATE invite SET expires_at = ? WHERE id = ?`).run(Date.now() - 1000, invite.id)
    expect(await findInvite(token)).toBeUndefined()
    await expect(acceptInvite(token, { name: "Ben", password })).rejects.toThrow("expired or was already used")
    expect(await userCount()).toBe(1)
  })

  test("a failed accept leaves the invite open and writes no grants", async () => {
    const o = await owner()
    const { token } = await createInvite(o.id, "ben@example.com", inA())
    await expect(acceptInvite(token, { name: "Ben", password: "short" })).rejects.toThrow(UserError)
    expect(await findInvite(token)).toBeDefined()
    const rows = getDb().prepare(`SELECT COUNT(*) AS n FROM workspace_member`).get() as { n: number }
    expect(Number(rows.n)).toBe(0)
  })

  test("can't go to an email that already has a user", async () => {
    const o = await owner()
    await expect(createInvite(o.id, "owner@example.com", inA())).rejects.toThrow("already uses that email")
  })

  test("with grants, accepting gives exactly those grants", async () => {
    const o = await owner()
    const grants: WorkspaceGrant[] = [
      { workspace: WS_A, role: "admin" },
      { workspace: WS_B, role: "readonly" },
    ]
    const { token, invite } = await createInvite(o.id, "ben@example.com", { owner: false, grants })
    expect(invite).toMatchObject({ owner: false, grants })
    expect((await listInvites())[0]).toMatchObject({ email: "ben@example.com", owner: false, grants })

    const { user } = await acceptInvite(token, { name: "Ben", password })
    expect(roleOf(user.id)).toBe("readonly")
    expect(grantsOf(user.id)).toEqual(grants)
    expect(grantRows(user.id)).toBe(2)
    expect((await listMembers()).find((m) => m.id === user.id)).toMatchObject({ owner: false, grants })
  })

  test("an owner invite makes an owner with no grants", async () => {
    const o = await owner()
    const { token, invite } = await createInvite(o.id, "cy@example.com", OWNER)
    expect(invite).toMatchObject({ owner: true, grants: [] })
    expect(await findInvite(token)).toMatchObject({ owner: true, grants: [] })

    const { user } = await acceptInvite(token, { name: "Cy", password })
    expect(roleOf(user.id)).toBe("owner")
    expect(grantRows(user.id)).toBe(0)
    expect((await listMembers()).find((m) => m.id === user.id)).toMatchObject({ owner: true, grants: [] })
  })

  test("for someone who isn't an owner, no grants is refused", async () => {
    const o = await owner()
    await expect(createInvite(o.id, "ben@example.com", { owner: false, grants: [] })).rejects.toThrow("Pick at least one workspace.")
    expect(await listInvites()).toEqual([])
  })

  test("a role that doesn't exist is refused", async () => {
    const o = await owner()
    const bad = { owner: false, grants: [{ workspace: WS_A, role: "owner" }] } as unknown as MemberAccess
    await expect(createInvite(o.id, "ben@example.com", bad)).rejects.toThrow("Pick a role for each workspace.")
    const nameless: MemberAccess = { owner: false, grants: [{ workspace: "", role: "member" }] }
    await expect(createInvite(o.id, "ben@example.com", nameless)).rejects.toThrow("Pick a role for each workspace.")
    expect(await listInvites()).toEqual([])
  })

  test("the same workspace twice collapses to one grant, the last one", async () => {
    const o = await owner()
    const twice: MemberAccess = {
      owner: false,
      grants: [
        { workspace: WS_A, role: "readonly" },
        { workspace: WS_B, role: "member" },
        { workspace: WS_A, role: "admin" },
      ],
    }
    const once: WorkspaceGrant[] = [
      { workspace: WS_A, role: "admin" },
      { workspace: WS_B, role: "member" },
    ]
    const { token, invite } = await createInvite(o.id, "ben@example.com", twice)
    expect(invite.grants).toEqual(once)
    const { user } = await acceptInvite(token, { name: "Ben", password })
    expect(grantsOf(user.id)).toEqual(once)

    const dee = await member(o.id, "dee@example.com")
    await setAccess(o.id, dee.id, twice)
    expect(grantsOf(dee.id)).toEqual(once)
  })
})

describe("access", () => {
  test("the last owner can't be demoted or removed", async () => {
    const o = await owner()
    const ben = await member(o.id)
    await expect(setAccess(ben.id, o.id, inA())).rejects.toThrow("Keep at least one owner.")
    await expect(removeMember(ben.id, o.id)).rejects.toThrow("Keep at least one owner.")
    expect(roleOf(o.id)).toBe("owner")
    expect(grantRows(o.id)).toBe(0)
  })

  test("nobody changes or removes themselves", async () => {
    const o = await owner()
    await expect(setAccess(o.id, o.id, inA())).rejects.toThrow("your own access")
    await expect(setAccess(o.id, o.id, OWNER)).rejects.toThrow("your own access")
    await expect(removeMember(o.id, o.id)).rejects.toThrow("remove yourself")
    const ben = await member(o.id)
    await expect(setAccess(ben.id, ben.id, OWNER)).rejects.toThrow("your own access")
    expect(roleOf(ben.id)).toBe("readonly")
    expect(grantsOf(ben.id)).toEqual([{ workspace: WS_A, role: "member" }])
  })

  test("someone who doesn't exist can't be changed or removed", async () => {
    const o = await owner()
    await expect(setAccess(o.id, "nobody", inA())).rejects.toThrow("That member doesn't exist.")
    await expect(removeMember(o.id, "nobody")).rejects.toThrow("That member doesn't exist.")
    expect(grantRows("nobody")).toBe(0)
  })

  test("an owner can be demoted while another owner remains", async () => {
    const o = await owner()
    const second = await member(o.id, "cy@example.com", OWNER)
    await setAccess(second.id, o.id, inA("readonly"))
    expect(roleOf(o.id)).toBe("readonly")
    expect(grantsOf(o.id)).toEqual([{ workspace: WS_A, role: "readonly" }])
  })

  test("making someone an owner drops their grants", async () => {
    const o = await owner()
    const ben = await member(o.id)
    await setAccess(o.id, ben.id, OWNER)
    expect(roleOf(ben.id)).toBe("owner")
    expect(grantRows(ben.id)).toBe(0)
  })

  test("a change replaces the grants and signs that person out everywhere", async () => {
    const o = await owner()
    const ben = await member(o.id, "ben@example.com", {
      owner: false,
      grants: [
        { workspace: WS_A, role: "admin" },
        { workspace: WS_B, role: "member" },
      ],
    })
    const dee = await member(o.id, "dee@example.com")
    const ctx = await (await getAuth()).$context
    await ctx.internalAdapter.createSession(ben.id)
    await ctx.internalAdapter.createSession(ben.id)
    await ctx.internalAdapter.createSession(o.id)
    await ctx.internalAdapter.createSession(dee.id)
    expect(await sessionCount(ben.id)).toBe(2)

    const next: WorkspaceGrant[] = [
      { workspace: WS_B, role: "readonly" },
      { workspace: WS_C, role: "member" },
    ]
    await setAccess(o.id, ben.id, { owner: false, grants: next })
    expect(grantsOf(ben.id)).toEqual(next)
    expect(roleOf(ben.id)).toBe("readonly")
    expect(await sessionCount(ben.id)).toBe(0)
    expect(await sessionCount(o.id)).toBe(1)
    // Someone else's access and sessions are untouched.
    expect(grantsOf(dee.id)).toEqual([{ workspace: WS_A, role: "member" }])
    expect(await sessionCount(dee.id)).toBe(1)
  })

  test("a refused change leaves the grants as they were", async () => {
    const o = await owner()
    const ben = await member(o.id)
    const bad = { owner: false, grants: [{ workspace: WS_B, role: "root" }] } as unknown as MemberAccess
    await expect(setAccess(o.id, ben.id, bad)).rejects.toThrow("Pick a role for each workspace.")
    expect(grantsOf(ben.id)).toEqual([{ workspace: WS_A, role: "member" }])
  })

  test("removing a member deletes their sessions, login and grants", async () => {
    const o = await owner()
    const ben = await member(o.id, "ben@example.com", {
      owner: false,
      grants: [
        { workspace: WS_A, role: "admin" },
        { workspace: WS_B, role: "member" },
      ],
    })
    const dee = await member(o.id, "dee@example.com")
    const ctx = await (await getAuth()).$context
    await ctx.internalAdapter.createSession(ben.id)
    expect(grantRows(ben.id)).toBe(2)
    await removeMember(o.id, ben.id)
    expect(await sessionCount(ben.id)).toBe(0)
    expect(await userCount()).toBe(2)
    const accounts = getDb().prepare(`SELECT COUNT(*) AS n FROM account WHERE userId = ?`).get(ben.id) as { n: number }
    expect(Number(accounts.n)).toBe(0)
    expect(grantRows(ben.id)).toBe(0)
    expect(grantsOf(dee.id)).toEqual([{ workspace: WS_A, role: "member" }])
  })
})

// What a dashboard from before workspaces would read as an instance-wide
// role: owner for an owner, and for anyone else written since, the least it knows.
describe("the role column", () => {
  const inviteRole = (id: string) => (getDb().prepare(`SELECT role FROM invite WHERE id = ?`).get(id) as { role: string }).role
  const highest: MemberAccess = {
    owner: false,
    grants: [
      { workspace: WS_A, role: "admin" },
      { workspace: WS_B, role: "member" },
    ],
  }

  test("an invite for someone who isn't an owner stores readonly, whatever it grants", async () => {
    const o = await owner()
    for (const role of ["admin", "member", "readonly"] as const) {
      const { invite } = await createInvite(o.id, `${role}@example.com`, inA(role))
      expect(inviteRole(invite.id), role).toBe("readonly")
    }
    const boss = await createInvite(o.id, "boss@example.com", OWNER)
    expect(inviteRole(boss.invite.id)).toBe("owner")
  })

  test("accepting an invite with grants stores readonly, an owner invite stores owner", async () => {
    const o = await owner()
    const ben = await member(o.id, "ben@example.com", highest)
    expect(roleOf(ben.id)).toBe("readonly")
    expect(grantsOf(ben.id)).toEqual(highest.owner ? [] : highest.grants)
    const cy = await member(o.id, "cy@example.com", OWNER)
    expect(roleOf(cy.id)).toBe("owner")
  })

  test("changing someone to chosen workspaces stores readonly, to owner stores owner", async () => {
    const o = await owner()
    const cy = await member(o.id, "cy@example.com", OWNER)
    await setAccess(o.id, cy.id, highest)
    expect(roleOf(cy.id)).toBe("readonly")
    await setAccess(o.id, cy.id, OWNER)
    expect(roleOf(cy.id)).toBe("owner")
    await setAccess(o.id, cy.id, { owner: false, grants: [] })
    expect(roleOf(cy.id)).toBe("readonly")
    expect(roleOf(o.id)).toBe("owner")
  })

  test("a role from before workspaces is rewritten to readonly by the next change", async () => {
    const o = await owner()
    const ben = await member(o.id)
    getDb().prepare(`UPDATE "user" SET role = 'member' WHERE id = ?`).run(ben.id)
    await setAccess(o.id, ben.id, inA("admin"))
    expect(roleOf(ben.id)).toBe("readonly")
    expect(grantsOf(ben.id)).toEqual([{ workspace: WS_A, role: "admin" }])
  })

  test("an invite from before workspaces, accepted before the upgrade, keeps its own role", async () => {
    const o = await owner()
    for (const role of ["member", "readonly"]) {
      const token = `legacy-token-${role}`
      const now = Date.now()
      getDb()
        .prepare(`INSERT INTO invite (id, token_hash, email, role, workspaces, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, NULL, ?, ?, ?)`)
        .run(`legacy-${role}`, createHash("sha256").update(token).digest("hex"), `old-${role}@example.com`, role, o.id, now, now + 60_000)
      const { user } = await acceptInvite(token, { name: "Old", password })
      expect(roleOf(user.id), role).toBe(role)
      expect(grantRows(user.id)).toBe(0)
    }
  })
})

describe("an accept that can't write its grants", () => {
  test("takes the account back and leaves the invite open", async () => {
    const o = await owner()
    const { token } = await createInvite(o.id, "ben@example.com", inA("admin"))
    getDb().exec(`DROP TABLE workspace_member`)
    await expect(acceptInvite(token, { name: "Ben", password })).rejects.toThrow()
    expect(await userCount()).toBe(1)
    const left = getDb().prepare(`SELECT (SELECT COUNT(*) FROM "user" WHERE email = 'ben@example.com') AS users, (SELECT COUNT(*) FROM account WHERE userId != ?) AS accounts`).get(o.id) as { users: number; accounts: number }
    expect([Number(left.users), Number(left.accounts)]).toEqual([0, 0])
    expect(await findInvite(token)).toMatchObject({ email: "ben@example.com", grants: [{ workspace: WS_A, role: "admin" }] })

    // Once grants can be written again, the same link works.
    migrateOwn(getDb())
    const { user } = await acceptInvite(token, { name: "Ben", password })
    expect(grantsOf(user.id)).toEqual([{ workspace: WS_A, role: "admin" }])
    expect(await userCount()).toBe(2)
  })
})
