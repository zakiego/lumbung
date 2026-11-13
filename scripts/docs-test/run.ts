// docs-test: runs every code sample in the docs against a live engine
// (PLAN 11, rule 1).
//
//   node scripts/docs-test/run.ts [page-filter]
//
// For each hand-written page under apps/site/src/content/docs/docs (generated
// reference pages are checked by drift tests instead), it starts a fresh
// lumbungd, sets up a fixture, and runs the page's samples in order, sharing
// one working folder:
//
//   sh, bash   run with bash -euo pipefail; the lumbung CLI and lumbungd are
//              on PATH and LUMBUNG_URL, LUMBUNG_TOKEN (instance admin) and
//              LUMBUNG_DATA_DIR are set
//   ts, js     run with node; @lumbung/client resolves to the built SDK
//   sql        run through `lumbung shell --db app`
//   others     (json, yaml, text, ...) aren't programs and aren't run
//
// A fence with title="path", or a ts/js fence whose first line is a comment
// naming a file (// src/schema.ts), is written to that path instead of run;
// a .ts or .js one is then imported once to check it loads.
//
// Markers, as HTML comments on their own line:
//   <!-- docs-test: skip REASON -->   don't run the next sample (REASON required)
//   <!-- docs-test: fresh -->         start this page with an empty instance
//   <!-- docs-test: empty-app -->     start with a database `app` and no tables
//   <!-- docs-test: continue -->      run the next ts/js sample appended to
//                                     the page's last sample without this
//                                     marker (its setup), as one program
// Otherwise the fixture is a database `app` with a `users` table
// (id, email, name) holding one row, as the quickstart leaves it.
//
// "http://127.0.0.1:4870" in samples becomes the test engine's URL, and
// "<token>" becomes "$LUMBUNG_TOKEN".
import { execFileSync, spawnSync } from "node:child_process"
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, relative, resolve } from "node:path"
import { fileURLToPath } from "node:url"

import { startEngine, type Running } from "../../packages/client/test/engine.ts"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..")
const docsDir = join(root, "apps/site/src/content/docs/docs")
// Inside examples/drizzle, so samples resolve @lumbung/client and drizzle.
const workRoot = join(root, "examples/drizzle/.docs-test")

type Sample = { lang: string; code: string; title?: string; skip?: string; cont?: boolean; line: number }
type Page = { file: string; mode: "fixture" | "fresh" | "empty-app"; samples: Sample[] }

const RUN = new Set(["sh", "bash", "ts", "js", "sql"])
const fileComment = /^\/\/ ([\w./-]+\.(?:ts|js|mjs|json))\s*$/
const marker = /^\s*<!--\s*docs-test:\s*(skip|fresh|empty-app|continue)\b\s*(.*?)\s*-->\s*$/

function parse(file: string): Page {
  const lines = readFileSync(file, "utf8").split("\n")
  const page: Page = { file, mode: "fixture", samples: [] }
  let pendingSkip: string | undefined
  let pendingContinue = false
  for (let i = 0; i < lines.length; i++) {
    const m = lines[i]!.match(marker)
    if (m) {
      if (m[1] === "skip") {
        if (!m[2]) throw new Error(`${relative(root, file)}:${i + 1}: docs-test: skip needs a reason`)
        pendingSkip = m[2]
      } else if (m[1] === "continue") {
        pendingContinue = true
      } else {
        page.mode = m[1] as Page["mode"]
      }
      continue
    }
    const open = lines[i]!.match(/^(\s*)```(\w*)\s*(.*)$/)
    if (!open) continue
    const indent = open[1]!.length
    const lang = open[2]!.toLowerCase()
    let title = open[3]!.match(/title="([^"]+)"/)?.[1]
    const body: string[] = []
    const start = i + 1
    for (i++; i < lines.length && !/^\s*```\s*$/.test(lines[i]!); i++) {
      body.push(lines[i]!.slice(Math.min(indent, lines[i]!.length - lines[i]!.trimStart().length)))
    }
    if (!title && (lang === "ts" || lang === "js")) title = body[0]?.match(fileComment)?.[1]
    page.samples.push({ lang, code: body.join("\n") + "\n", title, skip: pendingSkip, cont: pendingContinue, line: start })
    pendingSkip = undefined
    pendingContinue = false
  }
  return page
}

function pagesUnder(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) return pagesUnder(p)
    return /\.mdx?$/.test(name) ? [p] : []
  })
}

function build(): string {
  const bin = process.env.LUMBUNG_BIN_DIR ?? mkdtempSync(join(tmpdir(), "lumbung-docs-bin-"))
  if (!process.env.LUMBUNG_BIN_DIR) {
    execFileSync("go", ["build", "-o", bin + "/", "./cmd/lumbungd", "./cmd/lumbung"], { cwd: join(root, "apps/engine"), stdio: "inherit" })
  }
  // Samples call curl -s, which exits 0 on a 401 or 404. This shim makes an
  // HTTP error fail the sample, while the page still shows plain curl.
  const real = execFileSync("bash", ["-c", "command -v curl"], { encoding: "utf8" }).trim()
  writeFileSync(join(bin, "curl"), `#!/usr/bin/env bash\nexec ${JSON.stringify(real)} --fail-with-body "$@"\n`, { mode: 0o755 })
  return bin
}

