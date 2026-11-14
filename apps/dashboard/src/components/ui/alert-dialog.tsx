import {
  AlertDialog,
  AlertDialogBackdrop,
  AlertDialogClose,
  AlertDialogContent as AlertDialogContentPart,
  AlertDialogDescription as AlertDialogDescriptionPart,
  AlertDialogTitle as AlertDialogTitlePart,
  AlertDialogTrigger,
} from "@ui-sdk/react/alert-dialog"
import { useState, type ReactNode } from "react"
import { type Styled, cn } from "@/lib/utils"
import { buttonVariants } from "./button"
import { backdrop, dialogSurface } from "./dialog"
import { Field, FieldLabel } from "./field"
import { Input } from "./input"

export { AlertDialog, AlertDialogClose, AlertDialogTrigger, createAlertDialogController } from "@ui-sdk/react/alert-dialog"

export function AlertDialogContent({ className, children, ...props }: Omit<Styled<typeof AlertDialogContentPart>, "children"> & { children?: ReactNode }) {
  return (
    <>
      <AlertDialogBackdrop className={backdrop} />
      <AlertDialogContentPart className={cn(dialogSurface, className)} {...props}>
        {children}
      </AlertDialogContentPart>
    </>
  )
}

export function AlertDialogTitle({ className, ...props }: Styled<typeof AlertDialogTitlePart>) {
  return <AlertDialogTitlePart className={cn("text-[15px] font-[590] tracking-[-0.012em] break-words [overflow-wrap:anywhere]", className)} {...props} />
}

export function AlertDialogDescription({ className, ...props }: Styled<typeof AlertDialogDescriptionPart>) {
  return <AlertDialogDescriptionPart className={cn("-mt-2 text-[13px] text-muted-foreground", className)} {...props} />
}

type TypedConfirmProps = {
  /** The trigger's text, like "Delete database". */
  trigger: ReactNode
  triggerClassName?: string
  title: string
  description?: string
  /** What the person types to unlock the action, usually the thing's name. */
  confirmText: string
  /** The confirming button's text. Keep it different from the trigger's, like "Delete forever". */
  actionLabel: string
  /** What the action does, for an agent's confirmation prompt. */
  agentConfirm: string
  /** Runs before the dialog closes. A rejection keeps it open and shows the message. */
  onConfirm: () => Promise<unknown>
}

export function TypedConfirm({ trigger, triggerClassName, title, description, confirmText, actionLabel, agentConfirm, onConfirm }: TypedConfirmProps) {
  const [typed, setTyped] = useState("")
  return (
    <AlertDialog onOpenChange={(open) => !open && setTyped("")}>
      <AlertDialogTrigger className={triggerClassName ?? buttonVariants({ variant: "destructive" })}>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogTitle>{title}</AlertDialogTitle>
        {description && <AlertDialogDescription className="[overflow-wrap:anywhere]">{description}</AlertDialogDescription>}
        <Field>
          <FieldLabel>
            Type <span className="font-mono [overflow-wrap:anywhere]">{confirmText}</span> to confirm
          </FieldLabel>
          <Input value={typed} onValueChange={setTyped} autoComplete="off" spellCheck={false} className="font-mono" />
        </Field>
        <AlertDialogClose
          value="confirm"
          action={onConfirm}
          data-agent-confirm={agentConfirm}
          disabled={typed !== confirmText}
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
