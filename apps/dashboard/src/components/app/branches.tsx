import type { BranchOut, Change, ColumnInfo, Diff } from "@lumbung/client"
import { Link } from "@tanstack/react-router"
import {
  ArrowUpFromLineIcon,
  ClockIcon,
  GitBranchPlusIcon,
  LockIcon,
  MinusIcon,
  PencilIcon,
  PlusIcon,
  RotateCcwIcon,
  Trash2Icon,
} from "lucide-react"
import { useState } from "react"
import { TypedConfirm } from "@/components/ui/alert-dialog"
import { buttonVariants } from "@/components/ui/button"
import { Switch } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Collapsible, CollapsiblePanel, CollapsibleTrigger } from "@/components/ui/layout"
import { NumberInput } from "@/components/ui/number-input"
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/components/ui/select"
import { Badge, Empty, StatusText, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { formatBytes, formatDate, timeAgo, timeLeft } from "@/lib/format"
import { cn } from "@/lib/utils"
import { CodeBlock } from "./connect"
import { FormDialog } from "./form-dialog"
import { NAME_PATTERN } from "./name-dialog"

export function parentName(branch: BranchOut, branches: BranchOut[]): string | undefined {
  return branch.parent_id ? branches.find((b) => b.id === branch.parent_id)?.name : undefined
}

export function BranchTable({ db, branches, current }: { db: string; branches: BranchOut[]; current: string }) {
  const defaultBranch = branches.find((b) => b.default)?.name
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Branch</TableHead>
          <TableHead className="hidden md:table-cell">Parent</TableHead>
          <TableHead className="text-right">Size</TableHead>
          <TableHead className="hidden text-right sm:table-cell">Expires</TableHead>
          <TableHead className="hidden text-right lg:table-cell">Created</TableHead>
        </tr>
      </thead>
      <tbody>
        {branches.map((b) => {
          const parent = parentName(b, branches)
          return (
            <TableRow key={b.id} className={cn("relative", b.name === current && "bg-accent/40")}>
              <TableCell className="w-1/2 max-w-0">
                <Link
                  to="/db/$db/branches"
                  params={{ db }}
                  search={{ branch: b.name === defaultBranch ? undefined : b.name }}
                  aria-current={b.name === current ? "true" : undefined}
                  title={b.name}
                  className="flex min-w-0 items-center gap-2 font-mono font-[510] after:absolute after:inset-0"
                >
                  <span className="truncate">{b.name}</span>
                  {b.default && <Badge className="font-sans">default</Badge>}
                  {b.protected && (
                    <>
                      <LockIcon aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
                      <span className="sr-only">Protected</span>
                    </>
                  )}
                </Link>
              </TableCell>
              <TableCell className="hidden max-w-0 w-1/4 md:table-cell">
                <span className="block truncate font-mono text-muted-foreground" title={parent}>
                  {parent ?? "-"}
                </span>
              </TableCell>
              <TableCell className="text-right whitespace-nowrap text-muted-foreground tabular-nums">{formatBytes(b.size_bytes)}</TableCell>
              <TableCell className="hidden text-right whitespace-nowrap sm:table-cell">
                {b.expires_at ? (
                  <span className="text-warning tabular-nums" title={formatDate(b.expires_at)} suppressHydrationWarning>
                    {timeLeft(b.expires_at)}
                  </span>
                ) : (
                  <span className="text-muted-foreground">Never</span>
                )}
              </TableCell>
              <TableCell className="hidden text-right whitespace-nowrap text-muted-foreground lg:table-cell">
                <span title={formatDate(b.created_at)} suppressHydrationWarning>
                  {timeAgo(b.created_at)}
                </span>
              </TableCell>
            </TableRow>
          )
        })}
      </tbody>
    </Table>
  )
}

type NewBranch = { name: string; from: string; expiresInDays?: number }

