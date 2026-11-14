import type { DatabaseOut } from "@lumbung/client"
import { Link } from "@tanstack/react-router"
import { DatabaseIcon } from "lucide-react"
import { Skeleton, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { TruncatedTip } from "@/components/ui/tooltip"
import { formatBytes, number, timeAgo } from "@/lib/format"
import type { DbUsage } from "@/lib/stats"
import { useWs } from "./workspace"

// usage: undefined while loading, null when stats couldn't load at all.
export function DatabaseTable({ databases, usage }: { databases: DatabaseOut[]; usage: Record<string, DbUsage> | null | undefined }) {
  const ws = useWs()
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Name</TableHead>
          <TableHead className="text-right">Requests</TableHead>
          <TableHead className="hidden text-right sm:table-cell">Size</TableHead>
          <TableHead className="hidden text-right md:table-cell">Branches</TableHead>
          <TableHead className="text-right">Last activity</TableHead>
        </tr>
      </thead>
      <tbody>
        {databases.map((d) => (
          <TableRow key={d.id} className="relative">
            <TableCell className="w-2/5 max-w-0">
              <TruncatedTip text={d.name}>
                <Link to="/w/$ws/db/$db" params={{ ws, db: d.name }} className="flex min-w-0 items-center gap-2 font-mono font-[510] after:absolute after:inset-0">
                  <DatabaseIcon aria-hidden="true" className="size-4 shrink-0 text-faint" />
                  <span data-truncate className="truncate">
                    {d.name}
                  </span>
                </Link>
              </TruncatedTip>
            </TableCell>
            <TableCell className="text-right whitespace-nowrap text-muted-foreground tabular-nums">
              <Requests usage={usage === undefined ? undefined : (usage?.[d.name] ?? null)} />
            </TableCell>
            <TableCell className="hidden text-right text-muted-foreground tabular-nums sm:table-cell">{formatBytes(d.size_bytes)}</TableCell>
            <TableCell className="hidden text-right text-muted-foreground tabular-nums md:table-cell">{number.format(d.branch_count)}</TableCell>
            <TableCell className="text-right whitespace-nowrap text-muted-foreground">
              <span suppressHydrationWarning>{d.last_activity_at ? timeAgo(d.last_activity_at) : "Never"}</span>
            </TableCell>
          </TableRow>
        ))}
      </tbody>
    </Table>
  )
}

function Requests({ usage }: { usage: DbUsage | null | undefined }) {
  if (usage === undefined) return <Skeleton className="ml-auto h-4 w-10" />
  if (usage === null) return <span>Couldn't load</span>
  return (
    <span>
      {number.format(usage.queries)}
      {usage.errors > 0 && (
        <span className="ml-2 text-destructive">
          <span className="sr-only">, </span>
          {number.format(usage.errors)} {usage.errors === 1 ? "error" : "errors"}
        </span>
      )}
    </span>
  )
}