async function api(engine: Running, method: string, path: string, body?: unknown) {
  const res = await fetch(engine.url + path, {
    method,
    headers: { authorization: `Bearer ${engine.token}`, "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`fixture ${method} ${path}: ${res.status} ${await res.text()}`)
}

async function fixture(engine: Running, mode: Page["mode"]) {
  if (mode === "fresh") return
  await api(engine, "POST", "/v1/databases", { name: "app" })
  if (mode === "empty-app") return
  await api(engine, "POST", "/v1/databases/app/batch", {
    statements: [
      { sql: "CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE, name TEXT)" },
      { sql: "INSERT INTO users (email, name) VALUES ('ana@example.com', 'Ana')" },
    ],
  })
}

function rewrite(code: string, engine: Running, lang: string): string {
  let out = code.replaceAll("http://127.0.0.1:4870", engine.url)
  if (lang === "sh" || lang === "bash") out = out.replaceAll("<token>", "$LUMBUNG_TOKEN")
  return out
}

async function runPage(page: Page, bin: string): Promise<{ ran: number; skipped: number; failures: string[] }> {
  const rel = relative(docsDir, page.file)
  const work = join(workRoot, rel.replace(/[/.]/g, "_"))
  rmSync(work, { recursive: true, force: true })
  mkdirSync(join(work, "src"), { recursive: true })
  const engine = await startEngine(join(bin, "lumbungd"))
  const failures: string[] = []
  let ran = 0
  let skipped = 0
  let baseProgram = ""
  try {
    await fixture(engine, page.mode)
    const env = {
      ...process.env,
      PATH: `${bin}:${process.env.PATH}`,
      HOME: work,
      LUMBUNG_URL: engine.url,
      LUMBUNG_TOKEN: engine.token,
      LUMBUNG_DATA_DIR: engine.dataDir,
      LUMBUNG_CONFIG: join(work, "lumbung-config.json"),
      NODE_NO_WARNINGS: "1",
    }
    for (const [i, s] of page.samples.entries()) {
      const where = `${relative(root, page.file)}:${s.line}`
      if (s.skip) {
        if (RUN.has(s.lang) || s.title) skipped++
        continue
      }
      const code = rewrite(s.code, engine, s.lang)
      if (s.title) {
        const target = join(work, s.title)
        mkdirSync(dirname(target), { recursive: true })
        writeFileSync(target, code)
        if (/\.(ts|js)$/.test(s.title)) {
          const r = spawnSync("node", ["--input-type=module", "-e", `await import(${JSON.stringify(target)})`], { cwd: work, env, encoding: "utf8", timeout: 60_000 })
          ran++
          if (r.status !== 0) failures.push(`${where} (${s.title}) doesn't load:\n${r.stderr || r.stdout}`)
        }
        continue
      }
      if (!RUN.has(s.lang)) continue
      ran++
      let r
      if (s.lang === "sh" || s.lang === "bash") {
        r = spawnSync("bash", ["-euo", "pipefail", "-c", code], { cwd: work, env, encoding: "utf8", timeout: 120_000 })
      } else if (s.lang === "ts" || s.lang === "js") {
        // A continued sample runs after the last sample that wasn't one
        // (usually the setup that makes a client), not after every sample
        // before it: those already ran.
        const program = s.cont ? baseProgram + code : code
        if (!s.cont) baseProgram = code
        const file = join(work, "src", `sample-${i}.${s.lang}`)
        writeFileSync(file, program)
        r = spawnSync("node", [file], { cwd: work, env, encoding: "utf8", timeout: 120_000 })
      } else {
        r = spawnSync(join(bin, "lumbung"), ["shell", "--db", "app"], { cwd: work, env, input: code, encoding: "utf8", timeout: 120_000 })
        if (r.status === 0 && /^error:/m.test(r.stderr)) r = { ...r, status: 1 }
      }
      if (r.status !== 0) {
        failures.push(`${where} (${s.lang}) failed with ${r.status ?? r.signal}:\n--- sample\n${code}--- output\n${(r.stdout + r.stderr).slice(-3000)}`)
        break // later samples on the page usually depend on this one
      }
    }
  } finally {
    await engine.stop()
  }
  return { ran, skipped, failures }
}

const filter = process.argv[2]
const files = pagesUnder(docsDir)
  .filter((f) => !readFileSync(f, "utf8").includes("Generated by apps/engine/internal/tools/docgen"))
  .filter((f) => !filter || f.includes(filter))
  .sort()
const bin = build()
let ran = 0
let skipped = 0
const failures: string[] = []
for (const file of files) {
  const page = parse(file)
  const runnable = page.samples.filter((s) => RUN.has(s.lang) || s.title)
  if (runnable.length === 0) continue
  const r = await runPage(page, bin)
  ran += r.ran
  skipped += r.skipped
  failures.push(...r.failures)
  console.log(`${r.failures.length ? "FAIL" : "ok  "} ${relative(docsDir, file)}  (${r.ran} run, ${r.skipped} skipped)`)
}
rmSync(workRoot, { recursive: true, force: true })
console.log(`\ndocs-test: ${ran} samples run, ${skipped} skipped with a reason, ${failures.length} failed`)
if (failures.length) {
  console.log("\n" + failures.join("\n\n"))
  process.exit(1)
}
