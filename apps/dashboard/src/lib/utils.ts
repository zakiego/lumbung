import { clsx, type ClassValue } from "clsx"
import type { ComponentProps, JSXElementConstructor } from "react"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// className narrowed to a string, so wrappers can merge it with cn().
export type Styled<C extends JSXElementConstructor<any>> = Omit<ComponentProps<C>, "className"> & { className?: string }
