import { createFileRoute } from "@tanstack/react-router"
import { FingerprintIcon, KeyRoundIcon } from "lucide-react"
import { type FormEvent, useCallback, useEffect, useState } from "react"
import { NewPassword, checkPassword } from "@/components/auth/auth-forms"
import { PageBody, PageHeader, PageTitle } from "@/components/app/shell"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { PasswordInput } from "@/components/ui/input"
import { Card, CardContent, CardHeader, CardTitle, Skeleton, StatusText } from "@/components/ui/surface"
import { toast, withToast } from "@/components/ui/toast"
import { authClient, unwrap } from "@/lib/auth-client"
import { formatDate } from "@/lib/format"
import { pageTitle } from "@/lib/paths"

export const Route = createFileRoute("/_app/_instance/account")({
  head: () => ({ meta: [{ title: pageTitle("Account") }] }),
  component: AccountPage,
})

function AccountPage() {
  const { viewer, passkeys, dashboardUrl } = Route.useRouteContext()
  return (
    <>
      <PageHeader>
        <PageTitle>Account</PageTitle>
      </PageHeader>
      <PageBody>
        <div className="flex max-w-2xl flex-col gap-6">
          <p className="text-[13px] text-muted-foreground">
            Signed in as <span className="text-foreground">{viewer.email}</span>
          </p>
          {passkeys ? <Passkeys /> : <PasskeysElsewhere url={dashboardUrl} />}
          <ChangePassword />
        </div>
      </PageBody>
    </>
  )
}

// On an extra origin (LUMBUNG_DASHBOARD_EXTRA_ORIGINS) the browser can't use
// passkeys bound to the dashboard URL, and the server refuses the endpoints.
function PasskeysElsewhere({ url }: { url: string }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Passkeys</CardTitle>
      </CardHeader>
      <CardContent>
        <p className="text-[13px] text-muted-foreground">
          Passkeys only work on <span className="break-all text-foreground">{url}</span>. Open it there to add or use one.
        </p>
      </CardContent>
    </Card>
  )
}

type Passkey = { id: string; name?: string | null; createdAt: string | Date }

function Passkeys() {
  const [list, setList] = useState<Passkey[] | null>(null)
  const [error, setError] = useState("")

  const load = useCallback(async () => {
    try {
      setList(unwrap(await authClient().passkey.listUserPasskeys()) as Passkey[])
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])
  useEffect(() => {
    void load()
  }, [load])

  async function add() {
    try {
      await withToast(
        authClient().passkey.addPasskey().then((res) => unwrap(res as Parameters<typeof unwrap>[0])),
        { loading: "Waiting for your passkey…", success: "Passkey added", error: "Couldn't add the passkey" },
      )
    } catch {
      return
    }
    await load()
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Passkeys</CardTitle>
        <Button variant="secondary" size="sm" onClick={add}>
          <FingerprintIcon aria-hidden="true" />
          Add passkey
        </Button>
      </CardHeader>
      <CardContent>
        {error ? (
          <StatusText tone="destructive">{error}</StatusText>
        ) : list === null ? (
          <Skeleton className="h-9" />
        ) : list.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">None yet</p>
        ) : (
          <ul className="flex flex-col divide-y">
            {list.map((p) => (
              <li key={p.id} className="flex items-center gap-3 py-2.5 text-[13px] first:pt-0 last:pb-0">
                <KeyRoundIcon aria-hidden="true" className="size-4 text-faint" />
                <span className="min-w-0 flex-1 truncate">{p.name || "Passkey"}</span>
                <span className="text-muted-foreground">{formatDate(String(p.createdAt))}</span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function ChangePassword() {
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) return
    setPending(true)
    setError("")
    try {
      checkPassword(next)
      unwrap(await authClient().changePassword({ currentPassword: current, newPassword: next, revokeOtherSessions: true }))
      setCurrent("")
      setNext("")
      toast.success("Password changed")
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setPending(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Password</CardTitle>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <Field>
            <FieldLabel>Current password</FieldLabel>
            <PasswordInput value={current} onValueChange={setCurrent} autoComplete="current-password" required />
          </Field>
          <NewPassword label="New password" value={next} onChange={setNext} error={error} />
          <div className="flex justify-end">
            <Button type="submit" disabled={pending}>
              {pending ? "Saving…" : "Change password"}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
