import { createHash, randomUUID } from "node:crypto"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest"
import type { Permission, Role, WorkspaceRole } from "@/lib/roles"
import { NoWorkspace, grantsOf, resetAccess, setGrants, visibleWorkspaces, workspaceFor } from "./access"
import { resetAuth } from "./auth"
import { resetConfig } from "./config"
import { getDb, setDb } from "./db"
import { AccessError, type Viewer, accessTo, denied } from "./session"
import { acceptInvite, createInvite, createOwner, findInvite } from "./users"

// The engine's workspace list, as the test sets it.
const engine = vi.hoisted(() => ({ workspaces: [] as { id: string; name: string; default: boolean }[], lists: 0, down: false }))

vi.mock("./engine", async (original) => ({
  ...(await original<typeof import("./engine")>()),
  serviceClient: () => ({
    workspaces: {
      list: async () => {
        engine.lists++
        if (engine.down) throw new Error("engine down")
        // More fields than the dashboard keeps, as the engine sends them.
        return { data: engine.workspaces.map((w) => ({ ...w, created_at: "2026-11-01T00:00:00Z", deleted_at: null })), nextCursor: "" }
      },
    },
  }),
}))

const DEFAULT = { id: "ws_aaaaaaaaaaaaaaaaaaaa", name: "default", default: true }
const AGENTS = { id: "ws_bbbbbbbbbbbbbbbbbbbb", name: "agents", default: false }
const STAGING = { id: "ws_cccccccccccccccccccc", name: "staging", default: false }
const GONE = "ws_zzzzzzzzzzzzzzzzzzzz"

let dir = ""

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), "lumbung-dashboard-"))
  process.env.LUMBUNG_DASHBOARD_DATA_DIR = dir
  process.env.LUMBUNG_AUTH_SECRET = "test-secret-that-is-long-enough-for-auth"
  process.env.LUMBUNG_DASHBOARD_URL = "http://localhost:4871"
  process.env.LUMBUNG_SERVICE_TOKEN = "lmb_service_testsecret"
  resetConfig()
  resetAuth()
  setDb(undefined)
  resetAccess()
  engine.workspaces = [DEFAULT, AGENTS, STAGING]
  engine.lists = 0
  engine.down = false
})

afterEach(() => {
  getDb().close()
  setDb(undefined)
  resetAuth()
  resetAccess()
  rmSync(dir, { recursive: true, force: true })
})

const password = "correct horse battery"

async function owner(): Promise<Viewer> {
  const u = await createOwner({ email: "owner@example.com", name: "Ana", password })
  return { id: u.id, name: u.name, email: u.email, owner: true }
}

// The first look at the list runs the upgrade step, which hands a non-owner
// with no role anywhere the default workspace. A real instance has run it by
// the time anyone is invited, so tests of today's model run it first too.
async function settled(): Promise<Viewer> {
  const o = await owner()
  await visibleWorkspaces(o)
  return o
}

async function person(ownerId: string, email: string, grants: { workspace: string; role: WorkspaceRole }[]): Promise<Viewer> {
  const { token } = await createInvite(ownerId, email, { owner: false, grants })
  const { user } = await acceptInvite(token, { name: email.split("@")[0]!, password })
  return { id: user.id, name: user.name, email: user.email, owner: false }
}

