import { getSession } from "@/fns/session"

type Session = Awaited<ReturnType<typeof getSession>>

// The layout checks the session on every navigation. In the browser, answer
// from a signed-in session we already have and refresh it in the background
// once it's older than the TTL, so navigation never waits on this check. The
// server still checks every call; a refresh that finds no session clears the
// cache and the next navigation goes to the login page.
const TTL_MS = 30_000
let cached: { at: number; session: Session } | undefined
let refreshing: Promise<Session> | undefined
let primed = false
// Bumped on sign-in and sign-out, so an answer to a request sent before then is dropped.
let generation = 0

function fetchSession(): Promise<Session> {
  if (refreshing) return refreshing
  const started = generation
  const request = getSession()
    .then((session) => {
      if (started === generation) cached = session.viewer ? { at: Date.now(), session } : undefined
      return session
    })
    .finally(() => {
      if (refreshing === request) refreshing = undefined
    })
  refreshing = request
  return request
}

export async function loadSession(): Promise<Session> {
  if (typeof window === "undefined") return getSession()
  if (!cached) return fetchSession()
  if (Date.now() - cached.at >= TTL_MS) fetchSession().catch(() => {})
  return cached.session
}

/**
 * Seeds the browser cache once per page load with the session the server
 * rendered, so the first client navigation doesn't ask again. Only the first
 * call counts, so a sign-out that clears the cache can't be undone by a render.
 */
export function primeSession(session: Session) {
  if (typeof window === "undefined" || primed) return
  primed = true
  if (session.viewer && !cached) cached = { at: Date.now(), session }
}

export function forgetSession() {
  cached = undefined
  refreshing = undefined
  generation++
}
