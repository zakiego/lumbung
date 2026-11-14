import { createFileRoute, useRouter, useRouterState } from "@tanstack/react-router"
import { z } from "zod"
import { type BrowseQuery, type DataActions, DataBrowser } from "@/components/app/data-browser"
import { DbFrame, OpenSql } from "@/components/app/db-frame"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { browseTable, deleteRows, getCell, insertRow, updateCell } from "@/fns/data"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { FILTER_OPS, FILTER_VALUE_MAX } from "@/server/grid"
import { useWs } from "@/components/app/workspace"

// A bad value in a shared link drops that one param instead of failing the page.
const search = z.object({
  table: z.string().min(1).max(1000).optional().catch(undefined),
  page: z.number().int().min(1).max(10_000_000).optional().catch(undefined),
  sort: z.string().max(1000).optional().catch(undefined),
  dir: z.enum(["asc", "desc"]).optional().catch(undefined),
  where: z
    .array(z.object({ column: z.string().max(1000), op: z.enum(FILTER_OPS), value: z.string().max(FILTER_VALUE_MAX).optional() }))
    .max(20)
    .optional()
    .catch(undefined),
})

export const Route = createFileRoute("/_app/w/$ws/db/$db/data")({
  validateSearch: search,
  loaderDeps: ({ search: s }) => ({ branch: s.branch, table: s.table, page: s.page ?? 1, sort: s.sort, dir: s.dir, where: s.where ?? [] }),
  loader: async ({ params, deps, parentMatchPromise }) => {
    const parent = await parentMatchPromise
    const database = parent.loaderData?.database
    const branches = parent.loaderData?.branches ?? []
    const branch = deps.branch && branches.some((b) => b.name === deps.branch) ? deps.branch : (database?.default_branch ?? "main")
    const result = await browseTable({
      data: {
        ws: params.ws,
        db: params.db,
        branch,
        table: deps.table,
        page: deps.page,
        sort: deps.sort ? { column: deps.sort, dir: deps.dir ?? "asc" } : null,
        filters: deps.where,
      },
    })
    return { branch, result }
  },
  head: ({ params }) => ({ meta: [{ title: pageTitle("Data", params.db) }] }),
  pendingComponent: () => (
    <DbFrame section="data">
      <RowsSkeleton rows={6} />
    </DbFrame>
  ),
  errorComponent: ({ error }) => (
    <DbFrame section="data">
      <ErrorState error={error} title="Couldn't load this branch" />
    </DbFrame>
  ),
  component: DataPage,
})

function DataPage() {
  const ws = useWs()
  const { db } = Route.useParams()
  const { branch, result } = Route.useLoaderData()
  const s = Route.useSearch()
  const navigate = Route.useNavigate()
  const router = useRouter()
  const busy = useRouterState({ select: (state) => state.isLoading })
  const { viewer } = Route.useRouteContext()
  const canWrite = can(viewer.role, "write")

  const table = result.table?.name ?? s.table
  const query: BrowseQuery = {
    table,
    page: result.page,
    sort: s.sort ? { column: s.sort, dir: s.dir ?? "asc" } : null,
    filters: s.where ?? [],
  }

  function onQueryChange(next: Partial<BrowseQuery>) {
    const merged = { ...query, ...next }
    void navigate({
      search: (prev) => ({
        ...prev,
        table: merged.table,
        page: merged.page > 1 ? merged.page : undefined,
        sort: merged.sort?.column,
        dir: merged.sort?.dir,
        where: merged.filters.length ? merged.filters : undefined,
      }),
    })
  }

  const target = { db, branch, table: table ?? "" }
  const actions: DataActions = {
    loadCell: (key, column) => getCell({ data: { ws, ...target, key, column } }),
    updateCell: async (key, column, value) => {
      await updateCell({ data: { ws, ...target, key, column, value } })
      await router.invalidate()
    },
    insertRow: async (values) => {
      await insertRow({ data: { ws, ...target, values } })
      await router.invalidate()
    },
    deleteRows: async (keys) => {
      await deleteRows({ data: { ws, ...target, keys } })
      await router.invalidate()
    },
  }

  return (
    <DbFrame section="data">
      <DataBrowser result={result} query={query} onQueryChange={onQueryChange} actions={actions} busy={busy} emptyAction={canWrite && <OpenSql />} />
    </DbFrame>
  )
}
