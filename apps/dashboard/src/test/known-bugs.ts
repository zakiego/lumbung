// Source bugs the tests know about. Each one is pinned by a `test.fails` in
// shell.test.tsx, so fixing the source makes that test fail and this file can shrink.

type Entry = { component: string; label?: string; buttons?: string[] }

/** The sidebar's scroll area, which an agent sees with no name (shell.tsx, SidebarBody). */
export function unnamedSidebarScroll(e: Entry): boolean {
  return !e.label && e.component === "scroll-area"
}

/** The search dialog, which has no name while it is closed and a page header gives it a second trigger. */
export function unnamedSearchDialog(e: Entry): boolean {
  return !e.label && e.component === "dialog" && (e.buttons ?? []).some((b) => b.startsWith("Search"))
}
