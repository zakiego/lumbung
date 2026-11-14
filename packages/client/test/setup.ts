import type { TestProject } from "vitest/node"

import { engineBinary, startEngine } from "./engine.ts"

declare module "vitest" {
  export interface ProvidedContext {
    url: string
    token: string
    bin: string
  }
}

export default async function setup(project: TestProject) {
  const bin = engineBinary()
  // Every test file shares this engine and its admin token, so the per-token
  // rate limit is raised: together they can pass the default burst.
  const engine = await startEngine(bin, { LUMBUNG_RATE_LIMIT: "100000", LUMBUNG_RATE_BURST: "100000" })
  project.provide("url", engine.url)
  project.provide("token", engine.token)
  project.provide("bin", bin)
  return engine.stop
}
