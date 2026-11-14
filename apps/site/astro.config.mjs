// @ts-check
import react from "@astrojs/react"
import starlight from "@astrojs/starlight"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "astro/config"

// Landing page at /, Starlight docs at /docs. Starlight serves every page in
// src/content/docs/, so the docs live one folder down in docs/ to get the
// /docs prefix (Starlight's documented way to run at a subpath). The landing
// uses React islands (ui-sdk) and Tailwind; its stylesheet is imported only
// by the landing layout, so the docs keep Starlight's own styles.

// The public origin for canonical, Open Graph, sitemap and robots URLs. The
// Pages build sets SITE_URL; a build without it gets the production default,
// so a localhost URL never ships in those tags. Dev stays on localhost.
const isBuild = process.argv.includes("build")
const site = process.env.SITE_URL ?? (isBuild ? "https://lumbung.zakiego.com" : "http://localhost:4872")

export default defineConfig({
  site,
  trailingSlash: "ignore",
  devToolbar: { enabled: false },
  vite: { plugins: [tailwindcss()] },
  integrations: [
    react(),
    starlight({
      title: "Lumbung",
      description: "Self-hosted SQLite databases with branches.",
      head: [
        { tag: "link", attrs: { rel: "icon", href: "/favicon.ico", sizes: "32x32" } },
        { tag: "link", attrs: { rel: "apple-touch-icon", href: "/apple-touch-icon.png" } },
        { tag: "link", attrs: { rel: "manifest", href: "/site.webmanifest" } },
      ],
      routeMiddleware: "./src/route-data.ts",
      sidebar: [
        {
          label: "Start",
          items: [
            { label: "What is Lumbung", slug: "docs" },
            { label: "Quickstart", slug: "docs/quickstart" },
            { label: "Install", slug: "docs/install" },
          ],
        },
        { label: "Concepts", items: [{ autogenerate: { directory: "docs/concepts" } }] },
        { label: "Guides", items: [{ autogenerate: { directory: "docs/guides" } }] },
        { label: "Reference", items: [{ autogenerate: { directory: "docs/reference" } }] },
        { label: "Self-hosting", items: [{ autogenerate: { directory: "docs/self-hosting" } }] },
        {
          label: "Help",
          items: [
            { label: "Troubleshooting", slug: "docs/troubleshooting" },
            { label: "Changelog", slug: "docs/changelog" },
          ],
        },
      ],
    }),
  ],
})
