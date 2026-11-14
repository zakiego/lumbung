import { EyeIcon, FilterIcon, LockIcon, PlusIcon, TableIcon, Trash2Icon, XIcon } from "lucide-react"
import { type FormEvent, type ReactNode, useEffect, useMemo, useState } from "react"
import { TypedConfirm } from "@/components/ui/alert-dialog"
import { Button, buttonVariants } from "@/components/ui/button"
import { Switch } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogFooter, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Field, FieldError, FieldLabel, Label } from "@/components/ui/field"
import { Input, Textarea } from "@/components/ui/input"
import { Pager } from "@/components/ui/pager"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { SelectEmpty, SelectGroupLabel, SelectList, SelectSearch } from "@/components/ui/select-search"
import { Empty, StatusText } from "@/components/ui/surface"
import { Spinner } from "@/components/ui/spinner"
import { toast } from "@/components/ui/toast"
import { Tooltip } from "@/components/ui/tooltip"
import type { BrowseResult, FullCell, GridObject } from "@/fns/data"
import { type Cell, type RowKey, blobSizeText, keyId } from "@/lib/cells"
import { number } from "@/lib/format"
import type { GridSort } from "@/components/ui/data-grid"
import { FILTER_OPS, FILTER_VALUE_MAX, type FilterOp, type GridFilter } from "@/server/grid"
import { TableGrid } from "./table-grid"

export interface BrowseQuery {
  table?: string
  page: number
  sort: GridSort
  filters: GridFilter[]
}

export interface DataActions {
  loadCell: (key: RowKey, column: string) => Promise<FullCell>
  updateCell: (key: RowKey, column: string, value: string | null) => Promise<unknown>
  insertRow: (values: Record<string, string | null>) => Promise<unknown>
  deleteRows: (keys: RowKey[]) => Promise<unknown>
}

type BrowserProps = {
  result: BrowseResult
  query: BrowseQuery
  onQueryChange: (next: Partial<BrowseQuery>) => void
  actions: DataActions
  busy?: boolean
  /** The next step when there are no tables, like a link to the SQL tab. */
  emptyAction?: ReactNode
}

const OP_LABEL: Record<FilterOp, string> = {
  "=": "=",
  "!=": "≠",
  "<": "<",
  "<=": "≤",
  ">": ">",
  ">=": "≥",
  contains: "contains",
  "is null": "is null",
  "is not null": "is not null",
}

const needsValue = (op: FilterOp) => op !== "is null" && op !== "is not null"

