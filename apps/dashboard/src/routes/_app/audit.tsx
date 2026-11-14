import { createFileRoute, redirect } from "@tanstack/react-router"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ComingNext } from "@/components/app/states"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"

export const Route = createFileRoute("/_app/audit")({
  beforeLoad: ({ context }) => {
    if (!can(context.viewer.role, "write")) throw redirect({ to: "/" })
  },
  head: () => ({ meta: [{ title: pageTitle("Audit log") }] }),
  component: () => (
    <>
      <PageHeader>
        <PageTitle>Audit log</PageTitle>
      </PageHeader>
      <PageBody>
        <ComingNext />
      </PageBody>
    </>
  ),
})
