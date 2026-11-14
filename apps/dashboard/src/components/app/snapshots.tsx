import type { ScheduleOut, SnapshotOut } from "@lumbung/client"
import { CalendarClockIcon, CameraIcon, ClockIcon, GitBranchPlusIcon, HandIcon, Trash2Icon } from "lucide-react"
import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NumberInput } from "@/components/ui/number-input"
import { Empty, StatusText, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { formatBytes, formatDate, number, timeAgo } from "@/lib/format"
import { Confirm, FormDialog } from "./form-dialog"
import { NAME_PATTERN } from "./name-dialog"

export function snapshotName(s: SnapshotOut): string {
  return s.label || `${s.kind === "scheduled" ? "Scheduled" : "Manual"} ${formatDate(s.created_at)}`
}

export function NewSnapshotDialog({ branch, onCreate }: { branch: string; onCreate: (label: string) => Promise<unknown> }) {
  const [label, setLabel] = useState("")
  return (
    <FormDialog
      trigger={
        <>
          <CameraIcon aria-hidden="true" />
          New snapshot
        </>
      }
      title={`Snapshot ${branch}`}
      submit="Take snapshot"
      pendingSubmit="Taking…"
      onOpen={() => setLabel("")}
      onSubmit={() => onCreate(label.trim()).then(() => undefined)}
    >
      <Field>
        <FieldLabel>Label</FieldLabel>
        <Input value={label} onValueChange={setLabel} maxLength={200} placeholder="before-migration" autoComplete="off" />
      </Field>
    </FormDialog>
  )
}

type Actions = {
  onRestore: (s: SnapshotOut, name: string) => Promise<unknown>
  onDelete: (s: SnapshotOut) => Promise<unknown>
}

export function SnapshotTable({ snapshots, canWrite, ...actions }: { snapshots: SnapshotOut[]; canWrite: boolean } & Actions) {
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Snapshot</TableHead>
          <TableHead className="hidden sm:table-cell">Kind</TableHead>
          <TableHead className="text-right">Size</TableHead>
          <TableHead className="hidden text-right md:table-cell">Taken</TableHead>
          {canWrite && (
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          )}
        </tr>
      </thead>
      <tbody>
        {snapshots.map((s) => {
          const name = snapshotName(s)
          return (
            <TableRow key={s.id}>
              <TableCell className="w-1/2 max-w-0">
                <span className={s.label ? "block truncate font-[510]" : "block truncate text-muted-foreground"} title={name}>
                  {name}
                </span>
              </TableCell>
              <TableCell className="hidden sm:table-cell">
                <StatusText icon={s.kind === "scheduled" ? <ClockIcon aria-hidden="true" /> : <HandIcon aria-hidden="true" />}>
                  {s.kind === "scheduled" ? "Scheduled" : "Manual"}
                </StatusText>
              </TableCell>
              <TableCell className="text-right whitespace-nowrap text-muted-foreground tabular-nums">{formatBytes(s.size_bytes)}</TableCell>
              <TableCell className="hidden text-right whitespace-nowrap text-muted-foreground md:table-cell">
                <span title={formatDate(s.created_at)} suppressHydrationWarning>
                  {timeAgo(s.created_at)}
                </span>
              </TableCell>
              {canWrite && (
                <TableCell className="w-px">
                  <div className="flex justify-end gap-1">
                    <RestoreDialog snapshot={s} name={name} onRestore={(branch) => actions.onRestore(s, branch)} />
                    <Confirm
                      trigger={<Trash2Icon aria-hidden="true" />}
                      triggerStyle={{ variant: "ghost", size: "icon-sm", label: `Delete ${name}` }}
                      title={`Delete ${name}?`}
                      description="Branches restored from it stay."
                      actionLabel="Delete snapshot"
                      agentConfirm={`Deletes the snapshot ${name}`}
                      onConfirm={() => actions.onDelete(s)}
                    />
                  </div>
                </TableCell>
              )}
            </TableRow>
          )
        })}
      </tbody>
    </Table>
  )
}