describe("visible workspaces", () => {
  test("an owner sees every workspace, as owner", async () => {
    const o = await settled()
    expect(await visibleWorkspaces(o)).toEqual([
      { ...DEFAULT, role: "owner" },
      { ...AGENTS, role: "owner" },
      { ...STAGING, role: "owner" },
    ])
    expect(grantsOf(o.id)).toEqual([])
  })

  test("anyone else sees only what they were given, with the role given", async () => {
    const o = await settled()
    const ben = await person(o.id, "ben@example.com", [
      { workspace: STAGING.id, role: "admin" },
      { workspace: AGENTS.id, role: "readonly" },
    ])
    expect(await visibleWorkspaces(ben)).toEqual([
      { ...AGENTS, role: "readonly" },
      { ...STAGING, role: "admin" },
    ])
    const dee = await person(o.id, "dee@example.com", [{ workspace: DEFAULT.id, role: "member" }])
    expect(await visibleWorkspaces(dee)).toEqual([{ ...DEFAULT, role: "member" }])
  })

  test("someone with no grants sees nothing, even claiming a known id", async () => {
    const o = await settled()
    const ben = await person(o.id, "ben@example.com", [{ workspace: AGENTS.id, role: "member" }])
    setGrants(ben.id, [])
    expect(await visibleWorkspaces(ben)).toEqual([])
    expect(await visibleWorkspaces({ id: "nobody", owner: false })).toEqual([])
  })

  test("a grant in a workspace the engine no longer lists is ignored", async () => {
    const o = await settled()
    const ben = await person(o.id, "ben@example.com", [
      { workspace: GONE, role: "admin" },
      { workspace: AGENTS.id, role: "member" },
    ])
    expect(await visibleWorkspaces(ben)).toEqual([{ ...AGENTS, role: "member" }])

    // A workspace that goes to the trash stops being visible with the next list.
    engine.workspaces = [DEFAULT, STAGING]
    resetAccess()
    expect(await visibleWorkspaces(ben)).toEqual([])
    expect(grantsOf(ben.id)).toHaveLength(2)
  })

  test("a role the dashboard doesn't know gives nothing", async () => {
    const o = await settled()
    const ben = await person(o.id, "ben@example.com", [{ workspace: AGENTS.id, role: "member" }])
    getDb().prepare(`UPDATE workspace_member SET role = 'owner' WHERE user_id = ?`).run(ben.id)
    expect(grantsOf(ben.id)).toEqual([])
    expect(await visibleWorkspaces(ben)).toEqual([])
  })

  test("roles are read fresh while the engine's list is shared for a moment", async () => {
    const o = await settled()
    const ben = await person(o.id, "ben@example.com", [{ workspace: AGENTS.id, role: "admin" }])
    expect(await visibleWorkspaces(ben)).toEqual([{ ...AGENTS, role: "admin" }])
    const before = engine.lists
    setGrants(ben.id, [{ workspace: AGENTS.id, role: "readonly" }])
    expect(await visibleWorkspaces(ben)).toEqual([{ ...AGENTS, role: "readonly" }])
    setGrants(ben.id, [])
    expect(await visibleWorkspaces(ben)).toEqual([])
    expect(engine.lists).toBe(before)
  })
})

describe("workspaceFor", () => {
  test("finds a workspace by name and by id, with the person's role", async () => {
    const o = await settled()
    const ben = await person(o.id, "ben@example.com", [{ workspace: AGENTS.id, role: "member" }])
    expect(await workspaceFor(ben, "agents")).toEqual({ ...AGENTS, role: "member" })
    expect(await workspaceFor(ben, AGENTS.id)).toEqual({ ...AGENTS, role: "member" })
    expect(await workspaceFor(o, "staging")).toEqual({ ...STAGING, role: "owner" })
  })

  test("one the person has no role in looks exactly like one that doesn't exist", async () => {
    const o = await settled()
    const ben = await person(o.id, "ben@example.com", [{ workspace: AGENTS.id, role: "admin" }])
    const failure = async (ref: string) => {
      const e = await workspaceFor(ben, ref).then(
        () => null,
        (err: unknown) => err,
      )
      expect(e).toBeInstanceOf(NoWorkspace)
      const err = e as NoWorkspace
      return { message: err.message, status: err.status }
    }
    const missing = await failure("nope")
    expect(missing).toEqual({ message: "That workspace doesn't exist.", status: 404 })
    expect(await failure(GONE)).toEqual(missing)
    expect(await failure(STAGING.name)).toEqual(missing)
    expect(await failure(STAGING.id)).toEqual(missing)
    expect(await failure(DEFAULT.name)).toEqual(missing)
    expect(await failure(DEFAULT.id)).toEqual(missing)
    expect(await failure("")).toEqual(missing)

    // The owner really has them, so the refusals above are about the role.
    expect((await workspaceFor(o, STAGING.id)).name).toBe("staging")
    await expect(workspaceFor(o, "nope")).rejects.toBeInstanceOf(NoWorkspace)
  })
})

