// Connect first: the overview's main job is getting code talking to the
// database, so the snippet leads and the numbers sit in a side rail.
import { KeyRoundIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Shell } from "./shell"
import { branches, database, formatBytes, number, snapshots, totals } from "./data"
import { Connect, RequestsChart } from "./parts"

export function ConnectFirst() {
  return (
    <Shell>
      {(branch) => (
        <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_288px]">
          <div className="flex min-w-0 flex-col gap-8">
            <div className="flex flex-col gap-2">
              <h1 className="text-2xl font-semibold tracking-tight text-balance">
                Connect to {database.name}
                <span className="text-muted-foreground"> on {branch}</span>
              </h1>
            </div>

            <ol className="flex flex-col gap-8">
              <li className="flex gap-4">
                <span className="flex size-6 shrink-0 items-center justify-center rounded-full border font-mono text-xs text-muted-foreground">
                  1
                </span>
                <div className="flex flex-1 flex-col gap-3">
                  <p className="text-[15px] font-medium">Create a token</p>
                  <div>
                    <Button size="sm" variant="outline">
                      <KeyRoundIcon data-icon="inline-start" />
                      New token for {database.name}
                    </Button>
                  </div>
                </div>
              </li>
              <li className="flex gap-4">
                <span className="flex size-6 shrink-0 items-center justify-center rounded-full border font-mono text-xs text-muted-foreground">
                  2
                </span>
                <div className="flex min-w-0 flex-1 flex-col gap-3">
                  <p className="text-[15px] font-medium">Run a query</p>
                  <Connect blockClassName="min-h-56" />
                </div>
              </li>
            </ol>
          </div>

          <aside className="flex flex-col gap-4">
            <Card size="sm">
              <CardHeader>
                <CardTitle className="text-[13px] font-normal text-muted-foreground">Requests, 24h</CardTitle>
              </CardHeader>
              <CardContent className="flex flex-col gap-3">
                <p className="text-2xl font-semibold tracking-tight tabular-nums">
                  {number.format(totals.reads24h + totals.writes24h)}
                </p>
                <RequestsChart compact />
              </CardContent>
            </Card>
            <Card size="sm">
              <CardContent>
                <dl className="grid grid-cols-2 gap-x-4 gap-y-5 text-[13px]">
                  <Fact label="Size" value={formatBytes(database.sizeBytes)} />
                  <Fact label="Tables" value={String(database.tables)} />
                  <Fact label="Branches" value={String(branches.length)} />
                  <Fact label="Snapshots" value={String(snapshots.count)} />
                  <Fact label="p95" value={`${totals.p95ms} ms`} />
                  <Fact label="Errors" value={String(totals.errors24h)} />
                </dl>
              </CardContent>
            </Card>
          </aside>
        </div>
      )}
    </Shell>
  )
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-medium tabular-nums">{value}</dd>
    </div>
  )
}
