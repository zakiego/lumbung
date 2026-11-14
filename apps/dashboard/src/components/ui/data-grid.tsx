"use client"

// A data grid built on ui-sdk's grid focus: one tab stop, arrow keys between
// cells, Enter opens a cell, Space selects its row. The header row is row 0
// and sorts on Enter or a click; with selection on, column 0 holds the row
// checkboxes. ui-sdk ships no grid, so this is the primitive for one.
import { type ChangeDetails, byCell, createChange, createGridFocus, definePrimitive, selects } from "@ui-sdk/core"
import { createParts } from "@ui-sdk/react/primitive"

export type GridSort = { column: string; dir: "asc" | "desc" } | null

// "select-all" isn't in the shared vocabulary.
type SelectReason = "item-press" | "list-navigation" | "select-all" | "imperative"

export type DataGridOptions = {
  columns?: string[]
  rowIds?: string[]
  /** What an agent sees for each row, in rowIds order. */
  rowLabels?: string[]
  selectable?: boolean
  sortable?: boolean
  selected?: string[]
  defaultSelected?: string[]
  onSelectedChange?: (selected: string[], details: ChangeDetails<SelectReason>) => void
  sort?: GridSort
  defaultSort?: GridSort
  onSortChange?: (sort: GridSort, details: ChangeDetails<"item-press" | "imperative">) => void
  onOpenCell?: (rowId: string, column: string) => void
}

type Cell = { row: number; col: number }

const MOVE_KEYS = ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Home", "End", "mod+Home", "mod+End", "PageUp", "PageDown"]

