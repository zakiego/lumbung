import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { forwardRef } from "react"
import { expect, test, vi } from "vitest"
import { UserMenu } from "./shell"

vi.mock("@tanstack/react-router", () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  Link: forwardRef<HTMLAnchorElement, { to: string }>(({ to, ...props }, ref) => <a ref={ref} href={to} {...props} />),
}))

const viewer = { id: "u1", name: "Ada", email: "ada@example.com", role: "owner" as const }

test("the user menu opens with the email heading the account group", async () => {
  const user = userEvent.setup()
  render(<UserMenu viewer={viewer} />)
  await user.click(screen.getByRole("button", { name: /Ada/ }))

  const groups = screen.getAllByRole("group")
  expect(groups.some((g) => g.textContent?.includes("ada@example.com") && g.textContent.includes("Account"))).toBe(true)
  expect(screen.getByRole("menuitemradio", { name: "System" })).toBeTruthy()
  expect(screen.getByRole("menuitem", { name: "Sign out" })).toBeTruthy()
})
