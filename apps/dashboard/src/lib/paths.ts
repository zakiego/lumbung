// The dashboard lives under /app on the public hostname, next to the site at
// / and the engine at /v1. Its own endpoints (auth, health) sit under it too,
// so a proxy only needs one rule for the dashboard.
export const APP_BASE = "/app"
export const AUTH_BASE = `${APP_BASE}/api/auth`

export function pageTitle(...parts: string[]): string {
  return [...parts, "Lumbung"].join(" · ")
}

// A public href inside the dashboard (it starts with /app), never another site.
export function safeNext(next: string | undefined): string {
  if (next && (next === APP_BASE || next.startsWith(`${APP_BASE}/`) || next.startsWith(`${APP_BASE}?`)) && !next.includes("\\")) return next
  return `${APP_BASE}/`
}
