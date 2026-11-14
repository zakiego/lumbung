import { type ReactNode, createContext, use } from "react"
import type { WorkspaceInfo } from "@/lib/workspace"

const WorkspaceContext = createContext<WorkspaceInfo | null>(null)

export function WorkspaceProvider({ workspace, children }: { workspace: WorkspaceInfo | null; children: ReactNode }) {
  return <WorkspaceContext value={workspace}>{children}</WorkspaceContext>
}

/** The workspace on screen. Null on a page shown to someone who has none yet. */
export function useWorkspaceOrNull(): WorkspaceInfo | null {
  return use(WorkspaceContext)
}

export function useWorkspace(): WorkspaceInfo {
  const w = use(WorkspaceContext)
  if (!w) throw new Error("useWorkspace outside a workspace")
  return w
}

/** The workspace's name, as links and server functions take it. */
export function useWs(): string {
  return useWorkspace().name
}
