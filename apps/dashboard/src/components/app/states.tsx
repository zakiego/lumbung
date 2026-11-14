import { useRouter } from "@tanstack/react-router"
import { CircleAlertIcon, SparklesIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Empty, Skeleton } from "@/components/ui/surface"

export function ErrorState({ error, title = "Couldn't load this page" }: { error: unknown; title?: string }) {
  const router = useRouter()
  return (
    <Empty icon={<CircleAlertIcon />} title={title} description={error instanceof Error ? error.message : String(error)}>
      <Button variant="secondary" onClick={() => router.invalidate()}>
        Try again
      </Button>
    </Empty>
  )
}

export function ComingNext() {
  return <Empty icon={<SparklesIcon />} title="Coming next" />
}

const PLACEHOLDERS = ["a", "b", "c", "d", "e", "f", "g", "h"]

export function RowsSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div className="flex flex-col gap-3" aria-busy="true" aria-label="Loading">
      {PLACEHOLDERS.slice(0, rows).map((id) => (
        <Skeleton key={id} className="h-11" />
      ))}
    </div>
  )
}
