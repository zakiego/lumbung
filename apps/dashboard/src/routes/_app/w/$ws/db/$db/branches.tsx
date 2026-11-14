import type { BranchOut, Diff } from "@lumbung/client"
import { Await, createFileRoute, useNavigate, useRouter } from "@tanstack/react-router"
import { CircleAlertIcon, GitCompareIcon } from "lucide-react"
import type { ReactNode } from "react"
import { z } from "zod"
import {
  BranchTable,
  DeleteBranch,
  DiffView,
  ExpiryDialog,
  NewBranchDialog,
  PromoteBranch,
  ProtectSwitch,
  RenameBranchDialog,
  ResetBranch,
  parentName,
} from "@/components/app/branches"
import { DbFrame, useDb } from "@/components/app/db-frame"
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/components/ui/select"
import { Empty, Skeleton, StatusText } from "@/components/ui/surface"
import { toast, withToast } from "@/components/ui/toast"
import { createBranch, deleteBranch, diffBranch, promoteBranch, resetBranch, updateBranch } from "@/fns/databases"
import { pageTitle } from "@/lib/paths"
import { can } from "@/lib/roles"
import { useWs } from "@/components/app/workspace"

type Settled<T> = { ok: true; value: T } | { ok: false; error: string }

function settle<T>(p: Promise<T>): Promise<Settled<T>> {
  return p.then(
    (value) => ({ ok: true as const, value }),
    (e: unknown) => ({ ok: false as const, error: e instanceof Error ? e.message : String(e) }),
  )
}

// What a branch is compared with: the chosen branch, else its parent, else the default branch.
function diffTarget(branches: BranchOut[], branch: string, against: string | undefined): string | undefined {
  const others = branches.filter((b) => b.name !== branch)
  if (against && others.some((b) => b.name === against)) return against
  const self = branches.find((b) => b.name === branch)
  const parent = self && parentName(self, branches)
  if (parent && parent !== branch) return parent
  return others.find((b) => b.default)?.name ?? others[0]?.name
}

export const Route = createFileRoute("/_app/w/$ws/db/$db/branches")({
  validateSearch: z.object({ against: z.string().min(1).max(100).optional() }),
  loaderDeps: ({ search }) => ({ branch: search.branch, against: search.against }),
  loader: async ({ params, deps, parentMatchPromise }) => {
    const parent = await parentMatchPromise
    const branches = parent.loaderData?.branches ?? []
    const fallback = parent.loaderData?.database.default_branch ?? "main"
    const branch = deps.branch && branches.some((b) => b.name === deps.branch) ? deps.branch : fallback
    const against = diffTarget(branches, branch, deps.against)
    return { against, diff: against ? settle(diffBranch({ data: { ws: params.ws, db: params.db, branch, against } })) : null }
  },
  head: ({ params }) => ({ meta: [{ title: pageTitle("Branches", params.db) }] }),
  component: BranchesPage,
})

