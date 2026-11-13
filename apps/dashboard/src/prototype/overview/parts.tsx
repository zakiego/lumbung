import { CheckIcon, CopyIcon } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts"
import { Button } from "@/components/ui/button"
import { type ChartConfig, ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart"
import { ScrollArea, ScrollBar } from "@/components/ui/scroll-area"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { cn } from "@/lib/utils"
import { requests, snippets } from "./data"

export function CopyButton({ text, className }: { text: string; className?: string }) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={copied ? "Copied" : "Copy"}
      className={cn("rounded-full active:scale-[0.97]", className)}
      onClick={() => {
        void navigator.clipboard?.writeText(text)
        setCopied(true)
        clearTimeout(timer.current)
        timer.current = setTimeout(() => setCopied(false), 1500)
      }}
    >
      {copied ? <CheckIcon className="animate-in zoom-in-80 duration-250" /> : <CopyIcon />}
    </Button>
  )
}

// A soft surface one step lighter than the page, a hairline border, no
// language label, and a copy button that fades in on hover.
export function CodeBlock({ code, className }: { code: string; className?: string }) {
  return (
    <div className={cn("group/code relative rounded-lg border bg-code", className)}>
      <ScrollArea className="w-full">
        <pre className="py-4 pr-14 pl-4 font-mono text-[13px] leading-[1.6] text-foreground/90">
          <code>{code}</code>
        </pre>
        <ScrollBar orientation="horizontal" />
      </ScrollArea>
      <CopyButton
        text={code}
        className="absolute top-2 right-2 opacity-0 transition-opacity duration-250 group-hover/code:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100"
      />
    </div>
  )
}

const connectTabs = [
  { value: "sdk", label: "SDK" },
  { value: "cli", label: "CLI" },
  { value: "curl", label: "curl" },
  { value: "mcp", label: "MCP" },
] as const

export function Connect({ className, blockClassName }: { className?: string; blockClassName?: string }) {
  return (
    <Tabs defaultValue="sdk" className={cn("gap-3", className)}>
      <TabsList variant="line" className="h-9 border-b w-full justify-start rounded-none">
        {connectTabs.map((t) => (
          <TabsTrigger key={t.value} value={t.value} className="flex-none px-2 text-[13px]">
            {t.label}
          </TabsTrigger>
        ))}
      </TabsList>
      {connectTabs.map((t) => (
        <TabsContent key={t.value} value={t.value}>
          <CodeBlock code={snippets[t.value]} className={blockClassName} />
        </TabsContent>
      ))}
    </Tabs>
  )
}

const chartConfig = {
  reads: { label: "Reads", color: "var(--chart-1)" },
  writes: { label: "Writes", color: "var(--chart-2)" },
} satisfies ChartConfig

export function RequestsChart({ className, compact }: { className?: string; compact?: boolean }) {
  return (
    <ChartContainer config={chartConfig} className={cn("aspect-auto w-full", compact ? "h-28" : "h-56", className)}>
      <AreaChart data={requests} margin={{ left: 0, right: 0, top: 4, bottom: 0 }}>
        <defs>
          <linearGradient id="fill-reads" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--color-reads)" stopOpacity={0.25} />
            <stop offset="100%" stopColor="var(--color-reads)" stopOpacity={0} />
          </linearGradient>
        </defs>
        {!compact && <CartesianGrid vertical={false} strokeOpacity={0.5} />}
        {!compact && (
          <XAxis dataKey="hour" tickLine={false} axisLine={false} tickMargin={8} interval={5} fontSize={12} />
        )}
        {!compact && <YAxis tickLine={false} axisLine={false} width={40} fontSize={12} />}
        <ChartTooltip cursor={false} content={<ChartTooltipContent indicator="line" />} />
        <Area isAnimationActive={false} dataKey="reads" type="monotone" stroke="var(--color-reads)" strokeWidth={2} fill="url(#fill-reads)" />
        <Area isAnimationActive={false} dataKey="writes" type="monotone" stroke="var(--color-writes)" strokeWidth={2} fill="transparent" />
      </AreaChart>
    </ChartContainer>
  )
}

export function Legend() {
  return (
    <div className="flex items-center gap-4 text-xs text-muted-foreground">
      <span className="flex items-center gap-1.5">
        <span className="size-2 rounded-full bg-chart-1" />
        Reads
      </span>
      <span className="flex items-center gap-1.5">
        <span className="size-2 rounded-full bg-chart-2" />
        Writes
      </span>
    </div>
  )
}
