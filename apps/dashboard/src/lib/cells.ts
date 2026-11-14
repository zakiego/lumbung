// How SQLite values travel from the dashboard's server to the browser. Lists
// carry previews: long text is cut and blobs are only a size and a few bytes,
// so a 5 MB value never reaches a page of rows.
import type { Value } from "@lumbung/client"

export const TEXT_PREVIEW = 1000
export const BLOB_PREVIEW = 16

export type Cell =
  | { t: "null" }
  | { t: "int"; v: string }
  | { t: "real"; v: string }
  /** `len` is the full length in characters; the value was cut when it's longer than `v`. */
  | { t: "text"; v: string; len: number; cut: boolean }
  | { t: "blob"; size: number; hex: string }

/** A value that identifies a row, sent back exactly as it was read. */
export type KeyValue = { t: "null" } | { t: "int"; v: string } | { t: "real"; v: string } | { t: "text"; v: string } | { t: "blob"; b64: string }

export type RowKey = KeyValue[]

function hexOf(bytes: Uint8Array, max = BLOB_PREVIEW): string {
  let out = ""
  for (let i = 0; i < Math.min(bytes.length, max); i++) out += bytes[i]!.toString(16).padStart(2, "0")
  return out
}

/** The first `max` characters (code points) of s and how many s has in all. */
export function cutText(s: string, max = TEXT_PREVIEW): { v: string; len: number; cut: boolean } {
  let count = 0
  let end = s.length
  for (let i = 0; i < s.length; i++) {
    if (count === max && end === s.length) end = i
    const code = s.charCodeAt(i)
    if (code >= 0xd800 && code <= 0xdbff && i + 1 < s.length) i++
    count++
  }
  if (count <= max) return { v: s, len: count, cut: false }
  return { v: s.slice(0, end), len: count, cut: true }
}

export function realText(n: number): string {
  if (n === Number.POSITIVE_INFINITY) return "Infinity"
  if (n === Number.NEGATIVE_INFINITY) return "-Infinity"
  return String(n)
}

/** Encodes a full value for a list. */
export function encodeCell(v: Value): Cell {
  if (v === null) return { t: "null" }
  if (typeof v === "bigint") return { t: "int", v: v.toString() }
  if (typeof v === "number") return { t: "real", v: realText(v) }
  if (typeof v === "string") return { t: "text", ...cutText(v) }
  return { t: "blob", size: v.byteLength, hex: hexOf(v) }
}

/**
 * Encodes a value the engine already cut down (grid.ts selects a prefix of
 * long text and blobs), with the full length it reported, or null when the
 * value wasn't cut.
 */
export function encodePreview(v: Value, fullLength: Value): Cell {
  const full = typeof fullLength === "bigint" ? Number(fullLength) : typeof fullLength === "number" ? fullLength : null
  if (typeof v === "string") {
    if (full === null) return { t: "text", ...cutText(v) }
    return { t: "text", v, len: full, cut: true }
  }
  if (v instanceof Uint8Array) return { t: "blob", size: full ?? v.byteLength, hex: hexOf(v) }
  return encodeCell(v)
}

function toBase64(bytes: Uint8Array): string {
  let bin = ""
  for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  return btoa(bin)
}

function fromBase64(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export function encodeKey(v: Value): KeyValue {
  if (v === null) return { t: "null" }
  if (typeof v === "bigint") return { t: "int", v: v.toString() }
  if (typeof v === "number") return { t: "real", v: realText(v) }
  if (typeof v === "string") return { t: "text", v }
  return { t: "blob", b64: toBase64(v) }
}

export function decodeKey(k: KeyValue): Value {
  switch (k.t) {
    case "null":
      return null
    case "int":
      return BigInt(k.v)
    case "real":
      return Number(k.v)
    case "text":
      return k.v
    case "blob":
      return fromBase64(k.b64)
  }
}

/** A stable string for a row key, for React keys and selection. */
export function keyId(key: RowKey): string {
  return JSON.stringify(key.map((k) => (k.t === "null" ? null : k.t === "blob" ? `b:${k.b64}` : `${k.t[0]}:${k.v}`)))
}

/** The text an editor starts with. Blobs aren't editable, so they have none. */
export function cellText(c: Cell): string | null {
  switch (c.t) {
    case "null":
    case "blob":
      return null
    default:
      return c.v
  }
}

export function isNumeric(c: Cell): boolean {
  return c.t === "int" || c.t === "real"
}

/** SQLite's affinity rules for a declared type (https://sqlite.org/datatype3.html#determination_of_column_affinity). */
export function affinity(declared: string): "integer" | "text" | "blob" | "real" | "numeric" {
  const t = declared.toUpperCase()
  if (t.includes("INT")) return "integer"
  if (t.includes("CHAR") || t.includes("CLOB") || t.includes("TEXT")) return "text"
  if (t === "" || t.includes("BLOB")) return "blob"
  if (t.includes("REAL") || t.includes("FLOA") || t.includes("DOUB")) return "real"
  return "numeric"
}
