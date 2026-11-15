// What the landing page and the shared head tags say about the site. The
// origin comes from Astro's `site` (SITE_URL), never from a hard-coded host.
export const site = {
  name: "Lumbung",
  title: "Lumbung: SQLite and file storage on your own server",
  headline: "SQLite and file storage on your own server",
  tagline: "Branch a database before you change it.",
  description: "Branch a database before you change it, and keep files next to it.",
  github: "https://github.com/zakiego/lumbung",
  ogAlt: "Lumbung: SQLite and file storage on your own server",
}

// The landing page's dark tokens (src/styles/landing.css), for the places
// that can't read CSS: the Open Graph cards, the icons and theme-color.
export const dark = {
  background: "#08090a",
  foreground: "#f7f8f8",
  muted: "#8a8f98",
  faint: "#62666d",
  mark: "#e8e2cf",
}

// Bump this when the Open Graph cards change, so a link shared before fetches
// the new one.
export const ogVersion = 2

// Dark first, matching the landing page; light follows the docs' theme.
export const themeColor = { dark: dark.background, light: "#ffffff" }

// The granary mark from public/favicon.svg, on a 32-unit grid.
export const markPath = "M16 3 3 13h4v9h18v-9h4L16 3Zm-5 12h10v5H11v-5Zm-2 9h2v5H9v-5Zm12 0h2v5h-2v-5Z"
