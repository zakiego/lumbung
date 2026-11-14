import {
  MenuContent as MenuContentPart,
  MenuGroupLabel as MenuGroupLabelPart,
  MenuItemIndicator,
  MenuItem as MenuItemPart,
  MenuRadioItem as MenuRadioItemPart,
  MenuSeparator as MenuSeparatorPart,
} from "@ui-sdk/react/menu"
import { CheckIcon } from "lucide-react"
import type { ReactNode } from "react"
import { type Styled, cn } from "@/lib/utils"
import { popupItem, popupSurface } from "./select"

export { Menu, MenuGroup, MenuRadioGroup, MenuTrigger, createMenuController } from "@ui-sdk/react/menu"

export function MenuContent({ className, ...props }: Styled<typeof MenuContentPart>) {
  return <MenuContentPart sideOffset={4} className={cn(popupSurface, "min-w-44", className)} {...props} />
}

export function MenuItem({ className, ...props }: Styled<typeof MenuItemPart>) {
  return (
    <MenuItemPart
      className={cn(popupItem, "data-destructive:text-destructive data-destructive:[&_svg]:text-destructive", className)}
      {...props}
    />
  )
}

export function MenuRadioItem({ className, children, ...props }: Omit<Styled<typeof MenuRadioItemPart>, "children"> & { children?: ReactNode }) {
  return (
    <MenuRadioItemPart className={cn(popupItem, "pr-8", className)} {...props}>
      {children}
      <MenuItemIndicator className="absolute right-2 inline-flex">
        <CheckIcon aria-hidden="true" className="text-foreground!" />
      </MenuItemIndicator>
    </MenuRadioItemPart>
  )
}

export function MenuGroupLabel({ className, ...props }: Styled<typeof MenuGroupLabelPart>) {
  return <MenuGroupLabelPart className={cn("px-2 pt-1.5 pb-1 text-xs font-[510] text-muted-foreground", className)} {...props} />
}

export function MenuSeparator({ className, ...props }: Styled<typeof MenuSeparatorPart>) {
  return <MenuSeparatorPart className={cn("-mx-1 my-1 h-px bg-border", className)} {...props} />
}
