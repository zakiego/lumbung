// Which paths on our public hostname belong to the Docker host (through the
// tunnel) and which to the static site on Cloudflare Pages.
//
// The Docker host owns:
//   /app/*       the dashboard (the tunnel's path rule sends it to its port)
//   /v1/*        the engine's REST API
//   /mcp/*       the engine's MCP endpoint
//   /health      the engine's healthcheck
//
// Everything else ("/", /docs, /og, /llms.txt, robots.txt, icons...) is the
// site. Matching is by whole path segment, so /apple-touch-icon.png,
// /v1x, /mcp-tools and /healthz stay on the site.
const APP_PREFIXES = ["/app", "/v1", "/mcp", "/health"]

const underAny = (pathname: string, prefixes: string[]) =>
  prefixes.some((p) => pathname === p || pathname.startsWith(`${p}/`))

export function isAppPath(pathname: string): boolean {
  return underAny(pathname, APP_PREFIXES)
}
