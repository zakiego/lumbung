import { GitBranchPlusIcon, PlusIcon } from "lucide-react"
import { type FormEvent, type ReactNode, useState } from "react"
import { Button, buttonVariants } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"

// Engine names: lowercase letters, digits and dashes, starting with a letter or digit.
export const NAME_PATTERN = "[a-z0-9][a-z0-9\\-]{0,62}"

type NameDialogProps = {
  trigger: string
  triggerIcon?: ReactNode
  triggerVariant?: "default" | "secondary" | "outline"
  title: string
  description?: string
  label: string
  placeholder?: string
  submit: string
  pendingSubmit: string
  /** Runs on submit. A rejection keeps the dialog open and shows its message under the field. */
  onSubmit: (name: string) => Promise<unknown>
}

export function NameDialog({ trigger, triggerIcon = <PlusIcon aria-hidden="true" />, triggerVariant = "default", title, description, label, placeholder, submit, pendingSubmit, onSubmit }: NameDialogProps) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)

  async function handle(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) return
    setPending(true)
    setError("")
    try {
      await onSubmit(name)
      setOpen(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next, details) => {
        if (!next && pending) return details.cancel()
        setOpen(next)
        if (next) {
          setName("")
          setError("")
        }
      }}
    >
      <DialogTrigger className={buttonVariants({ variant: triggerVariant })}>
        {triggerIcon}
        {trigger}
      </DialogTrigger>
      <DialogContent>
        <DialogTitle>{title}</DialogTitle>
        {description && <DialogDescription>{description}</DialogDescription>}
        <form onSubmit={handle} className="flex flex-col gap-4">
          <Field invalid={error ? true : undefined}>
            <FieldLabel>{label}</FieldLabel>
            <Input
              value={name}
              onValueChange={(v) => {
                setName(v)
                setError("")
              }}
              placeholder={placeholder}
              required
              pattern={NAME_PATTERN}
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
            <FieldError match="patternMismatch">Use lowercase letters, digits and dashes.</FieldError>
            <FieldError match={!!error}>{error}</FieldError>
          </Field>
          <DialogFooter>
            <Button type="submit" disabled={pending}>
              {pending ? pendingSubmit : submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function CreateDatabaseDialog({ onCreate }: { onCreate: (name: string) => Promise<unknown> }) {
  return <NameDialog trigger="New database" title="Create database" label="Name" placeholder="my-app" submit="Create" pendingSubmit="Creating…" onSubmit={onCreate} />
}

export function CreateBranchDialog({ from, onCreate }: { from: string; onCreate: (name: string) => Promise<unknown> }) {
  return (
    <NameDialog
      trigger="New branch"
      triggerVariant="secondary"
      triggerIcon={<GitBranchPlusIcon aria-hidden="true" />}
      title={`Branch from ${from}`}
      label="Branch name"
      placeholder="try-new-index"
      submit="Create"
      pendingSubmit="Creating…"
      onSubmit={onCreate}
    />
  )
}
