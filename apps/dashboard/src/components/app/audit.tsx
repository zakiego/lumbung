import type { AuditOut } from "@lumbung/client"
import { CircleAlertIcon, CircleCheckIcon, KeyRoundIcon, ScrollTextIcon } from "lucide-react"
import { Empty, StatusText, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { timeAgo } from "@/lib/format"

const time = new Intl.DateTimeFormat("en-US", { dateStyle: "medium", timeStyle: "medium" })

function fullTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : time.format(d)
}

// The dashboard tags its calls "dashboard:<email>"; show the email.
export function actorOf(e: AuditOut): { text: string; token: boolean } {
  if (e.actor) return { text: e.actor.replace(/^dashboard:/, ""), token: false }
  return { text: e.token_id || "unknown", token: true }
}

export function AuditTable({ entries }: { entries: AuditOut[] }) {
  if (entries.length === 0) return <Empty icon={<ScrollTextIcon />} title="Nothing logged yet" />
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Who</TableHead>
          <TableHead>Action</TableHead>
          <TableHead className="hidden md:table-cell">Target</TableHead>
          <TableHead>Result</TableHead>
          <TableHead className="text-right">When</TableHead>
        </tr>
      </thead>
      <tbody>
        {entries.map((e) => {
          const who = actorOf(e)
          return (
            <TableRow key={e.id}>
              <TableCell className="w-1/4 max-w-0">
                <span className="flex min-w-0 items-center gap-1.5" title={who.text}>
                  {who.token && (
                    <>
                      <KeyRoundIcon aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
                      <span className="sr-only">Token </span>
                    </>
                  )}
                  <span className={who.token ? "truncate font-mono text-muted-foreground" : "truncate"}>{who.text}</span>
                </span>
              </TableCell>
              <TableCell className="w-1/5 max-w-0">
                <span className="block truncate font-mono text-[12px]" title={e.action}>
                  {e.action}
                </span>
              </TableCell>
              <TableCell className="hidden w-1/4 max-w-0 md:table-cell">
                <span className="block truncate font-mono text-[12px] text-muted-foreground" title={e.target}>
                  {e.target || "-"}
                </span>
              </TableCell>
              <TableCell className="whitespace-nowrap">
                {e.result === "ok" ? (
                  <StatusText tone="success" icon={<CircleCheckIcon aria-hidden="true" />}>
                    OK
                  </StatusText>
                ) : (
                  <span className="flex max-w-48 min-w-0 items-center gap-2">
                    <StatusText tone="destructive" icon={<CircleAlertIcon aria-hidden="true" />}>
                      Error
                    </StatusText>
                    {e.code && (
                      <span className="truncate font-mono text-[12px] text-muted-foreground" title={e.code}>
                        {e.code}
                      </span>
                    )}
                  </span>
                )}
              </TableCell>
              <TableCell className="text-right whitespace-nowrap text-muted-foreground">
                <time dateTime={e.at} title={fullTime(e.at)} suppressHydrationWarning>
                  {timeAgo(e.at)}
                </time>
              </TableCell>
            </TableRow>
          )
        })}
      </tbody>
    </Table>
  )
}
