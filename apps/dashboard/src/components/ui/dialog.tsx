import {
  DialogBackdrop as DialogBackdropPart,
  DialogClose,
  DialogContent as DialogContentPart,
  DialogDescription as DialogDescriptionPart,
  DialogTitle as DialogTitlePart,
} from "@ui-sdk/react/dialog"
import { XIcon } from "lucide-react"
import type { ReactNode } from "react"
import { type Styled, cn } from "@/lib/utils"
import { buttonVariants } from "./button"

export { Dialog, DialogClose, DialogShortcut, DialogTrigger, createDialogController } from "@ui-sdk/react/dialog"

export const backdrop =
  "fixed inset-0 bg-overlay transition-opacity duration-180 data-starting-style:opacity-0 data-ending-style:opacity-0 motion-reduce:transition-none"

export const dialogSurface =
  "fixed inset-0 m-auto flex h-fit max-h-[calc(100svh-32px)] w-[calc(100vw-32px)] max-w-md flex-col gap-4 overflow-y-auto rounded-lg border bg-background p-5 text-foreground outline-none transition-[opacity,scale,translate] duration-180 ease-out data-starting-style:translate-y-[1%] data-starting-style:scale-95 data-starting-style:opacity-0 data-ending-style:scale-95 data-ending-style:opacity-0 motion-reduce:transition-none"

export function DialogContent({
  className,
  children,
  closeLabel = "Close",
  ...props
}: Omit<Styled<typeof DialogContentPart>, "children"> & { children?: ReactNode; closeLabel?: string }) {
  return (
    <>
      <DialogBackdropPart className={backdrop} />
      <DialogContentPart className={cn(dialogSurface, className)} {...props}>
        {children}
        <DialogClose aria-label={closeLabel} className={buttonVariants({ variant: "ghost", size: "icon-sm", className: "absolute top-3 right-3" })}>
          <XIcon aria-hidden="true" />
        </DialogClose>
      </DialogContentPart>
    </>
  )
}

export function DialogTitle({ className, ...props }: Styled<typeof DialogTitlePart>) {
  return <DialogTitlePart className={cn("pr-8 text-[15px] font-[590] tracking-[-0.012em]", className)} {...props} />
}

export function DialogDescription({ className, ...props }: Styled<typeof DialogDescriptionPart>) {
  return <DialogDescriptionPart className={cn("-mt-2 text-[13px] text-muted-foreground", className)} {...props} />
}

export function DialogFooter({ className, children }: { className?: string; children?: ReactNode }) {
  return <div className={cn("flex justify-end gap-2 pt-1", className)}>{children}</div>
}
