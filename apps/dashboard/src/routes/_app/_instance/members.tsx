import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { InviteDialog, InviteTable, MemberTable } from "@/components/app/members"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { toast, withToast } from "@/components/ui/toast"
import { cancelInvite, changeRole, getMembers, inviteMember, removeUser } from "@/fns/admin"
import { pageTitle } from "@/lib/paths"
import { ROLE_LABEL, can } from "@/lib/roles"

export const Route = createFileRoute("/_app/members")({
  beforeLoad: ({ context }) => {
    if (!can(context.viewer.role, "admin")) throw redirect({ to: "/" })
  },
  loader: () => getMembers(),
  head: () => ({ meta: [{ title: pageTitle("Members") }] }),
  component: MembersPage,
  pendingComponent: () => (
    <Frame>
      <RowsSkeleton />
    </Frame>
  ),
  errorComponent: ({ error }) => (
    <Frame>
      <ErrorState error={error} title="Couldn't load members" />
    </Frame>
  ),
})

function Frame({ children, actions }: { children: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <>
      <PageHeader actions={actions}>
        <PageTitle>Members</PageTitle>
      </PageHeader>
      <PageBody>{children}</PageBody>
    </>
  )
}

function MembersPage() {
  const { members, invites } = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const router = useRouter()

  async function invite(email: string, role: (typeof members)[number]["role"]) {
    const { url } = await inviteMember({ data: { email, role } })
    return url
  }

  return (
    <Frame actions={<InviteDialog onInvite={invite} onDone={() => void router.invalidate()} />}>
      <div className="flex flex-col gap-10">
        <MemberTable
          members={members}
          me={viewer.id}
          onRole={async (m, role) => {
            await withToast(changeRole({ data: { id: m.id, role } }), {
              loading: "Saving…",
              success: `${m.name || m.email} is ${ROLE_LABEL[role]} now`,
              error: "Couldn't change the role",
            })
            await router.invalidate()
          }}
          onRemove={async (m) => {
            await removeUser({ data: { id: m.id } })
            toast.success(`Removed ${m.name || m.email}`)
            await router.invalidate()
          }}
        />
        <section aria-labelledby="invites-title" className="flex flex-col gap-3">
          <h2 id="invites-title" className="text-[13px] font-[590]">
            Invites
          </h2>
          <InviteTable
            invites={invites}
            onResend={(i) => invite(i.email, i.role)}
            onChanged={() => void router.invalidate()}
            onCancel={async (i) => {
              await cancelInvite({ data: { id: i.id } })
              toast.success("Invite canceled")
              await router.invalidate()
            }}
          />
        </section>
      </div>
    </Frame>
  )
}
