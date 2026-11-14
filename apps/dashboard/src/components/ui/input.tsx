import { Input as InputPart } from "@ui-sdk/react/input"
import {
  PasswordInputControl,
  PasswordInput as PasswordInputPart,
  PasswordInputToggle,
} from "@ui-sdk/react/password-input"
import { Textarea as TextareaPart } from "@ui-sdk/react/textarea"
import { EyeIcon, EyeOffIcon } from "lucide-react"
import type { ComponentProps } from "react"
import { type Styled, cn } from "@/lib/utils"

// 16px text on phones so focusing a field doesn't zoom the page.
const field =
  "w-full min-w-0 rounded-md border border-input bg-transparent px-2.5 text-base text-foreground caret-brand transition-[border-color] duration-100 placeholder:text-faint focus-visible:border-ring focus-visible:outline-none data-disabled:cursor-not-allowed data-disabled:opacity-50 data-invalid:border-destructive md:text-[13px]"

export function Input({ className, ...props }: Styled<typeof InputPart>) {
  return <InputPart className={cn(field, "h-8", className)} {...props} />
}

export function Textarea({ className, ...props }: Styled<typeof TextareaPart>) {
  return <TextareaPart className={cn(field, "min-h-20 py-1.5", className)} {...props} />
}

type PasswordInputProps = Omit<ComponentProps<typeof PasswordInputPart>, "className" | "children"> & {
  className?: string
  placeholder?: string
  id?: string
}

export function PasswordInput({ className, placeholder, id, ...props }: PasswordInputProps) {
  return (
    <PasswordInputPart
      className={cn(
        "flex h-8 w-full items-center rounded-md border border-input transition-[border-color] duration-100 has-focus-visible:border-ring data-disabled:opacity-50 has-data-invalid:border-destructive",
        className,
      )}
      {...props}
    >
      <PasswordInputControl
        id={id}
        placeholder={placeholder}
        className="h-full min-w-0 flex-1 bg-transparent pl-2.5 text-base text-foreground caret-brand outline-none placeholder:text-faint md:text-[13px]"
      />
      <PasswordInputToggle
        aria-label="Show password"
        className="mr-0.5 inline-flex size-7 shrink-0 items-center justify-center rounded-[5px] text-faint hover:text-foreground"
      >
        {(state) => (state.visible ? <EyeOffIcon className="size-4" /> : <EyeIcon className="size-4" />)}
      </PasswordInputToggle>
    </PasswordInputPart>
  )
}
