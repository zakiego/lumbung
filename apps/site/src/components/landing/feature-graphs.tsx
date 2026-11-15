import { BotIcon, DatabaseIcon, FolderIcon, GitBranchIcon, type LucideIcon } from "lucide-react"
import type { CSSProperties, ReactNode } from "react"
import { cn } from "@/lib/utils"

// The small graphs above each feature. They render to plain SVG with no
// island: the picture is complete as drawn, and landing.css plays each one
// once when the page marks it seen (the fg-* classes).
const R = 20
const at = (seconds: number) => ({ "--at": `${seconds}s` }) as CSSProperties

function Graph({ children }: { children: ReactNode }) {
  return (
    <svg viewBox="0 0 360 132" className="fg notranslate block w-full max-w-[360px]" aria-hidden="true">
      {children}
    </svg>
  )
}

function Node({ x, y, icon: Icon, solid, className, style }: { x: number; y: number; icon: LucideIcon; solid?: boolean; className?: string; style?: CSSProperties }) {
  return (
    <g className={cn("origin-center [transform-box:fill-box]", className)} style={style}>
      <circle cx={x} cy={y} r={R} className={solid ? "fill-(--mark)" : "fill-background stroke-faint"} strokeWidth={1.5} />
      <Icon x={x - 9} y={y - 9} size={18} className={solid ? "text-background" : "text-secondary-text"} />
    </g>
  )
}

function Name({ x, y, anchor = "middle", className, style, children }: { x: number; y: number; anchor?: "start" | "middle"; className?: string; style?: CSSProperties; children: string }) {
  return (
    <text x={x} y={y} textAnchor={anchor} dominantBaseline="central" fontSize={12} className={cn("fill-muted-foreground font-mono", className)} style={style}>
      {children}
    </text>
  )
}

function Pulse({ d, delay = 0 }: { d: string; delay?: number }) {
  return <path d={d} pathLength={1} className="fg-comet fill-none stroke-foreground opacity-0 [stroke-dasharray:0.14_2]" style={at(delay)} strokeWidth={3} strokeLinecap="round" />
}

export function BranchTree() {
  const branch = "M52 40 C88 40 88 98 124 98 L176 98"
  return (
    <Graph>
      <line x1={12} x2={212} y1={40} y2={40} className="stroke-faint" strokeWidth={1.5} />
      <circle cx={52} cy={40} r={3.5} className="fill-background stroke-muted-foreground" strokeWidth={1.5} />
      <Node x={232} y={40} icon={DatabaseIcon} solid />
      <Name x={264} y={40} anchor="start">
        main
      </Name>

      <path d={branch} pathLength={1} className="fg-draw fill-none stroke-faint [stroke-dasharray:1]" strokeWidth={1.5} />
      <Node x={196} y={98} icon={GitBranchIcon} className="fg-pop" style={at(0.75)} />
      <Name x={228} y={98} anchor="start" className="fg-fade" style={at(0.85)}>
        branch
      </Name>
      <circle cx={146} cy={98} r={5} className="fg-pop origin-center fill-success stroke-background [transform-box:fill-box]" strokeWidth={2} style={at(1.35)} />
    </Graph>
  )
}

export function AgentBranch() {
  const link = "M64 52 L160 52"
  return (
    <Graph>
      <path d={link} className="fill-none stroke-faint" strokeWidth={1.5} />
      <Pulse d={link} />
      <line x1={200} x2={276} y1={52} y2={52} className="stroke-faint" strokeWidth={1.5} strokeDasharray="1 6" strokeLinecap="round" />

      <Node x={44} y={52} icon={BotIcon} />
      <Name x={44} y={90}>
        agent
      </Name>
      <Node x={180} y={52} icon={GitBranchIcon} />
      <circle cx={180} cy={52} r={R} className="fg-pop origin-center fill-none stroke-success [transform-box:fill-box]" strokeWidth={1.5} style={at(0.6)} />
      <Name x={180} y={90}>
        branch
      </Name>
      <Node x={296} y={52} icon={DatabaseIcon} solid />
      <Name x={296} y={90}>
        main
      </Name>
    </Graph>
  )
}

const clients = [
  { name: "libSQL", y: 28 },
  { name: "Drizzle", y: 92 },
]

export function Clients() {
  return (
    <Graph>
      {clients.map((client, i) => {
        const route = `M104 ${client.y} C182 ${client.y} 182 60 260 60`
        return (
          <g key={client.name}>
            <path d={route} className="fill-none stroke-faint" strokeWidth={1.5} />
            <Pulse d={route} delay={i * 0.3} />
            <rect x={8} y={client.y - 15} width={96} height={30} rx={8} className="fill-background stroke-faint" strokeWidth={1.5} />
            <Name x={56} y={client.y} className="fill-secondary-text">
              {client.name}
            </Name>
          </g>
        )
      })}
      <Node x={280} y={60} icon={GitBranchIcon} />
      <circle cx={280} cy={60} r={R} className="fg-pop origin-center fill-none stroke-success [transform-box:fill-box]" strokeWidth={1.5} style={at(0.9)} />
      <Name x={280} y={98}>
        branch
      </Name>
    </Graph>
  )
}

// When each file lands: the gaps shrink, so the three read as one move.
const files = [0.1, 0.24, 0.34]

export function Files() {
  return (
    <Graph>
      <line x1={64} x2={132} y1={52} y2={52} className="stroke-faint" strokeWidth={1.5} />
      <Node x={44} y={52} icon={DatabaseIcon} solid />
      <Name x={44} y={94}>
        database
      </Name>

      <rect x={132} y={27} width={196} height={50} rx={12} className="fill-none stroke-faint" strokeWidth={1.5} strokeDasharray="1 6" strokeLinecap="round" />
      <FolderIcon x={150} y={43} size={18} className="text-secondary-text" />
      {files.map((delay, i) => (
        <g key={delay} className="fg-pop origin-center [transform-box:fill-box]" style={at(delay)}>
          <rect x={188 + i * 42} y={35} width={28} height={34} rx={5} className={cn("stroke-faint", i === 2 ? "fill-(--mark) stroke-(--mark)" : "fill-background")} strokeWidth={1.5} />
          {[45, 52, 59].map((y, line) => (
            <line
              key={y}
              x1={195 + i * 42}
              x2={(line === 2 ? 203 : 209) + i * 42}
              y1={y}
              y2={y}
              className={i === 2 ? "stroke-background" : "stroke-faint"}
              strokeWidth={1.5}
              strokeLinecap="round"
            />
          ))}
        </g>
      ))}
      <Name x={230} y={94}>
        bucket
      </Name>
    </Graph>
  )
}
