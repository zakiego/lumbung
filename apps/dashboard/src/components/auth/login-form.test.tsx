import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"
import { LoginForm } from "./auth-forms"

const agent = createAgent()

test("an agent can find every control and sign in", async () => {
  const onSignIn = vi.fn(async (_email: string, _password: string) => {})
  render(<LoginForm onSignIn={onSignIn} onPasskey={async () => {}} />)

  const entries = agent.snapshot().filter((e) => !e.offPage && e.component !== "more")
  for (const entry of entries) expect(entry.label, `${entry.component} ${entry.ref} has no name`).toBeTruthy()
  const labels = entries.map((e) => e.label)
  for (const label of ["Email", "Password", "Sign in", "Sign in with a passkey"]) expect(labels).toContain(label)

  const result = await agent.act({
    steps: [
      { ref: "Email", action: "setValue", args: ["ana@example.com"] },
      { ref: "Password", action: "setValue", args: ["correct horse battery"] },
    ],
  })
  expect(result.ok, result.error?.message).toBe(true)
  const signIn = entries.find((e) => e.label === "Sign in")
  const press = await agent.act({ steps: [{ ref: signIn!.ref, action: "press" }] })
  expect(press.ok, press.error?.message).toBe(true)
  await waitFor(() => expect(onSignIn).toHaveBeenCalledWith("ana@example.com", "correct horse battery"))
})

test("an agent may fill the password but never read it", async () => {
  render(<LoginForm onSignIn={async () => {}} onPasskey={async () => {}} />)
  const result = await agent.act({ steps: [{ ref: "Password", action: "setValue", args: ["correct horse battery"] }] })
  expect(result.ok, result.error?.message).toBe(true)
  expect(screen.getByLabelText<HTMLInputElement>("Password").value).toBe("correct horse battery")
  const password = agent.snapshot().find((e) => e.label === "Password" && !e.offPage)
  expect(password).toBeDefined()
  expect(JSON.stringify(password)).not.toContain("correct horse")
})

test("a wrong password shows the server's message under the field", async () => {
  const user = userEvent.setup()
  render(
    <LoginForm
      onSignIn={async () => {
        throw new Error("Invalid email or password")
      }}
      onPasskey={async () => {}}
    />,
  )
  await user.type(screen.getByLabelText("Email"), "ana@example.com")
  await user.type(screen.getByLabelText("Password"), "nope nope nope")
  await user.click(screen.getByRole("button", { name: "Sign in" }))
  expect(await screen.findByText("Invalid email or password")).toBeTruthy()

  await user.type(screen.getByLabelText("Password"), "!")
  expect(screen.queryByText("Invalid email or password")).toBeNull()
})

test("empty fields don't submit", async () => {
  const user = userEvent.setup()
  const onSignIn = vi.fn(async () => {})
  render(<LoginForm onSignIn={onSignIn} onPasskey={async () => {}} />)
  await user.click(screen.getByRole("button", { name: "Sign in" }))
  expect(onSignIn).not.toHaveBeenCalled()
})

test("the passkey button runs the passkey flow and shows its failure", async () => {
  const user = userEvent.setup()
  const onPasskey = vi.fn(async () => {
    throw new Error("The passkey prompt was closed.")
  })
  render(<LoginForm onSignIn={async () => {}} onPasskey={onPasskey} />)
  await user.click(screen.getByRole("button", { name: "Sign in with a passkey" }))
  expect(onPasskey).toHaveBeenCalledOnce()
  expect(await screen.findByText("The passkey prompt was closed.")).toBeTruthy()
})

test("the password toggle shows and hides it", async () => {
  const user = userEvent.setup()
  render(<LoginForm onSignIn={async () => {}} onPasskey={async () => {}} />)
  const input = screen.getByLabelText<HTMLInputElement>("Password")
  expect(input.type).toBe("password")
  await user.click(screen.getByRole("button", { name: "Show password" }))
  expect(input.type).toBe("text")
})

test("on an extra origin the passkey button gives way to a note", () => {
  render(<LoginForm onSignIn={async () => {}} onPasskey={async () => {}} passkeysOnlyAt="https://db.example.com" />)
  expect(screen.queryByRole("button", { name: "Sign in with a passkey" })).toBeNull()
  expect(screen.getByText("https://db.example.com")).toBeTruthy()
  expect(screen.getByRole("button", { name: "Sign in" })).toBeTruthy()
})