function RestoreDialog({ snapshot, name, onRestore }: { snapshot: SnapshotOut; name: string; onRestore: (branch: string) => Promise<unknown> }) {
  const [branch, setBranch] = useState("")
  return (
    <FormDialog
      trigger={
        <>
          <GitBranchPlusIcon aria-hidden="true" />
          Restore
        </>
      }
      triggerStyle={{ variant: "secondary", size: "sm", label: `Restore ${name}` }}
      title={`Restore ${name}`}
      description="It becomes a new branch. Nothing is overwritten."
      submit="Create branch"
      pendingSubmit="Restoring…"
      onOpen={() => setBranch(`${snapshot.branch_name}-restore`.slice(0, 63))}
      onSubmit={() => onRestore(branch).then(() => undefined)}
    >
      <Field>
        <FieldLabel>New branch name</FieldLabel>
        <Input value={branch} onValueChange={setBranch} required pattern={NAME_PATTERN} autoComplete="off" spellCheck={false} className="font-mono" />
        <FieldError match="patternMismatch">Use lowercase letters, digits and dashes.</FieldError>
      </Field>
    </FormDialog>
  )
}

export type ScheduleInput = { everyMinutes: number; keepCount?: number; keepDays?: number }

export function scheduleText(s: ScheduleOut): string {
  const m = s.every_minutes
  const unit = (n: number, one: string, many: string) => (n === 1 ? one : `${number.format(n)} ${many}`)
  const every = m % 1440 === 0 ? unit(m / 1440, "day", "days") : m % 60 === 0 ? unit(m / 60, "hour", "hours") : unit(m, "minute", "min")
  const days = s.keep_days === 1 ? "1 day" : `${number.format(s.keep_days)} days`
  const keep = [s.keep_count ? `last ${number.format(s.keep_count)}` : "", s.keep_days ? days : ""].filter(Boolean)
  return `Every ${every}${keep.length ? `, keeps ${keep.join(" and ")}` : ""}`
}

export function ScheduleCard({
  schedule,
  canWrite,
  onSave,
  onRemove,
}: {
  schedule: ScheduleOut | null
  canWrite: boolean
  onSave: (s: ScheduleInput) => Promise<unknown>
  onRemove: () => Promise<unknown>
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-lg border px-4 py-3">
      <CalendarClockIcon aria-hidden="true" className="size-4 shrink-0 text-faint" />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-[13px] font-[510]">{schedule ? scheduleText(schedule) : "No schedule"}</span>
        {schedule && (
          <span className="text-xs text-muted-foreground" suppressHydrationWarning>
            {schedule.last_run_at ? `Last run ${timeAgo(schedule.last_run_at)}` : "Hasn't run yet"}
          </span>
        )}
      </div>
      {canWrite && (
        <div className="flex gap-2">
          {schedule && (
            <Button variant="ghost" onClick={() => onRemove().catch(() => {})}>
              Remove
            </Button>
          )}
          <ScheduleDialog schedule={schedule} onSave={onSave} />
        </div>
      )}
    </div>
  )
}

function ScheduleDialog({ schedule, onSave }: { schedule: ScheduleOut | null; onSave: (s: ScheduleInput) => Promise<unknown> }) {
  const [every, setEvery] = useState<number | null>(null)
  const [count, setCount] = useState<number | null>(null)
  const [days, setDays] = useState<number | null>(null)
  return (
    <FormDialog
      trigger={schedule ? "Edit schedule" : "Set schedule"}
      triggerStyle={{ variant: "secondary" }}
      title="Snapshot schedule"
      submit="Save schedule"
      pendingSubmit="Saving…"
      onOpen={() => {
        setEvery(schedule?.every_minutes ?? 1440)
        setCount(schedule?.keep_count || null)
        setDays(schedule?.keep_days || null)
      }}
      onSubmit={() => {
        if (!every) return Promise.reject(new Error("Set how often it runs."))
        return onSave({ everyMinutes: every, keepCount: count ?? undefined, keepDays: days ?? undefined }).then(() => undefined)
      }}
    >
      <Field>
        <FieldLabel>Every (minutes)</FieldLabel>
        <NumberInput value={every} onValueChange={setEvery} min={1} max={525600} required />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field>
          <FieldLabel>Keep last</FieldLabel>
          <NumberInput value={count} onValueChange={setCount} min={0} max={10000} placeholder="Any" />
        </Field>
        <Field>
          <FieldLabel>Keep days</FieldLabel>
          <NumberInput value={days} onValueChange={setDays} min={0} max={3650} placeholder="Any" />
        </Field>
      </div>
    </FormDialog>
  )
}

export function NoSnapshots() {
  return <Empty icon={<CameraIcon />} title="No snapshots yet" />
}
