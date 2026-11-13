import { createFileRoute, redirect } from "@tanstack/react-router"

// Until the real screens land (milestone 8), the root opens the prototype.
export const Route = createFileRoute("/")({
  beforeLoad: () => {
    throw redirect({ to: "/prototypes/overview" })
  },
})
