import {
  TabsIndicator,
  TabsList as TabsListPart,
  TabsPanel as TabsPanelPart,
  TabsTabIcon,
  TabsTab as TabsTabPart,
} from "@ui-sdk/react/tabs"
import type { ReactNode } from "react"
import { type Styled, cn } from "@/lib/utils"

export { Tabs } from "@ui-sdk/react/tabs"

// The hairline is an inset shadow, not a border, so the 2px bar can sit on it
// without overflowing a row that scrolls sideways.
export function TabsList({ className, children, ...props }: Omit<Styled<typeof TabsListPart>, "children"> & { children?: ReactNode }) {
  return (
    <TabsListPart className={cn("relative flex items-center gap-5 shadow-[inset_0_-1px_0_var(--border)]", className)} {...props}>
      {children}
      <TabsIndicator className="absolute bottom-0 left-(--active-tab-left) h-0.5 w-(--active-tab-width) rounded-full bg-foreground transition-[left,width] duration-200 ease-out-quad data-[activation-direction=none]:transition-none motion-reduce:transition-none" />
    </TabsListPart>
  )
}

type TabProps = Omit<Styled<typeof TabsTabPart>, "children"> & {
  children?: ReactNode
  icon?: ReactNode
  count?: number
}

export function TabsTab({ className, children, icon, count, ...props }: TabProps) {
  return (
    <TabsTabPart
      className={cn(
        "inline-flex h-10 shrink-0 cursor-default items-center gap-1.5 text-[13px] font-[510] text-muted-foreground transition-colors duration-100 hover:text-foreground data-disabled:opacity-50 data-selected:text-foreground",
        className,
      )}
      {...props}
    >
      {icon && <TabsTabIcon className="inline-flex text-faint [&_svg]:size-4">{icon}</TabsTabIcon>}
      {children}
      {count !== undefined && (
        <>
          {" "}
          <span className="text-xs font-normal text-faint tabular-nums">{count}</span>
        </>
      )}
    </TabsTabPart>
  )
}

export function TabsPanel({ className, ...props }: Styled<typeof TabsPanelPart>) {
  return <TabsPanelPart className={cn("outline-none", className)} {...props} />
}
