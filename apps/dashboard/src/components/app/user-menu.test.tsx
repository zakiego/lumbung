import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { forwardRef } from "react"
import { expect, test, vi } from "vitest"
import type { Role } from "@/lib/roles"
import { UserMenu } from "./shell"

vi.mock("@tanstack/react-router", () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  Link: forwardRef<HTMLAnchorElement, { to: string }>(({ to, ...props }, ref) => <a ref={ref} href={to} {...props} />),
}))

const viewer = { id: "u1", name: "Ada", email: "ada@example.com", owner: true, role: "owner" as const }

test("the user menu opens with the email heading the account group", async () => {
  const user = userEvent.setup()
  render(<UserMenu viewer={viewer} />)
  await user.click(screen.getByRole("button", { name: /Ada/ }))

  const groups = screen.getAllByRole("group")
  expect(groups.some((g) => g.textContent?.includes("ada@example.com") && g.textContent.includes("Account"))).toBe(true)
  expect(screen.getByRole("menuitem", { name: "Account" }).getAttribute("href")).toBe("/account")
  expect(screen.getByRole("menuitemradio", { name: "System" })).toBeTruthy()
  expect(screen.getByRole("menuitem", { name: "Sign out" })).toBeTruthy()
})

test.each<[Role, string]>([
  ["owner", "Owner"],
  ["admin", "Admin"],
  ["member", "Member"],
  ["readonly", "Read-only"],
])("the trigger shows the role in the workspace on screen: %s", (role, label) => {
  render(<UserMenu viewer={{ ...viewer, owner: role === "owner", role }} />)
  const trigger = screen.getByRole("button", { name: /Ada/ })
  expect(within(trigger).getByText(label)).toBeTruthy()
  for (const other of ["Owner", "Admin", "Member", "Read-only"]) if (other !== label) expect(within(trigger).queryByText(other)).toBeNull()
})

test("someone with no workspace yet shows no role", () => {
  render(<UserMenu viewer={{ ...viewer, owner: false, role: "readonly" }} placed={false} />)
  const trigger = screen.getByRole("button", { name: /Ada/ })
  for (const label of ["Owner", "Admin", "Member", "Read-only"]) expect(within(trigger).queryByText(label)).toBeNull()
})

test("a missing name falls back to the email", () => {
  render(<UserMenu viewer={{ ...viewer, name: "" }} />)
  expect(screen.getByRole("button", { name: /ada@example\.com/ })).toBeTruthy()
})
