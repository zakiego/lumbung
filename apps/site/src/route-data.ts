// Gives each docs page its own Open Graph card (src/pages/og/[...slug].png.ts).
import { defineRouteMiddleware } from "@astrojs/starlight/route-data"
import { ogVersion } from "@/lib/site"

export const onRequest = defineRouteMiddleware(({ locals, site }) => {
  const { entry, head } = locals.starlightRoute
  if (!site || !entry.id.startsWith("docs")) return
  const image = new URL(`/og/${entry.id}.png?v=${ogVersion}`, site).href
  head.push(
    { tag: "meta", attrs: { property: "og:image", content: image } },
    { tag: "meta", attrs: { property: "og:image:width", content: "1200" } },
    { tag: "meta", attrs: { property: "og:image:height", content: "630" } },
    { tag: "meta", attrs: { property: "og:image:alt", content: entry.data.title } },
    { tag: "meta", attrs: { name: "twitter:image", content: image } },
  )
})
