// Global request middleware for the dashboard.
import { createMiddleware, createStart } from "@tanstack/react-start"

// Security headers on every response. A header a route already set wins, so
// the sandbox CSP on object downloads (server/objects.ts) stays as it is. No
// script-src policy: the root route inlines its boot script.
const securityHeaders = createMiddleware().server(async ({ next }) => {
  const result = await next()
  const { config } = await import("@/server/config")
  const headers = result.response.headers
  const add = (name: string, value: string) => {
    try {
      if (!headers.has(name)) headers.set(name, value)
    } catch {
      // Some responses (a proxied fetch) have immutable headers.
    }
  }
  add("X-Frame-Options", "DENY")
  add("Content-Security-Policy", "frame-ancestors 'none'")
  add("X-Content-Type-Options", "nosniff")
  add("Referrer-Policy", "strict-origin-when-cross-origin")
  // No includeSubDomains: other names under the same domain may still be plain http.
  if (config().publicUrl.startsWith("https:")) add("Strict-Transport-Security", "max-age=31536000")
  return result
})

export const startInstance = createStart(() => ({
  requestMiddleware: [securityHeaders],
}))
