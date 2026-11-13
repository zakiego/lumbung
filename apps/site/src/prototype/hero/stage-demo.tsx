// Product stage: left-aligned copy, then a framed shot of the dashboard
// running the branch loop: copy main, migrate the copy, compare, main safe.
import { CheckIcon, CopyIcon, DatabaseIcon, GitBranchIcon, GitCompareIcon, ShieldCheckIcon } from "lucide-react"
import { cn } from "@/lib/utils"
import { CTA, Header, Stage, Typed, headline, sentence, useLoop } from "./shared"

const steps = [
  { icon: CopyIcon, caption: "Copy main to pr-42" },
  { icon: DatabaseIcon, caption: "Run the migration on pr-42" },
  { icon: GitCompareIcon, caption: "Compare pr-42 with main" },
  { icon: ShieldCheckIcon, caption: "main never changed" },
]

const columns = [
  { name: "id", type: "INTEGER" },
  { name: "email", type: "TEXT" },
  { name: "total", type: "REAL" },
]

export function StageDemo() {
  const step = useLoop(steps.length, 2600)
  const migrated = step >= 1
  const diffing = step >= 2
  const safe = step >= 3
  const Icon = steps[step]!.icon
  return (
    <div className="min-h-svh">
      <Header />
      <main className="mx-auto max-w-6xl px-6 pt-20 pb-24 md:pt-28">
        <h1 className="-ml-0.5 max-w-3xl text-[40px] leading-[1.05] font-[510] tracking-[-0.022em] text-balance md:text-[64px] md:leading-none">
          {headline}
        </h1>
        <p className="mt-6 max-w-xl text-[15px] leading-relaxed text-muted-foreground md:text-[17px]">{sentence}</p>
        <div className="mt-8">
          <CTA />
        </div>

        <Stage className="mt-16 md:mt-20">
          <div className="relative grid min-h-[420px] md:grid-cols-[220px_1fr]">
            <aside className="hidden flex-col gap-1 border-r border-white/[0.06] p-4 text-[13px] md:flex">
              <span className="px-2 pb-2 text-xs text-faint">Branches</span>
              <Row active={!diffing || safe} label="main" />
              {/* pr-42 appears once its caption has typed out. */}
              <div className="animate-in fade-in slide-in-from-top-1 fill-mode-both delay-1000 duration-500 ease-out">
                <Row active={diffing && !safe} label="pr-42" child />
              </div>
            </aside>
            <div className="flex flex-col gap-5 p-5 md:p-8">
              <div className="flex items-center gap-3 text-[13px] text-secondary-text">
                <span className="flex size-7 items-center justify-center rounded-full border border-white/10 bg-white/[0.04]">
                  <Icon className="size-3.5" />
                </span>
                <Typed key={step} text={steps[step]!.caption} className="font-mono" />
              </div>

              <div className="rounded-lg border border-white/[0.06] bg-white/[0.02]">
                <div className="flex items-center gap-2 border-b border-white/[0.06] px-4 py-2.5 font-mono text-xs text-muted-foreground">
                  <GitBranchIcon className="size-3.5" />
                  {diffing && !safe ? "pr-42 vs main" : migrated && !safe ? "pr-42" : "main"} · orders
                </div>
                <ul className="divide-y divide-white/[0.05] font-mono text-[13px]">
                  {columns.map((c) => (
                    <li key={c.name} className="flex justify-between px-4 py-2.5">
                      <span>{c.name}</span>
                      <span className="text-faint">{c.type}</span>
                    </li>
                  ))}
                  <li
                    className={cn(
                      "flex justify-between px-4 transition-[max-height,opacity,padding] duration-500 ease-out",
                      migrated && !safe ? "max-h-12 py-2.5 opacity-100" : "max-h-0 py-0 opacity-0",
                      diffing && "bg-[rgba(39,166,68,0.07)] text-[#27a644]",
                    )}
                  >
                    <span>{diffing ? "+ " : ""}coupon</span>
                    <span className={diffing ? "" : "text-faint"}>TEXT</span>
                  </li>
                </ul>
              </div>

              <div className="mt-auto grid grid-cols-3 gap-px overflow-hidden rounded-lg border border-white/[0.06] bg-white/[0.06] text-[13px]">
                <Metric label="Copy time" value="0.3 s" />
                <Metric label="Size" value="46.0 MB" />
                <Metric
                  label="main"
                  value={
                    <span className={cn("flex items-center gap-1.5", safe && "text-[#27a644]")}>
                      <CheckIcon className="size-3.5" />
                      Untouched
                    </span>
                  }
                />
              </div>
            </div>
            <div aria-hidden="true" className="pointer-events-none absolute inset-x-0 bottom-0 h-24 bg-gradient-to-t from-[#0b0c0d] to-transparent" />
          </div>
        </Stage>
      </main>
    </div>
  )
}

function Row({ label, active, child }: { label: string; active: boolean; child?: boolean }) {
  return (
    <div className={cn("flex items-center gap-2 rounded-md px-2 py-1.5 font-mono transition-colors duration-150", active ? "bg-white/[0.06] text-foreground" : "text-muted-foreground", child && "ml-4")}>
      <GitBranchIcon className="size-3.5 text-faint" />
      {label}
    </div>
  )
}

function Metric({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1 bg-[#0b0c0d] px-4 py-3">
      <span className="text-xs text-faint">{label}</span>
      <span className="font-medium tabular-nums">{value}</span>
    </div>
  )
}
