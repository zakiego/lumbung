import { getSession } from "@/fns/session"

type Session = Awaited<ReturnType<typeof getSession>>

// The layout checks the session on every navigation. In the browser, reuse a
// signed-in answer for a short while; the server still checks every call.
const TTL_MS = 30_000
let cached: { at: number; session: Session } | undefined

export async function loadSession(): Promise<Session> {
  if (typeof window !== "undefined" && cached && Date.now() - cached.at < TTL_MS) return cached.session
  const session = await getSession()
  if (typeof window !== "undefined" && session.viewer) cached = { at: Date.now(), session }
  return session
}

export function forgetSession() {
  cached = undefined
}
