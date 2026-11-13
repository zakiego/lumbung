import { useEffect, useState, type ReactNode } from "react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

export const headline = "Run SQLite databases with branches on your own server"
export const sentence = "Copy a database in a second, try your change on the copy, and keep main safe."

export function Mark({ className = "size-5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true" fill="currentColor">
      <path d="M16 3 3 13h4v9h18v-9h4L16 3Zm-5 12h10v5H11v-5Zm-2 9h2v5H9v-5Zm12 0h2v5h-2v-5Z" />
    </svg>
  )
}

export function Header() {
  return (
    <header className="sticky top-0 z-40 border-b border-white/[0.08] bg-[#0b0b0bcc] backdrop-blur-[20px]">
      <div className="mx-auto flex h-16 max-w-6xl items-center gap-6 px-6">
        <a href="/" className="flex items-center gap-2 text-[15px] font-semibold tracking-tight">
          <Mark />
          Lumbung
        </a>
        <nav className="ml-auto flex items-center gap-1 text-[13px]">
          <a href="/docs/" className="rounded-full px-3 py-1.5 text-muted-foreground transition-colors duration-100 hover:bg-white/[0.08] hover:text-foreground">
            Docs
          </a>
          <a
            href="https://github.com/zakiego/lumbung"
            className="hidden rounded-full px-3 py-1.5 text-muted-foreground transition-colors duration-100 hover:bg-white/[0.08] hover:text-foreground sm:block"
          >
            GitHub ↗
          </a>
          <span className="mx-2 h-5 w-px bg-border" aria-hidden="true" />
          <CTA />
        </nav>
      </div>
    </header>
  )
}

export function CTA({ className }: { className?: string }) {
  return (
    <Button
      nativeButton={false}
      render={<a href="/docs/quickstart/" />}
      className={cn("h-8 rounded-full px-3 text-[13px] active:scale-[0.97]", className)}
    >
      Get started
    </Button>
  )
}

// Steps through a demo on a loop, starting at the first step on load. With
// reduced motion it shows the finished state and stays there.
export function useLoop(count: number, ms: number) {
  const [step, setStep] = useState(0)
  useEffect(() => {
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) {
      setStep(count - 1)
      return
    }
    const id = setInterval(() => setStep((s) => (s + 1) % (count + 1)), ms)
    return () => clearInterval(id)
  }, [count, ms])
  // One extra beat on the last step before the loop restarts.
  return Math.min(step, count - 1)
}

// Types a line out one character at a time, about 55ms a character.
export function Typed({ text, className }: { text: string; className?: string }) {
  const [n, setN] = useState(0)
  useEffect(() => {
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) {
      setN(text.length)
      return
    }
    setN(0)
    const id = setInterval(() => setN((v) => (v >= text.length ? v : v + 1)), 55)
    return () => clearInterval(id)
  }, [text])
  return (
    <span className={className} aria-label={text}>
      <span aria-hidden="true">{text.slice(0, n)}</span>
    </span>
  )
}

// A framed stage with a gradient hairline, corner marks and a dot field.
export function Stage({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("relative rounded-2xl p-px [background:linear-gradient(to_right_bottom,rgba(255,255,255,0.12),rgba(255,255,255,0.04))]", className)}>
      <div className="relative overflow-hidden rounded-[15px] bg-[#0b0c0d]">
        <div
          aria-hidden="true"
          className="pointer-events-none absolute inset-0 text-white/[0.06] [background-image:radial-gradient(currentColor_1px,transparent_1px)] [background-size:20px_20px] [mask-image:radial-gradient(ellipse_at_top,black,transparent_75%)]"
        />
        {children}
      </div>
      {["top-2 left-2 border-t border-l", "top-2 right-2 border-t border-r", "bottom-2 left-2 border-b border-l", "bottom-2 right-2 border-b border-r"].map((c) => (
        <span key={c} aria-hidden="true" className={cn("pointer-events-none absolute size-3 border-white/25", c)} />
      ))}
    </div>
  )
}
