// Branch map: the overview is the database's branch tree, since branches
// are what makes Lumbung different. Numbers and connect come after.
import { ClockIcon, GitBranchPlusIcon, LockIcon } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { cn } from "@/lib/utils"
import { Shell } from "./shell"
import { type BranchRow, branches, database, formatBytes, number, snapshots, totals } from "./data"
import { Connect, Legend, RequestsChart } from "./parts"

type Node = BranchRow & { depth: number; last: boolean }

// Depth-first order, so each branch sits under its parent.
function tree(): Node[] {
  const out: Node[] = []
  const walk = (parent: string | undefined, depth: number) => {
    const kids = branches.filter((b) => b.parent === parent)
    kids.forEach((b, i) => {
      out.push({ ...b, depth, last: i === kids.length - 1 })
      walk(b.name, depth + 1)
    })
  }
  walk(undefined, 0)
  return out
}

export function BranchMap() {
  const nodes = tree()
  return (
    <Shell
      actions={
        <Button size="sm">
          <GitBranchPlusIcon data-icon="inline-start" />
          New branch
        </Button>
      }
    >
      {(branch) => (
        <div className="flex flex-col gap-8">
          <div className="relative overflow-hidden rounded-xl border bg-card">
            {/* A dot field behind the tree, faded at the edges. */}
            <div
              aria-hidden="true"
              className="pointer-events-none absolute inset-0 text-border [background-image:radial-gradient(currentColor_1px,transparent_1px)] [background-size:16px_16px] [mask-image:linear-gradient(to_right,black,transparent_85%)]"
            />
            <ul className="relative flex flex-col gap-3 p-5 md:p-8">
              {nodes.map((n) => (
                <li key={n.name} className="relative" style={{ paddingLeft: n.depth * 40 }}>
                  {n.depth > 0 && (
                    <span
                      aria-hidden="true"
                      className={cn(
                        "absolute top-[-12px] w-6 rounded-bl-lg border-b border-l border-faint/60",
                        n.last ? "h-[38px]" : "h-[38px]",
                      )}
                      style={{ left: (n.depth - 1) * 40 + 16 }}
                    />
                  )}
                  <BranchTile node={n} active={n.name === branch} />
                </li>
              ))}
            </ul>
          </div>

          <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
            <Stat label="Size" value={formatBytes(database.sizeBytes)} />
            <Stat label="Tables" value={String(database.tables)} />
            <Stat label="Requests, 24h" value={number.format(totals.reads24h + totals.writes24h)} />
            <Stat label="Last snapshot" value={snapshots.latest} />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader className="flex-row items-center justify-between">
                <CardTitle className="text-[15px]">Requests on {branch}</CardTitle>
                <Legend />
              </CardHeader>
              <CardContent>
                <RequestsChart className="h-48" />
              </CardContent>
            </Card>
            <Card>
              <CardHeader>
                <CardTitle className="text-[15px]">Connect</CardTitle>
              </CardHeader>
              <CardContent>
                <Connect />
              </CardContent>
            </Card>
          </div>
        </div>
      )}
    </Shell>
  )
}

function BranchTile({ node, active }: { node: Node; active: boolean }) {
  return (
    <div
      className={cn(
        "relative flex max-w-xl items-center gap-3 rounded-lg border bg-background px-3 py-2.5 text-[13px] transition-colors duration-150",
        active && "border-brand/60 ring-1 ring-brand/30",
      )}
    >
      <span className="relative flex size-2 shrink-0">
        {node.default && (
          <span className="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-60 motion-reduce:hidden" />
        )}
        <span className={cn("relative inline-flex size-2 rounded-full", node.default ? "bg-success" : "bg-faint")} />
      </span>
      <span className="min-w-0 truncate font-mono font-medium">{node.name}</span>
      {node.default && <Badge variant="outline">default</Badge>}
      {node.protected && <LockIcon className="size-3.5 shrink-0 text-faint" aria-label="Protected" />}
      <span className="ml-auto flex shrink-0 items-center gap-4 text-muted-foreground tabular-nums">
        <span className="hidden sm:inline">{number.format(node.writes24h)} writes</span>
        <span>{formatBytes(node.sizeBytes)}</span>
        {node.expiresInDays && (
          <span className="flex items-center gap-1 text-warning">
            <ClockIcon className="size-3.5" />
            {node.expiresInDays}d
          </span>
        )}
      </span>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card size="sm">
      <CardContent className="flex flex-col gap-1">
        <span className="text-[13px] text-muted-foreground">{label}</span>
        <span className="text-xl font-semibold tracking-tight tabular-nums">{value}</span>
      </CardContent>
    </Card>
  )
}
