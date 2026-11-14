import type { AuditOut } from "@lumbung/client"
import { Link, createFileRoute, useNavigate } from "@tanstack/react-router"
import { CircleAlertIcon, DatabaseIcon } from "lucide-react"
import { RecentActivity } from "@/components/app/activity"
import { DatabaseTable } from "@/components/app/database-table"
import { CreateDatabaseDialog, useCreateDatabase } from "@/components/app/name-dialog"
import { RequestsChart } from "@/components/app/requests-chart"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { AwaitLatest, ChartFailed, Deferred, Stat, StatGrid, WindowSelect } from "@/components/app/stats"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { Card, CardContent, CardHeader, CardTitle, Empty, Skeleton, StatusText } from "@/components/ui/surface"
import { listAudit } from "@/fns/admin"
import { getWorkspaceStats, listDatabases } from "@/fns/databases"
import { formatBytes, number } from "@/lib/format"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { type Settled, settle } from "@/lib/settle"
import { DEFAULT_WINDOW, WINDOW_LABEL, type WorkspaceStats, errorRate, windowSearch } from "@/lib/stats"

const ACTIVITY_ROWS = 6

export const Route = createFileRoute("/_app/")({
  validateSearch: windowSearch,
  loaderDeps: ({ search }) => ({ range: search.window ?? DEFAULT_WINDOW }),
  loader: async ({ deps, context }) => {
    const databases = await listDatabases()
    const activity: Promise<Settled<AuditOut[]>> | null = can(context.viewer.role, "write")
      ? settle(listAudit({ data: {} }).then((r) => r.data.slice(0, ACTIVITY_ROWS)))
      : null
    return {
      databases,
      stats: settle<WorkspaceStats | null>(
        databases.length
          ? getWorkspaceStats({ data: { window: deps.range, databases: databases.map((d) => ({ name: d.name, branch: d.default_branch })) } })
          : Promise.resolve(null),
      ),
      activity,
    }
  },
  head: () => ({ meta: [{ title: pageTitle("Overview") }] }),
  component: OverviewPage,
  pendingComponent: () => (
    <Frame>
      <div className="flex flex-col gap-8">
        <div className="grid grid-cols-2 gap-3 md:gap-4 lg:grid-cols-4">
          {["a", "b", "c", "d"].map((k) => (
            <Skeleton key={k} className="h-[74px] rounded-lg" />
          ))}
        </div>
        <RowsSkeleton />
        <Skeleton className="h-72 rounded-lg" />
      </div>
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
        <PageTitle>Overview</PageTitle>
      </PageHeader>
      <PageBody>{children}</PageBody>
    </>
  )
}

function OverviewPage() {
  const { databases, stats, activity } = Route.useLoaderData()
  const { range } = Route.useLoaderDeps()
  const { viewer } = Route.useRouteContext()
  const navigate = useNavigate({ from: Route.fullPath })
  const canWrite = can(viewer.role, "write")

  const create = useCreateDatabase()

  const actions = canWrite ? <CreateDatabaseDialog onCreate={create} /> : undefined

  if (databases.length === 0) {
    return (
      <Frame actions={actions}>
        <Empty icon={<DatabaseIcon />} title="No databases yet">
          {canWrite && <CreateDatabaseDialog onCreate={create} />}
        </Empty>
      </Frame>
    )
  }

  const storage = databases.reduce((n, d) => n + d.size_bytes, 0)
  const workspace = stats

  return (
    <Frame actions={actions}>
      <div className="flex flex-col gap-8">
        <StatGrid>
          <Stat label="Databases">
            {number.format(databases.length)}
          </Stat>
          <Stat label="Storage">{formatBytes(storage)}</Stat>
          <Stat label="Requests" hint={range}>
            <Deferred promise={workspace}>{(s) => number.format(s?.queries ?? 0)}</Deferred>
          </Stat>
          <Stat label="Error rate" hint={range}>
            <Deferred promise={workspace}>
              {(s) => <span className={s && s.errors > 0 ? "text-destructive" : undefined}>{errorRate(s?.queries ?? 0, s?.errors ?? 0)}</span>}
            </Deferred>
          </Stat>
        </StatGrid>

        <section aria-labelledby="databases-heading" className="flex flex-col gap-3">
          <h2 id="databases-heading" className="text-[13px] font-[590]">
            Databases
          </h2>
          <AwaitLatest promise={workspace} fallback={<DatabaseTable databases={databases} usage={undefined} />}>
            {(r) => <DatabaseTable databases={databases} usage={r.ok ? (r.value?.byDb ?? {}) : null} />}
          </AwaitLatest>
        </section>

        <div className={activity ? "grid gap-4 lg:grid-cols-3" : "grid gap-4"}>
          <Card className="min-w-0 lg:col-span-2">
            <CardHeader>
              <CardTitle>Requests</CardTitle>
              <WindowSelect value={range} onChange={(w) => navigate({ search: { window: w === DEFAULT_WINDOW ? undefined : w }, replace: true })} />
            </CardHeader>
            <CardContent>
              <AwaitLatest promise={workspace} fallback={<Skeleton className="h-60" />} dim>
                {(r) =>
                  r.ok ? (
                    <div className="flex flex-col gap-3">
                      <RequestsChart series={r.value?.series ?? []} label={`Requests across all databases, ${WINDOW_LABEL[range].toLowerCase()}`} />
                      {!!r.value?.failed.length && (
                        <StatusText tone="warning" icon={<CircleAlertIcon aria-hidden="true" />}>
                          {r.value.failed.length === 1 ? "1 database didn't report" : `${number.format(r.value.failed.length)} databases didn't report`}
                        </StatusText>
                      )}
                    </div>
                  ) : (
                    <ChartFailed message={r.error} />
                  )
                }
              </AwaitLatest>
            </CardContent>
          </Card>
          {activity && (
            <Card className="flex min-w-0 flex-col">
              <CardHeader>
                <CardTitle>Recent activity</CardTitle>
                <Link to="/audit" className="group/link inline-flex items-center gap-1 rounded-sm text-xs text-muted-foreground transition-colors duration-150 hover:text-foreground hover:duration-0">
                  View all
                  <span aria-hidden="true" className="transition-transform duration-100 ease-out-quad motion-safe:group-hover/link:translate-x-0.5">
                    →
                  </span>
                </Link>
              </CardHeader>
              <CardContent className="flex-1 pt-2">
                <AwaitLatest promise={activity} fallback={<RowsSkeleton rows={4} />}>
                  {(r) => (r.ok ? <RecentActivity entries={r.value} names={new Map(databases.map((d) => [d.id, d.name]))} /> : <ChartFailed message={r.error} />)}
                </AwaitLatest>
              </CardContent>
            </Card>
          )}
        </div>
      </div>
    </Frame>
  )
}
