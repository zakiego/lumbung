import type { ObjectOut } from "@lumbung/client"
import { Link, createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { ArchiveIcon, SettingsIcon } from "lucide-react"
import { useEffect, useState } from "react"
import { z } from "zod"
import {
  DEFAULT_OBJECT_SORT,
  OBJECT_GROUP_KEYS,
  OBJECT_SEARCH_ID,
  OBJECT_SORT_KEYS,
  defaultSortDir,
  groupSearch,
  type ObjectGroup,
  ObjectGroupMenu,
  ObjectSearch,
  type ObjectSort,
  ObjectSortMenu,
  ObjectTable,
  PublicBadge,
  UploadButton,
  prefixTrail,
  sortSearch,
} from "@/components/app/buckets"
import { errorText } from "@/components/app/form-dialog"
import { PageBody, PageHeader } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { Button, buttonVariants } from "@/components/ui/button"
import { Empty } from "@/components/ui/surface"
import { toast } from "@/components/ui/toast"
import { TruncatedTip } from "@/components/ui/tooltip"
import { deleteObject, getBucketPage } from "@/fns/buckets"
import { formatBytes, number } from "@/lib/format"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

export const Route = createFileRoute("/_app/buckets/$bucket/")({
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
    // Search filters this folder by name. It stays in the URL so a link keeps it.
    q: z.string().max(200).optional().catch(undefined),
    group: z.enum(OBJECT_GROUP_KEYS).optional().catch(undefined),
  }),
  loaderDeps: ({ search }) => ({ prefix: search.prefix ?? "" }),
  // A new folder is a new page: no state, or a late "Load more", carries over.
  remountDeps: ({ search }) => ({ prefix: search.prefix ?? "" }),
  loader: ({ params, deps }) => getBucketPage({ data: { bucket: params.bucket, prefix: deps.prefix } }),
  head: ({ params }) => ({ meta: [{ title: pageTitle(params.bucket, "Buckets") }] }),
  component: BucketPage,
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
          <ErrorState error={error} title="Couldn't load this bucket" />
        )}
      </Frame>
    )
  },
})

function Frame({ children, actions }: { children: React.ReactNode; actions?: React.ReactNode }) {
  const { bucket } = Route.useParams()
  const search = Route.useSearch()
  const prefix = search.prefix ?? ""
  const keep = { sort: search.sort, dir: search.dir, group: search.group }
  const trail = prefixTrail(prefix)
  return (
    <>
      <PageHeader actions={actions}>
        <nav aria-label="Breadcrumb" className="min-w-0">
          <ol className="flex min-w-0 items-center gap-1.5 text-[13px]">
            <li className="hidden sm:block">
              <Link to="/buckets" className="text-muted-foreground transition-colors duration-100 hover:text-foreground">
                Buckets
              </Link>
            </li>
            <li aria-hidden="true" className="hidden text-faint sm:block">
              /
            </li>
            <li className="min-w-0">
              <TruncatedTip text={bucket} side="bottom">
                {trail.length === 0 ? (
                  <h1 aria-current="page" className="truncate font-mono font-[590]">
                    {bucket}
                  </h1>
                ) : (
                  <Link to="/buckets/$bucket" params={{ bucket }} search={keep} className="block truncate font-mono text-muted-foreground hover:text-foreground">
                    {bucket}
                  </Link>
                )}
              </TruncatedTip>
            </li>
            {trail.map((t, i) => (
              <li key={t.prefix} className="flex min-w-0 items-center gap-1.5">
                <span aria-hidden="true" className="text-faint">
                  /
                </span>
                <TruncatedTip text={t.label} side="bottom">
                  {i === trail.length - 1 ? (
                    <h1 aria-current="page" className="truncate font-mono font-[590]">
                      {t.label}
                    </h1>
                  ) : (
                    <Link to="/buckets/$bucket" params={{ bucket }} search={{ prefix: t.prefix, ...keep }} className="block truncate font-mono text-muted-foreground hover:text-foreground">
                      {t.label}
                    </Link>
                  )}
                </TruncatedTip>
              </li>
            ))}
          </ol>
        </nav>
      </PageHeader>
      <PageBody>{children}</PageBody>
    </>
  )
}

