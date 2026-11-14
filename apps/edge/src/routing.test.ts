import { describe, expect, it } from "vitest"
import { isAppPath } from "./routing.ts"

describe("isAppPath", () => {
  it("sends the dashboard's and the engine's paths to the origin", () => {
    for (const p of [
      "/app",
      "/app/",
      "/app/login",
      "/app/databases/shop",
      "/app/assets/main-abc.js",
      "/app/api/health",
      "/app/_serverFn/abc",
      "/v1",
      "/v1/",
      "/v1/databases",
      "/v1/databases/shop/branches/main/query",
      "/v1/openapi.json",
      "/mcp",
      "/mcp/",
      "/health",
    ]) {
      expect(isAppPath(p), p).toBe(true)
    }
  })

  it("serves everything else from the site", () => {
    for (const p of [
      "/",
      "/docs",
      "/docs/",
      "/docs/self-hosting/backups",
      "/docs/reference/rest-api",
      "/og/index.png",
      "/llms.txt",
      "/robots.txt",
      "/sitemap-index.xml",
      "/site.webmanifest",
      "/favicon.svg",
      "/apple-touch-icon.png",
      "/apple",
      "/apps",
      "/application",
      "/v1x",
      "/v10",
      "/v2/databases",
      "/mcp-tools",
      "/mcpx",
      "/healthz",
      "/health-check",
      "/healthy",
      "/api/health",
    ]) {
      expect(isAppPath(p), p).toBe(false)
    }
  })

  it("matches by path segment, not by substring", () => {
    expect(isAppPath("/docs/app")).toBe(false)
    expect(isAppPath("/docs/v1/databases")).toBe(false)
    expect(isAppPath("/docs/mcp")).toBe(false)
    expect(isAppPath("/docs/health")).toBe(false)
  })
})
