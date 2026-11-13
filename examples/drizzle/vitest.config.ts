import { fileURLToPath } from "node:url"
import { defineConfig } from "vitest/config"

const client = (p: string) => fileURLToPath(new URL(`../../packages/client/src/${p}`, import.meta.url))

export default defineConfig({
  // Test against the SDK's source, so no build step is needed first.
  resolve: {
    alias: [
      { find: /^@lumbung\/client\/drizzle$/, replacement: client("drizzle.ts") },
      { find: /^@lumbung\/client$/, replacement: client("index.ts") },
    ],
  },
  test: { include: ["test/**/*.test.ts"], testTimeout: 60_000, hookTimeout: 180_000 },
})
