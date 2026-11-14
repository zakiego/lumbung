import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { Trash2Icon } from "lucide-react"
import { DbFrame, useDb } from "@/components/app/db-frame"
import { ExportForm, ImportForm, RenameForm, SettingsSection, uploadImport } from "@/components/app/db-settings"
import { MoveForm } from "@/components/app/workspace-settings"
import { TypedConfirm } from "@/components/ui/alert-dialog"
import { buttonVariants } from "@/components/ui/button"
import { toast } from "@/components/ui/toast"
import { deleteDatabase, renameDatabase } from "@/fns/databases"
import { moveDatabase } from "@/fns/workspaces"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { useWs } from "@/components/app/workspace"

export const Route = createFileRoute("/_app/w/$ws/db/$db/settings")({
  head: ({ params }) => ({ meta: [{ title: pageTitle("Settings", params.db) }] }),
  component: SettingsPage,
})

function SettingsPage() {
  const ws = useWs()
  const { database, branch, branchParam } = useDb()
  const { viewer, workspace, workspaces } = Route.useRouteContext()
  const others = (workspaces ?? []).filter((w) => w.id !== workspace.id)
  const router = useRouter()
  const navigate = useNavigate()
  const canWrite = can(viewer.role, "write")
  const canAdmin = can(viewer.role, "admin")
  const db = database.name

  return (
    <DbFrame section="settings">
      <div className="flex max-w-4xl flex-col">
        {canAdmin && (
          <SettingsSection title="Name">
            <RenameForm
              name={db}
              onRename={async (name) => {
                await renameDatabase({ data: { ws, db, name } })
                toast.success(`Renamed to ${name}`)
                await navigate({ to: "/w/$ws/db/$db/settings", params: { ws, db: name }, search: { branch: branchParam }, replace: true })
                await router.invalidate()
              }}
            />
          </SettingsSection>
        )}
        {canWrite && (
          <SettingsSection title="Import">
            <ImportForm
              branch={branch}
              onImport={async (file, mode) => {
                await uploadImport({ ws, db, branch, file, mode })
                toast.success(`Imported into ${branch}`)
                await router.invalidate()
              }}
            />
          </SettingsSection>
        )}
        <SettingsSection title="Export">
          <ExportForm ws={ws} db={db} branch={branch} />
        </SettingsSection>
        {viewer.owner && others.length > 0 && (
          <SettingsSection title="Move">
            <MoveForm
              what="database"
              name={db}
              targets={others}
              onMove={async (to) => {
                await moveDatabase({ data: { ws, db, to: to.id } })
                toast.success(`Moved ${db} to ${to.name}`)
                await navigate({ to: "/w/$ws/db/$db", params: { ws: to.name, db } })
                await router.invalidate()
              }}
            />
          </SettingsSection>
        )}
        {canAdmin && (
          <SettingsSection title="Delete database" description="It goes to Trash, where you can restore it." tone="danger">
            <TypedConfirm
              trigger={
                <>
                  <Trash2Icon aria-hidden="true" />
                  Delete database
                </>
              }
              triggerClassName={buttonVariants({ variant: "destructive", className: "self-start" })}
              title={`Delete ${db}?`}
              description="Every branch and snapshot moves to Trash with it."
              confirmText={db}
              actionLabel="Move to Trash"
              agentConfirm={`Moves the ${db} database and all its branches to Trash`}
              onConfirm={async () => {
                await deleteDatabase({ data: { ws, db } })
                toast.success(`${db} moved to Trash`)
                await navigate({ to: "/w/$ws", params: { ws } })
                await router.invalidate()
              }}
            />
          </SettingsSection>
        )}
      </div>
    </DbFrame>
  )
}
