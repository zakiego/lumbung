import { CheckIcon, CopyIcon } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/layout"
import { Tabs, TabsList, TabsPanel, TabsTab } from "@/components/ui/tabs"
import { toast } from "@/components/ui/toast"
import type { Snippet } from "@/lib/snippets"
import { cn } from "@/lib/utils"

export function CopyButton({ text, label, className }: { text: string; label: string; className?: string }) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={label}
      className={cn("rounded-full", className)}
      onClick={async (event) => {
        const button = event.currentTarget
        try {
          if (!navigator.clipboard) throw new Error("No clipboard")
          await navigator.clipboard.writeText(text)
        } catch {
          selectNear(button)
          toast.error("Couldn't copy. Select it and copy.")
          return
        }
        setCopied(true)
        clearTimeout(timer.current)
        timer.current = setTimeout(() => setCopied(false), 1500)
      }}
    >
      {copied ? <CheckIcon className="text-success motion-safe:animate-[pop_250ms_var(--ease-out-cubic)]" /> : <CopyIcon />}
      <span className="sr-only" aria-live="polite">
        {copied ? "Copied" : ""}
      </span>
    </Button>
  )
}

// Selects the code next to the button, so it can be copied by hand.
function selectNear(button: HTMLElement) {
  const code = button.parentElement?.querySelector("code")
  const selection = window.getSelection()
  if (!code || !selection) return
  const range = document.createRange()
  range.selectNodeContents(code)
  selection.removeAllRanges()
  selection.addRange(range)
}

export function CodeBlock({ code, copyLabel, className }: { code: string; copyLabel: string; className?: string }) {
  return (
    <div className={cn("group/code relative rounded-lg border bg-code", className)}>
      {/* Long lines wrap instead of scrolling; the float keeps the first lines clear of the copy button. */}
      <pre className="px-4 py-4 font-mono text-[13px] leading-[1.6] whitespace-pre-wrap text-foreground/90 [overflow-wrap:anywhere]">
        <span aria-hidden="true" className="float-right ml-2 h-6 w-8" />
        <code>{code}</code>
      </pre>
      <CopyButton
        text={code}
        label={copyLabel}
        className="absolute top-2 right-2 opacity-0 transition-opacity duration-250 group-hover/code:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100"
      />
    </div>
  )
}

export type DetailRow = { label: string; value: string; copy?: boolean }

/** Connection values, one per row, each with a copy button. */
export function ConnectDetails({ rows }: { rows: DetailRow[] }) {
  return (
    <dl className="divide-y rounded-lg border">
      {rows.map((r) => (
        <div key={r.label} className="grid gap-1 px-4 py-2.5 sm:grid-cols-[140px_minmax(0,1fr)] sm:items-center sm:gap-4">
          <dt className="text-[13px] text-muted-foreground">{r.label}</dt>
          <dd className="flex min-h-7 min-w-0 items-center gap-2">
            <code className="min-w-0 flex-1 font-mono text-[13px] [overflow-wrap:anywhere]">{r.value}</code>
            {r.copy !== false && <CopyButton text={r.value} label={`Copy ${r.label}`} className="-my-1 shrink-0" />}
          </dd>
        </div>
      ))}
    </dl>
  )
}

/** Code snippets behind line tabs, one per client. */
export function SnippetTabs({ snippets, label }: { snippets: Snippet[]; label: string }) {
  return (
    <Tabs defaultValue={snippets[0]?.value} className="flex min-w-0 flex-col gap-3">
      <ScrollArea axis="x" fade>
        <TabsList aria-label={label} className="w-full min-w-max gap-4">
          {snippets.map((t) => (
            <TabsTab key={t.value} value={t.value} className="h-9">
              {t.label}
            </TabsTab>
          ))}
        </TabsList>
      </ScrollArea>
      {snippets.map((t) => (
        <TabsPanel key={t.value} value={t.value}>
          <CodeBlock code={t.code} copyLabel={`Copy ${t.label} snippet`} />
        </TabsPanel>
      ))}
    </Tabs>
  )
}
