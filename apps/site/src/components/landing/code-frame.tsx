import { Button } from "@ui-sdk/react/button"
import {
  ScrollArea,
  ScrollAreaContent,
  ScrollAreaScrollbar,
  ScrollAreaThumb,
  ScrollAreaViewport,
} from "@ui-sdk/react/scroll-area"
import { CheckIcon, CopyIcon } from "lucide-react"
import { useEffect, useRef, useState, type ReactNode } from "react"

// A minimal code block: the highlighted code comes in as children (rendered by
// Astro's <Code>), scrolls sideways with an overlay scrollbar, and has a round
// copy button that shows on hover.
export function CodeFrame({ code, label, copyLabel, children }: { code: string; label: string; copyLabel: string; children?: ReactNode }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle")
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const frame = useRef<HTMLDivElement>(null)
  useEffect(() => () => clearTimeout(timer.current), [])

  async function copy() {
    clearTimeout(timer.current)
    try {
      if (!navigator.clipboard) throw new Error("No clipboard")
      await navigator.clipboard.writeText(code)
      setState("copied")
    } catch {
      selectCode(frame.current)
      setState("failed")
    }
    timer.current = setTimeout(() => setState("idle"), 2000)
  }

  return (
    <div ref={frame} className="group/code relative rounded-lg border border-hairline bg-code">
      <ScrollArea type="hover" className="relative overflow-hidden">
        <ScrollAreaViewport aria-label={label} className="overscroll-x-contain [scrollbar-width:none]">
          <ScrollAreaContent className="w-max min-w-full py-4 pr-14 pl-4 font-mono text-[13px] leading-[1.6] [&_pre]:!m-0 [&_pre]:!p-0">
            {children}
          </ScrollAreaContent>
        </ScrollAreaViewport>
        <ScrollAreaScrollbar
          orientation="horizontal"
          className="absolute right-1 bottom-0.5 left-1 flex h-2 touch-none flex-col p-0.5 opacity-0 transition-opacity duration-150 select-none data-visible:opacity-100"
        >
          <ScrollAreaThumb className="flex-1 rounded-full bg-foreground/25" />
        </ScrollAreaScrollbar>
      </ScrollArea>
      <Button
        aria-label={copyLabel}
        onClick={copy}
        className="press absolute top-2 right-2 grid size-8 place-items-center rounded-full border border-hairline bg-background text-muted-foreground opacity-0 transition-[opacity,color,scale] duration-150 ease-(--ease-out-quad) group-hover/code:opacity-100 hover:text-foreground focus-visible:opacity-100 data-[state=copied]:opacity-100 [@media(hover:none)]:opacity-100"
        data-state={state}
      >
        {state === "copied" ? (
          <CheckIcon aria-hidden="true" className="size-4 text-success motion-safe:animate-[pop_250ms_var(--ease-out-cubic)]" />
        ) : (
          <CopyIcon aria-hidden="true" className="size-4" />
        )}
      </Button>
      <span className="sr-only" aria-live="polite">
        {state === "copied" ? "Copied" : state === "failed" ? "Couldn't copy. The code is selected." : ""}
      </span>
    </div>
  )
}

// Selects the code so it can be copied by hand when the clipboard is blocked.
function selectCode(frame: HTMLElement | null) {
  const code = frame?.querySelector("pre")
  const selection = window.getSelection()
  if (!code || !selection) return
  const range = document.createRange()
  range.selectNodeContents(code)
  selection.removeAllRanges()
  selection.addRange(range)
}
