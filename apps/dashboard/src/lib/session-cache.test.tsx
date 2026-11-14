import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

const getSession = vi.fn()
vi.mock("@/fns/session", () => ({ getSession: () => getSession() }))

const viewer = { id: "u1", name: "Ana", email: "ana@example.com", owner: true }
const workspaces = [{ id: "ws_aaaaaaaaaaaaaaaaaaaa", name: "default", default: true, role: "owner" as const }]
const signedIn = { viewer, workspaces, lastWorkspace: "ws_aaaaaaaaaaaaaaaaaaaa", needsSetup: false, engineUrl: "https://engine.example.com", passkeys: true, dashboardUrl: "https://db.example.com" }
const signedOut = { viewer: null, workspaces: [], lastWorkspace: null, needsSetup: false, engineUrl: null, passkeys: true, dashboardUrl: "https://db.example.com" }

async function freshCache() {
  vi.resetModules()
  return import("./session-cache")
}

describe("session cache", () => {
  beforeEach(() => {
    vi.useFakeTimers()
    getSession.mockReset()
  })
  afterEach(() => vi.useRealTimers())

  it("asks the server once, then answers from the cache", async () => {
    const { loadSession } = await freshCache()
    getSession.mockResolvedValue(signedIn)
    expect(await loadSession()).toEqual(signedIn)
    expect(await loadSession()).toEqual(signedIn)
    expect(getSession).toHaveBeenCalledTimes(1)
  })

  it("answers from a stale cache right away and refreshes in the background", async () => {
    const { loadSession } = await freshCache()
    getSession.mockResolvedValue(signedIn)
    await loadSession()
    vi.advanceTimersByTime(31_000)
    let resolve!: (s: typeof signedOut) => void
    getSession.mockReturnValue(new Promise((r) => (resolve = r)))
    expect(await loadSession()).toEqual(signedIn)
    expect(getSession).toHaveBeenCalledTimes(2)
    // The refresh finds no session, so the next navigation asks again and sees it.
    resolve(signedOut)
    await vi.advanceTimersByTimeAsync(0)
    getSession.mockResolvedValue(signedOut)
    expect(await loadSession()).toEqual(signedOut)
    expect(getSession).toHaveBeenCalledTimes(3)
  })

  it("starts one background refresh at a time", async () => {
    const { loadSession } = await freshCache()
    getSession.mockResolvedValue(signedIn)
    await loadSession()
    vi.advanceTimersByTime(31_000)
    getSession.mockReturnValue(new Promise(() => {}))
    await loadSession()
    await loadSession()
    expect(getSession).toHaveBeenCalledTimes(2)
  })

  it("drops a refresh that was sent before sign-out", async () => {
    const { loadSession, forgetSession } = await freshCache()
    getSession.mockResolvedValue(signedIn)
    await loadSession()
    vi.advanceTimersByTime(31_000)
    let resolve!: (s: typeof signedIn) => void
    getSession.mockReturnValue(new Promise((r) => (resolve = r)))
    await loadSession()
    forgetSession()
    resolve(signedIn)
    await vi.advanceTimersByTimeAsync(0)
    getSession.mockResolvedValue(signedOut)
    expect(await loadSession()).toEqual(signedOut)
  })

  it("doesn't keep a session read while the engine was down", async () => {
    const { loadSession } = await freshCache()
    const engineDown = { ...signedIn, workspaces: null }
    getSession.mockResolvedValue(engineDown)
    expect(await loadSession()).toEqual(engineDown)
    getSession.mockResolvedValue(signedIn)
    expect(await loadSession()).toEqual(signedIn)
    expect(getSession).toHaveBeenCalledTimes(2)
  })

  it("is primed once per page load and a sign-out isn't undone by a later prime", async () => {
    const { loadSession, primeSession, forgetSession } = await freshCache()
    primeSession(signedIn)
    expect(await loadSession()).toEqual(signedIn)
    expect(getSession).not.toHaveBeenCalled()
    forgetSession()
    primeSession(signedIn)
    getSession.mockResolvedValue(signedOut)
    expect(await loadSession()).toEqual(signedOut)
  })
})
