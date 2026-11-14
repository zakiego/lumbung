import { Link, createFileRoute } from "@tanstack/react-router"
import { KeyRoundIcon } from "lucide-react"
import { ConnectDetails, SnippetTabs } from "@/components/app/connect"
import { DbFrame, useDb } from "@/components/app/db-frame"
import { SettingsSection } from "@/components/app/db-settings"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { buttonVariants } from "@/components/ui/button"
import { getConnectInfo } from "@/fns/databases"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { dbSnippets, libsqlUrl } from "@/lib/snippets"

export const Route = createFileRoute("/_app/db/$db/connect")({
  loader: () => getConnectInfo(),
  head: ({ params }) => ({ meta: [{ title: pageTitle("Connect", params.db) }] }),
  component: ConnectPage,
  pendingComponent: () => (
    <DbFrame section="connect">
      <RowsSkeleton />
    </DbFrame>
  ),
  errorComponent: ({ error }) => (
    <DbFrame section="connect">
      <ErrorState error={error} title="Couldn't load connection details" />
    </DbFrame>
  ),
})

function ConnectPage() {
  const { database, branch, branchParam } = useDb()
  const { engineUrl: url } = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const db = database.name

  return (
    <DbFrame section="connect">
      <div className="flex max-w-4xl flex-col">
        <SettingsSection title="Details">
          <ConnectDetails
            rows={[
              { label: "Engine URL", value: url },
              { label: "libSQL URL", value: libsqlUrl(url, db, branch) },
              { label: "MCP URL", value: `${url}/mcp` },
              { label: "Database", value: db },
              { label: "Branch", value: branch },
            ]}
          />
        </SettingsSection>
        <SettingsSection title="Token" description="Snippets read it from LUMBUNG_TOKEN.">
          {can(viewer.role, "admin") ? (
            <Link to="/tokens" className={buttonVariants({ variant: "secondary", className: "self-start" })}>
              <KeyRoundIcon aria-hidden="true" />
              Create token
            </Link>
          ) : (
            <p className="text-[13px] text-muted-foreground">Ask an admin for a token.</p>
          )}
        </SettingsSection>
        <SettingsSection title="Code">
          <SnippetTabs label="Connect with" snippets={dbSnippets({ url, db, branch, pinBranch: !!branchParam })} />
        </SettingsSection>
      </div>
    </DbFrame>
  )
}
