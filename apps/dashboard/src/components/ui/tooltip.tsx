import { TooltipContent as TooltipContentPart, Tooltip as TooltipPart, TooltipTrigger } from "@ui-sdk/react/tooltip"
import type { ComponentProps, ReactElement, ReactNode } from "react"
import { type Styled, cn } from "@/lib/utils"

export { TooltipTrigger } from "@ui-sdk/react/tooltip"

export function TooltipContent({ className, ...props }: Styled<typeof TooltipContentPart>) {
  return (
    <TooltipContentPart
      sideOffset={6}
      className={cn(
        "max-w-72 rounded-lg border bg-background px-2 py-1.5 text-xs font-[510] text-foreground/90 shadow-lg shadow-black/10 origin-(--transform-origin) transition-[opacity,scale] duration-120 ease-out-quad data-starting-style:scale-90 data-starting-style:opacity-0 data-ending-style:opacity-0 motion-reduce:transition-none dark:shadow-black/50",
        className,
      )}
      {...props}
    />
  )
}

type TipProps = Omit<ComponentProps<typeof TooltipPart>, "children"> & {
  /** The element the tip is for. It receives the trigger's props. */
  children: ReactElement<Record<string, unknown>>
  content: ReactNode
  side?: ComponentProps<typeof TooltipContentPart>["side"]
}

export function Tooltip({ children, content, side, ...props }: TipProps) {
  return (
    <TooltipPart delay={500} {...props}>
      <TooltipTrigger render={children} />
      <TooltipContent side={side}>{content}</TooltipContent>
    </TooltipPart>
  )
}
