import { createFileRoute, redirect } from "@tanstack/react-router"
import { CircleAlertIcon, CircleCheckIcon } from "lucide-react"
import { type ReactNode, useEffect, useState } from "react"
import { CopyButton } from "@/components/app/connect"
import { SettingsSection } from "@/components/app/db-settings"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { Field, FieldLabel } from "@/components/ui/field"
import { SegmentedControl, SegmentedControlItem } from "@/components/ui/segmented-control"
import { StatusText } from "@/components/ui/surface"
import { getInstance } from "@/fns/admin"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { type Theme, applyTheme, readTheme } from "@/lib/theme"

export const Route = createFileRoute("/_app/settings")({
  beforeLoad: ({ context }) => {
    if (!can(context.viewer.role, "admin")) throw redirect({ to: "/" })
  },
  loader: () => getInstance(),
  head: () => ({ meta: [{ title: pageTitle("Settings") }] }),
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
        <PageTitle>Settings</PageTitle>
      </PageHeader>
      <PageBody className="max-w-4xl">{children}</PageBody>
    </>
  )
}

function SettingsPage() {
  const data = Route.useLoaderData()
  const { engineUrl, publicUrl } = data
  const health = data.health
  const healthy = health?.status === "ok"
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
      <SettingsSection title="Appearance">
        <ThemeField />
      </SettingsSection>
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
      <span className="truncate font-mono" title={value}>
        {value}
      </span>
      <CopyButton text={value} label={label} className="shrink-0" />
    </span>
  )
}

function ThemeField() {
  const [theme, setTheme] = useState<Theme>("system")
  useEffect(() => setTheme(readTheme()), [])
  return (
    <Field>
      <FieldLabel>Theme</FieldLabel>
      <SegmentedControl
        value={theme}
        onValueChange={(v) => {
          if (v !== "light" && v !== "dark" && v !== "system") return
          setTheme(v)
          applyTheme(v)
        }}
      >
        <SegmentedControlItem value="light">Light</SegmentedControlItem>
        <SegmentedControlItem value="dark">Dark</SegmentedControlItem>
        <SegmentedControlItem value="system">System</SegmentedControlItem>
      </SegmentedControl>
    </Field>
  )
}
