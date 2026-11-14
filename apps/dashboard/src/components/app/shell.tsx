import type { DatabaseOut } from "@lumbung/client"
import { Link, type LinkProps, useRouter } from "@tanstack/react-router"
import {
  DatabaseIcon,
  KeyRoundIcon,
  LayoutGridIcon,
  LogOutIcon,
  MenuIcon,
  PanelLeftIcon,
  ScrollTextIcon,
  SearchIcon,
  SettingsIcon,
  UserIcon,
  UsersIcon,
} from "lucide-react"
import { createContext, type ReactNode, use, useEffect, useState } from "react"
import { Avatar } from "@/components/ui/avatar"
import { Button, buttonVariants } from "@/components/ui/button"
import { DialogShortcut, DialogTrigger, createDialogController } from "@/components/ui/dialog"
import { Drawer, DrawerContent, DrawerTitle, DrawerTrigger, createDrawerController } from "@/components/ui/drawer"
import { ScrollArea, Separator } from "@/components/ui/layout"
import { Menu, MenuContent, MenuGroup, MenuGroupLabel, MenuItem, MenuRadioGroup, MenuRadioItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu"
import { toast } from "@/components/ui/toast"
import { Tooltip } from "@/components/ui/tooltip"
import { authClient } from "@/lib/auth-client"
import { ROLE_LABEL, can } from "@/lib/roles"
import { forgetSession } from "@/lib/session-cache"
import { type Theme, applyTheme, readSidebarCollapsed, readTheme, setSidebarCollapsed } from "@/lib/theme"
import { cn } from "@/lib/utils"
import type { Viewer } from "@/server/session"
import { CommandPalette } from "./command-palette"
import { Mark } from "./mark"

type ShellState = {
  drawer: ReturnType<typeof createDrawerController<unknown, unknown>>
  palette: ReturnType<typeof createDialogController<unknown, unknown>>
  collapsed: boolean
  toggleCollapsed: () => void
}

const ShellContext = createContext<ShellState | null>(null)

function useShell(): ShellState {
  const s = use(ShellContext)
  if (!s) throw new Error("useShell outside AppShell")
  return s
}

export function AppShell({ viewer, databases, children }: { viewer: Viewer; databases: DatabaseOut[] | null; children: ReactNode }) {
  const [drawer] = useState(() => createDrawerController<unknown, unknown>())
  const [palette] = useState(() => createDialogController<unknown, unknown>())
  const router = useRouter()
  const [collapsed, setCollapsed] = useState(false)
  useEffect(() => setCollapsed(readSidebarCollapsed()), [])
  const toggleCollapsed = () => {
    setSidebarCollapsed(!collapsed)
    setCollapsed(!collapsed)
  }
  return (
    <ShellContext value={{ drawer, palette, collapsed, toggleCollapsed }}>
      <div className="flex min-h-svh">
        <aside
          data-sidebar-rail=""
          className="sticky top-0 hidden h-svh w-60 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground md:flex sidebar-collapsed:w-[53px]"
        >
          <SidebarBody viewer={viewer} databases={databases} rail={collapsed} />
        </aside>
        <Drawer controller={drawer} side="start" preventEdgeSwipe>
          <DrawerContent>
            <DrawerTitle className="sr-only">Navigation</DrawerTitle>
            <SidebarBody viewer={viewer} databases={databases} onNavigate={() => drawer.close()} />
          </DrawerContent>
        </Drawer>
        <div className="flex min-w-0 flex-1 flex-col">{children}</div>
      </div>
      <CommandPalette controller={palette} viewer={viewer} databases={databases} navigate={(to, params) => router.navigate({ to, params } as never)} />
    </ShellContext>
  )
}

type NavLink = { to: LinkProps["to"]; params?: Record<string, string>; label: string; icon: ReactNode; exact?: boolean }

function SidebarBody({ viewer, databases, rail = false, onNavigate }: { viewer: Viewer; databases: DatabaseOut[] | null; rail?: boolean; onNavigate?: () => void }) {
  const all: (NavLink & { show: boolean })[] = [
    { show: can(viewer.role, "admin"), to: "/tokens", label: "Tokens", icon: <KeyRoundIcon /> },
    { show: can(viewer.role, "admin"), to: "/members", label: "Members", icon: <UsersIcon /> },
    { show: can(viewer.role, "write"), to: "/audit", label: "Audit log", icon: <ScrollTextIcon /> },
    { show: can(viewer.role, "admin"), to: "/settings", label: "Settings", icon: <SettingsIcon /> },
  ]
  const instance = all.filter((item) => item.show)

  return (
    <>
      <div className="flex h-14 shrink-0 items-center px-3">
        <Link to="/" onClick={onNavigate} className="flex min-w-0 items-center gap-2 rounded-md p-0.5">
          <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <Mark className="size-4" />
          </span>
          <span className="truncate text-[13px] font-[590] sidebar-collapsed:sr-only">Lumbung</span>
        </Link>
      </div>
      {!onNavigate && <SearchTrigger rail={rail} />}
      <ScrollArea className="min-h-0 flex-1" viewportClassName="px-2 pb-4">
        <nav aria-label="Main" className="flex flex-col gap-5">
          <div className="flex flex-col gap-0.5">
            <NavItem to="/" label="Overview" icon={<LayoutGridIcon />} exact rail={rail} onNavigate={onNavigate} />
          </div>
          <NavGroup label="Databases">
            {databases === null && <p className="px-2 text-xs text-muted-foreground sidebar-collapsed:hidden">Can't load databases</p>}
            {databases?.length === 0 && <p className="px-2 text-xs text-muted-foreground sidebar-collapsed:hidden">None yet</p>}
            {databases?.map((d) => (
              <NavItem key={d.id} to="/db/$db" params={{ db: d.name }} label={d.name} icon={<DatabaseIcon />} rail={rail} onNavigate={onNavigate} />
            ))}
          </NavGroup>
          {instance.length > 0 && (
            <NavGroup label="Instance">
              {instance.map(({ show: _, ...item }) => (
                <NavItem key={item.label} {...item} rail={rail} onNavigate={onNavigate} />
              ))}
            </NavGroup>
          )}
        </nav>
      </ScrollArea>
      <div className="shrink-0 border-t border-sidebar-border p-2">
        <UserMenu viewer={viewer} />
      </div>
    </>
  )
}

function SearchTrigger({ rail }: { rail: boolean }) {
  const { palette } = useShell()
  return (
    <div className="shrink-0 px-2 pb-3">
      <Tooltip content="Search" side="right" disabled={!rail}>
        <DialogTrigger
          controller={palette}
          shortcut="mod+k"
          aria-label={rail ? "Search" : undefined}
          className="flex h-8 w-full min-w-0 cursor-default items-center gap-2 rounded-md border bg-background px-2 text-[13px] text-muted-foreground transition-colors duration-150 hover:text-foreground hover:duration-0 sidebar-collapsed:justify-center sidebar-collapsed:px-0 [&_svg]:size-4 [&_svg]:shrink-0 dark:bg-secondary"
        >
          <SearchIcon aria-hidden="true" className="text-faint" />
          <span className="flex-1 truncate text-left sidebar-collapsed:sr-only">Search…</span>
          <DialogShortcut className="text-xs text-muted-foreground sidebar-collapsed:hidden" />
        </DialogTrigger>
      </Tooltip>
    </div>
  )
}

function NavGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <h2 className="flex h-7 items-center px-2 text-xs font-[510] text-muted-foreground sidebar-collapsed:sr-only">{label}</h2>
      {children}
    </div>
  )
}

