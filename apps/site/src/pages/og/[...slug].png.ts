// One Open Graph card per page: /og/index.png for the landing page and
// /og/docs/<page>.png for each docs page.
import type { APIRoute, GetStaticPaths } from "astro"
import { getCollection } from "astro:content"
import { renderCard } from "@/lib/og"
import { site } from "@/lib/site"

type Props = { title: string; line?: string; section?: string }

export const getStaticPaths: GetStaticPaths = async () => {
  const pages = await getCollection("docs")
  return [
    { params: { slug: "index" }, props: { title: site.headline, line: site.description } },
    ...pages.map((page) => ({
      params: { slug: page.id },
      props: { title: page.data.title, line: page.data.description, section: "Docs" },
    })),
  ]
}

export const GET: APIRoute = async ({ props, site: origin }) => {
  const { title, line, section } = props as Props
  const host = origin ? origin.host : "localhost"
  const png = await renderCard({ title, line, section, host })
  return new Response(png, { headers: { "Content-Type": "image/png" } })
}
