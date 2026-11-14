import type { ObjectOut } from "@lumbung/client"
import { Drawer, DrawerBackdrop, DrawerClose, DrawerContent, DrawerTitle, type DrawerController } from "@ui-sdk/react/drawer"
import { Link } from "@tanstack/react-router"
import { DownloadIcon, ExternalLinkIcon, Trash2Icon, XIcon } from "lucide-react"
import { type ReactNode, useSyncExternalStore } from "react"
import { buttonVariants } from "@/components/ui/button"
import { formatBytes, formatDateTime } from "@/lib/format"
import { objectURL, publicObjectURL } from "@/lib/objects"
import { cn } from "@/lib/utils"
import { CopyButton } from "./connect"
import { Confirm } from "./form-dialog"
import { useWs } from "./workspace"

export type ObjectAccess = { publicRead: boolean; s3Endpoint: string; canAdmin: boolean }

const PREVIEW_TYPES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"])
const PREVIEW_MAX_BYTES = 10 * 1024 * 1024

let query: MediaQueryList | null | undefined
const wide = () => (query ??= typeof matchMedia === "function" ? matchMedia("(min-width: 768px)") : null)
function useWide(): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const m = wide()
      m?.addEventListener("change", onChange)
      return () => m?.removeEventListener("change", onChange)
    },
    () => !!wide()?.matches,
    () => false,
  )
}

export function ObjectDetails({
  controller,
  bucket,
  prefix,
  access,
  canWrite,
  onDelete,
}: {
  controller: DrawerController<ObjectOut>
  bucket: string
  prefix: string
  access?: ObjectAccess
  canWrite: boolean
  onDelete: (key: string) => Promise<unknown>
}) {
  const ws = useWs()
  const side = useWide() ? "end" : "bottom"
  return (
    <Drawer controller={controller} side={side}>
      <DrawerBackdrop className="fixed inset-0 bg-overlay" />
      <DrawerContent<ObjectOut>
        className={cn(
          "flex flex-col bg-background text-foreground outline-none",
          side === "end" ? "h-full w-[min(400px,100vw)] border-l" : "max-h-[85svh] w-full rounded-t-xl border-t",
        )}
      >
        {({ payload: o }) =>
          o && (
            <>
              {side === "bottom" && <div aria-hidden="true" className="mx-auto mt-2 h-1 w-9 shrink-0 rounded-full bg-border" />}
              <div className="flex items-start gap-2 px-5 pt-4 pb-3">
                <DrawerTitle className="line-clamp-3 min-w-0 flex-1 pt-1 font-mono text-[14px] font-[590] [overflow-wrap:anywhere]">{o.key.slice(prefix.length)}</DrawerTitle>
                <DrawerClose aria-label="Close" className={buttonVariants({ variant: "ghost", size: "icon-sm", className: "-mr-1.5" })}>
                  <XIcon aria-hidden="true" />
                </DrawerClose>
              </div>
              <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto overscroll-contain px-5 pb-5">
                {PREVIEW_TYPES.has(o.content_type) && o.size <= PREVIEW_MAX_BYTES && (
                  <img src={objectURL(ws, bucket, o.key)} alt="" decoding="async" className="h-40 w-full shrink-0 rounded-lg bg-code object-contain outline outline-1 -outline-offset-1 outline-white/10 sm:h-60" />
                )}
                <PublicLink bucket={bucket} objectKey={o.key} access={access} />
                <Rows>
                  {(prefix || o.key.length > 80) && <Row label="Key" mono value={o.key} />}
                  <Row label="Size" value={formatBytes(o.size)} />
                  <Row label="Type" value={o.content_type} />
                  <Row label="Modified" value={formatDateTime(o.last_modified)} />
                  <Row label="ETag" mono value={o.etag} />
                </Rows>
                <Group title="Headers" entries={o.headers} />
                <Group title="Metadata" entries={o.metadata} />
              </div>
              <div className="flex gap-2 border-t px-5 py-4 pb-[max(16px,env(safe-area-inset-bottom))]">
                <a href={objectURL(ws, bucket, o.key)} download className={buttonVariants({ variant: "secondary", className: "flex-1" })}>
                  <DownloadIcon aria-hidden="true" />
                  Download
                </a>
                {canWrite && (
                  <Confirm
                    trigger={
                      <>
                        <Trash2Icon aria-hidden="true" />
                        Delete
                      </>
                    }
                    triggerStyle={{ variant: "ghost", className: "text-destructive hover:text-destructive" }}
                    title={`Delete ${o.key.slice(prefix.length)}?`}
                    description="There is no versioning or trash for objects, so this can't be undone."
                    actionLabel="Delete object"
                    agentConfirm={`Deletes the object ${o.key} from ${bucket} for good`}
                    onConfirm={async () => {
                      await onDelete(o.key)
                      controller.close()
                    }}
                  />
                )}
              </div>
            </>
          )
        }
      </DrawerContent>
    </Drawer>
  )
}

function PublicLink({ bucket, objectKey, access }: { bucket: string; objectKey: string; access?: ObjectAccess }) {
  const ws = useWs()
  if (!access) return null
  const url = access.publicRead ? publicObjectURL(access.s3Endpoint, bucket, objectKey) : null
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-xs font-[510] text-muted-foreground">Public link</h3>
      {url ? (
        <div className="flex items-start gap-1 rounded-lg border bg-code py-1.5 pr-1.5 pl-3">
          <code className="min-w-0 flex-1 py-1 font-mono text-[13px] leading-[1.5] break-all text-foreground/90 select-all">{url}</code>
          <CopyButton text={url} label="Copy public link" />
          <a href={url} target="_blank" rel="noreferrer" aria-label="Open public link" className={buttonVariants({ variant: "ghost", size: "icon-sm", className: "rounded-full" })}>
            <ExternalLinkIcon aria-hidden="true" />
          </a>
        </div>
      ) : (
        <p className="text-[13px] text-muted-foreground">
          {access.publicRead ? "This key can't be linked." : "Off, the bucket is private."}
          {!access.publicRead && access.canAdmin && (
            <>
              {" "}
              <Link to="/w/$ws/buckets/$bucket/settings" params={{ ws, bucket }} className="text-foreground underline decoration-faint underline-offset-2 hover:decoration-foreground">
                Bucket settings
              </Link>
            </>
          )}
        </p>
      )}
    </section>
  )
}

function Rows({ children }: { children: ReactNode }) {
  return <dl className="flex flex-col text-[13px]">{children}</dl>
}

function Row({ label, value, mono, stacked }: { label: string; value: string; mono?: boolean; stacked?: boolean }) {
  return (
    <div className={cn("flex border-b py-2.5 last:border-b-0", stacked ? "flex-col gap-0.5" : "items-baseline justify-between gap-6")}>
      <dt className={cn("text-muted-foreground [overflow-wrap:anywhere]", !stacked && "max-w-[45%] shrink-0")}>{label}</dt>
      <dd className={cn("min-w-0 [overflow-wrap:anywhere]", !stacked && "text-right", mono && "font-mono text-[12px]")}>{value}</dd>
    </div>
  )
}

function Group({ title, entries }: { title: string; entries: Record<string, string> | undefined }) {
  const list = Object.entries(entries ?? {})
  if (list.length === 0) return null
  return (
    <section className="flex flex-col gap-1">
      <h3 className="text-xs font-[510] text-muted-foreground">{title}</h3>
      <Rows>
        {list.map(([k, v]) => (
          <Row key={k} label={k} value={v} stacked />
        ))}
      </Rows>
    </section>
  )
}
