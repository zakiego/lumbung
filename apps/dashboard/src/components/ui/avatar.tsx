import { AvatarFallback, Avatar as AvatarPart } from "@ui-sdk/react/avatar"
import { cn } from "@/lib/utils"

// Decorative: the name is always shown next to it.
export function Avatar({ name, className }: { name: string; className?: string }) {
  const initials =
    name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((w) => w[0]?.toUpperCase())
      .join("") || "?"
  return (
    <AvatarPart aria-hidden="true" className={cn("inline-flex size-7 shrink-0 items-center justify-center rounded-md bg-accent text-[11px] font-[590] text-foreground", className)}>
      <AvatarFallback>{initials}</AvatarFallback>
    </AvatarPart>
  )
}