function NavItem({ to, params, label, icon, exact, rail, onNavigate }: NavLink & { rail: boolean; onNavigate?: () => void }) {
  return (
    <Tooltip content={label} side="right" disabled={!rail}>
      <Link
        to={to}
        params={params as never}
        onClick={onNavigate}
        activeOptions={{ exact, includeSearch: false }}
        className="flex h-8 min-w-0 items-center gap-2 rounded-md px-2 text-[13px] text-sidebar-foreground transition-colors duration-150 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground hover:duration-0 data-[status=active]:bg-sidebar-accent data-[status=active]:font-[510] data-[status=active]:text-sidebar-accent-foreground [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-faint data-[status=active]:[&_svg]:text-foreground"
      >
        {icon}
        <span className="truncate sidebar-collapsed:sr-only">{label}</span>
      </Link>
    </Tooltip>
  )
}

function UserMenu({ viewer }: { viewer: Viewer }) {
  const router = useRouter()
  const [theme, setTheme] = useState<Theme>("system")
  useEffect(() => setTheme(readTheme()), [])
  // The palette can change the theme too, so re-read it whenever the menu opens.
  const syncTheme = (open: boolean) => open && setTheme(readTheme())

  async function signOut() {
    let message = ""
    try {
      const { error } = await authClient().signOut()
      if (error) message = error.message || error.statusText || "Try again."
    } catch (e) {
      message = e instanceof Error ? e.message : String(e)
    }
    if (message) {
      toast.error({ title: "Couldn't sign out", description: message })
      return
    }
    forgetSession()
    await router.navigate({ to: "/login" })
  }

  return (
    <Menu onOpenChange={syncTheme}>
      <MenuTrigger className="flex w-full min-w-0 cursor-default items-center gap-2 rounded-md p-1 text-left transition-colors duration-150 hover:bg-sidebar-accent hover:duration-0 data-open:bg-sidebar-accent">
        <Avatar name={viewer.name || viewer.email} />
        <span className="grid min-w-0 leading-tight sidebar-collapsed:sr-only">
          <span className="truncate text-[13px] font-[510] text-foreground">{viewer.name || viewer.email}</span>
          <span className="truncate text-xs text-muted-foreground">{ROLE_LABEL[viewer.role]}</span>
        </span>
      </MenuTrigger>
      <MenuContent side="top" align="start" className="w-56">
        <MenuGroupLabel className="truncate font-normal">{viewer.email}</MenuGroupLabel>
        <MenuItem render={<Link to="/account" />}>
          <UserIcon aria-hidden="true" />
          Account
        </MenuItem>
        <MenuSeparator />
        <MenuGroup>
          <MenuGroupLabel>Theme</MenuGroupLabel>
          <MenuRadioGroup
            value={theme}
            onValueChange={(v) => {
              const t = v as Theme
              setTheme(t)
              applyTheme(t)
            }}
          >
            <MenuRadioItem value="light">Light</MenuRadioItem>
            <MenuRadioItem value="dark">Dark</MenuRadioItem>
            <MenuRadioItem value="system">System</MenuRadioItem>
          </MenuRadioGroup>
        </MenuGroup>
        <MenuSeparator />
        <MenuItem onSelect={signOut}>
          <LogOutIcon aria-hidden="true" />
          Sign out
        </MenuItem>
      </MenuContent>
    </Menu>
  )
}

