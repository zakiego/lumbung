import { createRouter } from "@tanstack/react-router"
import { ErrorState } from "@/components/app/states"
import { isStaleBuildError, reloadForNewBuild } from "@/lib/stale-build"
import { routeTree } from "./routeTree.gen"

export function getRouter() {
  return createRouter({
    routeTree,
    scrollRestoration: true,
    defaultPreload: "intent",
    // Route error components are split into their own chunks, so after a
    // deploy they can be missing too. This one ships in the main bundle.
    defaultErrorComponent: ({ error }) => <ErrorState error={error} />,
    defaultOnCatch: (error) => {
      if (isStaleBuildError(error)) reloadForNewBuild()
    },
  })
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
