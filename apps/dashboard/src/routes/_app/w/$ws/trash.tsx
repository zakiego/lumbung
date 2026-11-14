import type { DatabaseOut } from "@lumbung/client"
import { Link, createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { ArchiveRestoreIcon, Trash2Icon } from "lucide-react"
import { PageBody, PageHeader } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { Button } from "@/components/ui/button"
import { Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { withToast } from "@/components/ui/toast"
import { TruncatedTip } from "@/components/ui/tooltip"
import { listTrash, restoreDatabase } from "@/fns/databases"
import { formatBytes, formatDate } from "@/lib/format"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { useWs } from "@/components/app/workspace"

export const Route = createFileRoute("/_app/w/$ws/trash")({
  beforeLoad: ({ context, params }) => {
    if (!can(context.viewer.role, "admin")) throw redirect({ to: "/w/$ws", params })
  },
  loader: ({ params }) => listTrash({ data: { ws: params.ws } }),
  head: ({ params }) => ({ meta: [{ title: pageTitle("Trash", params.ws) }] }),
  component: TrashPage,
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton rows={3} />
    </Frame>
  ),
  errorComponent: ({ error }) => (
    <Frame>
      <ErrorState error={error} title="Couldn't load the trash" />
    </Frame>
  ),
})

function Frame({ children }: { children: React.ReactNode }) {
  const ws = useWs()
  return (
    <>
      <PageHeader>
        <nav aria-label="Breadcrumb" className="min-w-0">
          <ol className="flex items-center gap-1.5 text-[13px]">
            <li>
              <Link to="/w/$ws" params={{ ws }} className="text-muted-foreground transition-colors duration-100 hover:text-foreground">
                Databases
              </Link>
            </li>
            <li aria-hidden="true" className="text-faint">
              /
            </li>
            <li>
              <h1 aria-current="page" className="font-[590]">
                Trash
              </h1>
            </li>
          </ol>
        </nav>
      </PageHeader>
      <PageBody>{children}</PageBody>
    </>
  )
}

function TrashPage() {
  const ws = useWs()
  const databases = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const router = useRouter()
  const canWrite = can(viewer.role, "admin")

  async function restore(d: DatabaseOut) {
    await withToast(restoreDatabase({ data: { ws, db: d.id } }), { loading: `Restoring ${d.name}…`, success: `Restored ${d.name}`, error: "Couldn't restore it" })
    await router.invalidate()
  }

  return (
    <Frame>
      {databases.length === 0 ? (
        <Empty icon={<Trash2Icon />} title="Trash is empty" />
      ) : (
        <Table>
          <thead>
            <tr>
              <TableHead>Name</TableHead>
              <TableHead className="text-right">Size</TableHead>
              <TableHead className="text-right">Deleted</TableHead>
              <TableHead className="text-right">Removed for good</TableHead>
              {canWrite && (
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </tr>
          </thead>
          <tbody>
            {databases.map((d) => (
              <TableRow key={d.id}>
                <TableCell className="max-w-0 w-1/3">
                  <TruncatedTip text={d.name}>
                    <span tabIndex={0} className="block truncate rounded-sm font-mono font-[510]">
                      {d.name}
                    </span>
                  </TruncatedTip>
                </TableCell>
                <TableCell className="text-right text-muted-foreground tabular-nums">{formatBytes(d.size_bytes)}</TableCell>
                <TableCell className="text-right whitespace-nowrap text-muted-foreground">{d.deleted_at ? formatDate(d.deleted_at) : "-"}</TableCell>
                <TableCell className="text-right whitespace-nowrap text-muted-foreground">{d.purge_after ? formatDate(d.purge_after) : "-"}</TableCell>
                {canWrite && (
                  <TableCell className="w-px text-right">
                    <Button variant="secondary" size="sm" aria-label={`Restore ${d.name}`} onClick={() => restore(d).catch(() => {})}>
                      <ArchiveRestoreIcon aria-hidden="true" />
                      Restore
                    </Button>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </tbody>
        </Table>
      )}
    </Frame>
  )
}
