import type { DatabaseOut } from "@lumbung/client"
import { Link, createFileRoute, useRouter } from "@tanstack/react-router"
import { DatabaseIcon, Trash2Icon } from "lucide-react"
import { CreateDatabaseDialog } from "@/components/app/name-dialog"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { buttonVariants } from "@/components/ui/button"
import { Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { toast } from "@/components/ui/toast"
import { createDatabase, listDatabases } from "@/fns/databases"
import { formatBytes, number, timeAgo } from "@/lib/format"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

export const Route = createFileRoute("/_app/")({
  loader: () => listDatabases(),
  head: () => ({ meta: [{ title: pageTitle("Databases") }] }),
  component: DatabasesPage,
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton />
    </Frame>
  ),
  errorComponent: ({ error }) => (
    <Frame>
      <ErrorState error={error} title="Couldn't load databases" />
    </Frame>
  ),
})

function Frame({ children, actions }: { children: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <>
      <PageHeader actions={actions}>
        <PageTitle>Databases</PageTitle>
      </PageHeader>
      <PageBody>{children}</PageBody>
    </>
  )
}

function DatabasesPage() {
  const databases = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const router = useRouter()
  const canWrite = can(viewer.role, "write")

  async function create(name: string) {
    // A failure shows under the dialog's field, so only success gets a toast.
    await createDatabase({ data: { name } })
    toast.success(`Created ${name}`)
    await router.invalidate()
    await router.navigate({ to: "/db/$db", params: { db: name } })
  }

  const actions = (
    <>
      <Link to="/trash" className={buttonVariants({ variant: "ghost" })}>
        <Trash2Icon aria-hidden="true" />
        Trash
      </Link>
      {canWrite && <CreateDatabaseDialog onCreate={create} />}
    </>
  )

  return (
    <Frame actions={actions}>
      {databases.length === 0 ? (
        <Empty icon={<DatabaseIcon />} title="No databases yet" />
      ) : (
        <DatabaseTable databases={databases} />
      )}
    </Frame>
  )
}

function DatabaseTable({ databases }: { databases: DatabaseOut[] }) {
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Name</TableHead>
          <TableHead className="text-right">Size</TableHead>
          <TableHead className="text-right">Branches</TableHead>
          <TableHead className="text-right">Last activity</TableHead>
        </tr>
      </thead>
      <tbody>
        {databases.map((d) => (
          <TableRow key={d.id} className="relative">
            <TableCell className="max-w-0 w-1/2">
              <Link to="/db/$db" params={{ db: d.name }} className="flex min-w-0 items-center gap-2 font-mono font-[510] after:absolute after:inset-0">
                <DatabaseIcon aria-hidden="true" className="size-4 shrink-0 text-faint" />
                <span className="truncate">{d.name}</span>
              </Link>
            </TableCell>
            <TableCell className="text-right text-muted-foreground tabular-nums">{formatBytes(d.size_bytes)}</TableCell>
            <TableCell className="text-right text-muted-foreground tabular-nums">{number.format(d.branch_count)}</TableCell>
            <TableCell className="text-right whitespace-nowrap text-muted-foreground">
              <span suppressHydrationWarning>{d.last_activity_at ? timeAgo(d.last_activity_at) : "Never"}</span>
            </TableCell>
          </TableRow>
        ))}
      </tbody>
    </Table>
  )
}
