import type { TokenOut } from "@lumbung/client"
import { KeyRoundIcon, PlusIcon, ShieldIcon, Trash2Icon } from "lucide-react"
import { useEffect, useState } from "react"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NumberInput } from "@/components/ui/number-input"
import { type ListData, Select, SelectContent, SelectGroup, SelectItem, SelectTrigger } from "@/components/ui/select"
import { SelectGroupLabel } from "@/components/ui/select-search"
import { Badge, Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { formatDate, timeAgo } from "@/lib/format"
import { Confirm, FormDialog, SecretValue, ShownOnce } from "./form-dialog"

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

export type NewToken = { name: string; access: Access; database?: string; branch?: string; bucket?: string; accessKey?: boolean; expiresInDays?: number }

export type CreatedToken = { token: string; access_key_id?: string; secret_access_key?: string }

const ALL = "*"
const DB = "db:"
const BUCKET = "bucket:"

type CreateTokenProps = {
  databases: string[]
  buckets?: string[]
  loadBranches: (db: string) => Promise<string[]>
  onCreate: (t: NewToken) => Promise<CreatedToken>
}

export function CreateTokenDialog({ databases, buckets = [], loadBranches, onCreate }: CreateTokenProps) {
  const [name, setName] = useState("")
  const [access, setAccess] = useState<Access>("read")
  const [target, setTarget] = useState(ALL)
  const [branch, setBranch] = useState(ALL)
  const [branches, setBranches] = useState<string[]>([])
  const [accessKey, setAccessKey] = useState(false)
  const [days, setDays] = useState<number | null>(null)

  const database = target.startsWith(DB) ? target.slice(DB.length) : ""
  const bucket = target.startsWith(BUCKET) ? target.slice(BUCKET.length) : ""
  // A database token never reaches a bucket, so its access key would sign nothing.
  const canSign = !database

  useEffect(() => {
    setBranch(ALL)
    setBranches([])
    if (!database) return
    let live = true
    loadBranches(database)
      .then((list) => live && setBranches(list))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [database, loadBranches])

  const targets: ListData = [
    { value: ALL, label: "Everything" },
    ...(databases.length > 0 ? [{ type: "group" as const, label: "Databases", items: databases.map((d) => ({ value: DB + d, label: d })) }] : []),
    ...(buckets.length > 0 ? [{ type: "group" as const, label: "Buckets", items: buckets.map((b) => ({ value: BUCKET + b, label: `Bucket ${b}` })) }] : []),
  ]

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
        setTarget(ALL)
        setAccessKey(false)
        setDays(null)
      }}
      onSubmit={async () => {
        const created = await onCreate({
          name: name.trim(),
          access,
          database: database || undefined,
          branch: database && branch !== ALL ? branch : undefined,
          bucket: bucket || undefined,
          accessKey: canSign && accessKey ? true : undefined,
          expiresInDays: days ?? undefined,
        })
        if (!created.secret_access_key) {
          return <ShownOnce title={`${name.trim()} is ready`} value={created.token} copyLabel="Copy token" note="Copy it now. It won't be shown again." />
        }
        return (
          <ShownOnce title={`${name.trim()} is ready`} note="Copy them now. The secrets won't be shown again.">
            <div className="flex flex-col gap-3">
              <Secret label="Token" value={created.token} copyLabel="Copy token" />
              <Secret label="Access key ID" value={created.access_key_id ?? ""} copyLabel="Copy access key ID" />
              <Secret label="Secret access key" value={created.secret_access_key} copyLabel="Copy secret access key" />
            </div>
          </ShownOnce>
        )
      }}
    >
      <Field>
        <FieldLabel>Token name</FieldLabel>
        <Input value={name} onValueChange={setName} required maxLength={100} placeholder="ci-deploy" autoComplete="off" />
      </Field>
      <Field>
        <FieldLabel>Reaches</FieldLabel>
        <Select items={targets} value={target} onValueChange={(v) => typeof v === "string" && setTarget(v)}>
          <SelectTrigger className="w-full" />
          <SelectContent>
            <SelectItem value={ALL}>Everything</SelectItem>
            {databases.length > 0 && (
              <SelectGroup>
                <SelectGroupLabel>Databases</SelectGroupLabel>
                {databases.map((d) => (
                  <SelectItem key={d} value={DB + d} className="font-mono">
                    <span className="truncate">{d}</span>
                  </SelectItem>
                ))}
              </SelectGroup>
            )}
            {buckets.length > 0 && (
              <SelectGroup>
                <SelectGroupLabel>Buckets</SelectGroupLabel>
                {buckets.map((b) => (
                  <SelectItem key={b} value={BUCKET + b} className="font-mono">
                    <span className="truncate">{b}</span>
                  </SelectItem>
                ))}
              </SelectGroup>
            )}
          </SelectContent>
        </Select>
      </Field>
      {database && (
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
      {canSign && (
        <Field className="flex-row items-center gap-2">
          <Checkbox checked={accessKey} onCheckedChange={(next) => setAccessKey(next === true)} />
          <FieldLabel>S3 access key</FieldLabel>
        </Field>
      )}
    </FormDialog>
  )
}

function Secret({ label, value, copyLabel }: { label: string; value: string; copyLabel: string }) {
  return (
    <div role="group" aria-label={label} className="flex flex-col gap-1.5">
      <span aria-hidden="true" className="text-xs text-muted-foreground">
        {label}
      </span>
      <SecretValue value={value} copyLabel={copyLabel} />
    </div>
  )
}
