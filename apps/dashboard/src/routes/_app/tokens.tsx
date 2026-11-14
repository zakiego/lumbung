import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { useCallback } from "react"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { CreateTokenDialog, type TokenNames, TokenTable } from "@/components/app/tokens"
import { toast } from "@/components/ui/toast"
import { createToken, listTokens, revokeToken } from "@/fns/admin"
import { listBuckets } from "@/fns/buckets"
import { getDatabase, listDatabases } from "@/fns/databases"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

export const Route = createFileRoute("/_app/tokens")({
  beforeLoad: ({ context }) => {
    if (!can(context.viewer.role, "admin")) throw redirect({ to: "/" })
  },
  loader: async () => {
    const [tokens, databases, buckets] = await Promise.all([listTokens(), listDatabases(), listBuckets().catch(() => [])])
    const names: TokenNames = {
      databases: Object.fromEntries(databases.map((d) => [d.id, d.name])),
      branches: {},
      buckets: Object.fromEntries(buckets.map((b) => [b.id, b.name])),
    }
    // Branch-limited tokens name a branch by id; look up the names of those databases' branches.
    const withBranch = [...new Set(tokens.filter((t) => t.branch_id && names.databases[t.database_id]).map((t) => names.databases[t.database_id]!))]
    const lists = await Promise.all(withBranch.map((db) => getDatabase({ data: { db } }).catch(() => null)))
    for (const l of lists) for (const b of l?.branches ?? []) names.branches[b.id] = b.name
    return { tokens, names, databases: databases.map((d) => d.name), buckets: buckets.map((b) => b.name) }
  },
  head: () => ({ meta: [{ title: pageTitle("Tokens") }] }),
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
  return (
    <>
      <PageHeader actions={actions}>
        <PageTitle>Tokens</PageTitle>
      </PageHeader>
      <PageBody>{children}</PageBody>
    </>
  )
}

function TokensPage() {
  const { tokens, names, databases, buckets } = Route.useLoaderData()
  const router = useRouter()
  const loadBranches = useCallback(async (db: string) => (await getDatabase({ data: { db } })).branches.map((b) => b.name), [])

  return (
    <Frame
      actions={
        <CreateTokenDialog
          databases={databases}
          buckets={buckets}
          loadBranches={loadBranches}
          onCreate={async (t) => {
            const created = await createToken({ data: t })
            void router.invalidate()
            return created
          }}
        />
      }
    >
      <TokenTable
        tokens={tokens}
        names={names}
        onRevoke={async (t) => {
          await revokeToken({ data: { id: t.id } })
          toast.success(`Revoked ${t.name}`)
          await router.invalidate()
        }}
      />
    </Frame>
  )
}
