// Ledger: hairlines over cards, numbers first, everything on one page width.
import { GitBranchIcon, LockIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { Shell } from "./shell"
import { branches, database, formatBytes, number, totals } from "./data"
import { Connect, Legend, RequestsChart } from "./parts"

const stats = [
  { label: "Size", value: formatBytes(database.sizeBytes) },
  { label: "Tables", value: String(database.tables) },
  { label: "Requests, 24h", value: number.format(totals.reads24h + totals.writes24h) },
  { label: "p95 latency", value: `${totals.p95ms} ms` },
]

export function Ledger() {
  return (
    <Shell actions={<Button size="sm">Open SQL</Button>}>
      {(branch) => (
        <div className="flex flex-col gap-12">
          <dl className="grid grid-cols-2 gap-y-6 md:grid-cols-4 md:divide-x">
            {stats.map((s) => (
              <div key={s.label} className="flex flex-col gap-1 md:px-6 md:first:pl-0">
                <dt className="text-[13px] text-muted-foreground">{s.label}</dt>
                <dd className="text-2xl font-semibold tracking-tight tabular-nums">{s.value}</dd>
              </div>
            ))}
          </dl>

          <section className="flex flex-col gap-4">
            <div className="flex items-center justify-between">
              <h2 className="text-[15px] font-semibold">Requests on {branch}</h2>
              <Legend />
            </div>
            <RequestsChart />
          </section>

          <Separator />

          <div className="grid gap-12 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
            <section className="flex min-w-0 flex-col gap-3">
              <h2 className="text-[15px] font-semibold">Connect</h2>
              <Connect />
            </section>
            <section className="flex flex-col gap-3">
              <div className="flex items-center justify-between">
                <h2 className="text-[15px] font-semibold">Branches</h2>
                <Button variant="ghost" size="sm" className="text-muted-foreground">
                  View all →
                </Button>
              </div>
              <ul className="divide-y border-y">
                {branches.map((b) => (
                  <li key={b.name} className="flex h-12 items-center gap-3 text-[13px]">
                    <GitBranchIcon className="size-4 shrink-0 text-faint" />
                    <span className="min-w-0 truncate font-mono">{b.name}</span>
                    {b.protected && <LockIcon className="size-3.5 shrink-0 text-faint" aria-label="Protected" />}
                    <span className="ml-auto shrink-0 text-muted-foreground tabular-nums">
                      {b.expiresInDays ? `expires in ${b.expiresInDays}d` : formatBytes(b.sizeBytes)}
                    </span>
                  </li>
                ))}
              </ul>
            </section>
          </div>
        </div>
      )}
    </Shell>
  )
}
