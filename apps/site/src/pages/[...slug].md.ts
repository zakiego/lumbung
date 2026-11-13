// Every docs page as plain Markdown at /docs/<page>.md, for agents.
import type { APIRoute, GetStaticPaths } from "astro"
import { getCollection, type CollectionEntry } from "astro:content"

export const getStaticPaths: GetStaticPaths = async () => {
  const pages = await getCollection("docs")
  return pages.map((page) => ({ params: { slug: page.id }, props: { page } }))
}

// Drop MDX comments and docs-test markers: they mean nothing to a reader.
function clean(body: string): string {
  return body
    .replace(/\{\/\*[\s\S]*?\*\/\}\n*/g, "")
    .replace(/<!--\s*docs-test:[^>]*-->\n*/g, "")
    .trim()
}

export const GET: APIRoute = ({ props }) => {
  const { page } = props as { page: CollectionEntry<"docs"> }
  const head = `# ${page.data.title}\n\n${page.data.description ? `${page.data.description}\n\n` : ""}`
  return new Response(head + clean(page.body ?? "") + "\n", {
    headers: { "Content-Type": "text/markdown; charset=utf-8" },
  })
}
