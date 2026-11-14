import { Await, useRouter } from "@tanstack/react-router"
import { CircleAlertIcon, RotateCwIcon } from "lucide-react"
import { type ReactNode, useDeferredValue } from "react"
import { Button } from "@/components/ui/button"
import { SegmentedControl, SegmentedControlItem } from "@/components/ui/segmented-control"
import { Skeleton, StatusText } from "@/components/ui/surface"
import { Tooltip } from "@/components/ui/tooltip"
import type { Settled } from "@/lib/settle"
import { WINDOWS, type StatsWindow } from "@/lib/stats"

export function StatGrid({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-2 gap-3 md:gap-4 lg:grid-cols-4">{children}</dl>
}

export function Stat({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-lg border bg-card px-4 py-3">
      <dt className="flex min-w-0 items-center gap-1.5 text-[13px] text-muted-foreground">
        <span className="truncate">
          {label}
          {hint && <> · {hint}</>}
        </span>
      </dt>
      <dd className="truncate text-xl font-[590] tracking-[-0.012em] tabular-nums">{children}</dd>
    </div>
  )
}

// The router reloads a stale loader in the background (coming back to a page,
// a new window), which hands over a fresh promise. A plain Await would drop the
// last result for its skeleton until that settles. This keeps the last result
// up instead; the fallback only shows on the first load. With `dim`, a reload
// slower than 300ms fades the old result so it doesn't pass for the new one.
export function AwaitLatest<T>({ promise, fallback, dim, children }: { promise: Promise<T>; fallback: ReactNode; dim?: boolean; children: (value: T) => ReactNode }) {
  const shown = useDeferredValue(promise)
  const stale = shown !== promise
  return (
    <Await promise={shown} fallback={fallback}>
      {(value) =>
        dim ? (
          <div aria-busy={stale || undefined} data-stale={stale || undefined} className="transition-opacity duration-200 ease-out-cubic data-stale:opacity-50 data-stale:delay-300">
            {children(value)}
          </div>
        ) : (
          children(value)
        )
      }
    </Await>
  )
}

export function Deferred<T>({ promise, children }: { promise: Promise<Settled<T>>; children: (value: T) => ReactNode }) {
  return (
    <AwaitLatest promise={promise} fallback={<Skeleton className="mt-1 h-6 w-16" />}>
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
    </AwaitLatest>
  )
}

export function WindowSelect({ value, onChange }: { value: StatsWindow; onChange: (w: StatsWindow) => void }) {
  return (
    <SegmentedControl aria-label="Time window" value={value} onValueChange={(v) => v && onChange(v as StatsWindow)}>
      {WINDOWS.map((w) => (
        <SegmentedControlItem key={w} value={w} className="px-2 tabular-nums">
          {w}
        </SegmentedControlItem>
      ))}
    </SegmentedControl>
  )
}

export function ChartFailed({ message }: { message: string }) {
  const router = useRouter()
  return (
    <div className="flex h-60 flex-col items-center justify-center gap-3 text-center">
      <StatusText tone="destructive" icon={<CircleAlertIcon aria-hidden="true" />} className="max-w-sm">
        {message}
      </StatusText>
      <Button variant="secondary" size="sm" onClick={() => router.invalidate()}>
        <RotateCwIcon aria-hidden="true" />
        Try again
      </Button>
    </div>
  )
}
