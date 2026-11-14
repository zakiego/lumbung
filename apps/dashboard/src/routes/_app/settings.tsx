import { createFileRoute, redirect } from "@tanstack/react-router"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ComingNext } from "@/components/app/states"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

export const Route = createFileRoute("/_app/settings")({
  beforeLoad: ({ context }) => {
    if (!can(context.viewer.role, "admin")) throw redirect({ to: "/" })
  },
  head: () => ({ meta: [{ title: pageTitle("Settings") }] }),
  component: () => (
    <>
      <PageHeader>
        <PageTitle>Settings</PageTitle>
      </PageHeader>
      <PageBody>
        <ComingNext />
      </PageBody>
    </>
  ),
})
