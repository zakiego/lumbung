import { Link, Outlet, createFileRoute } from "@tanstack/react-router"
import { DatabaseIcon } from "lucide-react"
import { z } from "zod"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState } from "@/components/app/states"
import { buttonVariants } from "@/components/ui/button"
import { Empty } from "@/components/ui/surface"
import { getDatabase } from "@/fns/databases"
import { pageTitle } from "@/lib/paths"

export const Route = createFileRoute("/_app/db/$db")({
  validateSearch: z.object({ branch: z.string().min(1).max(100).optional() }),
  loader: ({ params }) => getDatabase({ data: { db: params.db } }),
  head: ({ params }) => ({ meta: [{ title: pageTitle(params.db) }] }),
  component: () => <Outlet />,
  errorComponent: ({ error }) => {
    const missing = !!(error as { code?: string }).code?.endsWith("not_found") || (error instanceof Error && /not found/i.test(error.message))
    return (
      <>
        <PageHeader>
          <PageTitle>Database</PageTitle>
        </PageHeader>
        <PageBody>
          {missing ? (
            <Empty icon={<DatabaseIcon />} title="This database doesn't exist">
              <Link to="/" className={buttonVariants({ variant: "secondary" })}>
                All databases
              </Link>
            </Empty>
          ) : (
            <ErrorState error={error} title="Couldn't load this database" />
          )}
        </PageBody>
      </>
    )
  },
})