function BucketPage() {
  const page = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const search = Route.useSearch()
  const prefix = search.prefix ?? ""
  const sort: ObjectSort = { key: search.sort ?? DEFAULT_OBJECT_SORT.key, dir: search.dir ?? (search.sort ? defaultSortDir(search.sort) : DEFAULT_OBJECT_SORT.dir) }
  const navigate = useNavigate({ from: Route.fullPath })
  const group: ObjectGroup = search.group ?? "none"
  const setSort = (next: ObjectSort) => {
    void navigate({ search: (s) => ({ ...s, sort: undefined, dir: undefined, ...sortSearch(next) }), replace: true, resetScroll: false })
  }
  const setGroup = (next: ObjectGroup) => {
    void navigate({ search: (s) => ({ ...s, group: groupSearch(next).group }), replace: true, resetScroll: false })
  }
  // The field answers each key at once; the URL follows.
  const [query, setQuery] = useState(search.q ?? "")
  const changeQuery = (next: string) => {
    setQuery(next)
    void navigate({ search: (s) => ({ ...s, q: next.trim() ? next : undefined }), replace: true, resetScroll: false })
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
      const next = await getBucketPage({ data: { bucket: name, prefix, cursor: more.cursor } })
      setMore((m) => ({ objects: [...m.objects, ...next.objects], prefixes: [...m.prefixes, ...next.prefixes], cursor: next.nextCursor }))
    } catch (e) {
      toast.error({ title: "Couldn't load more", description: errorText(e) })
    } finally {
      setLoading(false)
    }
  }

  // A delete drops the row in place, so pages loaded with "Load more" stay.
  async function remove(key: string) {
    await deleteObject({ data: { bucket: name, key } })
    toast.success(`Deleted ${key.slice(prefix.length)}`)
    setRemoved((r) => new Set(r).add(key))
  }

  const actions = (
    <>
      {canAdmin && (
        <Link to="/buckets/$bucket/settings" params={{ bucket: name }} className={buttonVariants({ variant: "ghost" })}>
          <SettingsIcon aria-hidden="true" />
          <span className="max-sm:sr-only">Settings</span>
        </Link>
      )}
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
      </div>
      {(page.objects.length + page.prefixes.length > 0 || query) && (
        <div className="mb-3 flex items-center gap-1">
          <ObjectSearch value={query} onValueChange={changeQuery} className="mr-auto min-w-0 flex-1 sm:max-w-xs" />
          <ObjectGroupMenu group={group} onGroupChange={setGroup} className="md:-mr-2" />
          {/* Type and Modified are hidden on narrow screens, so their sort lives here. */}
          <ObjectSortMenu sort={sort} onSortChange={setSort} className="-mr-2 md:hidden" />
        </div>
      )}
      <ObjectTable
        bucket={name}
        prefix={prefix}
        objects={[...page.objects, ...more.objects].filter((o) => !removed.has(o.key))}
        prefixes={[...page.prefixes, ...more.prefixes]}
        canWrite={canWrite}
        onDelete={remove}
        sort={sort}
        onSortChange={setSort}
        query={query}
        onQueryClear={() => {
          changeQuery("")
          // The button goes away with the empty state, so focus returns to the field.
          document.getElementById(OBJECT_SEARCH_ID)?.focus()
        }}
        group={group}
        footer={
          more.cursor && (
            <div className="mt-4 flex flex-col items-center gap-2">
              {(query.trim() || sort.key !== "name" || group !== "none") && <p className="text-xs text-muted-foreground">{query.trim() ? "Searching loaded items" : "Sorted within loaded items"}</p>}
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
