import type { WorkspaceLimitsOut, WorkspaceOut, WorkspaceUsageOut } from "@lumbung/client"
import { ArrowRightLeftIcon } from "lucide-react"
import { type FormEvent, useState } from "react"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NumberInput } from "@/components/ui/number-input"
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/components/ui/select"
import { Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { formatBytes, formatDate, number } from "@/lib/format"
import { Confirm, FormDialog, errorText } from "./form-dialog"
import { NAME_PATTERN } from "./name-dialog"

const MB = 1024 * 1024

/** "3 of 20", or just "3" when the workspace has no limit of its own. */
export function ofLimit(used: string, limit: string | null): string {
  return limit ? `${used} of ${limit}` : used
}

export function UsageList({ usage, limits }: { usage: WorkspaceUsageOut; limits: WorkspaceLimitsOut }) {
  const rows: [string, string][] = [
    ["Databases", ofLimit(number.format(usage.databases), limits.max_databases ? number.format(limits.max_databases) : null)],
    ["Buckets", ofLimit(number.format(usage.buckets), limits.max_buckets ? number.format(limits.max_buckets) : null)],
    ["Database storage", ofLimit(formatBytes(usage.database_bytes), limits.max_database_bytes ? formatBytes(limits.max_database_bytes) : null)],
    ["Object storage", ofLimit(formatBytes(usage.object_bytes), limits.max_object_bytes ? formatBytes(limits.max_object_bytes) : null)],
    ["Tokens", number.format(usage.tokens)],
  ]
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-8 gap-y-3 text-[13px]">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="flex h-7 items-center text-muted-foreground">{label}</dt>
          <dd className="flex h-7 min-w-0 items-center tabular-nums">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

export type LimitsPatch = { maxDatabases?: number; maxBuckets?: number; maxDatabaseBytes?: number; maxObjectBytes?: number }

/** A byte limit as whole megabytes for the field, or null for none. */
function toMB(bytes: number): number | null {
  return bytes > 0 ? Math.max(1, Math.round(bytes / MB)) : null
}

// An empty field is "no limit", which the engine stores as 0. Only the
// fields that changed are sent, so a byte limit set elsewhere to something
// that isn't a whole megabyte survives a save that didn't touch it.
export function LimitsForm({ limits, onSave }: { limits: WorkspaceLimitsOut; onSave: (patch: LimitsPatch) => Promise<unknown> }) {
  const start = {
    databases: limits.max_databases || null,
    buckets: limits.max_buckets || null,
    databaseMB: toMB(limits.max_database_bytes),
    objectMB: toMB(limits.max_object_bytes),
  }
  const [databases, setDatabases] = useState<number | null>(start.databases)
  const [buckets, setBuckets] = useState<number | null>(start.buckets)
  const [databaseMB, setDatabaseMB] = useState<number | null>(start.databaseMB)
  const [objectMB, setObjectMB] = useState<number | null>(start.objectMB)
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)

  const patch: LimitsPatch = {}
  if (databases !== start.databases) patch.maxDatabases = databases ?? 0
  if (buckets !== start.buckets) patch.maxBuckets = buckets ?? 0
  if (databaseMB !== start.databaseMB) patch.maxDatabaseBytes = (databaseMB ?? 0) * MB
  if (objectMB !== start.objectMB) patch.maxObjectBytes = (objectMB ?? 0) * MB
  const changed = Object.keys(patch).length > 0

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (pending || !changed) return
    setPending(true)
    setError("")
    try {
      await onSave(patch)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setPending(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <div className="grid max-w-sm grid-cols-2 gap-3">
        <Field>
          <FieldLabel>Databases</FieldLabel>
          <NumberInput value={databases} onValueChange={setDatabases} min={1} max={1_000_000} placeholder="No limit" />
        </Field>
        <Field>
          <FieldLabel>Buckets</FieldLabel>
          <NumberInput value={buckets} onValueChange={setBuckets} min={1} max={1_000_000} placeholder="No limit" />
        </Field>
        <Field>
          <FieldLabel>Database storage (MB)</FieldLabel>
          <NumberInput value={databaseMB} onValueChange={setDatabaseMB} min={1} max={8_000_000_000} placeholder="No limit" />
        </Field>
        <Field>
          <FieldLabel>Object storage (MB)</FieldLabel>
          <NumberInput value={objectMB} onValueChange={setObjectMB} min={1} max={8_000_000_000} placeholder="No limit" />
        </Field>
      </div>
      {error && (
        <p role="alert" className="text-[13px] text-destructive">
          {error}
        </p>
      )}
      <Button type="submit" variant="secondary" className="self-start" disabled={pending || !changed}>
        {pending ? "Saving…" : "Save limits"}
      </Button>
    </form>
  )
}

type Target = { id: string; name: string }

/** Moves a database or a bucket to another workspace. Every token that reached it stops working. */
export function MoveForm({ what, name, targets, onMove }: { what: "database" | "bucket"; name: string; targets: Target[]; onMove: (to: Target) => Promise<unknown> }) {
  const [to, setTo] = useState(targets[0]?.id ?? "")
  const target = targets.find((t) => t.id === to)
  const items = targets.map((t) => ({ value: t.id, label: t.name }))
  return (
    <div className="flex flex-col gap-4">
      <Field>
        <FieldLabel>Workspace</FieldLabel>
        <Select items={items} value={to} onValueChange={(v) => typeof v === "string" && setTo(v)}>
          <SelectTrigger className="max-w-sm font-mono" />
          <SelectContent>
            {targets.map((t) => (
              <SelectItem key={t.id} value={t.id} className="font-mono">
                {t.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      {target && (
        <Confirm
          trigger={
            <>
              <ArrowRightLeftIcon aria-hidden="true" />
              Move
            </>
          }
          triggerStyle={{ variant: "secondary", className: "self-start" }}
          title={`Move ${name} to ${target.name}?`}
          description={`Tokens that reach this ${what} stop working. Make new ones in ${target.name}.`}
          actionLabel={`Move ${what}`}
          agentConfirm={`Moves the ${what} ${name} to the workspace ${target.name} and revokes the tokens that reach it`}
          onConfirm={() => onMove(target)}
        />
      )}
    </div>
  )
}

/** Workspaces in the trash, with a way back. A name taken since then needs a new one. */
export function DeletedWorkspaces({ workspaces, onRestore }: { workspaces: WorkspaceOut[]; onRestore: (w: WorkspaceOut, name?: string) => Promise<unknown> }) {
  if (workspaces.length === 0) return <Empty title="No deleted workspaces" />
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Workspace</TableHead>
          <TableHead className="hidden w-px text-right sm:table-cell">Deleted</TableHead>
          <TableHead className="hidden w-px text-right whitespace-nowrap sm:table-cell">Removed for good</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </tr>
      </thead>
      <tbody>
        {workspaces.map((w) => (
          <TableRow key={w.id}>
            <TableCell className="max-w-0 w-1/2 py-2">
              <span title={w.name} className="block font-mono font-[510] [overflow-wrap:anywhere] sm:truncate">
                {w.name}
              </span>
              {w.purge_after && <span className="block text-xs text-muted-foreground sm:hidden">Removed for good {formatDate(w.purge_after)}</span>}
            </TableCell>
            <TableCell className="hidden text-right whitespace-nowrap text-muted-foreground sm:table-cell">{w.deleted_at ? formatDate(w.deleted_at) : "-"}</TableCell>
            <TableCell className="hidden text-right whitespace-nowrap text-muted-foreground sm:table-cell">{w.purge_after ? formatDate(w.purge_after) : "-"}</TableCell>
            <TableCell className="w-px">
              <RestoreWorkspace workspace={w} onRestore={onRestore} />
            </TableCell>
          </TableRow>
        ))}
      </tbody>
    </Table>
  )
}

function RestoreWorkspace({ workspace: w, onRestore }: { workspace: WorkspaceOut; onRestore: (w: WorkspaceOut, name?: string) => Promise<unknown> }) {
  const [name, setName] = useState(w.name)
  return (
    <FormDialog
      trigger="Restore"
      triggerStyle={{ variant: "secondary", size: "sm", label: `Restore ${w.name}` }}
      title={`Restore ${w.name}`}
      description="Its databases, buckets and tokens come back with it."
      submit="Restore"
      pendingSubmit="Restoring…"
      onOpen={() => setName(w.name)}
      onSubmit={async () => {
        await onRestore(w, name === w.name ? undefined : name)
      }}
    >
      <Field>
        <FieldLabel>Name</FieldLabel>
        <WorkspaceNameInput value={name} onValueChange={setName} />
        <FieldError match="patternMismatch">Use lowercase letters, digits and dashes.</FieldError>
      </Field>
    </FormDialog>
  )
}

function WorkspaceNameInput({ value, onValueChange }: { value: string; onValueChange: (v: string) => void }) {
  return <Input value={value} onValueChange={onValueChange} required pattern={NAME_PATTERN} autoComplete="off" spellCheck={false} className="font-mono" />
}
