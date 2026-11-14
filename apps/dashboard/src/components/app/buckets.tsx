import type { BucketOut, ObjectOut } from "@lumbung/client"
import { Link } from "@tanstack/react-router"
import { ArchiveIcon, DownloadIcon, FileIcon, FolderIcon, GlobeIcon, Trash2Icon, UploadIcon } from "lucide-react"
import { type ReactNode, useRef, useState } from "react"
import { Button, buttonVariants } from "@/components/ui/button"
import { Badge, Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { toast } from "@/components/ui/toast"
import { TruncatedTip } from "@/components/ui/tooltip"
import { formatBytes, number, timeAgo } from "@/lib/format"
import { APP_BASE } from "@/lib/paths"
import { Confirm, errorText } from "./form-dialog"
import { NameDialog } from "./name-dialog"

export const BUCKET_PATTERN = "[a-z0-9][a-z0-9\\-]{1,61}[a-z0-9]"

export function CreateBucketDialog({ onCreate, variant }: { onCreate: (name: string) => Promise<unknown>; variant?: "default" | "secondary" }) {
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
  if (file.size <= PART_BYTES) {
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

export function ObjectTable({
  bucket,
  prefix,
  objects,
  prefixes,
  canWrite,
  onDelete,
  footer,
}: {
  bucket: string
  prefix: string
  objects: ObjectOut[]
  prefixes: string[]
  canWrite: boolean
  onDelete: (key: string) => Promise<unknown>
  footer?: ReactNode
}) {
  // A key equal to the folder itself is a folder marker some S3 tools make:
  // it has no name of its own here, so it isn't listed.
  const rows: Row[] = [
    ...prefixes.map((p): Row => ({ kind: "folder", prefix: p })),
    ...objects.filter((o) => o.key !== prefix).map((o): Row => ({ kind: "object", object: o })),
  ]
  if (rows.length === 0) {
    return <Empty icon={<FolderIcon />} title={prefix ? "This folder is empty" : "No objects yet"} description="Upload files here, or put them with an S3 client, the CLI or the SDK." />
  }
  return (
    <>
      <Table>
        <thead>
          <tr>
            <TableHead>Name</TableHead>
            <TableHead className="text-right">Size</TableHead>
            <TableHead className="hidden md:table-cell">Type</TableHead>
            <TableHead className="hidden text-right sm:table-cell">Modified</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) =>
            r.kind === "folder" ? (
              <TableRow key={`p:${r.prefix}`} className="relative">
                <TableCell className="max-w-0 w-1/2">
                  <TruncatedTip text={r.prefix.slice(prefix.length)}>
                    <Link
                      to="/buckets/$bucket"
                      params={{ bucket }}
                      search={{ prefix: r.prefix }}
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
      </Table>
      {footer}
    </>
  )
}
