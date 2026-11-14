import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, test } from "vitest"
import { getAuth, resetAuth } from "./auth"
import { resetConfig } from "./config"
import { getDb, setDb } from "./db"
import { UserError, acceptInvite, createInvite, createOwner, findInvite, removeMember, setRole, userCount } from "./users"

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

async function owner() {
  return createOwner({ email: "Owner@Example.com", name: "Ana", password })
}

async function member(ownerId: string, email = "ben@example.com", role: "member" | "owner" | "readonly" = "member") {
  const { token } = await createInvite(ownerId, email, role)
  const { user } = await acceptInvite(token, { name: "Ben", password })
  return user
}

async function sessionCount(userId: string) {
  const row = getDb().prepare(`SELECT COUNT(*) AS n FROM session WHERE userId = ?`).get(userId) as { n: number }
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
    const { token, invite } = await createInvite(o.id, "Ben@Example.com", "member")
    expect(invite.email).toBe("ben@example.com")
    expect(await findInvite(token)).toMatchObject({ email: "ben@example.com", role: "member" })

    const { user } = await acceptInvite(token, { name: "Ben", password })
    expect(user.email).toBe("ben@example.com")
    expect(await findInvite(token)).toBeUndefined()
    await expect(acceptInvite(token, { name: "Ben again", password })).rejects.toThrow("expired or was already used")
    expect(await userCount()).toBe(2)
  })

  test("stop working once they expire", async () => {
    const o = await owner()
    const { token, invite } = await createInvite(o.id, "ben@example.com", "member")
    getDb().prepare(`UPDATE invite SET expires_at = ? WHERE id = ?`).run(Date.now() - 1000, invite.id)
    expect(await findInvite(token)).toBeUndefined()
    await expect(acceptInvite(token, { name: "Ben", password })).rejects.toThrow("expired or was already used")
    expect(await userCount()).toBe(1)
  })

  test("a failed accept leaves the invite open", async () => {
    const o = await owner()
    const { token } = await createInvite(o.id, "ben@example.com", "member")
    await expect(acceptInvite(token, { name: "Ben", password: "short" })).rejects.toThrow(UserError)
    expect(await findInvite(token)).toBeDefined()
  })

  test("can't go to an email that already has a user", async () => {
    const o = await owner()
    await expect(createInvite(o.id, "owner@example.com", "member")).rejects.toThrow("already uses that email")
  })
})

describe("roles", () => {
  test("the last owner can't be demoted or removed", async () => {
    const o = await owner()
    const ben = await member(o.id)
    await expect(setRole(ben.id, o.id, "member")).rejects.toThrow("Keep at least one owner.")
    await expect(removeMember(ben.id, o.id)).rejects.toThrow("Keep at least one owner.")
  })

  test("nobody changes or removes themselves", async () => {
    const o = await owner()
    await expect(setRole(o.id, o.id, "member")).rejects.toThrow("your own role")
    await expect(removeMember(o.id, o.id)).rejects.toThrow("remove yourself")
  })

  test("an owner can be demoted while another owner remains", async () => {
    const o = await owner()
    const second = await member(o.id, "cy@example.com", "owner")
    await setRole(second.id, o.id, "member")
    const row = getDb().prepare(`SELECT role FROM "user" WHERE id = ?`).get(o.id) as { role: string }
    expect(row.role).toBe("member")
  })

  test("a role change signs that person out everywhere", async () => {
    const o = await owner()
    const ben = await member(o.id)
    const ctx = await (await getAuth()).$context
    await ctx.internalAdapter.createSession(ben.id)
    await ctx.internalAdapter.createSession(ben.id)
    await ctx.internalAdapter.createSession(o.id)
    expect(await sessionCount(ben.id)).toBe(2)

    await setRole(o.id, ben.id, "readonly")
    expect(await sessionCount(ben.id)).toBe(0)
    expect(await sessionCount(o.id)).toBe(1)
  })

  test("removing a member deletes their sessions and login", async () => {
    const o = await owner()
    const ben = await member(o.id)
    const ctx = await (await getAuth()).$context
    await ctx.internalAdapter.createSession(ben.id)
    await removeMember(o.id, ben.id)
    expect(await sessionCount(ben.id)).toBe(0)
    expect(await userCount()).toBe(1)
    const accounts = getDb().prepare(`SELECT COUNT(*) AS n FROM account WHERE userId = ?`).get(ben.id) as { n: number }
    expect(Number(accounts.n)).toBe(0)
  })
})
