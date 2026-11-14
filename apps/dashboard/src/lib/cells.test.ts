import { describe, expect, test } from "vitest"
import { TEXT_PREVIEW, affinity, cutText, decodeKey, encodeCell, encodeKey, encodePreview, keyId } from "./cells"

describe("encodeCell", () => {
  test("types", () => {
    expect(encodeCell(null)).toEqual({ t: "null" })
    expect(encodeCell(9007199254740993n)).toEqual({ t: "int", v: "9007199254740993" })
    expect(encodeCell(1.5)).toEqual({ t: "real", v: "1.5" })
    expect(encodeCell(2)).toEqual({ t: "real", v: "2" })
    expect(encodeCell(Number.NEGATIVE_INFINITY)).toEqual({ t: "real", v: "-Infinity" })
    expect(encodeCell("hi")).toEqual({ t: "text", v: "hi", len: 2, cut: false })
  })

  test("a 5 MB text value is cut to the preview with its full length", () => {
    const big = "x".repeat(5 * 1024 * 1024)
    const c = encodeCell(big)
    expect(c).toMatchObject({ t: "text", len: big.length, cut: true })
    expect(c.t === "text" && c.v.length).toBe(TEXT_PREVIEW)
  })

  test("a blob is its size and a short hex preview", () => {
    const bytes = new Uint8Array(40).map((_, i) => i)
    expect(encodeCell(bytes)).toEqual({ t: "blob", size: 40, hex: "000102030405060708090a0b0c0d0e0f" })
    expect(encodeCell(new Uint8Array())).toEqual({ t: "blob", size: 0, hex: "" })
  })
})

test("cutText counts characters, not UTF-16 units, and never splits a pair", () => {
  const emoji = "😀".repeat(5)
  expect(cutText(emoji, 3)).toEqual({ v: "😀😀😀", len: 5, cut: true })
  expect(cutText(emoji, 5)).toEqual({ v: emoji, len: 5, cut: false })
})

test("encodePreview trusts the engine's length", () => {
  expect(encodePreview("abc", 5_000_000n)).toEqual({ t: "text", v: "abc", len: 5_000_000, cut: true })
  expect(encodePreview("abc", null)).toEqual({ t: "text", v: "abc", len: 3, cut: false })
  expect(encodePreview(new Uint8Array([255, 1]), 2048n)).toEqual({ t: "blob", size: 2048, hex: "ff01" })
  expect(encodePreview(7n, null)).toEqual({ t: "int", v: "7" })
})

test("encodePreview without a length still knows a cut value was cut", () => {
  const long = encodePreview("x".repeat(TEXT_PREVIEW + 1), "unknown")
  expect(long).toMatchObject({ t: "text", len: null, cut: true })
  expect(long.t === "text" && long.v.length).toBe(TEXT_PREVIEW)
  expect(encodePreview("x".repeat(TEXT_PREVIEW), "unknown")).toMatchObject({ len: TEXT_PREVIEW, cut: false })
  expect(encodePreview(new Uint8Array(17), "unknown")).toMatchObject({ t: "blob", size: null })
  expect(encodePreview(new Uint8Array(16), "unknown")).toMatchObject({ t: "blob", size: 16 })
})

test("keys round-trip exactly", () => {
  for (const v of [null, -9223372036854775808n, 0.1, "a\"b", new Uint8Array([0, 255])]) {
    expect(decodeKey(encodeKey(v))).toEqual(v)
  }
  expect(keyId([encodeKey(1n), encodeKey("1")])).not.toBe(keyId([encodeKey("1"), encodeKey(1n)]))
})

test("affinity follows SQLite's rules", () => {
  expect(affinity("BIGINT")).toBe("integer")
  expect(affinity("varchar(20)")).toBe("text")
  expect(affinity("")).toBe("blob")
  expect(affinity("DOUBLE")).toBe("real")
  expect(affinity("DECIMAL(10,2)")).toBe("numeric")
})
