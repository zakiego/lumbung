// The favicon set, drawn from the granary mark in public/favicon.svg at build
// time: favicon.ico (32 and 16px), apple-touch-icon.png and the manifest's
// icons. Each is the mark on the page's dark ground, since these can't follow
// the color scheme the way the SVG does.
import type { APIRoute, GetStaticPaths } from "astro"
import sharp from "sharp"
import { dark, markPath } from "@/lib/site"

// size: the icon's edge. inset: how much of it the mark fills. radius: corner
// rounding; 0 where the platform rounds the icon itself.
const icons = {
  "favicon.ico": { sizes: [32, 16], inset: 0.8, radius: 0.2 },
  "apple-touch-icon.png": { sizes: [180], inset: 0.6, radius: 0 },
  "icon-192.png": { sizes: [192], inset: 0.6, radius: 0 },
  "icon-512.png": { sizes: [512], inset: 0.5, radius: 0 },
} as const

type Name = keyof typeof icons

function svg(size: number, inset: number, radius: number) {
  const mark = size * inset
  const offset = (size - mark) / 2
  return Buffer.from(
    `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${size} ${size}">` +
      `<rect width="${size}" height="${size}" rx="${size * radius}" fill="${dark.background}"/>` +
      `<g transform="translate(${offset} ${offset}) scale(${mark / 32})"><path d="${markPath}" fill="${dark.mark}"/></g>` +
      `</svg>`,
  )
}

const png = (size: number, inset: number, radius: number) => sharp(svg(size, inset, radius)).png().toBuffer()

// An ICO file can hold PNG images as they are: a 6-byte header, a 16-byte
// entry per image, then the images.
function ico(images: { size: number; data: Buffer }[]) {
  const header = Buffer.alloc(6 + 16 * images.length)
  header.writeUInt16LE(0, 0)
  header.writeUInt16LE(1, 2)
  header.writeUInt16LE(images.length, 4)
  let offset = header.length
  images.forEach(({ size, data }, i) => {
    const at = 6 + 16 * i
    header.writeUInt8(size, at)
    header.writeUInt8(size, at + 1)
    header.writeUInt16LE(1, at + 4)
    header.writeUInt16LE(32, at + 6)
    header.writeUInt32LE(data.length, at + 8)
    header.writeUInt32LE(offset, at + 12)
    offset += data.length
  })
  return Buffer.concat([header, ...images.map((image) => image.data)])
}

export const getStaticPaths: GetStaticPaths = () => Object.keys(icons).map((icon) => ({ params: { icon } }))

export const GET: APIRoute = async ({ params }) => {
  const name = params.icon as Name
  const { sizes, inset, radius } = icons[name]
  if (name === "favicon.ico") {
    const images = await Promise.all(sizes.map(async (size) => ({ size, data: await png(size, inset, radius) })))
    return new Response(new Uint8Array(ico(images)), { headers: { "Content-Type": "image/x-icon" } })
  }
  return new Response(new Uint8Array(await png(sizes[0], inset, radius)), { headers: { "Content-Type": "image/png" } })
}
