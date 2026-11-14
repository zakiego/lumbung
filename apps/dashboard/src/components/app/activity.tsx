import type { AuditOut } from "@lumbung/client"
import { CircleAlertIcon, KeyRoundIcon, ScrollTextIcon } from "lucide-react"
import { Empty } from "@/components/ui/surface"
import { timeAgo } from "@/lib/format"
import { actorOf } from "./audit"

// Targets are ids like "db_x/br_y". Show the database's name when it's known;
// the full target stays in the title.
export function targetLabel(target: string, names: Map<string, string>): string {
  return names.get(target.split("/")[0] ?? "") ?? target
}

export function RecentActivity({ entries, names = new Map() }: { entries: AuditOut[]; names?: Map<string, string> }) {
  if (entries.length === 0) return <Empty icon={<ScrollTextIcon />} title="Nothing logged yet" className="h-full border-0 py-10" />
  return (
    <ol className="flex flex-col">
      {entries.map((e) => {
        const who = actorOf(e)
        const target = e.target ? targetLabel(e.target, names) : ""
        return (
          <li key={e.id} className="flex min-w-0 items-center gap-3 border-b py-2.5 last:border-b-0">
            <div className="grid min-w-0 flex-1 gap-0.5">
              <span className="flex min-w-0 items-center gap-1.5 text-[13px]">
                {e.result !== "ok" && (
                  <>
                    <CircleAlertIcon aria-hidden="true" className="size-3.5 shrink-0 text-destructive" />
                    <span className="sr-only">Failed: </span>
                  </>
                )}
                <span className="max-w-[70%] shrink-0 truncate font-mono text-[12px]" title={e.action}>
                  {e.action}
                </span>
                {target && (
                  <span className="min-w-0 truncate font-mono text-[12px] text-muted-foreground" title={e.target}>
                    {target}
                  </span>
                )}
              </span>
              <span className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground" title={who.text}>
                {who.token && (
                  <>
                    <KeyRoundIcon aria-hidden="true" className="size-3 shrink-0 text-faint" />
                    <span className="sr-only">Token </span>
                  </>
                )}
                <span className="truncate">{who.text}</span>
              </span>
            </div>
            <time dateTime={e.at} className="shrink-0 text-xs whitespace-nowrap text-muted-foreground" suppressHydrationWarning>
              {timeAgo(e.at)}
            </time>
          </li>
        )
      })}
    </ol>
  )
}
