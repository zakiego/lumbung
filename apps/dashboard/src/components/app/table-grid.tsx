import {
  ScrollAreaContent,
  ScrollArea as ScrollAreaPart,
  ScrollAreaCorner,
  ScrollAreaScrollbar,
  ScrollAreaThumb,
  ScrollAreaViewport,
} from "@ui-sdk/react/scroll-area"
import { ArrowDownIcon, ArrowUpIcon, CheckIcon, MinusIcon } from "lucide-react"
import { type ReactNode, useId, useMemo } from "react"
import { DataGridParts, type GridSort } from "@/components/ui/data-grid"
import { type Cell, affinity, blobSizeText, isNumeric } from "@/lib/cells"
import { number } from "@/lib/format"
import { cn } from "@/lib/utils"

const { Root, Row, Head, Cell: GridCell } = DataGridParts

export interface GridColumnView {
  name: string
  /** The declared type, shown under the name. */
  type?: string
}

export interface GridRowView {
  id: string
  cells: Cell[]
}

type TableGridProps = {
  label: string
  columns: GridColumnView[]
  rows: GridRowView[]
  sort?: GridSort
  onSortChange?: (sort: GridSort) => void
  selected?: string[]
  onSelectedChange?: (ids: string[]) => void
  onOpenCell?: (rowId: string, column: number) => void
  /** Shown under the header when there are no rows. */
  empty?: ReactNode
  busy?: boolean
  className?: string
}

/** Column ids for the grid: the name, or the name and position when a result repeats it. */
function columnIds(columns: GridColumnView[]): string[] {
  const seen = new Map<string, number>()
  for (const c of columns) seen.set(c.name, (seen.get(c.name) ?? 0) + 1)
  return columns.map((c, i) => (seen.get(c.name)! > 1 ? `${c.name} (${i + 1})` : c.name))
}

const bar =
  "flex touch-none p-0.5 opacity-0 transition-opacity duration-150 select-none data-visible:opacity-100 data-[orientation=horizontal]:h-2.5 data-[orientation=horizontal]:flex-col data-[orientation=vertical]:w-2.5"

export function CellValue({ cell }: { cell: Cell }) {
  switch (cell.t) {
    case "null":
      return <span className="text-[11px] tracking-wide text-muted-foreground">NULL</span>
    case "blob":
      return (
        <span className="text-muted-foreground">
          <span className="text-[11px] tracking-wide text-muted-foreground">BLOB</span> {blobSizeText(cell.size)}
        </span>
      )
    case "text":
      return <span dir="auto">{cell.v}</span>
    default:
      return <>{cell.v}</>
  }
}

function SortIcon({ dir }: { dir: "asc" | "desc" | undefined }) {
  if (!dir) return null
  const Icon = dir === "asc" ? ArrowUpIcon : ArrowDownIcon
  return <Icon aria-hidden="true" className="size-3.5 shrink-0 text-foreground" />
}

const box =
  "inline-flex size-4 items-center justify-center rounded-[4px] border border-input-strong text-white transition-[background-color,border-color] duration-100 [&_svg]:size-3"

