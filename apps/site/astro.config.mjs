// @ts-check
import starlight from "@astrojs/starlight"
import { defineConfig } from "astro/config"

// Landing page at /, Starlight docs at /docs. Starlight serves every page in
// src/content/docs/, so the docs live one folder down in docs/ to get the
// /docs prefix (Starlight's documented way to run at a subpath).
export default defineConfig({
  site: process.env.SITE_URL ?? "http://localhost:4872",
  trailingSlash: "ignore",
  integrations: [
    starlight({
      title: "Lumbung",
      description: "Self-hosted SQLite databases with branches.",
      sidebar: [
        {
          label: "Start",
          items: [{ label: "What is Lumbung", slug: "docs" }],
        },
        { label: "Concepts", items: [{ autogenerate: { directory: "docs/concepts" } }] },
        { label: "Guides", items: [{ autogenerate: { directory: "docs/guides" } }] },
        { label: "Reference", items: [{ autogenerate: { directory: "docs/reference" } }] },
      ],
    }),
  ],
})
