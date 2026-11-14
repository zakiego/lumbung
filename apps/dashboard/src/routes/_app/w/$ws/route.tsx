import { Link, Outlet, createFileRoute, notFound, useRouteContext } from "@tanstack/react-router"
import { BoxIcon } from "lucide-react"
import { useEffect } from "react"
import { AppShell, LoneShell } from "@/components/app/shell"
import { ErrorState } from "@/components/app/states"
import { buttonVariants } from "@/components/ui/button"
import { Empty } from "@/components/ui/surface"
import { listBuckets } from "@/fns/buckets"
import { listDatabases } from "@/fns/databases"
import { pageTitle } from "@/lib/paths"
import { type Person, findWorkspace, rememberWorkspace } from "@/lib/workspace"

// Everything inside one workspace. The workspace comes from the URL, and the
// person's role in it from their session; someone with no role in it gets the
// same page as for a workspace that doesn't exist.
export const Route = createFileRoute("/_app/w/$ws")({
  beforeLoad: ({ context, params }) => {
    if (!context.workspaces) throw new Error("Can't reach the engine.")
    const workspace = findWorkspace(context.workspaces, params.ws)
    if (!workspace) throw notFound()
    const viewer: Person = { ...context.account, role: workspace.role }
    return { viewer, workspace }
  },
  // The sidebar still renders when the engine is down; pages show their own errors.
  loader: async ({ params }) => {
    const [databases, buckets] = await Promise.all([
      listDatabases({ data: { ws: params.ws } }).catch(() => null),
      listBuckets({ data: { ws: params.ws } }).catch(() => null),
    ])
    return { databases, buckets }
  },
  head: ({ params }) => ({ meta: [{ title: pageTitle(params.ws) }] }),
  component: WorkspaceLayout,
  notFoundComponent: NoWorkspace,
  errorComponent: ({ error }) => <Unreachable error={error} />,
})

function WorkspaceLayout() {
  const { viewer, workspace, workspaces } = Route.useRouteContext()
  const { databases, buckets } = Route.useLoaderData()
  useEffect(() => rememberWorkspace(workspace.id), [workspace.id])
  return (
    <AppShell viewer={viewer} workspace={workspace} workspaces={workspaces ?? []} databases={databases} buckets={buckets}>
      <Outlet />
    </AppShell>
  )
}

// Both states keep the shell, so the person can still reach their account and sign out.
function NoWorkspace() {
  const { account } = useRouteContext({ from: "/_app" })
  return (
    <LoneShell account={account} title="Workspace">
      <Empty icon={<BoxIcon />} title="That workspace doesn't exist">
        <Link to="/" className={buttonVariants({ variant: "secondary" })}>
          Go to your workspace
        </Link>
      </Empty>
    </LoneShell>
  )
}

function Unreachable({ error }: { error: unknown }) {
  const { account } = useRouteContext({ from: "/_app" })
  return (
    <LoneShell account={account} title="Workspace">
      <ErrorState error={error} title="Couldn't load this workspace" />
    </LoneShell>
  )
}
