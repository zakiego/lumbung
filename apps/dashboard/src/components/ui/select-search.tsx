// A search field and a scrolling list inside SelectContent, for long lists
// like a database's tables.
import {
  SelectEmpty as SelectEmptyPart,
  SelectGroupLabel as SelectGroupLabelPart,
  SelectInput as SelectInputPart,
  SelectList as SelectListPart,
} from "@ui-sdk/react/select"
import { type Styled, cn } from "@/lib/utils"

export function SelectSearch({ className, ...props }: Styled<typeof SelectInputPart>) {
  return (
    <SelectInputPart
      className={cn(
        "mb-1 h-8 w-full rounded-md border-b-0 bg-transparent px-2 font-sans text-base text-foreground caret-brand outline-none placeholder:text-muted-foreground md:text-[13px]",
        className,
      )}
      {...props}
    />
  )
}

export function SelectList({ className, ...props }: Styled<typeof SelectListPart>) {
  return <SelectListPart className={cn("max-h-72 overflow-y-auto overscroll-contain", className)} {...props} />
}

export function SelectEmpty({ className, ...props }: Styled<typeof SelectEmptyPart>) {
  // ui-sdk keeps this live region mounted and only fills it while nothing matches,
  // so the padding goes on data-empty or it shows as a gap above the list.
  return <SelectEmptyPart className={cn("px-2 data-empty:py-6 text-center font-sans text-[13px] text-muted-foreground", className)} {...props} />
}

export function SelectGroupLabel({ className, ...props }: Styled<typeof SelectGroupLabelPart>) {
  return <SelectGroupLabelPart className={cn("px-2 pt-1.5 pb-1 font-sans text-xs font-[510] text-muted-foreground", className)} {...props} />
}
