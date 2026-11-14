import type { Point } from "@lumbung/client"
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import { number } from "@/lib/format"

const hour = new Intl.DateTimeFormat("en-US", { hour: "2-digit", minute: "2-digit", hour12: false })

type Row = { at: string; label: string; queries: number; errors: number }

export function RequestsChart({ series }: { series: Point[] }) {
  const rows: Row[] = series.map((p) => ({ at: p.at, label: hour.format(new Date(p.at)), queries: p.queries, errors: p.errors }))
  const total = rows.reduce((n, r) => n + r.queries, 0)
  return (
    <figure className="flex flex-col gap-3">
      <figcaption className="flex items-center gap-4 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <span aria-hidden="true" className="size-2 rounded-full bg-chart-1" />
          Queries
        </span>
        <span className="flex items-center gap-1.5">
          <span aria-hidden="true" className="size-2 rounded-full bg-destructive" />
          Errors
        </span>
        <span className="sr-only">{number.format(total)} queries in this window</span>
      </figcaption>
      <div aria-hidden="true" className="h-52 w-full text-muted-foreground">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={rows} margin={{ left: 0, right: 4, top: 4, bottom: 0 }}>
            <defs>
              <linearGradient id="fill-queries" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--chart-1)" stopOpacity={0.22} />
                <stop offset="100%" stopColor="var(--chart-1)" stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid vertical={false} stroke="var(--border)" />
            <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={32} fontSize={11} stroke="currentColor" />
            <YAxis tickLine={false} axisLine={false} width={40} fontSize={11} stroke="currentColor" allowDecimals={false} />
            <Tooltip cursor={{ stroke: "var(--border)" }} isAnimationActive={false} content={<ChartTip />} />
            <Area isAnimationActive={false} dataKey="queries" type="monotone" stroke="var(--chart-1)" strokeWidth={2} fill="url(#fill-queries)" />
            <Area isAnimationActive={false} dataKey="errors" type="monotone" stroke="var(--destructive)" strokeWidth={1.5} fill="transparent" />
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </figure>
  )
}

function ChartTip({ active, payload }: { active?: boolean; payload?: { payload: Row }[] }) {
  const row = payload?.[0]?.payload
  if (!active || !row) return null
  return (
    <div className="rounded-lg border bg-background px-2.5 py-2 text-xs shadow-lg shadow-black/10 dark:shadow-black/50">
      <div className="mb-1 font-[510] text-foreground">{row.label}</div>
      <div className="flex justify-between gap-6 text-muted-foreground">
        Queries <span className="text-foreground tabular-nums">{number.format(row.queries)}</span>
      </div>
      <div className="flex justify-between gap-6 text-muted-foreground">
        Errors <span className="text-foreground tabular-nums">{number.format(row.errors)}</span>
      </div>
    </div>
  )
}
