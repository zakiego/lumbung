import { Toggle, ToggleIcon } from "@ui-sdk/react/toggle"
import { PauseIcon, PlayIcon } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"

type Line = { text: string; tone?: "add" | "dim" }
export type Step = { cmd: string; out: Line[] }

// The branch loop from the docs, with the CLI's real output. The profile saved
// by `lumbung login --db shop` supplies the database, so no command needs --db.
export const session: Step[] = [
  { cmd: "lumbung branch create pr-42", out: [{ text: "Created branch pr-42 (br_k3x9q2m7v4p8w1z6c5n0)" }] },
  { cmd: "lumbung migrate apply --branch pr-42", out: [{ text: "applied 0007_orders_by_email.sql" }] },
  {
    cmd: "lumbung branch diff pr-42",
    out: [
      { text: "+ index orders_by_email", tone: "add" },
      { text: "    + CREATE INDEX orders_by_email ON orders(email)", tone: "add" },
    ],
  },
  {
    cmd: 'lumbung query "SELECT count(*) FROM orders" --branch pr-42',
    out: [{ text: "count(*)", tone: "dim" }, { text: "18420" }],
  },
  { cmd: "lumbung branch delete pr-42 --yes", out: [{ text: "Done: delete pr-42" }] },
  { cmd: "clear", out: [] },
]

const CREATE = 0
const QUERY = 3
const DELETE = 4
const CLEAR = session.length - 1

export const TYPE_MS = 55
const RUN_MS = 250

// How long a finished step stays before the next one starts: enough to read it.
const holdMs = (step: number) => (step === CLEAR ? 600 : 1100 + 500 * session[step].out.length)

// null is the poster: the session finished up to the query, which is what
// shows without JS, with reduced motion, and before hydration.
export type Frame = { step: number; typed: number; ran: boolean } | null

export function next(frame: NonNullable<Frame>): { frame: NonNullable<Frame>; delay: number } {
  const { step, typed, ran } = frame
  if (typed < session[step].cmd.length) return { frame: { step, typed: typed + 1, ran }, delay: TYPE_MS }
  if (!ran) return { frame: { step, typed, ran: true }, delay: RUN_MS }
  return { frame: { step: step === CLEAR ? 0 : step + 1, typed: 0, ran: false }, delay: holdMs(step) }
}

const reducedMotion = () => matchMedia("(prefers-reduced-motion: reduce)")

