import { NumberInputControl, NumberInputGroup, NumberInput as NumberInputPart } from "@ui-sdk/react/number-input"
import type { ComponentProps } from "react"
import { cn } from "@/lib/utils"

type NumberInputProps = Omit<ComponentProps<typeof NumberInputPart>, "className" | "children"> & {
  className?: string
  placeholder?: string
  "aria-label"?: string
}

// Whole numbers without grouping, so "1440" reads as typed. Put it in a Field for its label.
export function NumberInput({ className, placeholder, "aria-label": ariaLabel, ...props }: NumberInputProps) {
  return (
    <NumberInputPart format={{ maximumFractionDigits: 0, useGrouping: false }} {...props}>
      <NumberInputGroup
        className={cn(
          "flex h-8 w-full items-center rounded-md border border-input transition-[border-color] duration-100 has-focus-visible:border-ring data-disabled:opacity-50 data-invalid:border-destructive",
          className,
        )}
      >
        <NumberInputControl
          aria-label={ariaLabel}
          placeholder={placeholder}
          className="h-full w-full min-w-0 bg-transparent px-2.5 text-base text-foreground tabular-nums caret-brand outline-none placeholder:text-muted-foreground md:text-[13px]"
        />
      </NumberInputGroup>
    </NumberInputPart>
  )
}
