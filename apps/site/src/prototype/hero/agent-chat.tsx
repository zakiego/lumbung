// Agent chat: centered copy over an AI agent using Lumbung through MCP. It
// tests a schema change on a branch before touching main.
import { CheckIcon, GitBranchIcon, GitCompareIcon, PlayIcon, SparklesIcon } from "lucide-react"
import { cn } from "@/lib/utils"
import { CTA, Header, Stage, headline, sentence, useLoop } from "./shared"

const tools = [
  { icon: GitBranchIcon, name: "create_branch", detail: "pr-45 from main · 0.3s" },
  { icon: PlayIcon, name: "execute", detail: "ALTER TABLE orders ADD coupon TEXT" },
  { icon: GitCompareIcon, name: "diff_branches", detail: "orders: + coupon TEXT" },
]

export function AgentChat() {
  // 0 user, 1-3 tool calls, 4 reply
  const step = useLoop(5, 1500)
  return (
    <div className="min-h-svh">
      <Header />
      <main className="mx-auto flex max-w-6xl flex-col items-center px-6 pt-20 pb-24 text-center md:pt-28">
        <h1 className="max-w-3xl text-[40px] leading-[1.05] font-[510] tracking-[-0.022em] text-balance md:text-[64px] md:leading-none">
          {headline}
        </h1>
        <p className="mt-6 max-w-xl text-[15px] leading-relaxed text-muted-foreground md:text-[17px]">{sentence}</p>
        <div className="mt-8">
          <CTA />
        </div>

        <Stage className="mt-16 w-full max-w-2xl text-left md:mt-20">
          <div className="flex min-h-[400px] flex-col gap-4 p-5 md:p-8">
            <Enter show>
              <div className="ml-auto max-w-[85%] rounded-2xl rounded-br-md bg-white/[0.06] px-4 py-2.5 text-[14px] text-secondary-text">
                Add a coupon column to orders. Test it before main.
              </div>
            </Enter>
            <div className="flex flex-col gap-2">
              {tools.map((t, i) => (
                <Enter key={t.name} show={step >= i + 1}>
                  <div className="flex items-center gap-3 rounded-lg border border-white/[0.06] bg-white/[0.02] px-3 py-2 text-[13px]">
                    <t.icon className="size-4 shrink-0 text-faint" />
                    <span className="font-mono text-foreground">{t.name}</span>
                    <span className="min-w-0 truncate font-mono text-muted-foreground">{t.detail}</span>
                    <CheckIcon className={cn("ml-auto size-4 shrink-0 text-[#27a644]", step > i + 1 || step === 4 ? "opacity-100" : "opacity-0", "transition-opacity duration-200")} />
                  </div>
                </Enter>
              ))}
            </div>
            <Enter show={step >= 4}>
              <div className="flex gap-3 text-[14px] leading-relaxed text-secondary-text">
                <SparklesIcon className="mt-1 size-4 shrink-0 text-faint" />
                <p>
                  Tested on <span className="font-mono text-foreground">pr-45</span>. The migration works and main is untouched. Run it on main?
                </p>
              </div>
            </Enter>
          </div>
        </Stage>
        <p className="mt-4 font-mono text-xs text-faint">Claude, Cursor or any MCP client</p>
      </main>
    </div>
  )
}

// Messages enter with opacity and a 2px blur, the way chat demos do.
function Enter({ show, children }: { show: boolean; children: React.ReactNode }) {
  return (
    <div
      className={cn(
        "transition-[opacity,filter,transform] duration-300 ease-out",
        show ? "translate-y-0 opacity-100 blur-0" : "pointer-events-none translate-y-1 opacity-0 blur-[2px]",
      )}
    >
      {children}
    </div>
  )
}
