import { Outlet, RouterProvider, createMemoryHistory, createRootRoute, createRoute, createRouter } from "@tanstack/react-router"
import { render } from "@testing-library/react"
import type { ReactNode } from "react"
import { WorkspaceProvider } from "@/components/app/workspace"
import type { WorkspaceInfo } from "@/lib/workspace"

export const TEST_WORKSPACE: WorkspaceInfo = { id: "ws_aaaaaaaaaaaaaaaaaaaa", name: "default", default: true, role: "owner" }

/** Wraps `ui` in the workspace on screen, for components that read it without a router. */
export function inWorkspace(ui: ReactNode, workspace: WorkspaceInfo | null = TEST_WORKSPACE) {
  return <WorkspaceProvider workspace={workspace}>{ui}</WorkspaceProvider>
}

/** A page's place in the route tree, for components that read route params: `{ route: "/w/$ws/tokens", at: "/w/agents/tokens" }`. */
export type Place = { route: string; at: string }

// Components with <Link> need a router; this one renders `ui` at every path, inside a workspace.
// With a `place`, `ui` is the page of that route instead, so it sees the route's params.
// The router comes back too, so a test can see where a click went.
export async function renderWithRouter(ui: ReactNode, workspace: WorkspaceInfo | null = TEST_WORKSPACE, place?: Place) {
  const page = () => inWorkspace(ui, workspace)
  const root = createRootRoute({ component: place ? Outlet : page, notFoundComponent: page })
  const routeTree = place ? root.addChildren([createRoute({ getParentRoute: () => root, path: place.route, component: page })]) : root
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [place?.at ?? "/"] }) })
  await router.load()
  return Object.assign(render(<RouterProvider router={router} />), { router })
}
