import { Link, createFileRoute, useRouter } from "@tanstack/react-router"
import { MailXIcon } from "lucide-react"
import { AuthLayout, InviteForm } from "@/components/auth/auth-forms"
import { buttonVariants } from "@/components/ui/button"
import { Empty } from "@/components/ui/surface"
import { acceptInvite, getInvite } from "@/fns/session"
import { pageTitle } from "@/lib/paths"
import { forgetSession } from "@/lib/session-cache"

export const Route = createFileRoute("/invite/$token")({
  loader: ({ params }) => (params.token.length >= 10 && params.token.length <= 200 ? getInvite({ data: { token: params.token } }) : null),
  head: () => ({ meta: [{ title: pageTitle("Join") }] }),
  component: InvitePage,
})

function InvitePage() {
  const invite = Route.useLoaderData()
  const { token } = Route.useParams()
  const router = useRouter()
  if (!invite) {
    return (
      <AuthLayout title="Join Lumbung">
        <Empty icon={<MailXIcon />} title="This invite expired or was already used" description="Ask an owner for a new one.">
          <Link to="/login" className={buttonVariants({ variant: "secondary" })}>
            Sign in
          </Link>
        </Empty>
      </AuthLayout>
    )
  }
  return (
    <AuthLayout title="Join Lumbung">
      <InviteForm
        email={invite.email}
        owner={invite.owner}
        onAccept={async (values) => {
          await acceptInvite({ data: { token, ...values } })
          forgetSession()
          await router.navigate({ to: "/" })
        }}
      />
    </AuthLayout>
  )
}
