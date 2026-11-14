import { PopoverContent as PopoverContentPart } from "@ui-sdk/react/popover"
import { type Styled, cn } from "@/lib/utils"
import { popupSurface } from "./select"

export { Popover, PopoverClose, PopoverDescription, PopoverTitle, PopoverTrigger } from "@ui-sdk/react/popover"

export function PopoverContent({ className, ...props }: Styled<typeof PopoverContentPart>) {
  return <PopoverContentPart sideOffset={6} className={cn(popupSurface, "w-72 p-3", className)} {...props} />
}
