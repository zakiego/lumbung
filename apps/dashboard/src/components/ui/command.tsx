import {
  CommandEmpty as CommandEmptyPart,
  CommandGroupLabel as CommandGroupLabelPart,
  CommandInput as CommandInputPart,
  CommandItem as CommandItemPart,
  CommandList as CommandListPart,
  Command as CommandPart,
  CommandSeparator as CommandSeparatorPart,
} from "@ui-sdk/react/command"
import { DialogBackdrop, DialogContent as DialogContentPart, DialogTitle } from "@ui-sdk/react/dialog"
import { SearchIcon } from "lucide-react"
import type { ReactNode } from "react"
import { type Styled, cn } from "@/lib/utils"
import { popupItem } from "./select"

export { CommandGroup, CommandItemIcon } from "@ui-sdk/react/command"

// Opened from the keyboard many times a day, so it appears and leaves without animation.
export function CommandDialogContent({ title, children }: { title: string; children: ReactNode }) {
  return (
    <>
      <DialogBackdrop className="fixed inset-0 bg-overlay" />
      <DialogContentPart className="fixed inset-x-0 top-[12vh] mx-auto flex max-h-[min(30rem,76vh)] max-sm:top-[max(0.5rem,env(safe-area-inset-top))] max-sm:max-h-[min(30rem,50dvh)] w-[calc(100vw-32px)] max-w-xl flex-col overflow-hidden rounded-xl border bg-popover text-popover-foreground shadow-2xl shadow-black/20 outline-none dark:shadow-black/60">
        <DialogTitle className="sr-only">{title}</DialogTitle>
        {children}
      </DialogContentPart>
    </>
  )
}

export function Command({ className, ...props }: Styled<typeof CommandPart>) {
  return <CommandPart className={cn("flex min-h-0 flex-1 flex-col", className)} {...props} />
}

export function CommandInput({ className, ...props }: Styled<typeof CommandInputPart>) {
  return (
    <div className="flex h-12 shrink-0 items-center gap-2.5 border-b px-4">
      <SearchIcon aria-hidden="true" className="size-4 shrink-0 text-faint" />
      <CommandInputPart
        className={cn("h-full min-w-0 flex-1 bg-transparent text-[14px] caret-brand outline-none placeholder:text-muted-foreground max-sm:text-[16px]", className)}
        {...props}
      />
    </div>
  )
}

export function CommandList({ className, ...props }: Styled<typeof CommandListPart>) {
  return <CommandListPart className={cn("min-h-0 flex-1 overflow-y-auto overscroll-contain p-1.5 scroll-py-1.5", className)} {...props} />
}

export function CommandEmpty({ className, ...props }: Styled<typeof CommandEmptyPart>) {
  return <CommandEmptyPart className={cn("hidden px-4 py-10 text-center text-[13px] text-muted-foreground data-empty:block", className)} {...props} />
}

export function CommandGroupLabel({ className, ...props }: Styled<typeof CommandGroupLabelPart>) {
  return <CommandGroupLabelPart className={cn("px-2.5 pt-2 pb-1 text-xs font-[510] text-muted-foreground", className)} {...props} />
}

export function CommandItem({ className, ...props }: Styled<typeof CommandItemPart>) {
  return <CommandItemPart className={cn(popupItem, "min-h-9 px-2.5", className)} {...props} />
}

export function CommandSeparator({ className, ...props }: Styled<typeof CommandSeparatorPart>) {
  return <CommandSeparatorPart className={cn("-mx-1.5 my-1.5 h-px bg-border", className)} {...props} />
}