describe("accessTo", () => {
  const PERMISSIONS: Permission[] = ["read", "write", "admin", "owner"]
  const ALLOWED: Record<Role, Permission[]> = {
    owner: ["read", "write", "admin", "owner"],
    admin: ["read", "write", "admin"],
    member: ["read", "write"],
    readonly: ["read"],
  }

  async function viewers(): Promise<Record<Role, Viewer>> {
    const o = await settled()
    return {
      owner: o,
      admin: await person(o.id, "admin@example.com", [{ workspace: AGENTS.id, role: "admin" }]),
      member: await person(o.id, "member@example.com", [{ workspace: AGENTS.id, role: "member" }]),
      readonly: await person(o.id, "readonly@example.com", [{ workspace: AGENTS.id, role: "readonly" }]),
    }
  }

  async function status(viewer: Viewer, ws: string, p: Permission): Promise<{ status: number; message: string }> {
    return accessTo(viewer, ws, p).then(
      () => ({ status: 200, message: "" }),
      (e: unknown) => {
        expect(e).toBeInstanceOf(AccessError)
        return { status: (e as AccessError).status, message: (e as AccessError).message }
      },
    )
  }

  test.each<Role>(["owner", "admin", "member", "readonly"])("%s: allowed up to their level, 403 above it", async (role) => {
    const viewer = (await viewers())[role]
    for (const p of PERMISSIONS) {
      const got = await status(viewer, "agents", p)
      if (ALLOWED[role].includes(p)) expect(got, `${role} ${p}`).toEqual({ status: 200, message: "" })
      else expect(got, `${role} ${p}`).toEqual({ status: 403, message: denied(p) })
      expect((await status(viewer, AGENTS.id, p)).status, `${role} ${p} by id`).toBe(got.status)
    }
  })

  test.each<WorkspaceRole>(["admin", "member", "readonly"])("%s: a workspace they have no role in is a 404 for every permission", async (role) => {
    const viewer = (await viewers())[role]
    for (const p of PERMISSIONS) {
      for (const ref of [STAGING.name, STAGING.id, DEFAULT.name, DEFAULT.id, "nope", GONE]) {
        expect(await status(viewer, ref, p), `${role} ${p} ${ref}`).toEqual({ status: 404, message: "That workspace doesn't exist." })
      }
    }
  })

  test("an owner gets a 404 only for a workspace that doesn't exist", async () => {
    const { owner: o } = await viewers()
    for (const p of PERMISSIONS) {
      expect((await status(o, "nope", p)).status).toBe(404)
      expect((await status(o, STAGING.name, p)).status).toBe(200)
    }
  })

  test("the 403 says which level it takes", () => {
    expect(denied("write")).toBe("Your role can't make changes.")
    expect(denied("admin")).toBe("Only an admin of this workspace can do that.")
    expect(denied("owner")).toBe("Only an owner can do that.")
  })

  test("the access names the workspace by id, the role and the viewer", async () => {
    const all = await viewers()
    const a = await accessTo(all.member, "agents", "write")
    expect(a.viewer).toBe(all.member)
    expect(a.workspace).toEqual({ ...AGENTS, role: "member" })
    expect(a.role).toBe("member")
    expect(a.engine).toBeTruthy()
    expect((await accessTo(all.owner, AGENTS.id, "owner")).role).toBe("owner")
  })

  test("an engine that doesn't answer is a 502 that doesn't repeat the engine's own error", async () => {
    const { member } = await viewers()
    resetAccess()
    engine.down = true
    const e = await accessTo(member, "agents", "read").catch((err: unknown) => err)
    expect(e).toBeInstanceOf(AccessError)
    expect((e as AccessError).status).toBe(502)
    // The engine's message names its address, which isn't for the browser.
    expect((e as Error).message).toBe("Can't reach the engine.")

    // The failed list isn't kept: the next request asks the engine again.
    engine.down = false
    expect((await accessTo(member, "agents", "read")).role).toBe("member")
  })
})

