import { defineConfig } from "vitest/config"

export default defineConfig({
  test: {
    globalSetup: ["./test/setup.ts"],
    include: ["test/**/*.test.ts"],
    testTimeout: 30_000,
    hookTimeout: 120_000,
  },
})
