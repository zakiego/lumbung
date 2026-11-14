import { Await, createFileRoute, useRouter } from "@tanstack/react-router"
import { CircleAlertIcon } from "lucide-react"
import type { ReactNode } from "react"
import { BranchMap } from "@/components/app/branch-map"
import { Connect } from "@/components/app/connect"
import { DbFrame, useDb } from "@/components/app/db-frame"
import { NewBranchDialog } from "@/components/app/branches"
import { RequestsChart } from "@/components/app/requests-chart"
import { Card, CardContent, CardHeader, CardTitle, Skeleton, StatusText } from "@/components/ui/surface"
import { toast } from "@/components/ui/toast"
import { Tooltip } from "@/components/ui/tooltip"
import { listSnapshots } from "@/fns/admin"
import { createBranch, getConnectInfo, getSchema, getStats } from "@/fns/databases"
import { formatBytes, number, timeAgo } from "@/lib/format"
import { can } from "@/lib/roles"

type Settled<T> = { ok: true; value: T } | { ok: false; error: string }

// Each card loads on its own, so one slow or failing call never blanks the page.
function settle<T>(p: Promise<T>): Promise<Settled<T>> {
  return p.then(
    (value) => ({ ok: true as const, value }),
    (e: unknown) => ({ ok: false as const, error: e instanceof Error ? e.message : String(e) }),
  )
}

export const Route = createFileRoute("/_app/db/$db/")({
  loaderDeps: ({ search }) => ({ branch: search.branch }),
  loader: async ({ params, deps, parentMatchPromise }) => {
    const parent = await parentMatchPromise
    const database = parent.loaderData?.database
    const branches = parent.loaderData?.branches ?? []
    const branch = deps.branch && branches.some((b) => b.name === deps.branch) ? deps.branch : (database?.default_branch ?? "main")
    const ref = { db: params.db, branch }
    return {
      connect: await getConnectInfo(),
      schema: settle(getSchema({ data: ref })),
      stats: settle(getStats({ data: { ...ref, window: "24h" } })),
      snapshots: settle(listSnapshots({ data: ref })),
    }
  },
  component: OverviewPage,
})

function OverviewPage() {
  const { database, branches, branch, branchParam } = useDb()
  const { connect, schema, stats, snapshots } = Route.useLoaderData()
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

        <dl className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <Stat label="Size">{formatBytes(current?.size_bytes ?? database.size_bytes)}</Stat>
          <Stat label="Tables">
            <Deferred promise={schema}>{(s) => number.format(s.tables.filter((t) => !t.name.startsWith("sqlite_")).length)}</Deferred>
          </Stat>
          <Stat label="Requests, 24h">
            <Deferred promise={stats}>{(s) => number.format(s.queries)}</Deferred>
          </Stat>
          <Stat label="Last snapshot">
            <Deferred promise={snapshots}>
              {(s) => {
                const latest = s.data.reduce<string | undefined>((max, snap) => (!max || snap.created_at > max ? snap.created_at : max), undefined)
                return latest ? <span suppressHydrationWarning>{timeAgo(latest)}</span> : <span className="text-muted-foreground">None</span>
              }}
            </Deferred>
          </Stat>
        </dl>

        <div className="grid gap-4 lg:grid-cols-2">
          <Card className="min-w-0">
            <CardHeader>
              <CardTitle className="truncate">
                Requests on <span className="font-mono">{branch}</span>
              </CardTitle>
              <span className="text-xs text-muted-foreground">Last 24 hours</span>
            </CardHeader>
            <CardContent>
              <Await promise={stats} fallback={<Skeleton className="h-60" />}>
                {(r) => (r.ok ? <RequestsChart series={r.value.series} /> : <Failed message={r.error} />)}
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

function Stat({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-lg border bg-card px-4 py-3">
      <dt className="text-[13px] text-muted-foreground">{label}</dt>
      <dd className="truncate text-xl font-[590] tracking-[-0.012em] tabular-nums">{children}</dd>
    </div>
  )
}

function Deferred<T>({ promise, children }: { promise: Promise<Settled<T>>; children: (value: T) => ReactNode }) {
  return (
    <Await promise={promise} fallback={<Skeleton className="mt-1 h-6 w-16" />}>
      {(r) =>
        r.ok ? (
          children(r.value)
        ) : (
          <Tooltip content={r.error}>
            <span tabIndex={0} className="inline-flex rounded-sm">
              <StatusText icon={<CircleAlertIcon aria-hidden="true" />}>Couldn't load</StatusText>
            </span>
          </Tooltip>
        )
      }
    </Await>
  )
}

function Failed({ message }: { message: string }) {
  return (
    <div className="flex h-60 items-center justify-center">
      <StatusText tone="destructive" icon={<CircleAlertIcon aria-hidden="true" />}>
        {message}
      </StatusText>
    </div>
  )
}
