import type { TokenOut } from "@lumbung/client"
import { KeyRoundIcon, PlusIcon, ShieldIcon, Trash2Icon, XIcon } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NumberInput } from "@/components/ui/number-input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { type ListData, Select, SelectContent, SelectGroup, SelectItem, SelectTrigger } from "@/components/ui/select"
import { SelectGroupLabel } from "@/components/ui/select-search"
import { Badge, Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { formatDate, timeAgo } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Confirm, FormDialog, SecretValue, ShownOnce } from "./form-dialog"

export type Access = TokenOut["access"]

export const ACCESS_LABEL: Record<Access, string> = { read: "Read", write: "Read and write", admin: "Admin" }

export type TokenNames = { databases: Record<string, string>; branches: Record<string, string>; buckets?: Record<string, string> }

/** What a token with no target reaches: the workspace it is in, or for an instance token every workspace. */
export const WHOLE_WORKSPACE = "Whole workspace"
export const EVERY_WORKSPACE = "Every workspace"

export function TokenTable({
  tokens,
  names,
  everything = WHOLE_WORKSPACE,
  onRevoke,
}: {
  tokens: TokenOut[]
  names: TokenNames
  everything?: string
  onRevoke: (t: TokenOut) => Promise<unknown>
}) {
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
          const reach = reaches(t, names)
          const shown = reach.slice(0, MAX_SHOWN)
          const more = reach.length - shown.length
          return (
            <TableRow key={t.id}>
              <TableCell className="w-1/2 max-w-0 py-3 align-top sm:w-2/5">
                <div className="flex min-w-0 items-center gap-2">
                  <span className="truncate font-[510]" title={t.name}>
                    {t.name}
                  </span>
                  {t.service && <Badge>service</Badge>}
                </div>
              </TableCell>
              <TableCell className="w-1/2 max-w-0 py-3 align-top sm:w-1/4">
                {reach.length === 0 ? (
                  <span className="block truncate text-muted-foreground">{everything}</span>
                ) : (
                  <ul className="flex flex-col gap-1">
                    {shown.map((r) => (
                      <li key={r.key} className="h-5 truncate font-mono leading-5 text-muted-foreground" title={r.label}>
                        {r.label}
                      </li>
                    ))}
                    {more > 0 && (
                      <li className="h-5 leading-5">
                        <Popover>
                          <PopoverTrigger className="relative cursor-default rounded-sm text-muted-foreground underline underline-offset-4 transition-colors duration-100 select-none after:absolute after:-inset-2 hover:text-foreground active:text-foreground data-open:text-foreground">
                            {more} more
                          </PopoverTrigger>
                          <PopoverContent className="max-h-72 w-80 max-w-[calc(100vw-32px)] overflow-y-auto overscroll-contain">
                            <ul className="flex flex-col gap-1.5 text-[13px]">
                              {reach.map((r) => (
                                <li key={r.key} className="flex items-baseline justify-between gap-3">
                                  <span className="min-w-0 font-mono [overflow-wrap:anywhere]">{r.label}</span>
                                  <span className="shrink-0 whitespace-nowrap text-muted-foreground">
                                    <AccessLevel access={r.access} />
                                  </span>
                                </li>
                              ))}
                            </ul>
                          </PopoverContent>
                        </Popover>
                      </li>
                    )}
                  </ul>
                )}
              </TableCell>
              <TableCell className="hidden py-3 align-top whitespace-nowrap text-muted-foreground sm:table-cell">
                {reach.length < 2 ? (
                  <AccessLevel access={t.access} />
                ) : (
                  <ul className="flex flex-col gap-1">
                    {shown.map((r) => (
                      <li key={r.key} className="flex h-5 items-center leading-5">
                        <AccessLevel access={r.access} />
                      </li>
                    ))}
                  </ul>
                )}
              </TableCell>
              <TableCell className="hidden py-3 text-right align-top whitespace-nowrap text-muted-foreground md:table-cell">{t.expires_at ? formatDate(t.expires_at) : "Never"}</TableCell>
              <TableCell className="hidden py-3 text-right align-top whitespace-nowrap text-muted-foreground lg:table-cell">
                <span suppressHydrationWarning>{t.last_used_at ? timeAgo(t.last_used_at) : "Never"}</span>
              </TableCell>
              <TableCell className="w-px py-2 text-right align-top">
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

const MAX_SHOWN = 3

type Reach = { key: string; label: string; access: Access }

// What a token reaches below a whole workspace, one entry per grant. Empty for a workspace or instance token, which reach everything.
function reaches(t: TokenOut, names: TokenNames): Reach[] {
  const grants = t.grants?.length
    ? t.grants
    : t.scope === "instance" || t.scope === "workspace"
      ? []
      : [{ database_id: t.database_id, branch_id: t.branch_id, bucket_id: t.bucket_id, access: t.access }]
  return grants.map((g) => {
    if (g.bucket_id) return { key: g.bucket_id, label: `Bucket ${names.buckets?.[g.bucket_id] ?? g.bucket_id}`, access: g.access }
    const db = names.databases[g.database_id] ?? g.database_id
    const branch = g.branch_id ? (names.branches[g.branch_id] ?? g.branch_id) : ""
    return { key: `${g.database_id}/${g.branch_id}`, label: branch ? `${db} / ${branch}` : db, access: g.access }
  })
}

function AccessLevel({ access }: { access: Access }) {
  return (
    <span className="inline-flex items-center gap-1">
      {access === "admin" && <ShieldIcon aria-hidden="true" className="size-3.5 text-muted-foreground" />}
      {ACCESS_LABEL[access]}
    </span>
  )
}

export type NewGrant = { access: Access; database?: string; branch?: string; bucket?: string }

/** One reach is sent as access, database, branch and bucket; several are sent as grants. */
export type NewToken = {
  name: string
  access?: Access
  database?: string
  branch?: string
  bucket?: string
  grants?: NewGrant[]
  accessKey?: boolean
  expiresInDays?: number
}

export type CreatedToken = { token: string; access_key_id?: string; secret_access_key?: string }

const ALL = "*"
const DB = "db:"
const BUCKET = "bucket:"

const ACCESS = ["read", "write", "admin"] as const
const MAX_GRANTS = 20

type CreateTokenProps = {
  databases: string[]
  buckets?: string[]
  /** The engine's MCP endpoint, shown next to a new token so both can be pasted into an agent. */
  mcpUrl?: string
  loadBranches: (db: string) => Promise<string[]>
  /** The label for a token with no target. */
  everything?: string
  onCreate: (t: NewToken) => Promise<CreatedToken>
}

// `added` marks a row the person added, which is the only kind that animates in.
type Row = { id: number; target: string; branch: string; access: Access; added?: boolean }

const databaseOf = (r: Row) => (r.target.startsWith(DB) ? r.target.slice(DB.length) : "")
const bucketOf = (r: Row) => (r.target.startsWith(BUCKET) ? r.target.slice(BUCKET.length) : "")

function grantOf(r: Row): NewGrant {
  const database = databaseOf(r)
  return { access: r.access, database: database || undefined, branch: database && r.branch !== ALL ? r.branch : undefined, bucket: bucketOf(r) || undefined }
}

export function CreateTokenDialog({ databases, buckets = [], mcpUrl, loadBranches, everything: everythingLabel = WHOLE_WORKSPACE, onCreate }: CreateTokenProps) {
  const [name, setName] = useState("")
  const nextId = useRef(1)
  const [rows, setRows] = useState<Row[]>([{ id: 0, target: ALL, branch: ALL, access: "read" }])
  const [accessKey, setAccessKey] = useState(false)
  const [days, setDays] = useState<number | null>(null)
  const addButton = useRef<HTMLButtonElement>(null)

  const everything = rows[0]?.target === ALL
  // Only a token that reaches a bucket has anything for an access key to sign.
  const canSign = everything || rows.some((r) => bucketOf(r))
  const usedBuckets = new Set(rows.map(bucketOf).filter(Boolean))
  // The next thing to offer: a bucket no row has yet, else a database. With
  // every database taken, offer the first again, for another of its branches.
  const unused = [...buckets.filter((b) => !usedBuckets.has(b)).map((b) => BUCKET + b), ...databases.map((d) => DB + d)].find((t) => !rows.some((r) => r.target === t))
  const nextTarget = unused ?? (databases[0] ? DB + databases[0] : undefined)
  const canAdd = rows.length < MAX_GRANTS && nextTarget !== undefined

  useEffect(() => {
    if (!canSign) setAccessKey(false)
  }, [canSign])

  const update = (id: number, patch: Partial<Row>) => setRows((list) => list.map((r) => (r.id === id ? { ...r, ...patch } : r)))

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
        setRows([{ id: nextId.current++, target: ALL, branch: ALL, access: "read" }])
        setAccessKey(false)
        setDays(null)
      }}
      pinned
      onSubmit={async () => {
        const grants = rows.map(grantOf)
        // One row for every branch already reaches them all, so two rows of a database need a branch each.
        const clash = grants.some((g, i) => g.database && grants.some((o, j) => j > i && o.database === g.database && (!g.branch || !o.branch || o.branch === g.branch)))
        if (clash) throw new Error("Pick a different branch for each row of the same database.")
        const created = await onCreate({
          name: name.trim(),
          ...(grants.length === 1 ? grants[0] : { grants }),
          accessKey: canSign && accessKey ? true : undefined,
          expiresInDays: days ?? undefined,
        })
        if (!created.secret_access_key && !mcpUrl) {
          return <ShownOnce title={`${name.trim()} is ready`} value={created.token} copyLabel="Copy token" note="Copy it now. It won't be shown again." />
        }
        return (
          <ShownOnce title={`${name.trim()} is ready`} note={created.secret_access_key ? "Copy them now. The secrets won't be shown again." : "Copy the token now. It won't be shown again."}>
            <div className="flex flex-col gap-3">
              <Secret label="Token" value={created.token} copyLabel="Copy token" />
              {created.secret_access_key && (
                <>
                  <Secret label="Access key ID" value={created.access_key_id ?? ""} copyLabel="Copy access key ID" />
                  <Secret label="Secret access key" value={created.secret_access_key} copyLabel="Copy secret access key" />
                </>
              )}
              {mcpUrl && <Secret label="MCP URL" value={mcpUrl} copyLabel="Copy MCP URL" secret={false} />}
            </div>
          </ShownOnce>
        )
      }}
    >
      <Field>
        <FieldLabel>Token name</FieldLabel>
        <Input value={name} onValueChange={setName} required maxLength={100} placeholder="ci-deploy" autoComplete="off" />
      </Field>
      <div className="flex flex-col gap-1.5">
        <div aria-hidden="true" className={cn("grid gap-2 text-[13px] font-[510]", rows.length > 1 ? GRID_REMOVABLE : GRID)}>
          <span>Reaches</span>
          <span>Access</span>
        </div>
        <div className="flex flex-col gap-3">
          {rows.map((row, index) => (
            <GrantRow
              key={row.id}
              row={row}
              suffix={index === 0 ? "" : ` ${index + 1}`}
              databases={databases}
              buckets={buckets.filter((b) => b === bucketOf(row) || !usedBuckets.has(b))}
              offerEverything={rows.length === 1}
              everything={everythingLabel}
              removable={rows.length > 1}
              loadBranches={loadBranches}
              onChange={(patch) => update(row.id, patch)}
              onRemove={() => {
                setRows((list) => list.filter((r) => r.id !== row.id))
                // The removed row took focus with it.
                requestAnimationFrame(() => addButton.current?.focus())
              }}
            />
          ))}
        </div>
        {!everything && (
          <Button
            ref={addButton}
            type="button"
            variant="ghost"
            size="sm"
            className="-ml-1.5 self-start"
            disabled={!canAdd}
            focusableWhenDisabled
            onClick={() => {
              if (!nextTarget) return
              const id = nextId.current++
              setRows((list) => [...list, { id, target: nextTarget, branch: ALL, access: "read", added: true }])
            }}
          >
            <PlusIcon aria-hidden="true" />
            Add another
          </Button>
        )}
      </div>
      <Field>
        <FieldLabel>Expires in (days)</FieldLabel>
        <NumberInput value={days} onValueChange={setDays} min={1} max={3650} placeholder="Never" />
      </Field>
      {canSign && (
        <Field className="flex-row items-center gap-2">
          <Checkbox checked={accessKey} onCheckedChange={(next) => setAccessKey(next === true)} />
          <FieldLabel>S3 access key</FieldLabel>
        </Field>
      )}
    </FormDialog>
  )
}

