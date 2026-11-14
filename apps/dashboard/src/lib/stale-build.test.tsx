import { screen } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { ErrorState } from "@/components/app/states"
import { renderWithRouter } from "@/test/router"
import { isStaleBuildError, reloadForNewBuild } from "./stale-build"

describe("isStaleBuildError", () => {
  it.each([
    "Importing a module script failed.",
    "Failed to fetch dynamically imported module: https://example.test/app/assets/schema-abc.js",
    "error loading dynamically imported module: https://example.test/app/assets/schema-abc.js",
  ])("matches %s", (message) => {
    expect(isStaleBuildError(new TypeError(message))).toBe(true)
  })

  it("ignores other errors", () => {
    expect(isStaleBuildError(new Error("database not found"))).toBe(false)
    expect(isStaleBuildError(undefined)).toBe(false)
  })
})

describe("reloadForNewBuild", () => {
  const reload = vi.fn()
  const T = 1_700_000_000_000

  beforeEach(() => {
    sessionStorage.clear()
    vi.stubGlobal("location", { ...window.location, reload })
  })

  afterEach(() => {
    reload.mockReset()
    vi.unstubAllGlobals()
  })

  it("reloads once, then holds off for the cooldown", () => {
    expect(reloadForNewBuild(T)).toBe(true)
    expect(reloadForNewBuild(T + 5_000)).toBe(false)
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it("reloads again for a later deploy in the same tab", () => {
    reloadForNewBuild(T)
    expect(reloadForNewBuild(T + 60_000)).toBe(true)
    expect(reload).toHaveBeenCalledTimes(2)
  })
})

describe("ErrorState", () => {
  it("offers a reload instead of the raw import error", async () => {
    await renderWithRouter(<ErrorState error={new TypeError("Importing a module script failed.")} />)
    expect(screen.getByRole("heading", { name: "Lumbung was updated" })).toBeTruthy()
    expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy()
    expect(screen.queryByText(/Importing a module script/)).toBeNull()
  })

  it("still shows other errors as they are", async () => {
    await renderWithRouter(<ErrorState error={new Error("database not found")} />)
    expect(screen.getByText("database not found")).toBeTruthy()
    expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy()
  })
})
