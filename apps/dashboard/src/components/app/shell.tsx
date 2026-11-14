import type { BucketOut, DatabaseOut } from "@lumbung/client"
import { Link, type LinkProps, useParams, useRouter } from "@tanstack/react-router"
import {
  ArchiveIcon,
  ChevronsUpDownIcon,
  DatabaseIcon,
  KeyRoundIcon,
  LayoutGridIcon,
  LogOutIcon,
  MenuIcon,
  PanelLeftIcon,
  PlusIcon,
  ScrollTextIcon,
  SearchIcon,
  SettingsIcon,
  SlidersHorizontalIcon,
  Trash2Icon,
  UserIcon,
  UsersIcon,
} from "lucide-react"
import { createContext, type ReactElement, type ReactNode, use, useEffect, useState } from "react"
import { Avatar } from "@/components/ui/avatar"
import { Button, buttonVariants } from "@/components/ui/button"
import { DialogShortcut, DialogTrigger, createDialogController } from "@/components/ui/dialog"
import { Drawer, DrawerContent, DrawerTitle, DrawerTrigger, createDrawerController } from "@/components/ui/drawer"
import { ScrollArea, Separator } from "@/components/ui/layout"
import { Menu, MenuContent, MenuGroup, MenuGroupLabel, MenuItem, MenuRadioGroup, MenuRadioItem, MenuSeparator, MenuTrigger } from "@/components/ui/menu"
import { toast } from "@/components/ui/toast"
import { Tooltip, TruncatedTip } from "@/components/ui/tooltip"
import { authClient } from "@/lib/auth-client"
import { createWorkspace } from "@/fns/workspaces"
import { ROLE_LABEL, can } from "@/lib/roles"
import { forgetSession } from "@/lib/session-cache"
import { type Theme, applyTheme, readSidebarCollapsed, readTheme, setSidebarCollapsed } from "@/lib/theme"
import { cn } from "@/lib/utils"
import type { Person, WorkspaceInfo } from "@/lib/workspace"
import { CreateBucketDialog, useCreateBucket } from "./buckets"
import { CommandPalette } from "./command-palette"
import { Mark } from "./mark"
import { CreateDatabaseDialog, NameDialog, useCreateDatabase } from "./name-dialog"
import { WorkspaceProvider, useWorkspaceOrNull } from "./workspace"

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

type ShellData = {
  viewer: Person
  /** The workspace on screen. Null for someone who has none yet. */
  workspace: WorkspaceInfo | null
  /** Every workspace the person may use, for the switcher. */
  workspaces: WorkspaceInfo[]
  databases: DatabaseOut[] | null
  buckets?: BucketOut[] | null
}

export function AppShell({ viewer, workspace, workspaces, databases, buckets = [], children }: ShellData & { children: ReactNode }) {
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
    <WorkspaceProvider workspace={workspace}>
     <ShellContext value={{ drawer, palette, collapsed, toggleCollapsed }}>
      <div className="flex min-h-svh">
        <aside
          data-sidebar-rail=""
          className="sticky top-0 hidden h-svh w-60 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground md:flex sidebar-collapsed:w-[53px]"
        >
          <SidebarBody viewer={viewer} workspace={workspace} workspaces={workspaces} databases={databases} buckets={buckets} rail={collapsed} />
        </aside>
        <Drawer controller={drawer} side="start" preventEdgeSwipe>
          <DrawerContent>
            <DrawerTitle className="sr-only">Navigation</DrawerTitle>
            <SidebarBody viewer={viewer} workspace={workspace} workspaces={workspaces} databases={databases} buckets={buckets} onNavigate={() => drawer.close()} />
          </DrawerContent>
        </Drawer>
        <div className="flex min-w-0 flex-1 flex-col">{children}</div>
      </div>
      <CommandPalette
        controller={palette}
        viewer={viewer}
        workspace={workspace}
        workspaces={workspaces}
        databases={databases}
        navigate={(to, params) => router.navigate({ to, params } as never)}
      />
     </ShellContext>
    </WorkspaceProvider>
  )
}

type NavLink = { to: LinkProps["to"]; params?: Record<string, string>; label: string; /** The full name, when two items share a label. */ name?: string; icon: ReactNode; exact?: boolean }

