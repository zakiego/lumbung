// The Drizzle example, end to end: drizzle-kit's migration files applied by
// `lumbung migrate apply`, then the app's queries through Drizzle.
import { execFileSync } from "node:child_process"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { afterAll, beforeAll, expect, test } from "vitest"

import { createClient } from "@lumbung/client"

import { engineBinary, startEngine, type Running } from "../../../packages/client/test/engine.ts"
import { addUserWithPost, connect, latestPosts } from "../src/app.ts"

const here = dirname(fileURLToPath(import.meta.url))
let engine: Running
let cli: string

beforeAll(async () => {
  engine = await startEngine(engineBinary())
  const prebuilt = process.env.LUMBUNG_CLI_BIN
  cli = prebuilt ?? join(mkdtempSync(join(tmpdir(), "lumbung-cli-")), "lumbung")
  if (!prebuilt) {
    execFileSync("go", ["build", "-o", cli, "./cmd/lumbung"], { cwd: resolve(here, "../../../apps/engine"), stdio: "inherit" })
  }
})

afterAll(async () => {
  await engine?.stop()
})

test("drizzle-kit migrations, then Drizzle queries", async () => {
  await createClient({ url: engine.url, token: engine.token }).databases.create("blog")
  const env = {
    ...process.env,
    LUMBUNG_URL: engine.url,
    LUMBUNG_TOKEN: engine.token,
    LUMBUNG_DATABASE: "blog",
    LUMBUNG_CONFIG: join(tmpdir(), "lumbung-example-none.json"),
  }
  const out = execFileSync(cli, ["migrate", "apply", "--dir", resolve(here, "../drizzle")], { env }).toString()
  expect(out).toContain("applied 0000_init.sql")

  const db = connect(engine.url, engine.token, "blog")
  await addUserWithPost(db, "ana@example.com", "Ana", "First post")
  await addUserWithPost(db, "budi@example.com", "Budi", "Second post")
  expect(await latestPosts(db)).toEqual([
    { title: "Second post", author: "Budi" },
    { title: "First post", author: "Ana" },
  ])
  // The unique index from the migration holds. Drizzle wraps driver errors;
  // the LumbungError, with its stable code, is the cause.
  await expect(addUserWithPost(db, "ana@example.com", "Dup", "x")).rejects.toMatchObject({ cause: { code: "constraint_violation" } })
})
