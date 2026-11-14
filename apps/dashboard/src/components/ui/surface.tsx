// Plain pieces with no behavior: styled HTML, no ui-sdk part needed.
import type { ComponentProps, ReactNode } from "react"
import { cn } from "@/lib/utils"

export function Card({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("rounded-lg border bg-card text-card-foreground", className)} {...props} />
}

export function CardHeader({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("flex items-center justify-between gap-3 px-4 pt-4", className)} {...props} />
}

export function CardTitle({ className, ...props }: ComponentProps<"h2">) {
  return <h2 className={cn("text-[13px] font-[590]", className)} {...props} />
}

export function CardContent({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("p-4", className)} {...props} />
}

// For a short tag on a row, like "default". A status is StatusText, never a pill.
export function Badge({ className, ...props }: ComponentProps<"span">) {
  return (
    <span
      className={cn("inline-flex h-5 shrink-0 items-center rounded-full border px-2 text-[11px] font-[510] text-muted-foreground", className)}
      {...props}
    />
  )
}

export function StatusText({ icon, tone = "muted", children, className }: { icon?: ReactNode; tone?: "muted" | "success" | "warning" | "destructive"; children: ReactNode; className?: string }) {
  const color = { muted: "text-muted-foreground", success: "text-success", warning: "text-warning", destructive: "text-destructive" }[tone]
  return (
    <span className={cn("inline-flex items-center gap-1 text-[13px] [&_svg]:size-3.5 [&_svg]:shrink-0", color, className)}>
      {icon}
      {children}
    </span>
  )
}

export function Skeleton({ className, ...props }: ComponentProps<"div">) {
  return <div aria-hidden="true" className={cn("rounded-md bg-accent motion-safe:animate-pulse", className)} {...props} />
}

export function Kbd({ className, ...props }: ComponentProps<"kbd">) {
  return (
    <kbd
      className={cn("inline-flex h-5 min-w-5 items-center justify-center rounded-[4px] border px-1 font-sans text-[11px] font-[510] text-muted-foreground", className)}
      {...props}
    />
  )
}

// One title and at most one short line: no paragraphs in empty states.
export function Empty({ icon, title, description, children, className }: { icon?: ReactNode; title: string; description?: ReactNode; children?: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed px-6 py-14 text-center", className)}>
      {icon && <div className="flex size-9 items-center justify-center rounded-lg border bg-card text-muted-foreground [&_svg]:size-4">{icon}</div>}
      <div className="flex flex-col gap-1">
        <h2 className="text-[14px] font-[590]">{title}</h2>
        {description && <p className="max-w-sm text-[13px] text-muted-foreground">{description}</p>}
      </div>
      {children}
    </div>
  )
}

export function Table({ className, ...props }: ComponentProps<"table">) {
  return (
    <div className="w-full overflow-x-auto">
      <table className={cn("w-full border-collapse text-[13px]", className)} {...props} />
    </div>
  )
}

export function TableHead({ className, ...props }: ComponentProps<"th">) {
  return <th className={cn("h-9 border-b px-3 text-left text-xs font-[510] whitespace-nowrap text-muted-foreground first:pl-0 last:pr-0", className)} {...props} />
}

export function TableRow({ className, ...props }: ComponentProps<"tr">) {
  return <tr className={cn("border-b transition-colors duration-150 hover:bg-accent/50 hover:duration-0", className)} {...props} />
}

export function TableCell({ className, ...props }: ComponentProps<"td">) {
  return <td className={cn("h-11 px-3 align-middle first:pl-0 last:pr-0", className)} {...props} />
}
