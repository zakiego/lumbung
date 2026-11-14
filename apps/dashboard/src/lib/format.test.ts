import { expect, test } from "vitest"
import { formatBytes } from "./format"

test("formatBytes: each unit from bytes to petabytes", () => {
  expect(formatBytes(0)).toBe("0 B")
  expect(formatBytes(1023)).toBe("1023 B")
  expect(formatBytes(1024)).toBe("1.0 KB")
  expect(formatBytes(1536)).toBe("1.5 KB")
  expect(formatBytes(1024 ** 2)).toBe("1.0 MB")
  expect(formatBytes(1024 ** 3)).toBe("1.00 GB")
  expect(formatBytes(5 * 1024 ** 3)).toBe("5.00 GB")
  expect(formatBytes(1024 ** 4)).toBe("1.00 TB")
  expect(formatBytes(1.5 * 1024 ** 4)).toBe("1.50 TB")
  expect(formatBytes(1024 ** 5)).toBe("1 PB")
  expect(formatBytes(1.5 * 1024 ** 5)).toBe("1.5 PB")
  expect(formatBytes(7.45 * 1024 ** 5)).toBe("7.45 PB")
})

test("formatBytes: past petabytes the number is grouped, with no unit above PB", () => {
  expect(formatBytes(2048 * 1024 ** 5)).toBe("2,048 PB")
  // The largest byte limit the limits form can send.
  expect(formatBytes(8_000_000_000 * 1024 * 1024)).toBe("7.45 PB")
})
