import type { BucketOut, ObjectOut } from "@lumbung/client"
import { Link, useRouter } from "@tanstack/react-router"
import { ArchiveIcon, ArrowDownIcon, ArrowUpIcon, DownloadIcon, FileIcon, FolderIcon, GlobeIcon, GroupIcon, SearchIcon, SearchXIcon, Trash2Icon, UploadIcon } from "lucide-react"
import { type ReactNode, useRef, useState } from "react"
import { Button, buttonVariants } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Menu, MenuContent, MenuGroup, MenuGroupLabel, MenuRadioGroup, MenuRadioItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu"
import { Badge, Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { toast } from "@/components/ui/toast"
import { createBucket } from "@/fns/buckets"
import { TruncatedTip } from "@/components/ui/tooltip"
import { formatBytes, number, timeAgo } from "@/lib/format"
import { APP_BASE } from "@/lib/paths"
import { cn } from "@/lib/utils"
import { Confirm, errorText } from "./form-dialog"
import { NameDialog, type TriggerSlot } from "./name-dialog"

export const BUCKET_PATTERN = "[a-z0-9][a-z0-9\\-]{1,61}[a-z0-9]"

/** Creates a bucket and opens it. A failure shows under the dialog's field, so only success gets a toast. */
export function useCreateBucket(onDone?: () => void) {
  const router = useRouter()
  return async (name: string) => {
    await createBucket({ data: { name } })
    toast.success(`Created ${name}`)
    await router.invalidate()
    await router.navigate({ to: "/buckets/$bucket", params: { bucket: name } })
    onDone?.()
  }
}

export function CreateBucketDialog({ onCreate, variant, ...slot }: { onCreate: (name: string) => Promise<unknown>; variant?: "default" | "secondary" } & TriggerSlot) {
  return (
    <NameDialog
      trigger="New bucket"
      triggerVariant={variant}
      title="Create bucket"
      description="3 to 63 lowercase letters, digits and dashes. S3 clients use the name in their paths."
      label="Name"
      placeholder="photos"
      submit="Create"
      pendingSubmit="Creating…"
      pattern={BUCKET_PATTERN}
      patternHint="Use 3 to 63 lowercase letters, digits and dashes, starting and ending with a letter or digit."
      onSubmit={onCreate}
      {...slot}
    />
  )
}

export function BucketTable({ buckets }: { buckets: BucketOut[] }) {
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Name</TableHead>
          <TableHead className="text-right">Objects</TableHead>
          <TableHead className="text-right">Size</TableHead>
          <TableHead className="hidden text-right sm:table-cell">Access</TableHead>
        </tr>
      </thead>
      <tbody>
        {buckets.map((b) => (
          <TableRow key={b.id} className="relative">
            <TableCell className="max-w-0 w-1/2">
              <TruncatedTip text={b.name}>
                <Link to="/buckets/$bucket" params={{ bucket: b.name }} className="flex min-w-0 items-center gap-2 font-mono font-[510] after:absolute after:inset-0">
                  <ArchiveIcon aria-hidden="true" className="size-4 shrink-0 text-faint" />
                  <span data-truncate className="truncate">
                    {b.name}
                  </span>
                </Link>
              </TruncatedTip>
            </TableCell>
            <TableCell className="text-right text-muted-foreground tabular-nums">{number.format(b.object_count)}</TableCell>
            <TableCell className="text-right text-muted-foreground tabular-nums">{formatBytes(b.size_bytes)}</TableCell>
            <TableCell className="hidden text-right sm:table-cell">{b.public_read ? <PublicBadge /> : <span className="text-muted-foreground">Private</span>}</TableCell>
          </TableRow>
        ))}
      </tbody>
    </Table>
  )
}

export function PublicBadge() {
  return (
    <Badge>
      <GlobeIcon aria-hidden="true" className="size-3" />
      Public read
    </Badge>
  )
}

/** The folders of a prefix, for breadcrumbs: "a/b/" gives ["a/", "a/b/"]. */
export function prefixTrail(prefix: string): { label: string; prefix: string }[] {
  const out: { label: string; prefix: string }[] = []
  let at = 0
  while (at < prefix.length) {
    const next = prefix.indexOf("/", at)
    if (next < 0) break
    out.push({ label: prefix.slice(at, next) || "/", prefix: prefix.slice(0, next + 1) })
    at = next + 1
  }
  return out
}

