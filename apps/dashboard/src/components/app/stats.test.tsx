import { act, render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { AwaitLatest } from "./stats"

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}

const view = (promise: Promise<string>) => (
  <AwaitLatest promise={promise} fallback={<p>Loading</p>} dim>
    {(value) => <p>{value}</p>}
  </AwaitLatest>
)

describe("AwaitLatest", () => {
  it("shows the fallback only on the first load", async () => {
    const first = deferred<string>()
    // Rendered inside an async act so React's Suspense retries get flushed.
    let rerender!: ReturnType<typeof render>["rerender"]
    await act(async () => {
      rerender = render(view(first.promise)).rerender
    })
    expect(screen.getByText("Loading")).toBeTruthy()

    await act(async () => first.resolve("24h"))
    expect(await screen.findByText("24h")).toBeTruthy()

    // A loader reload hands over a new promise. The last result stays up.
    const second = deferred<string>()
    await act(async () => rerender(view(second.promise)))
    expect(screen.queryByText("Loading")).toBeNull()
    expect(screen.getByText("24h").parentElement?.getAttribute("aria-busy")).toBe("true")

    await act(async () => second.resolve("48h"))
    expect((await screen.findByText("48h")).parentElement?.hasAttribute("aria-busy")).toBe(false)
    expect(screen.queryByText("Loading")).toBeNull()
  })
})