export function DataBrowser({ result, query, onQueryChange, actions, busy, emptyAction }: BrowserProps) {
  const { table, rows, total, page, pageSize, objects } = result
  const [selected, setSelected] = useState<string[]>([])
  const [open, setOpen] = useState<{ row: string; col: number } | null>(null)

  const keyed = useMemo(() => rows.map((r, i) => ({ id: r.key ? keyId(r.key) : `${page}:${i}`, ...r })), [rows, page])
  const byId = useMemo(() => new Map(keyed.map((r) => [r.id, r])), [keyed])
  useEffect(() => setSelected([]), [keyed])

  if (!objects.length)
    return (
      <Empty icon={<TableIcon />} title="No tables yet">
        {emptyAction}
      </Empty>
    )

  const pageCount = Math.max(1, Math.ceil(total / pageSize))
  const editable = !!table?.editable
  const openRow = open ? byId.get(open.row) : undefined
  const openColumn = open && table ? table.columns[open.col] : undefined
  const from = total ? (page - 1) * pageSize + 1 : 0
  const to = Math.min(total, page * pageSize)

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <TablePicker objects={objects} value={table?.name ?? query.table ?? ""} onChange={(name) => onQueryChange({ table: name, page: 1, sort: null, filters: [] })} />
        {table && <FilterBar columns={table.columns.map((c) => c.name)} filters={query.filters} onChange={(filters) => onQueryChange({ filters, page: 1 })} />}
        <div className="ml-auto flex items-center gap-2">
          {table?.readOnly ? (
            <Tooltip content={table.readOnly}>
              <span tabIndex={0} aria-label={`Read-only. ${table.readOnly}`} className="inline-flex rounded-sm">
                <StatusText icon={<LockIcon aria-hidden="true" />}>Read-only</StatusText>
              </span>
            </Tooltip>
          ) : (
            table && !editable && <StatusText icon={<LockIcon aria-hidden="true" />}>Read-only</StatusText>
          )}
          {editable && selected.length > 0 && (
            <TypedConfirm
              trigger={
                <>
                  <Trash2Icon aria-hidden="true" />
                  Delete {selected.length === 1 ? "row" : `${number.format(selected.length)} rows`}
                </>
              }
              triggerClassName={buttonVariants({ variant: "secondary", className: "text-destructive" })}
              title={`Delete ${selected.length === 1 ? "1 row" : `${number.format(selected.length)} rows`}?`}
              confirmText="delete"
              actionLabel="Delete forever"
              agentConfirm={`Deletes ${selected.length} rows from ${table!.name}`}
              onConfirm={async () => {
                const keys = selected.map((id) => byId.get(id)?.key).filter((k): k is RowKey => !!k)
                await actions.deleteRows(keys)
                setSelected([])
                toast.success(keys.length === 1 ? "Deleted 1 row" : `Deleted ${number.format(keys.length)} rows`)
              }}
            />
          )}
          {editable && table && <InsertRowDialog table={table} onInsert={actions.insertRow} />}
        </div>
      </div>

      {result.missing ? (
        <Empty icon={<TableIcon />} title="This table doesn't exist on this branch" />
      ) : result.error ? (
        <Empty icon={<TableIcon />} title="Couldn't load rows" description={result.error} />
      ) : (
        table && (
          <TableGrid
            label={table.name}
            columns={table.columns.map((c) => ({ name: c.name, type: c.type }))}
            rows={keyed}
            busy={busy}
            sort={query.sort}
            onSortChange={(sort) => onQueryChange({ sort, page: 1 })}
            selected={editable ? selected : undefined}
            onSelectedChange={editable ? setSelected : undefined}
            onOpenCell={(row, col) => setOpen({ row, col })}
            empty={query.filters.length ? "No rows match" : "No rows"}
          />
        )
      )}

      {table && !result.error && total > 0 && (
        <div className="flex items-center justify-end gap-3">
          <p className="text-[13px] text-muted-foreground tabular-nums">
            {from === to ? number.format(from) : `${number.format(from)}–${number.format(to)}`} of {number.format(total)}
          </p>
          {pageCount > 1 && <Pager label="Rows pages" page={page} pageCount={pageCount} onPageChange={(p) => onQueryChange({ page: p })} />}
        </div>
      )}

      {table && openRow && openColumn && (
        <CellDialog
          key={`${open!.row}:${open!.col}`}
          column={openColumn.name}
          type={openColumn.type}
          cell={openRow.cells[open!.col]!}
          editable={editable && !openColumn.generated}
          load={openRow.key ? () => actions.loadCell(openRow.key!, openColumn.name) : undefined}
          onSave={(value) => actions.updateCell(openRow.key!, openColumn.name, value)}
          onClose={() => setOpen(null)}
        />
      )}
    </div>
  )
}