export function Terminal() {
  const [frame, setFrame] = useState<Frame>(null)
  const [paused, setPaused] = useState(false)
  const [onScreen, setOnScreen] = useState(true)
  const root = useRef<HTMLElement>(null)

  // Start at the first step, unless motion is reduced; follow the setting live.
  useEffect(() => {
    const query = reducedMotion()
    const apply = () => setFrame(query.matches ? null : { step: 0, typed: 0, ran: false })
    apply()
    query.addEventListener("change", apply)
    return () => query.removeEventListener("change", apply)
  }, [])

  // Hold still while scrolled away or in a background tab.
  useEffect(() => {
    const el = root.current
    if (!el) return
    let inView = true
    const update = () => setOnScreen(inView && document.visibilityState === "visible")
    const observer = new IntersectionObserver(([entry]) => {
      inView = entry.isIntersecting
      update()
    })
    observer.observe(el)
    document.addEventListener("visibilitychange", update)
    return () => {
      observer.disconnect()
      document.removeEventListener("visibilitychange", update)
    }
  }, [])

  useEffect(() => {
    if (!frame || paused || !onScreen) return
    const { frame: following, delay } = next(frame)
    const id = setTimeout(() => setFrame(following), delay)
    return () => clearTimeout(id)
  }, [frame, paused, onScreen])

  const cleared = frame?.step === CLEAR && frame.ran
  const branchOpen =
    frame === null || ((frame.step > CREATE || frame.ran) && !(frame.step > DELETE || (frame.step === DELETE && frame.ran)))

  return (
    <figure
      ref={root}
      data-terminal={frame ? "live" : "poster"}
      className="scheme-dark relative rounded-2xl p-px [background:linear-gradient(to_right_bottom,rgba(255,255,255,0.14),rgba(255,255,255,0.04))] shadow-[0_12px_40px_rgba(0,0,0,0.16)] dark:shadow-[0_2px_32px_#08090a]"
    >
      <div className="relative overflow-hidden rounded-[15px] bg-[#0b0c0d] text-foreground">
        <div
          aria-hidden="true"
          className="pointer-events-none absolute inset-0 text-white/[0.07] [background-image:radial-gradient(currentColor_1px,transparent_1px)] [background-size:20px_20px] [mask-image:radial-gradient(ellipse_at_top_right,black,transparent_70%)]"
        />

        <div className="relative flex h-11 items-center gap-2 border-b border-white/[0.06] pr-2 pl-4">
          {["a", "b", "c"].map((dot) => (
            <span key={dot} aria-hidden="true" className="size-2.5 rounded-full bg-white/10" />
          ))}
          <span className="ml-2 font-mono text-xs text-muted-foreground">~/shop</span>
          {frame !== null || paused ? (
            <Toggle
              aria-label="Pause demo"
              pressed={paused}
              onPressedChange={setPaused}
              className="press ml-auto grid size-7 place-items-center rounded-full text-muted-foreground transition-[background-color,color,opacity,scale] duration-150 ease-(--ease-out-cubic) starting:opacity-0 hover:bg-hover hover:text-foreground data-pressed:text-foreground"
            >
              <ToggleIcon className="size-3.5 [&_svg]:size-3.5">{paused ? <PlayIcon /> : <PauseIcon />}</ToggleIcon>
            </Toggle>
          ) : null}
        </div>

        {/* Lines fade in as they arrive and out on clear (opacity with
            visibility, so hidden lines keep their height). */}
        <div className="relative px-4 py-4 font-mono text-[12px] leading-[1.7] sm:px-5 sm:text-[13px]">
          <div data-screen aria-hidden="true">
            {session.map((s, i) => {
              const current = frame !== null && frame.step === i ? frame : null
              const done = frame === null ? i <= QUERY : i < frame.step
              const hidden = cleared || (frame === null ? i > QUERY : i > frame.step)
              const showOut = done || current?.ran
              return (
                <div
                  key={s.cmd}
                  className={cn(
                    "mb-2.5 transition-[opacity,visibility] duration-150 ease-(--ease-out-cubic) last:mb-0",
                    hidden && "invisible opacity-0 duration-150",
                  )}
                >
                  <div className="flex gap-2">
                    <span className="text-faint">$</span>
                    {/* The full command sits invisible under the typed part, so a
                        line that wraps on a phone holds its height while typing. */}
                    <span className="grid min-w-0 [overflow-wrap:anywhere]">
                      <span className="invisible col-start-1 row-start-1">{s.cmd}</span>
                      <span
                        data-typing={current ? "" : undefined}
                        className={cn("col-start-1 row-start-1 transition-colors duration-300 ease-(--ease-out-cubic)", !current && "text-secondary-text")}
                      >
                        {current ? s.cmd.slice(0, current.typed) : s.cmd}
                      </span>
                    </span>
                  </div>
                  {s.out.length > 0 && (
                    <div
                      className={cn(
                        "pl-4 whitespace-pre-wrap [overflow-wrap:anywhere] text-secondary-text transition-[opacity,visibility] duration-150 ease-(--ease-out-cubic)",
                        !showOut && "invisible opacity-0",
                      )}
                    >
                      {s.out.map((line) => (
                        <div key={line.text} className={cn(line.tone === "add" && "text-success", line.tone === "dim" && "text-muted-foreground")}>
                          {line.text}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
          <div
            aria-hidden="true"
            className={cn(
              "absolute inset-x-4 top-4 flex gap-2 transition-[opacity,visibility] duration-150 ease-(--ease-out-cubic) sm:inset-x-5",
              !cleared && "invisible opacity-0",
            )}
          >
            <span className="text-faint">$</span>
          </div>
        </div>

        <div className="relative flex h-9 items-center gap-3 border-t border-white/[0.06] px-4 font-mono text-[11px] text-muted-foreground sm:px-5">
          <span className="flex items-center gap-2">
            <span aria-hidden="true" className="size-1.5 rounded-full bg-success motion-safe:animate-[pulse-dot_2.4s_ease-in-out_infinite]" />
            lumbungd
          </span>
          <span className="ml-auto flex items-center gap-1.5" aria-hidden="true">
            <span className="rounded-full border border-white/10 px-2 py-0.5 text-secondary-text">main</span>
            <span
              data-open={branchOpen}
              className="rounded-full border border-white/10 px-2 py-0.5 text-secondary-text [will-change:opacity,scale] transition-[opacity,scale] duration-200 ease-(--ease-out-cubic) data-[open=false]:scale-[0.94] data-[open=false]:opacity-0 data-[open=false]:duration-150 data-[open=true]:delay-120"
            >
              pr-42
            </span>
          </span>
        </div>
      </div>

      {["-top-2.5 -left-2.5 border-t border-l", "-top-2.5 -right-2.5 border-t border-r", "-bottom-2.5 -left-2.5 border-b border-l", "-bottom-2.5 -right-2.5 border-b border-r"].map((c) => (
        <span key={c} aria-hidden="true" className={cn("pointer-events-none absolute size-3 border-faint", c)} />
      ))}

      <figcaption className="sr-only">
        A terminal session: create the branch pr-42 of the shop database, apply a migration to it, diff it against main,
        query it, then delete it.
      </figcaption>
    </figure>
  )
}
