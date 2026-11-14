import { CollapsiblePanel as CollapsiblePanelPart } from "@ui-sdk/react/collapsible"
import {
  ScrollAreaContent,
  ScrollArea as ScrollAreaPart,
  ScrollAreaScrollbar,
  ScrollAreaThumb,
  ScrollAreaViewport,
} from "@ui-sdk/react/scroll-area"
import { Separator as SeparatorPart } from "@ui-sdk/react/separator"
import type { ReactNode } from "react"
import { type Styled, cn } from "@/lib/utils"

export { Collapsible, CollapsibleTrigger } from "@ui-sdk/react/collapsible"

export function Separator({ className, orientation = "horizontal", ...props }: Styled<typeof SeparatorPart>) {
  return (
    <SeparatorPart
      orientation={orientation}
      className={cn("shrink-0 bg-border", orientation === "vertical" ? "h-4 w-px" : "h-px w-full", className)}
      {...props}
    />
  )
}

export function CollapsiblePanel({ className, ...props }: Styled<typeof CollapsiblePanelPart>) {
  return (
    <CollapsiblePanelPart
      className={cn(
        "h-(--collapsible-panel-height) overflow-hidden transition-[height] duration-250 ease-out-quad data-starting-style:h-0 data-ending-style:h-0 motion-reduce:transition-none",
        className,
      )}
      {...props}
    />
  )
}

type ScrollAreaProps = {
  className?: string
  viewportClassName?: string
  children?: ReactNode
  /** Which scrollbars to draw. The native ones stay hidden. */
  axis?: "x" | "y" | "both"
  label?: string
  /** Fades the inline edges while there's more to scroll that way. */
  fade?: boolean
}

const bar =
  "flex touch-none p-0.5 opacity-0 transition-opacity duration-150 select-none data-visible:opacity-100 data-[orientation=horizontal]:h-2 data-[orientation=horizontal]:flex-col data-[orientation=vertical]:w-2"

export function ScrollArea({ className, viewportClassName, children, axis = "y", label, fade }: ScrollAreaProps) {
  return (
    <ScrollAreaPart type="hover" data-fade-x={fade ? "" : undefined} className={cn("relative overflow-hidden", className)}>
      <ScrollAreaViewport aria-label={label} className={cn("size-full overscroll-contain [scrollbar-width:none]", viewportClassName)}>
        <ScrollAreaContent className={axis === "y" ? undefined : "w-max min-w-full"}>{children}</ScrollAreaContent>
      </ScrollAreaViewport>
      {axis !== "x" && (
        <ScrollAreaScrollbar orientation="vertical" className={cn(bar, "absolute top-0 right-0 bottom-0")}>
          <ScrollAreaThumb className="flex-1 rounded-full bg-foreground/25" />
        </ScrollAreaScrollbar>
      )}
      {axis !== "y" && (
        <ScrollAreaScrollbar orientation="horizontal" className={cn(bar, "absolute right-0 bottom-0 left-0")}>
          <ScrollAreaThumb className="flex-1 rounded-full bg-foreground/25" />
        </ScrollAreaScrollbar>
      )}
    </ScrollAreaPart>
  )
}
