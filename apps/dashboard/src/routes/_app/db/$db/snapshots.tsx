import { createFileRoute } from "@tanstack/react-router"
import { DbFrame } from "@/components/app/db-frame"
import { ComingNext } from "@/components/app/states"
import { pageTitle } from "@/lib/paths"

export const Route = createFileRoute("/_app/db/$db/snapshots")({
  head: ({ params }) => ({ meta: [{ title: pageTitle("Snapshots", params.db) }] }),
  component: () => (
    <DbFrame section="snapshots">
      <ComingNext />
    </DbFrame>
  ),
})
