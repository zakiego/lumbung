import { createFileRoute, redirect } from "@tanstack/react-router"
import { BoxIcon } from "lucide-react"
import { LoneShell } from "@/components/app/shell"
import { ErrorState } from "@/components/app/states"
import { Empty } from "@/components/ui/surface"
import { lastWorkspace, pickWorkspace } from "@/lib/workspace"

// /app goes to the workspace last opened. Someone who was given none sees
// that said plainly, instead of an empty workspace.
export const Route = createFileRoute("/_app/")({
  beforeLoad: ({ context }) => {
    const workspace = pickWorkspace(context.workspaces ?? [], lastWorkspace() ?? context.lastWorkspace)
    if (workspace) throw redirect({ to: "/w/$ws", params: { ws: workspace.name }, replace: true })
  },
  component: NoWorkspaces,
})

function NoWorkspaces() {
  const { account, workspaces } = Route.useRouteContext()
  return (
    <LoneShell account={account} title="Lumbung">
      {workspaces ? (
        <Empty icon={<BoxIcon />} title="No workspace yet" description="Ask an owner to give you access to one." />
      ) : (
        <ErrorState error={new Error("Can't reach the engine.")} title="Couldn't load workspaces" />
      )}
    </LoneShell>
  )
}
