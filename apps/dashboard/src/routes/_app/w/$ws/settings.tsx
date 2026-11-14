import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { Trash2Icon } from "lucide-react"
import type { ReactNode } from "react"
import { RenameForm, SettingsSection } from "@/components/app/db-settings"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { LimitsForm, UsageList } from "@/components/app/workspace-settings"
import { TypedConfirm } from "@/components/ui/alert-dialog"
import { buttonVariants } from "@/components/ui/button"
import { toast } from "@/components/ui/toast"
import { deleteWorkspace, getWorkspace, updateWorkspace } from "@/fns/workspaces"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { forgetSession } from "@/lib/session-cache"

export const Route = createFileRoute("/_app/w/$ws/settings")({
  beforeLoad: ({ context, params }) => {
    if (!can(context.viewer.role, "admin")) throw redirect({ to: "/w/$ws", params })
  },
  loader: ({ params }) => getWorkspace({ data: { ws: params.ws } }),
  head: ({ params }) => ({ meta: [{ title: pageTitle("Settings", params.ws) }] }),
  component: WorkspaceSettingsPage,
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton rows={5} />
    </Frame>
  ),
  errorComponent: ({ error }) => (
    <Frame>
      <ErrorState error={error} title="Couldn't load this workspace" />
    </Frame>
  ),
})

function Frame({ children }: { children: ReactNode }) {
  return (
    <>
      <PageHeader>
        <PageTitle>Workspace settings</PageTitle>
      </PageHeader>
      <PageBody>
        <div className="max-w-4xl">{children}</div>
      </PageBody>
    </>
  )
}

function WorkspaceSettingsPage() {
  const { workspace } = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const router = useRouter()
  const ws = workspace.name
  const limits = workspace.limits ?? { max_databases: 0, max_buckets: 0, max_database_bytes: 0, max_object_bytes: 0 }

  // The session carries the workspace list, so a change to it asks for the session again.
  async function refresh() {
    forgetSession()
    await router.invalidate()
  }

  return (
    <Frame>
      {viewer.owner && (
        <SettingsSection title="Name">
          <RenameForm
            name={ws}
            label="Workspace name"
            onRename={async (name) => {
              await updateWorkspace({ data: { ws: workspace.id, name } })
              toast.success(`Renamed to ${name}`)
              forgetSession()
              await router.navigate({ to: "/w/$ws/settings", params: { ws: name }, replace: true })
              await router.invalidate()
            }}
          />
        </SettingsSection>
      )}
      {workspace.usage && (
        <SettingsSection title="Usage">
          <UsageList usage={workspace.usage} limits={limits} />
        </SettingsSection>
      )}
      {viewer.owner && (
        <SettingsSection title="Limits" description="Leave a field empty for no limit.">
          <LimitsForm
            key={JSON.stringify(limits)}
            limits={limits}
            onSave={async (patch) => {
              await updateWorkspace({ data: { ws: workspace.id, ...patch } })
              toast.success("Limits saved")
              await refresh()
            }}
          />
        </SettingsSection>
      )}
      {viewer.owner && !workspace.default && (
        <SettingsSection title="Delete workspace" description="You can restore it from Instance settings." tone="danger">
          <TypedConfirm
            trigger={
              <>
                <Trash2Icon aria-hidden="true" />
                Delete workspace
              </>
            }
            triggerClassName={buttonVariants({ variant: "destructive", className: "self-start" })}
            title={`Delete ${ws}?`}
            description="Its databases, buckets and tokens stop working now."
            confirmText={ws}
            actionLabel="Delete"
            agentConfirm={`Deletes the workspace ${ws} with everything in it. An owner can restore it from Instance settings`}
            onConfirm={async () => {
              await deleteWorkspace({ data: { ws: workspace.id } })
              toast.success(`Deleted ${ws}`)
              forgetSession()
              await router.navigate({ to: "/" })
              await router.invalidate()
            }}
          />
        </SettingsSection>
      )}
    </Frame>
  )
}
