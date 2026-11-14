import { cleanup } from "@testing-library/react"
import { afterEach } from "vitest"

declare global {
  var __UI_SDK_STRICT__: boolean | undefined
  var __reducedMotion: boolean
}
globalThis.__UI_SDK_STRICT__ = true
globalThis.__reducedMotion = false

// jsdom has neither. Tests flip __reducedMotion before rendering.
window.matchMedia = (query: string) =>
  ({
    matches: query.includes("reduce") ? globalThis.__reducedMotion : false,
    media: query,
    addEventListener() {},
    removeEventListener() {},
  }) as unknown as MediaQueryList
globalThis.IntersectionObserver = class {
  observe() {}
  disconnect() {}
  unobserve() {}
  takeRecords() {
    return []
  }
} as unknown as typeof IntersectionObserver
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
}
Element.prototype.getAnimations = () => []

afterEach(() => {
  cleanup()
  globalThis.__reducedMotion = false
})
