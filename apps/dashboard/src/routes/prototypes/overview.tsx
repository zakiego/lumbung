import { createFileRoute } from "@tanstack/react-router"
import { TooltipProvider } from "@/components/ui/tooltip"
import { BranchMap } from "@/prototype/overview/branch-map"
import { ConnectFirst } from "@/prototype/overview/connect-first"
import { Ledger } from "@/prototype/overview/ledger"
import { Picker } from "@/prototype/picker"

export const Route = createFileRoute("/prototypes/overview")({
  component: () => (
    <TooltipProvider delay={500}>
      <Picker
        variants={[
          { name: "Ledger", component: Ledger },
          { name: "Connect first", component: ConnectFirst },
          { name: "Branch map", component: BranchMap },
        ]}
      />
    </TooltipProvider>
  ),
})
