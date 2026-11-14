import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from "@tanstack/react-router"
import { render } from "@testing-library/react"
import type { ReactNode } from "react"

// Components with <Link> need a router; this one renders `ui` at every path.
export async function renderWithRouter(ui: ReactNode) {
  const router = createRouter({ routeTree: createRootRoute({ component: () => ui }), history: createMemoryHistory({ initialEntries: ["/"] }) })
  await router.load()
  return render(<RouterProvider router={router} />)
}
