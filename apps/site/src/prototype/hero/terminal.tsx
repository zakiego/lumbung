// Terminal split: copy on the left, a CLI session on the right that types
// out the same branch loop a developer would run.
import { cn } from "@/lib/utils"
import { CTA, Header, Stage, Typed, headline, sentence, useLoop } from "./shared"

const session = [
  { cmd: "lumbung branch create pr-42 --db shop", out: ["Created pr-42 from main in 0.3s (46.0 MB)"] },
  { cmd: "lumbung migrate apply --db shop --branch pr-42 --dir migrations", out: ["0007_add_coupon.sql  applied"] },
  { cmd: "lumbung branch diff pr-42 --db shop", out: ["table orders  changed", "  + coupon TEXT"] },
  { cmd: "lumbung query --db shop \"SELECT count(*) FROM orders\"", out: ["count(*)", "18420   (main is untouched)"] },
]

export function Terminal() {
  const step = useLoop(session.length, 5200)
  return (
    <div className="min-h-svh">
      <Header />
      <main className="mx-auto grid max-w-6xl items-center gap-12 px-6 pt-16 pb-24 md:pt-24 lg:grid-cols-[minmax(0,5fr)_minmax(0,6fr)]">
        <div>
          <h1 className="-ml-0.5 text-[40px] leading-[1.05] font-[510] tracking-[-0.022em] text-balance md:text-[56px] md:leading-[1.05]">
            {headline}
          </h1>
          <p className="mt-6 max-w-md text-[15px] leading-relaxed text-muted-foreground md:text-[17px]">{sentence}</p>
          <div className="mt-8">
            <CTA />
          </div>
        </div>

        <Stage>
          <div className="flex items-center gap-2 border-b border-white/[0.06] px-4 py-3">
            {[0, 1, 2].map((i) => (
              <span key={i} className="size-2.5 rounded-full bg-white/10" />
            ))}
            <span className="ml-2 font-mono text-xs text-faint">~/shop</span>
          </div>
          <div className="relative min-h-[360px] p-5 font-mono text-[13px] leading-[1.7]">
            {session.map((s, i) => (
              <div
                key={s.cmd}
                className={cn(
                  "mb-3 transition-opacity duration-300",
                  i > step ? "hidden" : i < step ? "opacity-50" : "opacity-100",
                )}
              >
                <div className="flex gap-2">
                  <span className="text-faint">$</span>
                  {i === step ? <Typed key={`${step}-cmd`} text={s.cmd} className="break-all" /> : <span className="break-all">{s.cmd}</span>}
                </div>
                {/* Output fades in once the command has finished typing. */}
                <div
                  className={cn("pl-4 text-secondary-text", i === step && "animate-in fade-in fill-mode-both duration-300")}
                  style={i === step ? { animationDelay: `${s.cmd.length * 55 + 250}ms` } : undefined}
                >
                  {s.out.map((line) => (
                    <div key={line} className={line.trim().startsWith("+") ? "text-[#27a644]" : undefined}>
                      {line}
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </Stage>
      </main>
    </div>
  )
}
