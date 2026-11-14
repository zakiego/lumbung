import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { useCallback } from "react"
import { z } from "zod"
import { type Scope, ScopeFrame } from "@/components/app/scope-tabs"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { CreateTokenDialog, EVERY_WORKSPACE, type TokenNames, TokenTable } from "@/components/app/tokens"
import { useWs } from "@/components/app/workspace"
import { toast } from "@/components/ui/toast"
import { createToken, listTokens, revokeToken } from "@/fns/admin"
import { listBuckets } from "@/fns/buckets"
import { getDatabase, listDatabases } from "@/fns/databases"
import { createInstanceToken, listInstanceTokens, revokeInstanceToken } from "@/fns/workspaces"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

const NO_NAMES: TokenNames = { databases: {}, branches: {}, buckets: {} }

// The workspace's tokens. An owner can also manage the instance's, which
// belong to no workspace and reach all of them.
export const Route = createFileRoute("/_app/w/$ws/tokens")({
  validateSearch: z.object({ scope: z.literal("instance").optional() }),
  beforeLoad: ({ context, params, search }) => {
    if (!can(context.viewer.role, "admin")) throw redirect({ to: "/w/$ws", params })
    if (search.scope && !context.viewer.owner) throw redirect({ to: "/w/$ws/tokens", params, search: {} })
  },
  loaderDeps: ({ search }) => ({ scope: search.scope }),
  loader: async ({ params, deps }) => {
    if (deps.scope) return { tokens: await listInstanceTokens(), names: NO_NAMES, databases: [], buckets: [] }
    const ws = params.ws
    const [tokens, databases, buckets] = await Promise.all([listTokens({ data: { ws } }), listDatabases({ data: { ws } }), listBuckets({ data: { ws } }).catch(() => [])])
    const names: TokenNames = {
      databases: Object.fromEntries(databases.map((d) => [d.id, d.name])),
      branches: {},
      buckets: Object.fromEntries(buckets.map((b) => [b.id, b.name])),
    }
    // Branch-limited tokens name a branch by id; look up the names of those databases' branches.
    const limited = tokens.flatMap((t) => (t.grants?.length ? t.grants : [t])).filter((g) => g.branch_id && names.databases[g.database_id])
    const withBranch = [...new Set(limited.map((g) => names.databases[g.database_id]!))]
    const lists = await Promise.all(withBranch.map((db) => getDatabase({ data: { ws, db } }).catch(() => null)))
    for (const l of lists) for (const b of l?.branches ?? []) names.branches[b.id] = b.name
    return { tokens, names, databases: databases.map((d) => d.name), buckets: buckets.map((b) => b.name) }
  },
  head: ({ params }) => ({ meta: [{ title: pageTitle("Tokens", params.ws) }] }),
  component: TokensPage,
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton />
    </Frame>
  ),
  errorComponent: ({ error }) => (
    <Frame>
      <ErrorState error={error} title="Couldn't load tokens" />
    </Frame>
  ),
})

function Frame({ children, actions }: { children: React.ReactNode; actions?: React.ReactNode }) {
  const { viewer } = Route.useRouteContext()
  const scope: Scope = Route.useSearch().scope ?? "workspace"
  return (
    <ScopeFrame title="Tokens" to="/w/$ws/tokens" scope={scope} owner={viewer.owner} actions={actions}>
      {children}
    </ScopeFrame>
  )
}

function TokensPage() {
  const ws = useWs()
  const { tokens, names, databases, buckets } = Route.useLoaderData()
  const { engineUrl } = Route.useRouteContext()
  const instance = Route.useSearch().scope === "instance"
  const mcpUrl = engineUrl ? `${engineUrl.replace(/\/+$/, "")}/mcp` : undefined
  const router = useRouter()
  const loadBranches = useCallback(async (db: string) => (await getDatabase({ data: { ws, db } })).branches.map((b) => b.name), [ws])

  return (
    <Frame
      actions={
        <CreateTokenDialog
          key={instance ? "instance" : "workspace"}
          databases={databases}
          buckets={buckets}
          mcpUrl={mcpUrl}
          loadBranches={loadBranches}
          everything={instance ? EVERY_WORKSPACE : undefined}
          onCreate={async (t) => {
            const created = instance
              ? await createInstanceToken({ data: { name: t.name, access: t.access ?? "read", accessKey: t.accessKey, expiresInDays: t.expiresInDays } })
              : await createToken({ data: { ws, ...t } })
            void router.invalidate()
            return created
          }}
        />
      }
    >
      <TokenTable
        tokens={tokens}
        names={names}
        everything={instance ? EVERY_WORKSPACE : undefined}
        onRevoke={async (t) => {
          if (instance) await revokeInstanceToken({ data: { id: t.id } })
          else await revokeToken({ data: { ws, id: t.id } })
          toast.success(`Revoked ${t.name}`)
          await router.invalidate()
        }}
      />
    </Frame>
  )
}
