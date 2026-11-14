import { createFileRoute } from "@tanstack/react-router"
import { DbFrame, useDb } from "@/components/app/db-frame"
import { SqlConsole } from "@/components/app/sql-console"
import { explainSql, runSql } from "@/fns/sql"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

export const Route = createFileRoute("/_app/db/$db/sql")({
  head: ({ params }) => ({ meta: [{ title: pageTitle("SQL", params.db) }] }),
  component: SqlPage,
})

function SqlPage() {
  const { database, branch } = useDb()
  const { viewer } = Route.useRouteContext()
  const db = database.name
  return (
    <DbFrame section="sql" wide>
      <SqlConsole
        key={db}
        db={db}
        readOnly={!can(viewer.role, "write")}
        run={(statements) => runSql({ data: { db, branch, statements } })}
        explain={(sql) => explainSql({ data: { db, branch, sql } })}
      />
    </DbFrame>
  )
}
