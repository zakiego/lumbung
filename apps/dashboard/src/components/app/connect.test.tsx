import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, test, vi } from "vitest"
import { Toaster } from "@/components/ui/toast"
import { CodeBlock } from "./connect"

afterEach(() => vi.unstubAllGlobals())

test("a failed copy says so and selects the text instead of claiming it copied", async () => {
  const user = userEvent.setup()
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText: vi.fn(async () => Promise.reject(new Error("denied"))) } })
  render(
    <>
      <CodeBlock code="lumbung query 'SELECT 1'" copyLabel="Copy snippet" />
      <Toaster />
    </>,
  )
  await user.click(screen.getByRole("button", { name: "Copy snippet" }))
  expect(await screen.findByText("Couldn't copy. Select it and copy.")).toBeTruthy()
  expect(screen.queryByText("Copied")).toBeNull()
  expect(window.getSelection()?.toString()).toBe("lumbung query 'SELECT 1'")
})

test("a copy that works says Copied", async () => {
  const user = userEvent.setup()
  const writeText = vi.fn(async () => {})
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } })
  render(<CodeBlock code="SELECT 1" copyLabel="Copy snippet" />)
  await user.click(screen.getByRole("button", { name: "Copy snippet" }))
  await waitFor(() => expect(screen.getByText("Copied")).toBeTruthy())
  expect(writeText).toHaveBeenCalledWith("SELECT 1")
})
