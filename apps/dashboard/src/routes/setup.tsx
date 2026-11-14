import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { AuthLayout, SetupForm } from "@/components/auth/auth-forms"
import { getSession, setupOwner } from "@/fns/session"
import { pageTitle } from "@/lib/paths"
import { forgetSession } from "@/lib/session-cache"

export const Route = createFileRoute("/setup")({
  beforeLoad: async () => {
    const session = await getSession()
    if (!session.needsSetup) throw redirect({ to: session.viewer ? "/" : "/login" })
  },
  head: () => ({ meta: [{ title: pageTitle("Set up") }] }),
  component: SetupPage,
})

function SetupPage() {
  const router = useRouter()
  return (
    <AuthLayout title="Set up Lumbung">
      <SetupForm
        onSetup={async (values) => {
          await setupOwner({ data: values })
          forgetSession()
          await router.navigate({ to: "/" })
        }}
      />
    </AuthLayout>
  )
}