export function objectURL(bucket: string, key: string): string {
  return `${APP_BASE}/api/objects?${new URLSearchParams({ bucket, key })}`
}

/** Files larger than this go up in parts of this size. */
export const PART_BYTES = 16 * 1024 * 1024

async function jsonOrThrow<T>(res: Response): Promise<T> {
  const body = (await res.json().catch(() => ({}))) as T & { error?: string }
  if (!res.ok) throw new Error(body.error || `Upload failed (HTTP ${res.status})`)
  return body
}

/**
 * Uploads one file under a prefix, through the dashboard's streaming route.
 * A large file goes up as a multipart upload, one part per request, so no
 * request runs long.
 */
export async function uploadFile(bucket: string, prefix: string, file: File, f: typeof fetch = fetch): Promise<{ key: string; etag: string }> {
  const key = prefix + file.name
  const url = objectURL(bucket, key)
  const type = file.type || "application/octet-stream"
  // A key with a "." or ".." segment can't travel in an S3 path, so it goes
  // up in one piece through the key-in-query route.
  const dotted = key.split("/").some((s) => s === "." || s === "..")
  if (file.size <= PART_BYTES || dotted) {
    return jsonOrThrow(await f(url, { method: "PUT", body: file, headers: { "content-type": type } }))
  }
  const { uploadId } = await jsonOrThrow<{ uploadId: string }>(await f(`${url}&uploads`, { method: "POST", headers: { "x-object-content-type": type } }))
  try {
    const parts: { number: number; etag: string }[] = []
    for (let n = 1, at = 0; at < file.size; n++, at += PART_BYTES) {
      const q = new URLSearchParams({ uploadId, partNumber: String(n) })
      const { etag } = await jsonOrThrow<{ etag: string }>(await f(`${url}&${q}`, { method: "PUT", body: file.slice(at, at + PART_BYTES) }))
      parts.push({ number: n, etag })
    }
    return await jsonOrThrow(
      await f(`${url}&${new URLSearchParams({ uploadId })}`, { method: "POST", body: JSON.stringify({ parts }), headers: { "content-type": "application/json" } }),
    )
  } catch (e) {
    await f(`${url}&${new URLSearchParams({ uploadId })}`, { method: "DELETE" }).catch(() => {})
    throw e
  }
}

export function UploadButton({ bucket, prefix, onDone }: { bucket: string; prefix: string; onDone: () => Promise<unknown> }) {
  const input = useRef<HTMLInputElement>(null)
  const [left, setLeft] = useState(0)

  async function upload(files: File[]) {
    if (files.length === 0) return
    setLeft(files.length)
    let ok = 0
    let uploaded = ""
    for (const f of files) {
      try {
        await uploadFile(bucket, prefix, f)
        ok++
        uploaded = f.name
      } catch (e) {
        toast.error({ title: `Couldn't upload ${f.name}`, description: errorText(e) })
      }
      setLeft((n) => n - 1)
    }
    if (ok) toast.success(ok === 1 ? `Uploaded ${uploaded}` : `Uploaded ${ok} files`)
    await onDone()
  }

  return (
    <>
      <input
        ref={input}
        type="file"
        multiple
        hidden
        aria-hidden="true"
        tabIndex={-1}
        onChange={(e) => {
          const files = [...(e.currentTarget.files ?? [])]
          e.currentTarget.value = ""
          void upload(files)
        }}
      />
      <Button onClick={() => input.current?.click()} disabled={left > 0}>
        <UploadIcon aria-hidden="true" />
        {left > 0 ? `Uploading ${left}…` : "Upload"}
      </Button>
    </>
  )
}

type Row = { kind: "folder"; prefix: string } | { kind: "object"; object: ObjectOut }

export const OBJECT_SORT_KEYS = ["name", "size", "type", "modified"] as const
export type ObjectSortKey = (typeof OBJECT_SORT_KEYS)[number]
export type SortDir = "asc" | "desc"
export type ObjectSort = { key: ObjectSortKey; dir: SortDir }
export const DEFAULT_OBJECT_SORT: ObjectSort = { key: "name", dir: "asc" }
const SORT_LABEL: Record<ObjectSortKey, string> = { name: "Name", size: "Size", type: "Type", modified: "Modified" }

