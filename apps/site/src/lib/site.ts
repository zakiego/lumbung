// What the landing page and the shared head tags say about the site. The
// origin comes from Astro's `site` (SITE_URL), never from a hard-coded host.
export const site = {
  name: "Lumbung",
  title: "Lumbung: SQLite databases with branches on your own server",
  headline: "Run SQLite databases with branches on your own server",
  description: "Copy a database, try your change on the copy, and keep main safe.",
  github: "https://github.com/zakiego/lumbung",
  ogAlt: "Lumbung: run SQLite databases with branches on your own server",
}

// Dark first, matching the landing page; light follows the docs' theme.
export const themeColor = { dark: "#08090a", light: "#ffffff" }

// The granary mark from public/favicon.svg, on a 32-unit grid.
export const markPath = "M16 3 3 13h4v9h18v-9h4L16 3Zm-5 12h10v5H11v-5Zm-2 9h2v5H9v-5Zm12 0h2v5h-2v-5Z"
