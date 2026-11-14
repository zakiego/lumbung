import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, test, vi } from "vitest"
import { Toaster } from "@/components/ui/toast"
import { CodeBlock, ConnectDetails, SnippetTabs } from "./connect"

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

test("each detail row copies its own value, and a row marked copy: false has no button", async () => {
  const user = userEvent.setup()
  const writeText = vi.fn(async () => {})
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } })
  render(
    <ConnectDetails
      rows={[
        { label: "S3 endpoint", value: "https://db.example.com/v1/s3" },
        { label: "Addressing", value: "Path-style", copy: false },
      ]}
    />,
  )
  await user.click(screen.getByRole("button", { name: "Copy S3 endpoint" }))
  expect(writeText).toHaveBeenCalledWith("https://db.example.com/v1/s3")
  expect(screen.queryByRole("button", { name: "Copy Addressing" })).toBeNull()
})

test("snippet tabs show the first snippet and switch on click", async () => {
  const user = userEvent.setup()
  render(
    <SnippetTabs
      label="Connect with"
      snippets={[
        { value: "sdk", label: "SDK", code: "createClient()" },
        { value: "curl", label: "curl", code: "curl -s https://db.example.com" },
      ]}
    />,
  )
  expect(screen.getByText("createClient()")).toBeTruthy()
  await user.click(screen.getByRole("tab", { name: "curl" }))
  await waitFor(() => expect(screen.getByText("curl -s https://db.example.com")).toBeTruthy())
})