export function TablePicker({ objects, value, onChange }: { objects: GridObject[]; value: string; onChange: (name: string) => void }) {
  const tables = objects.filter((o) => o.kind === "table")
  const views = objects.filter((o) => o.kind === "view")
  const [current, setCurrent] = useState(value)
  useEffect(() => setCurrent(value), [value])
  const group = (label: string, list: GridObject[]) =>
    list.length > 0 && (
      <SelectGroup>
        {tables.length > 0 && views.length > 0 && <SelectGroupLabel>{label}</SelectGroupLabel>}
        {list.map((o) => (
          <SelectItem key={`${o.kind}:${o.name}`} value={o.name} label={o.name}>
            {o.kind === "view" ? <EyeIcon aria-hidden="true" /> : <TableIcon aria-hidden="true" />}
            <span className="truncate" title={o.name}>
              {o.name}
            </span>
          </SelectItem>
        ))}
      </SelectGroup>
    )
  return (
    <Select
      value={current || null}
      items={objects.map((o) => o.name)}
      onValueChange={(next) => {
        if (typeof next !== "string") return
        setCurrent(next)
        onChange(next)
      }}
    >
      <SelectTrigger aria-label="Table" className="max-w-[min(20rem,100%)] font-mono">
        <TableIcon aria-hidden="true" className="text-faint" />
        <SelectValue className="truncate" />
      </SelectTrigger>
      <SelectContent className="w-[min(22rem,calc(100vw-32px))] font-mono">
        <SelectSearch aria-label="Search tables" placeholder="Search…" />
        <SelectEmpty>No matches</SelectEmpty>
        <SelectList>
          {group("Tables", tables)}
          {group("Views", views)}
        </SelectList>
      </SelectContent>
    </Select>
  )
}

