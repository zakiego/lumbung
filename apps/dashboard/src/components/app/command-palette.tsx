import type { DatabaseOut } from "@lumbung/client"
import {
  DatabaseIcon,
  KeyRoundIcon,
  BoxIcon,
  LayoutGridIcon,
  type LucideIcon,
  MonitorIcon,
  MoonIcon,
  ScrollTextIcon,
  SettingsIcon,
  SlidersHorizontalIcon,
  SunIcon,
  Trash2Icon,
  UserIcon,
  UsersIcon,
} from "lucide-react"
import { Fragment } from "react"
import {
  Command,
  CommandDialogContent,
  CommandEmpty,
  CommandGroup,
  CommandGroupLabel,
  CommandInput,
  CommandItem,
  CommandItemIcon,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import { Dialog, type createDialogController } from "@/components/ui/dialog"
import { can } from "@/lib/roles"
import { type Theme, applyTheme } from "@/lib/theme"
import type { Person, WorkspaceInfo } from "@/lib/workspace"

type Controller = ReturnType<typeof createDialogController<unknown, unknown>>

type Entry = { value: string; label: string; icon: LucideIcon; keywords?: string[]; mono?: boolean; run: () => unknown }

export type Navigate = (to: string, params?: Record<string, string>) => unknown

export function CommandPalette({
  controller,
  viewer,
  workspace,
  workspaces = [],
  databases,
  navigate,
}: {
  controller: Controller
  viewer: Pick<Person, "role" | "owner">
  /** The workspace on screen. Null for someone who has none yet. */
  workspace: Pick<WorkspaceInfo, "id" | "name"> | null
  workspaces?: Pick<WorkspaceInfo, "id" | "name">[]
  databases: DatabaseOut[] | null
  navigate: Navigate
}) {
  const go = (to: string, params?: Record<string, string>) => () => navigate(to, params)
  const theme = (t: Theme) => () => applyTheme(t)

  const ws = workspace?.name ?? ""
  const inside = !!workspace
  const admin = inside && can(viewer.role, "admin")
  const pages: (Entry & { show: boolean })[] = [
    { show: inside, value: "page:overview", label: "Overview", icon: LayoutGridIcon, keywords: ["home", "dashboard"], run: go("/w/$ws", { ws }) },
    { show: admin, value: "page:tokens", label: "Tokens", icon: KeyRoundIcon, keywords: ["api", "keys"], run: go("/w/$ws/tokens", { ws }) },
    { show: admin, value: "page:audit", label: "Audit log", icon: ScrollTextIcon, keywords: ["activity", "history"], run: go("/w/$ws/audit", { ws }) },
    { show: admin, value: "page:trash", label: "Trash", icon: Trash2Icon, keywords: ["deleted", "restore"], run: go("/w/$ws/trash", { ws }) },
    { show: admin, value: "page:workspace", label: "Workspace settings", icon: SlidersHorizontalIcon, keywords: ["limits", "usage"], run: go("/w/$ws/settings", { ws }) },
    { show: viewer.owner, value: "page:members", label: "Members", icon: UsersIcon, keywords: ["invite", "people", "users"], run: go("/members") },
    { show: viewer.owner, value: "page:settings", label: "Instance settings", icon: SettingsIcon, keywords: ["instance", "engine"], run: go("/settings") },
    { show: true, value: "page:account", label: "Account", icon: UserIcon, keywords: ["password", "passkey", "profile"], run: go("/account") },
  ]

  const groups: { label: string; items: Entry[] }[] = [
    {
      label: "Databases",
      items: (databases ?? []).map((d) => ({
        value: `db:${d.name}`,
        label: d.name,
        icon: DatabaseIcon,
        keywords: ["database"],
        mono: true,
        run: go("/w/$ws/db/$db", { ws, db: d.name }),
      })),
    },
    {
      label: "Workspaces",
      items: workspaces
        .filter((w) => w.id !== workspace?.id)
        .map((w) => ({ value: `ws:${w.id}`, label: w.name, icon: BoxIcon, keywords: ["workspace", "switch"], mono: true, run: go("/w/$ws", { ws: w.name }) })),
    },
    { label: "Go to", items: pages.filter((p) => p.show) },
    {
      label: "Theme",
      items: [
        { value: "theme:light", label: "Light theme", icon: SunIcon, run: theme("light") },
        { value: "theme:dark", label: "Dark theme", icon: MoonIcon, run: theme("dark") },
        { value: "theme:system", label: "System theme", icon: MonitorIcon, keywords: ["auto"], run: theme("system") },
      ],
    },
  ].filter((g) => g.items.length > 0)

  const byValue = new Map(groups.flatMap((g) => g.items.map((i) => [i.value, i] as const)))

  return (
    <Dialog controller={controller}>
      <CommandDialogContent title="Search">
        <Command
          autoHighlight
          loop
          onAction={(value) => {
            controller.close()
            return byValue.get(value)?.run()
          }}
        >
          <CommandInput aria-label="Search" placeholder="Search databases, workspaces and pages…" />
          <CommandEmpty>No results</CommandEmpty>
          <CommandList>
            {groups.map((g, i) => (
              <Fragment key={g.label}>
                {i > 0 && <CommandSeparator />}
                <CommandGroup>
                  <CommandGroupLabel>{g.label}</CommandGroupLabel>
                  {g.items.map((item) => (
                    <CommandItem key={item.value} value={item.value} label={item.label} keywords={item.keywords}>
                      <CommandItemIcon>
                        <item.icon />
                      </CommandItemIcon>
                      <span className={item.mono ? "truncate font-mono" : "truncate"}>{item.label}</span>
                    </CommandItem>
                  ))}
                </CommandGroup>
              </Fragment>
            ))}
          </CommandList>
        </Command>
      </CommandDialogContent>
    </Dialog>
  )
}
