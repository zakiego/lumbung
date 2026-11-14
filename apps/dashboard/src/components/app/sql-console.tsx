import { HistoryIcon, ListTreeIcon, LockIcon, PlayIcon } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Button, buttonVariants } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/input"
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu"
import { Kbd, StatusText } from "@/components/ui/surface"
import type { PlanStep, RunResult } from "@/fns/sql"
import { number, timeAgo } from "@/lib/format"
import { splitSql, textToRun } from "@/lib/sql-split"
import { CellDialog } from "./data-browser"
import { TableGrid, rowCountLabel } from "./table-grid"

const HISTORY_MAX = 50
/** Cells drawn per result before "Show more", so a 1,000-row, 300-column result stays responsive. */
const CELL_BUDGET = 15_000

export interface HistoryEntry {
  sql: string
  at: number
}

const historyKey = (db: string) => `lumbung:sql-history:${db}`
const draftKey = (db: string) => `lumbung:sql-draft:${db}`

export function readDraft(db: string): string {
  try {
    return sessionStorage.getItem(draftKey(db)) ?? ""
  } catch {
    return ""
  }
}

function saveDraft(db: string, text: string) {
  try {
    if (text) sessionStorage.setItem(draftKey(db), text)
    else sessionStorage.removeItem(draftKey(db))
  } catch {}
}

export function readHistory(db: string): HistoryEntry[] {
  try {
    const raw = localStorage.getItem(historyKey(db))
    const list: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(list) ? list.filter((e): e is HistoryEntry => typeof e?.sql === "string" && typeof e?.at === "number").slice(0, HISTORY_MAX) : []
  } catch {
    return []
  }
}

export function pushHistory(db: string, sql: string, list: HistoryEntry[], now = Date.now()): HistoryEntry[] {
  const next = [{ sql, at: now }, ...list.filter((e) => e.sql !== sql)].slice(0, HISTORY_MAX)
  try {
    localStorage.setItem(historyKey(db), JSON.stringify(next))
  } catch {
    // Storage can be full or blocked; history is a convenience.
  }
  return next
}

type Output =
  | { kind: "results"; run: RunResult; statements: string[]; id: number }
  | { kind: "plan"; steps: PlanStep[]; sql: string }
  | { kind: "error"; message: string }

type SqlConsoleProps = {
  db: string
  readOnly: boolean
  run: (statements: string[]) => Promise<RunResult>
  explain: (sql: string) => Promise<PlanStep[]>
}

function message(e: unknown) {
  return e instanceof Error ? e.message : String(e)
}

export function SqlConsole({ db, readOnly, run, explain }: SqlConsoleProps) {
  const editor = useRef<HTMLTextAreaElement>(null)
  const [text, setText] = useState("")
  const [output, setOutput] = useState<Output | null>(null)
  const [pending, setPending] = useState<"run" | "plan" | null>(null)
  const [history, setHistory] = useState<HistoryEntry[]>([])
  const [mac, setMac] = useState(true)
  const runs = useRef(0)

  useEffect(() => setHistory(readHistory(db)), [db])
  useEffect(() => setText(readDraft(db)), [db])
  useEffect(() => setMac(/Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)), [])

  function selected(): string {
    const el = editor.current
    const value = el?.value ?? text
    return el ? textToRun(value, el.selectionStart, el.selectionEnd) : value
  }

  async function runNow() {
    if (pending) return
    const source = selected()
    const statements = splitSql(source).map((s) => s.sql)
    if (!statements.length) return
    setPending("run")
    setHistory((h) => pushHistory(db, source.trim(), h))
    try {
      const result = await run(statements)
      setOutput({ kind: "results", run: result, statements, id: ++runs.current })
    } catch (e) {
      setOutput({ kind: "error", message: message(e) })
    } finally {
      setPending(null)
    }
  }

  async function planNow() {
    if (pending) return
    const statements = splitSql(selected())
    if (!statements.length) return
    if (statements.length > 1) {
      setOutput({ kind: "error", message: "Plan one statement at a time. Select it first." })
      return
    }
    setPending("plan")
    try {
      setOutput({ kind: "plan", steps: await explain(statements[0]!.sql), sql: statements[0]!.sql })
    } catch (e) {
      setOutput({ kind: "error", message: message(e) })
    } finally {
      setPending(null)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <Field>
        <FieldLabel className="sr-only">SQL</FieldLabel>
        <Textarea
          ref={editor}
          value={text}
          onValueChange={(v) => {
            setText(v)
            saveDraft(db, v)
          }}
          submitShortcut="mod+enter"
          onSubmit={() => void runNow()}
          autoResize
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          placeholder="SELECT 1"
          className="max-h-[50svh] min-h-40 bg-code font-mono text-base leading-relaxed md:text-[13px]"
        />
      </Field>

      <div className="flex flex-wrap items-center gap-2">
        <Button onClick={() => runNow()} disabled={pending === "plan"}>
          <PlayIcon aria-hidden="true" />
          Run
          <Kbd className="ml-1 border-primary-foreground/25 text-primary-foreground/70" aria-hidden="true">
            {mac ? "⌘↵" : "Ctrl ↵"}
          </Kbd>
        </Button>
        <Button variant="secondary" onClick={() => planNow()} disabled={pending === "run"}>
          <ListTreeIcon aria-hidden="true" />
          Plan
        </Button>
        <Menu>
          <MenuTrigger className={buttonVariants({ variant: "ghost" })} disabled={!history.length}>
            <HistoryIcon aria-hidden="true" />
            History
          </MenuTrigger>
          <MenuContent align="start" className="max-h-80 w-[min(28rem,calc(100vw-32px))] overflow-y-auto">
            {history.map((h) => (
              <MenuItem
                key={`${h.at}:${h.sql}`}
                onSelect={() => {
                  setText(h.sql)
                  saveDraft(db, h.sql)
                }}
                className="justify-between gap-4"
              >
                <span className="truncate font-mono text-xs">{h.sql.replace(/\s+/g, " ")}</span>
                <span className="shrink-0 text-xs text-muted-foreground" suppressHydrationWarning>
                  {timeAgo(new Date(h.at).toISOString())}
                </span>
              </MenuItem>
            ))}
          </MenuContent>
        </Menu>
        {readOnly && (
          <StatusText icon={<LockIcon aria-hidden="true" />} className="ml-auto">
            Read-only
          </StatusText>
        )}
      </div>

      <div aria-busy={pending ? true : undefined} className="relative flex flex-col gap-6">
        {/* Always mounted, so screen readers hear each new summary. Empty, it leaves the flow. */}
        <p aria-live="polite" className="text-[13px] text-muted-foreground tabular-nums empty:absolute">
          {output?.kind === "results" && summary(output.run)}
        </p>
        {output?.kind === "error" && (
          <pre role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2.5 font-mono text-xs leading-relaxed whitespace-pre-wrap text-destructive [overflow-wrap:anywhere]">
            {output.message}
          </pre>
        )}
        {output?.kind === "plan" && <Plan steps={output.steps} />}
        {output?.kind === "results" && <Results key={output.id} run={output.run} statements={output.statements} />}
      </div>
    </div>
  )
}

