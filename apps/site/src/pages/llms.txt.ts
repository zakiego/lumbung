// llms.txt: an index of the docs for agents, with links to each page as
// plain Markdown (served next to it as /docs/<page>.md).
import type { APIRoute } from "astro"
import { getCollection } from "astro:content"

export const GET: APIRoute = async ({ site }) => {
  const base = site ? site.origin : ""
  const pages = (await getCollection("docs")).sort((a, b) => a.id.localeCompare(b.id))
  const lines = [
    "# Lumbung",
    "",
    "> Self-hosted SQLite databases with branches. One server holds many databases, each with branches, reached through a REST API, the libSQL protocol, a TypeScript SDK, a CLI, MCP and a dashboard. Buckets store files next to them behind an S3-compatible API.",
    "",
    "## Docs",
    "",
    ...pages.map((p) => `- [${p.data.title}](${base}/${p.id}.md): ${p.data.description ?? ""}`),
    "",
  ]
  return new Response(lines.join("\n"), { headers: { "Content-Type": "text/plain; charset=utf-8" } })
}
