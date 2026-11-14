import { createFileRoute, redirect, useNavigate, useRouter } from "@tanstack/react-router"
import { Trash2Icon } from "lucide-react"
import { type FormEvent, useState } from "react"
import { SettingsSection } from "@/components/app/db-settings"
import { errorText } from "@/components/app/form-dialog"
import { BucketFrame } from "@/components/app/bucket-frame"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { TypedConfirm } from "@/components/ui/alert-dialog"
import { Button, buttonVariants } from "@/components/ui/button"
import { Switch } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/input"
import { toast } from "@/components/ui/toast"
import { deleteBucket, getBucket, updateBucket } from "@/fns/buckets"
import { number } from "@/lib/format"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { useWs } from "@/components/app/workspace"
import { MoveForm } from "@/components/app/workspace-settings"
import { moveBucket } from "@/fns/workspaces"

export const Route = createFileRoute("/_app/w/$ws/buckets/$bucket/settings")({
  beforeLoad: ({ context, params }) => {
    if (!can(context.viewer.role, "admin")) throw redirect({ to: "/w/$ws/buckets/$bucket", params })
  },
  loader: ({ params }) => getBucket({ data: { ws: params.ws, bucket: params.bucket } }),
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton />
    </Frame>
  ),
  head: ({ params }) => ({ meta: [{ title: pageTitle("Settings", params.bucket) }] }),
  component: SettingsPage,
  errorComponent: ({ error }) => (
    <Frame>
      <ErrorState error={error} title="Couldn't load this bucket" />
    </Frame>
  ),
})

function Frame({ children }: { children: React.ReactNode }) {
  const { bucket } = Route.useParams()
  return (
    <BucketFrame bucket={bucket} section="settings">
      {children}
    </BucketFrame>
  )
}

function SettingsPage() {
  const ws = useWs()
  const { bucket, s3Endpoint } = Route.useLoaderData()
  const { viewer, workspace, workspaces } = Route.useRouteContext()
  const others = (workspaces ?? []).filter((w) => w.id !== workspace.id)
  const router = useRouter()
  const navigate = useNavigate()
  const name = bucket.name

  return (
    <Frame>
      <div className="flex max-w-4xl flex-col">
        <SettingsSection title="Public read" description="Anyone can download objects without a token. Listing, uploading and deleting still need one.">
          <PublicReadForm
            value={bucket.public_read}
            onChange={async (publicRead) => {
              await updateBucket({ data: { ws, bucket: name, publicRead } })
              toast.success(publicRead ? `${name} is public` : `${name} is private`)
              await router.invalidate()
            }}
          />
          {bucket.public_read && (
            <p className="text-[13px] text-muted-foreground [overflow-wrap:anywhere]">
              Objects are at <span className="font-mono text-foreground">{`${s3Endpoint}/${name}/<key>`}</span>
            </p>
          )}
        </SettingsSection>
        <SettingsSection title="CORS origins" description="Sites that may call the S3 API from a browser, for uploads with presigned URLs. One per line.">
          <CorsForm
            origins={bucket.cors_origins}
            onSave={async (corsOrigins) => {
              await updateBucket({ data: { ws, bucket: name, corsOrigins } })
              toast.success("Saved CORS origins")
              await router.invalidate()
            }}
          />
        </SettingsSection>
        {viewer.owner && others.length > 0 && (
          <SettingsSection title="Move">
            <MoveForm
              what="bucket"
              name={name}
              targets={others}
              onMove={async (to) => {
                await moveBucket({ data: { ws, bucket: name, to: to.id } })
                toast.success(`Moved ${name} to ${to.name}`)
                await navigate({ to: "/w/$ws/buckets/$bucket", params: { ws: to.name, bucket: name } })
                await router.invalidate()
              }}
            />
          </SettingsSection>
        )}
        <SettingsSection
          title="Delete bucket"
          description={
            bucket.object_count > 0
              ? `Only an empty bucket can be deleted. This one holds ${number.format(bucket.object_count)} ${bucket.object_count === 1 ? "object" : "objects"}.`
              : "Its tokens are revoked. This can't be undone."
          }
          tone="danger"
        >
          <TypedConfirm
            trigger={
              <>
                <Trash2Icon aria-hidden="true" />
                Delete bucket
              </>
            }
            triggerClassName={buttonVariants({ variant: "destructive", className: "self-start" })}
            title={`Delete ${name}?`}
            description="The bucket must be empty. Tokens limited to it are revoked."
            confirmText={name}
            actionLabel="Delete for good"
            agentConfirm={`Deletes the empty bucket ${name} and revokes its tokens`}
            onConfirm={async () => {
              await deleteBucket({ data: { ws, bucket: name } })
              toast.success(`Deleted ${name}`)
              await navigate({ to: "/w/$ws/buckets", params: { ws } })
              await router.invalidate()
            }}
          />
        </SettingsSection>
      </div>
    </Frame>
  )
}

function PublicReadForm({ value, onChange }: { value: boolean; onChange: (next: boolean) => Promise<unknown> }) {
  const [pending, setPending] = useState(false)
  return (
    <label className="flex items-center gap-3 text-[13px]">
      <Switch
        checked={value}
        disabled={pending}
        onCheckedChange={async (next: boolean) => {
          setPending(true)
          try {
            await onChange(next)
          } catch (e) {
            toast.error({ title: "Couldn't change public read", description: errorText(e) })
          } finally {
            setPending(false)
          }
        }}
      />
      {value ? "Public read is on" : "Public read is off"}
    </label>
  )
}

function CorsForm({ origins, onSave }: { origins: string[]; onSave: (next: string[]) => Promise<unknown> }) {
  const [text, setText] = useState(origins.join("\n"))
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)
  const next = text
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter(Boolean)
  const changed = next.join("\n") !== origins.join("\n")

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (pending || !changed) return
    setPending(true)
    setError("")
    try {
      await onSave(next)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setPending(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <Field invalid={error ? true : undefined}>
        <FieldLabel className="sr-only">CORS origins</FieldLabel>
        <Textarea
          value={text}
          onValueChange={(v: string) => {
            setText(v)
            setError("")
          }}
          rows={4}
          placeholder={"https://app.example.com\nhttp://localhost:5173"}
          spellCheck={false}
          className="max-w-lg font-mono"
        />
        <FieldDescription>Each is scheme://host[:port], or * for any site.</FieldDescription>
        <FieldError match={!!error}>{error}</FieldError>
      </Field>
      <Button type="submit" variant="secondary" className="self-start" disabled={pending || !changed}>
        {pending ? "Saving…" : "Save"}
      </Button>
    </form>
  )
}