export const dataGrid = definePrimitive({
  name: "data-grid",
  options: {
    columns: "list",
    rowIds: "list",
    rowLabels: "list",
    selectable: "boolean",
    sortable: "boolean",
    selected: "list",
    defaultSelected: "list",
    onSelectedChange: "callback",
    sort: "value",
    defaultSort: "value",
    onSortChange: "callback",
    onOpenCell: "callback",
  },
  controlled: ["selected", "sort"],
  state: (o: DataGridOptions) => ({
    active: null as Cell | null,
    columns: o.columns ?? [],
    rowIds: o.rowIds ?? [],
    selectable: !!o.selectable,
    sortable: !!o.sortable,
    selected: o.selected ?? o.defaultSelected ?? ([] as string[]),
    sort: (o.sort ?? o.defaultSort ?? null) as GridSort,
  }),
  sync: (o) => ({
    columns: o.columns ?? [],
    rowIds: o.rowIds ?? [],
    selectable: !!o.selectable,
    sortable: !!o.sortable,
    ...(o.selected === undefined ? {} : { selected: o.selected }),
    ...(o.sort === undefined ? {} : { sort: o.sort }),
  }),
  api: (context) => {
    const change = createChange(context)
    const s = () => context.store.get()
    const offset = () => (s().selectable ? 1 : 0)
    const grid = createGridFocus(context, {
      key: "active",
      rows: () => s().rowIds.length + 1,
      cols: () => s().columns.length + offset(),
      pageSize: () => 10,
    })

    const toggleRow = (rowId: string, event?: Event) => {
      const { selectable, selected, rowIds } = s()
      if (!selectable || !rowIds.includes(rowId)) return false
      const next = selected.includes(rowId) ? selected.filter((id) => id !== rowId) : [...selected, rowId]
      return change("selected", next, { reason: event ? "item-press" : "imperative", event })
    }
    const toggleAll = (event?: Event) => {
      const { selectable, selected, rowIds } = s()
      if (!selectable) return false
      const all = rowIds.length > 0 && rowIds.every((id) => selected.includes(id))
      return change("selected", all ? [] : [...rowIds], { reason: "select-all", event })
    }
    const sortBy = (column: string, dir?: "asc" | "desc", event?: Event) => {
      const { sortable, sort, columns } = s()
      if (!sortable || !columns.includes(column)) return false
      const next = dir ?? (sort?.column === column && sort.dir === "asc" ? "desc" : "asc")
      return change("sort", { column, dir: next }, { reason: event ? "item-press" : "imperative", event })
    }
    const openCell = (rowId: string, column: string) => {
      if (!s().rowIds.includes(rowId) || !s().columns.includes(column)) return false
      context.options().onOpenCell?.(rowId, column)
      return true
    }
    /** What a press, Enter or Space does on a cell. */
    const activate = (cell: Cell, how: "press" | "enter" | "space", event?: Event) => {
      const col = cell.col - offset()
      if (cell.row === 0) {
        if (col < 0) return toggleAll(event)
        return sortBy(s().columns[col]!, undefined, event)
      }
      const rowId = s().rowIds[cell.row - 1]
      if (rowId === undefined) return false
      if (col < 0 || how === "space") return toggleRow(rowId, event)
      if (how === "enter") return openCell(rowId, s().columns[col]!)
      return false
    }
    const keydown = (event: KeyboardEvent, cell: Cell) => {
      if (grid.keydown(event)) return
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault()
        activate(cell, event.key === "Enter" ? "enter" : "space", event)
      }
    }
    const items = () => s().rowIds.map((value, i) => ({ value, label: context.options().rowLabels?.[i] ?? value }))
    return { grid, toggleRow, toggleAll, sortBy, openCell, activate, keydown, items }
  },
  slots: (slot) => ({
    root: slot({
      role: "grid",
      keys: { none: "Focus stays on a cell, whose keydown handles them." },
      attrs: (state) => ({
        "aria-rowcount": String(state.rowIds.length + 1),
        "aria-colcount": String(state.columns.length + (state.selectable ? 1 : 0)),
        "aria-multiselectable": state.selectable ? "true" : undefined,
      }),
    }),
    row: slot<{ id?: string; rowId?: string }>({
      props: { id: "id", rowId: "string" },
      role: "row",
      selects: selects({ selected: ({ props }) => (props.rowId ? [props.rowId] : []), active: [], sort: [] }),
      attrs: (state, { props }) => {
        const on = !!props.rowId && state.selected.includes(props.rowId)
        return {
          "aria-selected": state.selectable && props.rowId ? String(on) : undefined,
          "data-selected": on ? "" : undefined,
        }
      },
    }),
    head: slot<Cell & { id?: string }>({
      props: { id: "id", row: "number", col: "number" },
      role: "columnheader",
      keys: [...MOVE_KEYS, "Enter", " "],
      selects: selects({ active: byCell }),
      attrs: (state, { props, api }) => {
        const col = props.col - (state.selectable ? 1 : 0)
        const name = state.columns[col]
        const sorted = col >= 0 && state.sortable && state.sort?.column === name ? state.sort.dir : null
        const all = state.rowIds.length > 0 && state.rowIds.every((id) => state.selected.includes(id))
        const some = !all && state.rowIds.some((id) => state.selected.includes(id))
        return {
          tabindex: api.grid.tabIndex(state, props),
          "aria-sort": state.sortable && col >= 0 ? (sorted === "asc" ? "ascending" : sorted === "desc" ? "descending" : "none") : undefined,
          "data-sorted": sorted ?? undefined,
          "data-sortable": state.sortable && col >= 0 ? "" : undefined,
          "data-select": col < 0 ? "" : undefined,
          "data-checked": col < 0 && all ? "" : undefined,
          "data-indeterminate": col < 0 && some ? "" : undefined,
          "data-active": api.grid.isActive(state, props) ? "" : undefined,
        }
      },
      on: {
        keydown: (event, { api, props }) => api.keydown(event as KeyboardEvent, props),
        pointerdown: (event, { api, props }) => api.grid.focus(props, { reason: "item-press", event }),
        click: (event, { api, props }) => api.activate(props, "press", event),
      },
      bind: (el, { api, props }) => api.grid.bind(el, props),
    }),
    cell: slot<Cell & { id?: string }>({
      props: { id: "id", row: "number", col: "number" },
      role: "gridcell",
      focus: "roving",
      keys: [...MOVE_KEYS, "Enter", " "],
      selects: selects({ active: byCell, sort: [], selected: [] }),
      attrs: (state, { props, api }) => ({
        tabindex: api.grid.tabIndex(state, props),
        "data-select": props.col < (state.selectable ? 1 : 0) ? "" : undefined,
        "data-active": api.grid.isActive(state, props) ? "" : undefined,
      }),
      on: {
        keydown: (event, { api, props }) => api.keydown(event as KeyboardEvent, props),
        pointerdown: (event, { api, props }) => api.grid.focus(props, { reason: "item-press", event }),
        click: (event, { api, props }) => api.activate(props, "press", event),
        dblclick: (event, { api, props }) => api.activate(props, "enter", event),
      },
      bind: (el, { api, props }) => api.grid.bind(el, props),
    }),
  }),
  agent: { actions: ["sortBy", "toggleRow", "toggleAll", "openCell"], items: (api) => api.items() },
})

export const DataGridParts = createParts(dataGrid, { root: "table", row: "tr", head: "th", cell: "td" })