function SidebarBody({
  viewer,
  workspace,
  workspaces,
  databases,
  buckets = [],
  rail = false,
  onNavigate,
}: ShellData & { rail?: boolean; onNavigate?: () => void }) {
  return (
    <>
      <div className="flex h-14 shrink-0 items-center px-2">
        <WorkspaceSwitcher viewer={viewer} workspace={workspace} workspaces={workspaces} onNavigate={onNavigate} />
      </div>
      {!onNavigate && <SearchTrigger rail={rail} />}
      <ScrollArea className="min-h-0 flex-1" viewportClassName="px-2 pb-4">
        <nav aria-label="Main" className="flex flex-col gap-5">
          {workspace && <WorkspaceNav viewer={viewer} ws={workspace.name} databases={databases} buckets={buckets} rail={rail} onNavigate={onNavigate} />}
          {viewer.owner && (
            <NavGroup label="Instance">
              <NavItem to="/members" label="Members" icon={<UsersIcon />} rail={rail} onNavigate={onNavigate} />
              <NavItem to="/settings" label="Settings" name="Instance settings" icon={<SettingsIcon />} rail={rail} onNavigate={onNavigate} />
            </NavGroup>
          )}
        </nav>
      </ScrollArea>
      <div className="shrink-0 border-t border-sidebar-border p-2">
        <UserMenu viewer={viewer} placed={!!workspace || viewer.owner} />
      </div>
    </>
  )
}

