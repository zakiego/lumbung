import { Link, useNavigate } from "@tanstack/react-router"
import { BoxIcon, ServerIcon } from "lucide-react"
import type { ReactNode } from "react"
import { Tabs, TabsList, TabsPanel, TabsTab } from "@/components/ui/tabs"
import { CONTAINER, PageBody, PageHeader, PageTitle } from "./shell"
import { useWs } from "./workspace"

export type Scope = "workspace" | "instance"

/**
 * A page that an owner can read for the workspace or for the whole
 * instance, like tokens and the audit log. Everyone else gets the
 * workspace's alone, with no tabs.
 */
export function ScopeFrame({
  title,
  to,
  scope,
  owner,
  actions,
  children,
}: {
  title: string
  to: "/w/$ws/tokens" | "/w/$ws/audit"
  scope: Scope
  owner: boolean
  actions?: ReactNode
  children: ReactNode
}) {
  const ws = useWs()
  const navigate = useNavigate()
  const header = (
    <PageHeader actions={actions}>
      <PageTitle>{title}</PageTitle>
    </PageHeader>
  )
  if (!owner) {
    return (
      <>
        {header}
        <PageBody>{children}</PageBody>
      </>
    )
  }
  return (
    <>
      {header}
      <Tabs value={scope} onValueChange={(v) => void navigate({ to, params: { ws }, search: v === "instance" ? { scope: "instance" } : {} })}>
        <div className="shadow-[inset_0_-1px_0_var(--border)]">
          <div className={CONTAINER}>
            <TabsList aria-label={`${title} scope`} className="w-full shadow-none">
              <TabsTab value="workspace" icon={<BoxIcon />} render={<Link to={to} params={{ ws }} search={{}} />}>
                Workspace
              </TabsTab>
              <TabsTab value="instance" icon={<ServerIcon />} render={<Link to={to} params={{ ws }} search={{ scope: "instance" }} />}>
                Instance
              </TabsTab>
            </TabsList>
          </div>
        </div>
        <TabsPanel value={scope}>
          <PageBody>{children}</PageBody>
        </TabsPanel>
      </Tabs>
    </>
  )
}
