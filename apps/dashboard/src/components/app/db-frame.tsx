import type { BranchOut } from "@lumbung/client"
import { Link, getRouteApi, useNavigate } from "@tanstack/react-router"
import {
  CameraIcon,
  CodeIcon,
  GitBranchIcon,
  LayoutGridIcon,
  type LucideIcon,
  NetworkIcon,
  SettingsIcon,
  TableIcon,
  TerminalIcon,
} from "lucide-react"
import { type ReactNode, useEffect, useState } from "react"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tabs, TabsList, TabsPanel, TabsTab } from "@/components/ui/tabs"
import { PageBody, PageHeader } from "./shell"

const dbRoute = getRouteApi("/_app/db/$db")

export type DbSection = "overview" | "data" | "sql" | "schema" | "branches" | "snapshots" | "settings"

const SECTIONS: { value: DbSection; label: string; icon: LucideIcon; to: string }[] = [
  { value: "overview", label: "Overview", icon: LayoutGridIcon, to: "/db/$db" },
  { value: "data", label: "Data", icon: TableIcon, to: "/db/$db/data" },
  { value: "sql", label: "SQL", icon: TerminalIcon, to: "/db/$db/sql" },
  { value: "schema", label: "Schema", icon: CodeIcon, to: "/db/$db/schema" },
  { value: "branches", label: "Branches", icon: NetworkIcon, to: "/db/$db/branches" },
  { value: "snapshots", label: "Snapshots", icon: CameraIcon, to: "/db/$db/snapshots" },
  { value: "settings", label: "Settings", icon: SettingsIcon, to: "/db/$db/settings" },
]

// The branch comes from ?branch=, else the database's default branch.
export function useDb() {
  const { database, branches } = dbRoute.useLoaderData()
  const search = dbRoute.useSearch()
  const branch = search.branch && branches.some((b) => b.name === search.branch) ? search.branch : database.default_branch
  return { database, branches, branch, branchParam: branch === database.default_branch ? undefined : branch }
}

export function DbFrame({ section, actions, wide, children }: { section: DbSection; actions?: ReactNode; wide?: boolean; children: ReactNode }) {
  const { database, branches, branch, branchParam } = useDb()
  return (
    <>
      <PageHeader actions={actions}>
        <nav aria-label="Breadcrumb" className="min-w-0">
          <ol className="flex min-w-0 items-center gap-1.5 text-[13px]">
            <li className="hidden sm:block">
              <Link to="/" className="text-muted-foreground transition-colors duration-100 hover:text-foreground">
                Databases
              </Link>
            </li>
            <li aria-hidden="true" className="hidden text-faint sm:block">
              /
            </li>
            <li className="min-w-0">
              <h1 aria-current="page" className="truncate font-[590]">
                {database.name}
              </h1>
            </li>
          </ol>
        </nav>
        <BranchSwitcher branches={branches} value={branch} defaultBranch={database.default_branch} />
      </PageHeader>
      <Tabs value={section}>
        <div className="overflow-x-auto [scrollbar-width:none]">
          <TabsList aria-label="Database pages" className="w-full min-w-max px-4 md:px-6">
            {SECTIONS.map((s) => (
              <TabsTab
                key={s.value}
                value={s.value}
                icon={<s.icon />}
                render={<Link to={s.to} params={{ db: database.name }} search={{ branch: branchParam }} />}
              >
                {s.label}
              </TabsTab>
            ))}
          </TabsList>
        </div>
        <TabsPanel value={section}>
          <PageBody wide={wide}>{children}</PageBody>
        </TabsPanel>
      </Tabs>
    </>
  )
}

function BranchSwitcher({ branches, value, defaultBranch }: { branches: BranchOut[]; value: string; defaultBranch: string }) {
  const navigate = useNavigate()
  const [selected, setSelected] = useState(value)
  useEffect(() => setSelected(value), [value])
  return (
    <Select
      value={selected}
      items={branches.map((b) => b.name)}
      onValueChange={(next) => {
        if (typeof next !== "string") return
        setSelected(next)
        // Stay on the current page; only the branch in the URL changes.
        void navigate({ to: ".", search: ((prev: Record<string, unknown>) => ({ ...prev, branch: next === defaultBranch ? undefined : next })) as never })
      }}
    >
      <SelectTrigger aria-label="Branch" className="max-w-56 border-transparent font-mono text-[13px] hover:border-input">
        <GitBranchIcon aria-hidden="true" className="text-faint" />
        <SelectValue className="truncate" />
      </SelectTrigger>
      <SelectContent className="font-mono">
        {branches.map((b) => (
          <SelectItem key={b.id} value={b.name}>
            <span className="truncate">{b.name}</span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