function WorkspaceNav({
  viewer,
  ws,
  databases,
  buckets,
  rail,
  onNavigate,
}: {
  viewer: Person
  ws: string
  databases: DatabaseOut[] | null
  buckets: BucketOut[] | null
  rail: boolean
  onNavigate?: () => void
}) {
  const canAdmin = can(viewer.role, "admin")
  const createDb = useCreateDatabase(onNavigate)
  const createBucket = useCreateBucket(onNavigate)
  const params = { ws }
  return (
    <>
      <div className="flex flex-col gap-0.5">
        <NavItem to="/w/$ws" params={params} label="Overview" icon={<LayoutGridIcon />} exact rail={rail} onNavigate={onNavigate} />
      </div>
      {(databases === null || databases.length > 0 || canAdmin) && (
        <NavGroup label="Databases">
          {databases === null && <p className="px-2 text-xs text-muted-foreground sidebar-collapsed:hidden">Can't load databases</p>}
          {databases?.length === 0 && <CreateDatabaseDialog onCreate={createDb} {...createSlot("New database", rail)} />}
          {databases?.map((d) => (
            <NavItem key={d.id} to="/w/$ws/db/$db" params={{ ws, db: d.name }} label={d.name} icon={<DatabaseIcon />} rail={rail} onNavigate={onNavigate} />
          ))}
        </NavGroup>
      )}
      {(buckets === null || buckets.length > 0 || canAdmin) && (
        <NavGroup label="Buckets" to="/w/$ws/buckets" params={params} onNavigate={onNavigate}>
          {buckets === null && <p className="px-2 text-xs text-muted-foreground sidebar-collapsed:hidden">Can't load buckets</p>}
          {buckets?.length === 0 && <CreateBucketDialog onCreate={createBucket} {...createSlot("New bucket", rail)} />}
          {buckets?.map((b) => (
            <NavItem key={b.id} to="/w/$ws/buckets/$bucket" params={{ ws, bucket: b.name }} label={b.name} icon={<ArchiveIcon />} rail={rail} onNavigate={onNavigate} />
          ))}
        </NavGroup>
      )}
      {canAdmin && (
        <NavGroup label="Workspace">
          <NavItem to="/w/$ws/tokens" params={params} label="Tokens" icon={<KeyRoundIcon />} rail={rail} onNavigate={onNavigate} />
          <NavItem to="/w/$ws/audit" params={params} label="Audit log" icon={<ScrollTextIcon />} rail={rail} onNavigate={onNavigate} />
          <NavItem to="/w/$ws/trash" params={params} label="Trash" icon={<Trash2Icon />} rail={rail} onNavigate={onNavigate} />
          <NavItem to="/w/$ws/settings" params={params} label="Settings" name="Workspace settings" icon={<SlidersHorizontalIcon />} rail={rail} onNavigate={onNavigate} />
        </NavGroup>
      )}
    </>
  )
}

// The workspace on screen, at the top of the sidebar. It opens the list of
// workspaces the person may use, and for an owner a way to make another.
function WorkspaceSwitcher({
  viewer,
  workspace,
  workspaces,
  onNavigate,
}: {
  viewer: Person
  workspace: WorkspaceInfo | null
  workspaces: WorkspaceInfo[]
  onNavigate?: () => void
}) {
  const router = useRouter()
  const [creating, setCreating] = useState(false)
  const go = async (name: string) => {
    onNavigate?.()
    await router.navigate({ to: "/w/$ws", params: { ws: name } })
  }
  const brand = (
    <>
      <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
        <Mark className="size-4" />
      </span>
      <span data-truncate className={cn("min-w-0 flex-1 truncate text-left text-[13px] font-[590] sidebar-collapsed:sr-only", workspace && "font-mono")}>
        {workspace?.name ?? "Lumbung"}
      </span>
    </>
  )
  if (!workspace) return <div className="flex min-w-0 items-center gap-2 p-1">{brand}</div>
  // With one workspace and no way to make another, there is nothing to switch to.
  if (workspaces.length < 2 && !viewer.owner) {
    return (
      <TruncatedTip text={workspace.name} side="right">
        <Link to="/w/$ws" params={{ ws: workspace.name }} onClick={onNavigate} className="flex w-full min-w-0 items-center gap-2 rounded-md p-1">
          {brand}
        </Link>
      </TruncatedTip>
    )
  }
  return (
    <>
      <Menu>
        <MenuTrigger
          aria-label={`Workspace ${workspace.name}. Switch workspace`}
          title={workspace.name}
          className="flex w-full min-w-0 cursor-default items-center gap-2 rounded-md p-1 transition-colors duration-150 select-none hover:bg-sidebar-accent hover:duration-0 active:bg-sidebar-accent active:duration-0 data-open:bg-sidebar-accent"
        >
          {brand}
          <ChevronsUpDownIcon aria-hidden="true" className="size-3.5 shrink-0 text-faint sidebar-collapsed:hidden" />
        </MenuTrigger>
        <MenuContent align="start" className="flex max-h-(--available-height) w-64 flex-col">
          <MenuGroup className="min-h-0 overflow-y-auto overscroll-contain">
            <MenuGroupLabel>Workspaces</MenuGroupLabel>
            <MenuRadioGroup
              value={workspace.id}
              onValueChange={(id) => {
                const next = workspaces.find((w) => w.id === id)
                if (next && next.id !== workspace.id) void go(next.name)
              }}
            >
              {workspaces.map((w) => (
                <MenuRadioItem key={w.id} value={w.id} className="h-auto min-h-8 py-1.5">
                  <span className="min-w-0 font-mono [overflow-wrap:anywhere]">{w.name}</span>
                </MenuRadioItem>
              ))}
            </MenuRadioGroup>
          </MenuGroup>
          {viewer.owner && (
            <>
              <MenuSeparator />
              <MenuItem onSelect={() => setCreating(true)}>
                <PlusIcon aria-hidden="true" />
                New workspace
              </MenuItem>
            </>
          )}
        </MenuContent>
      </Menu>
      {viewer.owner && (
        <NameDialog
          open={creating}
          onOpenChange={setCreating}
          title="Create workspace"
          label="Name"
          placeholder="my-agent"
          submit="Create"
          pendingSubmit="Creating…"
          onSubmit={async (name) => {
            await createWorkspace({ data: { name } })
            toast.success(`Created ${name}`)
            // The session carries the list of workspaces; ask for it again.
            forgetSession()
            await router.invalidate()
            await go(name)
          }}
        />
      )}
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

function NavGroup({
  label,
  to,
  params,
  onNavigate,
  children,
}: {
  label: string
  to?: LinkProps["to"]
  params?: Record<string, string>
  onNavigate?: () => void
  children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <h2 className="flex h-7 items-center px-2 text-xs font-[510] text-muted-foreground sidebar-collapsed:sr-only">
        {to ? (
          <Link
            to={to}
            params={params as never}
            onClick={onNavigate}
            activeOptions={{ exact: true }}
            className="rounded-sm transition-colors duration-150 hover:text-foreground hover:duration-0 sidebar-collapsed:invisible"
          >
            {label}
          </Link>
        ) : (
          label
        )}
      </h2>
      {children}
    </div>
  )
}

const NAV_ITEM =
  "flex h-8 min-w-0 items-center gap-2 rounded-md px-2 text-[13px] transition-colors duration-150 hover:bg-sidebar-accent hover:text-sidebar-accent-foreground hover:duration-0 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-faint"

// The first item of an empty group: a quiet button that opens the create dialog.
function createSlot(label: string, rail: boolean) {
  return {
    triggerClassName: cn(
      NAV_ITEM,
      "w-full cursor-default text-left text-muted-foreground data-open:bg-sidebar-accent data-open:text-sidebar-accent-foreground [&>span]:truncate sidebar-collapsed:[&>span]:sr-only",
    ),
    wrapTrigger: (trigger: ReactElement<Record<string, unknown>>) => (
      <Tooltip content={label} side="right" disabled={!rail}>
        {trigger}
      </Tooltip>
    ),
  }
}

function NavItem({ to, params, label, name, icon, exact, rail, onNavigate }: NavLink & { rail: boolean; onNavigate?: () => void }) {
  return (
    <Tooltip content={name ?? label} side="right" disabled={!rail}>
      <Link
        to={to}
        aria-label={name}
        params={params as never}
        onClick={onNavigate}
        activeOptions={{ exact, includeSearch: false }}
        className={cn(NAV_ITEM, "text-sidebar-foreground data-[status=active]:bg-sidebar-accent data-[status=active]:font-[510] data-[status=active]:text-sidebar-accent-foreground data-[status=active]:[&_svg]:text-foreground")}
      >
        {icon}
        <span className="truncate sidebar-collapsed:sr-only">{label}</span>
      </Link>
    </Tooltip>
  )
}

// `placed` is false for someone with no workspace yet, who has no role to show.
export function UserMenu({ viewer, placed = true }: { viewer: Person; placed?: boolean }) {
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
      <MenuTrigger className="flex w-full min-w-0 cursor-default items-center gap-2 rounded-md p-1 text-left transition-colors duration-150 select-none hover:bg-sidebar-accent hover:duration-0 active:bg-sidebar-accent active:duration-0 data-open:bg-sidebar-accent">
        <Avatar name={viewer.name || viewer.email} />
        <span className="grid min-w-0 leading-tight sidebar-collapsed:sr-only">
          <span className="truncate text-[13px] font-[510] text-foreground">{viewer.name || viewer.email}</span>
          {placed && <span className="truncate text-xs text-muted-foreground">{ROLE_LABEL[viewer.role]}</span>}
        </span>
      </MenuTrigger>
      <MenuContent side="top" align="start" className="w-56">
        <MenuGroup>
          <MenuGroupLabel className="truncate font-normal">{viewer.email}</MenuGroupLabel>
          <MenuItem render={<Link to="/account" />}>
            <UserIcon aria-hidden="true" />
            Account
          </MenuItem>
        </MenuGroup>
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
  // On a phone and with the sidebar collapsed, nothing else says which
  // workspace the page is in. Pages outside a workspace don't get it.
  const workspace = useWorkspaceOrNull()
  const inside = useParams({ strict: false }).ws !== undefined && workspace
  return (
    <header className="sticky top-0 z-20 flex h-[calc(3.5rem+env(safe-area-inset-top))] shrink-0 items-center gap-2 bg-background/85 pt-[env(safe-area-inset-top)] pr-[max(1rem,env(safe-area-inset-right))] pl-[max(1rem,env(safe-area-inset-left))] backdrop-blur-md md:px-6">
      <DrawerTrigger controller={drawer} aria-label="Open navigation" className={buttonVariants({ variant: "ghost", size: "icon-sm", className: "-ml-1.5 md:hidden" })}>
        <MenuIcon aria-hidden="true" />
      </DrawerTrigger>
      <Button variant="ghost" size="icon-sm" aria-label="Toggle sidebar" aria-expanded={!collapsed} onClick={toggleCollapsed} className="-ml-1.5 hidden md:inline-flex">
        <PanelLeftIcon aria-hidden="true" />
      </Button>
      <Separator orientation="vertical" className="mr-1 hidden md:block" />
      {inside && (
        // It starts at no width and takes only the room the title leaves, so the title is never the one cut.
        <span className={cn("flex min-w-0 max-w-fit grow-[9999] basis-0 items-center gap-1.5 overflow-hidden px-0.5 py-1 text-[13px]", !collapsed && "md:hidden")}>
          <TruncatedTip text={workspace.name}>
            <Link
              to="/w/$ws"
              params={{ ws: workspace.name }}
              className="block min-w-0 truncate rounded-sm font-mono text-muted-foreground transition-colors duration-100 hover:text-foreground"
            >
              {workspace.name}
            </Link>
          </TruncatedTip>
          <span aria-hidden="true" className="shrink-0 text-faint">
            /
          </span>
        </span>
      )}
      <div className="flex min-w-0 flex-auto items-center gap-2">{children}</div>
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

/** The shell for someone who isn't in a workspace: none yet, one that doesn't exist, or an engine that didn't answer. */
export function LoneShell({
  account,
  title,
  workspaces = [],
  children,
}: {
  account: Omit<Person, "role">
  title: string
  /** The workspaces the person may use, for search. */
  workspaces?: WorkspaceInfo[]
  children: ReactNode
}) {
  return (
    <AppShell viewer={{ ...account, role: account.owner ? "owner" : "readonly" }} workspace={null} workspaces={workspaces} databases={[]} buckets={[]}>
      <PageHeader>
        <PageTitle>{title}</PageTitle>
      </PageHeader>
      <PageBody>{children}</PageBody>
    </AppShell>
  )
}