export function PageHeader({ children, actions }: { children: ReactNode; actions?: ReactNode }) {
  const { drawer, palette, collapsed, toggleCollapsed } = useShell()
  return (
    <header className="sticky top-0 z-20 flex h-[calc(3.5rem+env(safe-area-inset-top))] shrink-0 items-center gap-2 bg-background/85 pt-[env(safe-area-inset-top)] pr-[max(1rem,env(safe-area-inset-right))] pl-[max(1rem,env(safe-area-inset-left))] backdrop-blur-md md:px-6">
      <DrawerTrigger controller={drawer} aria-label="Open navigation" className={buttonVariants({ variant: "ghost", size: "icon-sm", className: "-ml-1.5 md:hidden" })}>
        <MenuIcon aria-hidden="true" />
      </DrawerTrigger>
      <Button variant="ghost" size="icon-sm" aria-label="Toggle sidebar" aria-expanded={!collapsed} onClick={toggleCollapsed} className="-ml-1.5 hidden md:inline-flex">
        <PanelLeftIcon aria-hidden="true" />
      </Button>
      <Separator orientation="vertical" className="mr-1 hidden md:block" />
      <div className="flex min-w-0 flex-1 items-center gap-2">{children}</div>
      <DialogTrigger controller={palette} aria-label="Search" className={buttonVariants({ variant: "ghost", size: "icon-sm", className: "md:hidden" })}>
        <SearchIcon aria-hidden="true" />
      </DialogTrigger>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </header>
  )
}

export function PageTitle({ children }: { children: ReactNode }) {
  return <h1 className="truncate text-[13px] font-[590]">{children}</h1>
}

// One content width for the tab row and every page body, so left edges line up.
export const CONTAINER = "mx-auto w-full max-w-6xl px-4 md:px-6"

export function PageBody({ children, className }: { children: ReactNode; className?: string }) {
  return <main className={cn(CONTAINER, "flex-1 py-6 md:py-8", className)}>{children}</main>
}
