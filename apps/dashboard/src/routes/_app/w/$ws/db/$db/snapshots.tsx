import type { SnapshotOut } from "@lumbung/client"
import { createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { useEffect, useState } from "react"
import { DbFrame, useDb } from "@/components/app/db-frame"
import { NewSnapshotDialog, NoSnapshots, ScheduleCard, SnapshotTable } from "@/components/app/snapshots"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { Button } from "@/components/ui/button"
import { toast, withToast } from "@/components/ui/toast"
import { createSnapshot, deleteSchedule, deleteSnapshot, listSnapshots, restoreSnapshot, setSchedule } from "@/fns/admin"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { useWs } from "@/components/app/workspace"

export const Route = createFileRoute("/_app/w/$ws/db/$db/snapshots")({
  loaderDeps: ({ search }) => ({ branch: search.branch }),
  loader: async ({ params, deps, parentMatchPromise }) => {
    const parent = await parentMatchPromise
    const branches = parent.loaderData?.branches ?? []
    const fallback = parent.loaderData?.database.default_branch ?? "main"
    const branch = deps.branch && branches.some((b) => b.name === deps.branch) ? deps.branch : fallback
    return listSnapshots({ data: { ws: params.ws, db: params.db, branch } })
  },
  head: ({ params }) => ({ meta: [{ title: pageTitle("Snapshots", params.db) }] }),
  component: SnapshotsPage,
  pendingComponent: () => (
    <DbFrame section="snapshots">
      <RowsSkeleton />
    </DbFrame>
  ),
  errorComponent: ({ error }) => (
    <DbFrame section="snapshots">
      <ErrorState error={error} title="Couldn't load snapshots" />
    </DbFrame>
  ),
})

function SnapshotsPage() {
  const ws = useWs()
  const { database, branch } = useDb()
  const first = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const router = useRouter()
  const navigate = useNavigate()
  const canWrite = can(viewer.role, "write")
  const db = database.name

  const [more, setMore] = useState<SnapshotOut[]>([])
  const [cursor, setCursor] = useState(first.nextCursor)
  useEffect(() => {
    setMore([])
    setCursor(first.nextCursor)
  }, [first])

  async function loadMore() {
    try {
      const page = await listSnapshots({ data: { ws, db, branch, cursor } })
      setMore((prev) => [...prev, ...page.data])
      setCursor(page.nextCursor)
    } catch (e) {
      toast.error({ title: "Couldn't load more", description: e instanceof Error ? e.message : String(e) })
    }
  }

  const snapshots = [...first.data, ...more]

  return (
    <DbFrame
      section="snapshots"
      actions={
        canWrite && (
          <NewSnapshotDialog
            branch={branch}
            onCreate={async (label) => {
              await createSnapshot({ data: { ws, db, branch, label: label || undefined } })
              toast.success("Snapshot taken")
              await router.invalidate()
            }}
          />
        )
      }
    >
      <div className="flex flex-col gap-6">
        <ScheduleCard
          schedule={first.schedule}
          canWrite={canWrite}
          onSave={async (s) => {
            await setSchedule({ data: { ws, db, branch, ...s } })
            toast.success("Schedule saved")
            await router.invalidate()
          }}
          onRemove={async () => {
            await withToast(deleteSchedule({ data: { ws, db, branch } }), { loading: "Removing…", success: "Schedule removed", error: "Couldn't remove it" })
            await router.invalidate()
          }}
        />
        {snapshots.length === 0 ? (
          <NoSnapshots />
        ) : (
          <SnapshotTable
            snapshots={snapshots}
            canWrite={canWrite}
            onRestore={async (s, name) => {
              await restoreSnapshot({ data: { ws, db, snapshot: s.id, name } })
              toast.success(`Restored to ${name}`)
              await router.invalidate()
              await navigate({ to: "/w/$ws/db/$db/branches", params: { ws, db }, search: { branch: name } })
            }}
            onDelete={async (s) => {
              await deleteSnapshot({ data: { ws, db, snapshot: s.id } })
              toast.success("Snapshot deleted")
              await router.invalidate()
            }}
          />
        )}
        {cursor && (
          <Button variant="secondary" className="self-center" onClick={loadMore}>
            Load more
          </Button>
        )}
      </div>
    </DbFrame>
  )
}
