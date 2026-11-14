import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { afterEach, expect, test, vi } from "vitest"
import { CodeFrame } from "./code-frame"

afterEach(() => vi.unstubAllGlobals())

const frame = (
  <CodeFrame code="docker compose up -d" label="Install commands" copyLabel="Copy the install commands">
    <pre>
      <code>docker compose up -d</code>
    </pre>
  </CodeFrame>
)

test("a copy that works says Copied", async () => {
  const user = userEvent.setup()
  const writeText = vi.fn(async () => {})
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } })
  render(frame)
  await user.click(screen.getByRole("button", { name: "Copy the install commands" }))
  const live = () => document.querySelector("[aria-live]")?.textContent
  await waitFor(() => expect(live()?.trim()).toBe("Copied"))
  expect(writeText).toHaveBeenCalledWith("docker compose up -d")

  // A second copy inside the reset window changes the live text, so it's announced again.
  const first = live()
  await user.click(screen.getByRole("button", { name: "Copy the install commands" }))
  await waitFor(() => expect(live()).not.toBe(first))
  expect(live()?.trim()).toBe("Copied")
})

test("a blocked clipboard selects the code instead of claiming it copied", async () => {
  const user = userEvent.setup()
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText: vi.fn(async () => Promise.reject(new Error("denied"))) } })
  render(frame)
  await user.click(screen.getByRole("button", { name: "Copy the install commands" }))
  expect(await screen.findByText("Couldn't copy. The code is selected.")).toBeTruthy()
  expect(document.querySelector("[aria-live]")?.textContent).not.toContain("Copied")
  expect(window.getSelection()?.toString()).toBe("docker compose up -d")
})

test("the code is a named region and an agent finds every control by name", () => {
  const agent = createAgent()
  render(frame)
  // aria-label on a plain div is dropped; the region role is what keeps the name.
  expect(screen.getByRole("region", { name: "Install commands" }).querySelector("pre")).toBeTruthy()
  const entries = agent.snapshot().filter((entry) => !entry.offPage && entry.component !== "more")
  expect(entries.map((entry) => entry.label).sort()).toEqual(["Copy the install commands", "Install commands"])
})
