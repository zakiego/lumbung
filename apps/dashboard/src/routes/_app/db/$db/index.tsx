import { Await, createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { BranchMap } from "@/components/app/branch-map"
import { Connect } from "@/components/app/connect"
import { DbFrame, useDb } from "@/components/app/db-frame"
import { NewBranchDialog } from "@/components/app/branches"
import { RequestsChart } from "@/components/app/requests-chart"
import { ChartFailed, Deferred, Stat, StatGrid, WindowSelect } from "@/components/app/stats"
import { Card, CardContent, CardHeader, CardTitle, Skeleton } from "@/components/ui/surface"
import { toast } from "@/components/ui/toast"
import { listSnapshots } from "@/fns/admin"
import { createBranch, getConnectInfo, getSchema, getStats } from "@/fns/databases"
import { formatBytes, number, timeAgo } from "@/lib/format"
import { can } from "@/lib/roles"
import { settle } from "@/lib/settle"
import { DEFAULT_WINDOW, WINDOW_LABEL, errorRate, windowSearch } from "@/lib/stats"

export const Route = createFileRoute("/_app/db/$db/")({
  validateSearch: windowSearch,
  loaderDeps: ({ search }) => ({ branch: search.branch, range: search.window ?? DEFAULT_WINDOW }),
  loader: async ({ params, deps, parentMatchPromise }) => {
    const parent = await parentMatchPromise
    const database = parent.loaderData?.database
    const branches = parent.loaderData?.branches ?? []
    const branch = deps.branch && branches.some((b) => b.name === deps.branch) ? deps.branch : (database?.default_branch ?? "main")
    const ref = { db: params.db, branch }
    return {
      connect: await getConnectInfo(),
      schema: settle(getSchema({ data: ref })),
      stats: settle(getStats({ data: { ...ref, window: deps.range } })),
      snapshots: settle(listSnapshots({ data: ref })),
    }
  },
  component: OverviewPage,
})

function OverviewPage() {
  const { database, branches, branch, branchParam } = useDb()
  const { connect, schema, stats, snapshots } = Route.useLoaderData()
  const { range } = Route.useLoaderDeps()
  const navigate = useNavigate({ from: Route.fullPath })
  const { viewer } = Route.useRouteContext()
  const router = useRouter()
  const current = branches.find((b) => b.name === branch)

  async function newBranch(b: { name: string; from: string; expiresInDays?: number }) {
    await createBranch({ data: { db: database.name, ...b } })
    toast.success(`Created ${b.name}`)
    await router.invalidate()
    await router.navigate({ to: "/db/$db", params: { db: database.name }, search: { branch: b.name } })
  }

  return (
    <DbFrame section="overview" actions={can(viewer.role, "write") && <NewBranchDialog branches={branches} from={branch} onCreate={newBranch} />}>
      <div className="flex flex-col gap-8">
        <BranchMap db={database.name} branches={branches} current={branch} defaultBranch={database.default_branch} />

        <StatGrid>
          <Stat label="Size">{formatBytes(current?.size_bytes ?? database.size_bytes)}</Stat>
          <Stat label="Tables">
            <Deferred promise={schema}>{(s) => number.format(s.tables.filter((t) => !t.name.startsWith("sqlite_")).length)}</Deferred>
          </Stat>
          <Stat label="Requests" hint={range}>
            <Deferred promise={stats}>
              {(s) => (
                <>
                  {number.format(s.queries)}
                  {s.errors > 0 && <span className="ml-2 text-[13px] font-[510] text-destructive">{errorRate(s.queries, s.errors)} errors</span>}
                </>
              )}
            </Deferred>
          </Stat>
          <Stat label="Last snapshot">
            <Deferred promise={snapshots}>
              {(s) => {
                const latest = s.data.reduce<string | undefined>((max, snap) => (!max || snap.created_at > max ? snap.created_at : max), undefined)
                return latest ? <span suppressHydrationWarning>{timeAgo(latest)}</span> : <span className="text-muted-foreground">None</span>
              }}
            </Deferred>
          </Stat>
        </StatGrid>

        <div className="grid gap-4 lg:grid-cols-2">
          <Card className="min-w-0">
            <CardHeader>
              <CardTitle className="truncate">
                Requests on <span className="font-mono">{branch}</span>
              </CardTitle>
              <WindowSelect value={range} onChange={(w) => navigate({ search: (prev) => ({ ...prev, window: w === DEFAULT_WINDOW ? undefined : w }), replace: true })} />
            </CardHeader>
            <CardContent>
              <Await promise={stats} fallback={<Skeleton className="h-60" />}>
                {(r) => (r.ok ? <RequestsChart series={r.value.series} label={`Requests on ${branch}, ${WINDOW_LABEL[range].toLowerCase()}`} /> : <ChartFailed message={r.error} />)}
              </Await>
            </CardContent>
          </Card>
          <Card className="min-w-0">
            <CardHeader>
              <CardTitle>Connect</CardTitle>
            </CardHeader>
            <CardContent className="pt-2">
              <Connect url={connect.engineUrl} db={database.name} branch={branchParam} />
            </CardContent>
          </Card>
        </div>
      </div>
    </DbFrame>
  )
}
