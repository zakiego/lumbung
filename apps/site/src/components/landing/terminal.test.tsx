import { act, render, screen } from "@testing-library/react"
import { createAgent } from "@ui-sdk/react/agent"
import { afterEach, beforeEach, expect, test, vi } from "vitest"
import { FakeIntersectionObserver } from "@/test/setup"
import { next, POSTER, session, Terminal, TYPE_MS, type Frame } from "./terminal"

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

const lines = (root: HTMLElement) => [...root.querySelectorAll("[data-screen] > div")] as HTMLElement[]
// Each tick schedules the next one from an effect, so advance one at a time.
async function tick(times: number) {
  for (let i = 0; i < times; i++) await act(async () => vi.advanceTimersByTime(TYPE_MS))
}
const typed = (line: HTMLElement) => line.querySelector(".grid > span:last-child")?.textContent

test("the loop starts at the first step and types one character at a time", async () => {
  const { container } = render(<Terminal />)
  const [first, second] = lines(container)
  expect(container.querySelector("[data-terminal]")?.getAttribute("data-terminal")).toBe("live")
  expect(typed(first)).toBe("")
  expect(second.className).toContain("invisible")

  await tick(7)
  expect(typed(first)).toBe(session[0].cmd.slice(0, 7))
})

test("with reduced motion it shows the finished session and no pause control", () => {
  globalThis.__reducedMotion = true
  const { container } = render(<Terminal />)
  expect(container.querySelector("[data-terminal]")?.getAttribute("data-terminal")).toBe("poster")
  const all = lines(container)
  expect(typed(all[POSTER])).toBe(session[POSTER].cmd)
  expect(all[POSTER].className).not.toContain("invisible")
  expect(all[POSTER + 1].className).toContain("invisible")
  expect(screen.queryByRole("button", { name: "Pause demo" })).toBeNull()
})

test("Pause demo stops the typing and pressing it again goes on", async () => {
  const { container } = render(<Terminal />)
  const pause = screen.getByRole("button", { name: "Pause demo" })
  await tick(3)
  await act(async () => pause.click())
  expect(pause.getAttribute("aria-pressed")).toBe("true")
  const before = typed(lines(container)[0])
  await tick(20)
  expect(typed(lines(container)[0])).toBe(before)

  await act(async () => pause.click())
  await tick(2)
  expect(typed(lines(container)[0])?.length).toBe((before?.length ?? 0) + 2)
})

test("one full loop runs every step in order and comes back to the start", () => {
  let frame: NonNullable<Frame> = { step: 0, typed: 0, ran: false }
  const steps: number[] = []
  let total = 0
  do {
    const { frame: following, delay } = next(frame)
    if (following.step !== frame.step) steps.push(frame.step)
    total += delay
    frame = following
  } while (frame.step !== 0 || frame.typed !== 0 || frame.ran)
  expect(steps).toEqual(session.map((_, i) => i))
  // About 26s: long enough to read every step, short enough that a visitor
  // sees the loop come back around.
  expect(total).toBeLessThan(27_000)
})

test("an agent can find and press the pause control", async () => {
  vi.useRealTimers()
  const agent = createAgent()
  render(<Terminal />)
  const entries = agent.snapshot().filter((entry) => !entry.offPage && entry.component !== "more")
  expect(entries.length).toBeGreaterThan(0)
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()

  const result = await act(async () => agent.act({ steps: [{ ref: "Pause demo", action: "setPressed", args: [true] }] }))
  expect(result.ok).toBe(true)
  expect(screen.getByRole("button", { name: "Pause demo" }).getAttribute("aria-pressed")).toBe("true")
})

test("scrolled off screen, the loop holds still and picks up where it was", async () => {
  const { container } = render(<Terminal />)
  await tick(4)
  const observer = FakeIntersectionObserver.instances.at(-1)!
  await act(async () => observer.fire(false))
  const before = typed(lines(container)[0])
  await tick(20)
  expect(typed(lines(container)[0])).toBe(before)

  await act(async () => observer.fire(true))
  await tick(1)
  expect(typed(lines(container)[0])?.length).toBe((before?.length ?? 0) + 1)
})

test("in a hidden tab, the loop holds still", async () => {
  const { container } = render(<Terminal />)
  await tick(2)
  const state = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden")
  await act(async () => document.dispatchEvent(new Event("visibilitychange")))
  const before = typed(lines(container)[0])
  await tick(20)
  expect(typed(lines(container)[0])).toBe(before)

  state.mockReturnValue("visible")
  await act(async () => document.dispatchEvent(new Event("visibilitychange")))
  await tick(1)
  expect(typed(lines(container)[0])?.length).toBe((before?.length ?? 0) + 1)
  state.mockRestore()
})

test("unmounting stops the timer and lets go of the observer and the tab listener", async () => {
  const remove = vi.spyOn(document, "removeEventListener")
  const { unmount } = render(<Terminal />)
  await tick(2)
  const observer = FakeIntersectionObserver.instances.at(-1)!
  unmount()
  expect(observer.disconnected).toBe(true)
  expect(remove).toHaveBeenCalledWith("visibilitychange", expect.any(Function))
  expect(vi.getTimerCount()).toBe(0)
  remove.mockRestore()
})

test("pausing stops the status dot's pulse too", async () => {
  const { container } = render(<Terminal />)
  const dot = () => container.querySelector(".bg-success.rounded-full")!
  expect(dot().className).toContain("animate-")
  await act(async () => screen.getByRole("button", { name: "Pause demo" }).click())
  expect(dot().className).not.toContain("animate-")
})
