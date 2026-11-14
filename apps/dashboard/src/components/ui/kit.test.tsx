import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { createAgent } from "@ui-sdk/react/agent"
import { expect, test, vi } from "vitest"
import { TypedConfirm } from "./alert-dialog"
import { Checkbox, Switch } from "./checkbox"
import { ScrollArea } from "./layout"
import { Menu, MenuContent, MenuItem, MenuRadioGroup, MenuRadioItem, MenuTrigger } from "./menu"
import { Pagination } from "./pagination"
import { Select, SelectContent, SelectItem, SelectTrigger } from "./select"
import { Tabs, TabsList, TabsPanel, TabsTab } from "./tabs"
import { Toaster, toast } from "./toast"
import { Tooltip } from "./tooltip"

let approve = false
const agent = createAgent({ allow: (step) => !step.confirm || approve })

function Confirm({ onConfirm }: { onConfirm: () => Promise<unknown> }) {
  return (
    <TypedConfirm
      trigger="Delete database"
      title="Delete shop?"
      confirmText="shop"
      actionLabel="Delete forever"
      agentConfirm="Deletes the shop database"
      onConfirm={onConfirm}
    />
  )
}

test("the typed confirmation unlocks only for the exact name", async () => {
  const user = userEvent.setup()
  const onConfirm = vi.fn(async () => {})
  render(<Confirm onConfirm={onConfirm} />)
  const trigger = screen.getByRole("button", { name: "Delete database" })

  await user.click(trigger)
  await user.click(screen.getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull())
  expect(document.activeElement).toBe(trigger)
  expect(onConfirm).not.toHaveBeenCalled()

  await user.click(trigger)
  const action = screen.getByRole("button", { name: "Delete forever" }) as HTMLButtonElement
  expect(action.disabled).toBe(true)
  await user.type(screen.getByLabelText("Type shop to confirm"), "Shop")
  expect(action.disabled).toBe(true)
  await user.clear(screen.getByLabelText("Type shop to confirm"))
  await user.type(screen.getByLabelText("Type shop to confirm"), "shop")
  await user.click(screen.getByRole("button", { name: "Delete forever" }))
  expect(onConfirm).toHaveBeenCalledOnce()
  await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull())
})

test("a failed action keeps the dialog open with its message", async () => {
  const user = userEvent.setup()
  render(
    <Confirm
      onConfirm={async () => {
        throw new Error("The database is busy.")
      }}
    />,
  )
  await user.click(screen.getByRole("button", { name: "Delete database" }))
  await user.type(screen.getByLabelText("Type shop to confirm"), "shop")
  await user.click(screen.getByRole("button", { name: "Delete forever" }))
  expect(await screen.findByText("The database is busy.")).toBeTruthy()
  expect(screen.getByRole("alertdialog", { name: "Delete shop?" })).toBeTruthy()
})

test("an agent needs a person's yes to delete", async () => {
  const onConfirm = vi.fn(async () => {})
  render(<Confirm onConfirm={onConfirm} />)
  const remove = {
    steps: [
      { ref: "Delete database", action: "press" },
      { ref: "Type shop to confirm", action: "setValue", args: ["shop"] },
      { ref: "Delete shop?", action: "press", args: ["Delete forever"] },
    ],
  }
  approve = false
  expect((await agent.act(remove)).ok).toBe(false)
  expect(onConfirm).not.toHaveBeenCalled()
  // The refused press leaves the dialog open with the name typed; a person says yes this time.
  approve = true
  const result = await agent.act({ steps: [remove.steps[2]!] })
  expect(result.ok, result.error?.message).toBe(true)
  await waitFor(() => expect(onConfirm).toHaveBeenCalledOnce())
})

test("the kit's parts render and work together", async () => {
  const user = userEvent.setup()
  const onBranch = vi.fn()
  render(
    <>
      <Tabs value="a">
        <TabsList aria-label="Pages">
          <TabsTab value="a" render={<a href="#a" />}>
            A
          </TabsTab>
          <TabsTab value="b" render={<a href="#b" />} count={3}>
            B
          </TabsTab>
        </TabsList>
        <TabsPanel value="a">panel</TabsPanel>
      </Tabs>
      <Select items={["main", "dev"]} defaultValue="main" onValueChange={onBranch}>
        <SelectTrigger aria-label="Branch" />
        <SelectContent>
          <SelectItem value="main">main</SelectItem>
          <SelectItem value="dev">dev</SelectItem>
        </SelectContent>
      </Select>
      <Menu>
        <MenuTrigger>Account</MenuTrigger>
        <MenuContent>
          <MenuItem>Profile</MenuItem>
          <MenuRadioGroup defaultValue="dark">
            <MenuRadioItem value="dark">Dark</MenuRadioItem>
          </MenuRadioGroup>
        </MenuContent>
      </Menu>
      <Tooltip content="Copies the snippet">
        <button type="button">Copy</button>
      </Tooltip>
      <ScrollArea axis="x">
        <pre>code</pre>
      </ScrollArea>
      <Pagination pageCount={5} />
      <label>
        <Checkbox /> Keep it
      </label>
      <label>
        <Switch /> Daily
      </label>
      <Toaster />
    </>,
  )
  expect(screen.getByRole("tab", { name: "B 3" }).getAttribute("href")).toBe("#b")
  await user.click(screen.getByRole("combobox", { name: "Branch" }))
  await user.click(screen.getByRole("option", { name: "dev" }))
  expect(onBranch.mock.calls[0]?.[0]).toBe("dev")

  const account = screen.getByRole("button", { name: "Account" })
  await user.click(account)
  expect(screen.getByRole("menuitem", { name: "Profile" })).toBeTruthy()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("menu")).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(account))

  const keep = screen.getByRole("checkbox", { name: "Keep it" })
  keep.focus()
  await user.keyboard(" ")
  expect(keep.getAttribute("aria-checked")).toBe("true")
  const daily = screen.getByRole("switch", { name: "Daily" })
  daily.focus()
  await user.keyboard(" ")
  expect(daily.getAttribute("aria-checked")).toBe("true")

  expect(screen.getByRole("navigation", { name: "Pages" })).toBeTruthy()
  toast.success("Saved")
  expect(await screen.findByText("Saved")).toBeTruthy()
})
