// @ts-check
import react from "@astrojs/react"
import starlight from "@astrojs/starlight"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "astro/config"

// Landing page at /, Starlight docs at /docs. Starlight serves every page in
// src/content/docs/, so the docs live one folder down in docs/ to get the
// /docs prefix (Starlight's documented way to run at a subpath). The landing
// uses React components (shadcn) and Tailwind; its stylesheet is imported
// only by the landing layout, so the docs keep Starlight's own styles.
export default defineConfig({
  site: process.env.SITE_URL ?? "http://localhost:4872",
  trailingSlash: "ignore",
  devToolbar: { enabled: false },
  vite: { plugins: [tailwindcss()] },
  integrations: [
    react(),
    starlight({
      title: "Lumbung",
      description: "Self-hosted SQLite databases with branches.",
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
