import type { DatabaseOut } from "@lumbung/client"
import {
  DatabaseIcon,
  KeyRoundIcon,
  LayoutGridIcon,
  type LucideIcon,
  MonitorIcon,
  MoonIcon,
  ScrollTextIcon,
  SettingsIcon,
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
import type { Viewer } from "@/server/session"

type Controller = ReturnType<typeof createDialogController<unknown, unknown>>

type Entry = { value: string; label: string; icon: LucideIcon; keywords?: string[]; mono?: boolean; run: () => unknown }

export type Navigate = (to: string, params?: Record<string, string>) => unknown

export function CommandPalette({
  controller,
  viewer,
  databases,
  navigate,
}: {
  controller: Controller
  viewer: Pick<Viewer, "role">
  databases: DatabaseOut[] | null
  navigate: Navigate
}) {
  const go = (to: string, params?: Record<string, string>) => () => navigate(to, params)
  const theme = (t: Theme) => () => applyTheme(t)

  const pages: (Entry & { show: boolean })[] = [
    { show: true, value: "page:overview", label: "Overview", icon: LayoutGridIcon, keywords: ["home", "dashboard"], run: go("/") },
    { show: can(viewer.role, "admin"), value: "page:tokens", label: "Tokens", icon: KeyRoundIcon, keywords: ["api", "keys"], run: go("/tokens") },
    { show: can(viewer.role, "admin"), value: "page:members", label: "Members", icon: UsersIcon, keywords: ["invite", "people", "users"], run: go("/members") },
    { show: can(viewer.role, "write"), value: "page:audit", label: "Audit log", icon: ScrollTextIcon, keywords: ["activity", "history"], run: go("/audit") },
    { show: can(viewer.role, "admin"), value: "page:settings", label: "Settings", icon: SettingsIcon, keywords: ["instance"], run: go("/settings") },
    { show: true, value: "page:trash", label: "Trash", icon: Trash2Icon, keywords: ["deleted", "restore"], run: go("/trash") },
    { show: true, value: "page:account", label: "Account", icon: UserIcon, keywords: ["password", "passkey", "profile"], run: go("/account") },
  ]

  const groups: { label: string; items: Entry[] }[] = [
    {
      label: "Databases",
      items: (databases ?? []).map((d) => ({ value: `db:${d.name}`, label: d.name, icon: DatabaseIcon, keywords: ["database"], mono: true, run: go("/db/$db", { db: d.name }) })),
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
          <CommandInput aria-label="Search" placeholder="Search databases and pages…" />
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
