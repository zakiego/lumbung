import { Outlet, createFileRoute, redirect } from "@tanstack/react-router"
import { AppShell } from "@/components/app/shell"
import { listDatabases } from "@/fns/databases"
import { APP_BASE } from "@/lib/paths"
import { loadSession } from "@/lib/session-cache"

export const Route = createFileRoute("/_app")({
  beforeLoad: async ({ location }) => {
    const session = await loadSession()
    if (session.needsSetup) throw redirect({ to: "/setup" })
    if (!session.viewer) throw redirect({ to: "/login", search: { next: `${APP_BASE}${location.href}` } })
    return { viewer: session.viewer }
  },
  // The sidebar still renders when the engine is down; pages show their own errors.
  loader: async () => ({ databases: await listDatabases().catch(() => null) }),
  component: AppLayout,
})

function AppLayout() {
  const { viewer } = Route.useRouteContext()
  const { databases } = Route.useLoaderData()
  return (
    <AppShell viewer={viewer} databases={databases}>
      <Outlet />
    </AppShell>
  )
}
