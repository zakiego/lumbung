import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { InviteForm } from "./auth-forms"

const details = () => Object.fromEntries([...document.querySelectorAll("dt")].map((dt) => [dt.textContent, dt.nextElementSibling?.textContent]))

test("an owner invite says Owner, any other shows the email alone and names no workspace", () => {
  const owner = render(<InviteForm email="cy@example.com" owner onAccept={async () => {}} />)
  expect(details()).toEqual({ Email: "cy@example.com", Access: "Owner" })
  owner.unmount()
  render(<InviteForm email="ben@example.com" owner={false} onAccept={async () => {}} />)
  expect(details()).toEqual({ Email: "ben@example.com" })
  for (const word of ["Access", "Role", "Owner", "Admin", "Member", "Read-only", "workspace"]) expect(screen.queryByText(new RegExp(word, "i")), word).toBeNull()
})

test("joining sends the name and the password, once the password is long enough", async () => {
  const user = userEvent.setup()
  const onAccept = vi.fn(async (_v: { name: string; password: string }) => {})
  render(<InviteForm email="ben@example.com" owner={false} onAccept={onAccept} />)
  await user.type(screen.getByLabelText("Your name"), "Ben")
  await user.type(screen.getByLabelText("Password"), "short")
  await user.click(screen.getByRole("button", { name: "Join" }))
  expect(await screen.findByText("Use at least 10 characters.")).toBeTruthy()
  expect(onAccept).not.toHaveBeenCalled()

  await user.clear(screen.getByLabelText("Password"))
  await user.type(screen.getByLabelText("Password"), "correct horse battery")
  await user.click(screen.getByRole("button", { name: "Join" }))
  await waitFor(() => expect(onAccept).toHaveBeenCalledOnce())
  expect(onAccept).toHaveBeenCalledWith({ name: "Ben", password: "correct horse battery" })
})

test("a refusal from the server shows under the password", async () => {
  const user = userEvent.setup()
  render(
    <InviteForm
      email="ben@example.com"
      owner={false}
      onAccept={async () => {
        throw new Error("This invite expired or was already used.")
      }}
    />,
  )
  await user.type(screen.getByLabelText("Your name"), "Ben")
  await user.type(screen.getByLabelText("Password"), "correct horse battery")
  await user.click(screen.getByRole("button", { name: "Join" }))
  expect(await screen.findByText("This invite expired or was already used.")).toBeTruthy()
})
