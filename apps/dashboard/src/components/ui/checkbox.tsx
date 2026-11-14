import { CheckboxIndicator, Checkbox as CheckboxPart } from "@ui-sdk/react/checkbox"
import { Switch as SwitchPart, SwitchThumb } from "@ui-sdk/react/switch"
import { CheckIcon, MinusIcon } from "lucide-react"
import { type Styled, cn } from "@/lib/utils"

export function Checkbox({ className, ...props }: Omit<Styled<typeof CheckboxPart>, "children">) {
  return (
    <CheckboxPart
      className={cn(
        "inline-flex size-4 shrink-0 items-center justify-center rounded-[4px] border border-input transition-[background-color,border-color] duration-100 data-checked:border-brand data-checked:bg-brand data-indeterminate:border-brand data-indeterminate:bg-brand data-disabled:opacity-50",
        className,
      )}
      {...props}
    >
      <CheckboxIndicator className="text-white">
        {(state) => (state.indeterminate ? <MinusIcon className="size-3" /> : <CheckIcon className="size-3" />)}
      </CheckboxIndicator>
    </CheckboxPart>
  )
}

export function Switch({ className, ...props }: Omit<Styled<typeof SwitchPart>, "children">) {
  return (
    <SwitchPart
      className={cn(
        "inline-flex h-5 w-8 shrink-0 items-center rounded-full bg-muted-foreground p-[3px] transition-[background-color] duration-150 data-checked:bg-brand data-disabled:opacity-50",
        className,
      )}
      {...props}
    >
      <SwitchThumb className="size-3.5 rounded-full bg-white shadow-[0_4px_24px_rgb(0_0_0/0.2)] transition-transform duration-400 ease-switch data-checked:translate-x-3 motion-reduce:transition-none rtl:data-checked:-translate-x-3" />
    </SwitchPart>
  )
}
