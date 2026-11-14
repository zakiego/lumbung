import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { Trash2Icon } from "lucide-react"
import { DbFrame, useDb } from "@/components/app/db-frame"
import { ExportForm, ImportForm, RenameForm, SettingsSection, uploadImport } from "@/components/app/db-settings"
import { TypedConfirm } from "@/components/ui/alert-dialog"
import { buttonVariants } from "@/components/ui/button"
import { toast } from "@/components/ui/toast"
import { deleteDatabase, renameDatabase } from "@/fns/databases"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

export const Route = createFileRoute("/_app/db/$db/settings")({
  head: ({ params }) => ({ meta: [{ title: pageTitle("Settings", params.db) }] }),
  component: SettingsPage,
})

function SettingsPage() {
  const { database, branch, branchParam } = useDb()
  const { viewer } = Route.useRouteContext()
  const router = useRouter()
  const navigate = useNavigate()
  const canWrite = can(viewer.role, "write")
  const db = database.name

  return (
    <DbFrame section="settings">
      <div className="flex max-w-4xl flex-col">
        {canWrite && (
          <SettingsSection title="Name">
            <RenameForm
              name={db}
              onRename={async (name) => {
                await renameDatabase({ data: { db, name } })
                toast.success(`Renamed to ${name}`)
                await navigate({ to: "/db/$db/settings", params: { db: name }, search: { branch: branchParam }, replace: true })
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
                await uploadImport({ db, branch, file, mode })
                toast.success(`Imported into ${branch}`)
                await router.invalidate()
              }}
            />
          </SettingsSection>
        )}
        <SettingsSection title="Export">
          <ExportForm db={db} branch={branch} />
        </SettingsSection>
        {canWrite && (
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
                await deleteDatabase({ data: { db } })
                toast.success(`${db} moved to Trash`)
                await navigate({ to: "/" })
                await router.invalidate()
              }}
            />
          </SettingsSection>
        )}
      </div>
    </DbFrame>
  )
}
