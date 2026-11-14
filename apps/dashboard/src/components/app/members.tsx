import { LinkIcon, MailIcon, ShieldIcon, UserPlusIcon, UserXIcon, XIcon } from "lucide-react"
import { useState } from "react"
import { Avatar } from "@/components/ui/avatar"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/layout"
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/components/ui/select"
import { Badge, Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { formatDate, timeLeft } from "@/lib/format"
import { ROLE_LABEL, WORKSPACE_ROLES, type WorkspaceGrant, type WorkspaceRole, isWorkspaceRole } from "@/lib/roles"
import type { Workspace } from "@/server/access"
import type { Invite, Member, MemberAccess } from "@/server/users"
import { Confirm, FormDialog, ShownOnce } from "./form-dialog"

type Named = Pick<Workspace, "id" | "name">

type Shown = { id: string; label: string; role: WorkspaceRole }

// A role in a deleted workspace is shown, marked: restoring the workspace
// gives it back. A role in one that is gone for good is left out.
function shownGrants(grants: WorkspaceGrant[], workspaces: Named[], deleted: Named[]): Shown[] {
  const live = new Map(workspaces.map((w) => [w.id, w.name]))
  const gone = new Map(deleted.map((w) => [w.id, w.name]))
  return grants.flatMap((g) => {
    if (live.has(g.workspace)) return [{ id: g.workspace, label: live.get(g.workspace)!, role: g.role }]
    if (gone.has(g.workspace)) return [{ id: g.workspace, label: `${gone.get(g.workspace)} (deleted)`, role: g.role }]
    return []
  })
}

/** "Owner", or each workspace with the role in it. */
export function accessSummary(a: { owner: boolean; grants: WorkspaceGrant[] }, workspaces: Named[], deleted: Named[] = []): string {
  if (a.owner) return ROLE_LABEL.owner
  const parts = shownGrants(a.grants, workspaces, deleted).map((g) => `${g.label} · ${ROLE_LABEL[g.role]}`)
  return parts.length ? parts.join(", ") : "No workspace"
}

/** The summary on screen: workspace names in mono, like every other resource name. */
function AccessSummary({
  access,
  workspaces,
  deleted = [],
  className,
}: {
  access: { owner: boolean; grants: WorkspaceGrant[] }
  workspaces: Named[]
  deleted?: Named[]
  className?: string
}) {
  const text = accessSummary(access, workspaces, deleted)
  const grants = access.owner ? [] : shownGrants(access.grants, workspaces, deleted)
  return (
    <span title={text} className={`block truncate text-muted-foreground ${className ?? ""}`}>
        {grants.length === 0
          ? text
          : grants.map((g, i) => (
              <span key={g.id}>
                {i > 0 && ", "}
                <span className="font-mono">{g.label}</span> · {ROLE_LABEL[g.role]}
              </span>
            ))}
    </span>
  )
}

// One person's access while it is being edited: an owner, or a role (or none) in each workspace.
type Draft = { owner: boolean; roles: Record<string, WorkspaceRole | "none"> }

function draftOf(a: { owner: boolean; grants: WorkspaceGrant[] }): Draft {
  return { owner: a.owner, roles: Object.fromEntries(a.grants.map((g) => [g.workspace, g.role])) }
}

function accessOf(d: Draft, workspaces: Named[]): MemberAccess {
  if (d.owner) return { owner: true }
  return { owner: false, grants: workspaces.flatMap((w) => (isWorkspaceRole(d.roles[w.id]) ? [{ workspace: w.id, role: d.roles[w.id] as WorkspaceRole }] : [])) }
}

const KINDS = [
  { value: "owner", label: "Owner, every workspace" },
  { value: "chosen", label: "Chosen workspaces" },
]
const ROLE_ITEMS = [{ value: "none", label: "No access" }, ...WORKSPACE_ROLES.map((r) => ({ value: r, label: ROLE_LABEL[r] }))]

function AccessFields({ workspaces, value, onChange }: { workspaces: Named[]; value: Draft; onChange: (next: Draft) => void }) {
  return (
    <>
      <Field>
        <FieldLabel>Access</FieldLabel>
        <Select items={KINDS} value={value.owner ? "owner" : "chosen"} onValueChange={(v) => onChange({ ...value, owner: v === "owner" })}>
          <SelectTrigger className="w-full" />
          <SelectContent>
            {KINDS.map((k) => (
              <SelectItem key={k.value} value={k.value}>
                {k.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      {!value.owner && (
        <ScrollArea label="Workspaces" className="-my-1 max-h-64" viewportClassName="max-h-64">
          <ul className="flex flex-col">
            {workspaces.map((w) => (
              <li key={w.id} className="flex min-w-0 items-center justify-between gap-3 py-1">
                <span className="min-w-0 font-mono text-[13px] [overflow-wrap:anywhere]">{w.name}</span>
                <Select
                  items={ROLE_ITEMS}
                  value={value.roles[w.id] ?? "none"}
                  onValueChange={(v) => (v === "none" || isWorkspaceRole(v)) && onChange({ ...value, roles: { ...value.roles, [w.id]: v } })}
                >
                  <SelectTrigger aria-label={`Role in ${w.name}`} className="w-36 shrink-0" />
                  <SelectContent>
                    {ROLE_ITEMS.map((r) => (
                      <SelectItem key={r.value} value={r.value}>
                        {r.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </li>
            ))}
          </ul>
        </ScrollArea>
      )}
    </>
  )
}

type MemberActions = {
  onAccess: (m: Member, access: MemberAccess) => Promise<unknown>
  onRemove: (m: Member) => Promise<unknown>
}

type Lists = { workspaces: Named[]; /** Workspaces in the trash, whose roles come back on a restore. */ deleted?: Named[] }

export function MemberTable({ members, me, workspaces, deleted, onAccess, onRemove }: { members: Member[]; me: string } & Lists & MemberActions) {
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Member</TableHead>
          <TableHead className="hidden w-2/5 sm:table-cell">Access</TableHead>
          <TableHead className="hidden text-right md:table-cell">Joined</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </tr>
      </thead>
      <tbody>
        {members.map((m) => (
          <MemberRow key={m.id} member={m} self={m.id === me} workspaces={workspaces} deleted={deleted} onAccess={onAccess} onRemove={onRemove} />
        ))}
      </tbody>
    </Table>
  )
}

function MemberRow({ member: m, self, workspaces, deleted, onAccess, onRemove }: { member: Member; self: boolean } & Lists & MemberActions) {
  const [draft, setDraft] = useState(() => draftOf(m))
  const who = m.name || m.email
  return (
    <TableRow>
      <TableCell className="max-w-0">
        <div className="flex min-w-0 items-center gap-3 py-2">
          <Avatar name={who} />
          <div className="grid min-w-0 leading-tight">
            <span className="flex min-w-0 items-center gap-2">
              <span className="truncate font-[510]" title={who}>
                {who}
              </span>
              {self && <Badge>you</Badge>}
            </span>
            <span className="text-xs text-muted-foreground [overflow-wrap:anywhere] sm:truncate" title={m.email}>
              {m.email}
            </span>
            <AccessSummary access={m} workspaces={workspaces} deleted={deleted} className="text-xs sm:hidden" />
          </div>
        </div>
      </TableCell>
      <TableCell className="hidden max-w-0 w-2/5 sm:table-cell">
        <AccessSummary access={m} workspaces={workspaces} deleted={deleted} />
      </TableCell>
      <TableCell className="hidden w-px text-right whitespace-nowrap text-muted-foreground md:table-cell">{formatDate(m.createdAt)}</TableCell>
      <TableCell className="w-px">
        {!self && (
          <div className="flex justify-end gap-1">
            <FormDialog
              trigger={<ShieldIcon aria-hidden="true" />}
              triggerStyle={{ variant: "ghost", size: "icon-sm", label: `Change access for ${who}` }}
              title={`Access for ${who}`}
              description="Saving signs them out."
              submit="Save"
              pendingSubmit="Saving…"
              pinned
              onOpen={() => setDraft(draftOf(m))}
              onSubmit={async () => {
                await onAccess(m, accessOf(draft, workspaces))
              }}
            >
              <AccessFields workspaces={workspaces} value={draft} onChange={setDraft} />
            </FormDialog>
            <Confirm
              trigger={<UserXIcon aria-hidden="true" />}
              triggerStyle={{ variant: "ghost", size: "icon-sm", label: `Remove ${who}` }}
              title={`Remove ${who}?`}
              description="They're signed out, and tokens they made stop working."
              actionLabel="Remove member"
              agentConfirm={`Removes ${m.email} from the dashboard`}
              onConfirm={() => onRemove(m)}
            />
          </div>
        )}
      </TableCell>
    </TableRow>
  )
}

type InviteActions = {
  onResend: (i: Invite) => Promise<string>
  onCancel: (i: Invite) => Promise<unknown>
  onChanged: () => void
}

export function InviteTable({ invites, workspaces, deleted, onResend, onCancel, onChanged }: { invites: Invite[] } & Lists & InviteActions) {
  if (invites.length === 0) return <Empty icon={<MailIcon />} title="No open invites" />
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Email</TableHead>
          <TableHead className="hidden w-2/5 sm:table-cell">Access</TableHead>
          <TableHead className="hidden text-right md:table-cell">Expires</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </tr>
      </thead>
      <tbody>
        {invites.map((i) => (
          <TableRow key={i.id}>
            <TableCell className="max-w-0">
              <span className="block truncate" title={i.email}>
                {i.email}
              </span>
            </TableCell>
            <TableCell className="hidden max-w-0 w-2/5 sm:table-cell">
              <AccessSummary access={i} workspaces={workspaces} deleted={deleted} />
            </TableCell>
            <TableCell className="hidden text-right whitespace-nowrap text-muted-foreground tabular-nums md:table-cell">
              <span title={formatDate(i.expiresAt)} suppressHydrationWarning>
                {timeLeft(i.expiresAt)}
              </span>
            </TableCell>
            <TableCell className="w-px">
              <div className="flex justify-end gap-1">
                <FormDialog
                  trigger={<LinkIcon aria-hidden="true" />}
                  triggerStyle={{ variant: "ghost", size: "icon-sm", label: `New link for ${i.email}` }}
                  title={`New link for ${i.email}`}
                  description="The old link stops working."
                  submit="Make new link"
                  pendingSubmit="Making…"
                  onSubmit={async () => <InviteLink email={i.email} url={await onResend(i)} />}
                  onDone={onChanged}
                >
                  {null}
                </FormDialog>
                <Confirm
                  trigger={<XIcon aria-hidden="true" />}
                  triggerStyle={{ variant: "ghost", size: "icon-sm", label: `Cancel invite for ${i.email}` }}
                  title={`Cancel the invite for ${i.email}?`}
                  actionLabel="Cancel invite"
                  agentConfirm={`Cancels the invite for ${i.email}`}
                  onConfirm={() => onCancel(i)}
                />
              </div>
            </TableCell>
          </TableRow>
        ))}
      </tbody>
    </Table>
  )
}

function InviteLink({ email, url }: { email: string; url: string }) {
  return <ShownOnce title={`Invite for ${email}`} value={url} copyLabel="Copy invite link" note="Send it yourself. It's shown once and lasts 7 days." />
}

export function InviteDialog({
  workspaces,
  current,
  onInvite,
  onDone,
}: {
  workspaces: Named[]
  /** The workspace to offer first: the one the owner is looking at. */
  current?: string
  onInvite: (email: string, access: MemberAccess) => Promise<string>
  onDone?: () => void
}) {
  const fresh = (): Draft => ({ owner: false, roles: current ? { [current]: "member" } : {} })
  const [email, setEmail] = useState("")
  const [draft, setDraft] = useState<Draft>(fresh)
  return (
    <FormDialog
      trigger={
        <>
          <UserPlusIcon aria-hidden="true" />
          Invite
        </>
      }
      title="Invite someone"
      submit="Create invite"
      pendingSubmit="Creating…"
      pinned
      onOpen={() => {
        setEmail("")
        setDraft(fresh())
      }}
      onSubmit={async () => <InviteLink email={email.trim()} url={await onInvite(email.trim(), accessOf(draft, workspaces))} />}
      onDone={onDone}
    >
      <Field>
        <FieldLabel>Email</FieldLabel>
        <Input type="email" value={email} onValueChange={setEmail} required maxLength={200} placeholder="sam@example.com" autoComplete="off" />
      </Field>
      <AccessFields workspaces={workspaces} value={draft} onChange={setDraft} />
    </FormDialog>
  )
}
