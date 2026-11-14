import { createFileRoute, useRouter } from "@tanstack/react-router"
import { ArchiveIcon } from "lucide-react"
import { BucketTable, CreateBucketDialog } from "@/components/app/buckets"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { Empty } from "@/components/ui/surface"
import { toast } from "@/components/ui/toast"
import { createBucket, listBuckets } from "@/fns/buckets"
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
  const router = useRouter()
  const canAdmin = can(viewer.role, "admin")

  async function create(name: string) {
    await createBucket({ data: { name } })
    toast.success(`Created ${name}`)
    await router.invalidate()
    await router.navigate({ to: "/buckets/$bucket", params: { bucket: name } })
  }

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
