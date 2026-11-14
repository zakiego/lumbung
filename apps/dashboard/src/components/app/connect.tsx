import { CheckIcon, CopyIcon } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/layout"
import { Tabs, TabsList, TabsPanel, TabsTab } from "@/components/ui/tabs"
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
      onClick={async () => {
        await navigator.clipboard?.writeText(text)
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

export function CodeBlock({ code, copyLabel, className }: { code: string; copyLabel: string; className?: string }) {
  return (
    <div className={cn("group/code relative rounded-lg border bg-code", className)}>
      <ScrollArea axis="x">
        <pre className="py-4 pr-14 pl-4 font-mono text-[13px] leading-[1.6] text-foreground/90">
          <code>{code}</code>
        </pre>
      </ScrollArea>
      <CopyButton
        text={code}
        label={copyLabel}
        className="absolute top-2 right-2 opacity-0 transition-opacity duration-250 group-hover/code:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100"
      />
    </div>
  )
}

export function snippets({ url, db, branch }: { url: string; db: string; branch?: string }) {
  const sdkBranch = branch ? `\n  branch: "${branch}",` : ""
  const cliBranch = branch ? ` --branch ${branch}` : ""
  const path = branch ? `/v1/databases/${db}/branches/${branch}/query` : `/v1/databases/${db}/query`
  return {
    sdk: `import { createClient } from "@lumbung/client"

const db = createClient({
  url: "${url}",
  token: process.env.LUMBUNG_TOKEN!,
  database: "${db}",${sdkBranch}
})

const { rows } = await db.query("SELECT 1")`,
    cli: `echo "$LUMBUNG_TOKEN" | lumbung login --url ${url}
lumbung query "SELECT 1" --db ${db}${cliBranch}`,
    curl: `curl -s ${url}${path} \\
  -H "Authorization: Bearer $LUMBUNG_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d '{"sql": "SELECT 1"}'`,
    mcp: `claude mcp add --transport http lumbung ${url}/mcp \\
  --header "Authorization: Bearer $LUMBUNG_TOKEN"`,
  }
}

const TABS = [
  { value: "sdk", label: "SDK" },
  { value: "cli", label: "CLI" },
  { value: "curl", label: "curl" },
  { value: "mcp", label: "MCP" },
] as const

export function Connect({ url, db, branch }: { url: string; db: string; branch?: string }) {
  const code = snippets({ url, db, branch })
  return (
    <Tabs defaultValue="sdk" className="flex flex-col gap-3">
      <TabsList aria-label="Connect with" className="gap-4">
        {TABS.map((t) => (
          <TabsTab key={t.value} value={t.value} className="h-9">
            {t.label}
          </TabsTab>
        ))}
      </TabsList>
      {TABS.map((t) => (
        <TabsPanel key={t.value} value={t.value}>
          <CodeBlock code={code[t.value]} copyLabel={`Copy ${t.label} snippet`} />
        </TabsPanel>
      ))}
    </Tabs>
  )
}
