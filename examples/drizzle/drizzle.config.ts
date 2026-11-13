import { defineConfig } from "drizzle-kit"

// drizzle-kit only writes migration files here; `lumbung migrate apply`
// runs them against a branch.
export default defineConfig({
  dialect: "sqlite",
  schema: "./src/schema.ts",
  out: "./drizzle",
})
