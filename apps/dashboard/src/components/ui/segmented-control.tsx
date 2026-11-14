import {
  SegmentedControlIndicator,
  SegmentedControlItem as SegmentedControlItemPart,
  SegmentedControl as SegmentedControlPart,
} from "@ui-sdk/react/segmented-control"
import type { ReactNode } from "react"
import { type Styled, cn } from "@/lib/utils"

export function SegmentedControl({ className, children, ...props }: Omit<Styled<typeof SegmentedControlPart>, "children"> & { children?: ReactNode }) {
  return (
    <SegmentedControlPart className={cn("relative isolate inline-flex h-8 w-fit items-center rounded-md border bg-secondary p-0.5", className)} {...props}>
      <SegmentedControlIndicator className="absolute top-(--segmented-control-indicator-top) left-(--segmented-control-indicator-left) -z-10 h-(--segmented-control-indicator-height) w-(--segmented-control-indicator-width) rounded-[5px] bg-background shadow-xs transition-[left,width] duration-200 ease-out-quad motion-reduce:transition-none dark:bg-accent" />
      {children}
    </SegmentedControlPart>
  )
}

export function SegmentedControlItem({ className, ...props }: Styled<typeof SegmentedControlItemPart>) {
  return (
    <SegmentedControlItemPart
      className={cn(
        "inline-flex h-full cursor-default items-center gap-1.5 rounded-[5px] px-2.5 text-[13px] font-[510] text-muted-foreground transition-colors duration-100 outline-none hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring data-checked:text-foreground data-disabled:opacity-50 [&_svg]:size-4",
        className,
      )}
      {...props}
    />
  )
}
