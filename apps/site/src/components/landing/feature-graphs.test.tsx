import { renderToStaticMarkup } from "react-dom/server"
import { expect, test } from "vitest"
import { AgentBranch, BranchTree, Clients, Files } from "./feature-graphs"

const graphs = { BranchTree, AgentBranch, Clients, Files }

// The page renders these with no island, so the markup alone has to be right.
test.each(Object.entries(graphs))("%s is a decorative drawing the page can play once", (_name, Graph) => {
  const html = renderToStaticMarkup(<Graph />)
  const root = document.createElement("div")
  root.innerHTML = html
  const svg = root.querySelector("svg")!
  expect(svg.getAttribute("aria-hidden")).toBe("true")
  expect(svg.classList.contains("fg")).toBe(true)
  expect(root.querySelector(".fg-draw, .fg-pop, .fg-comet")).not.toBeNull()
  expect(root.querySelector("button, a, [tabindex]")).toBeNull()
})

test("the client graph names the two clients the copy mentions", () => {
  const html = renderToStaticMarkup(<Clients />)
  expect(html).toContain(">libSQL<")
  expect(html).toContain(">Drizzle<")
})
