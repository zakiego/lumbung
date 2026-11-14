import { type FormEvent, type ReactNode, useState } from "react"
import { AlertDialog, AlertDialogClose, AlertDialogContent, AlertDialogDescription, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog"
import { Button, type ButtonSize, type ButtonVariant, buttonVariants } from "@/components/ui/button"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { CopyButton } from "./connect"

export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

type TriggerStyle = { variant?: ButtonVariant; size?: ButtonSize; className?: string; label?: string }

type FormDialogProps = {
  trigger: ReactNode
  triggerStyle?: TriggerStyle
  title: string
  description?: string
  submit: string
  pendingSubmit: string
  /** Runs each time the dialog opens, to reset the fields. */
  onOpen?: () => void
  /**
   * Runs on submit. A rejection keeps the dialog open with its message. Return
   * a node to show it in place of the form, like a secret shown once.
   */
  onSubmit: () => Promise<ReactNode | void>
  /** Runs when the dialog closes after showing what onSubmit returned. */
  onDone?: () => void
  children: ReactNode
}

export function FormDialog({ trigger, triggerStyle = {}, title, description, submit, pendingSubmit, onOpen, onSubmit, onDone, children }: FormDialogProps) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [done, setDone] = useState<ReactNode>(null)

  async function handle(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) return
    setPending(true)
    setError("")
    try {
      const result = await onSubmit()
      if (result) setDone(result)
      else setOpen(false)
    } catch (e) {
      setError(errorText(e))
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
        if (!next && done) onDone?.()
        if (next) {
          setError("")
          setDone(null)
          onOpen?.()
        }
      }}
    >
      <DialogTrigger aria-label={triggerStyle.label} className={buttonVariants({ variant: triggerStyle.variant ?? "default", size: triggerStyle.size, className: triggerStyle.className })}>
        {trigger}
      </DialogTrigger>
      <DialogContent>
        {done ? (
          <>
            {done}
            <DialogFooter>
              <DialogClose className={buttonVariants({ variant: "secondary" })}>Done</DialogClose>
            </DialogFooter>
          </>
        ) : (
          <>
            <DialogTitle className="[overflow-wrap:anywhere]">{title}</DialogTitle>
            {description && <DialogDescription>{description}</DialogDescription>}
            <form onSubmit={handle} className="flex flex-col gap-4">
              {children}
              {error && (
                <p role="alert" className="text-xs text-destructive [overflow-wrap:anywhere]">
                  {error}
                </p>
              )}
              <DialogFooter>
                <Button type="submit" disabled={pending}>
                  {pending ? pendingSubmit : submit}
                </Button>
              </DialogFooter>
            </form>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

type ConfirmProps = {
  trigger: ReactNode
  triggerStyle?: TriggerStyle
  title: string
  description?: string
  actionLabel: string
  agentConfirm: string
  onConfirm: () => Promise<unknown>
}

// A plain yes or no, for actions that are easy to undo or lose nothing big.
export function Confirm({ trigger, triggerStyle = {}, title, description, actionLabel, agentConfirm, onConfirm }: ConfirmProps) {
  return (
    <AlertDialog>
      <AlertDialogTrigger aria-label={triggerStyle.label} className={buttonVariants({ variant: triggerStyle.variant ?? "ghost", size: triggerStyle.size, className: triggerStyle.className })}>
        {trigger}
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogTitle className="[overflow-wrap:anywhere]">{title}</AlertDialogTitle>
        {description && <AlertDialogDescription>{description}</AlertDialogDescription>}
        <AlertDialogClose
          value="confirm"
          action={onConfirm}
          data-agent-confirm={agentConfirm}
          render={(props, state) => (
            <div className="flex flex-col gap-3">
              {state.error instanceof Error && <p className="text-xs text-destructive">{state.error.message}</p>}
              <div className="flex justify-end gap-2">
                <AlertDialogClose className={buttonVariants({ variant: "ghost" })}>Cancel</AlertDialogClose>
                <button {...props} className={buttonVariants({ variant: "destructive" })}>
                  {state.pending ? "Working…" : actionLabel}
                </button>
              </div>
            </div>
          )}
        />
      </AlertDialogContent>
    </AlertDialog>
  )
}

export function ShownOnce({ title, value, copyLabel, note }: { title: string; value: string; copyLabel: string; note: string }) {
  return (
    <>
      <DialogTitle className="[overflow-wrap:anywhere]">{title}</DialogTitle>
      <DialogDescription>{note}</DialogDescription>
      <div data-agent-hidden className="relative rounded-lg border bg-code">
        <code className="block py-3 pr-12 pl-3 font-mono text-[13px] leading-[1.6] break-all text-foreground/90 select-all">{value}</code>
        <CopyButton text={value} label={copyLabel} className="absolute top-1.5 right-1.5" />
      </div>
    </>
  )
}
