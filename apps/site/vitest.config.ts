import { defineConfig } from "vitest/config"

// Component tests for the landing page's React islands, in jsdom. Astro's own
// config isn't merged: none of its integrations matter here.
export default defineConfig({
  resolve: { tsconfigPaths: true },
  test: { environment: "jsdom", include: ["src/**/*.test.tsx"], setupFiles: ["./src/test/setup.ts"] },
})
