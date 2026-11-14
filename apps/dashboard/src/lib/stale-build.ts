// A deploy replaces every hashed chunk under /app/assets. A tab still running
// the previous build then asks for chunks that no longer exist, and the
// dynamic import rejects. Reloading picks up the new build.

const STALE_BUILD_MESSAGES = [
  "Failed to fetch dynamically imported module", // Chromium
  "error loading dynamically imported module", // Firefox
  "Importing a module script failed", // Safari, every iOS browser
]

export function isStaleBuildError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : ""
  return STALE_BUILD_MESSAGES.some((prefix) => message.startsWith(prefix))
}

const RELOAD_KEY = "lumbung:stale-build-reload"
const RELOAD_COOLDOWN_MS = 10_000

// Reloads once per cooldown window. A timestamp instead of a flag, because
// Safari's message carries no chunk URL: a flag keyed on it would block the
// reload after every later deploy in the same tab. Returns false when it
// didn't reload, so the caller can offer a manual reload instead.
export function reloadForNewBuild(now = Date.now()): boolean {
  if (typeof window === "undefined") return false
  try {
    const last = Number(window.sessionStorage.getItem(RELOAD_KEY) ?? 0)
    if (now - last < RELOAD_COOLDOWN_MS) return false
    window.sessionStorage.setItem(RELOAD_KEY, String(now))
  } catch {
    // Storage blocked: without a guard a reload could loop, so leave it to the user.
    return false
  }
  window.location.reload()
  return true
}