// A column starts where people look first: A to Z for text, biggest and
// newest for numbers.
export function defaultSortDir(key: ObjectSortKey): SortDir {
  return key === "size" || key === "modified" ? "desc" : "asc"
}

// A header press flips the order on the active column.
export function nextObjectSort(current: ObjectSort, key: ObjectSortKey): ObjectSort {
  if (current.key === key) return { key, dir: current.dir === "asc" ? "desc" : "asc" }
  return { key, dir: defaultSortDir(key) }
}

// Natural order, so IMG_9 comes before IMG_10.
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" })
const byName = (a: string, b: string) => collator.compare(a, b) || (a < b ? -1 : a > b ? 1 : 0)

// Sorts what is loaded. Ties fall back to the name, A to Z.
export function sortObjects(objects: ObjectOut[], sort: ObjectSort): ObjectOut[] {
  const sign = sort.dir === "asc" ? 1 : -1
  const by = (a: ObjectOut, b: ObjectOut) => {
    switch (sort.key) {
      case "size":
        return a.size - b.size
      case "modified":
        return Date.parse(a.last_modified) - Date.parse(b.last_modified)
      case "type":
        return collator.compare(a.content_type, b.content_type)
      default:
        return byName(a.key, b.key)
    }
  }
  return [...objects].sort((a, b) => sign * by(a, b) || byName(a.key, b.key))
}

// Folders stay on top. They have no size or date, so only a name sort moves them.
export function sortFolders(prefixes: string[], sort: ObjectSort): string[] {
  const sorted = [...prefixes].sort(byName)
  return sort.key === "name" && sort.dir === "desc" ? sorted.reverse() : sorted
}

// Search and grouping work on what is loaded, like the sort.
export function matchesQuery(name: string, query: string): boolean {
  const q = query.trim().toLowerCase()
  return !q || name.toLowerCase().includes(q)
}

export const OBJECT_GROUP_KEYS = ["none", "type", "modified"] as const
export type ObjectGroup = (typeof OBJECT_GROUP_KEYS)[number]
const GROUP_LABEL: Record<ObjectGroup, string> = { none: "None", type: "Type", modified: "Modified" }

const TYPE_GROUPS = ["Images", "Videos", "Audio", "Documents", "Archives", "Other"] as const
const DOCUMENT_TYPE = /^(text\/|application\/(pdf|json|xml|yaml|x-yaml|rtf|msword|vnd\.ms-|vnd\.openxmlformats|vnd\.oasis\.opendocument))/
const ARCHIVE_TYPE = /^application\/(zip|gzip|x-gzip|x-tar|x-7z-compressed|x-rar|vnd\.rar|x-bzip2|x-xz|zstd)/

export function typeGroup(contentType: string): (typeof TYPE_GROUPS)[number] {
  const t = contentType.toLowerCase()
  if (t.startsWith("image/")) return "Images"
  if (t.startsWith("video/")) return "Videos"
  if (t.startsWith("audio/")) return "Audio"
  if (DOCUMENT_TYPE.test(t)) return "Documents"
  if (ARCHIVE_TYPE.test(t)) return "Archives"
  return "Other"
}

const DATE_GROUPS = ["Today", "Yesterday", "Previous 7 days", "Previous 30 days", "Older"] as const
const DAY = 24 * 60 * 60 * 1000

// Days count from local midnight, so "Yesterday" means the calendar day.
export function dateGroup(lastModified: string, now: Date): (typeof DATE_GROUPS)[number] {
  const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const t = Date.parse(lastModified)
  if (t >= midnight) return "Today"
  if (t >= midnight - DAY) return "Yesterday"
  if (t >= midnight - 7 * DAY) return "Previous 7 days"
  if (t >= midnight - 30 * DAY) return "Previous 30 days"
  return "Older"
}

export type ObjectSection = { label: string; objects: ObjectOut[] }

