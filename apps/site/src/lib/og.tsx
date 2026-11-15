// Open Graph cards, rendered at build time: satori lays the card out as SVG
// (text as paths), sharp turns it into a PNG. Satori reads WOFF but not WOFF2,
// so the static @fontsource packages supply the fonts.
import { readFile } from "node:fs/promises"
import { createRequire } from "node:module"
import satori from "satori"
import sharp from "sharp"
import { dark, markPath } from "./site"

const require = createRequire(import.meta.url)
const font = (file: string) => readFile(require.resolve(file))

let fonts: Promise<Parameters<typeof satori>[1]["fonts"]> | undefined
function loadFonts() {
  fonts ??= Promise.all([
    font("@fontsource/inter/files/inter-latin-400-normal.woff"),
    font("@fontsource/inter/files/inter-latin-500-normal.woff"),
  ]).then(([regular, medium]) => [
    { name: "Inter", data: regular, weight: 400 as const, style: "normal" as const },
    { name: "Inter", data: medium, weight: 500 as const, style: "normal" as const },
  ])
  return fonts
}

const c = dark

// About two lines at 32px in a 760px column: cut at a word, never mid-word.
export function trimLine(text: string, max = 90) {
  if (text.length <= max) return text
  const cut = text.slice(0, max + 1)
  const space = cut.lastIndexOf(" ")
  return `${(space > 0 ? cut.slice(0, space) : text.slice(0, max)).replace(/[\s,;:.]+$/, "")}…`
}

type Card = { title: string; line?: string; section?: string; host: string }

function corner(pos: Record<string, number>, sides: string[]) {
  const style: Record<string, string | number> = { position: "absolute", width: 20, height: 20, ...pos }
  for (const side of sides) style[`border${side}`] = `2px solid rgba(255,255,255,0.22)`
  return <div style={style} />
}

function card({ title, line, section, host }: Card) {
  return (
    <div
      style={{
        width: "100%",
        height: "100%",
        boxSizing: "border-box",
        display: "flex",
        flexDirection: "column",
        justifyContent: "space-between",
        padding: 72,
        background: c.background,
        color: c.foreground,
        fontFamily: "Inter",
        position: "relative",
      }}
    >
      <svg width="600" height="630" viewBox="0 0 600 630" style={{ position: "absolute", top: 0, right: 0 }}>
        <defs>
          <pattern id="dots" width="24" height="24" patternUnits="userSpaceOnUse">
            <circle cx="12" cy="12" r="1.5" fill="#ffffff" fillOpacity="0.14" />
          </pattern>
          <linearGradient id="fade" x1="0" y1="0" x2="1" y2="0">
            <stop offset="0" stopColor="#ffffff" stopOpacity="0" />
            <stop offset="0.7" stopColor="#ffffff" stopOpacity="1" />
          </linearGradient>
          <mask id="fade-mask">
            <rect width="600" height="630" fill="url(#fade)" />
          </mask>
        </defs>
        <rect width="600" height="630" fill="url(#dots)" mask="url(#fade-mask)" />
      </svg>
      {corner({ top: 32, left: 32 }, ["Top", "Left"])}
      {corner({ top: 32, right: 32 }, ["Top", "Right"])}
      {corner({ bottom: 32, left: 32 }, ["Bottom", "Left"])}
      {corner({ bottom: 32, right: 32 }, ["Bottom", "Right"])}

      <div style={{ display: "flex", alignItems: "center", gap: 16, fontSize: 32, fontWeight: 500, position: "relative" }}>
        <svg width="40" height="40" viewBox="0 0 32 32">
          <path d={markPath} fill={c.mark} />
        </svg>
        <span>Lumbung</span>
        {section ? <span style={{ color: c.faint }}>/</span> : null}
        {section ? <span style={{ color: c.muted, fontWeight: 400 }}>{section}</span> : null}
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 24, maxWidth: 980, position: "relative" }}>
        <div style={{ fontSize: 68, fontWeight: 500, lineHeight: 1.08, letterSpacing: "-0.022em", lineClamp: 3 }}>{title}</div>
        {line ? <div style={{ fontSize: 32, color: c.muted, lineHeight: 1.35, maxWidth: 760 }}>{trimLine(line)}</div> : null}
      </div>

      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", fontSize: 26, position: "relative" }}>
        {/* The hero graph in small: main, with a branch and a bucket on its orbit. */}
        <svg width="232" height="96" viewBox="0 0 232 96">
          <ellipse cx="116" cy="48" rx="102" ry="36" fill="none" stroke={c.muted} strokeWidth="3" strokeDasharray="1 10" strokeLinecap="round" />
          <circle cx="116" cy="48" r="24" fill={c.mark} />
          <circle cx="28" cy="30" r="12" fill={c.background} stroke={c.foreground} strokeWidth="3" />
          <circle cx="198" cy="69" r="12" fill={c.background} stroke={c.foreground} strokeWidth="3" />
        </svg>
        <span style={{ color: c.muted }}>{host}</span>
      </div>
    </div>
  )
}

export async function renderCard(input: Card): Promise<Uint8Array<ArrayBuffer>> {
  const svg = await satori(card(input), { width: 1200, height: 630, fonts: await loadFonts() })
  const png = await sharp(Buffer.from(svg)).png({ compressionLevel: 9 }).toBuffer()
  return new Uint8Array(png)
}