export function NewBranchDialog({ branches, from, onCreate }: { branches: BranchOut[]; from: string; onCreate: (b: NewBranch) => Promise<unknown> }) {
  const [name, setName] = useState("")
  const [source, setSource] = useState(from)
  const [days, setDays] = useState<number | null>(null)
  return (
    <FormDialog
      trigger={
        <>
          <GitBranchPlusIcon aria-hidden="true" />
          New branch
        </>
      }
      title="Create branch"
      submit="Create"
      pendingSubmit="Creating…"
      onOpen={() => {
        setName("")
        setSource(from)
        setDays(null)
      }}
      onSubmit={() => onCreate({ name, from: source, expiresInDays: days ?? undefined }).then(() => undefined)}
    >
      <Field>
        <FieldLabel>Branch name</FieldLabel>
        <Input
          value={name}
          onValueChange={setName}
          placeholder="try-new-index"
          required
          pattern={NAME_PATTERN}
          autoComplete="off"
          spellCheck={false}
          className="font-mono"
        />
        <FieldError match="patternMismatch">Use lowercase letters, digits and dashes.</FieldError>
      </Field>
      <Field>
        <FieldLabel>Copy from</FieldLabel>
        <Select items={branches.map((b) => b.name)} value={source} onValueChange={(v) => typeof v === "string" && setSource(v)}>
          <SelectTrigger className="w-full font-mono" />
          <SelectContent className="font-mono">
            {branches.map((b) => (
              <SelectItem key={b.id} value={b.name}>
                <span className="truncate">{b.name}</span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field>
        <FieldLabel>Delete after (days)</FieldLabel>
        <NumberInput value={days} onValueChange={setDays} min={1} max={3650} placeholder="Keep" />
      </Field>
    </FormDialog>
  )
}

export function RenameBranchDialog({ branch, onRename }: { branch: string; onRename: (name: string) => Promise<unknown> }) {
  const [name, setName] = useState(branch)
  return (
    <FormDialog
      trigger={
        <>
          <PencilIcon aria-hidden="true" />
          Rename
        </>
      }
      triggerStyle={{ variant: "secondary" }}
      title={`Rename ${branch}`}
      submit="Save name"
      pendingSubmit="Saving…"
      onOpen={() => setName(branch)}
      onSubmit={() => onRename(name).then(() => undefined)}
    >
      <Field>
        <FieldLabel>New name</FieldLabel>
        <Input value={name} onValueChange={setName} required pattern={NAME_PATTERN} autoComplete="off" spellCheck={false} className="font-mono" />
        <FieldError match="patternMismatch">Use lowercase letters, digits and dashes.</FieldError>
      </Field>
    </FormDialog>
  )
}

export function ExpiryDialog({ branch, expiresAt, onSave }: { branch: string; expiresAt: string | null; onSave: (days: number) => Promise<unknown> }) {
  const [days, setDays] = useState<number | null>(null)
  return (
    <FormDialog
      trigger={
        <>
          <ClockIcon aria-hidden="true" />
          Expiry
        </>
      }
      triggerStyle={{ variant: "secondary" }}
      title={`Expiry for ${branch}`}
      description={expiresAt ? `Deleted on ${formatDate(expiresAt)} now.` : undefined}
      submit="Save expiry"
      pendingSubmit="Saving…"
      onOpen={() => setDays(null)}
      onSubmit={() => onSave(days ?? 0).then(() => undefined)}
    >
      <Field>
        <FieldLabel>Delete after (days)</FieldLabel>
        <NumberInput value={days} onValueChange={setDays} min={0} max={3650} placeholder="Keep" />
        <FieldDescription>Empty or 0 keeps it.</FieldDescription>
      </Field>
    </FormDialog>
  )
}

export function ProtectSwitch({ branch, disabled, onChange }: { branch: BranchOut; disabled?: boolean; onChange: (protect: boolean) => Promise<unknown> }) {
  const [pending, setPending] = useState(false)
  return (
    <Field className="flex-row items-center gap-2">
      <Switch
        checked={branch.protected}
        disabled={disabled || pending}
        onCheckedChange={(next) => {
          setPending(true)
          onChange(next)
            .catch(() => {})
            .finally(() => setPending(false))
        }}
      />
      <FieldLabel>Protected</FieldLabel>
    </Field>
  )
}

export function PromoteBranch({ branch, onPromote }: { branch: string; onPromote: () => Promise<unknown> }) {
  return (
    <TypedConfirm
      trigger={
        <>
          <ArrowUpFromLineIcon aria-hidden="true" />
          Promote
        </>
      }
      triggerClassName={buttonVariants({ variant: "secondary" })}
      title={`Make ${branch} the default?`}
      description="Apps without a branch will use it. The old default stays."
      confirmText={branch}
      actionLabel="Make default"
      agentConfirm={`Makes ${branch} the default branch`}
      onConfirm={onPromote}
    />
  )
}

export function ResetBranch({ branch, parent, onReset }: { branch: string; parent: string; onReset: () => Promise<unknown> }) {
  return (
    <TypedConfirm
      trigger={
        <>
          <RotateCcwIcon aria-hidden="true" />
          Reset
        </>
      }
      triggerClassName={buttonVariants({ variant: "secondary" })}
      title={`Reset ${branch} to ${parent}?`}
      description={`Every change on ${branch} is lost.`}
      confirmText={branch}
      actionLabel="Reset branch"
      agentConfirm={`Throws away every change on ${branch} and copies ${parent} again`}
      onConfirm={onReset}
    />
  )
}

export function DeleteBranch({ branch, onDelete }: { branch: string; onDelete: () => Promise<unknown> }) {
  return (
    <TypedConfirm
      trigger={
        <>
          <Trash2Icon aria-hidden="true" />
          Delete
        </>
      }
      title={`Delete ${branch}?`}
      description="Its data is gone for good."
      confirmText={branch}
      actionLabel="Delete branch"
      agentConfirm={`Deletes the ${branch} branch and its data`}
      onConfirm={onDelete}
    />
  )
}

const CHANGE = {
  added: { icon: PlusIcon, tone: "success", label: "Added" },
  removed: { icon: MinusIcon, tone: "destructive", label: "Removed" },
  changed: { icon: PencilIcon, tone: "warning", label: "Changed" },
} as const

export function DiffView({ diff }: { diff: Diff }) {
  if (diff.changes.length === 0) {
    return <Empty title="Same schema" description={`${diff.branch} matches ${diff.against}.`} />
  }
  return (
    <div className="flex flex-col gap-6">
      <ul aria-label="Schema changes" className="flex flex-col divide-y rounded-lg border">
        {diff.changes.map((c) => (
          <ChangeRow key={`${c.kind}:${c.name}`} change={c} diff={diff} />
        ))}
      </ul>
      <Collapsible className="flex flex-col gap-3">
        <CollapsibleTrigger className={buttonVariants({ variant: "ghost", className: "self-start" })}>Full schema on both sides</CollapsibleTrigger>
        <CollapsiblePanel>
          <div className="grid gap-4 lg:grid-cols-2">
            <DdlSide title={diff.branch} ddl={diff.branch_ddl} />
            <DdlSide title={diff.against} ddl={diff.against_ddl} />
          </div>
        </CollapsiblePanel>
      </Collapsible>
    </div>
  )
}

function ChangeRow({ change: c, diff }: { change: Change; diff: Diff }) {
  const meta = CHANGE[c.change as keyof typeof CHANGE] ?? CHANGE.changed
  const Icon = meta.icon
  const cols = c.columns
  return (
    <li className="flex flex-col gap-2 px-4 py-3">
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
        <StatusText tone={meta.tone} icon={<Icon aria-hidden="true" />} className="w-20 shrink-0">
          {meta.label}
        </StatusText>
        <span className="text-[13px] text-muted-foreground">{c.kind}</span>
        <span className="min-w-0 font-mono text-[13px] font-[510] [overflow-wrap:anywhere]">{c.name}</span>
      </div>
      {cols && (cols.added.length > 0 || cols.removed.length > 0 || cols.changed.length > 0) && (
        <ul aria-label={`Columns of ${c.name}`} className="flex flex-col gap-1 pl-[92px] font-mono text-xs max-sm:pl-0">
          {cols.added.map((col) => (
            <li key={`a:${col.name}`} className="text-success [overflow-wrap:anywhere]">
              + {columnText(col)}
            </li>
          ))}
          {cols.removed.map((col) => (
            <li key={`r:${col.name}`} className="text-destructive [overflow-wrap:anywhere]">
              - {columnText(col)}
            </li>
          ))}
          {cols.changed.map((col) => (
            <li key={`c:${col.name}`} className="text-warning [overflow-wrap:anywhere]">
              ~ {columnText(col.before)} → {columnText(col.after)}
            </li>
          ))}
        </ul>
      )}
      <Collapsible>
        <CollapsibleTrigger className="ml-[92px] text-xs text-muted-foreground transition-colors duration-100 hover:text-foreground max-sm:ml-0" aria-label={`DDL for ${c.name}`}>
          DDL
        </CollapsibleTrigger>
        <CollapsiblePanel>
          <div className="grid gap-3 pt-2 lg:grid-cols-2">
            {c.before && <DdlSide title={diff.against} ddl={[c.before]} />}
            {c.after && <DdlSide title={diff.branch} ddl={[c.after]} />}
          </div>
        </CollapsiblePanel>
      </Collapsible>
    </li>
  )
}

function columnText(c: ColumnInfo): string {
  const parts = [c.name, c.type || "ANY"]
  if (c.pk) parts.push("PRIMARY KEY")
  if (c.not_null) parts.push("NOT NULL")
  if (c.default !== null) parts.push(`DEFAULT ${c.default}`)
  return parts.join(" ")
}

function DdlSide({ title, ddl }: { title: string; ddl: string[] }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <span className="truncate font-mono text-xs text-muted-foreground">{title}</span>
      {ddl.length === 0 ? (
        <p className="text-xs text-faint">No tables</p>
      ) : (
        <CodeBlock code={ddl.map((s) => (s.trimEnd().endsWith(";") ? s : `${s};`)).join("\n\n")} copyLabel={`Copy ${title} DDL`} />
      )}
    </div>
  )
}