export function TableGrid({ label, columns, rows, sort = null, onSortChange, selected, onSelectedChange, onOpenCell, empty, busy, className }: TableGridProps) {
  const uid = useId()
  const ids = useMemo(() => columnIds(columns), [columns])
  const rowIds = useMemo(() => rows.map((r) => r.id), [rows])
  const rowLabels = useMemo(() => rows.map((r) => r.cells.slice(0, 3).map((c) => (c.t === "null" ? "NULL" : c.t === "blob" ? "blob" : c.v.slice(0, 40))).join(", ")), [rows])
  const numeric = useMemo(() => columns.map((c) => (c.type ? ["integer", "real"].includes(affinity(c.type)) : false)), [columns])
  const selectable = !!onSelectedChange
  const off = selectable ? 1 : 0

  return (
    <ScrollAreaPart type="hover" className={cn("relative overflow-hidden rounded-lg border bg-background", className)}>
      <ScrollAreaViewport role="region" aria-label={label} className="max-h-[min(70svh,calc(100svh-16rem))] min-h-0 overscroll-contain [scrollbar-width:none]">
        <ScrollAreaContent className="w-max min-w-full">
          <Root
            aria-label={label}
            aria-busy={busy || undefined}
            columns={ids}
            rowIds={rowIds}
            rowLabels={rowLabels}
            selectable={selectable}
            sortable={!!onSortChange}
            selected={selectable ? (selected ?? []) : undefined}
            onSelectedChange={(next) => onSelectedChange?.(next)}
            sort={onSortChange ? sort : undefined}
            onSortChange={(next) => onSortChange?.(next)}
            onOpenCell={(rowId, column) => onOpenCell?.(rowId, ids.indexOf(column))}
            className={cn("w-full border-separate border-spacing-0 text-[13px] transition-opacity duration-150", busy && "opacity-60")}
          >
            <thead>
              <Row className="group/row">
                {selectable && (
                  <Head
                    row={0}
                    col={0}
                    id={`${uid}h0`}
                    aria-label="Select all rows"
                    className="group/head sticky top-0 left-0 z-30 w-10 border-b bg-background px-3 outline-none focus-visible:outline-1 focus-visible:-outline-offset-1 focus-visible:outline-ring"
                  >
                    <span
                      aria-hidden="true"
                      className={cn(box, "group-data-checked/head:border-brand group-data-checked/head:bg-brand group-data-indeterminate/head:border-brand group-data-indeterminate/head:bg-brand")}
                    >
                      <CheckIcon className="hidden group-data-checked/head:block" />
                      <MinusIcon className="hidden group-data-indeterminate/head:block" />
                    </span>
                  </Head>
                )}
                {columns.map((c, i) => (
                  <Head
                    key={ids[i]}
                    row={0}
                    col={i + off}
                    id={`${uid}h${i + off}`}
                    title={c.type ? `${c.name} · ${c.type}` : c.name}
                    className={cn(
                      "sticky top-0 z-20 h-9 border-b bg-background px-3 text-left align-middle text-xs font-[510] whitespace-nowrap text-muted-foreground outline-none select-none focus-visible:outline-1 focus-visible:-outline-offset-1 focus-visible:outline-ring data-sortable:hover:text-foreground",
                      numeric[i] && "text-right",
                    )}
                  >
                    <span className={cn("inline-flex max-w-64 items-center gap-1.5", numeric[i] && "flex-row-reverse")}>
                      <span className="truncate">{c.name}</span>
                      {c.type && <span className="shrink-0 font-mono text-[10.5px] font-normal text-muted-foreground uppercase">{c.type.length > 16 ? `${c.type.slice(0, 15)}…` : c.type}</span>}
                      <SortIcon dir={sort?.column === ids[i] ? sort.dir : undefined} />
                    </span>
                  </Head>
                ))}
              </Row>
            </thead>
            <tbody>
              {rows.map((r, ri) => (
                <Row
                  key={r.id}
                  rowId={r.id}
                  className="group/row bg-background transition-colors duration-150 hover:bg-accent hover:duration-0 data-selected:bg-[color-mix(in_oklab,var(--brand)_12%,var(--background))]"
                >
                  {selectable && (
                    <GridCell
                      row={ri + 1}
                      col={0}
                      id={`${uid}r${ri}c0`}
                      aria-label="Select row"
                      className="sticky left-0 z-10 w-10 border-b bg-inherit px-3 outline-none focus-visible:outline-1 focus-visible:-outline-offset-1 focus-visible:outline-ring"
                    >
                      <span aria-hidden="true" className={cn(box, "group-data-selected/row:border-brand group-data-selected/row:bg-brand")}>
                        <CheckIcon className="hidden group-data-selected/row:block" />
                      </span>
                    </GridCell>
                  )}
                  {r.cells.map((cell, ci) => (
                    <GridCell
                      key={ids[ci]}
                      row={ri + 1}
                      col={ci + off}
                      id={`${uid}r${ri}c${ci + off}`}
                      className={cn(
                        "h-9 border-b px-3 align-middle font-mono text-[12px] whitespace-nowrap outline-none focus-visible:outline-1 focus-visible:-outline-offset-1 focus-visible:outline-ring",
                        isNumeric(cell) && "text-right tabular-nums",
                      )}
                    >
                      <div className="max-w-80 truncate">
                        <CellValue cell={cell} />
                      </div>
                    </GridCell>
                  ))}
                </Row>
              ))}
            </tbody>
          </Root>
          {rows.length === 0 && empty && <div className="flex min-h-40 items-center justify-center px-6 py-10 text-[13px] text-muted-foreground">{empty}</div>}
        </ScrollAreaContent>
      </ScrollAreaViewport>
      <ScrollAreaScrollbar orientation="vertical" className={cn(bar, "absolute top-9 right-0 bottom-(--scroll-area-corner-height)")}>
        <ScrollAreaThumb className="flex-1 rounded-full bg-foreground/25" />
      </ScrollAreaScrollbar>
      <ScrollAreaScrollbar orientation="horizontal" className={cn(bar, "absolute right-(--scroll-area-corner-width) bottom-0 left-0")}>
        <ScrollAreaThumb className="flex-1 rounded-full bg-foreground/25" />
      </ScrollAreaScrollbar>
      <ScrollAreaCorner />
    </ScrollAreaPart>
  )
}

export function rowCountLabel(n: number): string {
  return `${number.format(n)} ${n === 1 ? "row" : "rows"}`
}
