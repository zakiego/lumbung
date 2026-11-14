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
// A controllable IntersectionObserver: tests call observers[i].fire(false) to
// scroll a component away and read .disconnected after unmount.
export class FakeIntersectionObserver {
  static instances: FakeIntersectionObserver[] = []
  disconnected = false
  constructor(private callback: IntersectionObserverCallback) {
    FakeIntersectionObserver.instances.push(this)
  }
  fire(isIntersecting: boolean) {
    this.callback([{ isIntersecting } as IntersectionObserverEntry], this as unknown as IntersectionObserver)
  }
  observe() {}
  unobserve() {}
  disconnect() {
    this.disconnected = true
  }
  takeRecords() {
    return []
  }
}
globalThis.IntersectionObserver = FakeIntersectionObserver as unknown as typeof IntersectionObserver
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
}
Element.prototype.getAnimations = () => []

afterEach(() => {
  cleanup()
  globalThis.__reducedMotion = false
  FakeIntersectionObserver.instances = []
})
