import type { BranchOut } from "@lumbung/client"
import { Link, getRouteApi, useNavigate } from "@tanstack/react-router"
import {
  CameraIcon,
  CodeIcon,
  GitBranchIcon,
  LayoutGridIcon,
  type LucideIcon,
  NetworkIcon,
  PlugIcon,
  SettingsIcon,
  TableIcon,
  TerminalIcon,
} from "lucide-react"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { buttonVariants } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tabs, TabsList, TabsPanel, TabsTab } from "@/components/ui/tabs"
import { TruncatedTip } from "@/components/ui/tooltip"
import { ScrollArea } from "@/components/ui/layout"
import { CONTAINER, PageBody, PageHeader } from "./shell"
import { useWs } from "./workspace"

const dbRoute = getRouteApi("/_app/w/$ws/db/$db")

export type DbSection = "overview" | "data" | "sql" | "schema" | "branches" | "snapshots" | "connect" | "settings"

const SECTIONS: { value: DbSection; label: string; icon: LucideIcon; to: string }[] = [
  { value: "overview", label: "Overview", icon: LayoutGridIcon, to: "/w/$ws/db/$db" },
  { value: "data", label: "Data", icon: TableIcon, to: "/w/$ws/db/$db/data" },
  { value: "sql", label: "SQL", icon: TerminalIcon, to: "/w/$ws/db/$db/sql" },
  { value: "schema", label: "Schema", icon: CodeIcon, to: "/w/$ws/db/$db/schema" },
  { value: "branches", label: "Branches", icon: NetworkIcon, to: "/w/$ws/db/$db/branches" },
  { value: "snapshots", label: "Snapshots", icon: CameraIcon, to: "/w/$ws/db/$db/snapshots" },
  { value: "connect", label: "Connect", icon: PlugIcon, to: "/w/$ws/db/$db/connect" },
  { value: "settings", label: "Settings", icon: SettingsIcon, to: "/w/$ws/db/$db/settings" },
]

// The branch comes from ?branch=, else the database's default branch.
export function useDb() {
  const { database, branches } = dbRoute.useLoaderData()
  const search = dbRoute.useSearch()
  const branch = search.branch && branches.some((b) => b.name === search.branch) ? search.branch : database.default_branch
  return { database, branches, branch, branchParam: branch === database.default_branch ? undefined : branch }
}

export function DbFrame({ section, actions, children }: { section: DbSection; actions?: ReactNode; children: ReactNode }) {
  const ws = useWs()
  const { database, branches, branch, branchParam } = useDb()
  const tabRow = useRef<HTMLDivElement>(null)
  useEffect(() => {
    // Scroll only the tab row; scrollIntoView can move the window too.
    const row = tabRow.current?.querySelector<HTMLElement>('[data-slot="scroll-area-viewport"]')
    const tab = row?.querySelector<HTMLElement>("[data-selected]")
    if (!row || !tab) return
    const r = row.getBoundingClientRect()
    const t = tab.getBoundingClientRect()
    if (t.left < r.left) row.scrollLeft += t.left - r.left - 16
    else if (t.right > r.right) row.scrollLeft += t.right - r.right + 16
  }, [section])
  return (
    <>
      <PageHeader actions={actions}>
        <nav aria-label="Breadcrumb" className="min-w-0">
          <ol className="flex min-w-0 items-center gap-1.5 text-[13px]">
            <li className="hidden sm:block">
              <Link to="/w/$ws" params={{ ws }} className="text-muted-foreground transition-colors duration-100 hover:text-foreground">
                Databases
              </Link>
            </li>
            <li aria-hidden="true" className="hidden text-faint sm:block">
              /
            </li>
            <li className="min-w-0">
              <TruncatedTip text={database.name} side="bottom">
                <h1 aria-current="page" className="truncate font-[590]">
                  {database.name}
                </h1>
              </TruncatedTip>
            </li>
          </ol>
        </nav>
        <BranchSwitcher branches={branches} value={branch} defaultBranch={database.default_branch} />
      </PageHeader>
      <Tabs value={section}>
        <div ref={tabRow} className="shadow-[inset_0_-1px_0_var(--border)]">
          <div className={CONTAINER}>
            {/* No drawn scrollbar: on touch it stays up after a tap and reads
                as a second tab indicator on the hairline. */}
            <ScrollArea axis="x" fade bare>
              <TabsList aria-label="Database pages" className="w-full min-w-max shadow-none">
                {SECTIONS.map((s) => (
                  <TabsTab
                    key={s.value}
                    value={s.value}
                    icon={<s.icon />}
                    render={<Link to={s.to} params={{ ws, db: database.name }} search={{ branch: branchParam }} />}
                  >
                    {s.label}
                  </TabsTab>
                ))}
              </TabsList>
            </ScrollArea>
          </div>
        </div>
        <TabsPanel value={section}>
          <PageBody>{children}</PageBody>
        </TabsPanel>
      </Tabs>
    </>
  )
}

export function OpenSql() {
  const ws = useWs()
  const { database, branchParam } = useDb()
  return (
    <Link to="/w/$ws/db/$db/sql" params={{ ws, db: database.name }} search={{ branch: branchParam }} className={buttonVariants({ variant: "secondary" })}>
      <TerminalIcon aria-hidden="true" />
      Open SQL
    </Link>
  )
}

function BranchSwitcher({ branches, value, defaultBranch }: { branches: BranchOut[]; value: string; defaultBranch: string }) {
  const navigate = useNavigate()
  return (
    <BranchPicker
      branches={branches}
      value={value}
      // Stay on the current page; only the branch in the URL changes.
      onChange={(next) => void navigate({ to: ".", search: ((prev: Record<string, unknown>) => ({ ...prev, branch: next === defaultBranch ? undefined : next })) as never })}
    />
  )
}

export function BranchPicker({ branches, value, onChange }: { branches: BranchOut[]; value: string; onChange: (branch: string) => void }) {
  const [selected, setSelected] = useState(value)
  useEffect(() => setSelected(value), [value])
  return (
    <Select
      value={selected}
      items={branches.map((b) => b.name)}
      onValueChange={(next) => {
        if (typeof next !== "string") return
        setSelected(next)
        onChange(next)
      }}
    >
      <SelectTrigger aria-label="Branch" className="max-w-56 border-transparent font-mono text-[13px] hover:border-input">
        <GitBranchIcon aria-hidden="true" className="text-faint" />
        <SelectValue className="truncate" />
      </SelectTrigger>
      <SelectContent className="font-mono">
        {branches.map((b) => (
          <SelectItem key={b.id} value={b.name}>
            <span className="min-w-0 [overflow-wrap:anywhere]">{b.name}</span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