describe("the upgrade to workspaces", () => {
  function sha256(s: string) {
    return createHash("sha256").update(s).digest("hex")
  }

  // An invite as the dashboard wrote it before workspaces: a role, no workspaces.
  function legacyInvite(ownerId: string, email: string, role: string): { id: string; token: string } {
    const id = randomUUID()
    const token = `legacy-token-${id}`
    const now = Date.now()
    getDb()
      .prepare(`INSERT INTO invite (id, token_hash, email, role, workspaces, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, NULL, ?, ?, ?)`)
      .run(id, sha256(token), email, role, ownerId, now, now + 60_000)
    return { id, token }
  }

  async function legacyUser(ownerId: string, email: string, role: string | null) {
    const { token } = legacyInvite(ownerId, email, "member")
    const { user } = await acceptInvite(token, { name: email.split("@")[0]!, password })
    getDb().prepare(`UPDATE "user" SET role = ? WHERE id = ?`).run(role, user.id)
    return user.id
  }

  const userRole = (id: string) => (getDb().prepare(`SELECT role FROM "user" WHERE id = ?`).get(id) as { role: string | null }).role
  const inviteRow = (id: string) => getDb().prepare(`SELECT role, workspaces FROM invite WHERE id = ?`).get(id) as { role: string; workspaces: string | null }
  const meta = () => getDb().prepare(`SELECT value FROM dashboard_meta WHERE key = 'workspace_roles'`).get() as { value: string } | undefined
  const allGrants = () => getDb().prepare(`SELECT user_id, workspace_id, role FROM workspace_member ORDER BY user_id, workspace_id`).all()

  async function legacyInstance() {
    const o = await owner()
    const member = await legacyUser(o.id, "member@example.com", "member")
    const readonly = await legacyUser(o.id, "readonly@example.com", "readonly")
    const unset = await legacyUser(o.id, "unset@example.com", null)
    const openReadonly = legacyInvite(o.id, "later@example.com", "readonly")
    const openMember = legacyInvite(o.id, "soon@example.com", "member")
    const openOwner = legacyInvite(o.id, "boss@example.com", "owner")
    return { o, member, readonly, unset, openReadonly, openMember, openOwner }
  }

  test("an invite from before workspaces makes a user with its old role and no grants", async () => {
    const o = await owner()
    const { token } = legacyInvite(o.id, "old@example.com", "readonly")
    expect(await findInvite(token)).toMatchObject({ role: "readonly", owner: false, grants: [] })
    const { user } = await acceptInvite(token, { name: "Old", password })
    expect(userRole(user.id)).toBe("readonly")
    expect(grantsOf(user.id)).toEqual([])
    expect(meta()).toBeUndefined()
  })

  test("on the first look, old roles become roles in the default workspace", async () => {
    const { o, member, readonly, unset, openReadonly, openMember, openOwner } = await legacyInstance()
    expect(allGrants()).toEqual([])
    expect(meta()).toBeUndefined()

    expect(await visibleWorkspaces(o)).toHaveLength(3)

    expect(grantsOf(member)).toEqual([{ workspace: DEFAULT.id, role: "member" }])
    expect(grantsOf(readonly)).toEqual([{ workspace: DEFAULT.id, role: "readonly" }])
    // A role the old dashboard didn't know read as read-only there, and still does.
    expect(grantsOf(unset)).toEqual([{ workspace: DEFAULT.id, role: "readonly" }])
    expect(grantsOf(o.id)).toEqual([])
    expect(allGrants()).toHaveLength(3)
    // The role column is left alone, so a dashboard from before workspaces still finds everyone's old role.
    expect([userRole(member), userRole(readonly), userRole(unset), userRole(o.id)]).toEqual(["member", "readonly", null, "owner"])

    expect(await visibleWorkspaces({ id: member, owner: false })).toEqual([{ ...DEFAULT, role: "member" }])
    expect(await visibleWorkspaces({ id: readonly, owner: false })).toEqual([{ ...DEFAULT, role: "readonly" }])

    // An open invite keeps its role too, and gains the default workspace.
    expect(inviteRow(openReadonly.id)).toEqual({ role: "readonly", workspaces: JSON.stringify([{ workspace: DEFAULT.id, role: "readonly" }]) })
    expect(inviteRow(openMember.id)).toEqual({ role: "member", workspaces: JSON.stringify([{ workspace: DEFAULT.id, role: "member" }]) })
    expect(inviteRow(openOwner.id)).toEqual({ role: "owner", workspaces: null })
    expect(await findInvite(openReadonly.token)).toMatchObject({ owner: false, grants: [{ workspace: DEFAULT.id, role: "readonly" }] })
    expect(await findInvite(openOwner.token)).toMatchObject({ owner: true, grants: [] })
    expect(Number(meta()?.value)).toBeGreaterThan(0)

    // The upgraded invite then gives its role like any other: in the workspace, with the least in the role column.
    const { user } = await acceptInvite(openReadonly.token, { name: "Later", password })
    expect(userRole(user.id)).toBe("readonly")
    expect(grantsOf(user.id)).toEqual([{ workspace: DEFAULT.id, role: "readonly" }])
    const soon = await acceptInvite(openMember.token, { name: "Soon", password })
    expect(userRole(soon.user.id)).toBe("readonly")
    expect(grantsOf(soon.user.id)).toEqual([{ workspace: DEFAULT.id, role: "member" }])
    const boss = await acceptInvite(openOwner.token, { name: "Boss", password })
    expect(userRole(boss.user.id)).toBe("owner")
    expect(grantsOf(boss.user.id)).toEqual([])
  })

  test("it runs once: a grant removed afterwards stays removed", async () => {
    const { o, member, readonly, unset } = await legacyInstance()
    await visibleWorkspaces(o)
    const stamp = meta()?.value
    expect(stamp).toBeTruthy()

    setGrants(member, [])
    getDb().prepare(`UPDATE "user" SET role = 'readonly' WHERE id = ?`).run(readonly)
    setGrants(readonly, [])
    const late = legacyInvite(o.id, "late@example.com", "readonly")

    // As after a restart: nothing remembered in memory, only the database.
    resetAccess()
    expect(await visibleWorkspaces(o)).toHaveLength(3)
    expect(grantsOf(member)).toEqual([])
    expect(grantsOf(readonly)).toEqual([])
    expect(userRole(readonly)).toBe("readonly")
    expect(await visibleWorkspaces({ id: member, owner: false })).toEqual([])
    expect(inviteRow(late.id)).toEqual({ role: "readonly", workspaces: null })
    expect(meta()?.value).toBe(stamp)
    // The one grant nobody removed is the only one left.
    expect(allGrants()).toEqual([{ user_id: unset, workspace_id: DEFAULT.id, role: "readonly" }])
  })

  test("someone an owner already placed in another workspace gets nothing in the default one", async () => {
    const o = await owner()
    const placed = await legacyUser(o.id, "placed@example.com", "member")
    const placedReadonly = await legacyUser(o.id, "placed-ro@example.com", "readonly")
    const carried = await legacyUser(o.id, "carried@example.com", "member")
    setGrants(placed, [{ workspace: AGENTS.id, role: "readonly" }])
    setGrants(placedReadonly, [
      { workspace: AGENTS.id, role: "admin" },
      { workspace: STAGING.id, role: "member" },
    ])

    await visibleWorkspaces(o)

    expect(grantsOf(placed)).toEqual([{ workspace: AGENTS.id, role: "readonly" }])
    expect(grantsOf(placedReadonly)).toEqual([
      { workspace: AGENTS.id, role: "admin" },
      { workspace: STAGING.id, role: "member" },
    ])
    expect(await visibleWorkspaces({ id: placed, owner: false })).toEqual([{ ...AGENTS, role: "readonly" }])
    await expect(workspaceFor({ id: placed, owner: false }, DEFAULT.id)).rejects.toBeInstanceOf(NoWorkspace)
    await expect(workspaceFor({ id: placedReadonly, owner: false }, "default")).rejects.toBeInstanceOf(NoWorkspace)
    // Someone with no role anywhere is still carried over, and the old role column is left alone for all three.
    expect(grantsOf(carried)).toEqual([{ workspace: DEFAULT.id, role: "member" }])
    expect([userRole(placed), userRole(placedReadonly), userRole(carried)]).toEqual(["member", "readonly", "member"])
    expect(allGrants()).toHaveLength(4)
    expect(meta()).toBeTruthy()
  })

  test("a role in a workspace that is gone still counts as placed", async () => {
    const o = await owner()
    const ben = await legacyUser(o.id, "ben@example.com", "member")
    setGrants(ben, [{ workspace: GONE, role: "admin" }])
    await visibleWorkspaces(o)
    expect(grantsOf(ben)).toEqual([{ workspace: GONE, role: "admin" }])
    expect(await visibleWorkspaces({ id: ben, owner: false })).toEqual([])
  })

  test("an existing grant in the default workspace is kept as it is", async () => {
    const o = await owner()
    const ben = await legacyUser(o.id, "ben@example.com", "readonly")
    setGrants(ben, [{ workspace: DEFAULT.id, role: "admin" }])
    await visibleWorkspaces(o)
    expect(grantsOf(ben)).toEqual([{ workspace: DEFAULT.id, role: "admin" }])
    expect(userRole(ben)).toBe("readonly")
  })

  test("it waits for a default workspace, and then uses its id", async () => {
    const o = await owner()
    const ben = await legacyUser(o.id, "ben@example.com", "member")
    engine.workspaces = [AGENTS, STAGING]
    expect(await visibleWorkspaces(o)).toHaveLength(2)
    expect(meta()).toBeUndefined()
    expect(grantsOf(ben)).toEqual([])

    engine.workspaces = [AGENTS, { ...STAGING, default: true }]
    resetAccess()
    await visibleWorkspaces(o)
    expect(grantsOf(ben)).toEqual([{ workspace: STAGING.id, role: "member" }])
    expect(meta()).toBeTruthy()
  })
})
