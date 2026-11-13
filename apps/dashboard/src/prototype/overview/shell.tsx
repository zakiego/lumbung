import {
  DatabaseIcon,
  GitBranchIcon,
  KeyRoundIcon,
  ScrollTextIcon,
  SettingsIcon,
  UsersIcon,
} from "lucide-react"
import type { ReactNode } from "react"
import { useState } from "react"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { branches, database } from "./data"

const databases = ["shop", "analytics", "notes-app"]
const instance = [
  { label: "Tokens", icon: KeyRoundIcon },
  { label: "Members", icon: UsersIcon },
  { label: "Audit log", icon: ScrollTextIcon },
  { label: "Settings", icon: SettingsIcon },
]
const sections = ["Overview", "Data", "SQL", "Schema", "Branches", "Snapshots", "Settings"]

export function Mark({ className = "size-4" }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true" fill="currentColor">
      <path d="M16 3 3 13h4v9h18v-9h4L16 3Zm-5 12h10v5H11v-5Zm-2 9h2v5H9v-5Zm12 0h2v5h-2v-5Z" />
    </svg>
  )
}

function AppSidebar() {
  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" tooltip="Lumbung">
              <span className="flex size-8 items-center justify-center rounded-md bg-sidebar-primary text-sidebar-primary-foreground">
                <Mark />
              </span>
              <span className="grid text-left leading-tight">
                <span className="text-[13px] font-semibold">Lumbung</span>
                <span className="font-mono text-xs text-muted-foreground">db.example.com</span>
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Databases</SidebarGroupLabel>
          <SidebarMenu className="gap-1">
            {databases.map((name) => (
              <SidebarMenuItem key={name}>
                <SidebarMenuButton isActive={name === database.name} tooltip={name}>
                  <DatabaseIcon />
                  <span>{name}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>
        <SidebarGroup>
          <SidebarGroupLabel>Instance</SidebarGroupLabel>
          <SidebarMenu className="gap-1">
            {instance.map((item) => (
              <SidebarMenuItem key={item.label}>
                <SidebarMenuButton tooltip={item.label}>
                  <item.icon />
                  <span>{item.label}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            ))}
          </SidebarMenu>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" tooltip="Zaki">
              <Avatar className="size-8 rounded-md">
                <AvatarFallback className="rounded-md text-xs">ZM</AvatarFallback>
              </Avatar>
              <span className="grid text-left leading-tight">
                <span className="text-[13px] font-medium">Zaki</span>
                <span className="text-xs text-muted-foreground">Owner</span>
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

export function BranchSwitcher({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <Select value={value} onValueChange={(v) => v && onChange(v)}>
      <SelectTrigger size="sm" className="gap-1.5 font-mono text-[13px]" aria-label="Branch">
        <GitBranchIcon className="text-muted-foreground" />
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {branches.map((b) => (
          <SelectItem key={b.name} value={b.name} className="font-mono text-[13px]">
            {b.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

// The app shell every variant shares: sidebar-07 (collapses to icons), a
// header with the branch switcher, and line tabs for the database's pages.
export function Shell({ children, actions }: { children: (branch: string) => ReactNode; actions?: ReactNode }) {
  const [branch, setBranch] = useState("main")
  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset>
        <header className="flex h-14 shrink-0 items-center gap-2 px-4">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="mr-1 data-vertical:h-4 data-vertical:self-auto" />
          <Breadcrumb>
            <BreadcrumbList>
              <BreadcrumbItem className="hidden sm:block">
                <BreadcrumbLink href="#">Databases</BreadcrumbLink>
              </BreadcrumbItem>
              <BreadcrumbSeparator className="hidden sm:block" />
              <BreadcrumbItem className="font-medium text-foreground">{database.name}</BreadcrumbItem>
            </BreadcrumbList>
          </Breadcrumb>
          <BranchSwitcher value={branch} onChange={setBranch} />
          <div className="ml-auto flex items-center gap-2">{actions}</div>
        </header>
        <Tabs defaultValue="Overview" className="gap-0">
          <div className="overflow-x-auto border-b px-4">
            <TabsList variant="line" className="h-10">
              {sections.map((s) => (
                <TabsTrigger key={s} value={s} className="px-2 text-[13px]">
                  {s}
                </TabsTrigger>
              ))}
            </TabsList>
          </div>
        </Tabs>
        <div className="mx-auto w-full max-w-6xl flex-1 px-4 py-6 md:px-8 md:py-8">{children(branch)}</div>
      </SidebarInset>
    </SidebarProvider>
  )
}
