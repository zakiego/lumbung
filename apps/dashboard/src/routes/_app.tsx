import { Outlet, createFileRoute, redirect } from "@tanstack/react-router"
import { APP_BASE } from "@/lib/paths"
import { loadSession, primeSession } from "@/lib/session-cache"

// Signed-in pages. The layouts below pick the workspace: /w/$ws from the URL,
// and the pages outside a workspace from the one last opened.
export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ location }) => {
    const session = await loadSession()
    if (session.needsSetup) throw redirect({ to: "/setup" })
    if (!session.viewer) throw redirect({ to: "/login", search: { next: `${APP_BASE}${location.href}` } })
    return {
      account: session.viewer,
      // Null when the engine didn't answer.
      workspaces: session.workspaces,
      lastWorkspace: session.lastWorkspace,
      engineUrl: session.engineUrl ?? "",
      passkeys: session.passkeys,
      dashboardUrl: session.dashboardUrl,
    }
  },
  component: AppLayout,
})

function AppLayout() {
  const { account, workspaces, lastWorkspace, engineUrl, passkeys, dashboardUrl } = Route.useRouteContext()
  primeSession({ viewer: account, workspaces, lastWorkspace, needsSetup: false, engineUrl, passkeys, dashboardUrl })
  return <Outlet />
}
