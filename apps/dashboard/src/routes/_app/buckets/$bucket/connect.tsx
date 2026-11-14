import { Link, createFileRoute } from "@tanstack/react-router"
import { ArchiveIcon, KeyRoundIcon } from "lucide-react"
import { BucketFrame } from "@/components/app/bucket-frame"
import { ConnectDetails, type DetailRow, SnippetTabs } from "@/components/app/connect"
import { SettingsSection } from "@/components/app/db-settings"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { buttonVariants } from "@/components/ui/button"
import { Empty } from "@/components/ui/surface"
import { getBucketConnect } from "@/fns/buckets"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { S3_REGION, bucketSnippets, s3Endpoint } from "@/lib/snippets"

export const Route = createFileRoute("/_app/buckets/$bucket/connect")({
  loader: ({ params }) => getBucketConnect({ data: { bucket: params.bucket } }),
  head: ({ params }) => ({ meta: [{ title: pageTitle("Connect", params.bucket) }] }),
  component: ConnectPage,
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton />
    </Frame>
  ),
  errorComponent: ({ error }) => {
    const missing = !!(error as { code?: string }).code?.endsWith("not_found") || (error instanceof Error && /no bucket/i.test(error.message))
    return (
      <Frame>
        {missing ? (
          <Empty icon={<ArchiveIcon />} title="This bucket doesn't exist">
            <Link to="/buckets" className={buttonVariants({ variant: "secondary" })}>
              All buckets
            </Link>
          </Empty>
        ) : (
          <ErrorState error={error} title="Couldn't load connection details" />
        )}
      </Frame>
    )
  },
})

function Frame({ children }: { children: React.ReactNode }) {
  const { bucket } = Route.useParams()
  return (
    <BucketFrame bucket={bucket} section="connect">
      {children}
    </BucketFrame>
  )
}

function ConnectPage() {
  const { bucket, engineUrl: url } = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const name = bucket.name
  const endpoint = s3Endpoint(url)
  const rows: DetailRow[] = [
    { label: "Engine URL", value: url },
    { label: "S3 endpoint", value: endpoint },
    { label: "Region", value: S3_REGION },
    { label: "Bucket", value: name },
    { label: "Addressing", value: "Path-style", copy: false },
  ]
  // A pattern, not a working URL, so there's nothing to copy.
  if (bucket.public_read) rows.push({ label: "Object URL", value: `${endpoint}/${name}/<key>`, copy: false })

  return (
    <Frame>
      <div className="flex max-w-4xl flex-col">
        <SettingsSection title="Details">
          <ConnectDetails rows={rows} />
        </SettingsSection>
        <SettingsSection title="Access key" description="Snippets read it from LUMBUNG_TOKEN and LUMBUNG_ACCESS_KEY_*.">
          {can(viewer.role, "admin") ? (
            <Link to="/tokens" className={buttonVariants({ variant: "secondary", className: "self-start" })}>
              <KeyRoundIcon aria-hidden="true" />
              Create access key
            </Link>
          ) : (
            <p className="text-[13px] text-muted-foreground">Ask an admin for an access key.</p>
          )}
        </SettingsSection>
        <SettingsSection title="Code">
          <SnippetTabs label="Connect with" snippets={bucketSnippets({ url, bucket: name })} />
        </SettingsSection>
      </div>
    </Frame>
  )
}
