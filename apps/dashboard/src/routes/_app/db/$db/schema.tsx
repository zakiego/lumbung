import { createFileRoute } from "@tanstack/react-router"
import { DbFrame, OpenSql } from "@/components/app/db-frame"
import { SchemaView } from "@/components/app/schema-view"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { getSchema } from "@/fns/databases"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

export const Route = createFileRoute("/_app/db/$db/schema")({
  loaderDeps: ({ search }) => ({ branch: search.branch }),
  loader: async ({ params, deps, parentMatchPromise }) => {
    const parent = await parentMatchPromise
    const database = parent.loaderData?.database
    const branches = parent.loaderData?.branches ?? []
    const branch = deps.branch && branches.some((b) => b.name === deps.branch) ? deps.branch : (database?.default_branch ?? "main")
    return { schema: await getSchema({ data: { db: params.db, branch } }) }
  },
  head: ({ params }) => ({ meta: [{ title: pageTitle("Schema", params.db) }] }),
  pendingComponent: () => (
    <DbFrame section="schema">
      <RowsSkeleton rows={5} />
    </DbFrame>
  ),
  errorComponent: ({ error }) => (
    <DbFrame section="schema">
      <ErrorState error={error} title="Couldn't load the schema" />
    </DbFrame>
  ),
  component: SchemaPage,
})

function SchemaPage() {
  const { schema } = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  return (
    <DbFrame section="schema">
      <SchemaView schema={schema} emptyAction={can(viewer.role, "write") && <OpenSql />} />
    </DbFrame>
  )
}
