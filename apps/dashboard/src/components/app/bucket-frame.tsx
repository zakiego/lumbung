import { Link, useRouteContext } from "@tanstack/react-router"
import { FilesIcon, type LucideIcon, PlugIcon, SettingsIcon } from "lucide-react"
import type { ReactNode } from "react"
import { ScrollArea } from "@/components/ui/layout"
import { Tabs, TabsList, TabsPanel, TabsTab } from "@/components/ui/tabs"
import { TruncatedTip } from "@/components/ui/tooltip"
import { can } from "@/lib/roles"
import { type ObjectGroup, type ObjectSortKey, type SortDir, prefixTrail } from "./buckets"
import { CONTAINER, PageBody, PageHeader } from "./shell"
import { useWs } from "./workspace"

export type BucketSection = "objects" | "connect" | "settings"

const SECTIONS: { value: BucketSection; label: string; icon: LucideIcon; to: string; admin?: boolean }[] = [
  { value: "objects", label: "Objects", icon: FilesIcon, to: "/w/$ws/buckets/$bucket" },
  { value: "connect", label: "Connect", icon: PlugIcon, to: "/w/$ws/buckets/$bucket/connect" },
  { value: "settings", label: "Settings", icon: SettingsIcon, to: "/w/$ws/buckets/$bucket/settings", admin: true },
]

/** A bucket page: breadcrumb (with the folder trail on Objects), line tabs, then the body. */
export function BucketFrame({
  bucket,
  section,
  prefix = "",
  keep = {},
  actions,
  children,
}: {
  bucket: string
  section: BucketSection
  prefix?: string
  /** The object list's sort, carried through the folder links. */
  keep?: { sort?: ObjectSortKey; dir?: SortDir; group?: ObjectGroup }
  actions?: ReactNode
  children: ReactNode
}) {
  const ws = useWs()
  const { viewer } = useRouteContext({ from: "/_app/w/$ws" })
  const trail = section === "objects" ? prefixTrail(prefix) : []
  const sections = SECTIONS.filter((s) => !s.admin || can(viewer.role, "admin"))
  return (
    <>
      <PageHeader actions={actions}>
        <nav aria-label="Breadcrumb" className="min-w-0">
          <ol className="flex min-w-0 items-center gap-1.5 text-[13px]">
            <li className="hidden sm:block">
              <Link to="/w/$ws/buckets" params={{ ws }} className="text-muted-foreground transition-colors duration-100 hover:text-foreground">
                Buckets
              </Link>
            </li>
            <li aria-hidden="true" className="hidden text-faint sm:block">
              /
            </li>
            <li className="min-w-0">
              <TruncatedTip text={bucket} side="bottom">
                {trail.length === 0 ? (
                  <h1 aria-current="page" className="truncate font-mono font-[590]">
                    {bucket}
                  </h1>
                ) : (
                  <Link to="/w/$ws/buckets/$bucket" params={{ ws, bucket }} search={keep} className="block truncate font-mono text-muted-foreground hover:text-foreground">
                    {bucket}
                  </Link>
                )}
              </TruncatedTip>
            </li>
            {trail.map((t, i) => (
              <li key={t.prefix} className="flex min-w-0 items-center gap-1.5">
                <span aria-hidden="true" className="text-faint">
                  /
                </span>
                <TruncatedTip text={t.label} side="bottom">
                  {i === trail.length - 1 ? (
                    <h1 aria-current="page" className="truncate font-mono font-[590]">
                      {t.label}
                    </h1>
                  ) : (
                    <Link to="/w/$ws/buckets/$bucket" params={{ ws, bucket }} search={{ prefix: t.prefix, ...keep }} className="block truncate font-mono text-muted-foreground hover:text-foreground">
                      {t.label}
                    </Link>
                  )}
                </TruncatedTip>
              </li>
            ))}
          </ol>
        </nav>
      </PageHeader>
      <Tabs value={section}>
        <div className="shadow-[inset_0_-1px_0_var(--border)]">
          <div className={CONTAINER}>
            {/* No drawn scrollbar: on touch it stays up after a tap and reads
                as a second tab indicator on the hairline. */}
            <ScrollArea axis="x" fade bare>
              <TabsList aria-label="Bucket pages" className="w-full min-w-max shadow-none">
                {sections.map((s) => (
                  <TabsTab key={s.value} value={s.value} icon={<s.icon />} render={<Link to={s.to} params={{ ws, bucket }} />}>
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
