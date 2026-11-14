import { describe, expect, test } from "vitest"
import { type WorkspaceInfo, findWorkspace, lastWorkspace, legacyHref, pickWorkspace, rememberWorkspace } from "./workspace"

const DEFAULT: WorkspaceInfo = { id: "ws_aaaaaaaaaaaaaaaaaaaa", name: "default", default: true, role: "owner" }
const AGENTS: WorkspaceInfo = { id: "ws_bbbbbbbbbbbbbbbbbbbb", name: "agents", default: false, role: "admin" }
const STAGING: WorkspaceInfo = { id: "ws_cccccccccccccccccccc", name: "staging", default: false, role: "readonly" }
const all = [AGENTS, DEFAULT, STAGING]

describe("pickWorkspace", () => {
  test("the wanted one, by id or by name", () => {
    expect(pickWorkspace(all, STAGING.id)).toBe(STAGING)
    expect(pickWorkspace(all, "staging")).toBe(STAGING)
    expect(pickWorkspace(all, AGENTS.id)).toBe(AGENTS)
  })

  test("falls back to the default when the wanted one isn't there, or none is wanted", () => {
    expect(pickWorkspace(all, "ws_zzzzzzzzzzzzzzzzzzzz")).toBe(DEFAULT)
    expect(pickWorkspace(all, "nope")).toBe(DEFAULT)
    expect(pickWorkspace(all)).toBe(DEFAULT)
    expect(pickWorkspace(all, null)).toBe(DEFAULT)
    expect(pickWorkspace(all, "")).toBe(DEFAULT)
  })

  test("without a default, the first", () => {
    expect(pickWorkspace([STAGING, AGENTS])).toBe(STAGING)
    expect(pickWorkspace([STAGING, AGENTS], "nope")).toBe(STAGING)
    expect(pickWorkspace([STAGING, AGENTS], "agents")).toBe(AGENTS)
  })

  test("null when there are none", () => {
    expect(pickWorkspace([])).toBeNull()
    expect(pickWorkspace([], DEFAULT.id)).toBeNull()
  })
})

describe("findWorkspace", () => {
  test("by id or by name, and nothing else", () => {
    expect(findWorkspace(all, AGENTS.id)).toBe(AGENTS)
    expect(findWorkspace(all, "agents")).toBe(AGENTS)
    expect(findWorkspace(all, "default")).toBe(DEFAULT)
    expect(findWorkspace(all, "nope")).toBeUndefined()
    expect(findWorkspace(all, "")).toBeUndefined()
    expect(findWorkspace(all, "AGENTS")).toBeUndefined()
    expect(findWorkspace([], "agents")).toBeUndefined()
  })
})

describe("legacyHref", () => {
  test("null when the person has no workspace, or the list didn't load", () => {
    expect(legacyHref([], "db/shop")).toBeNull()
    expect(legacyHref(null, "db/shop")).toBeNull()
  })

  test("the same page in the default workspace", () => {
    expect(legacyHref(all, "db/shop/sql")).toBe("/app/w/default/db/shop/sql")
    expect(legacyHref(all, "tokens")).toBe("/app/w/default/tokens")
    expect(legacyHref(all, "")).toBe("/app/w/default")
  })

  test("without a default, the first one the person can use", () => {
    expect(legacyHref([STAGING, AGENTS], "buckets/photos")).toBe("/app/w/staging/buckets/photos")
  })

  test("drops empty, . and .. segments, so the link can't leave the workspace", () => {
    expect(legacyHref(all, "/db//shop/")).toBe("/app/w/default/db/shop")
    expect(legacyHref(all, "../../login")).toBe("/app/w/default/login")
    expect(legacyHref(all, "db/./shop/../../..")).toBe("/app/w/default/db/shop")
    expect(legacyHref(all, "..")).toBe("/app/w/default")
    expect(legacyHref(all, "/")).toBe("/app/w/default")
  })

  test("percent-encodes each segment", () => {
    expect(legacyHref(all, "db/a b/sql")).toBe("/app/w/default/db/a%20b/sql")
    expect(legacyHref(all, "buckets/x?y#z")).toBe("/app/w/default/buckets/x%3Fy%23z")
    expect(legacyHref(all, "db/%2e%2e/x")).toBe("/app/w/default/db/%252e%252e/x")
    expect(legacyHref(all, "db/ünï")).toBe("/app/w/default/db/%C3%BCn%C3%AF")
    expect(legacyHref(all, "db/a\\b")).toBe("/app/w/default/db/a%5Cb")
  })

  test("keeps the search string", () => {
    expect(legacyHref(all, "db/shop", "?branch=dev")).toBe("/app/w/default/db/shop?branch=dev")
    expect(legacyHref(all, "", "?x=1")).toBe("/app/w/default?x=1")
  })
})

describe("the last workspace", () => {
  test("is remembered for pages outside a workspace", () => {
    expect(lastWorkspace()).toBeUndefined()
    rememberWorkspace(AGENTS.id)
    expect(lastWorkspace()).toBe(AGENTS.id)
    rememberWorkspace(STAGING.id)
    expect(lastWorkspace()).toBe(STAGING.id)
  })
})
