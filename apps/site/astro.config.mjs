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
// Pages build sets SITE_URL. Without it, Astro's own command decides: `dev`
// stays on localhost, anything else (build, preview, a programmatic build)
// gets the production default, so a localhost URL never ships in those tags.
/** @returns {import("astro").AstroIntegration} */
function siteUrl() {
  return {
    name: "lumbung-site-url",
    hooks: {
      "astro:config:setup": ({ command, config, updateConfig }) => {
        if (config.site) return
        updateConfig({ site: command === "dev" ? "http://localhost:4872" : "https://lumbung.zakiego.com" })
      },
    },
  }
}

export default defineConfig({
  site: process.env.SITE_URL || undefined,
  trailingSlash: "ignore",
  devToolbar: { enabled: false },
  vite: { plugins: [tailwindcss()] },
  integrations: [
    siteUrl(),
    react(),
    starlight({
      title: "Lumbung",
      description: "Self-hosted SQLite databases with branches.",
      logo: { dark: "./src/assets/mark-dark.svg", light: "./src/assets/mark-light.svg", alt: "" },
      social: [{ icon: "github", label: "GitHub", href: "https://github.com/zakiego/lumbung" }],
      customCss: ["@fontsource-variable/inter", "@fontsource-variable/jetbrains-mono", "./src/styles/docs.css"],
      // Code blocks: vesper on dark (the landing's code colors), lines that
      // wrap instead of scrolling sideways, and no terminal title bar. A block
      // with a title= still gets its file tab.
      expressiveCode: {
        themes: ["vesper", "github-light"],
        defaultProps: { wrap: true, frame: "code" },
        styleOverrides: {
          borderRadius: "8px",
          codeFontSize: "0.8125rem",
          codeLineHeight: "1.7",
          codePaddingBlock: "0.875rem",
          codePaddingInline: "1rem",
          uiFontFamily: "var(--__sl-font)",
          uiFontSize: "0.8125rem",
          // One per theme, in the order above: a step off each page color.
          codeBackground: ["#101010", "#f9f8f9"],
          frames: { frameBoxShadowCssValue: "none" },
        },
      },
      head: [
        // The page backgrounds in src/styles/docs.css (--sl-color-black).
        { tag: "meta", attrs: { name: "theme-color", content: "#08090a", media: "(prefers-color-scheme: dark)" } },
        { tag: "meta", attrs: { name: "theme-color", content: "#ffffff", media: "(prefers-color-scheme: light)" } },
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
