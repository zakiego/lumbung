// Starts a real lumbungd for tests (PLAN 9: tests run against a real engine
// binary, not mocks). Set LUMBUNG_BIN to use a prebuilt binary; otherwise it
// is built from apps/engine.
import { execFileSync, spawn, type ChildProcess } from "node:child_process"
import { mkdtempSync, readFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const engineDir = resolve(here, "../../../apps/engine")

export function engineBinary(): string {
  const prebuilt = process.env.LUMBUNG_BIN
  if (prebuilt) return prebuilt
  const out = join(mkdtempSync(join(tmpdir(), "lumbung-bin-")), "lumbungd")
  execFileSync("go", ["build", "-o", out, "./cmd/lumbungd"], { cwd: engineDir, stdio: "inherit" })
  return out
}

export interface Running {
  url: string
  token: string
  dataDir: string
  stop: () => Promise<void>
}

export async function startEngine(bin: string, env: Record<string, string> = {}): Promise<Running> {
  const dataDir = mkdtempSync(join(tmpdir(), "lumbung-data-"))
  const child: ChildProcess = spawn(bin, [], {
    env: { ...process.env, LUMBUNG_DATA_DIR: dataDir, LUMBUNG_ADDR: "127.0.0.1:0", LUMBUNG_MIN_FREE_DISK: "1", ...env },
    stdio: ["ignore", "ignore", "pipe"],
  })
  const url = await new Promise<string>((resolveURL, reject) => {
    let buf = ""
    const timer = setTimeout(() => reject(new Error(`lumbungd didn't start:\n${buf}`)), 30_000)
    child.stderr!.on("data", (chunk: Buffer) => {
      buf += chunk.toString()
      for (const line of buf.split("\n")) {
        if (!line.includes("lumbungd listening")) continue
        try {
          const addr = JSON.parse(line).addr as string
          clearTimeout(timer)
          resolveURL(`http://${addr}`)
          return
        } catch {
          // partial line
        }
      }
    })
    child.on("exit", (code) => reject(new Error(`lumbungd exited with ${code}:\n${buf}`)))
  })
  const token = readFileSync(join(dataDir, "secrets", "service-token"), "utf8").trim()
  return {
    url,
    token,
    dataDir,
    stop: () =>
      new Promise<void>((done) => {
        child.once("exit", () => {
          rmSync(dataDir, { recursive: true, force: true })
          done()
        })
        child.kill("SIGTERM")
      }),
  }
}
