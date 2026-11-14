import type { TokenOut } from "@lumbung/client"
import { KeyRoundIcon, PlusIcon, ShieldIcon, Trash2Icon } from "lucide-react"
import { useEffect, useState } from "react"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NumberInput } from "@/components/ui/number-input"
import { Select, SelectContent, SelectItem, SelectTrigger } from "@/components/ui/select"
import { Badge, Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { formatDate, timeAgo } from "@/lib/format"
import { Confirm, FormDialog, ShownOnce } from "./form-dialog"

export type Access = TokenOut["access"]

export const ACCESS_LABEL: Record<Access, string> = { read: "Read", write: "Read and write", admin: "Admin" }

export type TokenNames = { databases: Record<string, string>; branches: Record<string, string>; buckets?: Record<string, string> }

export function TokenTable({ tokens, names, onRevoke }: { tokens: TokenOut[]; names: TokenNames; onRevoke: (t: TokenOut) => Promise<unknown> }) {
  if (tokens.length === 0) return <Empty icon={<KeyRoundIcon />} title="No tokens yet" />
  return (
    <Table>
      <thead>
        <tr>
          <TableHead>Name</TableHead>
          <TableHead>Reaches</TableHead>
          <TableHead className="hidden sm:table-cell">Access</TableHead>
          <TableHead className="hidden text-right md:table-cell">Expires</TableHead>
          <TableHead className="hidden text-right lg:table-cell">Last used</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </tr>
      </thead>
      <tbody>
        {tokens.map((t) => {
          const db = t.database_id ? (names.databases[t.database_id] ?? t.database_id) : ""
          const branch = t.branch_id ? (names.branches[t.branch_id] ?? t.branch_id) : ""
          const bucket = t.bucket_id ? (names.buckets?.[t.bucket_id] ?? t.bucket_id) : ""
          const reach = t.scope === "instance" ? "Everything" : t.scope === "bucket" ? `Bucket ${bucket}` : branch ? `${db} / ${branch}` : db
          return (
            <TableRow key={t.id}>
              <TableCell className="w-2/5 max-w-0">
                <div className="flex min-w-0 items-center gap-2">
                  <span className="truncate font-[510]" title={t.name}>
                    {t.name}
                  </span>
                  {t.service && <Badge>service</Badge>}
                </div>
              </TableCell>
              <TableCell className="w-1/4 max-w-0">
                <span className={t.scope === "instance" ? "block truncate text-muted-foreground" : "block truncate font-mono text-muted-foreground"} title={reach}>
                  {reach}
                </span>
              </TableCell>
              <TableCell className="hidden whitespace-nowrap text-muted-foreground sm:table-cell">
                <span className="inline-flex items-center gap-1">
                  {t.access === "admin" && <ShieldIcon aria-hidden="true" className="size-3.5 text-muted-foreground" />}
                  {ACCESS_LABEL[t.access]}
                </span>
              </TableCell>
              <TableCell className="hidden text-right whitespace-nowrap text-muted-foreground md:table-cell">{t.expires_at ? formatDate(t.expires_at) : "Never"}</TableCell>
              <TableCell className="hidden text-right whitespace-nowrap text-muted-foreground lg:table-cell">
                <span suppressHydrationWarning>{t.last_used_at ? timeAgo(t.last_used_at) : "Never"}</span>
              </TableCell>
              <TableCell className="w-px text-right">
                {!t.service && (
                  <Confirm
                    trigger={<Trash2Icon aria-hidden="true" />}
                    triggerStyle={{ variant: "ghost", size: "icon-sm", label: `Revoke ${t.name}` }}
                    title={`Revoke ${t.name}?`}
                    description="Apps using it stop working right away."
                    actionLabel="Revoke token"
                    agentConfirm={`Revokes the token ${t.name}`}
                    onConfirm={() => onRevoke(t)}
                  />
                )}
              </TableCell>
            </TableRow>
          )
        })}
      </tbody>
    </Table>
  )
}

export type NewToken = { name: string; access: Access; database?: string; branch?: string; expiresInDays?: number }

const ALL = "*"

type CreateTokenProps = {
  databases: string[]
  loadBranches: (db: string) => Promise<string[]>
  onCreate: (t: NewToken) => Promise<{ token: string }>
}

export function CreateTokenDialog({ databases, loadBranches, onCreate }: CreateTokenProps) {
  const [name, setName] = useState("")
  const [access, setAccess] = useState<Access>("read")
  const [database, setDatabase] = useState(ALL)
  const [branch, setBranch] = useState(ALL)
  const [branches, setBranches] = useState<string[]>([])
  const [days, setDays] = useState<number | null>(null)

  useEffect(() => {
    setBranch(ALL)
    setBranches([])
    if (database === ALL) return
    let live = true
    loadBranches(database)
      .then((list) => live && setBranches(list))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [database, loadBranches])

  return (
    <FormDialog
      trigger={
        <>
          <PlusIcon aria-hidden="true" />
          New token
        </>
      }
      title="Create token"
      submit="Create token"
      pendingSubmit="Creating…"
      onOpen={() => {
        setName("")
        setAccess("read")
        setDatabase(ALL)
        setDays(null)
      }}
      onSubmit={async () => {
        const { token } = await onCreate({
          name: name.trim(),
          access,
          database: database === ALL ? undefined : database,
          branch: database === ALL || branch === ALL ? undefined : branch,
          expiresInDays: days ?? undefined,
        })
        return <ShownOnce title={`${name.trim()} is ready`} value={token} copyLabel="Copy token" note="Copy it now. It won't be shown again." />
      }}
    >
      <Field>
        <FieldLabel>Token name</FieldLabel>
        <Input value={name} onValueChange={setName} required maxLength={100} placeholder="ci-deploy" autoComplete="off" />
      </Field>
      <Field>
        <FieldLabel>Reaches</FieldLabel>
        <Select
          items={[{ value: ALL, label: "Every database" }, ...databases.map((d) => ({ value: d, label: d }))]}
          value={database}
          onValueChange={(v) => typeof v === "string" && setDatabase(v)}
        >
          <SelectTrigger className="w-full" />
          <SelectContent>
            <SelectItem value={ALL}>Every database</SelectItem>
            {databases.map((d) => (
              <SelectItem key={d} value={d} className="font-mono">
                <span className="truncate">{d}</span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      {database !== ALL && (
        <Field>
          <FieldLabel>Branch</FieldLabel>
          <Select
            items={[{ value: ALL, label: "Every branch" }, ...branches.map((b) => ({ value: b, label: b }))]}
            value={branch}
            onValueChange={(v) => typeof v === "string" && setBranch(v)}
          >
            <SelectTrigger className="w-full" />
            <SelectContent>
              <SelectItem value={ALL}>Every branch</SelectItem>
              {branches.map((b) => (
                <SelectItem key={b} value={b} className="font-mono">
                  <span className="truncate">{b}</span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      )}
      <div className="grid grid-cols-2 gap-3">
        <Field>
          <FieldLabel>Access</FieldLabel>
          <Select
            items={(["read", "write", "admin"] as const).map((a) => ({ value: a, label: ACCESS_LABEL[a] }))}
            value={access}
            onValueChange={(v) => (v === "read" || v === "write" || v === "admin") && setAccess(v)}
          >
            <SelectTrigger className="w-full" />
            <SelectContent>
              {(["read", "write", "admin"] as const).map((a) => (
                <SelectItem key={a} value={a}>
                  {ACCESS_LABEL[a]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel>Expires in (days)</FieldLabel>
          <NumberInput value={days} onValueChange={setDays} min={1} max={3650} placeholder="Never" />
        </Field>
      </div>
    </FormDialog>
  )
}