export function FilterBar({ columns, filters, onChange }: { columns: string[]; filters: GridFilter[]; onChange: (filters: GridFilter[]) => void }) {
  const [open, setOpen] = useState(false)
  const [column, setColumn] = useState<string | null>(columns[0] ?? null)
  const [op, setOp] = useState<FilterOp>("=")
  const [value, setValue] = useState("")

  function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!column) return
    onChange([...filters, needsValue(op) ? { column, op, value } : { column, op }])
    setValue("")
    setOpen(false)
  }

  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
      {filters.map((f, i) => {
        const text = `${f.column} ${OP_LABEL[f.op]}${needsValue(f.op) ? ` ${f.value ?? ""}` : ""}`
        return (
          <span key={`${i}:${text}`} className="inline-flex h-7 max-w-64 items-center gap-1 rounded-md border bg-secondary pr-0.5 pl-2 font-mono text-xs">
            <span className="truncate" title={text}>
              {f.column} <span className="text-muted-foreground">{OP_LABEL[f.op]}</span> {needsValue(f.op) && (f.value === "" ? <span className="text-muted-foreground">''</span> : f.value)}
            </span>
            <Button variant="ghost" size="icon-sm" className="size-6" aria-label={`Remove filter ${text}`} onClick={() => onChange(filters.filter((_, j) => j !== i))}>
              <XIcon className="size-3.5" />
            </Button>
          </span>
        )
      })}
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger className={buttonVariants({ variant: "ghost" })}>
          <FilterIcon aria-hidden="true" />
          Filter
        </PopoverTrigger>
        <PopoverContent align="start" className="w-80" aria-label="Add a filter">
          <form onSubmit={add} className="flex flex-col gap-3">
            <Field>
              <FieldLabel>Column</FieldLabel>
              <Select value={column} items={columns} onValueChange={(v) => typeof v === "string" && setColumn(v)}>
                <SelectTrigger className="w-full font-mono" />
                <SelectContent className="font-mono">
                  <SelectSearch aria-label="Search columns" placeholder="Search…" />
                  <SelectEmpty>No matches</SelectEmpty>
                  <SelectList>
                    {columns.map((c) => (
                      <SelectItem key={c} value={c} label={c}>
                        <span className="truncate">{c}</span>
                      </SelectItem>
                    ))}
                  </SelectList>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel>Operator</FieldLabel>
              <Select value={op} items={FILTER_OPS.map((o) => ({ value: o, label: OP_LABEL[o] }))} onValueChange={(v) => v && setOp(v as FilterOp)}>
                <SelectTrigger className="w-full" />
                <SelectContent>
                  {FILTER_OPS.map((o) => (
                    <SelectItem key={o} value={o} label={OP_LABEL[o]}>
                      {OP_LABEL[o]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {needsValue(op) && (
              <Field>
                <FieldLabel>Value</FieldLabel>
                <Input value={value} onValueChange={setValue} maxLength={FILTER_VALUE_MAX} autoComplete="off" spellCheck={false} className="font-mono" />
              </Field>
            )}
            <Button type="submit" disabled={!column}>
              Apply filter
            </Button>
          </form>
        </PopoverContent>
      </Popover>
      {filters.length > 1 && (
        <Button variant="ghost" onClick={() => onChange([])}>
          Clear
        </Button>
      )}
    </div>
  )
}

type CellDialogProps = {
  column: string
  type: string
  cell: Cell
  editable: boolean
  /** Reads the full value, for text the list cut short. */
  load?: () => Promise<FullCell>
  onSave: (value: string | null) => Promise<unknown>
  onClose: () => void
}

export function CellDialog({ column, type, cell, editable, load, onSave, onClose }: CellDialogProps) {
  const cut = cell.t === "text" && cell.cut
  // A cut value that can't be read in full would save as its prefix.
  const canEdit = editable && cell.t !== "blob" && !(cut && !load)
  const [text, setText] = useState(cell.t === "null" || cell.t === "blob" ? "" : cell.v)
  const [isNull, setNull] = useState(cell.t === "null")
  const [loading, setLoading] = useState(cut && !!load)
  const [loadError, setLoadError] = useState("")
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)

  useEffect(() => {
    if (!cut || !load) return
    let live = true
    load().then(
      (full) => {
        if (!live) return
        setText(full.t === "null" ? "" : full.v)
        setNull(full.t === "null")
        setLoading(false)
      },
      (e: unknown) => {
        if (!live) return
        setLoadError(e instanceof Error ? e.message : String(e))
        setLoading(false)
      },
    )
    return () => {
      live = false
    }
    // The dialog is keyed by cell, so the value is read once per open.
  }, [])

  const writable = canEdit && !loading && !loadError

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!writable || pending) return
    setPending(true)
    setError("")
    try {
      await onSave(isNull ? null : text)
      toast.success(`Saved ${column}`)
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog
      defaultOpen
      onOpenChange={(next, details) => {
        if (!next && pending) return details.cancel()
        if (!next) onClose()
      }}
    >
      <DialogContent className="max-w-2xl">
        <DialogTitle className="flex min-w-0 items-baseline gap-2">
          <span className="truncate font-mono" title={column}>
            {column}
          </span>
          {type && <span className="shrink-0 font-mono text-[11px] font-normal text-muted-foreground uppercase">{type}</span>}
        </DialogTitle>
        {cell.t === "blob" ? (
          <div className="flex flex-col gap-2 text-[13px]">
            <p className="tabular-nums">{blobSizeText(cell.size)}</p>
            {cell.hex && <code className="rounded-md border bg-code px-2 py-1.5 font-mono text-xs break-all text-muted-foreground">{cell.hex.replace(/(..)/g, "$1 ").trim()}{(cell.size ?? Number.POSITIVE_INFINITY) > cell.hex.length / 2 && " …"}</code>}
            <p className="text-muted-foreground">Blobs can't be edited here.</p>
          </div>
        ) : (
          <form onSubmit={save} className="flex flex-col gap-3">
            {loading ? (
              <div className="flex h-40 items-center justify-center rounded-md border text-faint" aria-busy="true">
                <Spinner />
              </div>
            ) : canEdit ? (
              <Field invalid={error ? true : undefined}>
                <FieldLabel className="sr-only">Value</FieldLabel>
                <Textarea
                  value={isNull ? "" : text}
                  onValueChange={(v) => {
                    setText(v)
                    setError("")
                  }}
                  readOnly={!writable}
                  disabled={isNull}
                  placeholder={isNull ? "NULL" : undefined}
                  spellCheck={false}
                  className="max-h-[50svh] min-h-40 resize-y font-mono text-base leading-relaxed md:text-[13px]"
                />
                <FieldError match={!!error}>{error}</FieldError>
              </Field>
            ) : isNull ? (
              <p className="font-mono text-[13px] text-muted-foreground">NULL</p>
            ) : (
              <pre
                tabIndex={0}
                aria-label="Value"
                className="max-h-[50svh] overflow-auto rounded-md border bg-code px-2.5 py-1.5 font-mono text-[13px] leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]"
              >
                {text}
              </pre>
            )}
            {loadError && (
              <p role="alert" className="text-xs text-destructive">
                {loadError}
              </p>
            )}
            {cut && !load && cell.t === "text" && (
              <p className="text-xs text-muted-foreground tabular-nums">
                First {number.format([...cell.v].length)}
                {cell.len !== null && ` of ${number.format(cell.len)}`} characters
              </p>
            )}
            {canEdit && (
              <DialogFooter className="items-center justify-between">
                <Label className="inline-flex items-center gap-2">
                  <Switch checked={isNull} onCheckedChange={setNull} disabled={!writable} aria-label="NULL" />
                  <span className="font-mono text-xs">NULL</span>
                </Label>
                <Button type="submit" disabled={!writable || pending}>
                  {pending ? "Saving…" : "Save"}
                </Button>
              </DialogFooter>
            )}
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

type InsertTable = NonNullable<BrowseResult["table"]>

export function InsertRowDialog({ table, onInsert }: { table: InsertTable; onInsert: (values: Record<string, string | null>) => Promise<unknown> }) {
  const columns = table.columns.filter((c) => !c.generated)
  const pk = table.columns.filter((c) => c.pk > 0)
  const alias = pk.length === 1 && pk[0]!.type.toUpperCase() === "INTEGER" ? pk[0]!.name : null
  const [open, setOpen] = useState(false)
  const [values, setValues] = useState<Record<string, string>>({})
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) return
    setPending(true)
    setError("")
    try {
      const filled = Object.fromEntries(Object.entries(values).filter(([, v]) => v !== ""))
      await onInsert(filled)
      toast.success(`Inserted into ${table.name}`)
      setOpen(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next, details) => {
        if (!next && pending) return details.cancel()
        setOpen(next)
        if (next) {
          setValues({})
          setError("")
        }
      }}
    >
      <DialogTrigger className={buttonVariants({ variant: "secondary" })}>
        <PlusIcon aria-hidden="true" />
        Insert row
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogTitle className="truncate">
          Insert into <span className="font-mono">{table.name}</span>
        </DialogTitle>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-3">
            {columns.map((c) => (
              <Field key={c.name}>
                <FieldLabel className="flex min-w-0 items-baseline gap-2">
                  <span className="truncate font-mono" title={c.name}>
                    {c.name}
                  </span>
                  {c.type && <span className="shrink-0 font-mono text-[10.5px] font-normal text-muted-foreground uppercase">{c.type}</span>}
                </FieldLabel>
                <Input
                  value={values[c.name] ?? ""}
                  onValueChange={(v) => setValues((prev) => ({ ...prev, [c.name]: v }))}
                  placeholder={placeholderFor(c, c.name === alias)}
                  autoComplete="off"
                  spellCheck={false}
                  className="font-mono"
                />
              </Field>
            ))}
          </div>
          {error && (
            <p role="alert" className="text-xs text-destructive">
              {error}
            </p>
          )}
          <DialogFooter className="sticky bottom-0 -mx-5 -mb-5 border-t bg-background px-5 py-3">
            <Button type="submit" disabled={pending}>
              {pending ? "Inserting…" : "Insert"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function placeholderFor(c: InsertTable["columns"][number], rowidAlias: boolean): string {
  if (c.default !== null) return c.default
  if (rowidAlias) return "Auto"
  return c.notNull ? "" : "NULL"
}
