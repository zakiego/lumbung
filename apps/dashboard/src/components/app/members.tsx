import { LinkIcon, MailIcon, UserPlusIcon, UserXIcon, XIcon } from "lucide-react"
import { useEffect, useState } from "react"
import { Avatar } from "@/components/ui/avatar"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/components/ui/select"
import { Badge, Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { formatDate, timeLeft } from "@/lib/format"
import { ROLES, ROLE_LABEL, type Role } from "@/lib/roles"
import type { Invite, Member } from "@/server/users"
import { Confirm, FormDialog, ShownOnce } from "./form-dialog"

const roleItems = ROLES.map((r) => ({ value: r, label: ROLE_LABEL[r] }))

function RoleSelect({ value, label, onChange, className }: { value: Role; label?: string; onChange: (r: Role) => void; className?: string }) {
  return (
    <Select items={roleItems} value={value} onValueChange={(v) => typeof v === "string" && (ROLES as readonly string[]).includes(v) && onChange(v as Role)}>
      <SelectTrigger aria-label={label} className={className} />
      <SelectContent>
        {ROLES.map((r) => (
          <SelectItem key={r} value={r}>
            {ROLE_LABEL[r]}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

type MemberActions = {
  onRole: (m: Member, role: Role) => Promise<unknown>
  onRemove: (m: Member) => Promise<unknown>
}

export function MemberTable({ members, me, onRole, onRemove }: { members: Member[]; me: string } & MemberActions) {
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Member</TableHead>
          <TableHead className="w-36">Role</TableHead>
          <TableHead className="hidden text-right md:table-cell">Joined</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </tr>
      </thead>
      <tbody>
        {members.map((m) => (
          <MemberRow key={m.id} member={m} self={m.id === me} onRole={onRole} onRemove={onRemove} />
        ))}
      </tbody>
    </Table>
  )
}

function MemberRow({ member: m, self, onRole, onRemove }: { member: Member; self: boolean } & MemberActions) {
  const [role, setRole] = useState(m.role)
  useEffect(() => setRole(m.role), [m.role])
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
            <span className="truncate text-xs text-muted-foreground" title={m.email}>
              {m.email}
            </span>
          </div>
        </div>
      </TableCell>
      <TableCell className="w-36">
        {self ? (
          <span className="pl-2.5 text-muted-foreground">{ROLE_LABEL[m.role]}</span>
        ) : (
          <RoleSelect
            value={role}
            label={`Role for ${who}`}
            className="w-full border-transparent hover:border-input"
            onChange={(next) => {
              setRole(next)
              onRole(m, next).catch(() => setRole(m.role))
            }}
          />
        )}
      </TableCell>
      <TableCell className="hidden text-right whitespace-nowrap text-muted-foreground md:table-cell">{formatDate(m.createdAt)}</TableCell>
      <TableCell className="w-px text-right">
        {!self && (
          <Confirm
            trigger={<UserXIcon aria-hidden="true" />}
            triggerStyle={{ variant: "ghost", size: "icon-sm", label: `Remove ${who}` }}
            title={`Remove ${who}?`}
            description="They're signed out and lose access."
            actionLabel="Remove member"
            agentConfirm={`Removes ${m.email} from the dashboard`}
            onConfirm={() => onRemove(m)}
          />
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

export function InviteTable({ invites, onResend, onCancel, onChanged }: { invites: Invite[] } & InviteActions) {
  if (invites.length === 0) return <Empty icon={<MailIcon />} title="No open invites" />
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Email</TableHead>
          <TableHead className="hidden sm:table-cell">Role</TableHead>
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
            <TableCell className="hidden whitespace-nowrap text-muted-foreground sm:table-cell">{ROLE_LABEL[i.role]}</TableCell>
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

export function InviteDialog({ onInvite, onDone }: { onInvite: (email: string, role: Role) => Promise<string>; onDone?: () => void }) {
  const [email, setEmail] = useState("")
  const [role, setRole] = useState<Role>("member")
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
      onOpen={() => {
        setEmail("")
        setRole("member")
      }}
      onSubmit={async () => <InviteLink email={email.trim()} url={await onInvite(email.trim(), role)} />}
      onDone={onDone}
    >
      <Field>
        <FieldLabel>Email</FieldLabel>
        <Input type="email" value={email} onValueChange={setEmail} required maxLength={200} placeholder="sam@example.com" autoComplete="off" />
      </Field>
      <Field>
        <FieldLabel>Role</FieldLabel>
        <RoleSelect value={role} onChange={setRole} className="w-full" />
      </Field>
    </FormDialog>
  )
}
