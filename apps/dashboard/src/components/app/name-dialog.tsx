import { useRouter } from "@tanstack/react-router"
import { PlusIcon } from "lucide-react"
import { type FormEvent, type ReactElement, type ReactNode, useState } from "react"
import { Button, buttonVariants } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { toast } from "@/components/ui/toast"
import { createDatabase } from "@/fns/databases"
import { useWs } from "./workspace"

// Engine names: lowercase letters, digits and dashes, starting with a letter or digit.
export const NAME_PATTERN = "[a-z0-9][a-z0-9\\-]{0,62}"

type NameDialogProps = {
  /** The trigger's label. Leave it out, with `open` and `onOpenChange`, when something else opens the dialog. */
  trigger?: string
  open?: boolean
  onOpenChange?: (open: boolean) => void
  triggerIcon?: ReactNode
  triggerVariant?: "default" | "secondary" | "outline"
  /** Replaces the button look, for a trigger that sits somewhere else, like the sidebar. */
  triggerClassName?: string
  /** Wraps the trigger, for a tooltip around it. */
  wrapTrigger?: (trigger: ReactElement<Record<string, unknown>>) => ReactNode
  title: string
  description?: string
  label: string
  placeholder?: string
  submit: string
  pendingSubmit: string
  /** The name rule, as an HTML pattern. Default: database and branch names. */
  pattern?: string
  /** What to show when the name breaks the pattern. */
  patternHint?: string
  /** Runs on submit. A rejection keeps the dialog open and shows its message under the field. */
  onSubmit: (name: string) => Promise<unknown>
}

export function NameDialog({
  trigger,
  triggerIcon = <PlusIcon aria-hidden="true" />,
  triggerVariant = "default",
  triggerClassName,
  wrapTrigger = (t) => t,
  title,
  description,
  label,
  placeholder,
  submit,
  pendingSubmit,
  pattern = NAME_PATTERN,
  patternHint = "Use lowercase letters, digits and dashes.",
  onSubmit,
  open: openProp,
  onOpenChange,
}: NameDialogProps) {
  const [openState, setOpenState] = useState(false)
  const open = openProp ?? openState
  const setOpen = (next: boolean) => {
    // Cleared on the way out too: a dialog opened from outside never passes through its own trigger.
    if (!next) {
      setName("")
      setError("")
    }
    setOpenState(next)
    onOpenChange?.(next)
  }
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
      {trigger !== undefined &&
        wrapTrigger(
          <DialogTrigger className={triggerClassName ?? buttonVariants({ variant: triggerVariant })}>
            {triggerIcon}
            <span>{trigger}</span>
          </DialogTrigger>,
        )}
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
              pattern={pattern}
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
            <FieldError match="patternMismatch">{patternHint}</FieldError>
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

/** Creates a database and opens it. A failure shows under the dialog's field, so only success gets a toast. */
export function useCreateDatabase(onDone?: () => void) {
  const router = useRouter()
  const ws = useWs()
  return async (name: string) => {
    await createDatabase({ data: { ws, name } })
    toast.success(`Created ${name}`)
    await router.invalidate()
    await router.navigate({ to: "/w/$ws/db/$db", params: { ws, db: name } })
    onDone?.()
  }
}

export type TriggerSlot = Pick<NameDialogProps, "triggerClassName" | "wrapTrigger">

export function CreateDatabaseDialog({ onCreate, ...slot }: { onCreate: (name: string) => Promise<unknown> } & TriggerSlot) {
  return <NameDialog trigger="New database" title="Create database" label="Name" placeholder="my-app" submit="Create" pendingSubmit="Creating…" onSubmit={onCreate} {...slot} />
}