// Splits sorted objects into labeled sections, dropping empty ones. Dates run
// newest first unless the list is sorted oldest first.
export function groupObjects(objects: ObjectOut[], group: ObjectGroup, sort: ObjectSort, now = new Date()): ObjectSection[] {
  const sorted = sortObjects(objects, sort)
  if (group === "none") return [{ label: "", objects: sorted }]
  const labels: readonly string[] = group === "type" ? TYPE_GROUPS : sort.key === "modified" && sort.dir === "asc" ? [...DATE_GROUPS].reverse() : DATE_GROUPS
  const of = (o: ObjectOut) => (group === "type" ? typeGroup(o.content_type) : dateGroup(o.last_modified, now))
  return labels.map((label) => ({ label, objects: sorted.filter((o) => of(o) === label) })).filter((s) => s.objects.length > 0)
}

// The URL leaves the default out, so a plain bucket link stays plain.
export function sortSearch(sort: ObjectSort): { sort?: ObjectSortKey; dir?: SortDir } {
  return sort.key === DEFAULT_OBJECT_SORT.key && sort.dir === DEFAULT_OBJECT_SORT.dir ? {} : { sort: sort.key, dir: sort.dir }
}

export function groupSearch(group: ObjectGroup): { group?: ObjectGroup } {
  return group === "none" ? {} : { group }
}

function SortIcon({ dir, className }: { dir: SortDir; className?: string }) {
  const Icon = dir === "asc" ? ArrowUpIcon : ArrowDownIcon
  return <Icon aria-hidden="true" className={className} />
}

function SortHead({
  column,
  sort,
  onSortChange,
  align = "left",
  className,
}: {
  column: ObjectSortKey
  sort: ObjectSort
  onSortChange?: (sort: ObjectSort) => void
  align?: "left" | "right"
  className?: string
}) {
  const active = sort.key === column
  const label = SORT_LABEL[column]
  if (!onSortChange) return <TableHead className={cn(align === "right" && "text-right", className)}>{label}</TableHead>
  return (
    <TableHead aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : undefined} className={cn(align === "right" && "text-right", className)}>
      <Button
        variant="ghost"
        size="sm"
        // A block body: a returned Promise would put the button in its pending state.
        onClick={() => {
          onSortChange(nextObjectSort(sort, column))
        }}
        // Text-only like the data grid header, so nothing pokes past the table edge.
        className={cn("px-0 hover:bg-transparent focus-visible:-outline-offset-1", active && "text-foreground")}
      >
        {/* The arrow sits on the outer side, so the label lines up with the values. */}
        {active && align === "right" && <SortIcon dir={sort.dir} className="size-3.5" />}
        {label}
        {active && align === "left" && <SortIcon dir={sort.dir} className="size-3.5" />}
      </Button>
    </TableHead>
  )
}

export function ObjectSortMenu({ sort, onSortChange, className }: { sort: ObjectSort; onSortChange: (sort: ObjectSort) => void; className?: string }) {
  return (
    <Menu>
      <MenuTrigger
        aria-label={`Sort by ${SORT_LABEL[sort.key]}, ${sort.dir === "asc" ? "ascending" : "descending"}`}
        className={buttonVariants({ variant: "ghost", size: "sm", className })}
      >
        <SortIcon dir={sort.dir} />
        {SORT_LABEL[sort.key]}
      </MenuTrigger>
      <MenuContent align="end">
        <MenuGroup>
          <MenuGroupLabel>Sort by</MenuGroupLabel>
          <MenuRadioGroup value={sort.key} onValueChange={(v) => onSortChange(nextObjectSort(sort, v as ObjectSortKey))}>
            {OBJECT_SORT_KEYS.map((k) => (
              <MenuRadioItem key={k} value={k}>
                {SORT_LABEL[k]}
              </MenuRadioItem>
            ))}
          </MenuRadioGroup>
        </MenuGroup>
        <MenuSeparator />
        <MenuGroup>
          <MenuGroupLabel>Order</MenuGroupLabel>
          <MenuRadioGroup value={sort.dir} onValueChange={(v) => onSortChange({ key: sort.key, dir: v as SortDir })}>
            <MenuRadioItem value="asc">Ascending</MenuRadioItem>
            <MenuRadioItem value="desc">Descending</MenuRadioItem>
          </MenuRadioGroup>
        </MenuGroup>
      </MenuContent>
    </Menu>
  )
}

