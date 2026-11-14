import { createFileRoute, redirect, useRouter } from "@tanstack/react-router"
import { z } from "zod"
import { AuthLayout, LoginForm } from "@/components/auth/auth-forms"
import { getSession } from "@/fns/session"
import { authClient, unwrap } from "@/lib/auth-client"
import { pageTitle, safeNext } from "@/lib/paths"
import { forgetSession } from "@/lib/session-cache"

export const Route = createFileRoute("/login")({
  validateSearch: z.object({ next: z.string().max(500).optional() }),
  beforeLoad: async ({ search }) => {
    const session = await getSession()
    if (session.needsSetup) throw redirect({ to: "/setup" })
    if (session.viewer) throw redirect({ href: safeNext(search.next) })
  },
  head: () => ({ meta: [{ title: pageTitle("Sign in") }] }),
  component: LoginPage,
})

function LoginPage() {
  const router = useRouter()
  const { next } = Route.useSearch()
  const done = async () => {
    forgetSession()
    await router.navigate({ href: safeNext(next) })
  }
  return (
    <AuthLayout title="Sign in to Lumbung">
      <LoginForm
        onSignIn={async (email, password) => {
          unwrap(await authClient().signIn.email({ email, password }))
          await done()
        }}
        onPasskey={async () => {
          unwrap(await authClient().signIn.passkey())
          await done()
        }}
      />
    </AuthLayout>
  )
}
