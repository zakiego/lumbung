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
  const engine = await startEngine(bin)
  project.provide("url", engine.url)
  project.provide("token", engine.token)
  project.provide("bin", bin)
  return engine.stop
}
