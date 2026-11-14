import { createFileRoute } from "@tanstack/react-router"
import { DbFrame, useDb } from "@/components/app/db-frame"
import { SqlConsole } from "@/components/app/sql-console"
import { explainSql, runSql } from "@/fns/sql"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { useWs } from "@/components/app/workspace"

export const Route = createFileRoute("/_app/w/$ws/db/$db/sql")({
  head: ({ params }) => ({ meta: [{ title: pageTitle("SQL", params.db) }] }),
  component: SqlPage,
})

function SqlPage() {
  const ws = useWs()
  const { database, branch } = useDb()
  const { viewer } = Route.useRouteContext()
  const db = database.name
  return (
    <DbFrame section="sql">
      <SqlConsole
        key={db}
        db={db}
        readOnly={!can(viewer.role, "write")}
        run={(statements) => runSql({ data: { ws, db, branch, statements } })}
        explain={(sql) => explainSql({ data: { ws, db, branch, sql } })}
      />
    </DbFrame>
  )
}
