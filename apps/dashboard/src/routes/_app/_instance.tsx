import { Outlet, createFileRoute } from "@tanstack/react-router"
import { AppShell } from "@/components/app/shell"
import { listBuckets } from "@/fns/buckets"
import { listDatabases } from "@/fns/databases"
import { type Person, lastWorkspace, pickWorkspace } from "@/lib/workspace"

// Pages outside a workspace: members, the instance's settings, the account.
// They keep the sidebar of the workspace the person last had open.
export const Route = createFileRoute("/_app/_instance")({
  beforeLoad: ({ context }) => {
    const workspace = pickWorkspace(context.workspaces ?? [], lastWorkspace() ?? context.lastWorkspace)
    const viewer: Person = { ...context.account, role: context.account.owner ? "owner" : (workspace?.role ?? "readonly") }
    return { viewer, workspace }
  },
  loader: async ({ context }) => {
    const ws = context.workspace?.name
    if (!ws) return { databases: [], buckets: [] }
    const [databases, buckets] = await Promise.all([listDatabases({ data: { ws } }).catch(() => null), listBuckets({ data: { ws } }).catch(() => null)])
    return { databases, buckets }
  },
  component: InstanceLayout,
})

function InstanceLayout() {
  const { viewer, workspace, workspaces } = Route.useRouteContext()
  const { databases, buckets } = Route.useLoaderData()
  return (
    <AppShell viewer={viewer} workspace={workspace} workspaces={workspaces ?? []} databases={databases} buckets={buckets}>
      <Outlet />
    </AppShell>
  )
}
