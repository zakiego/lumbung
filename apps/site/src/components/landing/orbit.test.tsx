import { act, render, screen } from "@testing-library/react"
import { createAgent } from "@ui-sdk/react/agent"
import { renderToString } from "react-dom/server"
import { expect, test, vi } from "vitest"
import { FakeIntersectionObserver } from "@/test/setup"
import { Orbit } from "./orbit"

const stage = (root: HTMLElement) => root.querySelector("[data-stage]")!
const pause = () => screen.getByRole("button", { name: "Pause animation" })

test("the graph is a figure with a caption, and the drawing is hidden from screen readers", () => {
  const { container } = render(<Orbit />)
  expect(screen.getByRole("figure").textContent).toContain("Two branches and a file bucket circle it")
  expect(container.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true")
  // Three moons, and no words to read while they move.
  expect(container.querySelectorAll(".orbit-spin")).toHaveLength(3)
  expect(container.querySelector("text")).toBeNull()
})

test("moons on neighboring orbits can never touch, whatever their speeds", () => {
  const { container } = render(<Orbit />)
  const orbits = [...container.querySelectorAll("[data-orbit]")].map((orbit) => [Number(orbit.getAttribute("rx")), Number(orbit.getAttribute("ry"))])
  const moon = Number(container.querySelector("[data-moon]")?.getAttribute("r"))
  const main = Number(container.querySelector("circle:not(.ring-pulse):not([data-moon])")?.getAttribute("r"))
  expect(orbits).toHaveLength(3)

  // The inner moon clears main at its closest, the top of the flat orbit.
  expect(orbits[0][1] - main - moon).toBeGreaterThan(2)
  const steps = 720
  const point = ([rx, ry]: number[], i: number) => [rx * Math.cos((i / steps) * 2 * Math.PI), ry * Math.sin((i / steps) * 2 * Math.PI)]
  for (const [inner, outer] of [orbits.slice(0, 2), orbits.slice(1)]) {
    let closest = Number.POSITIVE_INFINITY
    for (let i = 0; i < steps; i++)
      for (let j = 0; j < steps; j++) {
        const [a, b] = [point(inner, i), point(outer, j)]
        closest = Math.min(closest, Math.hypot(a[0] - b[0], a[1] - b[1]))
      }
    expect(closest - moon * 2).toBeGreaterThan(3)
  }
})

test("Pause animation holds the loop and pressing it again lets it run", async () => {
  const { container } = render(<Orbit />)
  expect(stage(container).getAttribute("data-paused")).toBe("false")

  await act(async () => pause().click())
  expect(pause().getAttribute("aria-pressed")).toBe("true")
  expect(stage(container).getAttribute("data-paused")).toBe("true")

  await act(async () => pause().click())
  expect(pause().getAttribute("aria-pressed")).toBe("false")
  expect(stage(container).getAttribute("data-paused")).toBe("false")
})

test("the server renders no pause control, since the loop needs no JS to run", () => {
  expect(renderToString(<Orbit />)).not.toContain("Pause animation")
})

test("with reduced motion there is no loop, so no pause control", () => {
  globalThis.__reducedMotion = true
  render(<Orbit />)
  expect(screen.queryByRole("button", { name: "Pause animation" })).toBeNull()
})

test("scrolled off screen, the loop holds still and picks up again", async () => {
  const { container } = render(<Orbit />)
  const observer = FakeIntersectionObserver.instances.at(-1)!
  await act(async () => observer.fire(false))
  expect(stage(container).getAttribute("data-paused")).toBe("true")

  await act(async () => observer.fire(true))
  expect(stage(container).getAttribute("data-paused")).toBe("false")
})

test("in a hidden tab, the loop holds still", async () => {
  const { container } = render(<Orbit />)
  const state = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden")
  await act(async () => document.dispatchEvent(new Event("visibilitychange")))
  expect(stage(container).getAttribute("data-paused")).toBe("true")

  state.mockReturnValue("visible")
  await act(async () => document.dispatchEvent(new Event("visibilitychange")))
  expect(stage(container).getAttribute("data-paused")).toBe("false")
  state.mockRestore()
})

test("unmounting lets go of the observer and the tab listener", () => {
  const remove = vi.spyOn(document, "removeEventListener")
  const { unmount } = render(<Orbit />)
  const observer = FakeIntersectionObserver.instances.at(-1)!
  unmount()
  expect(observer.disconnected).toBe(true)
  expect(remove).toHaveBeenCalledWith("visibilitychange", expect.any(Function))
  remove.mockRestore()
})

test("an agent can find and press the pause control", async () => {
  const agent = createAgent()
  render(<Orbit />)
  const entries = agent.snapshot().filter((entry) => !entry.offPage && entry.component !== "more")
  expect(entries.length).toBeGreaterThan(0)
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()

  const result = await act(async () => agent.act({ steps: [{ ref: "Pause animation", action: "setPressed", args: [true] }] }))
  expect(result.ok).toBe(true)
  expect(pause().getAttribute("aria-pressed")).toBe("true")
})
