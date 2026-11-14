import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { CircleAlertIcon, CircleCheckIcon } from "lucide-react"
import type { ReactNode } from "react"
import { CopyButton } from "@/components/app/connect"
import { SettingsSection } from "@/components/app/db-settings"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { DeletedWorkspaces } from "@/components/app/workspace-settings"
import { StatusText } from "@/components/ui/surface"
import { toast } from "@/components/ui/toast"
import { getInstance } from "@/fns/admin"
import { listDeletedWorkspaces, restoreWorkspace } from "@/fns/workspaces"
import { pageTitle } from "@/lib/paths"
import { forgetSession } from "@/lib/session-cache"

export const Route = createFileRoute("/_app/_instance/settings")({
  beforeLoad: ({ context }) => {
    if (!context.viewer.owner) throw redirect({ to: "/" })
  },
  loader: async () => {
    const [instance, deleted] = await Promise.all([getInstance(), listDeletedWorkspaces().catch(() => null)])
    return { ...instance, deleted }
  },
  head: () => ({ meta: [{ title: pageTitle("Instance settings") }] }),
  component: SettingsPage,
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton rows={3} />
    </Frame>
  ),
  errorComponent: ({ error }) => (
    <Frame>
      <ErrorState error={error} title="Couldn't load settings" />
    </Frame>
  ),
})

function Frame({ children }: { children: ReactNode }) {
  return (
    <>
      <PageHeader>
        <PageTitle>Instance settings</PageTitle>
      </PageHeader>
      <PageBody>
        <div className="max-w-4xl">{children}</div>
      </PageBody>
    </>
  )
}

function SettingsPage() {
  const data = Route.useLoaderData()
  const { engineUrl, publicUrl } = data
  const health = data.health
  const healthy = health?.status === "ok"
  const router = useRouter()
  return (
    <Frame>
      <SettingsSection title="Engine">
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-8 gap-y-3 text-[13px]">
          <Row label="Status">
            {healthy ? (
              <StatusText tone="success" icon={<CircleCheckIcon aria-hidden="true" />}>
                Healthy
              </StatusText>
            ) : (
              <StatusText tone="destructive" icon={<CircleAlertIcon aria-hidden="true" />}>
                {health ? `Unhealthy (${health.status})` : "Unreachable"}
              </StatusText>
            )}
          </Row>
          <Row label="Version">
            <span className="font-mono">{health?.version ?? "-"}</span>
          </Row>
          <Row label="Meta version">
            <span className="font-mono tabular-nums">{health?.meta_version ?? "-"}</span>
          </Row>
          <Row label="Engine URL">
            <Url value={engineUrl} label="Copy engine URL" />
          </Row>
          <Row label="Public URL">
            <Url value={publicUrl} label="Copy public URL" />
          </Row>
        </dl>
      </SettingsSection>
      {data.deleted && data.deleted.length > 0 && (
        <SettingsSection title="Deleted workspaces">
          <DeletedWorkspaces
            workspaces={data.deleted}
            onRestore={async (w, name) => {
              const back = await restoreWorkspace({ data: { id: w.id, name } })
              toast.success(`Restored ${back.name}`)
              forgetSession()
              await router.invalidate()
            }}
          />
        </SettingsSection>
      )}
    </Frame>
  )
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="flex h-7 items-center text-muted-foreground">{label}</dt>
      <dd className="flex h-7 min-w-0 items-center">{children}</dd>
    </>
  )
}

function Url({ value, label }: { value: string; label: string }) {
  return (
    <span className="flex min-w-0 items-center gap-1">
      <code className="truncate font-mono" title={value}>
        {value}
      </code>
      <CopyButton text={value} label={label} className="shrink-0" />
    </span>
  )
}