export function ObjectGroupMenu({ group, onGroupChange, className }: { group: ObjectGroup; onGroupChange: (group: ObjectGroup) => void; className?: string }) {
  return (
    <Menu>
      <MenuTrigger className={buttonVariants({ variant: "ghost", size: "sm", className })}>
        <GroupIcon aria-hidden="true" />
        {group === "none" ? "Group" : `Group: ${GROUP_LABEL[group]}`}
      </MenuTrigger>
      <MenuContent align="end">
        <MenuGroup>
          <MenuGroupLabel>Group by</MenuGroupLabel>
          <MenuRadioGroup value={group} onValueChange={(v) => onGroupChange(v as ObjectGroup)}>
            {OBJECT_GROUP_KEYS.map((k) => (
              <MenuRadioItem key={k} value={k}>
                {GROUP_LABEL[k]}
              </MenuRadioItem>
            ))}
          </MenuRadioGroup>
        </MenuGroup>
      </MenuContent>
    </Menu>
  )
}

export const OBJECT_SEARCH_ID = "object-search"

export function ObjectSearch({ value, onValueChange, className }: { value: string; onValueChange: (value: string) => void; className?: string }) {
  return (
    <Field className={className}>
      <FieldLabel className="sr-only">Search this folder</FieldLabel>
      <div className="relative">
        <SearchIcon aria-hidden="true" className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-faint" />
        <Input id={OBJECT_SEARCH_ID} type="search" value={value} onValueChange={onValueChange} placeholder="Search by name" maxLength={200} className="pl-8" autoComplete="off" spellCheck={false} />
      </div>
    </Field>
  )
}

const plural = new Intl.PluralRules("en")
const matchCount = (n: number) => (plural.select(n) === "one" ? "1 match" : `${number.format(n)} matches`)

