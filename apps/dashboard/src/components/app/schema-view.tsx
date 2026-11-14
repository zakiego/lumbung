import type { Index, Schema, Table as SchemaTable, Trigger, View } from "@lumbung/client"
import { ChevronRightIcon, CodeIcon, EyeIcon, KeyRoundIcon, SearchIcon, TableIcon, ZapIcon } from "lucide-react"
import { type ReactNode, useDeferredValue, useState } from "react"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Collapsible, CollapsiblePanel, CollapsibleTrigger, ScrollArea } from "@/components/ui/layout"
import { Badge, Empty, Table, TableCell, TableHead, TableRow } from "@/components/ui/surface"
import { number } from "@/lib/format"
import { CopyButton } from "./connect"

/** DDL in a soft block with an overlay scrollbar and a copy button that shows on hover. */
export function Ddl({ sql, label }: { sql: string; label: string }) {
  return (
    <div className="group/code relative min-w-0 rounded-lg border bg-code">
      <ScrollArea axis="both" viewportClassName="max-h-80" label={label}>
        <pre className="py-3 pr-14 pl-4 font-mono text-[12.5px] leading-[1.6] text-foreground/90">
          <code>{sql}</code>
        </pre>
      </ScrollArea>
      <CopyButton
        text={sql}
        label={`Copy ${label}`}
        className="absolute top-2 right-2 opacity-0 transition-opacity duration-250 group-hover/code:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100"
      />
    </div>
  )
}

function Section({ title, count, children }: { title: string; count: number; children: ReactNode }) {
  return (
    <section aria-label={title} className="flex flex-col gap-2">
      <h2 className="text-[13px] font-[590]">
        {title} <span className="font-normal text-faint tabular-nums">{number.format(count)}</span>
      </h2>
      <div className="flex flex-col divide-y rounded-lg border">{children}</div>
    </section>
  )
}

function Item({ icon, name, meta, defaultOpen, children }: { icon: ReactNode; name: string; meta?: ReactNode; defaultOpen?: boolean; children: ReactNode }) {
  return (
    <Collapsible defaultOpen={defaultOpen} className="group/item min-w-0">
      <CollapsibleTrigger className="flex h-10 w-full min-w-0 cursor-default items-center gap-2 px-3 text-left text-[13px] outline-none transition-colors duration-150 hover:bg-accent/50 hover:duration-0 focus-visible:bg-accent/50">
        <ChevronRightIcon aria-hidden="true" className="size-3.5 shrink-0 text-faint transition-transform duration-150 group-data-open/item:rotate-90 motion-reduce:transition-none" />
        <span className="flex shrink-0 text-faint [&_svg]:size-4">{icon}</span>
        <span className="min-w-0 truncate font-mono" title={name}>
          {name}
        </span>
        {meta && <span className="ml-auto shrink-0 pl-2 text-xs text-muted-foreground tabular-nums">{meta}</span>}
      </CollapsibleTrigger>
      <CollapsiblePanel>
        <div className="flex flex-col gap-4 px-3 pt-1 pb-4">{children}</div>
      </CollapsiblePanel>
    </Collapsible>
  )
}

function Columns({ table }: { table: SchemaTable }) {
  const cols = table.columns.filter((c) => c.hidden !== 1)
  return (
    <ScrollArea axis="both" viewportClassName="max-h-[28rem]" label={`Columns of ${table.name}`}>
      <Table className="min-w-[32rem]">
        <thead>
          <tr>
            <TableHead>Column</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Not null</TableHead>
            <TableHead>Default</TableHead>
          </tr>
        </thead>
        <tbody>
          {cols.map((c) => (
            <TableRow key={c.name} className="hover:bg-transparent">
              <TableCell className="h-9 max-w-72">
                <span className="flex min-w-0 items-center gap-1.5 font-mono text-xs">
                  <span className="truncate" title={c.name}>
                    {c.name}
                  </span>
                  {c.pk > 0 && <KeyRoundIcon aria-label="Primary key" className="size-3.5 shrink-0 text-faint" />}
                  {(c.hidden === 2 || c.hidden === 3) && <Badge className="h-4 px-1.5 text-[10px]">generated</Badge>}
                </span>
              </TableCell>
              <TableCell className="h-9 max-w-48 truncate font-mono text-xs text-muted-foreground uppercase" title={c.type}>
                {c.type || <span className="text-faint normal-case">none</span>}
              </TableCell>
              <TableCell className="h-9 text-xs text-muted-foreground">{c.not_null ? "Yes" : <span className="text-faint">No</span>}</TableCell>
              <TableCell className="h-9 max-w-64 truncate font-mono text-xs text-muted-foreground" title={c.default ?? undefined}>
                {c.default ?? <span className="text-faint">-</span>}
              </TableCell>
            </TableRow>
          ))}
        </tbody>
      </Table>
    </ScrollArea>
  )
}

