import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { InviteDialog, InviteTable, MemberTable } from "@/components/app/members"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { ErrorState, RowsSkeleton } from "@/components/app/states"
import { toast } from "@/components/ui/toast"
import { cancelInvite, changeAccess, getMembers, inviteMember, removeUser } from "@/fns/admin"
import { pageTitle } from "@/lib/paths"
import type { MemberAccess } from "@/server/users"

export const Route = createFileRoute("/_app/_instance/members")({
  beforeLoad: ({ context }) => {
    if (!context.viewer.owner) throw redirect({ to: "/" })
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
  const { members, invites, workspaces, deleted } = Route.useLoaderData()
  const revokedNote = (n: number) => (n > 0 ? `, and revoked ${n} ${n === 1 ? "token" : "tokens"} they made` : "")
  const { viewer, workspace } = Route.useRouteContext()
  const router = useRouter()

  async function invite(email: string, access: MemberAccess) {
    const { url } = await inviteMember({ data: { email, access } })
    return url
  }

  return (
    <Frame actions={<InviteDialog workspaces={workspaces} current={workspace?.id} onInvite={invite} onDone={() => void router.invalidate()} />}>
      <div className="flex flex-col gap-10">
        <MemberTable
          members={members}
          me={viewer.id}
          workspaces={workspaces}
          deleted={deleted}
          onAccess={async (m, access) => {
            const { revoked } = await changeAccess({ data: { id: m.id, access } })
            toast.success(`Changed access for ${m.name || m.email}${revokedNote(revoked)}`)
            await router.invalidate()
          }}
          onRemove={async (m) => {
            const { revoked } = await removeUser({ data: { id: m.id } })
            toast.success(`Removed ${m.name || m.email}${revokedNote(revoked)}`)
            await router.invalidate()
          }}
        />
        <section aria-labelledby="invites-title" className="flex flex-col gap-3">
          <h2 id="invites-title" className="text-[13px] font-[590]">
            Invites
          </h2>
          <InviteTable
            invites={invites}
            workspaces={workspaces}
            deleted={deleted}
            // A workspace deleted since the invite was made is left out of the new one.
            onResend={(i) => invite(i.email, i.owner ? { owner: true } : { owner: false, grants: i.grants.filter((g) => workspaces.some((w) => w.id === g.workspace)) })}
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
