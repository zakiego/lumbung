import { defineConfig } from "vitest/config"

// Server tests run in Node against a temp data folder; component tests run in
// jsdom. The app's Vite config isn't merged: its Start and Nitro plugins only
// matter for the real build.
export default defineConfig({
  resolve: { tsconfigPaths: true },
  test: {
    projects: [
      {
        extends: true,
        test: { name: "server", environment: "node", include: ["src/**/*.test.ts"] },
      },
      {
        extends: true,
        test: { name: "ui", environment: "jsdom", include: ["src/**/*.test.tsx"], setupFiles: ["./src/test/setup.ts"] },
      },
    ],
  },
})
