import { Button as ButtonPart } from "@ui-sdk/react/button"
import type { ComponentProps, ReactNode } from "react"
import { cn } from "@/lib/utils"
import { Spinner } from "./spinner"

export type ButtonVariant = "default" | "secondary" | "outline" | "ghost" | "destructive"
export type ButtonSize = "sm" | "default" | "icon" | "icon-sm"

const base =
  "group/button relative inline-flex shrink-0 cursor-default items-center justify-center gap-1.5 whitespace-nowrap rounded-md text-[13px] font-[510] select-none transition-[background-color,color,border-color,scale] duration-100 ease-out-quad active:scale-[0.97] disabled:pointer-events-none disabled:opacity-50 data-disabled:pointer-events-none data-disabled:opacity-50 aria-disabled:opacity-50 data-pending:cursor-progress [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 motion-reduce:active:scale-100"

const variants: Record<ButtonVariant, string> = {
  default: "bg-primary text-primary-foreground hover:bg-primary/90",
  secondary: "border bg-secondary text-secondary-foreground hover:bg-accent",
  outline: "border bg-transparent text-foreground hover:bg-accent",
  ghost: "text-muted-foreground hover:bg-accent hover:text-foreground data-open:bg-accent data-open:text-foreground",
  destructive: "bg-destructive-fill text-white hover:bg-destructive-fill/90",
}

const sizes: Record<ButtonSize, string> = {
  sm: "h-7 px-2.5 text-[12px]",
  default: "h-8 px-3",
  icon: "size-8",
  "icon-sm": "size-7",
}

// For ui-sdk triggers that look like buttons (DialogTrigger, MenuTrigger).
export function buttonVariants({ variant = "default", size = "default", className }: { variant?: ButtonVariant; size?: ButtonSize; className?: string } = {}) {
  return cn(base, variants[variant], sizes[size], className)
}

type ButtonProps = Omit<ComponentProps<typeof ButtonPart>, "className" | "children"> & {
  className?: string
  children?: ReactNode
  variant?: ButtonVariant
  size?: ButtonSize
}

// Return a Promise from onClick and the button shows a spinner until it settles.
export function Button({ className, variant, size, children, ...props }: ButtonProps) {
  return (
    <ButtonPart className={buttonVariants({ variant, size, className })} {...props}>
      <span className="inline-flex items-center gap-1.5 group-data-pending/button:invisible">{children}</span>
      <Spinner className="absolute inset-0 m-auto hidden group-data-pending/button:block" />
    </ButtonPart>
  )
}
