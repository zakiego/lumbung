import { createFileRoute } from "@tanstack/react-router"
import { DbFrame } from "@/components/app/db-frame"
import { ComingNext } from "@/components/app/states"
import { pageTitle } from "@/lib/paths"

export const Route = createFileRoute("/_app/db/$db/data")({
  head: ({ params }) => ({ meta: [{ title: pageTitle("Data", params.db) }] }),
  component: () => (
    <DbFrame section="data">
      <ComingNext />
    </DbFrame>
  ),
})