function IndexLine({ index }: { index: Index }) {
  return (
    <li className="flex min-w-0 flex-col gap-2">
      <div className="flex min-w-0 items-center gap-2 text-xs">
        <span className="truncate font-mono" title={index.name}>
          {index.name}
        </span>
        {index.unique && <Badge>unique</Badge>}
        <span className="min-w-0 truncate font-mono text-muted-foreground">({index.columns.join(", ")})</span>
      </div>
      {index.sql && <Ddl sql={index.sql} label={`${index.name} DDL`} />}
    </li>
  )
}

function SubHead({ children }: { children: ReactNode }) {
  return <h3 className="text-xs font-[510] text-muted-foreground">{children}</h3>
}

const OPEN_UP_TO = 3

export function SchemaView({ schema }: { schema: Schema }) {
  const [q, setQ] = useState("")
  const query = useDeferredValue(q.trim().toLowerCase())
  const match = (name: string) => !query || name.toLowerCase().includes(query)

  const tables = schema.tables.filter((t) => !t.name.startsWith("sqlite_"))
  if (!tables.length && !schema.views.length) return <Empty icon={<CodeIcon />} title="No tables yet" description="Create one in the SQL tab." />

  const shownTables = tables.filter((t) => match(t.name))
  const shownViews = schema.views.filter((v) => match(v.name))
  const indexesOf = (t: string) => schema.indexes.filter((i) => i.table === t)
  const triggersOf = (t: string) => schema.triggers.filter((tr) => tr.table === t)
  const open = tables.length + schema.views.length <= OPEN_UP_TO

  return (
    <div className="flex flex-col gap-6">
      <Field className="max-w-xs">
        <FieldLabel className="sr-only">Search schema</FieldLabel>
        <div className="relative">
          <SearchIcon aria-hidden="true" className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-faint" />
          <Input type="search" value={q} onValueChange={setQ} placeholder="Search tables and views" className="pl-8" autoComplete="off" spellCheck={false} />
        </div>
      </Field>

      {!shownTables.length && !shownViews.length && <Empty icon={<SearchIcon />} title="No matches" />}

      {shownTables.length > 0 && (
        <Section title="Tables" count={shownTables.length}>
          {shownTables.map((t) => (
            <TableItem key={t.name} table={t} indexes={indexesOf(t.name)} triggers={triggersOf(t.name)} defaultOpen={open} />
          ))}
        </Section>
      )}

      {shownViews.length > 0 && (
        <Section title="Views" count={shownViews.length}>
          {shownViews.map((v) => (
            <ViewItem key={v.name} view={v} defaultOpen={open} />
          ))}
        </Section>
      )}
    </div>
  )
}

function TableItem({ table, indexes, triggers, defaultOpen }: { table: SchemaTable; indexes: Index[]; triggers: Trigger[]; defaultOpen: boolean }) {
  const cols = table.columns.filter((c) => c.hidden !== 1).length
  const meta = [`${number.format(cols)} ${cols === 1 ? "column" : "columns"}`, indexes.length ? `${indexes.length} ${indexes.length === 1 ? "index" : "indexes"}` : "", table.virtual ? "virtual" : ""]
    .filter(Boolean)
    .join(" · ")
  return (
    <Item icon={<TableIcon />} name={table.name} meta={meta} defaultOpen={defaultOpen}>
      <Columns table={table} />
      {indexes.length > 0 && (
        <div className="flex flex-col gap-2">
          <SubHead>Indexes</SubHead>
          <ul className="flex flex-col gap-3">
            {indexes.map((i) => (
              <IndexLine key={i.name} index={i} />
            ))}
          </ul>
        </div>
      )}
      {triggers.length > 0 && (
        <div className="flex flex-col gap-2">
          <SubHead>Triggers</SubHead>
          <ul className="flex flex-col gap-3">
            {triggers.map((tr) => (
              <li key={tr.name} className="flex min-w-0 flex-col gap-2">
                <span className="flex min-w-0 items-center gap-1.5 font-mono text-xs">
                  <ZapIcon aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
                  <span className="truncate">{tr.name}</span>
                </span>
                <Ddl sql={tr.sql} label={`${tr.name} DDL`} />
              </li>
            ))}
          </ul>
        </div>
      )}
      <div className="flex flex-col gap-2">
        <SubHead>DDL</SubHead>
        <Ddl sql={table.sql} label={`${table.name} DDL`} />
      </div>
    </Item>
  )
}

function ViewItem({ view, defaultOpen }: { view: View; defaultOpen: boolean }) {
  return (
    <Item icon={<EyeIcon />} name={view.name} defaultOpen={defaultOpen}>
      <Ddl sql={view.sql} label={`${view.name} DDL`} />
    </Item>
  )
}
