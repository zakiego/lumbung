import { FingerprintIcon } from "lucide-react"
import { type FormEvent, type ReactNode, useState } from "react"
import { Mark } from "@/components/app/mark"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import { Input, PasswordInput } from "@/components/ui/input"
import { Separator } from "@/components/ui/layout"
import { ROLE_LABEL, type Role } from "@/lib/roles"

const MIN_PASSWORD = 10

export function AuthLayout({ title, children }: { title: string; children: ReactNode }) {
  return (
    <main className="flex min-h-svh flex-col items-center justify-center px-4 py-12">
      <div className="flex w-full max-w-sm flex-col gap-6">
        <div className="flex flex-col items-center gap-4 text-center">
          <span className="flex size-10 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <Mark className="size-5" />
          </span>
          <h1 className="text-xl font-[590] tracking-[-0.012em]">{title}</h1>
        </div>
        {children}
      </div>
    </main>
  )
}

function message(e: unknown) {
  return e instanceof Error ? e.message : String(e)
}

function useSubmit<T>(run: (values: T) => Promise<unknown>) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  async function submit(event: FormEvent<HTMLFormElement>, values: T) {
    event.preventDefault()
    if (pending) return
    setPending(true)
    setError("")
    try {
      await run(values)
    } catch (e) {
      setError(message(e))
    } finally {
      setPending(false)
    }
  }
  return { pending, error, setError, submit }
}

type LoginProps = {
  onSignIn: (email: string, password: string) => Promise<unknown>
  onPasskey: () => Promise<unknown>
}

export function LoginForm({ onSignIn, onPasskey }: LoginProps) {
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const form = useSubmit(({ email, password }: { email: string; password: string }) => onSignIn(email, password))
  return (
    <div className="flex flex-col gap-5">
      <form onSubmit={(e) => form.submit(e, { email, password })} className="flex flex-col gap-4">
        <Field>
          <FieldLabel>Email</FieldLabel>
          <Input type="email" value={email} onValueChange={setEmail} autoComplete="username webauthn" required />
        </Field>
        <Field invalid={form.error ? true : undefined}>
          <FieldLabel>Password</FieldLabel>
          <PasswordInput
            value={password}
            onValueChange={(v) => {
              setPassword(v)
              form.setError("")
            }}
            autoComplete="current-password"
            required
          />
          <FieldError match={!!form.error}>{form.error}</FieldError>
        </Field>
        <Button type="submit" disabled={form.pending} className="mt-1 w-full">
          {form.pending ? "Signing in…" : "Sign in"}
        </Button>
      </form>
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <Separator className="flex-1" />
        or
        <Separator className="flex-1" />
      </div>
      <Button
        variant="secondary"
        className="w-full"
        onClick={async () => {
          form.setError("")
          try {
            await onPasskey()
          } catch (e) {
            form.setError(message(e))
          }
        }}
      >
        <FingerprintIcon aria-hidden="true" />
        Sign in with a passkey
      </Button>
    </div>
  )
}

type SetupValues = { serviceToken: string; name: string; email: string; password: string }

export function SetupForm({ onSetup }: { onSetup: (values: SetupValues) => Promise<unknown> }) {
  const [values, setValues] = useState<SetupValues>({ serviceToken: "", name: "", email: "", password: "" })
  const set = (key: keyof SetupValues) => (v: string) => setValues((prev) => ({ ...prev, [key]: v }))
  const form = useSubmit(async (v: SetupValues) => {
    checkPassword(v.password)
    await onSetup(v)
  })
  return (
    <form onSubmit={(e) => form.submit(e, values)} className="flex flex-col gap-4">
      <Field>
        <FieldLabel>Service token</FieldLabel>
        <PasswordInput value={values.serviceToken} onValueChange={set("serviceToken")} autoComplete="off" required />
        <FieldDescription>
          Print it with <code className="font-mono text-foreground">lumbungd service-token</code>
        </FieldDescription>
      </Field>
      <Field>
        <FieldLabel>Your name</FieldLabel>
        <Input value={values.name} onValueChange={set("name")} autoComplete="name" required maxLength={100} />
      </Field>
      <Field>
        <FieldLabel>Email</FieldLabel>
        <Input type="email" value={values.email} onValueChange={set("email")} autoComplete="email" required />
      </Field>
      <NewPassword value={values.password} onChange={set("password")} error={form.error} />
      <Button type="submit" disabled={form.pending} className="mt-1 w-full">
        {form.pending ? "Creating…" : "Create owner"}
      </Button>
    </form>
  )
}

export function InviteForm({ email, role, onAccept }: { email: string; role: Role; onAccept: (values: { name: string; password: string }) => Promise<unknown> }) {
  const [name, setName] = useState("")
  const [password, setPassword] = useState("")
  const form = useSubmit(async (v: { name: string; password: string }) => {
    checkPassword(v.password)
    await onAccept(v)
  })
  return (
    <form onSubmit={(e) => form.submit(e, { name, password })} className="flex flex-col gap-4">
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 rounded-lg border bg-card px-4 py-3 text-[13px]">
        <dt className="text-muted-foreground">Email</dt>
        <dd className="truncate">{email}</dd>
        <dt className="text-muted-foreground">Role</dt>
        <dd>{ROLE_LABEL[role]}</dd>
      </dl>
      <Field>
        <FieldLabel>Your name</FieldLabel>
        <Input value={name} onValueChange={setName} autoComplete="name" required maxLength={100} />
      </Field>
      <NewPassword value={password} onChange={setPassword} error={form.error} />
      <Button type="submit" disabled={form.pending} className="mt-1 w-full">
        {form.pending ? "Joining…" : "Join"}
      </Button>
    </form>
  )
}

export function NewPassword({ value, onChange, error, label = "Password" }: { value: string; onChange: (v: string) => void; error?: string; label?: string }) {
  return (
    <Field invalid={error ? true : undefined}>
      <FieldLabel>{label}</FieldLabel>
      <PasswordInput value={value} onValueChange={onChange} autoComplete="new-password" required />
      <FieldDescription>At least {MIN_PASSWORD} characters</FieldDescription>
      <FieldError match={!!error}>{error}</FieldError>
    </Field>
  )
}

// Checked on submit rather than with minLength, which browsers only enforce while someone types.
export function checkPassword(password: string) {
  if (password.length < MIN_PASSWORD) throw new Error(`Use at least ${MIN_PASSWORD} characters.`)
}