function summary(run: RunResult): string {
  return `${run.results.length > 1 ? `${run.results.length} statements · ` : ""}${number.format(run.ms)} ms`
}

function Results({ run, statements }: { run: RunResult; statements: string[] }) {
  const [open, setOpen] = useState<{ result: number; row: string; col: number } | null>(null)
  const [extra, setExtra] = useState<Record<number, number>>({})
  const many = run.results.length > 1
  const openResult = open ? run.results[open.result] : undefined
  return (
    <>
      {run.results.map((r, i) => {
        const label = `Result ${i + 1}`
        const sql = statements[i] ?? ""
        const step = Math.max(50, Math.floor(CELL_BUDGET / Math.max(1, r.columns.length)))
        const shown = Math.min(r.rows.length, step * (1 + (extra[i] ?? 0)))
        return (
          <section key={`${i}:${sql}`} className="flex min-w-0 flex-col gap-2">
            <div className="flex min-w-0 items-baseline gap-3 text-[13px]">
              {many && <span className="truncate font-mono text-xs text-muted-foreground" title={sql}>{sql.replace(/\s+/g, " ")}</span>}
              <span className="ml-auto shrink-0 text-muted-foreground tabular-nums">
                {r.columns.length ? rowCountLabel(r.rowCount) : `${number.format(r.rowsAffected)} ${r.rowsAffected === 1 ? "row" : "rows"} changed`}
              </span>
            </div>
            {r.columns.length > 0 && (
              <TableGrid
                label={label}
                columns={r.columns.map((name) => ({ name }))}
                rows={r.rows.slice(0, shown).map((cells, ri) => ({ id: String(ri), cells }))}
                onOpenCell={(row, col) => setOpen({ result: i, row, col })}
                empty="No rows"
              />
            )}
            {(r.capped || shown < r.rows.length) && (
              <div className="flex items-center gap-3 text-xs text-muted-foreground tabular-nums">
                <span>
                  First {number.format(shown)} of {number.format(r.rowCount)} rows
                </span>
                {shown < r.rows.length && (
                  <Button variant="ghost" size="sm" onClick={() => setExtra((e) => ({ ...e, [i]: (e[i] ?? 0) + 1 }))} aria-label={`Show more rows of ${label}`}>
                    Show more
                  </Button>
                )}
              </div>
            )}
          </section>
        )
      })}
      {open && openResult && (
        <CellDialog
          key={`${open.result}:${open.row}:${open.col}`}
          column={openResult.columns[open.col] ?? ""}
          type=""
          cell={openResult.rows[Number(open.row)]![open.col]!}
          editable={false}
          onSave={async () => {}}
          onClose={() => setOpen(null)}
        />
      )}
    </>
  )
}

interface PlanNode extends PlanStep {
  children: PlanNode[]
}

export function planTree(steps: PlanStep[]): PlanNode[] {
  const nodes = new Map<number, PlanNode>(steps.map((s) => [s.id, { ...s, children: [] }]))
  const roots: PlanNode[] = []
  for (const s of steps) {
    const node = nodes.get(s.id)!
    const parent = s.parent !== s.id ? nodes.get(s.parent) : undefined
    if (parent) parent.children.push(node)
    else roots.push(node)
  }
  return roots
}

function PlanList({ nodes, root }: { nodes: PlanNode[]; root?: boolean }) {
  return (
    <ul className={root ? "flex flex-col gap-1.5" : "mt-1.5 ml-1.5 flex flex-col gap-1.5 border-l pl-4"}>
      {nodes.map((n) => (
        <li key={n.id} className="font-mono text-xs leading-relaxed [overflow-wrap:anywhere]">
          {n.detail}
          {n.children.length > 0 && <PlanList nodes={n.children} />}
        </li>
      ))}
    </ul>
  )
}

function Plan({ steps }: { steps: PlanStep[] }) {
  return (
    <section aria-label="Query plan" className="rounded-lg border bg-code px-4 py-3">
      {steps.length ? <PlanList nodes={planTree(steps)} root /> : <p className="text-[13px] text-muted-foreground">No plan</p>}
    </section>
  )
}
