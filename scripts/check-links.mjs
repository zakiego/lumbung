#!/usr/bin/env node
// Checks every internal link and anchor in the built site (apps/site/dist).
// A link to /docs/x/#y needs dist/docs/x/index.html to exist and to contain
// id="y". External links aren't fetched.
//
//   node scripts/check-links.mjs [dist folder]
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs"
import { join, relative, resolve } from "node:path"

const dist = resolve(process.argv[2] ?? "apps/site/dist")
if (!existsSync(dist)) {
  console.error(`check-links: ${dist} doesn't exist; build the site first`)
  process.exit(2)
}

function walk(dir) {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? walk(p) : [p]
  })
}

const pages = walk(dist).filter((p) => p.endsWith(".html"))
const idCache = new Map()
function ids(file) {
  if (!idCache.has(file)) {
    const html = readFileSync(file, "utf8")
    idCache.set(file, new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])))
  }
  return idCache.get(file)
}

// Where a site path lands in dist, or null.
function target(path) {
  const clean = decodeURIComponent(path).replace(/\/+$/, "")
  for (const candidate of [join(dist, clean, "index.html"), join(dist, clean), join(dist, `${clean}.html`)]) {
    if (existsSync(candidate) && statSync(candidate).isFile()) return candidate
  }
  return null
}

let broken = 0
for (const page of pages) {
  const html = readFileSync(page, "utf8")
  for (const m of html.matchAll(/\s(?:href|src)="([^"]+)"/g)) {
    const link = m[1].replace(/&amp;/g, "&")
    if (/^(https?:|mailto:|data:|#$)/.test(link) || link.startsWith("//")) continue
    let [path, hash] = link.split("#")
    path = path.split("?")[0]
    let file = page
    if (path) {
      if (!path.startsWith("/")) continue // relative asset paths from the theme
      file = target(path)
      if (!file) {
        console.log(`${relative(dist, page)}: broken link ${link}`)
        broken++
        continue
      }
    }
    if (hash && file.endsWith(".html") && !ids(file).has(decodeURIComponent(hash))) {
      console.log(`${relative(dist, page)}: missing anchor ${link}`)
      broken++
    }
  }
}
console.log(`check-links: ${pages.length} pages, ${broken} broken`)
process.exit(broken ? 1 : 0)
