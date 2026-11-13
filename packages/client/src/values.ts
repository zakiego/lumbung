import type { WireParam, WireValue } from "./generated.ts"

/** How integers come back: `number` (the default; throws past 2^53), `bigint`, or `string`. */
export type IntMode = "number" | "bigint" | "string"

/** A decoded SQLite value. */
export type Value = null | number | bigint | string | Uint8Array

/** What you can pass as a parameter. */
export type InValue = null | undefined | boolean | number | bigint | string | Uint8Array | ArrayBuffer | Date

function toBase64(bytes: Uint8Array): string {
  let bin = ""
  for (let i = 0; i < bytes.length; i += 0x8000) {
    bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  }
  return btoa(bin)
}

function fromBase64(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

/**
 * Encodes a parameter. Integers always travel as decimal strings, so a
 * bigint keeps every bit; a JS number with a fraction is a float.
 */
export function encodeValue(v: InValue): WireParam {
  if (v === null || v === undefined) return { type: "null" }
  switch (typeof v) {
    case "boolean":
      return { type: "integer", value: v ? "1" : "0" }
    case "bigint":
      if (v > 9223372036854775807n || v < -9223372036854775808n) {
        throw new RangeError(`${v} doesn't fit in a 64-bit integer`)
      }
      return { type: "integer", value: v.toString() }
    case "number":
      if (Number.isInteger(v)) {
        if (!Number.isSafeInteger(v)) {
          throw new RangeError(`${v} isn't a safe integer; pass a bigint to keep its exact value`)
        }
        return { type: "integer", value: String(v) }
      }
      if (Number.isNaN(v)) throw new RangeError("NaN can't be stored; SQLite turns it into NULL")
      if (v === Infinity) return { type: "float", value: "Infinity" }
      if (v === -Infinity) return { type: "float", value: "-Infinity" }
      return { type: "float", value: v }
    case "string":
      return { type: "text", value: v }
  }
  if (v instanceof Uint8Array) return { type: "blob", base64: toBase64(v) }
  if (v instanceof ArrayBuffer) return { type: "blob", base64: toBase64(new Uint8Array(v)) }
  if (v instanceof Date) return { type: "text", value: v.toISOString() }
  throw new TypeError(`can't send ${Object.prototype.toString.call(v)} as a SQLite value`)
}

/** Decodes a value from the API. */
export function decodeValue(w: WireValue, intMode: IntMode): Value {
  switch (w.type) {
    case "null":
      return null
    case "integer": {
      if (intMode === "string") return w.value
      const big = BigInt(w.value)
      if (intMode === "bigint") return big
      if (big > BigInt(Number.MAX_SAFE_INTEGER) || big < BigInt(Number.MIN_SAFE_INTEGER)) {
        throw new RangeError(
          `integer ${w.value} can't be a JS number without losing precision; use intMode "bigint" or "string"`,
        )
      }
      return Number(big)
    }
    case "float":
      if (w.value === "Infinity") return Infinity
      if (w.value === "-Infinity") return -Infinity
      return w.value
    case "text":
      return w.value
    case "blob":
      return fromBase64(w.base64)
  }
}