export function ObjectTable({
  bucket,
  prefix,
  objects,
  prefixes,
  canWrite,
  onDelete,
  sort = DEFAULT_OBJECT_SORT,
  onSortChange,
  query = "",
  onQueryClear,
  group = "none",
  footer,
}: {
  bucket: string
  prefix: string
  objects: ObjectOut[]
  prefixes: string[]
  canWrite: boolean
  onDelete: (key: string) => Promise<unknown>
  sort?: ObjectSort
  onSortChange?: (sort: ObjectSort) => void
  query?: string
  onQueryClear?: () => void
  group?: ObjectGroup
  footer?: ReactNode
}) {
  // A key equal to the folder itself is a folder marker some S3 tools make:
  // it has no name of its own here, so it isn't listed.
  const listed = objects.filter((o) => o.key !== prefix)
  const name = (key: string) => key.slice(prefix.length)
  const folders = sortFolders(prefixes, sort)
    .filter((p) => matchesQuery(name(p), query))
    .map((p): Row => ({ kind: "folder", prefix: p }))
  const files = groupObjects(
    listed.filter((o) => matchesQuery(name(o.key), query)),
    group,
    sort,
  ).map((g) => ({ label: g.label, rows: g.objects.map((o): Row => ({ kind: "object", object: o })) }))
  // Ungrouped, folders and files share one body. Grouped, folders get their own.
  const sections =
    group === "none"
      ? [{ label: "", rows: [...folders, ...(files[0]?.rows ?? [])] }]
      : [...(folders.length ? [{ label: "Folders", rows: folders }] : []), ...files]
  const matches = sections.reduce((n, g) => n + g.rows.length, 0)
  // Mounted in every branch, so the region exists before its text changes.
  const status = (
    <p role="status" className="sr-only">
      {query.trim() ? matchCount(matches) : ""}
    </p>
  )
  if (matches === 0) {
    if (query.trim() && (listed.length > 0 || prefixes.length > 0)) {
      return (
        <>
          {status}
          <Empty icon={<SearchXIcon />} title="No matches" description={`Nothing loaded here has “${query.trim()}” in its name.`}>
            {onQueryClear && (
              <Button variant="secondary" onClick={onQueryClear}>
                Clear search
              </Button>
            )}
          </Empty>
          {footer}
        </>
      )
    }
    return (
      <>
        {status}
        <Empty icon={<FolderIcon />} title={prefix ? "This folder is empty" : "No objects yet"} description="Upload files here, or put them with an S3 client, the CLI or the SDK." />
      </>
    )
  }
  return (
    <>
      {status}
      <Table>
        <thead>
          <tr>
            <SortHead column="name" sort={sort} onSortChange={onSortChange} />
            <SortHead column="size" sort={sort} onSortChange={onSortChange} align="right" />
            <SortHead column="type" sort={sort} onSortChange={onSortChange} className="hidden md:table-cell" />
            <SortHead column="modified" sort={sort} onSortChange={onSortChange} align="right" className="hidden sm:table-cell" />
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </tr>
        </thead>
        {sections.map((section) => (
          <tbody key={section.label || "all"}>
            {section.label && (
              <tr>
                <TableHead colSpan={5} scope="rowgroup" className="h-auto pt-5 pb-1.5">
                  {section.label} <span className="font-normal text-muted-foreground tabular-nums">{number.format(section.rows.length)}</span>
                </TableHead>
              </tr>
            )}
            {section.rows.map((r) =>
              r.kind === "folder" ? (
                <TableRow key={`p:${r.prefix}`} className="relative">
                  <TableCell className="max-w-0 w-1/2">
                    <TruncatedTip text={r.prefix.slice(prefix.length)}>
                      <Link
                        to="/buckets/$bucket"
                        params={{ bucket }}
                        search={{ prefix: r.prefix, ...sortSearch(sort), ...groupSearch(group) }}
                        className="flex min-w-0 items-center gap-2 font-mono font-[510] after:absolute after:inset-0"
                      >
                        <FolderIcon aria-hidden="true" className="size-4 shrink-0 text-faint" />
                        <span data-truncate className="truncate">
                          {r.prefix.slice(prefix.length)}
                        </span>
                      </Link>
                    </TruncatedTip>
                  </TableCell>
                  <TableCell />
                  <TableCell className="hidden md:table-cell" />
                  <TableCell className="hidden sm:table-cell" />
                  <TableCell />
                </TableRow>
              ) : (
                <TableRow key={`o:${r.object.key}`}>
                  <TableCell className="max-w-0 w-1/2">
                    <span className="flex min-w-0 items-center gap-2 font-mono">
                      <FileIcon aria-hidden="true" className="size-4 shrink-0 text-faint" />
                      <span className="truncate" title={r.object.key.slice(prefix.length)}>
                        {r.object.key.slice(prefix.length)}
                      </span>
                    </span>
                  </TableCell>
                  <TableCell className="text-right whitespace-nowrap text-muted-foreground tabular-nums">{formatBytes(r.object.size)}</TableCell>
                  <TableCell className="hidden max-w-40 truncate text-muted-foreground md:table-cell">{r.object.content_type}</TableCell>
                  <TableCell className="hidden text-right whitespace-nowrap text-muted-foreground sm:table-cell">
                    <span suppressHydrationWarning>{timeAgo(r.object.last_modified)}</span>
                  </TableCell>
                  <TableCell className="w-0 whitespace-nowrap text-right">
                    <div className="flex justify-end gap-1">
                      <a
                        href={objectURL(bucket, r.object.key)}
                        download
                        aria-label={`Download ${r.object.key}`}
                        className={buttonVariants({ variant: "ghost", size: "icon-sm" })}
                      >
                        <DownloadIcon aria-hidden="true" />
                      </a>
                      {canWrite && (
                        <Confirm
                          trigger={<Trash2Icon aria-hidden="true" />}
                          triggerStyle={{ size: "icon-sm", label: `Delete ${r.object.key}` }}
                          title={`Delete ${r.object.key.slice(prefix.length)}?`}
                          description="There is no versioning or trash for objects, so this can't be undone."
                          actionLabel="Delete object"
                          agentConfirm={`Deletes the object ${r.object.key} from ${bucket} for good`}
                          onConfirm={() => onDelete(r.object.key)}
                        />
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              ),
            )}
          </tbody>
        ))}
      </Table>
      {footer}
    </>
  )
}
