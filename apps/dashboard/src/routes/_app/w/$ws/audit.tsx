import type { AuditOut } from "@lumbung/client"
import { createFileRoute, redirect } from "@tanstack/react-router"
import { useEffect, useState } from "react"
import { AuditTable } from "@/components/app/audit"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { Button } from "@/components/ui/button"
import { toast } from "@/components/ui/toast"
import { listAudit } from "@/fns/admin"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

export const Route = createFileRoute("/_app/audit")({
  beforeLoad: ({ context }) => {
    if (!can(context.viewer.role, "write")) throw redirect({ to: "/" })
  },
  loader: () => listAudit({ data: {} }),
  head: () => ({ meta: [{ title: pageTitle("Audit log") }] }),
  component: AuditPage,
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton rows={6} />
    </Frame>
  ),
  errorComponent: ({ error }) => (
    <Frame>
      <ErrorState error={error} title="Couldn't load the audit log" />
    </Frame>
  ),
})

function Frame({ children }: { children: React.ReactNode }) {
  return (
    <>
      <PageHeader>
        <PageTitle>Audit log</PageTitle>
      </PageHeader>
      <PageBody>{children}</PageBody>
    </>
  )
}

function AuditPage() {
  const first = Route.useLoaderData()
  const [more, setMore] = useState<AuditOut[]>([])
  const [cursor, setCursor] = useState(first.nextCursor)
  useEffect(() => {
    setMore([])
    setCursor(first.nextCursor)
  }, [first])

  async function loadMore() {
    try {
      const page = await listAudit({ data: { cursor } })
      setMore((prev) => [...prev, ...page.data])
      setCursor(page.nextCursor)
    } catch (e) {
      toast.error({ title: "Couldn't load more", description: e instanceof Error ? e.message : String(e) })
    }
  }

  return (
    <Frame>
      <div className="flex flex-col gap-6">
        <AuditTable entries={[...first.data, ...more]} />
        {cursor && (
          <Button variant="secondary" className="self-center" onClick={loadMore}>
            Load older
          </Button>
        )}
      </div>
    </Frame>
  )
}
