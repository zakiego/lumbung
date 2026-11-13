// The prototype picker: harness chrome, copied from the prototype skill's
// PICKER.md spec. Its look is fixed and doesn't follow the product's tokens.
import { useEffect, useLayoutEffect, useRef, useState, type ComponentType } from "react"

const css = `
.proto-picker{position:fixed;bottom:24px;left:50%;transform:translateX(-50%);z-index:2147483647;display:flex;align-items:center;gap:2px;padding:4px;border-radius:999px;background:rgba(10,10,10,.82);-webkit-backdrop-filter:blur(12px) saturate(1.4);backdrop-filter:blur(12px) saturate(1.4);box-shadow:0 0 0 1px rgba(255,255,255,.08) inset,0 8px 24px rgba(0,0,0,.24),0 2px 6px rgba(0,0,0,.12);font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;font-size:13px;line-height:1;-webkit-font-smoothing:antialiased;user-select:none;-webkit-user-select:none}
.proto-picker-highlight{position:absolute;top:4px;left:0;height:28px;border-radius:999px;background:rgba(255,255,255,.12)}
.proto-picker[data-ready] .proto-picker-highlight{transition:transform 250ms cubic-bezier(.23,1,.32,1),width 250ms cubic-bezier(.23,1,.32,1)}
@media (prefers-reduced-motion:reduce){.proto-picker[data-ready] .proto-picker-highlight{transition:none}}
.proto-picker-item{position:relative;display:flex;align-items:center;height:28px;padding:0 12px;border:0;border-radius:999px;background:transparent;color:rgba(255,255,255,.55);font:inherit;cursor:pointer;transition:color 150ms ease-out,transform 160ms cubic-bezier(.23,1,.32,1)}
@media (hover:hover) and (pointer:fine){.proto-picker-item:hover{color:rgba(255,255,255,.85)}}
.proto-picker-item:active{transform:scale(.97)}
.proto-picker-item:focus-visible{outline:2px solid rgba(255,255,255,.4);outline-offset:2px}
.proto-picker-item[data-active]{color:#fff}
.proto-picker-divider{width:1px;height:16px;margin:0 4px;background:rgba(255,255,255,.12)}
.proto-picker-replay{padding:0 10px;font-size:14px}
.proto-picker[data-position="top"]{bottom:auto;top:24px}
`

export type Variant = { name: string; component: ComponentType }

export function Picker({ variants, replay = false }: { variants: Variant[]; replay?: boolean }) {
  const [current, setCurrent] = useState(0)
  const [mountKey, setMountKey] = useState(0)
  const [ready, setReady] = useState(false)
  const navRef = useRef<HTMLElement>(null)
  const highlightRef = useRef<HTMLSpanElement>(null)
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([])

  // Read ?v= after mount, so the server render and hydration agree.
  useEffect(() => {
    const v = Number.parseInt(new URLSearchParams(location.search).get("v") ?? "", 10)
    if (v >= 1 && v <= variants.length) setCurrent(v - 1)
    requestAnimationFrame(() => requestAnimationFrame(() => setReady(true)))
  }, [variants.length])

  useLayoutEffect(() => {
    const move = () => {
      const el = itemRefs.current[current]
      const hl = highlightRef.current
      if (!el || !hl) return
      hl.style.width = `${el.offsetWidth}px`
      hl.style.transform = `translateX(${el.offsetLeft}px)`
    }
    move()
    window.addEventListener("resize", move)
    return () => window.removeEventListener("resize", move)
  }, [current])

  const select = (i: number) => {
    if (i < 0 || i >= variants.length) return
    setCurrent(i)
    setMountKey((k) => k + 1)
    const url = new URL(location.href)
    url.searchParams.set("v", String(i + 1))
    history.replaceState(history.state, "", url)
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable) return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const n = Number.parseInt(e.key, 10)
      if (n >= 1 && n <= variants.length) select(n - 1)
      else if (e.key === "ArrowRight") select((current + 1) % variants.length)
      else if (e.key === "ArrowLeft") select((current - 1 + variants.length) % variants.length)
      else if (replay && (e.key === "r" || e.key === "R")) setMountKey((k) => k + 1)
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  })

  const Current = variants[current]!.component
  return (
    <>
      <style>{css}</style>
      <Current key={mountKey} />
      <nav ref={navRef} className="proto-picker" aria-label="Prototype variants" data-ready={ready ? "" : undefined}>
        <span ref={highlightRef} className="proto-picker-highlight" aria-hidden="true" />
        {variants.map((v, i) => (
          <button
            key={v.name}
            ref={(el) => {
              itemRefs.current[i] = el
            }}
            type="button"
            className="proto-picker-item"
            data-active={i === current ? "" : undefined}
            aria-current={i === current ? "true" : undefined}
            onClick={() => select(i)}
          >
            {v.name}
          </button>
        ))}
        {replay && (
          <>
            <span className="proto-picker-divider" aria-hidden="true" />
            <button
              type="button"
              className="proto-picker-item proto-picker-replay"
              aria-label="Replay animation (R)"
              onClick={() => setMountKey((k) => k + 1)}
            >
              ↻
            </button>
          </>
        )}
      </nav>
    </>
  )
}