// Reduced motion keeps the fade and drops the movement.
const ROW_ENTER =
  "transition-[opacity,translate] duration-150 ease-out-cubic starting:-translate-y-1 starting:opacity-0 motion-reduce:starting:translate-y-0"

const GRID = "grid-cols-[minmax(0,1fr)_8.5rem]"
const GRID_REMOVABLE = "grid-cols-[minmax(0,1fr)_8.5rem_1.75rem]"

type GrantRowProps = {
  row: Row
  /** Tells rows apart by name: "", " 2", " 3". */
  suffix: string
  databases: string[]
  buckets: string[]
  offerEverything: boolean
  everything: string
  removable: boolean
  loadBranches: (db: string) => Promise<string[]>
  onChange: (patch: Partial<Row>) => void
  onRemove: () => void
}

function GrantRow({ row, suffix, databases, buckets, offerEverything, everything, removable, loadBranches, onChange, onRemove }: GrantRowProps) {
  const database = databaseOf(row)
  const [branches, setBranches] = useState<string[]>([])

  useEffect(() => {
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
    ...(offerEverything ? [{ value: ALL, label: everything }] : []),
    ...(databases.length > 0 ? [{ type: "group" as const, label: "Databases", items: databases.map((d) => ({ value: DB + d, label: d })) }] : []),
    ...(buckets.length > 0 ? [{ type: "group" as const, label: "Buckets", items: buckets.map((b) => ({ value: BUCKET + b, label: `Bucket ${b}` })) }] : []),
  ]

  return (
    <div
      role="group"
      aria-label={`Reach${suffix || " 1"}`}
      className={cn("grid items-start gap-2", removable ? GRID_REMOVABLE : GRID, row.added && ROW_ENTER)}
    >
      <Field>
        <FieldLabel className="sr-only">Reaches{suffix}</FieldLabel>
        <Select items={targets} value={row.target} onValueChange={(v) => typeof v === "string" && onChange({ target: v, branch: ALL })}>
          <SelectTrigger className="w-full" title={database || bucketOf(row) || undefined} />
          <SelectContent>
            {offerEverything && <SelectItem value={ALL}>{everything}</SelectItem>}
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
      <Field>
        <FieldLabel className="sr-only">Access{suffix}</FieldLabel>
        <Select items={ACCESS.map((a) => ({ value: a, label: ACCESS_LABEL[a] }))} value={row.access} onValueChange={(v) => (v === "read" || v === "write" || v === "admin") && onChange({ access: v })}>
          <SelectTrigger className="w-full" />
          <SelectContent>
            {ACCESS.map((a) => (
              <SelectItem key={a} value={a}>
                {ACCESS_LABEL[a]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      {removable && (
        <Button type="button" variant="ghost" size="icon-sm" className="mt-0.5" aria-label={`Remove reach${suffix || " 1"}`} onClick={onRemove}>
          <XIcon aria-hidden="true" />
        </Button>
      )}
      {database && (
        <Field className="col-start-1">
          <FieldLabel className="sr-only">Branch{suffix}</FieldLabel>
          <Select items={[{ value: ALL, label: "Every branch" }, ...branches.map((b) => ({ value: b, label: b }))]} value={row.branch} onValueChange={(v) => typeof v === "string" && onChange({ branch: v })}>
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
    </div>
  )
}

function Secret({ label, value, copyLabel, secret }: { label: string; value: string; copyLabel: string; secret?: boolean }) {
  return (
    <div role="group" aria-label={label} className="flex flex-col gap-1.5">
      <span aria-hidden="true" className="text-xs text-muted-foreground">
        {label}
      </span>
      <SecretValue value={value} copyLabel={copyLabel} secret={secret} />
    </div>
  )
}
