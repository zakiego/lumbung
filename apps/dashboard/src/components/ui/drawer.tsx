import { DrawerBackdrop, DrawerContent as DrawerContentPart } from "@ui-sdk/react/drawer"
import type { ReactNode } from "react"
import { type Styled, cn } from "@/lib/utils"

export { Drawer, DrawerClose, DrawerTitle, DrawerTrigger, createDrawerController } from "@ui-sdk/react/drawer"

export function DrawerContent({ className, children, ...props }: Omit<Styled<typeof DrawerContentPart>, "children"> & { children?: ReactNode }) {
  return (
    <>
      <DrawerBackdrop className="fixed inset-0 bg-overlay" />
      <DrawerContentPart className={cn("flex h-full w-[min(288px,85vw)] flex-col border-r bg-sidebar text-sidebar-foreground outline-none", className)} {...props}>
        {children}
      </DrawerContentPart>
    </>
  )
}
