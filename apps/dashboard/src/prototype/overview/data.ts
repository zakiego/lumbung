// Mock data for the overview prototype. Shaped like the engine's API
// responses so the winner can swap in real calls.

export const database = {
  name: "shop",
  id: "db_18h025u2ct9odj4tkd2h",
  sizeBytes: 48_213_504,
  tables: 14,
  createdAt: "2026-10-02",
}

export type BranchRow = {
  name: string
  parent?: string
  sizeBytes: number
  protected?: boolean
  default?: boolean
  expiresInDays?: number
  createdAgo: string
  writes24h: number
}

export const branches: BranchRow[] = [
  { name: "main", sizeBytes: 48_213_504, protected: true, default: true, createdAgo: "6 weeks ago", writes24h: 18_420 },
  { name: "staging", parent: "main", sizeBytes: 47_890_432, createdAgo: "9 days ago", writes24h: 2_210 },
  { name: "pr-42", parent: "main", sizeBytes: 48_340_992, expiresInDays: 6, createdAgo: "1 day ago", writes24h: 380 },
  { name: "pr-45-add-coupons", parent: "staging", sizeBytes: 47_902_720, expiresInDays: 7, createdAgo: "2 hours ago", writes24h: 41 },
]

// Requests per hour over the last 24 hours.
export const requests = Array.from({ length: 24 }, (_, i) => {
  const hour = (new Date(Date.UTC(2026, 10, 13, 20)).getUTCHours() + i) % 24
  const wave = Math.sin(((hour - 6) / 24) * Math.PI * 2)
  const reads = Math.round(1400 + wave * 900 + ((i * 37) % 11) * 40)
  const writes = Math.round(reads * 0.18 + ((i * 13) % 7) * 12)
  return { hour: `${String(hour).padStart(2, "0")}:00`, reads, writes }
})

export const totals = {
  reads24h: requests.reduce((n, r) => n + r.reads, 0),
  writes24h: requests.reduce((n, r) => n + r.writes, 0),
  p95ms: 4.2,
  errors24h: 3,
}

export const snapshots = { count: 9, latest: "2 hours ago", schedule: "Daily at 02:00" }

export const url = "https://db.example.com"

export const snippets = {
  sdk: `import { createClient } from "@lumbung/client"

const db = createClient({
  url: "${url}",
  token: process.env.LUMBUNG_TOKEN!,
  database: "shop",
})

const { rows } = await db.query("SELECT * FROM orders LIMIT 10")`,
  cli: `lumbung login --url ${url}
lumbung shell --db shop`,
  curl: `curl ${url}/v1/databases/shop/query \\
  -H "Authorization: Bearer $LUMBUNG_TOKEN" \\
  -d '{"sql": "SELECT * FROM orders LIMIT 10"}'`,
  mcp: `{
  "mcpServers": {
    "lumbung": {
      "url": "${url}/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}`,
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`
  return `${(n / 1024 ** 3).toFixed(2)} GB`
}

export const number = new Intl.NumberFormat("en-US")
