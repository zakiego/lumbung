import { TooltipContent as TooltipContentPart, Tooltip as TooltipPart, TooltipTrigger } from "@ui-sdk/react/tooltip"
import { mergeProps } from "@ui-sdk/react/primitive"
import { type ComponentProps, type ReactElement, type ReactNode, cloneElement, useRef } from "react"
import { type Styled, cn } from "@/lib/utils"

export { TooltipTrigger } from "@ui-sdk/react/tooltip"

export function TooltipContent({ className, ...props }: Styled<typeof TooltipContentPart>) {
  return (
    <TooltipContentPart
      sideOffset={6}
      className={cn(
        "max-w-72 rounded-lg border bg-background px-2 py-1.5 text-xs font-[510] text-foreground/90 shadow-lg shadow-black/10 origin-(--transform-origin) transition-[opacity,scale] duration-120 ease-out-quad data-starting-style:scale-90 data-starting-style:opacity-0 data-ending-style:opacity-0 data-instant:transition-none motion-reduce:transition-opacity motion-reduce:data-starting-style:scale-100 dark:shadow-black/50",
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

/**
 * The full text of a cut-off name, on hover and on focus. Opens only while the
 * `[data-truncate]` element inside the trigger (or the trigger) is cut off. It
 * describes the trigger, so the trigger keeps its own name (badges, sr-only text).
 */
export function TruncatedTip({ text, children, side }: { text: string; children: ReactElement<Record<string, unknown>>; side?: TipProps["side"] }) {
  const trigger = useRef<HTMLButtonElement>(null)
  return (
    <TooltipPart
      delay={500}
      type="description"
      onOpenChange={(open, details) => {
        const root = trigger.current
        const el = root?.querySelector<HTMLElement>("[data-truncate]") ?? root
        if (open && el && el.scrollWidth <= el.clientWidth) details.cancel()
      }}
    >
      <TooltipTrigger
        ref={trigger}
        render={(props) => {
          // The trigger defaults to a button; a link, heading or span keeps its own semantics.
          const { type, ...rest } = props
          return cloneElement(children, mergeProps(children.props, children.type === "button" ? props : rest))
        }}
      />
      <TooltipContent side={side} className="[overflow-wrap:anywhere]">
        {text}
      </TooltipContent>
    </TooltipPart>
  )
}
