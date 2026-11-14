import { DownloadIcon, FileIcon, UploadIcon } from "lucide-react"
import { type FormEvent, type ReactNode, useState } from "react"
import { TypedConfirm } from "@/components/ui/alert-dialog"
import { Button, buttonVariants } from "@/components/ui/button"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { FileInput, FileInputTrigger, formatFileSize } from "@/components/ui/file-input"
import { Input } from "@/components/ui/input"
import { SegmentedControl, SegmentedControlItem } from "@/components/ui/segmented-control"
import { APP_BASE } from "@/lib/paths"
import { errorText } from "./form-dialog"
import { NAME_PATTERN } from "./name-dialog"

export const MAX_IMPORT_MB = 512

export function SettingsSection({ title, description, children, tone }: { title: string; description?: string; children: ReactNode; tone?: "danger" }) {
  return (
    <section className="grid gap-4 border-t py-8 first:border-t-0 first:pt-0 md:grid-cols-[240px_minmax(0,1fr)] md:gap-10">
      <div className="flex flex-col gap-1">
        <h2 className={tone === "danger" ? "text-[13px] font-[590] text-destructive" : "text-[13px] font-[590]"}>{title}</h2>
        {description && <p className="text-[13px] text-muted-foreground">{description}</p>}
      </div>
      <div className="flex min-w-0 flex-col gap-4">{children}</div>
    </section>
  )
}

export function RenameForm({ name, label = "Database name", onRename }: { name: string; label?: string; onRename: (next: string) => Promise<unknown> }) {
  const [value, setValue] = useState(name)
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (pending || value === name) return
    setPending(true)
    setError("")
    try {
      await onRename(value)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setPending(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <Field invalid={error ? true : undefined}>
        <FieldLabel className="sr-only">{label}</FieldLabel>
        <Input
          value={value}
          onValueChange={(v) => {
            setValue(v)
            setError("")
          }}
          required
          pattern={NAME_PATTERN}
          autoComplete="off"
          spellCheck={false}
          className="max-w-sm font-mono"
        />
        <FieldError match="patternMismatch">Use lowercase letters, digits and dashes.</FieldError>
        <FieldError match={!!error}>{error}</FieldError>
      </Field>
      <Button type="submit" variant="secondary" className="self-start" disabled={pending || value === name}>
        {pending ? "Renaming…" : "Rename"}
      </Button>
    </form>
  )
}

type Mode = "empty" | "replace"

export function formatOf(file: File): "db" | "sql" {
  return /\.sql$/i.test(file.name) ? "sql" : "db"
}

export async function uploadImport(o: { ws: string; db: string; branch: string; file: File; mode: Mode }) {
  const q = new URLSearchParams({ ws: o.ws, db: o.db, branch: o.branch, format: formatOf(o.file), mode: o.mode })
  const res = await fetch(`${APP_BASE}/api/import?${q}`, {
    method: "POST",
    body: o.file,
    headers: { "content-type": "application/octet-stream" },
    credentials: "same-origin",
  })
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as { error?: string } | null
    throw new Error(body?.error || `Import failed (${res.status}).`)
  }
}

export function ImportForm({ branch, onImport }: { branch: string; onImport: (file: File, mode: Mode) => Promise<unknown> }) {
  const [files, setFiles] = useState<File[]>([])
  const [mode, setMode] = useState<Mode>("empty")
  const [error, setError] = useState("")
  const [pending, setPending] = useState(false)
  const file = files[0]

  async function run() {
    if (!file) return
    setPending(true)
    setError("")
    try {
      await onImport(file, mode)
      setFiles([])
    } catch (e) {
      setError(errorText(e))
      throw e
    } finally {
      setPending(false)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <Field invalid={error ? true : undefined}>
        <FieldLabel>File</FieldLabel>
        <FileInput
          accept={[".db", ".sqlite", ".sqlite3", ".sql"]}
          maxSize={MAX_IMPORT_MB * 1024 * 1024}
          value={files}
          onValueChange={(v) => {
            setFiles(v)
            setError("")
          }}
          onReject={(rejected) => {
            const reasons = rejected[0]?.reasons ?? []
            setError(reasons.includes("file-too-large") ? `The file is over ${MAX_IMPORT_MB} MB.` : "Pick a .db, .sqlite or .sql file.")
          }}
        >
          <div className="flex min-w-0 flex-wrap items-center gap-3">
            <FileInputTrigger>
              <UploadIcon aria-hidden="true" />
              {file ? "Replace file" : "Choose file"}
            </FileInputTrigger>
            {file ? (
              <span className="flex min-w-0 items-center gap-1.5 text-[13px]">
                <FileIcon aria-hidden="true" className="size-4 shrink-0 text-faint" />
                <span className="truncate font-mono" title={file.name}>
                  {file.name}
                </span>
                <span className="shrink-0 text-muted-foreground tabular-nums">{formatFileSize(file.size)}</span>
              </span>
            ) : (
              <span className="text-[13px] text-muted-foreground">.db or .sql, up to {MAX_IMPORT_MB} MB</span>
            )}
          </div>
        </FileInput>
        <FieldError match={!!error}>{error}</FieldError>
      </Field>
      <Field>
        <FieldLabel>Mode</FieldLabel>
        <SegmentedControl value={mode} onValueChange={(v) => (v === "empty" || v === "replace") && setMode(v)}>
          <SegmentedControlItem value="empty">Empty branch only</SegmentedControlItem>
          <SegmentedControlItem value="replace">Replace data</SegmentedControlItem>
        </SegmentedControl>
      </Field>
      {mode === "replace" && file ? (
        <TypedConfirm
          trigger="Import"
          triggerClassName={buttonVariants({ className: "self-start" })}
          title={`Replace ${branch}?`}
          description={`Everything on ${branch} is replaced by ${file.name}.`}
          confirmText={branch}
          actionLabel="Replace data"
          agentConfirm={`Replaces every table on ${branch} with the imported file`}
          onConfirm={run}
        />
      ) : (
        <Button className="self-start" disabled={!file || pending} onClick={() => run().catch(() => {})}>
          {pending ? "Importing…" : "Import"}
        </Button>
      )}
    </div>
  )
}

export function ExportForm({ ws, db, branch }: { ws: string; db: string; branch: string }) {
  const [format, setFormat] = useState<"db" | "sql">("db")
  const href = `${APP_BASE}/api/export?${new URLSearchParams({ ws, db, branch, format })}`
  return (
    <div className="flex flex-col gap-4">
      <Field>
        <FieldLabel>Format</FieldLabel>
        <SegmentedControl value={format} onValueChange={(v) => (v === "db" || v === "sql") && setFormat(v)}>
          <SegmentedControlItem value="db">SQLite file</SegmentedControlItem>
          <SegmentedControlItem value="sql">SQL dump</SegmentedControlItem>
        </SegmentedControl>
      </Field>
      <Button variant="secondary" className="self-start" render={<a href={href} download />}>
        <DownloadIcon aria-hidden="true" />
        Download
      </Button>
    </div>
  )
}
