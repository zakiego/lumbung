import type { BranchOut } from "@lumbung/client"
import { Link } from "@tanstack/react-router"
import { ClockIcon, LockIcon } from "lucide-react"
import { Badge } from "@/components/ui/surface"
import { formatBytes, formatDate, timeLeft } from "@/lib/format"
import { cn } from "@/lib/utils"

type Node = { branch: BranchOut; depth: number }

const INDENT = 32

// Depth-first, so each branch sits under its parent. A branch whose parent is gone becomes a root.
export function branchTree(branches: BranchOut[]): Node[] {
  const ids = new Set(branches.map((b) => b.id))
  const byParent = new Map<string, BranchOut[]>()
  for (const b of branches) {
    const parent = b.parent_id && ids.has(b.parent_id) && b.parent_id !== b.id ? b.parent_id : ""
    byParent.set(parent, [...(byParent.get(parent) ?? []), b])
  }
  for (const list of byParent.values()) {
    list.sort((a, b) => Number(b.default) - Number(a.default) || a.created_at.localeCompare(b.created_at))
  }
  const out: Node[] = []
  const seen = new Set<string>()
  const walk = (parent: string, depth: number) => {
    for (const b of byParent.get(parent) ?? []) {
      if (seen.has(b.id)) continue
      seen.add(b.id)
      out.push({ branch: b, depth })
      walk(b.id, depth + 1)
    }
  }
  walk("", 0)
  return out
}

export function BranchMap({ db, branches, current, defaultBranch }: { db: string; branches: BranchOut[]; current: string; defaultBranch: string }) {
  const nodes = branchTree(branches)
  return (
    <div className="relative overflow-hidden rounded-xl border bg-card">
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 text-border [background-image:radial-gradient(currentColor_1px,transparent_1px)] [background-size:16px_16px] [mask-image:linear-gradient(to_right,black,transparent_85%)]"
      />
      <ul aria-label="Branches" className="relative flex flex-col gap-3 overflow-x-auto p-4 md:p-8">
        {nodes.map(({ branch: b, depth }) => (
          <li key={b.id} className="relative" style={{ paddingLeft: depth * INDENT }}>
            {depth > 0 && (
              <span
                aria-hidden="true"
                className="absolute -top-3 h-[34px] w-5 rounded-bl-lg border-b border-l border-faint/60"
                style={{ left: (depth - 1) * INDENT + 14 }}
              />
            )}
            <Link
              to="/db/$db"
              params={{ db }}
              search={{ branch: b.name === defaultBranch ? undefined : b.name }}
              aria-current={b.name === current ? "true" : undefined}
              className={cn(
                "relative flex max-w-xl min-w-64 items-center gap-3 rounded-lg border bg-background px-3 py-2.5 text-[13px] transition-colors duration-150 hover:border-faint/60",
                b.name === current && "border-brand/60 ring-1 ring-brand/30 hover:border-brand/60",
              )}
            >
              <span aria-hidden="true" className={cn("size-2 shrink-0 rounded-full", b.default ? "bg-success" : "bg-faint")} />
              <span className="min-w-0 truncate font-mono font-[510]">{b.name}</span>
              {b.default && <Badge>default</Badge>}
              {b.protected && (
                <>
                  <LockIcon aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
                  <span className="sr-only">Protected</span>
                </>
              )}
              <span className="ml-auto flex shrink-0 items-center gap-4 text-muted-foreground tabular-nums">
                <span>{formatBytes(b.size_bytes)}</span>
                {b.expires_at && (
                  <span className="flex items-center gap-1 text-warning" title={`Deleted on ${formatDate(b.expires_at)}`}>
                    <ClockIcon aria-hidden="true" className="size-3.5" />
                    <span suppressHydrationWarning>
                      <span className="sr-only">Expires in </span>
                      {timeLeft(b.expires_at)}
                    </span>
                  </span>
                )}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  )
}
