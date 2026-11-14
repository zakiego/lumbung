import { afterEach, expect, test, vi } from "vitest"

// The module remembers the last id, so each test starts from a fresh copy.
async function fresh() {
  vi.resetModules()
  return import("./workspace")
}

afterEach(() => vi.restoreAllMocks())

test("the workspace last opened is kept in a cookie under the dashboard's path", async () => {
  const { rememberWorkspace, lastWorkspace, LAST_WORKSPACE_COOKIE } = await fresh()
  const set = vi.spyOn(document, "cookie", "set")
  expect(LAST_WORKSPACE_COOKIE).toBe("lumbung.workspace")

  rememberWorkspace("ws_bbbbbbbbbbbbbbbbbbbb")
  expect(set.mock.calls).toEqual([["lumbung.workspace=ws_bbbbbbbbbbbbbbbbbbbb; path=/app; max-age=31536000; samesite=lax"]])
  expect(lastWorkspace()).toBe("ws_bbbbbbbbbbbbbbbbbbbb")

  // The same workspace again writes nothing; another one replaces it.
  rememberWorkspace("ws_bbbbbbbbbbbbbbbbbbbb")
  expect(set).toHaveBeenCalledTimes(1)
  rememberWorkspace("ws_cccccccccccccccccccc")
  expect(set).toHaveBeenCalledTimes(2)
  expect(set.mock.calls[1]![0]).toContain("lumbung.workspace=ws_cccccccccccccccccccc;")
})

test.each(["agents", "", "ws_bbbb", "WS_BBBBBBBBBBBBBBBBBBBB", "ws_bbbbbbbbbbbbbbbbbbbb; domain=evil.example", "ws_bbbbbbbbbbbbbbbbbbbb\n", " ws_bbbbbbbbbbbbbbbbbbbb"])(
  "only an id goes in the cookie, never %j",
  async (value) => {
    const { rememberWorkspace } = await fresh()
    const set = vi.spyOn(document, "cookie", "set")
    rememberWorkspace(value)
    expect(set).not.toHaveBeenCalled()
  },
)
