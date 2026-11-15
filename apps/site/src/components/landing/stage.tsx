import { Toggle, ToggleIcon } from "@ui-sdk/react/toggle"
import { PauseIcon, PlayIcon } from "lucide-react"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"

const corners = [
  "-top-2.5 -left-2.5 border-t border-l",
  "-top-2.5 -right-2.5 border-t border-r",
  "-bottom-2.5 -left-2.5 border-b border-l",
  "-bottom-2.5 -right-2.5 border-b border-r",
]

// The frame around a looping graph. The loop is CSS only, so it runs before
// hydration. This adds the pause control and holds the loop still while it is
// scrolled away or in a background tab.
export function Stage({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  // False on the server and with reduced motion, where there is no loop to pause.
  const [moving, setMoving] = useState(false)
  const [paused, setPaused] = useState(false)
  const [onScreen, setOnScreen] = useState(true)
  const root = useRef<HTMLElement>(null)

  useEffect(() => {
    const query = matchMedia("(prefers-reduced-motion: reduce)")
    const apply = () => setMoving(!query.matches)
    apply()
    query.addEventListener("change", apply)
    return () => query.removeEventListener("change", apply)
  }, [])

  useEffect(() => {
    const el = root.current
    if (!el) return
    let inView = true
    const update = () => setOnScreen(inView && document.visibilityState === "visible")
    const observer = new IntersectionObserver((entries) => {
      inView = entries.at(-1)?.isIntersecting ?? inView
      update()
    })
    observer.observe(el)
    update()
    document.addEventListener("visibilitychange", update)
    return () => {
      observer.disconnect()
      document.removeEventListener("visibilitychange", update)
    }
  }, [])

  return (
    <figure ref={root} className={cn("relative rounded-2xl border border-border bg-surface", className)}>
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 rounded-2xl text-foreground/[0.07] [background-image:radial-gradient(currentColor_1px,transparent_1px)] [background-size:20px_20px] [mask-image:radial-gradient(ellipse_at_top_right,black,transparent_70%)]"
      />
      <div data-stage data-paused={paused || !onScreen} className="relative">
        {children}
      </div>
      {moving ? (
        <Toggle
          aria-label="Pause animation"
          pressed={paused}
          onPressedChange={setPaused}
          className="press absolute top-3 right-3 grid size-7 place-items-center rounded-full text-muted-foreground transition-[background-color,color,opacity,scale] duration-150 ease-(--ease-out-cubic) after:absolute after:-inset-2 starting:opacity-0 hover:bg-hover hover:text-foreground data-pressed:text-foreground"
        >
          <ToggleIcon className="size-3.5 [&_svg]:size-3.5">{paused ? <PlayIcon /> : <PauseIcon />}</ToggleIcon>
        </Toggle>
      ) : null}
      {corners.map((c) => (
        <span key={c} aria-hidden="true" className={cn("pointer-events-none absolute size-3 border-faint", c)} />
      ))}
      <figcaption className="sr-only">{label}</figcaption>
    </figure>
  )
}
