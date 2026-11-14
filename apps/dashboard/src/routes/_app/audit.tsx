import { createFileRoute, redirect } from "@tanstack/react-router"
import { legacyHref } from "@/lib/workspace"

// A URL from before workspaces: the same page, in the default workspace.
export const Route = createFileRoute("/_app/audit")({
  beforeLoad: ({ context, location }) => {
    const href = legacyHref(context.workspaces, "audit", location.searchStr)
    throw href ? redirect({ href, replace: true }) : redirect({ to: "/", replace: true })
  },
})
