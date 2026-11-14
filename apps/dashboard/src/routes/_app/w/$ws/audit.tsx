import type { AuditOut } from "@lumbung/client"
import { createFileRoute, redirect } from "@tanstack/react-router"
import { useEffect, useState } from "react"
import { z } from "zod"
import { AuditTable } from "@/components/app/audit"
import { type Scope, ScopeFrame } from "@/components/app/scope-tabs"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { useWs } from "@/components/app/workspace"
import { Button } from "@/components/ui/button"
import { toast } from "@/components/ui/toast"
import { listAudit } from "@/fns/admin"
import { listInstanceAudit } from "@/fns/workspaces"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

// The workspace's log. An owner can also read the instance's: every
// workspace's entries, and what happened to the instance itself.
export const Route = createFileRoute("/_app/w/$ws/audit")({
  validateSearch: z.object({ scope: z.literal("instance").optional() }),
  beforeLoad: ({ context, params, search }) => {
    if (!can(context.viewer.role, "admin")) throw redirect({ to: "/w/$ws", params })
    if (search.scope && !context.viewer.owner) throw redirect({ to: "/w/$ws/audit", params, search: {} })
  },
  loaderDeps: ({ search }) => ({ scope: search.scope }),
  loader: ({ params, deps }) => (deps.scope ? listInstanceAudit({ data: {} }) : listAudit({ data: { ws: params.ws } })),
  head: ({ params }) => ({ meta: [{ title: pageTitle("Audit log", params.ws) }] }),
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
  const { viewer } = Route.useRouteContext()
  const scope: Scope = Route.useSearch().scope ?? "workspace"
  return (
    <ScopeFrame title="Audit log" to="/w/$ws/audit" scope={scope} owner={viewer.owner}>
      {children}
    </ScopeFrame>
  )
}

function AuditPage() {
  const ws = useWs()
  const { scope } = Route.useSearch()
  const first = Route.useLoaderData()
  const [more, setMore] = useState<AuditOut[]>([])
  const [cursor, setCursor] = useState(first.nextCursor)
  useEffect(() => {
    setMore([])
    setCursor(first.nextCursor)
  }, [first])

  async function loadMore() {
    try {
      const page = scope ? await listInstanceAudit({ data: { cursor } }) : await listAudit({ data: { ws, cursor } })
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
