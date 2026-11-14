import {
  SelectContent as SelectContentPart,
  SelectItemIndicator,
  SelectItem as SelectItemPart,
  SelectTrigger as SelectTriggerPart,
  SelectValue,
} from "@ui-sdk/react/select"
import { CheckIcon, ChevronDownIcon } from "lucide-react"
import type { ReactNode } from "react"
import { type Styled, cn } from "@/lib/utils"

export { Select, SelectGroup, SelectGroupLabel, SelectSeparator, SelectValue } from "@ui-sdk/react/select"

export const popupSurface =
  "rounded-lg border bg-popover p-1 text-popover-foreground shadow-lg shadow-black/10 outline-none origin-(--transform-origin) transition-[opacity,scale] duration-100 ease-out-quad data-starting-style:scale-95 data-starting-style:opacity-0 data-ending-style:scale-95 data-ending-style:opacity-0 motion-reduce:transition-none dark:shadow-black/50"

export const popupItem =
  "relative flex min-h-8 cursor-default items-center gap-2 rounded-md px-2 text-[13px] outline-none select-none data-disabled:opacity-50 data-highlighted:bg-accent data-highlighted:text-accent-foreground [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-faint data-highlighted:[&_svg]:text-foreground"

export function SelectTrigger({ className, children, ...props }: Omit<Styled<typeof SelectTriggerPart>, "children"> & { children?: ReactNode }) {
  return (
    <SelectTriggerPart
      className={cn(
        "inline-flex h-8 min-w-0 cursor-default items-center gap-1.5 rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground transition-[background-color] duration-100 hover:bg-accent data-disabled:opacity-50 data-open:bg-accent data-placeholder:text-faint [&_svg]:size-4 [&_svg]:shrink-0",
        className,
      )}
      {...props}
    >
      {children ?? <SelectValue className="truncate" />}
      <ChevronDownIcon aria-hidden="true" className="ml-auto text-faint" />
    </SelectTriggerPart>
  )
}

export function SelectContent({ className, ...props }: Styled<typeof SelectContentPart>) {
  return (
    <SelectContentPart
      sideOffset={4}
      className={cn(
        popupSurface,
        "max-h-(--available-height) min-w-(--anchor-width) overflow-y-auto duration-[80ms] data-starting-style:scale-[0.98] data-ending-style:scale-[0.98] data-ending-style:duration-[60ms]",
        className,
      )}
      {...props}
    />
  )
}

export function SelectItem({ className, children, ...props }: Omit<Styled<typeof SelectItemPart>, "children"> & { children?: ReactNode }) {
  return (
    <SelectItemPart className={cn(popupItem, "pr-8", className)} {...props}>
      {children}
      <SelectItemIndicator className="absolute right-2 inline-flex">
        <CheckIcon aria-hidden="true" className="text-foreground!" />
      </SelectItemIndicator>
    </SelectItemPart>
  )
}
