import { render, screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import * as worst from "@/test/worst-case"
import { renderWithRouter } from "@/test/router"
import { RecentActivity, targetLabel } from "./activity"
import { DatabaseTable } from "./database-table"

test("databases: a 63 character name, errors, no traffic, a database that didn't report", async () => {
  await renderWithRouter(<DatabaseTable databases={worst.databases(3)} usage={worst.usage} />)
  const link = screen.getByRole("link", { name: worst.LONG_DB })
  expect(link.getAttribute("href")).toBe(`/w/default/db/${worst.LONG_DB}`)
  expect(screen.getByText("1 error")).toBeTruthy()
  expect(screen.getByText("1,284,000")).toBeTruthy()
  expect(screen.getByText("5.00 GB")).toBeTruthy()
  expect(screen.getByText("Never")).toBeTruthy()
  const shop = screen.getByRole("link", { name: "shop" }).closest("tr")!
  expect(within(shop).getByText("Couldn't load")).toBeTruthy()
})

test("databases: stats that failed for every database, and 500 rows", async () => {
  const { unmount } = await renderWithRouter(<DatabaseTable databases={worst.databases(3)} usage={null} />)
  expect(screen.getAllByText("Couldn't load")).toHaveLength(3)
  unmount()
  const many = await renderWithRouter(<DatabaseTable databases={worst.databases(500)} usage={undefined} />)
  expect(many.container.querySelectorAll("tbody tr")).toHaveLength(500)
})

test("databases: links go to the workspace on screen", async () => {
  const agents = { id: "ws_bbbbbbbbbbbbbbbbbbbb", name: "agents", default: false, role: "readonly" as const }
  await renderWithRouter(<DatabaseTable databases={worst.databases(3)} usage={worst.usage} />, agents)
  expect(screen.getByRole("link", { name: "shop" }).getAttribute("href")).toBe("/w/agents/db/shop")
  expect(screen.getAllByRole("link").every((a) => a.getAttribute("href")!.startsWith("/w/agents/db/"))).toBe(true)
})

test("activity: database names instead of ids, token actors, failures and the empty state", () => {
  const names = new Map([["db_01JCX9", worst.LONG_DB]])
  expect(targetLabel(`db_01JCX9/br_01JCXA/${worst.LONG_BRANCH}`, names)).toBe(worst.LONG_DB)
  expect(targetLabel("db_unknown/br_1", names)).toBe("db_unknown/br_1")
  const { unmount } = render(<RecentActivity entries={worst.audit(3)} names={names} />)
  expect(screen.getAllByText(worst.LONG_DB)[0]!.getAttribute("title")).toBe(`db_01JCX9/br_01JCXA/${worst.LONG_BRANCH}`)
  expect(screen.getByText(worst.LONG_EMAIL).closest("[title]")?.getAttribute("title")).toBe(worst.LONG_EMAIL)
  expect(screen.getByText("tok_01JCX9ZK4M2")).toBeTruthy()
  expect(screen.getByText("Failed:")).toBeTruthy()
  unmount()
  render(<RecentActivity entries={[]} />)
  expect(screen.getByText("Nothing logged yet")).toBeTruthy()
})
