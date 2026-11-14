import type { ObjectOut } from "@lumbung/client"
import { Link, createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { ArchiveIcon } from "lucide-react"
import { useEffect, useState } from "react"
import { z } from "zod"
import { BucketFrame } from "@/components/app/bucket-frame"
import { DEFAULT_OBJECT_SORT, OBJECT_SORT_KEYS, defaultSortDir, type ObjectSort, ObjectSortMenu, ObjectTable, PublicBadge, UploadButton, sortSearch } from "@/components/app/buckets"
import { errorText } from "@/components/app/form-dialog"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { Button, buttonVariants } from "@/components/ui/button"
import { Empty } from "@/components/ui/surface"
import { toast } from "@/components/ui/toast"
import { deleteObject, getBucketPage } from "@/fns/buckets"
import { formatBytes, number } from "@/lib/format"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { useWs } from "@/components/app/workspace"

export const Route = createFileRoute("/_app/w/$ws/buckets/$bucket/")({
  // A prefix always ends at a folder: "a/b" means the folder "a/".
  validateSearch: z.object({
    prefix: z
      .string()
      .max(1024)
      .optional()
      .transform((p) => (p ? p.slice(0, p.lastIndexOf("/") + 1) || undefined : undefined)),
    // A bad sort in a hand-edited URL falls back to the default.
    sort: z.enum(OBJECT_SORT_KEYS).optional().catch(undefined),
    dir: z.enum(["asc", "desc"]).optional().catch(undefined),
  }),
  loaderDeps: ({ search }) => ({ prefix: search.prefix ?? "" }),
  // A new folder is a new page: no state, or a late "Load more", carries over.
  remountDeps: ({ search }) => ({ prefix: search.prefix ?? "" }),
  loader: ({ params, deps }) => getBucketPage({ data: { ws: params.ws, bucket: params.bucket, prefix: deps.prefix } }),
  head: ({ params }) => ({ meta: [{ title: pageTitle(params.bucket, "Buckets") }] }),
  component: BucketPage,
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton />
    </Frame>
  ),
  errorComponent: ({ error }) => {
    const ws = useWs()
    const missing = !!(error as { code?: string }).code?.endsWith("not_found") || (error instanceof Error && /no bucket/i.test(error.message))
    return (
      <Frame>
        {missing ? (
          <Empty icon={<ArchiveIcon />} title="This bucket doesn't exist">
            <Link to="/w/$ws/buckets" params={{ ws }} className={buttonVariants({ variant: "secondary" })}>
              All buckets
            </Link>
          </Empty>
        ) : (
          <ErrorState error={error} title="Couldn't load this bucket" />
        )}
      </Frame>
    )
  },
})

function Frame({ children, actions }: { children: React.ReactNode; actions?: React.ReactNode }) {
  const { bucket } = Route.useParams()
  const search = Route.useSearch()
  return (
    <BucketFrame bucket={bucket} section="objects" prefix={search.prefix ?? ""} keep={{ sort: search.sort, dir: search.dir }} actions={actions}>
      {children}
    </BucketFrame>
  )
}

function BucketPage() {
  const ws = useWs()
  const page = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const search = Route.useSearch()
  const prefix = search.prefix ?? ""
  const sort: ObjectSort = { key: search.sort ?? DEFAULT_OBJECT_SORT.key, dir: search.dir ?? (search.sort ? defaultSortDir(search.sort) : DEFAULT_OBJECT_SORT.dir) }
  const navigate = useNavigate({ from: Route.fullPath })
  const setSort = (next: ObjectSort) => {
    void navigate({ search: (s) => ({ prefix: s.prefix, ...sortSearch(next) }), replace: true, resetScroll: false })
  }
  const router = useRouter()
  const canWrite = can(viewer.role, "write")
  const canAdmin = can(viewer.role, "admin")
  const name = page.bucket.name

  // Later pages load in place, under the first one the loader brought.
  const [more, setMore] = useState<{ objects: ObjectOut[]; prefixes: string[]; cursor: string }>({ objects: [], prefixes: [], cursor: page.nextCursor })
  const [removed, setRemoved] = useState<Set<string>>(() => new Set())
  const [loading, setLoading] = useState(false)
  // A reload (after an upload) starts the listing over.
  useEffect(() => {
    setMore({ objects: [], prefixes: [], cursor: page.nextCursor })
    setRemoved(new Set())
  }, [page])

  async function loadMore() {
    setLoading(true)
    try {
      const next = await getBucketPage({ data: { ws, bucket: name, prefix, cursor: more.cursor } })
      setMore((m) => ({ objects: [...m.objects, ...next.objects], prefixes: [...m.prefixes, ...next.prefixes], cursor: next.nextCursor }))
    } catch (e) {
      toast.error({ title: "Couldn't load more", description: errorText(e) })
    } finally {
      setLoading(false)
    }
  }

  // A delete drops the row in place, so pages loaded with "Load more" stay.
  async function remove(key: string) {
    await deleteObject({ data: { ws, bucket: name, key } })
    toast.success(`Deleted ${key.slice(prefix.length)}`)
    setRemoved((r) => new Set(r).add(key))
  }

  const actions = (
    <>
      {canWrite && <UploadButton bucket={name} prefix={prefix} onDone={() => router.invalidate()} />}
    </>
  )

  return (
    <Frame actions={actions}>
      <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-muted-foreground">
        <span className="tabular-nums">
          The bucket holds {number.format(page.bucket.object_count)} {page.bucket.object_count === 1 ? "object" : "objects"}, {formatBytes(page.bucket.size_bytes)}
        </span>
        {page.bucket.public_read && <PublicBadge />}
        {/* Type and Modified are hidden on narrow screens, so their sort lives here. */}
        {page.objects.length + page.prefixes.length > 0 && <ObjectSortMenu sort={sort} onSortChange={setSort} className="-mr-2 ml-auto md:hidden" />}
      </div>
      <ObjectTable
        bucket={name}
        prefix={prefix}
        objects={[...page.objects, ...more.objects].filter((o) => !removed.has(o.key))}
        prefixes={[...page.prefixes, ...more.prefixes]}
        canWrite={canWrite}
        access={{ publicRead: page.bucket.public_read, s3Endpoint: page.s3Endpoint, canAdmin }}
        onDelete={remove}
        sort={sort}
        onSortChange={setSort}
        footer={
          more.cursor && (
            <div className="mt-4 flex flex-col items-center gap-2">
              {sort.key !== "name" && <p className="text-xs text-muted-foreground">Sorted within loaded items</p>}
              <Button variant="secondary" onClick={loadMore} disabled={loading}>
                {loading ? "Loading…" : "Load more"}
              </Button>
            </div>
          )
        }
      />
    </Frame>
  )
}
