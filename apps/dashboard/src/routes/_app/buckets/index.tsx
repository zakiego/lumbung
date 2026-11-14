import { createFileRoute } from "@tanstack/react-router"
import { ArchiveIcon } from "lucide-react"
import { BucketTable, CreateBucketDialog, useCreateBucket } from "@/components/app/buckets"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { Empty } from "@/components/ui/surface"
import { listBuckets } from "@/fns/buckets"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

export const Route = createFileRoute("/_app/buckets/")({
  loader: () => listBuckets(),
  head: () => ({ meta: [{ title: pageTitle("Buckets") }] }),
  component: BucketsPage,
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton />
    </Frame>
  ),
  errorComponent: ({ error }) => (
    <Frame>
      <ErrorState error={error} title="Couldn't load buckets" />
    </Frame>
  ),
})

function Frame({ children, actions }: { children: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <>
      <PageHeader actions={actions}>
        <PageTitle>Buckets</PageTitle>
      </PageHeader>
      <PageBody>{children}</PageBody>
    </>
  )
}

function BucketsPage() {
  const buckets = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const canAdmin = can(viewer.role, "admin")

  const create = useCreateBucket()

  return (
    <Frame actions={canAdmin && <CreateBucketDialog onCreate={create} />}>
      {buckets.length === 0 ? (
        <Empty
          icon={<ArchiveIcon />}
          title="No buckets yet"
          description="Buckets hold files next to your databases, reachable from S3 clients, the SDK, the CLI and MCP."
        >
          {canAdmin && <CreateBucketDialog onCreate={create} variant="secondary" />}
        </Empty>
      ) : (
        <BucketTable buckets={buckets} />
      )}
    </Frame>
  )
}