function BranchesPage() {
  const ws = useWs()
  const { database, branches, branch } = useDb()
  const { against, diff } = Route.useLoaderData()
  const { viewer } = Route.useRouteContext()
  const router = useRouter()
  const navigate = useNavigate()
  const canWrite = can(viewer.role, "write")
  const current = branches.find((b) => b.name === branch)
  const parent = current && parentName(current, branches)
  const db = database.name

  async function go(next: string | undefined) {
    await router.invalidate()
    await navigate({ to: "/w/$ws/db/$db/branches", params: { ws, db }, search: { branch: next && next !== database.default_branch ? next : undefined } })
  }

  async function create(b: { name: string; from: string; expiresInDays?: number }) {
    await createBranch({ data: { ws, db, ...b } })
    toast.success(`Created ${b.name}`)
    await go(b.name)
  }

  async function update(patch: { name?: string; protected?: boolean; expiresInDays?: number }, success: string) {
    await withToast(updateBranch({ data: { ws, db, branch, ...patch } }), { loading: "Saving…", success, error: "Couldn't save it" })
    await go(patch.name ?? branch)
  }

  return (
    <DbFrame section="branches" actions={canWrite && <NewBranchDialog branches={branches} from={branch} onCreate={create} />}>
      <div className="flex flex-col gap-10">
        <BranchTable db={db} branches={branches} current={branch} />

        {current && (
          <section aria-labelledby="branch-title" className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 id="branch-title" className="min-w-0 font-mono text-[15px] font-[590] [overflow-wrap:anywhere]">
                {branch}
              </h2>
              {canWrite && (
                <ProtectSwitch
                  branch={current}
                  disabled={current.default}
                  onChange={(next) => update({ protected: next }, next ? `Protected ${branch}` : `Unprotected ${branch}`)}
                />
              )}
            </div>
            {canWrite && (
              <div className="flex flex-wrap gap-2">
                <RenameBranchDialog branch={branch} onRename={(name) => update({ name }, `Renamed to ${name}`).then(() => undefined)} />
                <ExpiryDialog
                  branch={branch}
                  expiresAt={current.expires_at}
                  onSave={(days) => update({ expiresInDays: days }, days ? `Deleted in ${days === 1 ? "1 day" : `${days} days`}` : "Expiry cleared")}
                />
                {!current.default && (
                  <PromoteBranch
                    branch={branch}
                    onPromote={async () => {
                      await promoteBranch({ data: { ws, db, branch } })
                      toast.success(`${branch} is the default now`)
                      await go(branch)
                    }}
                  />
                )}
                {parent && !current.protected && (
                  <ResetBranch
                    branch={branch}
                    parent={parent}
                    onReset={async () => {
                      await resetBranch({ data: { ws, db, branch } })
                      toast.success(`Reset ${branch}`)
                      await go(branch)
                    }}
                  />
                )}
                {!current.default && !current.protected && (
                  <div className="sm:ml-auto">
                    <DeleteBranch
                      branch={branch}
                      onDelete={async () => {
                        await deleteBranch({ data: { ws, db, branch } })
                        toast.success(`Deleted ${branch}`)
                        await go(undefined)
                      }}
                    />
                  </div>
                )}
              </div>
            )}
          </section>
        )}

        <section aria-labelledby="diff-title" className="flex flex-col gap-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <h2 id="diff-title" className="text-[13px] font-[590]">
              Schema diff
            </h2>
            {against && (
              <Select
                items={branches.filter((b) => b.name !== branch).map((b) => b.name)}
                value={against}
                onValueChange={(next) => {
                  if (typeof next !== "string") return
                  void navigate({ to: ".", search: ((prev: Record<string, unknown>) => ({ ...prev, against: next })) as never })
                }}
              >
                <SelectTrigger aria-label="Compare with" className="max-w-64 font-mono">
                  <GitCompareIcon aria-hidden="true" className="text-faint" />
                  <span className="truncate">{against}</span>
                </SelectTrigger>
                <SelectContent className="font-mono">
                  {branches
                    .filter((b) => b.name !== branch)
                    .map((b) => (
                      <SelectItem key={b.id} value={b.name}>
                        <span className="truncate">{b.name}</span>
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            )}
          </div>
          {diff ? (
            <Await promise={diff} fallback={<Skeleton className="h-32" />}>
              {(r): ReactNode => (r.ok ? <DiffView diff={r.value as Diff} /> : <Failed message={r.error} />)}
            </Await>
          ) : (
            <Empty icon={<GitCompareIcon />} title="Nothing to compare" description="Create a branch to compare it with this one." />
          )}
        </section>
      </div>
    </DbFrame>
  )
}

function Failed({ message }: { message: string }) {
  return (
    <div className="flex min-h-32 items-center justify-center rounded-lg border border-dashed px-4">
      <StatusText tone="destructive" icon={<CircleAlertIcon aria-hidden="true" />} className="[overflow-wrap:anywhere]">
        {message}
      </StatusText>
    </div>
  )
}
